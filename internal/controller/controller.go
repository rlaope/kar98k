package controller

import (
	"context"
	"log"
	"math/rand"
	"sync"
	"time"

	"github.com/kar98k/internal/config"
	"github.com/kar98k/internal/health"
	"github.com/kar98k/internal/pattern"
	"github.com/kar98k/internal/worker"
)

// Controller orchestrates traffic generation.
type Controller struct {
	cfg       config.Controller
	targets   []config.Target
	engine    *pattern.Engine
	scheduler *Scheduler
	pool      *worker.Pool
	checker   *health.Checker
	metrics   *health.Metrics

	// Weighted target selection
	weightedTargets []config.Target
	totalWeight     int
	rng             *rand.Rand

	cancel context.CancelFunc
	wg     sync.WaitGroup
	mu     sync.RWMutex
}

// NewController creates a new controller.
func NewController(
	cfg config.Controller,
	targets []config.Target,
	engine *pattern.Engine,
	pool *worker.Pool,
	checker *health.Checker,
	metrics *health.Metrics,
) *Controller {
	c := &Controller{
		cfg:       cfg,
		targets:   targets,
		engine:    engine,
		scheduler: NewScheduler(cfg.Schedule),
		pool:      pool,
		checker:   checker,
		metrics:   metrics,
		rng:       rand.New(rand.NewSource(time.Now().UnixNano())),
	}

	c.buildWeightedTargets()
	return c
}

// buildWeightedTargets creates a weighted list for random selection.
func (c *Controller) buildWeightedTargets() {
	c.weightedTargets = nil
	c.totalWeight = 0

	for _, t := range c.targets {
		c.totalWeight += t.Weight
		c.weightedTargets = append(c.weightedTargets, t)
	}
}

// selectTarget picks a target based on weights.
func (c *Controller) selectTarget() config.Target {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if len(c.weightedTargets) == 0 {
		return config.Target{}
	}

	r := c.rng.Intn(c.totalWeight)
	cumulative := 0

	for _, t := range c.weightedTargets {
		cumulative += t.Weight
		if r < cumulative {
			return t
		}
	}

	return c.weightedTargets[0]
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

	// Request generation loop
	c.wg.Add(1)
	go c.generateLoop(ctx)

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
func (c *Controller) generateLoop(ctx context.Context) {
	defer c.wg.Done()

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

		target := c.selectTarget()
		if target.Name == "" {
			continue
		}

		// Skip unhealthy targets
		if c.checker != nil && !c.checker.IsHealthy(target.Name) {
			continue
		}

		job := worker.Job{
			Target: target,
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
	BaseTPS           float64
	MaxTPS            float64
	ScheduleMultiplier float64
	CurrentHour       int
	ActiveWorkers     int
	QueueSize         int
	PatternStatus     pattern.Status
}

// GetStatus returns the current status.
func (c *Controller) GetStatus() Status {
	schedInfo := c.scheduler.GetInfo()

	return Status{
		BaseTPS:            c.cfg.BaseTPS,
		MaxTPS:             c.cfg.MaxTPS,
		ScheduleMultiplier: schedInfo.CurrentMultiplier,
		CurrentHour:        schedInfo.CurrentHour,
		ActiveWorkers:      c.pool.Active(),
		QueueSize:          c.pool.QueueSize(),
		PatternStatus:      c.engine.GetStatus(),
	}
}
