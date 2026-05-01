package worker

import (
	"testing"
	"time"

	hdrhistogram "github.com/HdrHistogram/hdrhistogram-go"
	"github.com/kar98k/internal/config"
	"github.com/kar98k/internal/health"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// freshMetrics returns a Metrics bound to a private registry. Each test
// gets its own so promauto's global-default registration doesn't panic
// on duplicate names across cases.
func freshMetrics(t *testing.T) *health.Metrics {
	t.Helper()
	return health.NewMetricsWithRegistry(prometheus.NewRegistry())
}

// counterValue reads a Prometheus counter without going through the
// HTTP scrape path.
func counterValue(t *testing.T, c prometheus.Counter) float64 {
	t.Helper()
	var m dto.Metric
	if err := c.Write(&m); err != nil {
		t.Fatalf("counter Write: %v", err)
	}
	return m.GetCounter().GetValue()
}

// drainPool builds a Pool with a tiny, never-consumed queue so that
// Submit() reliably hits the drop branch after `queueSize` accepted jobs.
func drainPool(t *testing.T, queueSize int) *Pool {
	t.Helper()
	cfg := config.Worker{
		PoolSize:        1,
		QueueSize:       queueSize,
		MaxIdleConns:    1,
		IdleConnTimeout: time.Second,
	}
	// We don't call Start() — we just exercise Submit()/the queue. That
	// avoids spawning workers and protocol clients during the unit test.
	return NewPool(cfg, freshMetrics(t))
}

// newTestPool is the shared helper for tests that need a Pool but
// don't care about queue size — used by the latency / CO tests.
func newTestPool(t *testing.T) *Pool {
	return drainPool(t, 1)
}

func TestSubmit_QueueFullDropsAndIncrementsCounter(t *testing.T) {
	const queueSize = 4
	p := drainPool(t, queueSize)

	// Fill the queue exactly. None of these should drop.
	for i := 0; i < queueSize; i++ {
		if !p.Submit(Job{}) {
			t.Fatalf("unexpected drop while filling queue at i=%d", i)
		}
	}

	// One more submit must fail and bump the drop counter.
	if p.Submit(Job{}) {
		t.Fatalf("expected Submit to drop when queue is full")
	}

	if got := p.TotalDrops(); got != 1 {
		t.Fatalf("TotalDrops = %d, want 1", got)
	}
	if got := counterValue(t, p.metrics.QueueDropsTotal); got != 1 {
		t.Fatalf("QueueDropsTotal = %v, want 1", got)
	}

	// Three more drops should land on the counter the same way.
	for i := 0; i < 3; i++ {
		if p.Submit(Job{}) {
			t.Fatalf("expected drop on saturated queue, attempt %d", i)
		}
	}

	if got := p.TotalDrops(); got != 4 {
		t.Fatalf("TotalDrops = %d, want 4", got)
	}
	if got := counterValue(t, p.metrics.QueueDropsTotal); got != 4 {
		t.Fatalf("QueueDropsTotal = %v, want 4", got)
	}
}

func TestSubmit_AcceptsWhenQueueHasRoom(t *testing.T) {
	p := drainPool(t, 2)

	if !p.Submit(Job{}) {
		t.Fatalf("first submit should succeed on empty queue")
	}
	if got := p.TotalDrops(); got != 0 {
		t.Fatalf("TotalDrops = %d after a successful submit, want 0", got)
	}
	if got := counterValue(t, p.metrics.QueueDropsTotal); got != 0 {
		t.Fatalf("QueueDropsTotal = %v after a successful submit, want 0", got)
	}
}

func TestRecordDropSlot_ComputesSustainedRate(t *testing.T) {
	p := drainPool(t, 1)

	// Simulate a steady stream where 10% of submits drop, sustained for
	// two ticks. The ring buffer should reflect the cumulative ratio.
	p.recordDropSlot(10, 90)
	p.recordDropSlot(10, 90)

	gotRate := p.DropRate()
	const wantRate = 0.10
	if diff := gotRate - wantRate; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("DropRate = %v, want %v", gotRate, wantRate)
	}
}

func TestRecordDropSlot_ZeroWhenIdle(t *testing.T) {
	p := drainPool(t, 1)
	p.recordDropSlot(0, 0)
	if got := p.DropRate(); got != 0 {
		t.Fatalf("DropRate = %v with no traffic, want 0", got)
	}
}

func TestSuggestQueueSize_RoundsToPowerOfTwo(t *testing.T) {
	cases := []struct {
		tps  float64
		want int
	}{
		{0, 1},
		{1, 16},      // ceil_pow2(10) = 16
		{100, 1024},  // ceil_pow2(1000) = 1024
		{1024, 16384},
	}
	for _, tc := range cases {
		if got := suggestQueueSize(tc.tps); got != tc.want {
			t.Fatalf("suggestQueueSize(%v) = %d, want %d", tc.tps, got, tc.want)
		}
	}
}

