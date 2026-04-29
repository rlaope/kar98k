package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/kar98k/internal/config"
	"github.com/kar98k/internal/health"
	"github.com/kar98k/internal/worker"
)

// breakerState is the open/closed state of the circuit breaker.
type breakerState int

const (
	breakerClosed breakerState = iota // healthy — traffic flowing
	breakerOpen                       // tripped — pool paused
)

// breakerPool is the slice of *worker.Pool the breaker actually uses.
// Splitting it out lets tests inject a fake without spinning up a
// full pool with goroutines and a real rate limiter.
type breakerPool interface {
	ErrorRate() float64
	LatencyPercentile(percentile float64, corrected bool) float64
	Pause()
	Resume()
}

// compile-time assertion that *worker.Pool satisfies the interface.
var _ breakerPool = (*worker.Pool)(nil)

// CircuitBreaker watches the worker pool's sustained error rate and
// P95 latency, pauses traffic when either threshold is breached for
// the configured sustained window, and (optionally) auto-resumes
// after the metrics recover for ResumeAfter.
//
// Design choices worth knowing:
//   - We poll the pool every 1s instead of subscribing to events, so
//     the breaker is decoupled from the request hot path.
//   - The "sustained" check uses the pool's existing 60s ring buffer
//     for error rate; for P95 we sample the latency histogram which
//     is itself a sliding observation. SustainedFor controls how many
//     consecutive 1s ticks must breach before tripping.
//   - Auto-resume is intentionally one-shot per trip: when conditions
//     recover for ResumeAfter, we resume; if they breach again we
//     re-trip and the resume timer resets.
type CircuitBreaker struct {
	cfg     config.Safety
	pool    breakerPool
	metrics *health.Metrics

	mu             sync.RWMutex
	state          breakerState
	breachStreak   time.Duration // consecutive time conditions have been breached
	openedAt       time.Time
	manualPaused   bool // tripped via Pause() (e.g. `kar resume` semantics)
	lastWebhookFor breakerState
}

// NewCircuitBreaker constructs a breaker bound to a pool and a
// metrics surface. The breaker is dormant until Run is invoked.
func NewCircuitBreaker(cfg config.Safety, pool *worker.Pool, metrics *health.Metrics) *CircuitBreaker {
	return newCircuitBreaker(cfg, pool, metrics)
}

func newCircuitBreaker(cfg config.Safety, pool breakerPool, metrics *health.Metrics) *CircuitBreaker {
	return &CircuitBreaker{
		cfg:     cfg,
		pool:    pool,
		metrics: metrics,
	}
}

// Run polls the pool every 1s and trips/resumes as needed. Exits on
// ctx cancellation. Run blocks; callers should invoke it in its own
// goroutine.
func (b *CircuitBreaker) Run(ctx context.Context) {
	if b == nil || !b.cfg.Enabled {
		return
	}
	b.publishState()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.tick()
		}
	}
}

// tick is invoked once per polling interval. It compares the live
// pool metrics against the configured thresholds and decides whether
// to trip, hold, or resume.
func (b *CircuitBreaker) tick() {
	breach, reason := b.checkBreach()

	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case breakerClosed:
		if breach {
			b.breachStreak += time.Second
			if b.breachStreak >= b.cfg.SustainedFor {
				b.trip(reason)
			}
		} else {
			b.breachStreak = 0
		}
	case breakerOpen:
		if breach {
			// Re-arm the recovery clock — conditions still bad.
			b.openedAt = time.Now()
			return
		}
		if b.cfg.ResumeAfter > 0 && time.Since(b.openedAt) >= b.cfg.ResumeAfter && !b.manualPaused {
			b.resumeLocked("auto-resume — conditions recovered")
		}
	}
}

// checkBreach reads pool metrics and returns whether either threshold
// is currently exceeded.
func (b *CircuitBreaker) checkBreach() (bool, string) {
	if b.cfg.ErrorRateAbove > 0 {
		errRate := b.pool.ErrorRate() * 100 // ErrorRate returns 0..1, threshold is %
		if errRate >= b.cfg.ErrorRateAbove {
			return true, "error rate breach"
		}
	}
	if b.cfg.P95LatencyAbove > 0 {
		p95Ms := b.pool.LatencyPercentile(95, false)
		thresholdMs := float64(b.cfg.P95LatencyAbove) / float64(time.Millisecond)
		if p95Ms >= thresholdMs {
			return true, "P95 latency breach"
		}
	}
	return false, ""
}

// trip transitions the breaker to the open state and pauses the pool.
// Caller must hold b.mu.
func (b *CircuitBreaker) trip(reason string) {
	b.state = breakerOpen
	b.openedAt = time.Now()
	b.breachStreak = 0
	b.pool.Pause()
	b.publishStateLocked()
	log.Printf("[breaker] OPEN — %s; traffic paused", reason)
	go b.fireWebhook("open", reason)
}

// resumeLocked transitions the breaker to closed and resumes the pool.
// Caller must hold b.mu.
func (b *CircuitBreaker) resumeLocked(reason string) {
	b.state = breakerClosed
	b.breachStreak = 0
	b.manualPaused = false
	b.pool.Resume()
	b.publishStateLocked()
	log.Printf("[breaker] CLOSED — %s", reason)
	go b.fireWebhook("close", reason)
}

// ManualResume is the entry point for `kar resume`. Forces the
// breaker back to closed regardless of current metrics.
func (b *CircuitBreaker) ManualResume() {
	if b == nil || !b.cfg.Enabled {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state != breakerOpen {
		return
	}
	b.resumeLocked("manual resume via `kar resume`")
}

// State returns the current breaker state for status surfaces.
func (b *CircuitBreaker) State() (open bool, since time.Time) {
	if b == nil {
		return false, time.Time{}
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.state == breakerOpen, b.openedAt
}

// publishState writes the current state to Prometheus.
func (b *CircuitBreaker) publishState() {
	b.mu.RLock()
	defer b.mu.RUnlock()
	b.publishStateLocked()
}

func (b *CircuitBreaker) publishStateLocked() {
	if b.metrics == nil {
		return
	}
	v := 0.0
	if b.state == breakerOpen {
		v = 1.0
	}
	b.metrics.SetCircuitBreakerState(v)
}

// fireWebhook posts a small JSON payload to the operator-supplied URL
// when the breaker transitions. Best-effort: failures are logged and
// dropped so a flaky webhook doesn't block the breaker logic. Run
// from a goroutine in trip/resumeLocked so we don't block tick under
// b.mu.
func (b *CircuitBreaker) fireWebhook(transition, reason string) {
	if b.cfg.Webhook == "" {
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"transition": transition,
		"reason":     reason,
		"at":         time.Now().UTC().Format(time.RFC3339),
	})
	req, err := http.NewRequest("POST", b.cfg.Webhook, bytes.NewReader(payload))
	if err != nil {
		log.Printf("[breaker] webhook build failed: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[breaker] webhook POST failed: %v", err)
		return
	}
	resp.Body.Close()
}
