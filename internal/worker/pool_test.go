package worker

import (
	"testing"
	"time"

	"github.com/kar98k/internal/config"
	"github.com/kar98k/internal/health"
	"github.com/prometheus/client_golang/prometheus"
)

func freshMetrics(t *testing.T) *health.Metrics {
	t.Helper()
	return health.NewMetricsWithRegistry(prometheus.NewRegistry())
}

func newTestPool(t *testing.T) *Pool {
	t.Helper()
	cfg := config.Worker{
		PoolSize:        1,
		QueueSize:       1,
		MaxIdleConns:    1,
		IdleConnTimeout: time.Second,
	}
	return NewPool(cfg, freshMetrics(t))
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
