package targets

import (
	"math"
	"testing"

	"github.com/kar98k/internal/config"
)

func TestPick_NilOnEmpty(t *testing.T) {
	if got := New(nil).Pick(); got != nil {
		t.Fatalf("expected nil from empty picker, got %+v", got)
	}
	if got := New([]config.Target{}).Pick(); got != nil {
		t.Fatalf("expected nil from empty slice, got %+v", got)
	}
}

func TestPick_NilOnZeroWeight(t *testing.T) {
	tgts := []config.Target{
		{Name: "a", Weight: 0},
		{Name: "b", Weight: 0},
	}
	if got := New(tgts).Pick(); got != nil {
		t.Fatalf("expected nil when all weights zero, got %+v", got)
	}
}

func TestPick_DeterministicWithSeed(t *testing.T) {
	tgts := []config.Target{
		{Name: "a", Weight: 1},
		{Name: "b", Weight: 1},
		{Name: "c", Weight: 1},
	}
	p1 := NewWithSeed(tgts, 42)
	p2 := NewWithSeed(tgts, 42)

	for i := 0; i < 100; i++ {
		a := p1.Pick()
		b := p2.Pick()
		if a == nil || b == nil {
			t.Fatalf("iter %d: unexpected nil (a=%v b=%v)", i, a, b)
		}
		if a.Name != b.Name {
			t.Fatalf("iter %d: same seed diverged: %s vs %s", i, a.Name, b.Name)
		}
	}
}

func TestPick_WeightedDistributionWithin5Percent(t *testing.T) {
	tgts := []config.Target{
		{Name: "a", Weight: 1},
		{Name: "b", Weight: 3},
		{Name: "c", Weight: 6},
	}
	const samples = 10_000
	const tolerance = 0.05 // 5% of total samples

	p := NewWithSeed(tgts, 12345)
	counts := map[string]int{}
	for i := 0; i < samples; i++ {
		t := p.Pick()
		if t == nil {
			continue
		}
		counts[t.Name]++
	}

	expected := map[string]float64{
		"a": float64(samples) * 1.0 / 10.0,
		"b": float64(samples) * 3.0 / 10.0,
		"c": float64(samples) * 6.0 / 10.0,
	}
	for name, exp := range expected {
		got := float64(counts[name])
		diff := math.Abs(got-exp) / float64(samples)
		if diff > tolerance {
			t.Errorf("target %q: expected ~%.0f got %.0f (diff %.2f%% > %.0f%%)",
				name, exp, got, diff*100, tolerance*100)
		}
	}
}

func TestPick_SkipsZeroWeightEntries(t *testing.T) {
	tgts := []config.Target{
		{Name: "skip", Weight: 0},
		{Name: "only", Weight: 5},
	}
	p := NewWithSeed(tgts, 7)
	for i := 0; i < 50; i++ {
		got := p.Pick()
		if got == nil || got.Name != "only" {
			t.Fatalf("iter %d: expected 'only', got %+v", i, got)
		}
	}
}

func TestLen(t *testing.T) {
	if New(nil).Len() != 0 {
		t.Fatal("nil targets should have Len 0")
	}
	tgts := []config.Target{{Name: "a", Weight: 1}, {Name: "b", Weight: 0}}
	if New(tgts).Len() != 2 {
		t.Fatal("Len should count zero-weight entries")
	}
}
