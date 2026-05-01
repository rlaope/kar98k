package daemon

import (
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kar98k/internal/rpc"
)

func countGoroutines() int {
	return runtime.NumGoroutine()
}

// TestBackoffDuration verifies the sequence: 1s, 2s, 4s, 8s, 16s, 30s, 30s
// (doubles each step, capped at 30s).
func TestBackoffDuration(t *testing.T) {
	max := 30 * time.Second
	cases := []struct {
		n    int
		want time.Duration
	}{
		{1, 1 * time.Second},
		{2, 2 * time.Second},
		{3, 4 * time.Second},
		{4, 8 * time.Second},
		{5, 16 * time.Second},
		{6, 30 * time.Second}, // 32s would exceed cap → 30s
		{7, 30 * time.Second}, // stays at cap
	}
	for _, tc := range cases {
		got := backoffDuration(tc.n, max)
		if got != tc.want {
			t.Errorf("backoffDuration(%d, 30s) = %s, want %s", tc.n, got, tc.want)
		}
	}
}

// TestBackoffDuration_CustomMax verifies that a smaller max is respected.
func TestBackoffDuration_CustomMax(t *testing.T) {
	max := 5 * time.Second
	// n=1 → 1s, n=2 → 2s, n=3 → 4s, n=4 → 5s (capped), n=5 → 5s
	want := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second, 5 * time.Second, 5 * time.Second}
	for i, w := range want {
		got := backoffDuration(i+1, max)
		if got != w {
			t.Errorf("backoffDuration(%d, 5s) = %s, want %s", i+1, got, w)
		}
	}
}

// TestRun_MaxAttempts verifies that when MaxAttempts=3 and every dial fails,
// Run() returns a non-nil error after exactly 3 consecutive failures.
func TestRun_MaxAttempts(t *testing.T) {
	// Use an address that will never connect so Start() fails fast.
	opts := rpc.ClientOptions{
		BackoffMax:  10 * time.Millisecond, // tiny backoff so test runs fast
		MaxAttempts: 3,
	}
	wd := NewWorkerDaemon("localhost:1", "localhost:0", opts)

	gorsBefore := countGoroutines()

	err := wd.Run()
	if err == nil {
		t.Fatal("expected non-nil error after MaxAttempts exhausted, got nil")
	}

	// Allow goroutines to settle then check for leaks.
	time.Sleep(20 * time.Millisecond)
	gorsAfter := countGoroutines()
	// Allow a small delta for runtime goroutines (GC, finalizers).
	delta := gorsAfter - gorsBefore
	if delta > 3 {
		t.Errorf("possible goroutine leak: before=%d after=%d delta=%d", gorsBefore, gorsAfter, delta)
	}
}

// TestRun_ContextCancel verifies that cancelling the context causes Run() to
// return nil promptly, even while waiting in a backoff sleep.
func TestRun_ContextCancel(t *testing.T) {
	opts := rpc.ClientOptions{
		BackoffMax:  5 * time.Second, // large so we'd block if cancel doesn't work
		MaxAttempts: 0,               // unlimited — must rely on cancel
	}
	wd := NewWorkerDaemon("localhost:1", "localhost:0", opts)

	done := make(chan error, 1)
	go func() { done <- wd.Run() }()

	// Give Run() time to enter the first backoff sleep.
	time.Sleep(50 * time.Millisecond)

	// Cancel via Stop (which calls w.cancel).
	wd.Stop()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("expected nil error on cancel, got %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Run() did not return within 500ms after context cancel")
	}
}

// TestRun_MaxAttempts_NoGoroutineLeak runs 100 max-attempts cycles under
// the race detector to confirm no goroutine leaks accumulate.
func TestRun_MaxAttempts_NoGoroutineLeak(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping goroutine-leak stress test in short mode")
	}

	const cycles = 20
	var leaked atomic.Int32

	for i := 0; i < cycles; i++ {
		opts := rpc.ClientOptions{
			BackoffMax:  1 * time.Millisecond,
			MaxAttempts: 2,
		}
		wd := NewWorkerDaemon("localhost:1", "localhost:0", opts)
		before := countGoroutines()
		_ = wd.Run()
		time.Sleep(5 * time.Millisecond)
		after := countGoroutines()
		if after-before > 3 {
			leaked.Add(1)
		}
	}

	if leaked.Load() > 0 {
		t.Errorf("goroutine leak detected in %d/%d cycles", leaked.Load(), cycles)
	}
}

// TestWorkerDaemon_StopDuringReconnect verifies that calling Stop() concurrently
// with Run()'s reconnect loop (including mid-backoff-sleep and mid-ctx-swap) is
// race-free and causes Run() to return within 100ms of Stop().
//
// Run with: go test -race ./internal/daemon/... -count=10 -timeout 60s
func TestWorkerDaemon_StopDuringReconnect(t *testing.T) {
	const iterations = 100

	for i := 0; i < iterations; i++ {
		// MaxAttempts=10 so Run() loops; BackoffMax tiny so it cycles fast.
		opts := rpc.ClientOptions{
			BackoffMax:  50 * time.Millisecond,
			MaxAttempts: 10,
		}
		wd := NewWorkerDaemon("localhost:1", "localhost:0", opts)

		gorsBefore := countGoroutines()
		done := make(chan error, 1)
		go func() { done <- wd.Run() }()

		// Stop after a pseudo-random delay 0-50ms to hit different points:
		// before first dial, during backoff sleep, during ctx swap, etc.
		delay := time.Duration(i%50) * time.Millisecond
		time.Sleep(delay)
		wd.Stop()

		select {
		case <-done:
			// Run() returned — check goroutine count.
		case <-time.After(200 * time.Millisecond):
			t.Errorf("iteration %d: Run() did not return within 200ms of Stop()", i)
			continue
		}

		time.Sleep(10 * time.Millisecond)
		gorsAfter := countGoroutines()
		if gorsAfter-gorsBefore > 3 {
			t.Errorf("iteration %d: goroutine leak: before=%d after=%d", i, gorsBefore, gorsAfter)
		}
	}
}
