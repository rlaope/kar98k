package rpc_test

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/kar98k/internal/health"
	"github.com/kar98k/internal/rpc"
	pb "github.com/kar98k/internal/rpc/proto"
)

// gatherDropCount sums the QueueDropsPerWorker counter value for a specific worker_id.
func gatherDropCount(t *testing.T, m *health.Metrics, workerID string) float64 {
	t.Helper()
	ch := make(chan prometheus.Metric, 64)
	m.QueueDropsPerWorker.Collect(ch)
	close(ch)

	var total float64
	for metric := range ch {
		var dm dto.Metric
		if err := metric.Write(&dm); err != nil {
			t.Fatalf("metric.Write: %v", err)
		}
		for _, lp := range dm.GetLabel() {
			if lp.GetName() == "worker_id" && lp.GetValue() == workerID {
				total += dm.GetCounter().GetValue()
			}
		}
	}
	return total
}

// gatherWorkerIDs collects the set of worker_id label values currently
// present in the ObservedTPSPerWorker GaugeVec.
func gatherWorkerIDs(t *testing.T, m *health.Metrics) map[string]struct{} {
	t.Helper()
	ch := make(chan prometheus.Metric, 64)
	m.ObservedTPSPerWorker.Collect(ch)
	close(ch)

	ids := make(map[string]struct{})
	for metric := range ch {
		var dm dto.Metric
		if err := metric.Write(&dm); err != nil {
			t.Fatalf("metric.Write: %v", err)
		}
		for _, lp := range dm.GetLabel() {
			if lp.GetName() == "worker_id" {
				ids[lp.GetValue()] = struct{}{}
			}
		}
	}
	return ids
}

// newTestMetrics returns a Metrics instance backed by a fresh registry so
// tests never collide on global Prometheus state.
func newTestMetrics() *health.Metrics {
	return health.NewMetricsWithRegistry(prometheus.NewRegistry())
}

// TestPerWorkerLabels_ThreeWorkers registers 3 workers, pushes a StatsPush
// for each, and asserts that exactly 3 distinct worker_id label series exist.
func TestPerWorkerLabels_ThreeWorkers(t *testing.T) {
	m := newTestMetrics()
	reg := rpc.NewWorkerRegistry(rpc.WithMetrics(m))
	defer reg.Stop()

	workers := []string{"w1", "w2", "w3"}
	for _, id := range workers {
		reg.Register(id, id+":9000")
	}

	for _, id := range workers {
		reg.RecordStats(&pb.StatsPush{
			WorkerId:    id,
			ObservedTps: 100.0,
			ErrorRate:   0.01,
			QueueDrops:  0,
		})
	}

	ids := gatherWorkerIDs(t, m)
	if len(ids) != 3 {
		t.Errorf("expected 3 distinct worker_id label series, got %d: %v", len(ids), ids)
	}
	for _, id := range workers {
		if _, ok := ids[id]; !ok {
			t.Errorf("missing worker_id=%q in label series", id)
		}
	}
}

// TestPerWorkerLabels_UnregisterDeletesSeries registers a worker, pushes stats,
// then unregisters it and asserts the label series is gone.
func TestPerWorkerLabels_UnregisterDeletesSeries(t *testing.T) {
	m := newTestMetrics()
	reg := rpc.NewWorkerRegistry(rpc.WithMetrics(m))
	defer reg.Stop()

	reg.Register("wx", "wx:9000")
	reg.RecordStats(&pb.StatsPush{
		WorkerId:    "wx",
		ObservedTps: 50.0,
	})

	before := gatherWorkerIDs(t, m)
	if _, ok := before["wx"]; !ok {
		t.Fatal("expected worker_id=wx label series after RecordStats")
	}

	reg.Unregister("wx")

	after := gatherWorkerIDs(t, m)
	if _, ok := after["wx"]; ok {
		t.Error("expected worker_id=wx label series to be deleted after Unregister")
	}
}

// TestPerWorkerLabels_CardinalityBound registers 5 workers, records stats for
// all, then unregisters 2. Asserts exactly 3 active label series remain.
func TestPerWorkerLabels_CardinalityBound(t *testing.T) {
	m := newTestMetrics()
	reg := rpc.NewWorkerRegistry(rpc.WithMetrics(m))
	defer reg.Stop()

	all := []string{"a", "b", "c", "d", "e"}
	for _, id := range all {
		reg.Register(id, id+":9000")
	}
	for _, id := range all {
		reg.RecordStats(&pb.StatsPush{WorkerId: id, ObservedTps: 10.0})
	}

	reg.Unregister("a")
	reg.Unregister("b")

	ids := gatherWorkerIDs(t, m)
	if len(ids) != 3 {
		t.Errorf("expected 3 label series after 2 evictions, got %d: %v", len(ids), ids)
	}
	for _, id := range []string{"c", "d", "e"} {
		if _, ok := ids[id]; !ok {
			t.Errorf("missing expected worker_id=%q", id)
		}
	}
}

// TestPerWorkerLabels_DropsCounterReset verifies reset semantics when a worker
// restarts and its cumulative drop counter resets to a lower value.
//
// Sequence: push drops=10 → push drops=5 (reset) → push drops=12.
// Expected counter total: 10 (first push) + 0 (reset, new baseline=5) + 7 (12-5) = 17.
func TestPerWorkerLabels_DropsCounterReset(t *testing.T) {
	m := newTestMetrics()
	reg := rpc.NewWorkerRegistry(rpc.WithMetrics(m))
	defer reg.Stop()

	reg.Register("wr", "wr:9000")

	reg.RecordStats(&pb.StatsPush{WorkerId: "wr", QueueDrops: 10})
	after10 := gatherDropCount(t, m, "wr")
	if after10 != 10 {
		t.Errorf("after drops=10: want counter=10, got %.0f", after10)
	}

	// Simulate worker restart: counter resets to 5 (< 10).
	reg.RecordStats(&pb.StatsPush{WorkerId: "wr", QueueDrops: 5})
	afterReset := gatherDropCount(t, m, "wr")
	if afterReset != 10 {
		t.Errorf("after reset to drops=5: want counter still=10 (no delta), got %.0f", afterReset)
	}

	// Now drops grows from the new baseline of 5.
	reg.RecordStats(&pb.StatsPush{WorkerId: "wr", QueueDrops: 12})
	afterGrowth := gatherDropCount(t, m, "wr")
	if afterGrowth != 17 {
		t.Errorf("after drops=12 (baseline=5): want counter=17, got %.0f", afterGrowth)
	}
}

// TestPerWorkerLabels_SweepDeletesSeries verifies that the heartbeat sweeper
// calls DeletePerWorker when a worker times out.
func TestPerWorkerLabels_SweepDeletesSeries(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping sweep test in short mode")
	}

	m := newTestMetrics()
	reg := rpc.NewWorkerRegistry(rpc.WithMetrics(m))
	defer reg.Stop()

	reg.Register("stale", "stale:9000")
	reg.RecordStats(&pb.StatsPush{WorkerId: "stale", ObservedTps: 20.0})

	before := gatherWorkerIDs(t, m)
	if _, ok := before["stale"]; !ok {
		t.Fatal("expected stale worker label series before sweep")
	}

	// Wait for the heartbeat sweeper to evict the worker (timeout = 5s + sweep tick = 1s).
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		after := gatherWorkerIDs(t, m)
		if _, ok := after["stale"]; !ok {
			return // evicted and label deleted as expected
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Error("stale worker label series was not deleted after sweep timeout")
}
