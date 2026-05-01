package controller

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/kar98k/internal/config"
	"github.com/kar98k/internal/health"
	"github.com/kar98k/internal/pattern"
	"github.com/kar98k/internal/targets"
	"github.com/kar98k/internal/worker"
	"github.com/kar98k/pkg/protocol"
)

// PoolFacade is the subset of *worker.Pool that the controller and
// daemon use. Splitting it out allows the master-mode WorkerRegistry
// (internal/rpc) to satisfy the same seam without a local pool.
//
// Methods used by the circuit breaker (ErrorRate, Pause, Resume) are
// kept in the existing breakerPool interface in breaker.go; they are
// not repeated here to avoid implementation drift.
type PoolFacade interface {
	SetRate(tps float64)
	// SetPhase records the active scenario phase name. In master mode the
	// registry stores it so subsequent SetRate broadcasts tag the
	// RateUpdate with phase_name; in solo mode the worker pool tracks it
	// alongside its own histograms so its CurrentPhase() reflects truth.
	// Empty string means "no scenarios" or "default phase". See #68.
	SetPhase(phase string)
	Submit(job worker.Job) bool
	GetClient(proto config.Protocol) protocol.Client
	Active() int
	QueueSize() int
	TotalDrops() int64
	DropRate() float64
	LatencyPercentile(percentile float64, corrected bool) float64
}

// compile-time assertion that *worker.Pool satisfies PoolFacade.
var _ PoolFacade = (*worker.Pool)(nil)

// Controller orchestrates traffic generation.
type Controller struct {
	cfg       config.Controller
	targets   []config.Target
	engine    *pattern.Engine
	scheduler *Scheduler
	pool      PoolFacade
	checker   *health.Checker
	metrics   *health.Metrics
	scenarios *ScenarioRunner
	breaker   *CircuitBreaker
	submitter Submitter
	picker    *targets.Picker

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewController creates a new controller. Pass submitter to choose
// between solo (LocalSubmitter) and master (NoopSubmitter) job
// generation. A nil submitter defaults to LocalSubmitter so existing
// solo-mode call-sites stay backward compatible.
func NewController(
	cfg config.Controller,
	tgts []config.Target,
	engine *pattern.Engine,
	pool PoolFacade,
	checker *health.Checker,
	metrics *health.Metrics,
	submitter Submitter,
) *Controller {
	c := &Controller{
		cfg:       cfg,
		targets:   tgts,
		engine:    engine,
		scheduler: NewScheduler(cfg.Schedule),
		pool:      pool,
		checker:   checker,
		metrics:   metrics,
		picker:    targets.New(tgts),
	}
	if submitter == nil {
		submitter = &LocalSubmitter{c: c}
	} else if ls, ok := submitter.(*LocalSubmitter); ok && ls.c == nil {
		// Why: callers pass &LocalSubmitter{} as a marker; bind it now
		// so its Run can reach generateLoop without exposing internals.
		ls.c = c
	}
	c.submitter = submitter
	return c
}

// AttachScenarios opts the controller into multi-phase mode. Pass an
// empty/nil slice to keep the existing single-pattern behaviour. The
// runner starts when Controller.Start is called.
func (c *Controller) AttachScenarios(scenarios []config.Scenario, defaultPattern config.Pattern) {
	if len(scenarios) == 0 {
		return
	}
	r := NewScenarioRunner(scenarios, c.engine, c.cfg.BaseTPS, c.cfg.MaxTPS, defaultPattern)
	if c.metrics != nil {
		r.SetMetrics(c.metrics)
	}
	r.SetOnPhase(c.pool.SetPhase)
	c.scenarios = r
}

// AttachSafety opts the controller into circuit-breaker mode. When
// safety.Enabled is false the call is a no-op and the breaker
// goroutine never runs. pool must be the concrete *worker.Pool; the
// breaker is not meaningful in master mode and should be skipped there.
func (c *Controller) AttachSafety(safety config.Safety, pool *worker.Pool) {
	if !safety.Enabled || pool == nil {
		return
	}
	c.breaker = NewCircuitBreaker(safety, pool, c.metrics)
}

// ManualResume forwards to the breaker so `kar resume` can clear an
// open circuit. No-op when safety is disabled or the breaker is
// already closed.
func (c *Controller) ManualResume() {
	c.breaker.ManualResume()
}

// BreakerOpen reports whether the circuit breaker is currently
// tripped. Returns (false, zero-time) when safety is disabled.
func (c *Controller) BreakerOpen() (bool, time.Time) {
	return c.breaker.State()
}

// Start begins traffic generation.
func (c *Controller) Start(ctx context.Context) {
	ctx, c.cancel = context.WithCancel(ctx)

	// Ramp-up phase
	if c.cfg.RampUpDuration > 0 {
		c.wg.Add(1)
		go c.rampUp(ctx)
	}

	// Main control loop
	c.wg.Add(1)
	go c.controlLoop(ctx)

	// Job generation strategy — LocalSubmitter runs the per-ms loop in
	// solo mode; NoopSubmitter returns immediately in master mode where
	// the WorkerRegistry fans rate out to remote workers instead.
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		c.submitter.Run(ctx)
	}()

	// Scenario timeline (multi-phase runs only).
	if c.scenarios != nil {
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			c.scenarios.Run(ctx)
		}()
	}

	// Circuit breaker watcher (safety mode only).
	if c.breaker != nil {
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			c.breaker.Run(ctx)
		}()
	}

	log.Printf("[controller] started with base TPS %.0f, max TPS %.0f", c.cfg.BaseTPS, c.cfg.MaxTPS)
}

