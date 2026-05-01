package script

import (
	"fmt"
	"time"

	"github.com/kar98k/internal/config"
	"go.starlark.net/starlark"
)

// ScenarioConfig holds the parsed scenario configuration from a script.
type ScenarioConfig struct {
	Name       string
	Chaos      ChaosConfig
	Stages     []Stage
	Thresholds map[string]string
	VUs        int
	Duration   time.Duration
	Inject     []config.InjectStep
}

// ChaosConfig configures kar98k's chaos traffic patterns.
type ChaosConfig struct {
	Preset         string
	SpikeFactor    float64
	NoiseAmplitude float64
	Lambda         float64
	MinInterval    time.Duration
	MaxInterval    time.Duration
}

// Stage defines a VU ramping stage.
type Stage struct {
	Duration time.Duration
	Target   int
}

// Presets for chaos configuration.
var chaosPresets = map[string]ChaosConfig{
	"none": {
		Preset:         "none",
		SpikeFactor:    1.0,
		Lambda:         0,
		NoiseAmplitude: 0,
		MinInterval:    0,
		MaxInterval:    0,
	},
	"gentle": {
		Preset:         "gentle",
		SpikeFactor:    1.5,
		Lambda:         0.003,
		NoiseAmplitude: 0.05,
		MinInterval:    3 * time.Minute,
		MaxInterval:    10 * time.Minute,
	},
	"moderate": {
		Preset:         "moderate",
		SpikeFactor:    2.0,
		Lambda:         0.005,
		NoiseAmplitude: 0.10,
		MinInterval:    2 * time.Minute,
		MaxInterval:    8 * time.Minute,
	},
	"aggressive": {
		Preset:         "aggressive",
		SpikeFactor:    3.0,
		Lambda:         0.01,
		NoiseAmplitude: 0.15,
		MinInterval:    1 * time.Minute,
		MaxInterval:    5 * time.Minute,
	},
}

// scenarioBuiltin implements the scenario() function in Starlark.
func scenarioBuiltin(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	rt := thread.Local("runtime").(*Runtime)

	var name starlark.String
	var pattern starlark.Value = starlark.None
	var vus starlark.Value = starlark.None
	var thresholds starlark.Value = starlark.None

	if err := starlark.UnpackArgs("scenario", args, kwargs,
		"name", &name,
		"pattern?", &pattern,
		"vus?", &vus,
		"thresholds?", &thresholds,
	); err != nil {
		return nil, err
	}

	rt.scenario.Name = string(name)

	// Parse chaos config
	if dict, ok := pattern.(*starlark.Dict); ok {
		if err := parseChaosDict(dict, &rt.scenario.Chaos); err != nil {
			return nil, err
		}
	}

	// Parse stages from ramp()
	if list, ok := vus.(*starlark.List); ok {
		if err := parseStagesList(list, &rt.scenario.Stages); err != nil {
			return nil, err
		}
	}

	// Parse thresholds
	if dict, ok := thresholds.(*starlark.Dict); ok {
		rt.scenario.Thresholds = make(map[string]string)
		for _, item := range dict.Items() {
			k, _ := starlark.AsString(item[0])
			v, _ := starlark.AsString(item[1])
			rt.scenario.Thresholds[k] = v
		}
	}

	return starlark.None, nil
}

