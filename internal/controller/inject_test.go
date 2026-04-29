package controller

import (
	"math"
	"testing"
	"time"

	"github.com/kar98k/internal/config"
)

// TestInjectTPSAt_NothingFor checks the no-op step returns zero TPS
// across its window. The runner clamps engine TPS to 1, so callers
// see the floor — but the evaluator itself stays honest.
func TestInjectTPSAt_NothingFor(t *testing.T) {
	steps := []config.InjectStep{
		{Type: config.InjectNothingFor, Duration: 30 * time.Second},
	}
	for _, at := range []time.Duration{0, 10 * time.Second, 30 * time.Second} {
		if got := injectTPSAt(steps, at); got != 0 {
			t.Fatalf("nothing_for at %s = %v, want 0", at, got)
		}
	}
}

// TestInjectTPSAt_ConstantHoldsValue verifies constant_tps returns
// the same TPS at every probe within the step.
func TestInjectTPSAt_ConstantHoldsValue(t *testing.T) {
	steps := []config.InjectStep{
		{Type: config.InjectConstantTPS, Duration: time.Minute, TPS: 200},
	}
	for _, at := range []time.Duration{0, 30 * time.Second, time.Minute} {
		if got := injectTPSAt(steps, at); got != 200 {
			t.Fatalf("constant_tps at %s = %v, want 200", at, got)
		}
	}
}

// TestInjectTPSAt_RampLinear is the canonical case: ramp 0→100 over
// 10s should land at 50 at the midpoint and on the endpoints exactly.
func TestInjectTPSAt_RampLinear(t *testing.T) {
	steps := []config.InjectStep{
		{Type: config.InjectRampTPS, Duration: 10 * time.Second, From: 0, To: 100},
	}
	cases := []struct {
		at   time.Duration
		want float64
	}{
		{0, 0},
		{2500 * time.Millisecond, 25},
		{5 * time.Second, 50},
		{10 * time.Second, 100},
	}
	for _, c := range cases {
		if got := injectTPSAt(steps, c.at); math.Abs(got-c.want) > 1e-9 {
			t.Fatalf("ramp at %s = %v, want %v", c.at, got, c.want)
		}
	}
}

// TestInjectTPSAt_HeavisideSCurve validates the sigmoid shape: it
// starts near From, ends near To, and is exactly halfway at the
// midpoint. The thresholds are loose because the curve is asymptotic
// — what we really care about is monotonic + symmetric + midpoint.
func TestInjectTPSAt_HeavisideSCurve(t *testing.T) {
	steps := []config.InjectStep{
		{Type: config.InjectHeavisideTPS, Duration: 10 * time.Second, From: 0, To: 100},
	}

	mid := injectTPSAt(steps, 5*time.Second)
	if math.Abs(mid-50) > 0.5 {
		t.Fatalf("heaviside midpoint = %v, want ~50", mid)
	}
	start := injectTPSAt(steps, 0)
	end := injectTPSAt(steps, 10*time.Second)
	if start > 0.1 || start < 0 {
		t.Fatalf("heaviside at t=0 = %v, want close to From=0", start)
	}
	if end < 99.9 {
		t.Fatalf("heaviside at t=duration = %v, want close to To=100", end)
	}

	// Monotonic increase
	prev := -1.0
	for i := 0; i <= 10; i++ {
		v := injectTPSAt(steps, time.Duration(i)*time.Second)
		if v < prev {
			t.Fatalf("heaviside not monotonic: i=%d v=%v prev=%v", i, v, prev)
		}
		prev = v
	}
}

// TestInjectTPSAt_StepSequenceAccumulates ensures the curve walker
// picks the right segment when steps are concatenated. The compound
// shape is "warm idle → ramp up → flat hold → ramp down".
func TestInjectTPSAt_StepSequenceAccumulates(t *testing.T) {
	steps := []config.InjectStep{
		{Type: config.InjectNothingFor, Duration: 5 * time.Second},
		{Type: config.InjectRampTPS, Duration: 10 * time.Second, From: 0, To: 100},
		{Type: config.InjectConstantTPS, Duration: 10 * time.Second, TPS: 100},
		{Type: config.InjectRampTPS, Duration: 5 * time.Second, From: 100, To: 0},
	}

	cases := []struct {
		at   time.Duration
		want float64
	}{
		{0, 0},                      // inside nothing_for
		{4 * time.Second, 0},        // still nothing_for
		{10 * time.Second, 50},      // halfway through ramp up
		{15 * time.Second, 100},     // ramp endpoint
		{20 * time.Second, 100},     // mid constant
		{25 * time.Second, 100},     // constant endpoint
		{27500 * time.Millisecond, 50}, // halfway through ramp down
		{30 * time.Second, 0},       // ramp down endpoint
	}
	for _, c := range cases {
		got := injectTPSAt(steps, c.at)
		if math.Abs(got-c.want) > 1e-6 {
			t.Fatalf("compound at %s = %v, want %v", c.at, got, c.want)
		}
	}
}

// TestInjectTPSAt_PastEndReturnsLastStepTerminal is a safety check —
// even though validation should make this unreachable, the evaluator
// must not panic if a caller probes past the curve.
func TestInjectTPSAt_PastEndReturnsLastStepTerminal(t *testing.T) {
	steps := []config.InjectStep{
		{Type: config.InjectRampTPS, Duration: 5 * time.Second, From: 0, To: 50},
	}
	if got := injectTPSAt(steps, time.Minute); math.Abs(got-50) > 1e-9 {
		t.Fatalf("post-curve probe = %v, want 50", got)
	}
}
