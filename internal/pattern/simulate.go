package pattern

import (
	"math"
	"math/rand"
	"time"

	"github.com/kar98k/internal/config"
)

// SamplePoint is one timestamp on a forecast timeline. Multipliers are
// the breakdown that produced TPS so callers can chart each layer.
type SamplePoint struct {
	Time         time.Time `json:"time"`
	Hour         int       `json:"hour"`
	ScheduleMult float64   `json:"schedule_mult"`
	PoissonMult  float64   `json:"poisson_mult"`
	NoiseMult    float64   `json:"noise_mult"`
	TPS          float64   `json:"tps"`
	Spiking      bool      `json:"spiking"`
}

type spikeEvent struct {
	start, peak, end time.Time
	factor           float64
}

// SimulateTimeline produces a deterministic forecast of the TPS curve
// over `duration` starting at `start`, sampled every `resolution`.
//
// scheduleMult maps an hour-of-day (0..23) to its multiplier; pass nil
// for the identity multiplier. The Poisson layer uses `seed` so the
// same seed reproduces the same spike timeline; pass 0 to seed from
// the wall clock.
//
// This path is wall-clock-free: it does not consult time.Now() once
// the timeline has been generated, so it can model any window — past,
// present, or future — without side effects. Spring noise is a
// stochastic random walk and collapses to 1.0 in the forecast; the
// curve reflects the deterministic schedule × spike contour.
func SimulateTimeline(
	cfg config.Pattern,
	baseTPS, maxTPS float64,
	scheduleMult func(int) float64,
	start time.Time,
	duration, resolution time.Duration,
	seed int64,
) []SamplePoint {
	if duration <= 0 {
		return nil
	}
	if resolution <= 0 {
		resolution = time.Minute
	}
	if scheduleMult == nil {
		scheduleMult = func(int) float64 { return 1 }
	}
	if seed == 0 {
		seed = time.Now().UnixNano()
	}
	rng := rand.New(rand.NewSource(seed))

	end := start.Add(duration)
	events := generatePoissonEvents(cfg.Poisson, start, end, rng)

	n := int(duration/resolution) + 1
	out := make([]SamplePoint, 0, n)
	for t := start; !t.After(end); t = t.Add(resolution) {
		sched := scheduleMult(t.Hour())
		poisson, spiking := poissonMultiplierAt(events, t)
		tps := baseTPS * sched * poisson
		if maxTPS > 0 && tps > maxTPS {
			tps = maxTPS
		}
		if tps < 1 {
			tps = 1
		}
		out = append(out, SamplePoint{
			Time:         t,
			Hour:         t.Hour(),
			ScheduleMult: sched,
			PoissonMult:  poisson,
			NoiseMult:    1,
			TPS:          tps,
			Spiking:      spiking,
		})
	}
	return out
}

// generatePoissonEvents lays spikes out along the window using inverse
// transform sampling — same math as runtime PoissonSpike, but driven
// by the supplied RNG instead of a wall clock so the schedule is
// reproducible from a seed.
func generatePoissonEvents(cfg config.Poisson, start, end time.Time, rng *rand.Rand) []spikeEvent {
	if !cfg.Enabled || cfg.Lambda <= 0 {
		return nil
	}
	minSec := cfg.MinInterval.Seconds()
	maxSec := cfg.MaxInterval.Seconds()
	if maxSec <= 0 {
		maxSec = 600
	}
	rampWindow := cfg.RampUp + cfg.RampDown

	var events []spikeEvent
	cursor := start
	for {
		u := rng.Float64()
		if u == 0 {
			u = 1e-10
		}
		intervalSec := -math.Log(u) / cfg.Lambda
		if intervalSec < minSec {
			intervalSec = minSec
		}
		if intervalSec > maxSec {
			intervalSec = maxSec
		}
		cursor = cursor.Add(time.Duration(intervalSec * float64(time.Second)))
		if !cursor.Before(end) {
			break
		}
		events = append(events, spikeEvent{
			start:  cursor,
			peak:   cursor.Add(cfg.RampUp),
			end:    cursor.Add(rampWindow),
			factor: cfg.SpikeFactor,
		})
		cursor = cursor.Add(rampWindow)
	}
	return events
}

// poissonMultiplierAt finds whether t lands inside any spike's ramp
// window and returns the corresponding multiplier. Events are
// monotonic in time and never overlap (cursor advances past the ramp
// window when scheduling), so a binary search is sufficient.
func poissonMultiplierAt(events []spikeEvent, t time.Time) (float64, bool) {
	lo, hi := 0, len(events)-1
	for lo <= hi {
		mid := (lo + hi) / 2
		e := events[mid]
		switch {
		case t.Before(e.start):
			hi = mid - 1
		case !t.Before(e.end):
			lo = mid + 1
		default:
			return spikeMultiplier(e, t), true
		}
	}
	return 1, false
}

func spikeMultiplier(e spikeEvent, t time.Time) float64 {
	if t.Before(e.peak) {
		elapsed := t.Sub(e.start).Seconds()
		total := e.peak.Sub(e.start).Seconds()
		if total <= 0 {
			return e.factor
		}
		return 1.0 + (e.factor-1.0)*(elapsed/total)
	}
	elapsed := t.Sub(e.peak).Seconds()
	total := e.end.Sub(e.peak).Seconds()
	if total <= 0 {
		return 1
	}
	decay := math.Exp(-3 * elapsed / total)
	return 1.0 + (e.factor-1.0)*decay
}