// chaosBuiltin implements the chaos() function that returns a config dict.
func chaosBuiltin(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var preset starlark.String
	var spikeFactor starlark.Float
	var noiseAmplitude starlark.Float
	var lambda starlark.Float

	hasSpike := false
	hasNoise := false
	hasLambda := false

	if err := starlark.UnpackArgs("chaos", args, kwargs,
		"preset?", &preset,
		"spike_factor?", &spikeFactor,
		"noise_amplitude?", &noiseAmplitude,
		"lambda?", &lambda,
	); err != nil {
		return nil, err
	}

	// Check which kwargs were provided
	for _, kv := range kwargs {
		k, _ := starlark.AsString(kv[0])
		switch k {
		case "spike_factor":
			hasSpike = true
		case "noise_amplitude":
			hasNoise = true
		case "lambda":
			hasLambda = true
		}
	}

	result := starlark.NewDict(8)

	presetStr := string(preset)
	if presetStr == "" {
		presetStr = "moderate"
	}

	base, ok := chaosPresets[presetStr]
	if !ok {
		return nil, fmt.Errorf("unknown chaos preset %q (use: none, gentle, moderate, aggressive)", presetStr)
	}

	if hasSpike {
		base.SpikeFactor = float64(spikeFactor)
	}
	if hasNoise {
		base.NoiseAmplitude = float64(noiseAmplitude)
	}
	if hasLambda {
		base.Lambda = float64(lambda)
	}

	result.SetKey(starlark.String("preset"), starlark.String(presetStr))
	result.SetKey(starlark.String("spike_factor"), starlark.Float(base.SpikeFactor))
	result.SetKey(starlark.String("noise_amplitude"), starlark.Float(base.NoiseAmplitude))
	result.SetKey(starlark.String("lambda"), starlark.Float(base.Lambda))
	result.SetKey(starlark.String("min_interval"), starlark.String(base.MinInterval.String()))
	result.SetKey(starlark.String("max_interval"), starlark.String(base.MaxInterval.String()))

	return result, nil
}

// rampBuiltin implements ramp([stage(...), ...]).
func rampBuiltin(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("ramp: expected 1 argument (list of stages), got %d", len(args))
	}
	// Pass through the list — parsing happens in scenario()
	return args[0], nil
}

// stageBuiltin implements stage("30s", 10).
func stageBuiltin(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var durationStr starlark.String
	var target starlark.Int

	if err := starlark.UnpackArgs("stage", args, kwargs,
		"duration", &durationStr,
		"target", &target,
	); err != nil {
		return nil, err
	}

	result := starlark.NewDict(2)
	result.SetKey(starlark.String("duration"), durationStr)
	t, _ := target.Int64()
	result.SetKey(starlark.String("target"), starlark.MakeInt64(t))

	return result, nil
}

func parseChaosDict(dict *starlark.Dict, cfg *ChaosConfig) error {
	if v, found, _ := dict.Get(starlark.String("preset")); found {
		s, _ := starlark.AsString(v)
		if base, ok := chaosPresets[s]; ok {
			*cfg = base
		}
	}
	if v, found, _ := dict.Get(starlark.String("spike_factor")); found {
		if f, ok := v.(starlark.Float); ok {
			cfg.SpikeFactor = float64(f)
		}
	}
	if v, found, _ := dict.Get(starlark.String("noise_amplitude")); found {
		if f, ok := v.(starlark.Float); ok {
			cfg.NoiseAmplitude = float64(f)
		}
	}
	if v, found, _ := dict.Get(starlark.String("lambda")); found {
		if f, ok := v.(starlark.Float); ok {
			cfg.Lambda = float64(f)
		}
	}
	if v, found, _ := dict.Get(starlark.String("min_interval")); found {
		s, _ := starlark.AsString(v)
		if d, err := time.ParseDuration(s); err == nil {
			cfg.MinInterval = d
		}
	}
	if v, found, _ := dict.Get(starlark.String("max_interval")); found {
		s, _ := starlark.AsString(v)
		if d, err := time.ParseDuration(s); err == nil {
			cfg.MaxInterval = d
		}
	}
	return nil
}

func parseStagesList(list *starlark.List, stages *[]Stage) error {
	iter := list.Iterate()
	defer iter.Done()

	var val starlark.Value
	for iter.Next(&val) {
		dict, ok := val.(*starlark.Dict)
		if !ok {
			return fmt.Errorf("stage: expected dict, got %s", val.Type())
		}

		var stage Stage

		if v, found, _ := dict.Get(starlark.String("duration")); found {
			s, _ := starlark.AsString(v)
			d, err := time.ParseDuration(s)
			if err != nil {
				return fmt.Errorf("stage duration %q: %w", s, err)
			}
			stage.Duration = d
		}

		if v, found, _ := dict.Get(starlark.String("target")); found {
			if i, ok := v.(starlark.Int); ok {
				t, _ := i.Int64()
				stage.Target = int(t)
			}
		}

		*stages = append(*stages, stage)
	}
	return nil
}
