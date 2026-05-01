package worker

import (
	"context"
	"sync"
	"testing"
	"time"

	hdrhistogram "github.com/HdrHistogram/hdrhistogram-go"
	"github.com/kar98k/internal/config"
)

// TestPool_SnapshotAndAdvancePhase verifies the atomic snapshot+flip
// returns the previous phase, the histograms encode round-trip, and
// the latency histograms are reset post-call.
func TestPool_SnapshotAndAdvancePhase(t *testing.T) {
	p := NewPool(config.Worker{PoolSize: 1, QueueSize: 1}, freshMetrics(t))

	// Record some latency samples in the default phase.
	for _, micros := range []time.Duration{500 * time.Microsecond, 1 * time.Millisecond, 5 * time.Millisecond} {
		p.recordLatency(micros)
	}

	rawN, _ := p.LatencySamples()
	if rawN != 3 {
		t.Fatalf("expected 3 raw samples before flip, got %d", rawN)
	}
	if p.CurrentPhase() != "" {
		t.Fatalf("expected default phase \"\", got %q", p.CurrentPhase())
	}

	rawBytes, corrBytes, prevPhase, err := p.SnapshotAndAdvancePhase("warmup")
	if err != nil {
		t.Fatalf("SnapshotAndAdvancePhase: %v", err)
	}
	if prevPhase != "" {
		t.Fatalf("prevPhase should be \"\" (default), got %q", prevPhase)
	}
	if len(rawBytes) == 0 || len(corrBytes) == 0 {
		t.Fatalf("expected non-empty snapshot bytes, got raw=%d corr=%d", len(rawBytes), len(corrBytes))
	}

	// Histograms reset after snapshot.
	rawN, corrN := p.LatencySamples()
	if rawN != 0 || corrN != 0 {
		t.Fatalf("expected 0 samples after snapshot, got raw=%d corr=%d", rawN, corrN)
	}
	if p.CurrentPhase() != "warmup" {
		t.Fatalf("expected currentPhase=warmup, got %q", p.CurrentPhase())
	}

	// Decode round-trip captures the original 3 samples.
	snap, err := hdrhistogram.Decode(rawBytes)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if snap.TotalCount() != 3 {
		t.Fatalf("decoded snapshot expected 3 samples, got %d", snap.TotalCount())
	}

	// Record more samples in the warmup phase, flip again.
	for _, micros := range []time.Duration{2 * time.Millisecond, 8 * time.Millisecond} {
		p.recordLatency(micros)
	}
	_, _, prev2, err := p.SnapshotAndAdvancePhase("steady")
	if err != nil {
		t.Fatalf("SnapshotAndAdvancePhase 2: %v", err)
	}
	if prev2 != "warmup" {
		t.Fatalf("expected prevPhase=warmup, got %q", prev2)
	}
	if p.CurrentPhase() != "steady" {
		t.Fatalf("expected currentPhase=steady, got %q", p.CurrentPhase())
	}
}

// TestPool_PhaseFlip_Race fires SnapshotAndAdvancePhase concurrently with
// recordLatency. Run with -race; must not panic and currentPhase must
// always reflect a value that was previously set.
func TestPool_PhaseFlip_Race(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping race test in -short mode")
	}
	p := NewPool(config.Worker{PoolSize: 1, QueueSize: 1}, freshMetrics(t))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup

	// Producer: continuously record latency.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			default:
				p.recordLatency(time.Microsecond * time.Duration(100+(time.Now().UnixNano()%100)))
			}
		}
	}()

	// Phase flipper: cycle through 4 phase names.
	phases := []string{"a", "b", "c", "d"}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			_, _, _, err := p.SnapshotAndAdvancePhase(phases[i%len(phases)])
			if err != nil {
				t.Errorf("flip error at i=%d: %v", i, err)
				return
			}
		}
	}()

	// Reader: spam CurrentPhase().
	wg.Add(1)
	go func() {
		defer wg.Done()
		seen := map[string]bool{"": true, "a": true, "b": true, "c": true, "d": true}
		for {
			select {
			case <-ctx.Done():
				return
			default:
				cp := p.CurrentPhase()
				if !seen[cp] {
					t.Errorf("unexpected phase observed: %q", cp)
					return
				}
			}
		}
	}()

	// Let the flipper finish, then cancel reader+producer.
	time.Sleep(50 * time.Millisecond)
	cancel()
	wg.Wait()
}

// TestPool_SetPhase verifies SetPhase updates CurrentPhase without snapshot/reset.
func TestPool_SetPhase(t *testing.T) {
	p := NewPool(config.Worker{PoolSize: 1, QueueSize: 1}, freshMetrics(t))

	p.recordLatency(1 * time.Millisecond)
	p.SetPhase("solo-phase")
	if p.CurrentPhase() != "solo-phase" {
		t.Fatalf("expected solo-phase, got %q", p.CurrentPhase())
	}
	// SetPhase does NOT reset histograms (only SnapshotAndAdvancePhase does).
	rawN, _ := p.LatencySamples()
	if rawN != 1 {
		t.Fatalf("SetPhase should not reset histograms; expected 1 sample, got %d", rawN)
	}
}
