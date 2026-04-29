package controller

import (
	"context"
	"testing"
	"time"

	"github.com/kar98k/internal/config"
	"github.com/kar98k/internal/pattern"
)

// TestScenarioRunner_AdvancesThroughEveryPhase exercises the full
// timeline against very short durations so we can observe each phase
// being applied without relying on long sleeps.
func TestScenarioRunner_AdvancesThroughEveryPhase(t *testing.T) {
	eng := pattern.NewEngine(config.Pattern{}, 100, 1000)
	scenarios := []config.Scenario{
		{Name: "warmup", Duration: 60 * time.Millisecond, BaseTPS: 20},
		{Name: "ramp", Duration: 60 * time.Millisecond, BaseTPS: 80},
		{Name: "soak", Duration: 60 * time.Millisecond, BaseTPS: 50, MaxTPS: 500},
	}
	runner := NewScenarioRunner(scenarios, eng, 100, 1000, config.Pattern{})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	go runner.Run(ctx)

	// Sample at the midpoint of each phase. The runner ticks on real
	// clock time, so we wait one phase-width between samples.
	pause := 60 * time.Millisecond

	time.Sleep(pause / 2)
	if got := eng.GetBaseTPS(); got != 20 {
		t.Fatalf("phase 1 baseTPS = %v, want 20", got)
	}
	if st := runner.Status(); st.Name != "warmup" || st.Index != 1 || st.Total != 3 {
		t.Fatalf("phase 1 status = %+v", st)
	}

	time.Sleep(pause)
	if got := eng.GetBaseTPS(); got != 80 {
		t.Fatalf("phase 2 baseTPS = %v, want 80", got)
	}
	if st := runner.Status(); st.Name != "ramp" || st.Index != 2 {
		t.Fatalf("phase 2 status = %+v", st)
	}

	time.Sleep(pause)
	if got := eng.GetBaseTPS(); got != 50 {
		t.Fatalf("phase 3 baseTPS = %v, want 50", got)
	}
	if got := eng.GetMaxTPS(); got != 500 {
		t.Fatalf("phase 3 maxTPS = %v, want 500", got)
	}

	// After the last phase elapses the runner exits but the engine
	// stays at the final phase's settings.
	time.Sleep(pause)
	if got := eng.GetBaseTPS(); got != 50 {
		t.Fatalf("post-timeline baseTPS = %v, want 50 (last phase persists)", got)
	}
	if st := runner.Status(); !st.Done {
		t.Fatalf("expected runner Done=true after timeline; got %+v", st)
	}
	if got := runner.Status().Transitions; got != 3 {
		t.Fatalf("Transitions = %d, want 3", got)
	}
}

// TestScenarioRunner_PhaseInheritsDefaults verifies the inheritance
// rule: a scenario that omits BaseTPS/MaxTPS/Pattern picks them up
// from the runner defaults rather than collapsing to zero.
func TestScenarioRunner_PhaseInheritsDefaults(t *testing.T) {
	eng := pattern.NewEngine(config.Pattern{}, 100, 1000)
	scenarios := []config.Scenario{
		// Only the name + duration set; everything else inherits.
		{Name: "inherit", Duration: 30 * time.Millisecond},
	}
	defaultPattern := config.Pattern{
		Poisson: config.Poisson{Enabled: true, Lambda: 0.05, SpikeFactor: 2,
			MinInterval: time.Second, MaxInterval: 10 * time.Second,
			RampUp: time.Second, RampDown: time.Second},
	}
	runner := NewScenarioRunner(scenarios, eng, 250, 2500, defaultPattern)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	go runner.Run(ctx)
	time.Sleep(15 * time.Millisecond)

	if got := eng.GetBaseTPS(); got != 250 {
		t.Fatalf("baseTPS = %v, want 250 (inherited)", got)
	}
	if got := eng.GetMaxTPS(); got != 2500 {
		t.Fatalf("maxTPS = %v, want 2500 (inherited)", got)
	}
	if !eng.GetStatus().PoissonEnabled {
		t.Fatalf("expected default Poisson to be applied")
	}
}

// TestScenarioRunner_StopsOnContextCancel guards the shutdown path —
// the runner must exit promptly when its context is cancelled.
func TestScenarioRunner_StopsOnContextCancel(t *testing.T) {
	eng := pattern.NewEngine(config.Pattern{}, 100, 1000)
	scenarios := []config.Scenario{
		{Name: "long", Duration: 5 * time.Second, BaseTPS: 42},
	}
	runner := NewScenarioRunner(scenarios, eng, 100, 1000, config.Pattern{})
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() { runner.Run(ctx); close(done) }()

	// Let the runner enter phase 1.
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("runner did not stop after ctx cancel")
	}
	if !runner.Status().Done {
		t.Fatalf("runner Status.Done = false after cancel")
	}
}

// TestScenarioRunner_NoOpWhenEmpty ensures Run is safe to call on a
// runner with no phases — the controller will hand it an empty list
// when scenarios mode is disabled.
func TestScenarioRunner_NoOpWhenEmpty(t *testing.T) {
	eng := pattern.NewEngine(config.Pattern{}, 100, 1000)
	runner := NewScenarioRunner(nil, eng, 100, 1000, config.Pattern{})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	runner.Run(ctx) // should return immediately
	if st := runner.Status(); st.Total != 0 {
		t.Fatalf("Status on empty runner Total = %d, want 0", st.Total)
	}
}
