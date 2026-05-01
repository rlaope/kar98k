package daemon

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kar98k/internal/rpc"
)

// TestNextMasterAddr_RoundRobin verifies the cursor wraps correctly
// across the list. Single-master case must always return that one
// address (regression for #69 single-master deployments).
func TestNextMasterAddr_RoundRobin(t *testing.T) {
	cases := []struct {
		name  string
		addrs []string
		picks int
		want  []string
	}{
		{"single", []string{"a:1"}, 5, []string{"a:1", "a:1", "a:1", "a:1", "a:1"}},
		{"primary+standby", []string{"a:1", "b:2"}, 6, []string{"a:1", "b:2", "a:1", "b:2", "a:1", "b:2"}},
		{"three", []string{"a:1", "b:2", "c:3"}, 7, []string{"a:1", "b:2", "c:3", "a:1", "b:2", "c:3", "a:1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := NewWorkerDaemonMulti(tc.addrs, "self:0", rpc.ClientOptions{})
			defer d.Stop()
			got := make([]string, 0, tc.picks)
			for i := 0; i < tc.picks; i++ {
				got = append(got, d.nextMasterAddr())
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("pick %d: got %q want %q (full got=%v)", i, got[i], tc.want[i], got)
				}
			}
		})
	}
}

// TestRun_MultiMasterCycle verifies that when both masters are
// unreachable the reconnect attempts ALTERNATE between the two
// addresses. We reuse the MaxAttempts exit path so the test
// terminates quickly.
func TestRun_MultiMasterCycle(t *testing.T) {
	addrs := []string{"127.0.0.1:1", "127.0.0.1:2"} // both will fail to dial
	opts := rpc.ClientOptions{
		BackoffMax:  10 * time.Millisecond,
		MaxAttempts: 4,
	}
	d := NewWorkerDaemonMulti(addrs, "self:0", opts)

	// Capture addrIdx after Run exits so we can assert it advanced as expected.
	// The Run goroutine calls nextMasterAddr exactly MaxAttempts times.
	done := make(chan error, 1)
	go func() { done <- d.Run() }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Run returned nil error after exhausting attempts; expected non-nil")
		}
		if !strings.Contains(err.Error(), "exceeded max reconnect attempts") {
			t.Fatalf("expected max-attempts exit, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		d.Stop()
		t.Fatal("Run did not exit after MaxAttempts=4 with 10ms backoff")
	}

	// addrIdx should equal MaxAttempts (one nextMasterAddr per attempt).
	if int(d.addrIdx) != opts.MaxAttempts {
		t.Errorf("addrIdx = %d, expected %d (one per dial attempt)", d.addrIdx, opts.MaxAttempts)
	}
}

// TestNewWorkerDaemon_SingleMasterRegression verifies the legacy
// single-master constructor still works and addrIdx advances as a
// constant (always pointing at the one address).
func TestNewWorkerDaemon_SingleMasterRegression(t *testing.T) {
	d := NewWorkerDaemon("single:7777", "self:0", rpc.ClientOptions{})
	defer d.Stop()
	if len(d.masterAddrs) != 1 {
		t.Fatalf("expected 1 masterAddr, got %d", len(d.masterAddrs))
	}
	for i := 0; i < 3; i++ {
		if got := d.nextMasterAddr(); got != "single:7777" {
			t.Fatalf("pick %d: got %q want single:7777", i, got)
		}
	}
}

// TestRun_MultiMasterCycle_PartialReachable starts a real TCP listener
// on one of the configured addrs and asserts the dial loop eventually
// reaches it within a few attempts. We can't drive the full RPC
// register flow without a master server, so we just verify that the
// listener accepts a connection (registers in dialAttempts counter).
//
// This complements TestRun_MultiMasterCycle by proving the round-robin
// actually exercises both addresses.
func TestRun_MultiMasterCycle_PartialReachable(t *testing.T) {
	// Start a listener on an OS-assigned port that accepts and immediately
	// drops connections. This will make NewWorkerClient succeed at TCP but
	// the gRPC Register call will fail — incrementing consecutive fails
	// without bailing because MaxAttempts is small.
	ln, err := newAcceptDropListener()
	if err != nil {
		t.Fatalf("listener: %v", err)
	}
	defer ln.Close()

	addrs := []string{
		"127.0.0.1:1", // unreachable
		ln.Addr().String(), // reachable but no master server
	}

	opts := rpc.ClientOptions{
		BackoffMax:  10 * time.Millisecond,
		MaxAttempts: 4,
	}
	d := NewWorkerDaemonMulti(addrs, "self:0", opts)

	done := make(chan error, 1)
	go func() { done <- d.Run() }()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		d.Stop()
		t.Fatal("Run did not exit after MaxAttempts")
	}

	// The reachable addr was picked at attempts #1 (idx 1), #3 (idx 3).
	// Just confirm addrIdx advanced.
	if int(d.addrIdx) < opts.MaxAttempts {
		t.Errorf("addrIdx = %d, expected ≥ %d", d.addrIdx, opts.MaxAttempts)
	}
	if atomic.LoadInt32(&ln.acceptCount) == 0 {
		t.Error("reachable listener never received a connection — round-robin may be broken")
	}
}
