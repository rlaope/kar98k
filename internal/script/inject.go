package script

import (
	"fmt"
	"time"

	"github.com/kar98k/internal/config"
	"github.com/kar98k/internal/controller"
	"go.starlark.net/starlark"
)

// nothingForBuiltin implements nothing_for(duration).
func nothingForBuiltin(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	rt := thread.Local("runtime").(*Runtime)

	var durationStr starlark.String
	if err := starlark.UnpackArgs("nothing_for", args, kwargs,
		"duration", &durationStr,
	); err != nil {
		return nil, err
	}

	d, err := parseDuration("nothing_for", string(durationStr))
	if err != nil {
		return nil, err
	}

	step := config.InjectStep{
		Type:     config.InjectNothingFor,
		Duration: d,
	}
	rt.scenario.Inject = append(rt.scenario.Inject, step)

	return injectStepToDict(step), nil
}

// constantTPSBuiltin implements constant_tps(tps, duration).
func constantTPSBuiltin(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	rt := thread.Local("runtime").(*Runtime)

	var tps starlark.Float
	var durationStr starlark.String
	if err := starlark.UnpackArgs("constant_tps", args, kwargs,
		"tps", &tps,
		"duration", &durationStr,
	); err != nil {
		return nil, err
	}

	if float64(tps) < 0 {
		return nil, fmt.Errorf("constant_tps: tps must be non-negative, got %v", float64(tps))
	}

	d, err := parseDuration("constant_tps", string(durationStr))
	if err != nil {
		return nil, err
	}

	step := config.InjectStep{
		Type:     config.InjectConstantTPS,
		Duration: d,
		TPS:      float64(tps),
	}
	rt.scenario.Inject = append(rt.scenario.Inject, step)

	return injectStepToDict(step), nil
}

// rampTPSBuiltin implements ramp_tps(from, to, duration).
func rampTPSBuiltin(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	rt := thread.Local("runtime").(*Runtime)

	var from, to starlark.Float
	var durationStr starlark.String
	if err := starlark.UnpackArgs("ramp_tps", args, kwargs,
		"from", &from,
		"to", &to,
		"duration", &durationStr,
	); err != nil {
		return nil, err
	}

	if float64(from) < 0 {
		return nil, fmt.Errorf("ramp_tps: from must be non-negative, got %v", float64(from))
	}
	if float64(to) < 0 {
		return nil, fmt.Errorf("ramp_tps: to must be non-negative, got %v", float64(to))
	}

	d, err := parseDuration("ramp_tps", string(durationStr))
	if err != nil {
		return nil, err
	}

	step := config.InjectStep{
		Type:     config.InjectRampTPS,
		Duration: d,
		From:     float64(from),
		To:       float64(to),
	}
	rt.scenario.Inject = append(rt.scenario.Inject, step)

	return injectStepToDict(step), nil
}

// heavisideTPSBuiltin implements heaviside_tps(from, to, duration, steepness=...).
// Why: steepness 0 means "use default 6" — encoded by leaving Steepness=0 so
// InjectTPSAt's branch handles it rather than duplicating the constant here.
func heavisideTPSBuiltin(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	rt := thread.Local("runtime").(*Runtime)

	var from, to starlark.Float
	var durationStr starlark.String
	var steepness starlark.Float

	if err := starlark.UnpackArgs("heaviside_tps", args, kwargs,
		"from", &from,
		"to", &to,
		"duration", &durationStr,
		"steepness?", &steepness,
	); err != nil {
		return nil, err
	}

	if float64(from) < 0 {
		return nil, fmt.Errorf("heaviside_tps: from must be non-negative, got %v", float64(from))
	}
	if float64(to) < 0 {
		return nil, fmt.Errorf("heaviside_tps: to must be non-negative, got %v", float64(to))
	}

	d, err := parseDuration("heaviside_tps", string(durationStr))
	if err != nil {
		return nil, err
	}

	step := config.InjectStep{
		Type:      config.InjectHeavisideTPS,
		Duration:  d,
		From:      float64(from),
		To:        float64(to),
		Steepness: float64(steepness),
	}
	rt.scenario.Inject = append(rt.scenario.Inject, step)

	return injectStepToDict(step), nil
}

