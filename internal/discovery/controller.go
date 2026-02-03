package discovery

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/kar98k/internal/config"
	"github.com/kar98k/internal/health"
	"github.com/kar98k/internal/worker"
)

// State represents the current state of the discovery process.
type State int

const (
	StateIdle State = iota
	StateRunning
	StateCompleted
	StateFailed
)

// Controller manages the adaptive load discovery process.
type Controller struct {
	cfg      config.Discovery
	pool     *worker.Pool
	metrics  *health.Metrics
	analyzer *Analyzer

	mu       sync.RWMutex
	state    State
	result   *Result
	cancel   context.CancelFunc
	startTime time.Time

	// Current search state
	currentTPS    float64
	lowTPS        float64
	highTPS       float64
	lastStableTPS float64
	breakingTPS   float64
	stepsCompleted int

	// Progress tracking
	progress     float64
	statusMsg    string

	// Callbacks for TUI updates
	onProgress func(progress float64, currentTPS float64, p95 float64, errRate float64, status string)
	onComplete func(result *Result)
}

// NewController creates a new discovery controller.
func NewController(cfg config.Discovery, pool *worker.Pool, metrics *health.Metrics) *Controller {
	return &Controller{
		cfg:      cfg,
		pool:     pool,
		metrics:  metrics,
		analyzer: NewAnalyzer(5 * time.Second), // 5 second sliding window
		state:    StateIdle,
		lowTPS:   cfg.MinTPS,
		highTPS:  cfg.MaxTPS,
	}
}

// SetProgressCallback sets the callback for progress updates.
func (c *Controller) SetProgressCallback(fn func(progress float64, currentTPS float64, p95 float64, errRate float64, status string)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onProgress = fn
}

// SetCompleteCallback sets the callback for completion.
func (c *Controller) SetCompleteCallback(fn func(result *Result)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onComplete = fn
}

// Start begins the discovery process.
func (c *Controller) Start(ctx context.Context) error {
	c.mu.Lock()
	if c.state == StateRunning {
		c.mu.Unlock()
		return fmt.Errorf("discovery already running")
	}

	ctx, c.cancel = context.WithCancel(ctx)
	c.state = StateRunning
	c.startTime = time.Now()
	c.lowTPS = c.cfg.MinTPS
	c.highTPS = c.cfg.MaxTPS
	c.currentTPS = c.cfg.MinTPS
	c.lastStableTPS = 0
	c.breakingTPS = 0
	c.stepsCompleted = 0
	c.analyzer.Reset()
	c.mu.Unlock()

	go c.run(ctx)

	return nil
}

// Stop stops the discovery process.
func (c *Controller) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cancel != nil {
		c.cancel()
	}
	c.state = StateIdle
}

// GetState returns the current state.
func (c *Controller) GetState() State {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state
}

// GetResult returns the discovery result.
func (c *Controller) GetResult() *Result {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.result
}

// GetProgress returns current progress (0-100).
func (c *Controller) GetProgress() float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.progress
}

// GetCurrentTPS returns the current TPS being tested.
func (c *Controller) GetCurrentTPS() float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.currentTPS
}

// GetSearchRange returns the current binary search range.
func (c *Controller) GetSearchRange() (low, high float64) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lowTPS, c.highTPS
}

// GetStatusMessage returns the current status message.
func (c *Controller) GetStatusMessage() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.statusMsg
}

// GetElapsed returns the elapsed time since discovery started.
func (c *Controller) GetElapsed() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.startTime.IsZero() {
		return 0
	}
	return time.Since(c.startTime)
}

// run executes the binary search algorithm.
func (c *Controller) run(ctx context.Context) {
	defer func() {
		c.mu.Lock()
		if c.state == StateRunning {
			c.state = StateCompleted
		}
		c.mu.Unlock()
	}()

	log.Printf("[discovery] starting adaptive load discovery (min=%.0f, max=%.0f, latency_limit=%dms, error_limit=%.1f%%)",
		c.cfg.MinTPS, c.cfg.MaxTPS, c.cfg.LatencyLimitMs, c.cfg.ErrorRateLimit)

	c.updateStatus("Starting discovery...")

	// Binary search loop
	for {
		select {
		case <-ctx.Done():
			c.updateStatus("Discovery cancelled")
			c.mu.Lock()
			c.state = StateFailed
			c.mu.Unlock()
			return
		default:
		}

		// Check convergence
		if c.hasConverged() {
			break
		}

		// Run a step at the current TPS
		stepResult := c.runStep(ctx)
		if stepResult == nil {
			// Context cancelled or error
			return
		}

		c.mu.Lock()
		c.stepsCompleted++

		if stepResult.Stable {
			// System is stable at this TPS, try higher
			c.lastStableTPS = c.currentTPS
			c.lowTPS = c.currentTPS

			if c.currentTPS >= c.highTPS {
				// Reached max, we're done
				c.mu.Unlock()
				break
			}

			// Binary search: try midpoint between current and high
			c.currentTPS = (c.lowTPS + c.highTPS) / 2
			c.updateStatusLocked(fmt.Sprintf("Stable at %.0f TPS, trying %.0f", c.lowTPS, c.currentTPS))
		} else {
			// System is unstable, record breaking point and try lower
			c.breakingTPS = c.currentTPS
			c.highTPS = c.currentTPS

			// Binary search: try midpoint between low and current
			c.currentTPS = (c.lowTPS + c.highTPS) / 2
			c.updateStatusLocked(fmt.Sprintf("Unstable at %.0f TPS, trying %.0f", c.highTPS, c.currentTPS))
		}

		// Update progress
		c.updateProgress()
		c.mu.Unlock()

		log.Printf("[discovery] step %d: tps=%.0f stable=%v p95=%.1fms err=%.2f%% range=[%.0f-%.0f]",
			c.stepsCompleted, stepResult.TPS, stepResult.Stable, stepResult.P95Latency,
			stepResult.ErrorRate, c.lowTPS, c.highTPS)
	}

	// Generate final result
	c.mu.Lock()
	snapshot := c.analyzer.TakeSnapshot()

	sustainedTPS := c.lastStableTPS
	if sustainedTPS == 0 {
		sustainedTPS = c.cfg.MinTPS
	}

	breakingTPS := c.breakingTPS
	if breakingTPS == 0 {
		breakingTPS = sustainedTPS * 1.2
	}

	c.result = NewResult(
		sustainedTPS,
		breakingTPS,
		snapshot.P95Latency,
		snapshot.ErrorRate,
		time.Since(c.startTime),
		c.stepsCompleted,
	)
	c.state = StateCompleted
	c.progress = 100

	onComplete := c.onComplete
	result := c.result
	c.mu.Unlock()

	c.updateStatus("Discovery complete!")

	log.Printf("[discovery] completed: sustained=%.0f breaking=%.0f p95=%.1fms err=%.2f%% duration=%s steps=%d",
		result.SustainedTPS, result.BreakingTPS, result.P95Latency, result.ErrorRate,
		result.TestDuration.Round(time.Second), result.StepsCompleted)

	if onComplete != nil {
		onComplete(result)
	}
}

