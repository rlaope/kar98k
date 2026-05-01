package rpc

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestInMemoryStore_AcquireRenewRelease(t *testing.T) {
	s := NewInMemoryStore(5 * time.Second)
	ctx := context.Background()

	fence, err := s.AcquireLease(ctx, "primary-1")
	if err != nil {
		t.Fatalf("AcquireLease: %v", err)
	}
	if fence == 0 {
		t.Fatal("fence should be non-zero")
	}

	holder, gotFence, err := s.ReadCurrentHolder(ctx)
	if err != nil {
		t.Fatalf("ReadCurrentHolder: %v", err)
	}
	if holder != "primary-1" || gotFence != fence {
		t.Fatalf("read mismatch: got holder=%q fence=%d, want primary-1/%d", holder, gotFence, fence)
	}

	if err := s.RenewLease(ctx, fence); err != nil {
		t.Fatalf("RenewLease: %v", err)
	}
	if err := s.ReleaseLease(ctx, fence); err != nil {
		t.Fatalf("ReleaseLease: %v", err)
	}

	holder, _, _ = s.ReadCurrentHolder(ctx)
	if holder != "" {
		t.Fatalf("after release, holder should be empty, got %q", holder)
	}
}

func TestInMemoryStore_FenceMonotonic(t *testing.T) {
	s := NewInMemoryStore(50 * time.Millisecond)
	ctx := context.Background()

	var prev uint64
	for i := 0; i < 100; i++ {
		fence, err := s.AcquireLease(ctx, "holder")
		if err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
		if fence <= prev {
			t.Fatalf("fence not monotonic: prev=%d got=%d at iter %d", prev, fence, i)
		}
		prev = fence
		if err := s.ReleaseLease(ctx, fence); err != nil {
			t.Fatalf("release %d: %v", i, err)
		}
	}
}

func TestInMemoryStore_ConcurrentAcquire(t *testing.T) {
	s := NewInMemoryStore(50 * time.Millisecond)
	ctx := context.Background()

	const goroutines = 50
	const rounds = 5

	for round := 0; round < rounds; round++ {
		var wg sync.WaitGroup
		var winners int32
		wg.Add(goroutines)
		for i := 0; i < goroutines; i++ {
			go func() {
				defer wg.Done()
				if _, err := s.AcquireLease(ctx, "racer"); err == nil {
					atomic.AddInt32(&winners, 1)
				} else if !errors.Is(err, ErrLeaseUnavailable) {
					t.Errorf("unexpected acquire error: %v", err)
				}
			}()
		}
		wg.Wait()
		if winners != 1 {
			t.Fatalf("round %d: expected exactly 1 winner, got %d", round, winners)
		}
		// Release for the next round.
		_, fence, _ := s.ReadCurrentHolder(ctx)
		if err := s.ReleaseLease(ctx, fence); err != nil {
			t.Fatalf("round %d release: %v", round, err)
		}
	}
}

func TestInMemoryStore_ForceLeaseTransfer(t *testing.T) {
	s := NewInMemoryStore(5 * time.Second)
	ctx := context.Background()

	if _, err := s.AcquireLease(ctx, "A"); err != nil {
		t.Fatalf("acquire A: %v", err)
	}

	// Force transfer A → B even though A's lease is fresh.
	if err := s.ForceLeaseTransfer(ctx, "B"); err != nil {
		t.Fatalf("ForceLeaseTransfer: %v", err)
	}

	holder, _, _ := s.ReadCurrentHolder(ctx)
	if holder != "B" {
		t.Fatalf("after transfer, holder should be B, got %q", holder)
	}

	// Audit metadata: prev holder should be A.
	s.mu.Lock()
	prev := s.current.LastTransferredBy
	s.mu.Unlock()
	if prev != "A" {
		t.Fatalf("LastTransferredBy should be A, got %q", prev)
	}
}

func TestInMemoryStore_ForceLeaseTransfer_ToEmptyReleases(t *testing.T) {
	s := NewInMemoryStore(5 * time.Second)
	ctx := context.Background()

	if _, err := s.AcquireLease(ctx, "A"); err != nil {
		t.Fatalf("acquire A: %v", err)
	}

	if err := s.ForceLeaseTransfer(ctx, ""); err != nil {
		t.Fatalf("ForceLeaseTransfer to empty: %v", err)
	}

	holder, _, _ := s.ReadCurrentHolder(ctx)
	if holder != "" {
		t.Fatalf("after transfer to empty, holder should be empty, got %q", holder)
	}
	s.mu.Lock()
	prev := s.current.LastTransferredBy
	at := s.current.LastTransferredAt
	s.mu.Unlock()
	if prev != "A" {
		t.Fatalf("LastTransferredBy should be A, got %q", prev)
	}
	if at.IsZero() {
		t.Fatal("LastTransferredAt should be recorded")
	}
}

func TestInMemoryStore_StaleFenceReleaseRejected(t *testing.T) {
	s := NewInMemoryStore(5 * time.Second)
	ctx := context.Background()

	fence1, err := s.AcquireLease(ctx, "A")
	if err != nil {
		t.Fatalf("acquire A: %v", err)
	}
	if err := s.ForceLeaseTransfer(ctx, "B"); err != nil {
		t.Fatalf("transfer A→B: %v", err)
	}

	// A tries to release with the OLD fence — must be rejected.
	if err := s.ReleaseLease(ctx, fence1); !errors.Is(err, ErrFenceConflict) {
		t.Fatalf("stale ReleaseLease should fail with ErrFenceConflict, got %v", err)
	}
	if err := s.RenewLease(ctx, fence1); !errors.Is(err, ErrFenceConflict) {
		t.Fatalf("stale RenewLease should fail with ErrFenceConflict, got %v", err)
	}
}

