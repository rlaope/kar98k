package script

import (
	"fmt"
	"time"

	"github.com/dop251/goja"
	"github.com/kar98k/internal/config"
	"github.com/kar98k/internal/controller"
)

// registerInjection registers inject DSL builtins on the JS runtime.
func (r *JSRunner) registerInjection() {
	r.vm.Set("nothing_for", func(call goja.FunctionCall) goja.Value {
		dur := mustParseDurationJS("nothing_for", call.Argument(0).String(), r.vm)
		step := config.InjectStep{Type: config.InjectNothingFor, Duration: dur}
		r.scenario.Inject = append(r.scenario.Inject, step)
		return jsInjectStepToObject(step, r.vm)
	})

	r.vm.Set("constant_tps", func(call goja.FunctionCall) goja.Value {
		tps := call.Argument(0).ToFloat()
		if tps < 0 {
			panic(r.vm.NewGoError(fmt.Errorf("constant_tps: tps must be non-negative, got %v", tps)))
		}
		dur := mustParseDurationJS("constant_tps", call.Argument(1).String(), r.vm)
		step := config.InjectStep{Type: config.InjectConstantTPS, Duration: dur, TPS: tps}
		r.scenario.Inject = append(r.scenario.Inject, step)
		return jsInjectStepToObject(step, r.vm)
	})

	r.vm.Set("ramp_tps", func(call goja.FunctionCall) goja.Value {
		from := call.Argument(0).ToFloat()
		to := call.Argument(1).ToFloat()
		if from < 0 {
			panic(r.vm.NewGoError(fmt.Errorf("ramp_tps: from must be non-negative, got %v", from)))
		}
		if to < 0 {
			panic(r.vm.NewGoError(fmt.Errorf("ramp_tps: to must be non-negative, got %v", to)))
		}
		dur := mustParseDurationJS("ramp_tps", call.Argument(2).String(), r.vm)
		step := config.InjectStep{Type: config.InjectRampTPS, Duration: dur, From: from, To: to}
		r.scenario.Inject = append(r.scenario.Inject, step)
		return jsInjectStepToObject(step, r.vm)
	})

	r.vm.Set("heaviside_tps", func(call goja.FunctionCall) goja.Value {
		from := call.Argument(0).ToFloat()
		to := call.Argument(1).ToFloat()
		if from < 0 {
			panic(r.vm.NewGoError(fmt.Errorf("heaviside_tps: from must be non-negative, got %v", from)))
		}
		if to < 0 {
			panic(r.vm.NewGoError(fmt.Errorf("heaviside_tps: to must be non-negative, got %v", to)))
		}
		dur := mustParseDurationJS("heaviside_tps", call.Argument(2).String(), r.vm)
		var steepness float64
		if len(call.Arguments) > 3 && call.Argument(3) != goja.Undefined() {
			steepness = call.Argument(3).ToFloat()
		}
		step := config.InjectStep{Type: config.InjectHeavisideTPS, Duration: dur, From: from, To: to, Steepness: steepness}
		r.scenario.Inject = append(r.scenario.Inject, step)
		return jsInjectStepToObject(step, r.vm)
	})

	r.vm.Set("inject_evaluate", func(call goja.FunctionCall) goja.Value {
		stepsVal := call.Argument(0)
		offsetStr := call.Argument(1).String()

		steps, err := parseJSInject(stepsVal, r.vm)
		if err != nil {
			panic(r.vm.NewGoError(fmt.Errorf("inject_evaluate: %w", err)))
		}

		offset, err := time.ParseDuration(offsetStr)
		if err != nil {
			panic(r.vm.NewGoError(fmt.Errorf("inject_evaluate: invalid offset %q: %w", offsetStr, err)))
		}

		if len(call.Arguments) > 2 && call.Argument(2) != goja.Undefined() {
			pd, err := time.ParseDuration(call.Argument(2).String())
			if err != nil {
				panic(r.vm.NewGoError(fmt.Errorf("inject_evaluate: invalid phase_duration: %w", err)))
			}
			var total time.Duration
			for _, s := range steps {
				total += s.Duration
			}
			if total != pd {
				panic(r.vm.NewGoError(fmt.Errorf("inject_evaluate: step durations sum to %s but phase_duration is %s — they must match", total, pd)))
			}
		}

		tps := controller.InjectTPSAt(steps, offset)
		return r.vm.ToValue(tps)
	})

	// injection.* — camelCase namespace required by #65. Delegates to the
	// same underlying logic as the snake_case globals above so there is no
	// double-counting when scripts use one style exclusively.
	inj := r.vm.NewObject()

	inj.Set("nothingFor", func(call goja.FunctionCall) goja.Value {
		dur := mustParseDurationJS("injection.nothingFor", call.Argument(0).String(), r.vm)
		step := config.InjectStep{Type: config.InjectNothingFor, Duration: dur}
		return jsInjectStepToObject(step, r.vm)
	})

	inj.Set("constantTps", func(call goja.FunctionCall) goja.Value {
		tps := call.Argument(0).ToFloat()
		if tps < 0 {
			panic(r.vm.NewGoError(fmt.Errorf("constant_tps requires tps >= 0")))
		}
		dur := mustParseDurationJS("injection.constantTps", call.Argument(1).String(), r.vm)
		step := config.InjectStep{Type: config.InjectConstantTPS, Duration: dur, TPS: tps}
		return jsInjectStepToObject(step, r.vm)
	})

	inj.Set("rampTps", func(call goja.FunctionCall) goja.Value {
		from := call.Argument(0).ToFloat()
		to := call.Argument(1).ToFloat()
		if from < 0 || to < 0 {
			panic(r.vm.NewGoError(fmt.Errorf("ramp_tps requires from/to >= 0")))
		}
		dur := mustParseDurationJS("injection.rampTps", call.Argument(2).String(), r.vm)
		step := config.InjectStep{Type: config.InjectRampTPS, Duration: dur, From: from, To: to}
		return jsInjectStepToObject(step, r.vm)
	})

	inj.Set("heavisideTps", func(call goja.FunctionCall) goja.Value {
		from := call.Argument(0).ToFloat()
		to := call.Argument(1).ToFloat()
		if from < 0 || to < 0 {
			panic(r.vm.NewGoError(fmt.Errorf("heaviside_tps requires from/to >= 0")))
		}
		dur := mustParseDurationJS("injection.heavisideTps", call.Argument(2).String(), r.vm)
		var steepness float64
		if len(call.Arguments) > 3 && call.Argument(3) != goja.Undefined() {
			opts := call.Argument(3).ToObject(r.vm)
			if v := opts.Get("steepness"); v != nil && v != goja.Undefined() {
				steepness = v.ToFloat()
			}
		}
		step := config.InjectStep{Type: config.InjectHeavisideTPS, Duration: dur, From: from, To: to, Steepness: steepness}
		return jsInjectStepToObject(step, r.vm)
	})

	// injection.evaluate(steps, "1m30s") — shared evaluator, parity with Starlark side.
	inj.Set("evaluate", func(call goja.FunctionCall) goja.Value {
		steps, err := parseJSInject(call.Argument(0), r.vm)
		if err != nil {
			panic(r.vm.NewGoError(fmt.Errorf("injection.evaluate: %w", err)))
		}
		at, err := time.ParseDuration(call.Argument(1).String())
		if err != nil {
			panic(r.vm.NewGoError(fmt.Errorf("injection.evaluate: invalid duration %q: %w", call.Argument(1).String(), err)))
		}
		return r.vm.ToValue(controller.InjectTPSAt(steps, at))
	})

	r.vm.Set("injection", inj)
}

