package script

import (
	"testing"
	"time"

	"github.com/kar98k/internal/config"
	"github.com/kar98k/internal/controller"
	"go.starlark.net/starlark"
)

// newTestRuntime creates a Runtime wired to a Starlark thread for builtin tests.
func newTestRuntime() (*Runtime, *starlark.Thread) {
	rt := &Runtime{
		scenario: ScenarioConfig{
			Chaos: chaosPresets["moderate"],
		},
		metrics: newMetrics(),
	}
	thread := &starlark.Thread{Name: "test"}
	thread.SetLocal("runtime", rt)
	return rt, thread
}

// callBuiltin calls a builtin function with the given thread, args, and kwargs.
func callBuiltin(thread *starlark.Thread, fn starlark.Callable, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	return starlark.Call(thread, fn, args, kwargs)
}

func TestNothingFor(t *testing.T) {
	rt, thread := newTestRuntime()
	b := starlark.NewBuiltin("nothing_for", nothingForBuiltin)

	val, err := callBuiltin(thread, b, starlark.Tuple{starlark.String("30s")}, nil)
	if err != nil {
		t.Fatalf("nothing_for: %v", err)
	}

	dict, ok := val.(*starlark.Dict)
	if !ok {
		t.Fatalf("expected dict, got %T", val)
	}

	v, _, _ := dict.Get(starlark.String("type"))
	if s, _ := starlark.AsString(v); s != string(config.InjectNothingFor) {
		t.Errorf("type = %q, want %q", s, config.InjectNothingFor)
	}

	v, _, _ = dict.Get(starlark.String("duration"))
	if s, _ := starlark.AsString(v); s != "30s" {
		t.Errorf("duration = %q, want %q", s, "30s")
	}

	if len(rt.scenario.Inject) != 1 {
		t.Fatalf("Inject len = %d, want 1", len(rt.scenario.Inject))
	}
	if rt.scenario.Inject[0].Type != config.InjectNothingFor {
		t.Errorf("Inject[0].Type = %q, want %q", rt.scenario.Inject[0].Type, config.InjectNothingFor)
	}
	if rt.scenario.Inject[0].Duration != 30*time.Second {
		t.Errorf("Inject[0].Duration = %v, want 30s", rt.scenario.Inject[0].Duration)
	}
}

func TestConstantTPS(t *testing.T) {
	rt, thread := newTestRuntime()
	b := starlark.NewBuiltin("constant_tps", constantTPSBuiltin)

	val, err := callBuiltin(thread, b, starlark.Tuple{starlark.Float(100), starlark.String("10m")}, nil)
	if err != nil {
		t.Fatalf("constant_tps: %v", err)
	}

	dict := val.(*starlark.Dict)
	v, _, _ := dict.Get(starlark.String("type"))
	if s, _ := starlark.AsString(v); s != string(config.InjectConstantTPS) {
		t.Errorf("type = %q, want %q", s, config.InjectConstantTPS)
	}
	v, _, _ = dict.Get(starlark.String("tps"))
	if f, ok := v.(starlark.Float); !ok || float64(f) != 100 {
		t.Errorf("tps = %v, want 100", v)
	}

	if len(rt.scenario.Inject) != 1 || rt.scenario.Inject[0].TPS != 100 {
		t.Errorf("Inject[0].TPS = %v, want 100", rt.scenario.Inject[0].TPS)
	}
}

func TestRampTPS(t *testing.T) {
	rt, thread := newTestRuntime()
	b := starlark.NewBuiltin("ramp_tps", rampTPSBuiltin)

	val, err := callBuiltin(thread, b, starlark.Tuple{starlark.Float(0), starlark.Float(100), starlark.String("2m")}, nil)
	if err != nil {
		t.Fatalf("ramp_tps: %v", err)
	}

	dict := val.(*starlark.Dict)
	v, _, _ := dict.Get(starlark.String("type"))
	if s, _ := starlark.AsString(v); s != string(config.InjectRampTPS) {
		t.Errorf("type = %q, want %q", s, config.InjectRampTPS)
	}
	v, _, _ = dict.Get(starlark.String("from"))
	if f, ok := v.(starlark.Float); !ok || float64(f) != 0 {
		t.Errorf("from = %v, want 0", v)
	}
	v, _, _ = dict.Get(starlark.String("to"))
	if f, ok := v.(starlark.Float); !ok || float64(f) != 100 {
		t.Errorf("to = %v, want 100", v)
	}

	if len(rt.scenario.Inject) != 1 {
		t.Fatalf("Inject len = %d, want 1", len(rt.scenario.Inject))
	}
	step := rt.scenario.Inject[0]
	if step.From != 0 || step.To != 100 || step.Duration != 2*time.Minute {
		t.Errorf("step = %+v, want From=0 To=100 Duration=2m", step)
	}
}

