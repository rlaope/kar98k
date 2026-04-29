package worker

import (
	"testing"
	"time"

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
