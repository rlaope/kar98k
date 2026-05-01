package rpc

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync/atomic"

	pb "github.com/kar98k/internal/rpc/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
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
	registry *WorkerRegistry
}

// NewMasterServer constructs a MasterServer backed by the given registry.
func NewMasterServer(registry *WorkerRegistry) *MasterServer {
	return &MasterServer{registry: registry}
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
		StatsIntervalMs: 2000,
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
// MasterServer backed by registry.
func NewGRPCServer(addr string, registry *WorkerRegistry) (*GRPCServer, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("grpc listen %s: %w", addr, err)
	}

	srv := grpc.NewServer()
	pb.RegisterKarMasterServer(srv, NewMasterServer(registry))

	return &GRPCServer{srv: srv, listener: ln, addr: addr}, nil
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
