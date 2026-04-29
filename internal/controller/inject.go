package controller

import (
	"math"
	"time"

	"github.com/kar98k/internal/config"
)

// defaultHeavisideSteepness is the sigmoid sharpness applied when an
// inject step omits its own steepness. Higher values approach a true
// step function; 6 was picked empirically to give a recognisable
// "knee" without being indistinguishable from constant_tps.
const defaultHeavisideSteepness = 6.0

// injectTPSAt returns the effective TPS at offset `at` (relative to
// the start of the phase) for an injection curve. Steps are walked
// in order; the first step whose accumulated window contains `at`
// produces the value.
//
// If `at` is past the end of the curve the last step's terminal TPS
// is returned — this is a safety net rather than a contract; the
// validator rejects configs whose inject duration doesn't sum to the
// phase duration.
func injectTPSAt(steps []config.InjectStep, at time.Duration) float64 {
	if len(steps) == 0 {
		return 0
	}
	if at < 0 {
		at = 0
	}

	var cursor time.Duration
	for i := range steps {
		end := cursor + steps[i].Duration
		if at < end || i == len(steps)-1 {
			local := at - cursor
			if local < 0 {
				local = 0
			}
			if local > steps[i].Duration {
				local = steps[i].Duration
			}
			return injectStepTPSAt(steps[i], local)
		}
		cursor = end
	}
	return injectStepTPSAt(steps[len(steps)-1], steps[len(steps)-1].Duration)
}

// injectStepTPSAt evaluates a single step at the local offset `t`
// (0 ≤ t ≤ step.Duration). The branches mirror config.InjectStepType.
func injectStepTPSAt(step config.InjectStep, t time.Duration) float64 {
	switch step.Type {
	case config.InjectNothingFor:
		return 0
	case config.InjectConstantTPS:
		return step.TPS
	case config.InjectRampTPS:
		if step.Duration <= 0 {
			return step.To
		}
		progress := float64(t) / float64(step.Duration)
		return step.From + (step.To-step.From)*progress
	case config.InjectHeavisideTPS:
		k := step.Steepness
		if k <= 0 {
			k = defaultHeavisideSteepness
		}
		if step.Duration <= 0 {
			return step.To
		}
		// Centre the sigmoid on the step midpoint, then stretch+shift so
		// that t=0 lands exactly on From and t=Duration lands exactly on
		// To. Without normalisation a vanilla sigmoid only saturates
		// asymptotically, leaving a noticeable error at the endpoints.
		centred := float64(t)/float64(step.Duration) - 0.5
		raw := 1.0 / (1.0 + math.Exp(-k*centred))
		lo := 1.0 / (1.0 + math.Exp(k/2))
		hi := 1.0 / (1.0 + math.Exp(-k/2))
		s := (raw - lo) / (hi - lo)
		return step.From + (step.To-step.From)*s
	default:
		return 0
	}
}