// parseJSInject converts a JS array of inject step objects to []config.InjectStep.
func parseJSInject(val goja.Value, vm *goja.Runtime) ([]config.InjectStep, error) {
	obj := val.ToObject(vm)
	length := obj.Get("length")
	if length == nil || length == goja.Undefined() {
		return nil, fmt.Errorf("inject must be an array")
	}
	n := int(length.ToInteger())
	steps := make([]config.InjectStep, 0, n)
	for i := 0; i < n; i++ {
		item := obj.Get(fmt.Sprintf("%d", i)).ToObject(vm)
		step, err := jsObjectToInjectStep(item, vm)
		if err != nil {
			return nil, fmt.Errorf("step %d: %w", i, err)
		}
		steps = append(steps, step)
	}
	return steps, nil
}

// jsObjectToInjectStep reads an inject step object created by the JS builtins.
func jsObjectToInjectStep(obj *goja.Object, vm *goja.Runtime) (config.InjectStep, error) {
	var step config.InjectStep

	if t := obj.Get("type"); t != nil && t != goja.Undefined() {
		step.Type = config.InjectStepType(t.String())
	}

	if d := obj.Get("duration"); d != nil && d != goja.Undefined() {
		dur, err := time.ParseDuration(d.String())
		if err != nil {
			return step, fmt.Errorf("duration %q: %w", d.String(), err)
		}
		step.Duration = dur
	}

	if v := obj.Get("tps"); v != nil && v != goja.Undefined() {
		step.TPS = v.ToFloat()
	}
	if v := obj.Get("from"); v != nil && v != goja.Undefined() {
		step.From = v.ToFloat()
	}
	if v := obj.Get("to"); v != nil && v != goja.Undefined() {
		step.To = v.ToFloat()
	}
	if v := obj.Get("steepness"); v != nil && v != goja.Undefined() {
		step.Steepness = v.ToFloat()
	}

	return step, nil
}

// jsInjectStepToObject converts an InjectStep to a goja object for JS script inspection.
func jsInjectStepToObject(step config.InjectStep, vm *goja.Runtime) goja.Value {
	obj := vm.NewObject()
	obj.Set("type", string(step.Type))
	obj.Set("duration", step.Duration.String())
	obj.Set("tps", step.TPS)
	obj.Set("from", step.From)
	obj.Set("to", step.To)
	obj.Set("steepness", step.Steepness)
	return obj
}

// mustParseDurationJS parses a duration string, panicking with a JS error on failure.
func mustParseDurationJS(fn, s string, vm *goja.Runtime) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		panic(vm.NewGoError(fmt.Errorf("%s: invalid duration %q: %w", fn, s, err)))
	}
	if d <= 0 {
		panic(vm.NewGoError(fmt.Errorf("%s: duration must be positive, got %q", fn, s)))
	}
	return d
}
