package rpc

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// TestGRPCServer_SelfFenceOnLeaseLoss verifies that when the HA lease is
// taken away, the server stops accepting connections within 1.5s. This
// is the hard SLA from #72: detect+self-fence within 1s, listener
// refuses within 1.5s.
func TestGRPCServer_SelfFenceOnLeaseLoss(t *testing.T) {
	store := NewInMemoryStore(500 * time.Millisecond)
	mgr := NewHALeaseManager(store, "primary-1")
	mgr.RenewInterval = 50 * time.Millisecond
	mgr.LeaseTTL = 200 * time.Millisecond
	mgr.AcquireTimeout = 1 * time.Second

	var failoverCount int32
	var detectedAtUnixNano int64 // populated when onFailover fires
	reg := NewWorkerRegistry()
	defer reg.Stop()

	srv, err := NewGRPCServer("127.0.0.1:0", reg,
		WithHALease(mgr, func() {
			// Capture detection time so the test can enforce the ≤1s
			// architectural budget (renew tick + GracefulStop dispatch).
			atomic.StoreInt64(&detectedAtUnixNano, time.Now().UnixNano())
			atomic.AddInt32(&failoverCount, 1)
		}))
	if err != nil {
		t.Fatalf("NewGRPCServer: %v", err)
	}

	serveDone := make(chan error, 1)
	go func() { serveDone <- srv.Serve() }()

	// Wait for the server to acquire and start accepting.
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", srv.Addr(), 100*time.Millisecond)
		if err == nil {
			c.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if mgr.Fence() == 0 {
		t.Fatal("primary did not acquire lease")
	}

	// Yank the lease away — RenewLease will fail with ErrFenceConflict.
	if err := store.ForceLeaseTransfer(context.Background(), "intruder"); err != nil {
		t.Fatalf("ForceLeaseTransfer: %v", err)
	}
	yankAt := time.Now()

	// Listener must refuse within 1.5s of yank.
	refuseDeadline := yankAt.Add(1500 * time.Millisecond)
	var refusedAt time.Time
	for time.Now().Before(refuseDeadline) {
		c, err := net.DialTimeout("tcp", srv.Addr(), 100*time.Millisecond)
		if err != nil {
			refusedAt = time.Now()
			break
		}
		c.Close()
		time.Sleep(20 * time.Millisecond)
	}
	if refusedAt.IsZero() {
		t.Fatalf("listener still accepting connections 1.5s after lease loss")
	}
	if got := refusedAt.Sub(yankAt); got > 1500*time.Millisecond {
		t.Errorf("self-fence latency too high: %v (budget 1.5s)", got)
	}
	// Detection (onFailover dispatch) must happen ≤1s of yank — this is the
	// hard architectural budget. The full listener-refuses path adds GracefulStop
	// drain time, hence the looser 1.5s above.
	detected := time.Unix(0, atomic.LoadInt64(&detectedAtUnixNano))
	if detected.IsZero() {
		t.Fatal("onFailover never recorded a detection time")
	}
	if got := detected.Sub(yankAt); got > 1*time.Second {
		t.Errorf("self-fence DETECTION latency too high: %v (budget 1s)", got)
	}
	if atomic.LoadInt32(&failoverCount) != 1 {
		t.Errorf("expected onFailover called exactly once, got %d", failoverCount)
	}

	// Serve must return cleanly.
	select {
	case <-serveDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after self-fence")
	}
}

// TestGRPCServer_GracefulStopReleasesLease verifies that Stop() invokes
// TransferAndRelease so a standby can pick up the lease quickly.
func TestGRPCServer_GracefulStopReleasesLease(t *testing.T) {
	store := NewInMemoryStore(5 * time.Second)
	mgr := NewHALeaseManager(store, "primary-1")
	mgr.RenewInterval = 50 * time.Millisecond
	mgr.AcquireTimeout = 1 * time.Second

	reg := NewWorkerRegistry()
	defer reg.Stop()

	srv, err := NewGRPCServer("127.0.0.1:0", reg, WithHALease(mgr, nil))
	if err != nil {
		t.Fatalf("NewGRPCServer: %v", err)
	}

	go func() { _ = srv.Serve() }()

	// Wait for lease acquisition.
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) && mgr.Fence() == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if mgr.Fence() == 0 {
		t.Fatal("primary did not acquire lease")
	}

	srv.Stop()

	// Holder should be empty after graceful stop.
	holder, _, _ := store.ReadCurrentHolder(context.Background())
	if holder != "" {
		t.Fatalf("after graceful Stop, holder should be empty, got %q", holder)
	}
}

// TestGRPCServer_StandbyAcquiresAfterPrimaryStops simulates the
// graceful-handoff happy path: primary acquires + serves, then Stops;
// standby's HALeaseManager.Run unblocks and acquires within ~LeaseTTL.
func TestGRPCServer_StandbyAcquiresAfterPrimaryStops(t *testing.T) {
	store := NewInMemoryStore(500 * time.Millisecond)
	primary := NewHALeaseManager(store, "primary")
	primary.RenewInterval = 50 * time.Millisecond
	primary.AcquireTimeout = 1 * time.Second
	standby := NewHALeaseManager(store, "standby")
	standby.RenewInterval = 50 * time.Millisecond
	standby.AcquireTimeout = 3 * time.Second

	primaryReg := NewWorkerRegistry()
	defer primaryReg.Stop()
	primarySrv, err := NewGRPCServer("127.0.0.1:0", primaryReg, WithHALease(primary, nil))
	if err != nil {
		t.Fatalf("primary NewGRPCServer: %v", err)
	}
	go func() { _ = primarySrv.Serve() }()

	// Wait for primary's acquisition.
	for primary.Fence() == 0 {
		time.Sleep(20 * time.Millisecond)
	}

	standbyDone := make(chan error, 1)
	standbyCtx, standbyCancel := context.WithCancel(context.Background())
	defer standbyCancel()
	go func() { standbyDone <- standby.Run(standbyCtx) }()

	// Standby should NOT have acquired yet.
	time.Sleep(100 * time.Millisecond)
	if standby.Fence() != 0 {
		t.Fatal("standby acquired while primary still holds — should have been blocked")
	}

	// Primary stops gracefully → standby should acquire ≤2s.
	primarySrv.Stop()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && standby.Fence() == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if standby.Fence() == 0 {
		t.Fatal("standby did not acquire within 2s of primary graceful Stop")
	}
}
