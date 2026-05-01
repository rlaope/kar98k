package rpc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"net"
	"os"
	"sync/atomic"

	"github.com/kar98k/internal/config"
	pb "github.com/kar98k/internal/rpc/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

// workerIDCounter generates monotonically increasing worker IDs.
var workerIDCounter uint64

func nextWorkerID() string {
	n := atomic.AddUint64(&workerIDCounter, 1)
	return fmt.Sprintf("worker-%d", n)
}

// MasterServer implements the KarMasterServer gRPC interface.
type MasterServer struct {
	pb.UnimplementedKarMasterServer
	registry        *WorkerRegistry
	statsIntervalMs int
}

// grpcServerConfig accumulates gRPC server-level options (TLS, auth) built
// by WithTLS and WithAuthToken before NewGRPCServer calls grpc.NewServer.
type grpcServerConfig struct {
	tlsCreds  credentials.TransportCredentials
	authToken string
}

// ServerOption configures a MasterServer.
type ServerOption func(*MasterServer)

// GRPCServerOption configures the underlying grpc.Server (TLS, auth).
type GRPCServerOption func(*grpcServerConfig)

// WithStatsIntervalMs overrides the stats interval sent to workers on Register.
// Default is 2000ms. Tests pass a smaller value (e.g. 100ms) to speed up sampling.
func WithStatsIntervalMs(ms int) ServerOption {
	return func(s *MasterServer) {
		s.statsIntervalMs = ms
	}
}

// WithTLS loads cert+key from tlsCfg and configures server-side TLS (or mTLS
// when ClientCA is set). Returns an error if any file is unreadable.
func WithTLS(tlsCfg *config.TLSConfig) (GRPCServerOption, error) {
	if tlsCfg == nil {
		return func(*grpcServerConfig) {}, nil
	}
	cert, err := tls.LoadX509KeyPair(tlsCfg.Cert, tlsCfg.Key)
	if err != nil {
		return nil, fmt.Errorf("load TLS cert/key: %w", err)
	}
	tc := &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	if tlsCfg.ClientCA != "" {
		caPEM, err := os.ReadFile(tlsCfg.ClientCA)
		if err != nil {
			return nil, fmt.Errorf("read client CA %s: %w", tlsCfg.ClientCA, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("parse client CA %s: no valid PEM block", tlsCfg.ClientCA)
		}
		tc.ClientCAs = pool
		tc.ClientAuth = tls.RequireAndVerifyClientCert
	}
	fp, err := certFingerprint(tlsCfg.Cert)
	if err != nil {
		log.Printf("[grpc] cert fingerprint unavailable: %v", err)
	} else {
		log.Printf("[grpc] TLS cert SHA-256: %s", fp)
	}
	creds := credentials.NewTLS(tc)
	return func(c *grpcServerConfig) { c.tlsCreds = creds }, nil
}

// WithAuthToken configures bearer-token auth interceptors on the gRPC server.
// When token is empty this is a no-op (plaintext default preserved).
func WithAuthToken(token string) GRPCServerOption {
	return func(c *grpcServerConfig) { c.authToken = token }
}

// GRPCServerOptionFromTLSConfig wraps a pre-built *tls.Config as a
// GRPCServerOption. Intended for tests that construct tls.Config directly
// rather than via file paths (which WithTLS requires).
func GRPCServerOptionFromTLSConfig(tc *tls.Config) GRPCServerOption {
	return func(c *grpcServerConfig) {
		if tc != nil {
			c.tlsCreds = credentials.NewTLS(tc)
		}
	}
}

// NewMasterServer constructs a MasterServer backed by the given registry.
// Callers that pass no opts get the 2000ms production default.
func NewMasterServer(registry *WorkerRegistry, opts ...ServerOption) *MasterServer {
	s := &MasterServer{
		registry:        registry,
		statsIntervalMs: 2000,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Register handles a worker joining the cluster.
func (s *MasterServer) Register(ctx context.Context, req *pb.RegisterReq) (*pb.RegisterResp, error) {
	if err := ValidateBounds(req.Bounds); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "bounds validation failed: %v", err)
	}

	id := nextWorkerID()
	s.registry.Register(id, req.WorkerAddr)
	log.Printf("[master] Register: id=%s addr=%s version=%s", id, req.WorkerAddr, req.Version)

	return &pb.RegisterResp{
		WorkerId:        id,
		StatsIntervalMs: uint32(s.statsIntervalMs),
	}, nil
}

// RateUpdates streams rate updates to a registered worker.
func (s *MasterServer) RateUpdates(req *pb.RateSubscribeReq, stream pb.KarMaster_RateUpdatesServer) error {
	sendCh, done, ok := s.registry.GetSendCh(req.WorkerId)
	if !ok {
		return status.Errorf(codes.NotFound, "worker %s not registered", req.WorkerId)
	}

	ctx := stream.Context()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-done:
			// Why: done is closed when the worker is evicted/unregistered; sendCh
			// is never closed, so we must use this signal to end the stream.
			return nil
		case update := <-sendCh:
			if err := stream.Send(update); err != nil {
				return err
			}
		}
	}
}

// Stats receives a client-side stream of StatsPush messages from a worker.
func (s *MasterServer) Stats(stream pb.KarMaster_StatsServer) error {
	for {
		push, err := stream.Recv()
		if err != nil {
			// Worker closed the stream — acknowledge and finish.
			_ = stream.SendAndClose(&pb.StatsAck{Ok: true})
			return nil
		}
		s.registry.RecordStats(push)
	}
}

// GRPCServer wraps a grpc.Server and its listener for lifecycle management.
type GRPCServer struct {
	srv      *grpc.Server
	listener net.Listener
	addr     string
}

// NewGRPCServer creates and binds a gRPC server on addr, registering the
// MasterServer backed by registry. Pass GRPCServerOption values (WithTLS,
// WithAuthToken) to enable TLS and bearer-token auth; zero options preserves
// the current plaintext/no-auth default.
func NewGRPCServer(addr string, registry *WorkerRegistry, gopts ...GRPCServerOption) (*GRPCServer, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("grpc listen %s: %w", addr, err)
	}

	gcfg := &grpcServerConfig{}
	for _, o := range gopts {
		o(gcfg)
	}

	var srvOpts []grpc.ServerOption
	if gcfg.tlsCreds != nil {
		srvOpts = append(srvOpts, grpc.Creds(gcfg.tlsCreds))
	}
	if gcfg.authToken != "" {
		srvOpts = append(srvOpts,
			grpc.UnaryInterceptor(UnaryAuthInterceptor(gcfg.authToken)),
			grpc.StreamInterceptor(StreamAuthInterceptor(gcfg.authToken)),
		)
	}

	srv := grpc.NewServer(srvOpts...)
	pb.RegisterKarMasterServer(srv, NewMasterServer(registry))

	return &GRPCServer{srv: srv, listener: ln, addr: addr}, nil
}

// Addr returns the address the server is listening on. Useful in tests that
// pass "127.0.0.1:0" to get an OS-assigned port.
func (g *GRPCServer) Addr() string {
	return g.listener.Addr().String()
}

// Serve begins accepting connections. Blocks until Stop is called.
func (g *GRPCServer) Serve() error {
	log.Printf("[grpc] serving on %s", g.addr)
	return g.srv.Serve(g.listener)
}

// Stop gracefully shuts down the gRPC server.
func (g *GRPCServer) Stop() {
	log.Printf("[grpc] stopping server")
	g.srv.GracefulStop()
}
