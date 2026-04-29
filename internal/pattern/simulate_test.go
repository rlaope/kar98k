package pattern

import (
	"testing"
	"time"

	"github.com/kar98k/internal/config"
)

func TestSimulateTimeline_FollowsScheduleCurve(t *testing.T) {
	cfg := config.Pattern{
		Poisson: config.Poisson{Enabled: false},
		Noise:   config.Noise{Enabled: false},
	}
	sched := func(h int) float64 {
		if h >= 9 && h < 17 {
			return 2.0
		}
		return 0.5
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	pts := SimulateTimeline(cfg, 100, 1000, sched, start, 24*time.Hour, time.Hour, 1)

	if got := len(pts); got != 25 {
		t.Fatalf("len(pts) = %d, want 25 (24h sweep + endpoint)", got)
	}
	// hour 10 → peak hours, baseTPS 100 × sched 2.0 = 200
	if got := pts[10].TPS; got < 199 || got > 201 {
		t.Fatalf("hour 10 TPS = %.2f, want ~200", got)
	}
	// hour 3 → baseTPS 100 × sched 0.5 = 50
	if got := pts[3].TPS; got != 50 {
		t.Fatalf("hour 3 TPS = %.2f, want 50", got)
	}
}

func TestSimulateTimeline_SeedDeterminism(t *testing.T) {
	cfg := config.Pattern{
		Poisson: config.Poisson{
			Enabled:     true,
			Lambda:      0.01,
			SpikeFactor: 3,
			MinInterval: time.Minute,
			MaxInterval: 30 * time.Minute,
			RampUp:      5 * time.Second,
			RampDown:    10 * time.Second,
		},
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a := SimulateTimeline(cfg, 100, 10_000, nil, start, time.Hour, time.Minute, 42)
	b := SimulateTimeline(cfg, 100, 10_000, nil, start, time.Hour, time.Minute, 42)

	if len(a) != len(b) {
		t.Fatalf("len mismatch %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].TPS != b[i].TPS || a[i].PoissonMult != b[i].PoissonMult {
			t.Fatalf("seed not deterministic at i=%d: %+v vs %+v", i, a[i], b[i])
		}
	}
}

func TestSimulateTimeline_RespectsMaxTPSCap(t *testing.T) {
	cfg := config.Pattern{
		Poisson: config.Poisson{
			Enabled:     true,
			Lambda:      10,
			SpikeFactor: 100,
			MinInterval: 100 * time.Millisecond,
			MaxInterval: time.Second,
			RampUp:      100 * time.Millisecond,
			RampDown:    100 * time.Millisecond,
		},
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	pts := SimulateTimeline(cfg, 100, 500, nil, start, time.Minute, 100*time.Millisecond, 1)

	for _, p := range pts {
		if p.TPS > 500 {
			t.Fatalf("TPS %.2f exceeds maxTPS 500 at %s", p.TPS, p.Time)
		}
	}
}

func TestSimulateTimeline_NoSpikesWhenPoissonDisabled(t *testing.T) {
	cfg := config.Pattern{
		Poisson: config.Poisson{Enabled: false},
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	pts := SimulateTimeline(cfg, 100, 1000, nil, start, time.Hour, time.Minute, 1)

	for _, p := range pts {
		if p.Spiking {
			t.Fatalf("got Spiking=true with poisson disabled at %s", p.Time)
		}
		if p.PoissonMult != 1 {
			t.Fatalf("poisson mult = %v, want 1 when disabled", p.PoissonMult)
		}
	}
}

func TestSimulateTimeline_EmptyOnZeroDuration(t *testing.T) {
	cfg := config.Pattern{}
	pts := SimulateTimeline(cfg, 100, 1000, nil, time.Now(), 0, time.Minute, 1)
	if len(pts) != 0 {
		t.Fatalf("len = %d, want 0 for zero duration", len(pts))
	}
}
