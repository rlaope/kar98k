package daemon

import (
	"context"
	"fmt"
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
//
// Multi-master support (#72): masterAddrs holds a list of master
// endpoints. The reconnect loop cycles through them in order on each
// new dial attempt so a worker can survive primary→standby failover
// without operator intervention. Single-master deployments pass a
// 1-element list.
type WorkerDaemon struct {
	masterAddrs []string
	addrIdx     int // round-robin cursor into masterAddrs (used only by Run goroutine)
	workerAddr  string
	clientOpts  rpc.ClientOptions

	// mu guards ctx, cancel, and the per-cycle resources (pool, checker,
	// client). Stop() snapshots these under mu so it never races with the
	// writes in Start() or the resets in Run().
	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	pool    *worker.Pool
	checker *health.Checker
	client  *rpc.WorkerClient

	// Fields written only in Start() before goroutines launch — no concurrent
	// writer, so no lock needed for these after Start() returns.
	cfg           config.Worker
	targets       []config.Target
	picker        *targets.Picker
	clientByProto map[config.Protocol]protocol.Client
	metrics       *health.Metrics

	// consecutiveStatsErrors is only read/written by goroutine B (stats pusher)
	// and reset in Run() after Wait() — single goroutine access at each point.
	consecutiveStatsErrors int

	draining atomic.Bool
	// stopped is set by Stop() before cancelling so Run() will not install a
	// fresh context for a new dial cycle after Stop() fires.
	stopped atomic.Bool

	wg sync.WaitGroup
}

// NewWorkerDaemon constructs a WorkerDaemon for a single master endpoint.
// Use NewWorkerDaemonMulti to dial primary+standby for HA. Both forms
// share the same plaintext/no-auth default — pass rpc.ClientOptions{}
// to keep the existing behaviour.
func NewWorkerDaemon(masterAddr, workerAddr string, opts rpc.ClientOptions) *WorkerDaemon {
	return NewWorkerDaemonMulti([]string{masterAddr}, workerAddr, opts)
}

// NewWorkerDaemonMulti is the multi-master constructor: the reconnect
// loop cycles through masterAddrs in order on each new dial attempt
// so a worker survives primary→standby failover without operator
// intervention. The list MUST be non-empty.
func NewWorkerDaemonMulti(masterAddrs []string, workerAddr string, opts rpc.ClientOptions) *WorkerDaemon {
	ctx, cancel := context.WithCancel(context.Background())
	return &WorkerDaemon{
		masterAddrs: masterAddrs,
		workerAddr:  workerAddr,
		clientOpts:  opts,
		ctx:         ctx,
		cancel:      cancel,
	}
}

// nextMasterAddr returns the next address in the round-robin sequence.
// Single-master deployments (len == 1) always return that one address.
// Called only from the Run goroutine, so addrIdx needs no lock.
func (w *WorkerDaemon) nextMasterAddr() string {
	addr := w.masterAddrs[w.addrIdx%len(w.masterAddrs)]
	w.addrIdx++
	return addr
}

// cancelCtx calls the current cancel function under mu.
func (w *WorkerDaemon) cancelCtx() {
	w.mu.Lock()
	c := w.cancel
	w.mu.Unlock()
	c()
}

// currentCtx returns the current context under mu.
func (w *WorkerDaemon) currentCtx() context.Context {
	w.mu.Lock()
	ctx := w.ctx
	w.mu.Unlock()
	return ctx
}

// swapCtx installs a new context+cancel pair and returns the new ctx.
// Must only be called from Run() between reconnect cycles after all goroutines
// from the previous cycle have exited (i.e., after Wait()).
func (w *WorkerDaemon) swapCtx() context.Context {
	newCtx, newCancel := context.WithCancel(context.Background())
	w.mu.Lock()
	w.ctx = newCtx
	w.cancel = newCancel
	w.mu.Unlock()
	return newCtx
}

// setCycleResources stores the per-cycle resources under mu. Called at the
// end of a successful Start() once all fields are populated.
func (w *WorkerDaemon) setCycleResources(pool *worker.Pool, checker *health.Checker, client *rpc.WorkerClient) {
	w.mu.Lock()
	w.pool = pool
	w.checker = checker
	w.client = client
	w.mu.Unlock()
}

// clearCycleResources zeroes the per-cycle resources under mu and returns the
// previous values so the caller can tear them down outside the lock.
func (w *WorkerDaemon) clearCycleResources() (pool *worker.Pool, checker *health.Checker, client *rpc.WorkerClient) {
	w.mu.Lock()
	pool = w.pool
	checker = w.checker
	client = w.client
	w.pool = nil
	w.checker = nil
	w.client = nil
	w.mu.Unlock()
	return
}

// backoffDuration returns 2^(n-1) seconds capped at max.
// n=1->1s, n=2->2s, n=3->4s, n=4->8s, n>=5->cap.
func backoffDuration(n int, max time.Duration) time.Duration {
	const base = time.Second
	d := base
	for i := 1; i < n; i++ {
		d *= 2
		if d >= max {
			return max
		}
	}
	if d > max {
		return max
	}
	return d
}

// teardownCycle drains and stops pool/checker/client obtained via clearCycleResources.
func teardownCycle(pool *worker.Pool, checker *health.Checker, client *rpc.WorkerClient) {
	const drainTimeout = 10 * time.Second
	if pool != nil {
		pool.Drain(drainTimeout)
		pool.Stop()
	}
	if checker != nil {
		checker.Stop()
	}
	if client != nil {
		client.Close()
	}
}

// Run connects to the master with automatic exponential-backoff reconnect.
// It blocks until ctx is cancelled or opts.MaxAttempts consecutive dial
// failures are exhausted -- in the latter case it returns a non-nil error
// so a process supervisor (k8s/systemd) can apply CrashLoopBackoff.
func (w *WorkerDaemon) Run() error {
	maxBackoff := w.clientOpts.BackoffMax
	if maxBackoff <= 0 {
		maxBackoff = 30 * time.Second
	}
	maxAttempts := w.clientOpts.MaxAttempts // 0 = unlimited

	consecutiveFails := 0
	attempt := 0

	for {
		if w.stopped.Load() {
			return nil
		}
		select {
		case <-w.currentCtx().Done():
			return nil
		default:
		}

		attempt++
		dialAddr := w.nextMasterAddr()
		log.Printf("[worker-daemon] connect attempt %d (master=%s)", attempt, dialAddr)

		err := w.Start(dialAddr)
		if err != nil {
			consecutiveFails++
			log.Printf("[worker-daemon] connect failed (attempt %d, consecutive=%d): %v",
				attempt, consecutiveFails, err)

			if maxAttempts > 0 && consecutiveFails >= maxAttempts {
				log.Printf("[worker-daemon] exceeded max reconnect attempts (%d), exiting", maxAttempts)
				return fmt.Errorf("exceeded max reconnect attempts (%d): %w", maxAttempts, err)
			}

			sleep := backoffDuration(consecutiveFails, maxBackoff)
			log.Printf("[worker-daemon] reconnect backoff %s (attempt %d)", sleep, attempt)
			select {
			case <-w.currentCtx().Done():
				return nil
			case <-time.After(sleep):
			}
			continue
		}

		// Start succeeded -- wait for all supervision goroutines to finish.
		w.Wait()

		// If stopped or context was cancelled, exit cleanly.
		if w.stopped.Load() {
			return nil
		}
		select {
		case <-w.currentCtx().Done():
			return nil
		default:
		}

		// Stream ended unexpectedly -- tear down this cycle then reconnect.
		pool, checker, client := w.clearCycleResources()
		teardownCycle(pool, checker, client)

		// Check stopped again before installing a new context so Stop() cannot
		// miss the new cancel if it fires between clearCycleResources and swapCtx.
		if w.stopped.Load() {
			return nil
		}
		consecutiveFails = 0
		w.consecutiveStatsErrors = 0
		w.draining.Store(false)
		newCtx := w.swapCtx()

		sleep := backoffDuration(1, maxBackoff)
		log.Printf("[worker-daemon] stream ended -- reconnecting in %s", sleep)
		select {
		case <-newCtx.Done():
			return nil
		case <-time.After(sleep):
		}
	}
}

// Start dials masterAddr, registers, and launches the three goroutines:
// A -- rate-update receiver, B -- stats pusher, C -- job submission loop.
//
// In multi-master deployments (#72) the caller (Run) selects the
// address per-attempt via nextMasterAddr so reconnect cycles between
// primary and standby. Tests / single-master callers can pass any of
// the configured masterAddrs.
func (w *WorkerDaemon) Start(masterAddr string) error {
	ctx := w.currentCtx()
	c, err := rpc.NewWorkerClient(masterAddr, w.workerAddr, w.clientOpts)
	if err != nil {
		return err
	}

	if err := c.Register(ctx, workerVersion); err != nil {
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
	pool := worker.NewPool(w.cfg, w.metrics)
	pool.Start(ctx)

	// Why: build the three protocol clients exactly once so MaxIdleConns
	// and HTTP/2 connection reuse actually take effect.
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

	// Health checker -- each worker checks its own targets locally.
	healthCfg := config.Health{
		Enabled:  true,
		Interval: 10 * time.Second,
		Timeout:  5 * time.Second,
	}
	checker := health.NewChecker(healthCfg, w.targets, w.metrics)
	checker.Start(ctx)

	rateStream, err := c.OpenRateUpdates(ctx)
	if err != nil {
		pool.Stop()
		checker.Stop()
		c.Close()
		return err
	}

	statsStream, err := c.OpenStats(ctx)
	if err != nil {
		pool.Stop()
		checker.Stop()
		c.Close()
		return err
	}

	// Publish resources under mu so Stop() can safely snapshot them.
	w.setCycleResources(pool, checker, c)

	// outOfBandStats carries final per-phase StatsPush messages produced by
	// the rate-update goroutine when a phase transition is observed. The
	// stats-sender goroutine drains it before each interval push so the
	// master sees a clean per-phase boundary. Buffered so a slow stats
	// sender never blocks the rate-update receiver. See #68.
	outOfBandStats := make(chan *pb.StatsPush, 8)

	// Goroutine A: receive rate updates from master.
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		c.RunRateUpdates(ctx, rateStream, func(u *pb.RateUpdate) {
			switch u.Command {
			case pb.Command_DRAIN:
				log.Printf("[worker-daemon] DRAIN command received, stopping job submission")
				w.draining.Store(true)
			case pb.Command_STOP:
				log.Printf("[worker-daemon] STOP command received")
				w.draining.Store(true)
				w.cancelCtx()
			default:
				if w.draining.Load() {
					return
				}
				// Phase transition: snapshot + flip atomically, ship the
				// previous phase's histograms out-of-band tagged with prevPhase
				// so master attributes them correctly. Empty PhaseName is
				// treated as "still in default phase" — no flip triggered when
				// already at "" (#68 v1<->v2 backwards compat).
				if u.PhaseName != pool.CurrentPhase() {
					rawBytes, corrBytes, prevPhase, err := pool.SnapshotAndAdvancePhase(u.PhaseName)
					if err != nil {
						log.Printf("[worker-daemon] phase-flip snapshot error: %v", err)
					} else {
						push := &pb.StatsPush{
							WorkerId:     c.WorkerID,
							Timestamp:    uint64(time.Now().UnixMilli()),
							ObservedTps:  0,
							QueueDrops:   pool.TotalDrops(),
							HdrRaw:       rawBytes,
							HdrCorrected: corrBytes,
							ErrorRate:    pool.ErrorRate(),
							PhaseName:    prevPhase,
						}
						select {
						case outOfBandStats <- push:
						default:
							log.Printf("[worker-daemon] out-of-band stats buffer full, dropping phase-flip snapshot for %q", prevPhase)
						}
					}
				}
				pool.SetRate(u.TargetTps)
			}
		})
		// Stream ended -- signal drain and exit.
		log.Printf("[worker-daemon] rate stream ended, draining")
		w.draining.Store(true)
		w.cancelCtx()
	}()

	// Goroutine B: push histogram snapshots to master.
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		c.StatsSender(ctx, statsStream, func() *pb.StatsPush {
			// Drain any out-of-band phase-boundary push first so the next
			// interval snapshot only contains samples from the new phase.
			select {
			case oob := <-outOfBandStats:
				return oob
			default:
			}

			rawBytes, corrBytes, err := pool.SnapshotAndResetHistograms()
			if err != nil {
				w.consecutiveStatsErrors++
				log.Printf("[worker-daemon] snapshot error %d/%d: %v",
					w.consecutiveStatsErrors, statsErrorBail, err)
				if w.consecutiveStatsErrors >= statsErrorBail {
					log.Printf("[worker-daemon] %d consecutive stats failures -- bailing",
						w.consecutiveStatsErrors)
					w.cancelCtx()
				}
				return nil
			}
			w.consecutiveStatsErrors = 0
			return &pb.StatsPush{
				WorkerId:     c.WorkerID,
				Timestamp:    uint64(time.Now().UnixMilli()),
				ObservedTps:  0,
				QueueDrops:   pool.TotalDrops(),
				HdrRaw:       rawBytes,
				HdrCorrected: corrBytes,
				ErrorRate:    pool.ErrorRate(),
				PhaseName:    pool.CurrentPhase(),
			}
		})
	}()

	// Goroutine C: local job submission loop.
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		w.runJobLoop(ctx, pool, checker)
	}()

	log.Printf("[worker-daemon] started (master=%s worker=%s targets=%d)",
		masterAddr, w.workerAddr, len(w.targets))
	return nil
}

// Wait blocks until the worker daemon exits.
func (w *WorkerDaemon) Wait() {
	w.wg.Wait()
}

// Stop signals a graceful shutdown. Safe to call concurrently with Run().
func (w *WorkerDaemon) Stop() {
	// Set stopped before cancelling so Run() will not install a fresh ctx
	// after the cancel fires.
	w.stopped.Store(true)
	w.draining.Store(true)
	w.cancelCtx()
	w.wg.Wait()
	// Snapshot resources under mu then tear them down outside the lock.
	pool, checker, client := w.clearCycleResources()
	teardownCycle(pool, checker, client)
	log.Printf("[worker-daemon] stopped")
}

// runJobLoop submits jobs to the local pool at the rate the limiter allows.
// Receives pool and checker as parameters (captured at Start time) to avoid
// reading w.pool/w.checker after they may have been cleared by Stop().
func (w *WorkerDaemon) runJobLoop(ctx context.Context, pool *worker.Pool, checker *health.Checker) {
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
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
				if checker != nil && !checker.IsHealthy(t.Name) {
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
				if !pool.Submit(job) {
					break
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
