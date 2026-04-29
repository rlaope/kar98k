package discovery

import (
	"math"
	"testing"
)

// closeEnough asserts that v is within tol of want. HdrHistogram
// quantises into ranges sized by significant-digits precision, so
// ValueAtQuantile reports the upper edge of the bucket — exact equality
// with a Go float input is not meaningful.
func closeEnough(t *testing.T, name string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Fatalf("%s = %v, want %v ± %v", name, got, want, tol)
	}
}

func TestAnalyzer_RecordAndPercentile(t *testing.T) {
	a := NewAnalyzer()

	// Uniform distribution 1..100 ms — P95 should land near 95ms.
	for i := 1; i <= 100; i++ {
		a.RecordLatency(float64(i), false)
	}

	if got := a.GetSampleCount(); got != 100 {
		t.Fatalf("SampleCount = %d, want 100", got)
	}
	closeEnough(t, "P95", a.GetP95Latency(), 95, 1)
	closeEnough(t, "P99", a.GetP99Latency(), 99, 1)
	closeEnough(t, "Avg", a.GetAvgLatency(), 50.5, 1)
}

func TestAnalyzer_ErrorRate(t *testing.T) {
	a := NewAnalyzer()

	for i := 0; i < 90; i++ {
		a.RecordLatency(10, false)
	}
	for i := 0; i < 10; i++ {
		a.RecordLatency(10, true)
	}

	closeEnough(t, "ErrorRate", a.GetErrorRate(), 10.0, 0.001)
	if got := a.GetTotalErrors(); got != 10 {
		t.Fatalf("TotalErrors = %d, want 10", got)
	}
	if got := a.GetTotalRequests(); got != 100 {
		t.Fatalf("TotalRequests = %d, want 100", got)
	}
}

func TestAnalyzer_ResetWindow_KeepsLifetimeCounts(t *testing.T) {
	a := NewAnalyzer()

	for i := 0; i < 50; i++ {
		a.RecordLatency(20, false)
	}
	a.ResetWindow()

	if got := a.GetSampleCount(); got != 0 {
		t.Fatalf("after ResetWindow, SampleCount = %d, want 0", got)
	}
	if got := a.GetTotalRequests(); got != 50 {
		t.Fatalf("after ResetWindow, TotalRequests = %d, want 50", got)
	}
}

func TestAnalyzer_Reset_ClearsEverything(t *testing.T) {
	a := NewAnalyzer()

	for i := 0; i < 50; i++ {
		a.RecordLatency(20, true)
	}
	a.Reset()

	if got := a.GetSampleCount(); got != 0 {
		t.Fatalf("after Reset, SampleCount = %d, want 0", got)
	}
	if got := a.GetTotalRequests(); got != 0 {
		t.Fatalf("after Reset, TotalRequests = %d, want 0", got)
	}
	if got := a.GetTotalErrors(); got != 0 {
		t.Fatalf("after Reset, TotalErrors = %d, want 0", got)
	}
}

func TestAnalyzer_TakeSnapshot(t *testing.T) {
	a := NewAnalyzer()

	// 980 fast samples + 20 slow ones places P99 squarely in the slow tail.
	for i := 0; i < 980; i++ {
		a.RecordLatency(10, false)
	}
	for i := 0; i < 20; i++ {
		a.RecordLatency(1000, true)
	}

	s := a.TakeSnapshot()

	if s.SampleCount != 1000 {
		t.Fatalf("SampleCount = %d, want 1000", s.SampleCount)
	}
	if s.TotalErrors != 20 {
		t.Fatalf("TotalErrors = %d, want 20", s.TotalErrors)
	}
	closeEnough(t, "P95", s.P95Latency, 10, 1)      // 95% are still fast
	closeEnough(t, "P99", s.P99Latency, 1000, 10)   // top 2% are slow
	if s.Timestamp.IsZero() {
		t.Fatalf("Timestamp should be set")
	}
}

func TestAnalyzer_EmptyReturnsZero(t *testing.T) {
	a := NewAnalyzer()
	if got := a.GetP95Latency(); got != 0 {
		t.Fatalf("empty P95 = %v, want 0", got)
	}
	if got := a.GetAvgLatency(); got != 0 {
		t.Fatalf("empty Avg = %v, want 0", got)
	}
	if got := a.GetErrorRate(); got != 0 {
		t.Fatalf("empty ErrorRate = %v, want 0", got)
	}
}
