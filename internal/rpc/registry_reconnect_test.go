package rpc_test

import (
	"testing"

	"github.com/kar98k/internal/rpc"
)

// TestReconnectCycle_RegistryOneEntry simulates a worker reconnect cycle:
// the worker disconnects (old entry evicted via addr-dedup on re-Register)
// and reconnects with a new ID for the same addr. After the cycle the
// registry must show exactly one entry for that addr and liveCount==1.
func TestReconnectCycle_RegistryOneEntry(t *testing.T) {
	reg := rpc.NewWorkerRegistry()
	defer reg.Stop()

	const addr = "worker-host:9000"

	// First connection.
	_ = reg.Register("id-v1", addr)
	if n := reg.Active(); n != 1 {
		t.Fatalf("after first Register: want Active=1, got %d", n)
	}

	// Simulate reconnect: master mints new ID for same addr.
	_ = reg.Register("id-v2", addr)

	// Registry must show exactly one entry.
	if n := reg.Active(); n != 1 {
		t.Errorf("after reconnect: want Active=1 (no stale entry), got %d", n)
	}

	// Only id-v2 must be reachable; id-v1 must be gone.
	_, _, ok2 := reg.GetSendCh("id-v2")
	if !ok2 {
		t.Error("id-v2 not found after reconnect")
	}
	_, _, ok1 := reg.GetSendCh("id-v1")
	if ok1 {
		t.Error("id-v1 still present after reconnect (stale entry leak)")
	}
}

// TestReconnectCycle_MultipleRounds runs 5 consecutive reconnect cycles on
// the same addr and asserts liveCount stays at 1 throughout.
func TestReconnectCycle_MultipleRounds(t *testing.T) {
	reg := rpc.NewWorkerRegistry()
	defer reg.Stop()

	const addr = "worker-host:9001"
	const rounds = 5

	for i := 0; i < rounds; i++ {
		id := "worker-round-" + string(rune('A'+i))
		reg.Register(id, addr)
		if n := reg.Active(); n != 1 {
			t.Errorf("round %d: want Active=1, got %d", i, n)
		}
	}
}

// TestReconnectCycle_TwoWorkersTwoAddrs verifies that reconnects on one addr
// do not affect a worker registered on a different addr.
func TestReconnectCycle_TwoWorkersTwoAddrs(t *testing.T) {
	reg := rpc.NewWorkerRegistry()
	defer reg.Stop()

	reg.Register("stable-id", "stable-host:9000")
	reg.Register("reconnect-id-v1", "reconnect-host:9000")

	if n := reg.Active(); n != 2 {
		t.Fatalf("initial: want Active=2, got %d", n)
	}

	// Reconnect the second worker — must not touch the first.
	reg.Register("reconnect-id-v2", "reconnect-host:9000")

	if n := reg.Active(); n != 2 {
		t.Errorf("after reconnect: want Active=2, got %d", n)
	}
	_, _, ok := reg.GetSendCh("stable-id")
	if !ok {
		t.Error("stable-id was incorrectly evicted by addr-dedup on different addr")
	}
}
