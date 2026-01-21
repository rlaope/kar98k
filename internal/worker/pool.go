package worker

import (
	"context"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kar98k/internal/config"
	"github.com/kar98k/internal/health"
	"github.com/kar98k/pkg/protocol"
	"golang.org/x/time/rate"
)

// Job represents a single request job.
type Job struct {
	Target config.Target
	Client protocol.Client
}

// Pool manages a pool of worker goroutines.
type Pool struct {
	cfg      config.Worker
	metrics  *health.Metrics
	clients  map[config.Protocol]protocol.Client
	limiter  *rate.Limiter
	jobs     chan Job
	wg       sync.WaitGroup
	active   int64
	cancel   context.CancelFunc
	mu       sync.RWMutex
	tpsCount int64
	lastTPS  time.Time
}

// NewPool creates a new worker pool.
func NewPool(cfg config.Worker, metrics *health.Metrics) *Pool {
	// Initialize protocol clients
	clientCfg := protocol.ClientConfig{
		MaxIdleConns:    cfg.MaxIdleConns,
		IdleConnTimeout: cfg.IdleConnTimeout,
		TLSInsecure:     true,
	}

	clients := map[config.Protocol]protocol.Client{
		config.ProtocolHTTP:  protocol.NewHTTPClient(clientCfg),
		config.ProtocolHTTP2: protocol.NewHTTP2Client(clientCfg),
		config.ProtocolGRPC:  protocol.NewGRPCClient(clientCfg),
	}

	return &Pool{
		cfg:     cfg,
		metrics: metrics,
		clients: clients,
		limiter: rate.NewLimiter(rate.Limit(100), 1), // Initial rate, will be updated
		jobs:    make(chan Job, cfg.QueueSize),
		lastTPS: time.Now(),
	}
}

// Start launches the worker pool.
func (p *Pool) Start(ctx context.Context) {
	ctx, p.cancel = context.WithCancel(ctx)

	// Start worker goroutines
	for i := 0; i < p.cfg.PoolSize; i++ {
		p.wg.Add(1)
		go p.worker(ctx)
	}

	// Start TPS measurement goroutine
	go p.measureTPS(ctx)

	log.Printf("[worker] started %d workers with queue size %d", p.cfg.PoolSize, p.cfg.QueueSize)
}

// worker is the main worker goroutine.
func (p *Pool) worker(ctx context.Context) {
	defer p.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case job, ok := <-p.jobs:
			if !ok {
				return
			}
			p.processJob(ctx, job)
		}
	}
}

// processJob executes a single job.
func (p *Pool) processJob(ctx context.Context, job Job) {
	// Wait for rate limiter
	if err := p.limiter.Wait(ctx); err != nil {
		return // Context cancelled
	}

	atomic.AddInt64(&p.active, 1)
	p.metrics.IncRequestsInFlight()
	defer func() {
		atomic.AddInt64(&p.active, -1)
		p.metrics.DecRequestsInFlight()
	}()

	// Update active workers metric
	p.metrics.SetActiveWorkers(int(atomic.LoadInt64(&p.active)))

	// Build request
	req := &protocol.Request{
		URL:     job.Target.URL,
		Method:  job.Target.Method,
		Headers: job.Target.Headers,
		Body:    []byte(job.Target.Body),
		Timeout: job.Target.Timeout,
	}

	// Execute request
	resp := job.Client.Do(ctx, req)

	// Record metrics
	p.metrics.RecordRequest(
		job.Target.Name,
		string(job.Target.Protocol),
		resp.StatusCode,
		resp.Duration.Seconds(),
	)

	// Increment TPS counter
	atomic.AddInt64(&p.tpsCount, 1)
}

// measureTPS periodically calculates and updates the actual TPS.
func (p *Pool) measureTPS(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			count := atomic.SwapInt64(&p.tpsCount, 0)
			p.metrics.SetCurrentTPS(float64(count))
		}
	}
}

// Submit adds a job to the queue.
func (p *Pool) Submit(job Job) bool {
	select {
	case p.jobs <- job:
		p.metrics.SetQueuedRequests(len(p.jobs))
		return true
	default:
		// Queue full, drop the job
		return false
	}
}

// SetRate updates the rate limiter.
func (p *Pool) SetRate(tps float64) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.limiter.SetLimit(rate.Limit(tps))
	p.limiter.SetBurst(int(tps / 10)) // Burst of 10% of TPS
	if p.limiter.Burst() < 1 {
		p.limiter.SetBurst(1)
	}

	p.metrics.SetTargetTPS(tps)
}

// GetClient returns the client for a given protocol.
func (p *Pool) GetClient(proto config.Protocol) protocol.Client {
	client, ok := p.clients[proto]
	if !ok {
		return p.clients[config.ProtocolHTTP]
	}
	return client
}

// Active returns the number of currently active workers.
func (p *Pool) Active() int {
	return int(atomic.LoadInt64(&p.active))
}

// QueueSize returns the current queue length.
func (p *Pool) QueueSize() int {
	return len(p.jobs)
}

// Stop gracefully stops the worker pool.
func (p *Pool) Stop() {
	if p.cancel != nil {
		p.cancel()
	}

	close(p.jobs)
	p.wg.Wait()

	// Close all clients
	for _, client := range p.clients {
		client.Close()
	}

	log.Printf("[worker] all workers stopped")
}

// Drain waits for all in-flight requests to complete with a timeout.
func (p *Pool) Drain(timeout time.Duration) {
	deadline := time.Now().Add(timeout)

	for atomic.LoadInt64(&p.active) > 0 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}

	remaining := atomic.LoadInt64(&p.active)
	if remaining > 0 {
		log.Printf("[worker] drain timeout with %d requests still in-flight", remaining)
	}
}