func TestHeavisideTPS_DefaultSteepness(t *testing.T) {
	rt, thread := newTestRuntime()
	b := starlark.NewBuiltin("heaviside_tps", heavisideTPSBuiltin)

	_, err := callBuiltin(thread, b, starlark.Tuple{starlark.Float(100), starlark.Float(500), starlark.String("30s")}, nil)
	if err != nil {
		t.Fatalf("heaviside_tps: %v", err)
	}

	if len(rt.scenario.Inject) != 1 {
		t.Fatalf("Inject len = %d, want 1", len(rt.scenario.Inject))
	}
	step := rt.scenario.Inject[0]
	if step.Type != config.InjectHeavisideTPS {
		t.Errorf("Type = %q, want %q", step.Type, config.InjectHeavisideTPS)
	}
	// Why: steepness 0 means "use default 6" — InjectTPSAt handles it
	if step.Steepness != 0 {
		t.Errorf("Steepness = %v, want 0 (default sentinel)", step.Steepness)
	}
}

func TestHeavisideTPS_ExplicitSteepness(t *testing.T) {
	rt, thread := newTestRuntime()
	b := starlark.NewBuiltin("heaviside_tps", heavisideTPSBuiltin)

	_, err := callBuiltin(thread, b,
		starlark.Tuple{starlark.Float(0), starlark.Float(1000), starlark.String("10s")},
		[]starlark.Tuple{{starlark.String("steepness"), starlark.Float(10)}},
	)
	if err != nil {
		t.Fatalf("heaviside_tps with steepness: %v", err)
	}

	if rt.scenario.Inject[0].Steepness != 10 {
		t.Errorf("Steepness = %v, want 10", rt.scenario.Inject[0].Steepness)
	}
}

func TestInjectEvaluate_ConstantTPS(t *testing.T) {
	_, thread := newTestRuntime()

	steps := []config.InjectStep{
		{Type: config.InjectConstantTPS, Duration: 10 * time.Minute, TPS: 100},
	}
	list := injectStepsToStarlarkList(steps)

	b := starlark.NewBuiltin("inject_evaluate", injectEvaluateBuiltin)
	val, err := callBuiltin(thread, b, starlark.Tuple{list, starlark.String("5m")}, nil)
	if err != nil {
		t.Fatalf("inject_evaluate: %v", err)
	}

	got := float64(val.(starlark.Float))
	want := controller.InjectTPSAt(steps, 5*time.Minute)
	if got != want {
		t.Errorf("inject_evaluate = %v, want %v", got, want)
	}
}

func TestInjectEvaluate_MidRamp(t *testing.T) {
	_, thread := newTestRuntime()

	steps := []config.InjectStep{
		{Type: config.InjectRampTPS, Duration: 2 * time.Minute, From: 0, To: 100},
	}
	list := injectStepsToStarlarkList(steps)

	b := starlark.NewBuiltin("inject_evaluate", injectEvaluateBuiltin)
	val, err := callBuiltin(thread, b, starlark.Tuple{list, starlark.String("1m")}, nil)
	if err != nil {
		t.Fatalf("inject_evaluate mid-ramp: %v", err)
	}

	got := float64(val.(starlark.Float))
	want := controller.InjectTPSAt(steps, 1*time.Minute)
	if got != want {
		t.Errorf("inject_evaluate mid-ramp = %v, want %v", got, want)
	}
}

func TestInjectEvaluate_EndRamp(t *testing.T) {
	_, thread := newTestRuntime()

	steps := []config.InjectStep{
		{Type: config.InjectRampTPS, Duration: 2 * time.Minute, From: 0, To: 100},
	}
	list := injectStepsToStarlarkList(steps)

	b := starlark.NewBuiltin("inject_evaluate", injectEvaluateBuiltin)
	val, err := callBuiltin(thread, b, starlark.Tuple{list, starlark.String("2m")}, nil)
	if err != nil {
		t.Fatalf("inject_evaluate end-ramp: %v", err)
	}

	got := float64(val.(starlark.Float))
	want := controller.InjectTPSAt(steps, 2*time.Minute)
	if got != want {
		t.Errorf("inject_evaluate end-ramp = %v, want %v", got, want)
	}
}

