package rpc_test

import (
	"fmt"
	"testing"

	"github.com/kar98k/internal/rpc"
	pb "github.com/kar98k/internal/rpc/proto"
)

func newStatsPush(id string) *pb.StatsPush {
	return &pb.StatsPush{WorkerId: id, ObservedTps: 10.0}
}

// TestRegister_AddrDedup verifies that registering a second worker with the
// same addr evicts the first entry: liveCount stays at 1, only the new id
// remains, and the old entry disappears from GetSendCh.
func TestRegister_AddrDedup(t *testing.T) {
	reg := rpc.NewWorkerRegistry()
	defer reg.Stop()

	_ = reg.Register("id1", "addr-A:9000")
	if got := reg.Active(); got != 1 {
		t.Fatalf("after first Register: want liveCount=1, got %d", got)
	}

	// Reconnect: same addr, new id.
	_ = reg.Register("id2", "addr-A:9000")

	if got := reg.Active(); got != 1 {
		t.Errorf("after dedup Register: want liveCount=1, got %d", got)
	}

	_, _, ok2 := reg.GetSendCh("id2")
	if !ok2 {
		t.Error("expected id2 to be present after dedup Register")
	}
	_, _, ok1 := reg.GetSendCh("id1")
	if ok1 {
		t.Error("expected id1 to be evicted after dedup Register")
	}
}

// TestRegister_AddrDedup_DoneChannelClosed verifies the done channel of the
// evicted entry is closed so any goroutine blocking on it can exit.
func TestRegister_AddrDedup_DoneChannelClosed(t *testing.T) {
	reg := rpc.NewWorkerRegistry()
	defer reg.Stop()

	// Capture the done channel before eviction by using GetSendCh (returns it).
	_ = reg.Register("id1", "addr-B:9000")
	_, doneCh, ok := reg.GetSendCh("id1")
	if !ok {
		t.Fatal("id1 not found immediately after Register")
	}

	// Reconnect evicts id1.
	_ = reg.Register("id2", "addr-B:9000")

	// done channel must be closed (readable immediately).
	select {
	case <-doneCh:
		// expected
	default:
		t.Error("id1's done channel was not closed after addr-dedup eviction")
	}

	if n := reg.Active(); n != 1 {
		t.Errorf("want Active()=1 after dedup, got %d", n)
	}
}

// TestRegister_AddrDedup_WithMetrics verifies that DeletePerWorker is called
// for the stale entry when metrics are wired in.
func TestRegister_AddrDedup_WithMetrics(t *testing.T) {
	m := newTestMetrics()
	reg := rpc.NewWorkerRegistry(rpc.WithMetrics(m))
	defer reg.Stop()

	reg.Register("id1", "addr-C:9000")
	reg.RecordStats(newStatsPush("id1"))

	before := gatherWorkerIDs(t, m)
	if _, ok := before["id1"]; !ok {
		t.Fatal("expected id1 label series before reconnect")
	}

	reg.Register("id2", "addr-C:9000")

	after := gatherWorkerIDs(t, m)
	if _, ok := after["id1"]; ok {
		t.Error("expected id1 label series to be deleted after addr-dedup eviction")
	}
}

// TestRegister_FirstTime_NoRegression verifies a brand-new registration (no
// prior entry for the addr) still works exactly as before.
func TestRegister_FirstTime_NoRegression(t *testing.T) {
	reg := rpc.NewWorkerRegistry()
	defer reg.Stop()

	ch := reg.Register("solo", "addr-D:9000")
	if ch == nil {
		t.Fatal("Register returned nil channel")
	}
	if got := reg.Active(); got != 1 {
		t.Errorf("want liveCount=1, got %d", got)
	}

	// A second worker with a different addr must not evict the first.
	_ = reg.Register("solo2", "addr-E:9000")
	if got := reg.Active(); got != 2 {
		t.Errorf("two distinct workers: want liveCount=2, got %d", got)
	}
}

// TestRegister_AddrDedup_Sequential exercises liveCount correctness across
// many sequential reconnects on the same addr.
func TestRegister_AddrDedup_Sequential(t *testing.T) {
	reg := rpc.NewWorkerRegistry()
	defer reg.Stop()

	const cycles = 50
	for i := 0; i < cycles; i++ {
		id := fmt.Sprintf("worker-%d", i)
		reg.Register(id, "addr-F:9000")
	}

	if got := reg.Active(); got != 1 {
		t.Errorf("after %d sequential reconnects: want liveCount=1, got %d", cycles, got)
	}
}
