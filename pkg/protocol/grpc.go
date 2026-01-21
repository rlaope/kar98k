package protocol

import (
	"context"
	"crypto/tls"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"
)

// GRPCClient implements Client for gRPC.
type GRPCClient struct {
	conns map[string]*grpc.ClientConn
	cfg   ClientConfig
}

// NewGRPCClient creates a new gRPC client.
func NewGRPCClient(cfg ClientConfig) *GRPCClient {
	return &GRPCClient{
		conns: make(map[string]*grpc.ClientConn),
		cfg:   cfg,
	}
}

// getConn returns a cached connection or creates a new one.
func (c *GRPCClient) getConn(target string) (*grpc.ClientConn, error) {
	if conn, ok := c.conns[target]; ok {
		return conn, nil
	}

	opts := []grpc.DialOption{
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                10 * time.Second,
			Timeout:             5 * time.Second,
			PermitWithoutStream: true,
		}),
	}

	if c.cfg.TLSInsecure {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	} else {
		opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
			InsecureSkipVerify: c.cfg.TLSInsecure,
		})))
	}

	conn, err := grpc.NewClient(target, opts...)
	if err != nil {
		return nil, err
	}

	c.conns[target] = conn
	return conn, nil
}

// Do executes a gRPC health check request.
// For simplicity, we use the standard gRPC health check protocol.
func (c *GRPCClient) Do(ctx context.Context, req *Request) *Response {
	start := time.Now()
	resp := &Response{}

	conn, err := c.getConn(req.URL)
	if err != nil {
		resp.Error = err
		resp.Duration = time.Since(start)
		return resp
	}

	if req.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}

	client := grpc_health_v1.NewHealthClient(conn)
	healthResp, err := client.Check(ctx, &grpc_health_v1.HealthCheckRequest{
		Service: "", // empty string means overall server health
	})

	resp.Duration = time.Since(start)

	if err != nil {
		resp.Error = err
		if s, ok := status.FromError(err); ok {
			resp.StatusCode = int(s.Code())
		}
		return resp
	}

	if healthResp.Status == grpc_health_v1.HealthCheckResponse_SERVING {
		resp.StatusCode = int(codes.OK)
	} else {
		resp.StatusCode = int(codes.Unavailable)
	}

	return resp
}

// Close releases all connections.
func (c *GRPCClient) Close() error {
	for _, conn := range c.conns {
		conn.Close()
	}
	c.conns = make(map[string]*grpc.ClientConn)
	return nil
}
