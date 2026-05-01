package rpc

import (
	"testing"

	hdrhistogram "github.com/HdrHistogram/hdrhistogram-go"
	"github.com/kar98k/internal/hdrbounds"
	pb "github.com/kar98k/internal/rpc/proto"
)

// makePhaseHistogram builds an encoded HdrHistogram raw payload with N
// samples at the given latencyMicros so tests can assert per-phase merge.
func makePhaseHistogram(t *testing.T, latencyMicros int64, count int) []byte {
	t.Helper()
	h := hdrhistogram.New(hdrbounds.Min, hdrbounds.Max, int(hdrbounds.SigFigs))
	for i := 0; i < count; i++ {
		if err := h.RecordValue(latencyMicros); err != nil {
			t.Fatalf("RecordValue: %v", err)
		}
	}
	b, err := h.Encode(hdrhistogram.V2CompressedEncodingCookieBase)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return b
}

// TestRegistry_PerPhaseMerge sends two pushes with distinct phase names.
// LatencyPercentileByPhase must return the per-phase value (NOT the global
// blend) and the global aggregate must include both.
func TestRegistry_PerPhaseMerge(t *testing.T) {
	reg := NewWorkerRegistry()
	defer reg.Stop()
	reg.Register("w1", "127.0.0.1:1")

	// Phase "warmup": 50 samples at 1ms.
	reg.RecordStats(&pb.StatsPush{
		WorkerId:  "w1",
		HdrRaw:    makePhaseHistogram(t, 1_000, 50),
		PhaseName: "warmup",
	})

	// Phase "peak": 50 samples at 10ms.
	reg.RecordStats(&pb.StatsPush{
		WorkerId:  "w1",
		HdrRaw:    makePhaseHistogram(t, 10_000, 50),
		PhaseName: "peak",
	})

	warmupP95 := reg.LatencyPercentileByPhase("warmup", 95, false)
	peakP95 := reg.LatencyPercentileByPhase("peak", 95, false)

	// Per-phase percentiles must be distinct (warmup ~1ms, peak ~10ms).
	if warmupP95 < 0.5 || warmupP95 > 2.0 {
		t.Errorf("warmup p95 = %.2fms, expected ~1ms", warmupP95)
	}
	if peakP95 < 8.0 || peakP95 > 12.0 {
		t.Errorf("peak p95 = %.2fms, expected ~10ms", peakP95)
	}
	if warmupP95 >= peakP95 {
		t.Errorf("warmup p95 (%.2fms) should be << peak p95 (%.2fms)", warmupP95, peakP95)
	}

	// Global p95 should reflect the combined distribution (somewhere
	// between the two — matters that it's > warmup and <= peak).
	globalP95 := reg.LatencyPercentile(95, false)
	if globalP95 <= warmupP95 || globalP95 > peakP95 {
		t.Errorf("global p95 (%.2fms) should be > warmup (%.2fms) and <= peak (%.2fms)",
			globalP95, warmupP95, peakP95)
	}
}

// TestRegistry_BackwardsCompat_EmptyPhase verifies a v1 worker (no phase
// tag) merges into the default "" phase without breaking, and the global
// aggregate stays correct.
func TestRegistry_BackwardsCompat_EmptyPhase(t *testing.T) {
	reg := NewWorkerRegistry()
	defer reg.Stop()
	reg.Register("v1-worker", "127.0.0.1:9")

	// v1 push: PhaseName field is the zero value (empty string).
	reg.RecordStats(&pb.StatsPush{
		WorkerId: "v1-worker",
		HdrRaw:   makePhaseHistogram(t, 5_000, 100),
	})

	defaultP95 := reg.LatencyPercentileByPhase("", 95, false)
	if defaultP95 < 4.0 || defaultP95 > 6.0 {
		t.Errorf("default-phase p95 = %.2fms, expected ~5ms", defaultP95)
	}

	globalP95 := reg.LatencyPercentile(95, false)
	if globalP95 < 4.0 || globalP95 > 6.0 {
		t.Errorf("global p95 = %.2fms, expected ~5ms (single bucket)", globalP95)
	}

	// Unknown phase returns 0 (no panic).
	if got := reg.LatencyPercentileByPhase("nonexistent", 95, false); got != 0 {
		t.Errorf("unknown phase should return 0, got %.2f", got)
	}
}

// TestRegistry_PhaseReentry verifies re-entering a previously seen phase
// MERGES into the existing histogram (does NOT reset). Matches solo
// internal/script/phase.go:46-50 name-only re-entry semantics.
func TestRegistry_PhaseReentry(t *testing.T) {
	reg := NewWorkerRegistry()
	defer reg.Stop()
	reg.Register("w1", "127.0.0.1:1")

	// First entry into phase "loop": 30 samples at 1ms.
	reg.RecordStats(&pb.StatsPush{
		WorkerId:  "w1",
		HdrRaw:    makePhaseHistogram(t, 1_000, 30),
		PhaseName: "loop",
	})
	// Detour to phase "rest": 30 samples at 100ms.
	reg.RecordStats(&pb.StatsPush{
		WorkerId:  "w1",
		HdrRaw:    makePhaseHistogram(t, 100_000, 30),
		PhaseName: "rest",
	})
	// Re-enter "loop": 30 more samples at 1ms.
	reg.RecordStats(&pb.StatsPush{
		WorkerId:  "w1",
		HdrRaw:    makePhaseHistogram(t, 1_000, 30),
		PhaseName: "loop",
	})

	snap := reg.PhaseSnapshot()
	loopFound := false
	for _, p := range snap {
		if p.Phase == "loop" {
			loopFound = true
			// Re-entry MERGED: total samples in loop bucket should be 60.
			if p.Samples != 60 {
				t.Errorf("loop bucket should have 60 samples (30+30), got %d", p.Samples)
			}
			// p95 should still be ~1ms because both batches were 1ms.
			if p.P95Ms < 0.5 || p.P95Ms > 2.0 {
				t.Errorf("loop p95 after re-entry = %.2fms, expected ~1ms", p.P95Ms)
			}
		}
	}
	if !loopFound {
		t.Fatal("loop phase missing from PhaseSnapshot()")
	}
}

// TestRegistry_SetPhase_BroadcastsInRateUpdate verifies SetRate carries the
// most-recently-set phase in pb.RateUpdate.PhaseName.
func TestRegistry_SetPhase_BroadcastsInRateUpdate(t *testing.T) {
	reg := NewWorkerRegistry()
	defer reg.Stop()

	ch := reg.Register("w1", "127.0.0.1:1")
	reg.SetPhase("steady")
	reg.SetRate(100.0)

	select {
	case u := <-ch:
		if u.PhaseName != "steady" {
			t.Errorf("expected RateUpdate.PhaseName=steady, got %q", u.PhaseName)
		}
		if u.TargetTps != 100.0 {
			t.Errorf("expected target_tps=100, got %v", u.TargetTps)
		}
	default:
		t.Fatal("no RateUpdate received on channel")
	}
}

// TestRegistry_PhaseSnapshot_EmptyPhasesNotReturned verifies that empty
// histograms are filtered out of PhaseSnapshot output.
func TestRegistry_PhaseSnapshot_EmptyPhasesNotReturned(t *testing.T) {
	reg := NewWorkerRegistry()
	defer reg.Stop()

	snap := reg.PhaseSnapshot()
	if len(snap) != 0 {
		t.Errorf("PhaseSnapshot() with no records should be empty, got %d entries", len(snap))
	}
}
