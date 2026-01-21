package health

import (
	"context"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/kar98k/internal/config"
	"github.com/kar98k/pkg/protocol"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Checker performs periodic health checks on targets.
type Checker struct {
	cfg      config.Health
	targets  []config.Target
	metrics  *Metrics
	clients  map[config.Protocol]protocol.Client
	statuses map[string]bool
	mu       sync.RWMutex
	cancel   context.CancelFunc
}

// NewChecker creates a new health checker.
func NewChecker(cfg config.Health, targets []config.Target, metrics *Metrics) *Checker {
	return &Checker{
		cfg:      cfg,
		targets:  targets,
		metrics:  metrics,
		clients:  make(map[config.Protocol]protocol.Client),
		statuses: make(map[string]bool),
	}
}

// Start begins periodic health checking.
func (c *Checker) Start(ctx context.Context) {
	if !c.cfg.Enabled {
		return
	}

	ctx, c.cancel = context.WithCancel(ctx)

	// Initialize clients
	clientCfg := protocol.ClientConfig{
		MaxIdleConns:    10,
		IdleConnTimeout: 30 * time.Second,
		TLSInsecure:     true,
	}

	c.clients[config.ProtocolHTTP] = protocol.NewHTTPClient(clientCfg)
	c.clients[config.ProtocolHTTP2] = protocol.NewHTTP2Client(clientCfg)
	c.clients[config.ProtocolGRPC] = protocol.NewGRPCClient(clientCfg)

	// Initialize all targets as healthy
	for _, t := range c.targets {
		c.statuses[t.Name] = true
		c.metrics.SetTargetHealth(t.Name, true)
	}

	go c.run(ctx)
}

// run is the main health check loop.
func (c *Checker) run(ctx context.Context) {
	ticker := time.NewTicker(c.cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.checkAll(ctx)
		}
	}
}

// checkAll performs health checks on all targets.
func (c *Checker) checkAll(ctx context.Context) {
	var wg sync.WaitGroup

	for _, target := range c.targets {
		wg.Add(1)
		go func(t config.Target) {
			defer wg.Done()
			c.checkTarget(ctx, t)
		}(target)
	}

	wg.Wait()
}

// checkTarget performs a health check on a single target.
func (c *Checker) checkTarget(ctx context.Context, target config.Target) {
	client, ok := c.clients[target.Protocol]
	if !ok {
		client = c.clients[config.ProtocolHTTP]
	}

	req := &protocol.Request{
		URL:     target.URL,
		Method:  "GET", // Health checks always use GET
		Headers: target.Headers,
		Timeout: c.cfg.Timeout,
	}

	checkCtx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()

	resp := client.Do(checkCtx, req)

	healthy := resp.Error == nil && resp.StatusCode >= 200 && resp.StatusCode < 400

	c.mu.Lock()
	prevStatus := c.statuses[target.Name]
	c.statuses[target.Name] = healthy
	c.mu.Unlock()

	c.metrics.SetTargetHealth(target.Name, healthy)

	// Log status changes
	if prevStatus != healthy {
		if healthy {
			log.Printf("[health] target %s is now healthy", target.Name)
		} else {
			log.Printf("[health] target %s is now unhealthy: %v", target.Name, resp.Error)
		}
	}
}

// IsHealthy returns whether a target is currently healthy.
func (c *Checker) IsHealthy(targetName string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.statuses[targetName]
}

// GetHealthyTargets returns a slice of healthy targets.
func (c *Checker) GetHealthyTargets() []config.Target {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var healthy []config.Target
	for _, t := range c.targets {
		if c.statuses[t.Name] {
			healthy = append(healthy, t)
		}
	}
	return healthy
}

// Stop stops the health checker.
func (c *Checker) Stop() {
	if c.cancel != nil {
		c.cancel()
	}

	for _, client := range c.clients {
		client.Close()
	}
}

// Server serves Prometheus metrics and health endpoints.
type Server struct {
	server *http.Server
}

// NewServer creates a new metrics/health HTTP server.
func NewServer(cfg config.Metrics) *Server {
	mux := http.NewServeMux()

	// Prometheus metrics endpoint
	mux.Handle(cfg.Path, promhttp.Handler())

	// Liveness probe
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	// Readiness probe
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	return &Server{
		server: &http.Server{
			Addr:    cfg.Address,
			Handler: mux,
		},
	}
}

// Start begins serving metrics.
func (s *Server) Start() error {
	log.Printf("[metrics] starting server on %s", s.server.Addr)
	return s.server.ListenAndServe()
}

// Stop gracefully stops the server.
func (s *Server) Stop(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}