// runStep runs a single TPS test step.
func (c *Controller) runStep(ctx context.Context) *StepResult {
	c.mu.Lock()
	tps := c.currentTPS
	c.mu.Unlock()

	// Reset analyzer for this step
	c.analyzer.ResetWindow()

	// Set the pool rate
	c.pool.SetRate(tps)

	// Create target for requests
	target := config.Target{
		Name:     "discovery",
		URL:      c.cfg.TargetURL,
		Protocol: c.cfg.Protocol,
		Method:   c.cfg.Method,
		Weight:   100,
		Timeout:  5 * time.Second,
	}

	client := c.pool.GetClient(c.cfg.Protocol)

	// Submit jobs for the step duration
	stepCtx, cancel := context.WithTimeout(ctx, c.cfg.StepDuration)
	defer cancel()

	startRequests := c.analyzer.GetTotalRequests()
	startErrors := c.analyzer.GetTotalErrors()

	// Job submission goroutine
	go func() {
		ticker := time.NewTicker(time.Second / time.Duration(tps))
		defer ticker.Stop()

		for {
			select {
			case <-stepCtx.Done():
				return
			case <-ticker.C:
				c.pool.Submit(worker.Job{
					Target: target,
					Client: client,
				})
			}
		}
	}()

	// Wait for step to complete, collecting metrics
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-stepCtx.Done():
			// Step complete, analyze results
			snapshot := c.analyzer.TakeSnapshot()

			endRequests := c.analyzer.GetTotalRequests()
			endErrors := c.analyzer.GetTotalErrors()

			stepRequests := endRequests - startRequests
			stepErrors := endErrors - startErrors

			stable := c.isStable(snapshot.P95Latency, snapshot.ErrorRate)

			return &StepResult{
				TPS:           tps,
				P95Latency:    snapshot.P95Latency,
				ErrorRate:     snapshot.ErrorRate,
				Stable:        stable,
				Duration:      c.cfg.StepDuration,
				TotalRequests: stepRequests,
				TotalErrors:   stepErrors,
			}

		case <-ctx.Done():
			// Discovery cancelled
			return nil

		case <-ticker.C:
			// Update progress callback
			snapshot := c.analyzer.TakeSnapshot()
			c.notifyProgress(tps, snapshot.P95Latency, snapshot.ErrorRate)
		}
	}
}

// isStable checks if the system is stable based on latency and error rate.
func (c *Controller) isStable(p95Latency, errorRate float64) bool {
	latencyOK := p95Latency <= float64(c.cfg.LatencyLimitMs)
	errorOK := errorRate <= c.cfg.ErrorRateLimit
	return latencyOK && errorOK
}

// hasConverged checks if the binary search has converged.
func (c *Controller) hasConverged() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.lowTPS == 0 {
		return false
	}

	// Check if the range has converged within the convergence rate
	rangeRatio := (c.highTPS - c.lowTPS) / c.lowTPS
	return rangeRatio < c.cfg.ConvergenceRate
}

// updateProgress calculates and updates the progress percentage.
func (c *Controller) updateProgress() {
	// Estimate progress based on how narrow the search range has become
	initialRange := c.cfg.MaxTPS - c.cfg.MinTPS
	currentRange := c.highTPS - c.lowTPS

	if initialRange > 0 {
		c.progress = (1 - currentRange/initialRange) * 100
		if c.progress > 99 {
			c.progress = 99
		}
	}
}

// updateStatus updates the status message.
func (c *Controller) updateStatus(msg string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.updateStatusLocked(msg)
}

func (c *Controller) updateStatusLocked(msg string) {
	c.statusMsg = msg
}

// notifyProgress notifies the progress callback.
func (c *Controller) notifyProgress(currentTPS, p95, errRate float64) {
	c.mu.RLock()
	onProgress := c.onProgress
	progress := c.progress
	status := c.statusMsg
	c.mu.RUnlock()

	if onProgress != nil {
		onProgress(progress, currentTPS, p95, errRate, status)
	}
}

// RecordRequest records a request result for analysis.
// This should be called by the worker pool for each completed request.
func (c *Controller) RecordRequest(latencyMs float64, isError bool) {
	if c.GetState() == StateRunning {
		c.analyzer.RecordLatency(latencyMs, isError)
	}
}
