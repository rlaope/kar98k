package controller

import (
	"testing"

	"github.com/kar98k/internal/config"
)

// TestGetMultiplierForHour_EmptyScheduleReturnsOne sanity-checks the
// fast path: no schedule means identity multiplier.
func TestGetMultiplierForHour_EmptyScheduleReturnsOne(t *testing.T) {
	s := NewScheduler(nil)
	for h := 0; h < 24; h++ {
		if got := s.GetMultiplierForHour(h); got != 1.0 {
			t.Fatalf("hour %d = %v, want 1.0", h, got)
		}
	}
}

// TestGetMultiplierForHour_LaterWinsWhenPriorityEqual locks in the
// historical override semantics: when no entry sets Priority, later
// entries override earlier ones at the same hour.
func TestGetMultiplierForHour_LaterWinsWhenPriorityEqual(t *testing.T) {
	s := NewScheduler([]config.ScheduleEntry{
		{Hours: []int{9, 10, 11, 12, 13, 14, 15, 16, 17}, TPSMultiplier: 1.5},
		{Hours: []int{12, 13}, TPSMultiplier: 2.0}, // lunch peak, declared after
	})
	if got := s.GetMultiplierForHour(10); got != 1.5 {
		t.Fatalf("hour 10 = %v, want 1.5", got)
	}
	if got := s.GetMultiplierForHour(12); got != 2.0 {
		t.Fatalf("hour 12 = %v, want 2.0 (later entry wins)", got)
	}
}

// TestGetMultiplierForHour_HigherPriorityWinsRegardlessOfOrder is the
// core #48 acceptance: a high-priority entry placed *first* still wins
// over a default-priority entry placed *second*.
func TestGetMultiplierForHour_HigherPriorityWinsRegardlessOfOrder(t *testing.T) {
	s := NewScheduler([]config.ScheduleEntry{
		{Hours: []int{12, 13}, TPSMultiplier: 2.0, Priority: 10}, // lunch peak, high priority, declared first
		{Hours: []int{9, 10, 11, 12, 13, 14, 15, 16, 17}, TPSMultiplier: 1.5},
	})
	if got := s.GetMultiplierForHour(10); got != 1.5 {
		t.Fatalf("hour 10 = %v, want 1.5", got)
	}
	if got := s.GetMultiplierForHour(12); got != 2.0 {
		t.Fatalf("hour 12 = %v, want 2.0 (priority 10 beats priority 0)", got)
	}
}

// TestGetMultiplierForHour_NegativePriorityLoses confirms the
// comparison treats Priority as a signed int — a -5 entry should lose
// to a default-0 entry covering the same hour.
func TestGetMultiplierForHour_NegativePriorityLoses(t *testing.T) {
	s := NewScheduler([]config.ScheduleEntry{
		{Hours: []int{0}, TPSMultiplier: 0.1, Priority: -5},
		{Hours: []int{0}, TPSMultiplier: 0.5}, // priority 0
	})
	if got := s.GetMultiplierForHour(0); got != 0.5 {
		t.Fatalf("hour 0 = %v, want 0.5 (priority 0 beats priority -5)", got)
	}
}

// TestGetMultiplierForHour_EqualPrioritiesFallBackToOrder verifies the
// tie-break rule when multiple entries share the highest priority.
func TestGetMultiplierForHour_EqualPrioritiesFallBackToOrder(t *testing.T) {
	s := NewScheduler([]config.ScheduleEntry{
		{Hours: []int{8}, TPSMultiplier: 1.0, Priority: 5},
		{Hours: []int{8}, TPSMultiplier: 3.0, Priority: 5}, // same priority, later wins
	})
	if got := s.GetMultiplierForHour(8); got != 3.0 {
		t.Fatalf("hour 8 = %v, want 3.0 (later wins on equal priority)", got)
	}
}

// TestGetMultiplierForHour_NormalizesNegativeAndOverflowHours protects
// the modular-arithmetic shim used by callers that may pass arbitrary
// hour offsets (e.g. forecast +27h).
func TestGetMultiplierForHour_NormalizesNegativeAndOverflowHours(t *testing.T) {
	s := NewScheduler([]config.ScheduleEntry{
		{Hours: []int{3}, TPSMultiplier: 0.5},
	})
	if got := s.GetMultiplierForHour(27); got != 0.5 {
		t.Fatalf("hour 27 (=3) = %v, want 0.5", got)
	}
	if got := s.GetMultiplierForHour(-21); got != 0.5 {
		t.Fatalf("hour -21 (=3) = %v, want 0.5", got)
	}
}

// TestGetAllMultipliers_ReflectsPriorityAcross24Hours ensures the
// 24-element snapshot used by simulate / dashboard agrees with the
// per-hour priority resolution.
func TestGetAllMultipliers_ReflectsPriorityAcross24Hours(t *testing.T) {
	s := NewScheduler([]config.ScheduleEntry{
		{Hours: []int{0, 1, 2, 3, 4, 5}, TPSMultiplier: 0.3},                            // night
		{Hours: []int{9, 10, 11, 12, 13, 14, 15, 16, 17}, TPSMultiplier: 1.5},           // business
		{Hours: []int{12, 13}, TPSMultiplier: 2.5, Priority: 10},                        // lunch peak
	})
	mult := s.GetAllMultipliers()
	if mult[3] != 0.3 {
		t.Fatalf("hour 3 = %v, want 0.3", mult[3])
	}
	if mult[10] != 1.5 {
		t.Fatalf("hour 10 = %v, want 1.5", mult[10])
	}
	if mult[12] != 2.5 {
		t.Fatalf("hour 12 = %v, want 2.5 (lunch priority wins)", mult[12])
	}
	if mult[20] != 1.0 {
		t.Fatalf("hour 20 = %v, want 1.0 (no entry → identity)", mult[20])
	}
}