// rampUp gradually increases TPS from 0 to base.
func (c *Controller) rampUp(ctx context.Context) {
	defer c.wg.Done()

	startTime := time.Now()
	startTPS := 1.0
	targetTPS := c.cfg.BaseTPS

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	log.Printf("[controller] starting ramp-up over %s", c.cfg.RampUpDuration)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			elapsed := time.Since(startTime)
			if elapsed >= c.cfg.RampUpDuration {
				c.pool.SetRate(targetTPS)
				log.Printf("[controller] ramp-up complete at %.0f TPS", targetTPS)
				return
			}

			progress := float64(elapsed) / float64(c.cfg.RampUpDuration)
			currentTPS := startTPS + (targetTPS-startTPS)*progress
			c.pool.SetRate(currentTPS)
		}
	}
}

// controlLoop periodically updates the target TPS.
func (c *Controller) controlLoop(ctx context.Context) {
	defer c.wg.Done()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.updateTPS()
		}
	}
}

// updateTPS calculates and applies the current target TPS.
func (c *Controller) updateTPS() {
	// Get schedule multiplier
	schedMult := c.scheduler.GetMultiplier()

	// Calculate TPS using pattern engine
	tps := c.engine.CalculateTPS(schedMult)

	// Update pool rate
	c.pool.SetRate(tps)

	// Update spike metric
	c.metrics.SetSpikeActive(c.engine.IsSpiking())
}

// generateLoop continuously submits jobs to the worker pool.
// Why: invoked by LocalSubmitter.Run inside a goroutine that already
// owns wg.Done — this loop must NOT decrement the wg itself.
func (c *Controller) generateLoop(ctx context.Context) {
	// Use a ticker that fires at a high rate
	// The actual TPS is controlled by the rate limiter in the pool
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.submitJobs(ctx)
		}
	}
}

// submitJobs submits jobs to the worker pool.
func (c *Controller) submitJobs(ctx context.Context) {
	// Submit multiple jobs per tick to keep the pool fed
	// The rate limiter in the pool controls actual execution rate
	for i := 0; i < 10; i++ {
		select {
		case <-ctx.Done():
			return
		default:
		}

		target := c.picker.Pick()
		if target == nil {
			continue
		}

		// Skip unhealthy targets
		if c.checker != nil && !c.checker.IsHealthy(target.Name) {
			continue
		}

		job := worker.Job{
			Target: *target,
			Client: c.pool.GetClient(target.Protocol),
		}

		if !c.pool.Submit(job) {
			// Queue full, back off
			return
		}
	}
}

// Stop gracefully stops the controller.
func (c *Controller) Stop() {
	log.Printf("[controller] stopping...")

	if c.cancel != nil {
		c.cancel()
	}

	c.wg.Wait()
	log.Printf("[controller] stopped")
}

// GetStatus returns the current controller status.
type Status struct {
	BaseTPS            float64
	MaxTPS             float64
	ScheduleMultiplier float64
	CurrentHour        int
	ActiveWorkers      int
	QueueSize          int
	QueueDrops         int64   // lifetime count of dropped jobs
	QueueDropRate      float64 // sustained drop rate over the last 60s, 0..1
	// Latency percentiles in milliseconds. *Raw values come from
	// observed request durations only; *Corrected values are
	// CO-corrected and synthesise samples for slots missed during
	// stalls. Both reach 0 before the first sample is recorded.
	LatencyP95Raw       float64
	LatencyP99Raw       float64
	LatencyP95Corrected float64
	LatencyP99Corrected float64
	PatternStatus       pattern.Status
	// Scenario describes the active phase when scenarios mode is on.
	// Total == 0 means scenarios mode is disabled and the single
	// top-level pattern is in effect.
	Scenario ScenarioStatus
}

// GetStatus returns the current status.
func (c *Controller) GetStatus() Status {
	schedInfo := c.scheduler.GetInfo()

	st := Status{
		BaseTPS:             c.cfg.BaseTPS,
		MaxTPS:              c.cfg.MaxTPS,
		ScheduleMultiplier:  schedInfo.CurrentMultiplier,
		CurrentHour:         schedInfo.CurrentHour,
		ActiveWorkers:       c.pool.Active(),
		QueueSize:           c.pool.QueueSize(),
		QueueDrops:          c.pool.TotalDrops(),
		QueueDropRate:       c.pool.DropRate(),
		LatencyP95Raw:       c.pool.LatencyPercentile(95, false),
		LatencyP99Raw:       c.pool.LatencyPercentile(99, false),
		LatencyP95Corrected: c.pool.LatencyPercentile(95, true),
		LatencyP99Corrected: c.pool.LatencyPercentile(99, true),
		PatternStatus:       c.engine.GetStatus(),
	}
	// Why: in master mode AttachScenarios is sometimes skipped, leaving
	// c.scenarios nil. Calling Status on the nil pointer is safe today
	// but the explicit guard documents the contract.
	if c.scenarios != nil {
		st.Scenario = c.scenarios.Status()
	}
	return st
}