// injectEvaluateBuiltin implements inject_evaluate(steps, offset).
// It wraps controller.InjectTPSAt so the math is shared with the YAML path.
// Optionally validates that step durations sum to the phase duration when a
// third argument phase_duration is provided.
func injectEvaluateBuiltin(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var stepsList starlark.Value
	var offsetStr starlark.String
	var phaseDurationStr starlark.String

	if err := starlark.UnpackArgs("inject_evaluate", args, kwargs,
		"steps", &stepsList,
		"offset", &offsetStr,
		"phase_duration?", &phaseDurationStr,
	); err != nil {
		return nil, err
	}

	list, ok := stepsList.(*starlark.List)
	if !ok {
		return nil, fmt.Errorf("inject_evaluate: steps must be a list, got %s", stepsList.Type())
	}

	steps, err := starlarkListToInjectSteps(list)
	if err != nil {
		return nil, fmt.Errorf("inject_evaluate: %w", err)
	}

	offset, err := parseDuration("inject_evaluate offset", string(offsetStr))
	if err != nil {
		return nil, err
	}

	if string(phaseDurationStr) != "" {
		pd, err := parseDuration("inject_evaluate phase_duration", string(phaseDurationStr))
		if err != nil {
			return nil, err
		}
		var total time.Duration
		for _, s := range steps {
			total += s.Duration
		}
		if total != pd {
			return nil, fmt.Errorf("inject_evaluate: step durations sum to %s but phase_duration is %s — they must match", total, pd)
		}
	}

	tps := controller.InjectTPSAt(steps, offset)
	return starlark.Float(tps), nil
}

// parseDuration wraps time.ParseDuration with a friendlier error prefix and
// rejects non-positive durations.
func parseDuration(fn, s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("%s: invalid duration %q: %w", fn, s, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%s: duration must be positive, got %q", fn, s)
	}
	return d, nil
}

// injectStepToDict converts an InjectStep to a Starlark dict for script inspection.
func injectStepToDict(step config.InjectStep) *starlark.Dict {
	d := starlark.NewDict(6)
	d.SetKey(starlark.String("type"), starlark.String(string(step.Type)))
	d.SetKey(starlark.String("duration"), starlark.String(step.Duration.String()))
	d.SetKey(starlark.String("tps"), starlark.Float(step.TPS))
	d.SetKey(starlark.String("from"), starlark.Float(step.From))
	d.SetKey(starlark.String("to"), starlark.Float(step.To))
	d.SetKey(starlark.String("steepness"), starlark.Float(step.Steepness))
	return d
}

// starlarkListToInjectSteps converts a Starlark list of step dicts to []config.InjectStep.
func starlarkListToInjectSteps(list *starlark.List) ([]config.InjectStep, error) {
	steps := make([]config.InjectStep, 0, list.Len())
	for i := 0; i < list.Len(); i++ {
		item := list.Index(i)
		dict, ok := item.(*starlark.Dict)
		if !ok {
			return nil, fmt.Errorf("step %d must be a dict, got %s", i, item.Type())
		}
		step, err := injectDictToStep(dict)
		if err != nil {
			return nil, fmt.Errorf("step %d: %w", i, err)
		}
		steps = append(steps, step)
	}
	return steps, nil
}

// injectDictToStep converts the dict returned by inject primitives back to InjectStep.
func injectDictToStep(dict *starlark.Dict) (config.InjectStep, error) {
	var step config.InjectStep

	if v, found, _ := dict.Get(starlark.String("type")); found {
		s, _ := starlark.AsString(v)
		step.Type = config.InjectStepType(s)
	}

	if v, found, _ := dict.Get(starlark.String("duration")); found {
		s, _ := starlark.AsString(v)
		d, err := time.ParseDuration(s)
		if err != nil {
			return step, fmt.Errorf("duration %q: %w", s, err)
		}
		step.Duration = d
	}

	if v, found, _ := dict.Get(starlark.String("tps")); found {
		if f, ok := v.(starlark.Float); ok {
			step.TPS = float64(f)
		}
	}

	if v, found, _ := dict.Get(starlark.String("from")); found {
		if f, ok := v.(starlark.Float); ok {
			step.From = float64(f)
		}
	}

	if v, found, _ := dict.Get(starlark.String("to")); found {
		if f, ok := v.(starlark.Float); ok {
			step.To = float64(f)
		}
	}

	if v, found, _ := dict.Get(starlark.String("steepness")); found {
		if f, ok := v.(starlark.Float); ok {
			step.Steepness = float64(f)
		}
	}

	return step, nil
}
