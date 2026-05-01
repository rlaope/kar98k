package daemon

import (
	"context"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kar98k/internal/config"
	"github.com/kar98k/internal/health"
	"github.com/kar98k/internal/rpc"
	pb "github.com/kar98k/internal/rpc/proto"
	"github.com/kar98k/internal/targets"
	"github.com/kar98k/internal/worker"
	"github.com/kar98k/pkg/protocol"
)

const workerVersion = "v1"

// statsErrorBail is the consecutive StatsSender failure count that
// triggers WorkerDaemon shutdown. The supervisor (systemd / k8s) is
// expected to restart the process and re-register with the master.
const statsErrorBail = 10

// WorkerDaemon runs the worker side of a distributed kar session.
// It dials the master, registers, receives per-tick TPS updates via a
// server-stream, pushes histogram snapshots via a client-stream, and
// runs a local job-submission loop identical to controller.generateLoop.
//
// No controller, no scheduler, no scenarios, no dashboard.
type WorkerDaemon struct {
	masterAddr string
	workerAddr string

	cfg     config.Worker
	pool    *worker.Pool
	metrics *health.Metrics
	checker *health.Checker
	client  *rpc.WorkerClient

	// targets populated from RegisterResp
	targets []config.Target
	picker  *targets.Picker

	// clientByProto holds one protocol.Client per protocol, built ONCE
	// in Start. The hot path reads this map directly so keep-alive,
	// connection pools, and HTTP/2 multiplexing remain effective.
	// Why: the previous per-Submit allocation defeated MaxIdleConns and
	// caused P95/P99 to drift well above the single-process baseline.
	clientByProto map[config.Protocol]protocol.Client

	// consecutiveStatsErrors tracks unbroken StatsSender failures so
	// the worker can self-evict after statsErrorBail and let the
	// supervisor restart it with a fresh master connection.
	consecutiveStatsErrors int

	draining atomic.Bool

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewWorkerDaemon constructs a WorkerDaemon. Start must be called to
// connect to the master and begin traffic.
func NewWorkerDaemon(masterAddr, workerAddr string) *WorkerDaemon {
	ctx, cancel := context.WithCancel(context.Background())
	return &WorkerDaemon{
		masterAddr: masterAddr,
		workerAddr: workerAddr,
		ctx:        ctx,
		cancel:     cancel,
	}
}

// Start dials master, registers, and launches the three goroutines:
// A — rate-update receiver, B — stats pusher, C — job submission loop.
func (w *WorkerDaemon) Start() error {
	c, err := rpc.NewWorkerClient(w.masterAddr, w.workerAddr)
	if err != nil {
		return err
	}
	w.client = c

	if err := c.Register(w.ctx, workerVersion); err != nil {
		c.Close()
		return err
	}

	// Build targets from RegisterResp.
	w.targets = targetSpecsToConfig(c.Targets)
	w.picker = targets.New(w.targets)

	// Build pool from RegisterResp pool config (fall back to safe defaults).
	poolCfg := config.Worker{
		PoolSize:        int(c.Pool.GetPoolSize()),
		QueueSize:       int(c.Pool.GetQueueSize()),
		MaxIdleConns:    100,
		IdleConnTimeout: 90 * time.Second,
	}
	if poolCfg.PoolSize <= 0 {
		poolCfg.PoolSize = 100
	}
	if poolCfg.QueueSize <= 0 {
		poolCfg.QueueSize = 10_000
	}
	w.cfg = poolCfg

	w.metrics = health.NewMetrics()
	w.pool = worker.NewPool(w.cfg, w.metrics)
	w.pool.Start(w.ctx)

	// Why: build the three protocol clients exactly once so MaxIdleConns
	// and HTTP/2 connection reuse actually take effect. The previous
	// per-Submit allocation rebuilt transports and caused tail-latency
	// regressions versus the single-process baseline.
	clientCfg := protocol.ClientConfig{
		MaxIdleConns:    w.cfg.MaxIdleConns,
		IdleConnTimeout: w.cfg.IdleConnTimeout,
		TLSInsecure:     true,
	}
	w.clientByProto = map[config.Protocol]protocol.Client{
		config.ProtocolHTTP:  protocol.NewHTTPClient(clientCfg),
		config.ProtocolHTTP2: protocol.NewHTTP2Client(clientCfg),
		config.ProtocolGRPC:  protocol.NewGRPCClient(clientCfg),
	}

	// Health checker — each worker checks its own targets locally.
	healthCfg := config.Health{
		Enabled:  true,
		Interval: 10 * time.Second,
		Timeout:  5 * time.Second,
	}
	w.checker = health.NewChecker(healthCfg, w.targets, w.metrics)
	w.checker.Start(w.ctx)

	rateStream, err := c.OpenRateUpdates(w.ctx)
	if err != nil {
		w.pool.Stop()
		c.Close()
		return err
	}

	statsStream, err := c.OpenStats(w.ctx)
	if err != nil {
		w.pool.Stop()
		c.Close()
		return err
	}

	// Goroutine A: receive rate updates from master.
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		c.RunRateUpdates(w.ctx, rateStream, func(u *pb.RateUpdate) {
			switch u.Command {
			case pb.Command_DRAIN:
				log.Printf("[worker-daemon] DRAIN command received, stopping job submission")
				w.draining.Store(true)
			case pb.Command_STOP:
				log.Printf("[worker-daemon] STOP command received")
				w.draining.Store(true)
				w.cancel()
			default:
				if !w.draining.Load() {
					w.pool.SetRate(u.TargetTps)
				}
			}
		})
		// Stream ended — drain and exit.
		log.Printf("[worker-daemon] rate stream ended, draining")
		w.draining.Store(true)
		w.cancel()
	}()

	// Goroutine B: push histogram snapshots to master. Self-evicts after
	// statsErrorBail consecutive failures so the supervisor can restart
	// us with a fresh master connection.
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		c.StatsSender(w.ctx, statsStream, func() *pb.StatsPush {
			rawBytes, corrBytes, err := w.pool.SnapshotAndResetHistograms()
			if err != nil {
				w.consecutiveStatsErrors++
				log.Printf("[worker-daemon] snapshot error %d/%d: %v",
					w.consecutiveStatsErrors, statsErrorBail, err)
				if w.consecutiveStatsErrors >= statsErrorBail {
					log.Printf("[worker-daemon] %d consecutive stats failures — bailing for supervisor restart",
						w.consecutiveStatsErrors)
					w.cancel()
				}
				return nil
			}
			w.consecutiveStatsErrors = 0
			return &pb.StatsPush{
				WorkerId:     c.WorkerID,
				Timestamp:    uint64(time.Now().UnixMilli()),
				ObservedTps:  0, // pool measures internally; master uses hdr data
				QueueDrops:   w.pool.TotalDrops(),
				HdrRaw:       rawBytes,
				HdrCorrected: corrBytes,
				ErrorRate:    w.pool.ErrorRate(),
			}
		})
	}()

	// Goroutine C: local job submission loop (mirrors controller.generateLoop).
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		w.runJobLoop()
	}()

	log.Printf("[worker-daemon] started (master=%s worker=%s targets=%d)",
		w.masterAddr, w.workerAddr, len(w.targets))
	return nil
}

