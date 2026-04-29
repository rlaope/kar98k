package pattern

import (
	"testing"
	"time"

	"github.com/kar98k/internal/config"
)

// quietPoisson returns a Poisson config that won't fire an auto spike
// during the test window: lambda is tiny and min-interval keeps the
// next-spike timer comfortably in the future.
func quietPoisson() config.Poisson {
	return config.Poisson{
		Enabled:     true,
		Lambda:      0.0001,
		SpikeFactor: 2.0,
		MinInterval: 5 * time.Minute,
		MaxInterval: 10 * time.Minute,
		RampUp:      100 * time.Millisecond,
		RampDown:    100 * time.Millisecond,
	}
}

func newTestEngine() *Engine {
	return NewEngine(config.Pattern{
		Poisson: quietPoisson(),
		Noise:   config.Noise{Enabled: false},
	}, 100, 1000)
}

func TestEngineStatus_IdleReportsNextSpike(t *testing.T) {
	e := newTestEngine()
	st := e.GetStatus()

	if st.SpikeKind != SpikeKindNone {
		t.Fatalf("idle SpikeKind = %q, want %q", st.SpikeKind, SpikeKindNone)
	}
	if st.PoissonSpiking {
		t.Fatalf("PoissonSpiking should be false when idle")
	}
	if st.NextSpikeIn <= 0 {
		t.Fatalf("NextSpikeIn = %v, want > 0 while idle", st.NextSpikeIn)
	}
}

func TestEngineStatus_ManualSpikeReportsManualKind(t *testing.T) {
	e := newTestEngine()

	e.TriggerManualSpike(0, time.Second)
	st := e.GetStatus()

	if !st.PoissonSpiking {
		t.Fatalf("PoissonSpiking should be true during manual spike")
	}
	if st.SpikeKind != SpikeKindManual {
		t.Fatalf("SpikeKind = %q during manual spike, want %q",
			st.SpikeKind, SpikeKindManual)
	}
	if st.NextSpikeIn != 0 {
		t.Fatalf("NextSpikeIn = %v during spike, want 0", st.NextSpikeIn)
	}
}

func TestEngineStatus_AutoSpikeReportsAutoKind(t *testing.T) {
	// Build an Engine whose Poisson schedule has already lapsed, so
	// the very next Multiplier()/GetStatus() will start an auto spike.
	cfg := quietPoisson()
	cfg.MinInterval = 10 * time.Millisecond
	cfg.MaxInterval = 20 * time.Millisecond
	cfg.RampUp = 100 * time.Millisecond
	cfg.RampDown = 100 * time.Millisecond
	cfg.Lambda = 100 // overwhelmingly likely to schedule near min-interval

	e := NewEngine(config.Pattern{
		Poisson: cfg,
		Noise:   config.Noise{Enabled: false},
	}, 100, 1000)

	// Wait past max-interval to guarantee the schedule has fired.
	time.Sleep(40 * time.Millisecond)

	// Touch Multiplier to trigger the auto-spike branch in PoissonSpike.
	_ = e.poisson.Multiplier()
	st := e.GetStatus()

	if !st.PoissonSpiking {
		t.Fatalf("expected auto spike to be active after schedule expiry")
	}
	if st.SpikeKind != SpikeKindAuto {
		t.Fatalf("SpikeKind = %q during auto spike, want %q",
			st.SpikeKind, SpikeKindAuto)
	}
}