// TestRecordLatency_CoordinatedOmissionCorrection synthesises a long
// stall during a fixed-rate run. The raw histogram should report the
// fast request as the dominant tail; the corrected histogram should
// stretch the stall across every missed slot, so its P99 lands near
// the stall length.
//
// This is the regression test called for in #51's acceptance criteria.
func TestRecordLatency_CoordinatedOmissionCorrection(t *testing.T) {
	p := newTestPool(t)

	// 100 TPS → expected inter-request interval = 10ms.
	p.SetRate(100)

	// 500 fast requests at 1ms each.
	for i := 0; i < 500; i++ {
		p.recordLatency(1 * time.Millisecond)
	}
	// One catastrophic 500ms stall.
	p.recordLatency(500 * time.Millisecond)

	rawP99 := p.LatencyPercentile(99, false)
	corrP99 := p.LatencyPercentile(99, true)

	// Raw: 500 fast + 1 slow → 99th percentile is still in the fast bulk.
	if rawP99 > 50 {
		t.Fatalf("raw P99 = %.2fms, expected to remain near fast bulk (<50ms)", rawP99)
	}

	// Corrected: the stall spans 500ms / 10ms = ~50 missed slots, each
	// recorded with values stepping from 10ms..500ms. With 500 fast
	// samples plus ~50 synthesised tail samples, P99 lands deep in the
	// synthesised tail.
	if corrP99 < 100 {
		t.Fatalf("corrected P99 = %.2fms, expected stall to push tail above 100ms", corrP99)
	}
	if corrP99 < rawP99 {
		t.Fatalf("corrected P99 (%.2fms) should be >= raw P99 (%.2fms)", corrP99, rawP99)
	}

	rawN, corrN := p.LatencySamples()
	if corrN <= rawN {
		t.Fatalf("corrected samples (%d) should exceed raw samples (%d) due to synthesis", corrN, rawN)
	}
}

func TestRecordLatency_NoStallNoSynthesis(t *testing.T) {
	p := newTestPool(t)
	p.SetRate(100) // 10ms expected interval

	// All requests well under the expected interval — no synthesis.
	for i := 0; i < 100; i++ {
		p.recordLatency(2 * time.Millisecond)
	}

	rawN, corrN := p.LatencySamples()
	if rawN != 100 {
		t.Fatalf("raw samples = %d, want 100", rawN)
	}
	if corrN != 100 {
		t.Fatalf("corrected samples = %d, want 100 (no synthesis when fast)", corrN)
	}
}

func TestLatencyPercentile_EmptyReturnsZero(t *testing.T) {
	p := newTestPool(t)
	if got := p.LatencyPercentile(99, false); got != 0 {
		t.Fatalf("empty raw P99 = %v, want 0", got)
	}
	if got := p.LatencyPercentile(99, true); got != 0 {
		t.Fatalf("empty corrected P99 = %v, want 0", got)
	}
}

func TestSnapshotAndResetHistograms(t *testing.T) {
	p := newTestPool(t)
	p.SetRate(100) // 10ms expected interval

	const nSamples = 50
	for i := 0; i < nSamples; i++ {
		p.recordLatency(5 * time.Millisecond)
	}

	rawN, corrN := p.LatencySamples()
	if rawN != nSamples {
		t.Fatalf("before snapshot: raw count = %d, want %d", rawN, nSamples)
	}
	if corrN != nSamples {
		t.Fatalf("before snapshot: corrected count = %d, want %d", corrN, nSamples)
	}

	rawBytes, corrBytes, err := p.SnapshotAndResetHistograms()
	if err != nil {
		t.Fatalf("SnapshotAndResetHistograms: %v", err)
	}
	if len(rawBytes) == 0 {
		t.Fatal("rawBytes is empty")
	}
	if len(corrBytes) == 0 {
		t.Fatal("corrBytes is empty")
	}

	// Histograms must be zeroed after snapshot.
	rawN2, corrN2 := p.LatencySamples()
	if rawN2 != 0 {
		t.Fatalf("after snapshot: raw count = %d, want 0", rawN2)
	}
	if corrN2 != 0 {
		t.Fatalf("after snapshot: corrected count = %d, want 0", corrN2)
	}

	// Decode the snapshot and verify sample count matches what we recorded.
	decoded, decErr := hdrhistogram.Decode(rawBytes)
	if decErr != nil {
		t.Fatalf("Decode rawBytes: %v", decErr)
	}
	if decoded.TotalCount() != nSamples {
		t.Fatalf("decoded raw count = %d, want %d", decoded.TotalCount(), nSamples)
	}
}