func TestInMemoryStore_TTLExpiry(t *testing.T) {
	s := NewInMemoryStore(50 * time.Millisecond)
	ctx := context.Background()

	if _, err := s.AcquireLease(ctx, "A"); err != nil {
		t.Fatalf("acquire A: %v", err)
	}

	// Wait beyond TTL — A's silent lease should be reclaimable.
	time.Sleep(100 * time.Millisecond)

	fence2, err := s.AcquireLease(ctx, "B")
	if err != nil {
		t.Fatalf("B acquire after TTL: %v", err)
	}
	if fence2 == 0 {
		t.Fatal("B fence must be non-zero")
	}
}

func TestHALeaseManager_RunHappyPath(t *testing.T) {
	s := NewInMemoryStore(500 * time.Millisecond)
	m := NewHALeaseManager(s, "primary-1")
	m.RenewInterval = 50 * time.Millisecond
	m.AcquireTimeout = 1 * time.Second

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- m.Run(ctx)
	}()

	// Let several renews tick.
	time.Sleep(200 * time.Millisecond)
	if m.Fence() == 0 {
		t.Fatal("fence should be set after Run starts")
	}
	cancel()

	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestHALeaseManager_OnLostFiresOnRenewError(t *testing.T) {
	s := NewInMemoryStore(500 * time.Millisecond)
	m := NewHALeaseManager(s, "primary-1")
	m.RenewInterval = 25 * time.Millisecond
	m.AcquireTimeout = 1 * time.Second

	lostCh := make(chan string, 1)
	m.OnLost = func(reason string) { lostCh <- reason }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = m.Run(ctx) }()

	// Wait for initial acquire.
	time.Sleep(50 * time.Millisecond)
	if m.Fence() == 0 {
		t.Fatal("primary should have acquired")
	}

	// Forcibly transfer the lease away — RenewLease will fail with ErrFenceConflict.
	if err := s.ForceLeaseTransfer(ctx, "intruder"); err != nil {
		t.Fatalf("ForceLeaseTransfer: %v", err)
	}

	// onLost MUST fire within 1 second (next renew + slack).
	select {
	case reason := <-lostCh:
		if reason == "" {
			t.Fatal("onLost called with empty reason")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("onLost did not fire within 1s of lease loss")
	}
}

// TestHALeaseManager_ConcurrentRun_OneActiveAtATime spins up two
// HALeaseManager instances racing to acquire the same lease. Only one
// must successfully transition into the renew loop at any moment; on
// graceful release the other must take over within ≤LeaseTTL. This
// stresses the architectural invariant that the InMemoryStore contract
// (and by extension HALeaseManager) prevents split-brain.
func TestHALeaseManager_ConcurrentRun_OneActiveAtATime(t *testing.T) {
	store := NewInMemoryStore(150 * time.Millisecond)
	a := NewHALeaseManager(store, "A")
	a.RenewInterval = 25 * time.Millisecond
	a.AcquireTimeout = 3 * time.Second
	b := NewHALeaseManager(store, "B")
	b.RenewInterval = 25 * time.Millisecond
	b.AcquireTimeout = 3 * time.Second

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = a.Run(ctx) }()
	go func() { _ = b.Run(ctx) }()

	// Wait for one of them to acquire.
	deadline := time.Now().Add(1 * time.Second)
	var leader, follower *HALeaseManager
	for time.Now().Before(deadline) {
		af, bf := a.Fence(), b.Fence()
		if af != 0 && bf == 0 {
			leader, follower = a, b
			break
		}
		if bf != 0 && af == 0 {
			leader, follower = b, a
			break
		}
		if af != 0 && bf != 0 {
			t.Fatalf("BOTH managers hold lease (split-brain): a=%d b=%d", af, bf)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if leader == nil {
		t.Fatal("neither manager acquired within 1s")
	}

	// Leader hands off explicitly via TransferAndRelease.
	if err := leader.TransferAndRelease(ctx, ""); err != nil {
		t.Fatalf("TransferAndRelease: %v", err)
	}

	// Follower must take over within LeaseTTL slack.
	deadline = time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if follower.Fence() != 0 {
			cancel()
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("follower did not acquire within 1s of leader release")
}

func TestHALeaseManager_TransferAndRelease(t *testing.T) {
	s := NewInMemoryStore(5 * time.Second)
	m := NewHALeaseManager(s, "primary-1")
	m.RenewInterval = 25 * time.Millisecond
	m.AcquireTimeout = 1 * time.Second

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = m.Run(ctx) }()
	time.Sleep(50 * time.Millisecond)

	if err := m.TransferAndRelease(ctx, ""); err != nil {
		t.Fatalf("TransferAndRelease: %v", err)
	}

	holder, _, _ := s.ReadCurrentHolder(ctx)
	if holder != "" {
		t.Fatalf("after TransferAndRelease, holder should be empty, got %q", holder)
	}
}
