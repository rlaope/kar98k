package controller

import (
	"sync"
	"testing"
	"time"

	"github.com/kar98k/internal/config"
	"github.com/kar98k/internal/health"
	"github.com/prometheus/client_golang/prometheus"
)

// fakePool is a hand-driven implementation of breakerPool used by the
// breaker tests. Setters drive what ErrorRate/LatencyPercentile return,
// and Pause/Resume increment counters so we can assert the breaker
// transitioned the pool exactly once.
type fakePool struct {
	mu          sync.Mutex
	errorRate   float64
	p95Ms       float64
	pauseCalls  int
	resumeCalls int
}

func (f *fakePool) ErrorRate() float64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.errorRate
}

func (f *fakePool) LatencyPercentile(_ float64, _ bool) float64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.p95Ms
}

func (f *fakePool) Pause() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pauseCalls++
}

func (f *fakePool) Resume() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resumeCalls++
}

func (f *fakePool) setErrorRate(v float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errorRate = v
}

func (f *fakePool) setP95Ms(v float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.p95Ms = v
}

// TestCircuitBreaker_TripsOnSustainedErrorRate drives the fake pool
// to a high error rate for longer than SustainedFor, then verifies
// the breaker tripped exactly once.
func TestCircuitBreaker_TripsOnSustainedErrorRate(t *testing.T) {
	pool := &fakePool{}
	metrics := health.NewMetricsWithRegistry(prometheus.NewRegistry())
	cfg := config.Safety{
		Enabled:        true,
		ErrorRateAbove: 50,
		SustainedFor:   3 * time.Second,
	}
	b := newCircuitBreaker(cfg, pool, metrics)

	pool.setErrorRate(0.75) // 75% errors

	// Drive 4 manual ticks to exceed SustainedFor (3s).
	for i := 0; i < 4; i++ {
		b.tick()
	}

	if pool.pauseCalls != 1 {
		t.Fatalf("pauseCalls = %d, want 1", pool.pauseCalls)
	}
	open, _ := b.State()
	if !open {
		t.Fatalf("breaker should be open after sustained breach")
	}
}

// TestCircuitBreaker_DoesNotTripOnTransientSpike confirms the
// sustained-window protection: a single tick of breach must not
// trip the breaker.
func TestCircuitBreaker_DoesNotTripOnTransientSpike(t *testing.T) {
	pool := &fakePool{}
	metrics := health.NewMetricsWithRegistry(prometheus.NewRegistry())
	cfg := config.Safety{
		Enabled:        true,
		ErrorRateAbove: 50,
		SustainedFor:   5 * time.Second,
	}
	b := newCircuitBreaker(cfg, pool, metrics)

	// One bad tick, then four good ticks. Breach streak resets.
	pool.setErrorRate(0.99)
	b.tick()
	pool.setErrorRate(0.0)
	for i := 0; i < 4; i++ {
		b.tick()
	}

	if pool.pauseCalls != 0 {
		t.Fatalf("pauseCalls = %d, want 0 (transient spike must not trip)", pool.pauseCalls)
	}
	open, _ := b.State()
	if open {
		t.Fatalf("breaker should remain closed on transient spike")
	}
}

// TestCircuitBreaker_AutoResumesAfterRecovery walks the breaker
// through trip → recovery → auto-resume.
func TestCircuitBreaker_AutoResumesAfterRecovery(t *testing.T) {
	pool := &fakePool{}
	metrics := health.NewMetricsWithRegistry(prometheus.NewRegistry())
	cfg := config.Safety{
		Enabled:        true,
		ErrorRateAbove: 50,
		SustainedFor:   2 * time.Second,
		ResumeAfter:    50 * time.Millisecond,
	}
	b := newCircuitBreaker(cfg, pool, metrics)

	// Trip
	pool.setErrorRate(0.99)
	b.tick()
	b.tick()
	b.tick()
	if open, _ := b.State(); !open {
		t.Fatalf("expected breaker open after sustained breach")
	}

	// Recover
	pool.setErrorRate(0.0)
	// Wait past ResumeAfter, then tick once more so the breaker
	// observes recovery + decides to resume.
	time.Sleep(60 * time.Millisecond)
	b.tick()

	if pool.resumeCalls != 1 {
		t.Fatalf("resumeCalls = %d, want 1 (auto-resume)", pool.resumeCalls)
	}
	if open, _ := b.State(); open {
		t.Fatalf("breaker should be closed after auto-resume")
	}
}

// TestCircuitBreaker_ManualResumeClearsOpenState exercises the
// `kar resume` path: caller forces the breaker closed regardless of
// current pool metrics.
func TestCircuitBreaker_ManualResumeClearsOpenState(t *testing.T) {
	pool := &fakePool{}
	metrics := health.NewMetricsWithRegistry(prometheus.NewRegistry())
	cfg := config.Safety{
		Enabled:        true,
		ErrorRateAbove: 50,
		SustainedFor:   1 * time.Second,
	}
	b := newCircuitBreaker(cfg, pool, metrics)

	// Trip the breaker.
	pool.setErrorRate(0.99)
	b.tick()
	b.tick()
	if open, _ := b.State(); !open {
		t.Fatalf("setup: breaker should be open")
	}

	// Pool is still bad; manual resume should still close it.
	b.ManualResume()

	if pool.resumeCalls != 1 {
		t.Fatalf("resumeCalls = %d, want 1 (manual resume)", pool.resumeCalls)
	}
	if open, _ := b.State(); open {
		t.Fatalf("breaker should be closed after manual resume")
	}
}

// TestCircuitBreaker_LatencyThresholdAlsoTrips verifies the P95
// latency check works the same way as error rate.
func TestCircuitBreaker_LatencyThresholdAlsoTrips(t *testing.T) {
	pool := &fakePool{}
	metrics := health.NewMetricsWithRegistry(prometheus.NewRegistry())
	cfg := config.Safety{
		Enabled:         true,
		P95LatencyAbove: 500 * time.Millisecond,
		SustainedFor:    2 * time.Second,
	}
	b := newCircuitBreaker(cfg, pool, metrics)

	pool.setP95Ms(800) // 800ms > 500ms threshold

	for i := 0; i < 3; i++ {
		b.tick()
	}

	if pool.pauseCalls != 1 {
		t.Fatalf("pauseCalls = %d, want 1 (P95 threshold breach)", pool.pauseCalls)
	}
}

// TestCircuitBreaker_DisabledIsNoOp ensures the breaker stays inert
// when safety.enabled is false — Run returns immediately and no
// state transitions happen.
func TestCircuitBreaker_DisabledIsNoOp(t *testing.T) {
	pool := &fakePool{}
	metrics := health.NewMetricsWithRegistry(prometheus.NewRegistry())
	cfg := config.Safety{Enabled: false}
	b := newCircuitBreaker(cfg, pool, metrics)

	pool.setErrorRate(0.99)
	for i := 0; i < 5; i++ {
		b.tick()
	}

	// tick() doesn't gate on Enabled (it's already past Run's check
	// for live use), but ManualResume must still be a no-op.
	b.ManualResume()
	if pool.pauseCalls != 0 {
		t.Fatalf("disabled breaker pauseCalls = %d, want 0", pool.pauseCalls)
	}
}
