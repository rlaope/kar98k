package script

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"go.starlark.net/starlark"
)

// Runtime holds the Starlark execution context.
type Runtime struct {
	scenario   ScenarioConfig
	metrics    *Metrics
	httpClient *http.Client

	// Starlark state
	thread  *starlark.Thread
	globals starlark.StringDict

	// User-defined functions
	setupFn    starlark.Callable
	defaultFn  starlark.Callable
	teardownFn starlark.Callable
}

// StarlarkRunner implements Runner for Starlark (.star) scripts.
type StarlarkRunner struct {
	rt *Runtime
}

func NewStarlarkRunner() *StarlarkRunner {
	rt := &Runtime{
		scenario: ScenarioConfig{
			Chaos: chaosPresets["moderate"],
		},
		metrics: newMetrics(),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 100,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
	return &StarlarkRunner{rt: rt}
}

func (r *StarlarkRunner) Load(path string) error {
	rt := r.rt

	rt.thread = &starlark.Thread{
		Name: "kar98k",
		Print: func(thread *starlark.Thread, msg string) {
			fmt.Println(msg)
		},
	}
	rt.thread.SetLocal("runtime", rt)

	builtins := starlark.StringDict{
		"scenario":        starlark.NewBuiltin("scenario", scenarioBuiltin),
		"chaos":           starlark.NewBuiltin("chaos", chaosBuiltin),
		"ramp":            starlark.NewBuiltin("ramp", rampBuiltin),
		"stage":           starlark.NewBuiltin("stage", stageBuiltin),
		"check":           starlark.NewBuiltin("check", checkBuiltin),
		"sleep":           starlark.NewBuiltin("sleep", sleepBuiltin),
		"think_time":      starlark.NewBuiltin("think_time", thinkTimeBuiltin),
		"group":           starlark.NewBuiltin("group", groupBuiltin),
		"phase":           starlark.NewBuiltin("phase", phaseBuiltinStarlark),
		"http":            httpModule(rt),
		"nothing_for":     starlark.NewBuiltin("nothing_for", nothingForBuiltin),
		"constant_tps":    starlark.NewBuiltin("constant_tps", constantTPSBuiltin),
		"ramp_tps":        starlark.NewBuiltin("ramp_tps", rampTPSBuiltin),
		"heaviside_tps":   starlark.NewBuiltin("heaviside_tps", heavisideTPSBuiltin),
		"inject_evaluate": starlark.NewBuiltin("inject_evaluate", injectEvaluateBuiltin),
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading script: %w", err)
	}

	globals, err := starlark.ExecFile(rt.thread, path, data, builtins)
	if err != nil {
		return fmt.Errorf("executing script: %w", err)
	}

	rt.globals = globals

	// Extract lifecycle functions
	if fn, ok := globals["setup"]; ok {
		if callable, ok := fn.(starlark.Callable); ok {
			rt.setupFn = callable
		}
	}
	if fn, ok := globals["default"]; ok {
		if callable, ok := fn.(starlark.Callable); ok {
			rt.defaultFn = callable
		}
	}
	if fn, ok := globals["teardown"]; ok {
		if callable, ok := fn.(starlark.Callable); ok {
			rt.teardownFn = callable
		}
	}

	if rt.defaultFn == nil {
		return fmt.Errorf("script must define a default() function")
	}

	return nil
}

func (r *StarlarkRunner) Setup() (interface{}, error) {
	if r.rt.setupFn == nil {
		return starlark.None, nil
	}

	result, err := starlark.Call(r.rt.thread, r.rt.setupFn, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("setup(): %w", err)
	}
	return result, nil
}

func (r *StarlarkRunner) Iterate(vuID int, data interface{}) error {
	starlarkData, ok := data.(starlark.Value)
	if !ok {
		starlarkData = starlark.None
	}

	// Each VU iteration needs its own thread (Starlark is not goroutine-safe)
	thread := &starlark.Thread{
		Name: fmt.Sprintf("vu-%d", vuID),
		Print: func(thread *starlark.Thread, msg string) {
			fmt.Printf("[VU %d] %s\n", vuID, msg)
		},
	}
	thread.SetLocal("runtime", r.rt)

	_, err := starlark.Call(thread, r.rt.defaultFn, starlark.Tuple{starlarkData}, nil)
	if err != nil {
		return fmt.Errorf("default() VU %d: %w", vuID, err)
	}
	return nil
}

func (r *StarlarkRunner) Teardown(data interface{}) error {
	if r.rt.teardownFn == nil {
		return nil
	}

	starlarkData, ok := data.(starlark.Value)
	if !ok {
		starlarkData = starlark.None
	}

	_, err := starlark.Call(r.rt.thread, r.rt.teardownFn, starlark.Tuple{starlarkData}, nil)
	if err != nil {
		return fmt.Errorf("teardown(): %w", err)
	}
	return nil
}

func (r *StarlarkRunner) Scenario() *ScenarioConfig { return &r.rt.scenario }
func (r *StarlarkRunner) Metrics() *Metrics          { return r.rt.metrics }
func (r *StarlarkRunner) Close() error               { return nil }
