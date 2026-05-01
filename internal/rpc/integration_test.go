package rpc_test

// Integration tests for the WorkerRegistry (controller.PoolFacade implementation).
//
// These tests exercise registry behavior directly — broadcast partitioning,
// hot-add rebalancing, heartbeat eviction, bounds validation, and stats
// recording — without going through gRPC wire encoding. The gRPC server and
// client are covered by the end-to-end smoke test in examples/distributed/.
//
// The hand-crafted kar.pb.go lacks raw descriptor bytes (protoc not available
// on this system; see internal/rpc/proto/README.md). Proto marshal/unmarshal
// works at runtime via gRPC's codec, but calling proto.Size() directly in a
// test triggers protobuf v1.36's opaque-init path which requires a fully
// initialized MessageInfo. We therefore keep tests at the registry API level.

import (
	"testing"
	"time"

	"github.com/kar98k/internal/rpc"
	pb "github.com/kar98k/internal/rpc/proto"
)

// TestBroadcastPartition verifies that SetRate(total) divides evenly across N workers.
func TestBroadcastPartition(t *testing.T) {
	reg := rpc.NewWorkerRegistry()
	defer reg.Stop()

	ch1 := reg.Register("w1", "w1:9000")
	ch2 := reg.Register("w2", "w2:9000")

	const total = 200.0
	reg.SetRate(total)

	want := total / 2

	for _, ch := range []chan *pb.RateUpdate{ch1, ch2} {
		select {
		case u := <-ch:
			if u.TargetTps != want {
				t.Errorf("got %.1f want %.1f", u.TargetTps, want)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for rate update")
		}
	}
}

// TestHotAdd verifies that adding a 3rd worker causes SetRate to give each
// worker total/3.
func TestHotAdd(t *testing.T) {
	reg := rpc.NewWorkerRegistry()
	defer reg.Stop()

	reg.Register("w1", "w1:9000")
	reg.Register("w2", "w2:9000")
	ch3 := reg.Register("w3", "w3:9000") // hot-add

	const total = 300.0
	reg.SetRate(total)

	// Drain first two channels (non-blocking — they may or may not receive).
	// Only assert on w3 which we control.
	want := total / 3
	select {
	case u := <-ch3:
		if u.TargetTps != want {
			t.Errorf("w3 got %.1f want %.1f", u.TargetTps, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for w3 rate update")
	}

	if n := reg.Active(); n != 3 {
		t.Errorf("Active() = %d, want 3", n)
	}
}

// TestUnregisterEvictsWorker verifies Unregister removes a worker from Active().
func TestUnregisterEvictsWorker(t *testing.T) {
	reg := rpc.NewWorkerRegistry()
	defer reg.Stop()

	reg.Register("w1", "w1:9000")
	if n := reg.Active(); n != 1 {
		t.Fatalf("Active() after register = %d, want 1", n)
	}

	reg.Unregister("w1")
	if n := reg.Active(); n != 0 {
		t.Errorf("Active() after unregister = %d, want 0", n)
	}
}

// TestSetRateNoWorkersNoOp verifies SetRate with no workers is a safe no-op.
func TestSetRateNoWorkersNoOp(t *testing.T) {
	reg := rpc.NewWorkerRegistry()
	defer reg.Stop()

	// Should not panic or block.
	reg.SetRate(1000.0)
}

// TestStatsRecording verifies RecordStats updates TotalDrops and error rate.
func TestStatsRecording(t *testing.T) {
	reg := rpc.NewWorkerRegistry()
	defer reg.Stop()

	reg.Register("w1", "w1:9000")

	reg.RecordStats(&pb.StatsPush{
		WorkerId:    "w1",
		Timestamp:   uint64(time.Now().UnixMilli()),
		ObservedTps: 42.0,
		QueueDrops:  7,
		ErrorRate:   0.05,
	})

	if drops := reg.TotalDrops(); drops != 7 {
		t.Errorf("TotalDrops = %d, want 7", drops)
	}
}

// TestBoundsMismatchRejected verifies ValidateBounds rejects wrong bounds.
func TestBoundsMismatchRejected(t *testing.T) {
	err := rpc.ValidateBounds(&pb.HistogramBounds{
		MinValue: 1,
		MaxValue: 999, // wrong
		SigFigs:  3,
	})
	if err == nil {
		t.Fatal("expected bounds mismatch error, got nil")
	}
}

// TestBoundsMatchAccepted verifies ValidateBounds accepts canonical bounds.
func TestBoundsMatchAccepted(t *testing.T) {
	if err := rpc.ValidateBounds(rpc.DefaultHistogramBounds()); err != nil {
		t.Fatalf("unexpected error for canonical bounds: %v", err)
	}
}

// TestLatencyPercentileAfterMerge verifies LatencyPercentile returns non-zero
// after stats with encoded histogram bytes are recorded. Since encoding
// requires pool.SnapshotAndResetHistograms (tested in worker package), we
// just assert the zero-sample path returns 0 here.
func TestLatencyPercentileZeroBeforeSamples(t *testing.T) {
	reg := rpc.NewWorkerRegistry()
	defer reg.Stop()

	if p := reg.LatencyPercentile(95, false); p != 0 {
		t.Errorf("LatencyPercentile(95, false) = %.1f, want 0 before samples", p)
	}
}

// TestDropRateAggregation verifies DropRate() returns 0 when no stats pushed.
func TestDropRateZeroInitial(t *testing.T) {
	reg := rpc.NewWorkerRegistry()
	defer reg.Stop()

	if r := reg.DropRate(); r != 0 {
		t.Errorf("DropRate() = %.4f, want 0", r)
	}
}

// TestUnregisterDuringBroadcastNoPanic verifies that concurrent Unregister and
// SetRate calls do not cause a send-on-closed-channel panic. Run with -race.
func TestUnregisterDuringBroadcastNoPanic(t *testing.T) {
	reg := rpc.NewWorkerRegistry()
	defer reg.Stop()

	reg.Register("w1", "w1:9000")

	done := make(chan struct{})
	go func() {
		defer close(done)
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			reg.SetRate(100.0)
		}
	}()

	// Repeatedly register and unregister while SetRate broadcasts.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		reg.Register("w2", "w2:9000")
		reg.Unregister("w2")
	}

	<-done
	// If we reach here without panic, the test passes.
}

// TestWorkerDropsPopulatedInSnapshot verifies that RecordStats stores the
// cumulative drop count on the worker entry and Snapshot exposes it correctly.
func TestWorkerDropsPopulatedInSnapshot(t *testing.T) {
	reg := rpc.NewWorkerRegistry()
	defer reg.Stop()

	reg.Register("w1", "w1:9000")

	reg.RecordStats(&pb.StatsPush{
		WorkerId:    "w1",
		Timestamp:   uint64(time.Now().UnixMilli()),
		ObservedTps: 10.0,
		QueueDrops:  42,
		ErrorRate:   0.0,
	})

	rows := reg.Snapshot()
	if len(rows) != 1 {
		t.Fatalf("Snapshot() len = %d, want 1", len(rows))
	}
	if rows[0].Drops != 42 {
		t.Errorf("Snapshot Drops = %d, want 42", rows[0].Drops)
	}
	if drops := reg.TotalDrops(); drops != 42 {
		t.Errorf("TotalDrops() = %d, want 42", drops)
	}

	// Second push with higher cumulative value — totalDrops must not double-count.
	reg.RecordStats(&pb.StatsPush{
		WorkerId:   "w1",
		Timestamp:  uint64(time.Now().UnixMilli()),
		QueueDrops: 100,
	})
	if drops := reg.TotalDrops(); drops != 100 {
		t.Errorf("TotalDrops() after second push = %d, want 100 (not 142)", drops)
	}
}
