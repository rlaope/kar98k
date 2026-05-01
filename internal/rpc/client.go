package rpc

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"time"

	pb "github.com/kar98k/internal/rpc/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// ClientOptions configures optional TLS and auth for NewWorkerClient.
// Zero value preserves the current plaintext/no-auth default.
// BackoffMax and MaxAttempts are consumed by the WorkerDaemon reconnect
// loop (#69-T7) — NewWorkerClient ignores them.
type ClientOptions struct {
	TLSConfig          *tls.Config   // nil = plaintext
	AuthToken          string        // empty = no Authorization header
	BackoffMax         time.Duration // 0 → reconnect loop defaults to 30s
	MaxAttempts        int           // 0 = unlimited reconnect attempts
	AllowInsecureToken bool          // allow AuthToken over plaintext (not recommended)
}

// WorkerClient manages the gRPC connection from a worker to the master.
type WorkerClient struct {
	conn       *grpc.ClientConn
	client     pb.KarMasterClient
	masterAddr string
	workerAddr string

	WorkerID        string
	Targets         []*pb.TargetSpec
	Pool            *pb.WorkerPoolConfig
	StatsIntervalMs uint32
}

// NewWorkerClient dials the master and returns a connected client.
// Pass ClientOptions{} (zero value) to preserve current plaintext behavior.
// Returns an error if AuthToken is set without TLS and AllowInsecureToken is false
// (sending a bearer token over plaintext exposes it to network observers).
func NewWorkerClient(masterAddr, workerAddr string, opts ClientOptions) (*WorkerClient, error) {
	if opts.AuthToken != "" && opts.TLSConfig == nil && !opts.AllowInsecureToken {
		return nil, fmt.Errorf("auth token requires TLS; set --tls-ca or pass AllowInsecureToken to override")
	}

	dialOpts := []grpc.DialOption{transportCredential(opts.TLSConfig)}
	if opts.AuthToken != "" {
		dialOpts = append(dialOpts, grpc.WithPerRPCCredentials(bearerToken{
			tok:           opts.AuthToken,
			allowInsecure: opts.AllowInsecureToken,
		}))
	}

	conn, err := grpc.NewClient(masterAddr, dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("dial master %s: %w", masterAddr, err)
	}

	return &WorkerClient{
		conn:       conn,
		client:     pb.NewKarMasterClient(conn),
		masterAddr: masterAddr,
		workerAddr: workerAddr,
	}, nil
}

// transportCredential returns the appropriate gRPC transport option.
func transportCredential(cfg *tls.Config) grpc.DialOption {
	if cfg != nil {
		return grpc.WithTransportCredentials(credentials.NewTLS(cfg))
	}
	return grpc.WithTransportCredentials(insecure.NewCredentials())
}

// bearerToken implements grpc.PerRPCCredentials, attaching an Authorization
// header to every RPC call. RequireTransportSecurity returns true by default
// to prevent accidental token leakage over plaintext; set allowInsecure only
// when explicitly opted in via ClientOptions.AllowInsecureToken.
type bearerToken struct {
	tok           string
	allowInsecure bool
}

func (t bearerToken) GetRequestMetadata(_ context.Context, _ ...string) (map[string]string, error) {
	return map[string]string{
		headerAuthorization: bearerPrefix + t.tok,
	}, nil
}

func (t bearerToken) RequireTransportSecurity() bool { return !t.allowInsecure }

// Register calls the master Register RPC and stores the assigned ID + config.
func (c *WorkerClient) Register(ctx context.Context, version string) error {
	resp, err := c.client.Register(ctx, &pb.RegisterReq{
		WorkerAddr: c.workerAddr,
		Version:    version,
		Bounds:     DefaultHistogramBounds(),
	})
	if err != nil {
		return fmt.Errorf("Register RPC: %w", err)
	}

	c.WorkerID = resp.WorkerId
	c.Targets = resp.Targets
	c.Pool = resp.Pool
	c.StatsIntervalMs = resp.StatsIntervalMs
	if c.StatsIntervalMs == 0 {
		c.StatsIntervalMs = 2000
	}

	log.Printf("[worker-client] registered as %s (targets=%d stats_interval=%dms)",
		c.WorkerID, len(c.Targets), c.StatsIntervalMs)
	return nil
}

// OpenRateUpdates opens the server-streaming RateUpdates call and returns the
// stream. The caller is responsible for reading from it in a goroutine.
func (c *WorkerClient) OpenRateUpdates(ctx context.Context) (pb.KarMaster_RateUpdatesClient, error) {
	stream, err := c.client.RateUpdates(ctx, &pb.RateSubscribeReq{WorkerId: c.WorkerID})
	if err != nil {
		return nil, fmt.Errorf("RateUpdates RPC: %w", err)
	}
	return stream, nil
}

// OpenStats opens the client-streaming Stats call and returns the stream.
// The caller is responsible for sending on it and calling CloseAndRecv.
func (c *WorkerClient) OpenStats(ctx context.Context) (pb.KarMaster_StatsClient, error) {
	stream, err := c.client.Stats(ctx)
	if err != nil {
		return nil, fmt.Errorf("Stats RPC: %w", err)
	}
	return stream, nil
}

// RunRateUpdates reads the server-stream of RateUpdates, calling onRate for
// each received update. It blocks until ctx is cancelled or the stream ends.
func (c *WorkerClient) RunRateUpdates(ctx context.Context, stream pb.KarMaster_RateUpdatesClient, onRate func(*pb.RateUpdate)) {
	for {
		update, err := stream.Recv()
		if err != nil {
			select {
			case <-ctx.Done():
			default:
				log.Printf("[worker-client] RateUpdates stream ended: %v", err)
			}
			return
		}
		onRate(update)
	}
}

// StatsSender periodically calls snapshot() to build a StatsPush and sends it
// on stream. It blocks until ctx is cancelled.
func (c *WorkerClient) StatsSender(ctx context.Context, stream pb.KarMaster_StatsClient, snapshot func() *pb.StatsPush) {
	interval := time.Duration(c.StatsIntervalMs) * time.Millisecond
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			_, _ = stream.CloseAndRecv()
			return
		case <-ticker.C:
			push := snapshot()
			if push == nil {
				continue
			}
			if err := stream.Send(push); err != nil {
				log.Printf("[worker-client] Stats send error: %v", err)
				return
			}
		}
	}
}

// Close shuts down the gRPC connection.
func (c *WorkerClient) Close() {
	if c.conn != nil {
		c.conn.Close()
	}
}