func TestInjectEvaluate_PhaseDurationValidation(t *testing.T) {
	_, thread := newTestRuntime()

	steps := []config.InjectStep{
		{Type: config.InjectConstantTPS, Duration: 5 * time.Minute, TPS: 50},
	}
	list := injectStepsToStarlarkList(steps)

	b := starlark.NewBuiltin("inject_evaluate", injectEvaluateBuiltin)

	// Matching phase_duration — must succeed.
	_, err := callBuiltin(thread, b,
		starlark.Tuple{list, starlark.String("1m")},
		[]starlark.Tuple{{starlark.String("phase_duration"), starlark.String("5m")}},
	)
	if err != nil {
		t.Errorf("inject_evaluate with matching phase_duration: unexpected error: %v", err)
	}

	// Mismatched phase_duration — must fail.
	_, err = callBuiltin(thread, b,
		starlark.Tuple{list, starlark.String("1m")},
		[]starlark.Tuple{{starlark.String("phase_duration"), starlark.String("10m")}},
	)
	if err == nil {
		t.Error("inject_evaluate: expected error for mismatched phase_duration, got nil")
	}
}

func TestInjectBuiltin_InvalidArgs(t *testing.T) {
	_, thread := newTestRuntime()

	cases := []struct {
		name string
		fn   func(*starlark.Thread, *starlark.Builtin, starlark.Tuple, []starlark.Tuple) (starlark.Value, error)
		args starlark.Tuple
	}{
		{
			"nothing_for negative duration",
			nothingForBuiltin,
			starlark.Tuple{starlark.String("-1s")},
		},
		{
			"constant_tps negative tps",
			constantTPSBuiltin,
			starlark.Tuple{starlark.Float(-1), starlark.String("1m")},
		},
		{
			"ramp_tps negative from",
			rampTPSBuiltin,
			starlark.Tuple{starlark.Float(-10), starlark.Float(100), starlark.String("1m")},
		},
		{
			"heaviside_tps negative to",
			heavisideTPSBuiltin,
			starlark.Tuple{starlark.Float(0), starlark.Float(-100), starlark.String("1m")},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := starlark.NewBuiltin(tc.name, tc.fn)
			_, err := callBuiltin(thread, b, tc.args, nil)
			if err == nil {
				t.Errorf("%s: expected error for invalid args, got nil", tc.name)
			}
		})
	}
}

func TestScenarioInjectAccumulates(t *testing.T) {
	rt, thread := newTestRuntime()

	fns := []struct {
		b    *starlark.Builtin
		args starlark.Tuple
	}{
		{starlark.NewBuiltin("nothing_for", nothingForBuiltin), starlark.Tuple{starlark.String("30s")}},
		{starlark.NewBuiltin("ramp_tps", rampTPSBuiltin), starlark.Tuple{starlark.Float(0), starlark.Float(100), starlark.String("2m")}},
		{starlark.NewBuiltin("constant_tps", constantTPSBuiltin), starlark.Tuple{starlark.Float(100), starlark.String("10m")}},
		{starlark.NewBuiltin("heaviside_tps", heavisideTPSBuiltin), starlark.Tuple{starlark.Float(100), starlark.Float(500), starlark.String("30s")}},
	}

	for _, f := range fns {
		if _, err := callBuiltin(thread, f.b, f.args, nil); err != nil {
			t.Fatalf("%s: %v", f.b.Name(), err)
		}
	}

	if len(rt.scenario.Inject) != 4 {
		t.Errorf("Inject len = %d, want 4", len(rt.scenario.Inject))
	}

	want := []config.InjectStepType{
		config.InjectNothingFor,
		config.InjectRampTPS,
		config.InjectConstantTPS,
		config.InjectHeavisideTPS,
	}
	for i, wt := range want {
		if rt.scenario.Inject[i].Type != wt {
			t.Errorf("Inject[%d].Type = %q, want %q", i, rt.scenario.Inject[i].Type, wt)
		}
	}
}

// injectStepsToStarlarkList converts []config.InjectStep to a Starlark list of dicts,
// mirroring what the inject primitives produce so inject_evaluate can consume them.
func injectStepsToStarlarkList(steps []config.InjectStep) *starlark.List {
	vals := make([]starlark.Value, len(steps))
	for i, s := range steps {
		vals[i] = injectStepToDict(s)
	}
	return starlark.NewList(vals)
}
