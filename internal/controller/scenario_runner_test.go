package controller

import (
	"context"
	"testing"
	"time"

	"github.com/kar98k/internal/config"
	"github.com/kar98k/internal/health"
	"github.com/kar98k/internal/pattern"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
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

// TestScenarioRunner_InjectCurveDrivesEngineTPS confirms that an
// inject block actually drives the engine's base TPS while a phase
// runs — i.e. the new injection goroutine wires the evaluator to
// engine.SetBaseTPS at the documented cadence.
func TestScenarioRunner_InjectCurveDrivesEngineTPS(t *testing.T) {
	eng := pattern.NewEngine(config.Pattern{}, 100, 1000)
	scenarios := []config.Scenario{{
		Name:     "ramp-up",
		Duration: 200 * time.Millisecond,
		Inject: []config.InjectStep{
			{Type: config.InjectRampTPS, Duration: 200 * time.Millisecond, From: 10, To: 200},
		},
	}}
	runner := NewScenarioRunner(scenarios, eng, 100, 1000, config.Pattern{})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	go runner.Run(ctx)

	// Sample at ~50% of the ramp; the linear interpolator should land
	// somewhere between From=10 and To=200, well above the inherited
	// flat default.
	time.Sleep(110 * time.Millisecond)
	mid := eng.GetBaseTPS()
	if mid <= 50 || mid >= 180 {
		t.Fatalf("midpoint TPS = %v, want roughly halfway between 10 and 200", mid)
	}

	// Wait for the phase to expire; the injection goroutine should
	// stop, leaving the engine at whatever the final tick set.
	time.Sleep(150 * time.Millisecond)
	final := eng.GetBaseTPS()
	if final < 150 {
		t.Fatalf("final TPS = %v, want close to To=200", final)
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

// freshScenarioMetrics returns a Metrics bound to a private registry so
// duplicate-registration panics cannot occur across test cases.
func freshScenarioMetrics(t *testing.T) *health.Metrics {
	t.Helper()
	return health.NewMetricsWithRegistry(prometheus.NewRegistry())
}

// gaugeValue reads a Prometheus gauge without the HTTP scrape path.
func gaugeValue(t *testing.T, g prometheus.Gauge) float64 {
	t.Helper()
	var m dto.Metric
	if err := g.Write(&m); err != nil {
		t.Fatalf("gauge Write: %v", err)
	}
	return m.GetGauge().GetValue()
}

// counterVecValue reads a single label-set from a CounterVec.
func counterVecValue(t *testing.T, cv *prometheus.CounterVec, from, to string) float64 {
	t.Helper()
	c, err := cv.GetMetricWithLabelValues(from, to)
	if err != nil {
		t.Fatalf("GetMetricWithLabelValues(%q,%q): %v", from, to, err)
	}
	var m dto.Metric
	if err := c.Write(&m); err != nil {
		t.Fatalf("counter Write: %v", err)
	}
	return m.GetCounter().GetValue()
}

// TestScenarioRunner_MetricsCounterAdvances3TimesFor3Phases verifies that
// kar98k_scenario_phase_transitions_total is incremented once per applyPhase
// call — including the initial entry into phase 0 — so a 3-phase timeline
// produces a total count of 3.
func TestScenarioRunner_MetricsCounterAdvances3TimesFor3Phases(t *testing.T) {
	eng := pattern.NewEngine(config.Pattern{}, 100, 1000)
	scenarios := []config.Scenario{
		{Name: "alpha", Duration: 60 * time.Millisecond, BaseTPS: 10},
		{Name: "beta", Duration: 60 * time.Millisecond, BaseTPS: 20},
		{Name: "gamma", Duration: 60 * time.Millisecond, BaseTPS: 30},
	}
	m := freshScenarioMetrics(t)
	runner := NewScenarioRunner(scenarios, eng, 100, 1000, config.Pattern{})
	runner.SetMetrics(m)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	runner.Run(ctx) // blocks until timeline complete

	// Verify each labelled transition was recorded exactly once.
	if got := counterVecValue(t, m.ScenarioPhaseTransitionsTotal, "", "alpha"); got != 1 {
		t.Fatalf(`transitions{"","alpha"} = %v, want 1`, got)
	}
	if got := counterVecValue(t, m.ScenarioPhaseTransitionsTotal, "alpha", "beta"); got != 1 {
		t.Fatalf(`transitions{"alpha","beta"} = %v, want 1`, got)
	}
	if got := counterVecValue(t, m.ScenarioPhaseTransitionsTotal, "beta", "gamma"); got != 1 {
		t.Fatalf(`transitions{"beta","gamma"} = %v, want 1`, got)
	}
}

// TestScenarioRunner_MetricsGaugeMidRunAndZeroAfter verifies that
// kar98k_scenario_phase_index equals idx+1 while a phase is active and
// falls back to 0 once the timeline completes (markStopped resets it).
func TestScenarioRunner_MetricsGaugeMidRunAndZeroAfter(t *testing.T) {
	eng := pattern.NewEngine(config.Pattern{}, 100, 1000)
	pause := 80 * time.Millisecond
	scenarios := []config.Scenario{
		{Name: "p1", Duration: pause, BaseTPS: 10},
		{Name: "p2", Duration: pause, BaseTPS: 20},
	}
	m := freshScenarioMetrics(t)
	runner := NewScenarioRunner(scenarios, eng, 100, 1000, config.Pattern{})
	runner.SetMetrics(m)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() { runner.Run(ctx); close(done) }()

	// Mid-run: gauge should be 1 shortly after phase 1 starts.
	time.Sleep(pause / 4)
	if got := gaugeValue(t, m.ScenarioPhaseIndex); got != 1 {
		t.Fatalf("mid phase 1: ScenarioPhaseIndex = %v, want 1", got)
	}

	// Mid-run: wait for phase 2 to start; gauge should be 2.
	time.Sleep(pause)
	if got := gaugeValue(t, m.ScenarioPhaseIndex); got != 2 {
		t.Fatalf("mid phase 2: ScenarioPhaseIndex = %v, want 2", got)
	}

	// After timeline: gauge should be reset to 0 by markStopped.
	<-done
	if got := gaugeValue(t, m.ScenarioPhaseIndex); got != 0 {
		t.Fatalf("post-timeline: ScenarioPhaseIndex = %v, want 0", got)
	}
}