// Wait blocks until the worker daemon exits (context cancelled or drain complete).
func (w *WorkerDaemon) Wait() {
	w.wg.Wait()
}

// Stop signals a graceful shutdown and waits up to drainTimeout for
// in-flight requests to complete before tearing down the pool.
func (w *WorkerDaemon) Stop() {
	const drainTimeout = 10 * time.Second
	w.draining.Store(true)
	w.cancel()
	w.wg.Wait()
	w.pool.Drain(drainTimeout)
	w.pool.Stop()
	w.checker.Stop()
	w.client.Close()
	log.Printf("[worker-daemon] stopped")
}

// runJobLoop is Goroutine C: submits jobs to the local pool at the rate
// the pool's limiter allows. Mirrors controller.submitJobs — 1ms tick ×
// 10 attempts per tick; actual rate is controlled by pool.SetRate.
func (w *WorkerDaemon) runJobLoop() {
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
			if w.draining.Load() {
				return
			}
			for i := 0; i < 10; i++ {
				t := w.picker.Pick()
				if t == nil {
					continue
				}
				if w.checker != nil && !w.checker.IsHealthy(t.Name) {
					continue
				}
				client, ok := w.clientByProto[t.Protocol]
				if !ok {
					client = w.clientByProto[config.ProtocolHTTP]
				}
				job := worker.Job{
					Target: *t,
					Client: client,
				}
				if !w.pool.Submit(job) {
					break // queue full — back off for this tick
				}
			}
		}
	}
}

// targetSpecsToConfig converts proto TargetSpec messages received in
// RegisterResp into config.Target values the pool and health checker
// understand.
func targetSpecsToConfig(specs []*pb.TargetSpec) []config.Target {
	out := make([]config.Target, 0, len(specs))
	for _, s := range specs {
		if s == nil {
			continue
		}
		t := config.Target{
			Name:     s.Name,
			URL:      s.Url,
			Protocol: config.Protocol(s.Protocol),
			Method:   s.Method,
			Weight:   int(s.Weight),
			Timeout:  time.Duration(s.TimeoutMs) * time.Millisecond,
		}
		if t.Weight <= 0 {
			t.Weight = 1
		}
		if t.Method == "" {
			t.Method = "GET"
		}
		if t.Timeout <= 0 {
			t.Timeout = 30 * time.Second
		}
		out = append(out, t)
	}
	return out
}

