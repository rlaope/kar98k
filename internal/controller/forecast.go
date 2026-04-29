package controller

import (
	"time"

	"github.com/kar98k/internal/config"
	"github.com/kar98k/internal/pattern"
)

// ForecastTimeline returns a sample-point timeline for the supplied
// config. When cfg.Scenarios is non-empty, scenarios and inject
// curves drive TPS at each tick — otherwise the single top-level
// pattern block does. Poisson, schedule, and noise multipliers
// always compose on top.
//
// The function is deterministic given (start, seed) and never calls
// time.Now() once the sample window is computed. Pass seed=0 to
// derive one from start.UnixNano. Both kar simulate and the daemon
// dashboard's /api/forecast endpoint share this code path so they
// can never disagree about what the curve looks like.
func ForecastTimeline(
	cfg *config.Config,
	sched *Scheduler,
	start time.Time,
	duration, resolution time.Duration,
	seed int64,
) []pattern.SamplePoint {
	if len(cfg.Scenarios) == 0 {
		return pattern.SimulateTimeline(
			cfg.Pattern,
			cfg.Controller.BaseTPS,
			cfg.Controller.MaxTPS,
			sched.GetMultiplierForHour,
			start,
			duration,
			resolution,
			seed,
		)
	}
	return forecastScenarios(cfg, sched, start, resolution, seed)
}

// forecastScenarios is the multi-phase path. It walks each phase,
// reuses pattern.SimulateTimeline to compute Poisson/schedule
// overlays for that phase's window, then for inject phases overrides
// the per-tick TPS with the inject curve value (still applying the
// pre-computed multipliers).
func forecastScenarios(
	cfg *config.Config,
	sched *Scheduler,
	start time.Time,
	resolution time.Duration,
	seed int64,
) []pattern.SamplePoint {
	if seed == 0 {
		seed = start.UnixNano()
	}
	patCfg := cfg.Pattern

	var out []pattern.SamplePoint
	cursor := start

	for sIdx, sc := range cfg.Scenarios {
		phasePat := patCfg
		if sc.Pattern != nil {
			phasePat = *sc.Pattern
		}

		phaseBaseTPS := cfg.Controller.BaseTPS
		if sc.BaseTPS > 0 {
			phaseBaseTPS = sc.BaseTPS
		}
		phaseMaxTPS := cfg.Controller.MaxTPS
		if sc.MaxTPS > 0 {
			phaseMaxTPS = sc.MaxTPS
		}

		phaseEnd := cursor.Add(sc.Duration)
		phasePts := pattern.SimulateTimeline(
			phasePat,
			phaseBaseTPS,
			phaseMaxTPS,
			sched.GetMultiplierForHour,
			cursor,
			sc.Duration,
			resolution,
			seed,
		)

		for i, p := range phasePts {
			p.Phase = sc.Name

			if len(sc.Inject) > 0 {
				localOffset := p.Time.Sub(cursor)
				injectBase := injectTPSAt(sc.Inject, localOffset)
				tps := injectBase * p.ScheduleMult * p.PoissonMult
				if phaseMaxTPS > 0 && tps > phaseMaxTPS {
					tps = phaseMaxTPS
				}
				if tps < 1 {
					tps = 1
				}
				p.TPS = tps
			}

			// Skip the last sample of every non-final phase to avoid
			// duplicating the boundary timestamp with the next phase's
			// first sample.
			isLastPoint := i == len(phasePts)-1
			isLastPhase := sIdx == len(cfg.Scenarios)-1
			if isLastPoint && !isLastPhase && p.Time.Equal(phaseEnd) {
				continue
			}

			out = append(out, p)
		}

		cursor = phaseEnd
		// Different seeds per phase so Poisson event timing varies
		// between phases instead of repeating identical patterns.
		seed++
	}

	return out
}
