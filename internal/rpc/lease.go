package rpc

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"
)

// Lease holds the durable state of a master HA lease. Audit metadata
// (LastTransferredBy / LastTransferredAt) records who handed off the
// lease and when so post-incident review can reconstruct the failover
// chain. Fence is a Kleppmann-pattern monotonic token: a lease taken
// by a later holder MUST have a strictly higher fence than any prior
// lease, so a stale primary that resumes after a partition cannot
// successfully write under the old fence.
type Lease struct {
	Holder            string
	Fence             uint64
	LastTransferredBy string
	LastTransferredAt time.Time
	lastRenew         time.Time
}

// HAStore is the pluggable coordination backend for master HA. The
// default production backend is etcd (build-tagged ha_etcd); an
// in-memory backend is shipped for tests. NO file-based backend
// exists — files cannot provide fencing across split-brain.
//
// Implementations MUST guarantee:
//   - AcquireLease fails when an unexpired holder exists (holder != "" and
//     time.Since(lastRenew) < leaseTTL). ForceLeaseTransfer is the only
//     way to override.
//   - Fence is strictly monotonically increasing across the lifetime of
//     the store, including across restarts (etcd does this via Revision;
//     InMemoryStore does it via a counter).
//   - RenewLease/ReleaseLease return ErrFenceConflict when fence does
//     not match the current holder's fence.
//   - ForceLeaseTransfer to "" releases the lease without requiring a
//     fence (graceful shutdown path so SIGTERM doesn't have to remember
//     its own fence).
type HAStore interface {
	AcquireLease(ctx context.Context, holderID string) (fence uint64, err error)
	RenewLease(ctx context.Context, fence uint64) error
	ReleaseLease(ctx context.Context, fence uint64) error
	ForceLeaseTransfer(ctx context.Context, toHolderID string) error
	ReadCurrentHolder(ctx context.Context) (holderID string, fence uint64, err error)
}

// ErrFenceConflict is returned by Renew/Release when the supplied fence
// does not match the current lease — typically the caller has been
// fenced out by another holder.
var ErrFenceConflict = errors.New("ha: fence conflict (lease held by another fence)")

// ErrLeaseUnavailable is returned by AcquireLease when an unexpired
// holder already owns the lease.
var ErrLeaseUnavailable = errors.New("ha: lease currently held by another holder")

// ErrLeaseAcquireFailed is returned by HALeaseManager.Run when initial
// acquire times out — the supervisor should restart the process.
var ErrLeaseAcquireFailed = errors.New("ha: lease acquire timed out")

// InMemoryStore is an in-process HAStore intended for tests. It is NOT
// safe to share across processes — there is no actual coordination,
// only a sync.Mutex. A single InMemoryStore in a real deployment has
// the same SPOF as a single master with no HA.
type InMemoryStore struct {
	mu        sync.Mutex
	leaseTTL  time.Duration
	current   Lease
	fenceSeed uint64
}

// NewInMemoryStore constructs an InMemoryStore. leaseTTL is how long a
// silent (no-renew) holder retains the lease before it becomes
// available to other acquirers.
func NewInMemoryStore(leaseTTL time.Duration) *InMemoryStore {
	return &InMemoryStore{leaseTTL: leaseTTL}
}

// AcquireLease takes the lease for holderID, returning a fresh fence.
// Fails with ErrLeaseUnavailable when another live holder owns it.
func (s *InMemoryStore) AcquireLease(ctx context.Context, holderID string) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.current.Holder != "" && time.Since(s.current.lastRenew) < s.leaseTTL {
		return 0, ErrLeaseUnavailable
	}
	s.fenceSeed++
	s.current = Lease{
		Holder:            holderID,
		Fence:             s.fenceSeed,
		lastRenew:         time.Now(),
		LastTransferredBy: s.current.Holder, // who held the lease before
		LastTransferredAt: time.Now(),
	}
	return s.fenceSeed, nil
}

// RenewLease extends the current lease's TTL. Returns ErrFenceConflict
// when fence does not match the active holder.
func (s *InMemoryStore) RenewLease(ctx context.Context, fence uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.current.Holder == "" || s.current.Fence != fence {
		return ErrFenceConflict
	}
	s.current.lastRenew = time.Now()
	return nil
}

// ReleaseLease clears the lease when fence matches.
func (s *InMemoryStore) ReleaseLease(ctx context.Context, fence uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.current.Holder == "" || s.current.Fence != fence {
		return ErrFenceConflict
	}
	s.current = Lease{
		LastTransferredBy: s.current.Holder,
		LastTransferredAt: time.Now(),
	}
	return nil
}

// ForceLeaseTransfer overrides the current holder. Passing "" releases
// the lease without requiring a fence (graceful shutdown path).
func (s *InMemoryStore) ForceLeaseTransfer(ctx context.Context, toHolderID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	prev := s.current.Holder
	if toHolderID == "" {
		s.current = Lease{
			LastTransferredBy: prev,
			LastTransferredAt: time.Now(),
		}
		return nil
	}
	s.fenceSeed++
	s.current = Lease{
		Holder:            toHolderID,
		Fence:             s.fenceSeed,
		lastRenew:         time.Now(),
		LastTransferredBy: prev,
		LastTransferredAt: time.Now(),
	}
	return nil
}

// ReadCurrentHolder returns the holderID and fence of the live lease,
// or ("", 0, nil) when nobody holds it.
func (s *InMemoryStore) ReadCurrentHolder(ctx context.Context) (string, uint64, error) {
	if err := ctx.Err(); err != nil {
		return "", 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current.Holder == "" || time.Since(s.current.lastRenew) >= s.leaseTTL {
		return "", 0, nil
	}
	return s.current.Holder, s.current.Fence, nil
}

// HALeaseManager wraps a HAStore with the renew loop, self-fence
// callback, and graceful transfer protocol used by GRPCServer to gate
// Serve on lease acquisition. Construct with NewHALeaseManager and
// drive with Run(ctx). On RenewLease error the manager invokes
// onLost(reason) so the embedding server can self-fence (typically
// grpcServer.GracefulStop in a goroutine).
type HALeaseManager struct {
	Store         HAStore
	HolderID      string
	RenewInterval time.Duration
	LeaseTTL      time.Duration

	// AcquireTimeout caps the initial AcquireLease attempt; on timeout
	// Run returns ErrLeaseAcquireFailed so the supervisor can restart.
	AcquireTimeout time.Duration

	// OnLost is called once when the renew loop detects the lease is
	// gone. The embedding GRPCServer wires this to GracefulStop.
	OnLost func(reason string)

	mu    sync.Mutex
	fence uint64
}

// NewHALeaseManager constructs a manager with sane defaults. RenewInterval
// of 1s and LeaseTTL of 5s are conservative — failover SLA is bounded
// by LeaseTTL plus AcquireTimeout on the standby.
func NewHALeaseManager(store HAStore, holderID string) *HALeaseManager {
	return &HALeaseManager{
		Store:          store,
		HolderID:       holderID,
		RenewInterval:  1 * time.Second,
		LeaseTTL:       5 * time.Second,
		AcquireTimeout: 5 * time.Second,
	}
}

// Run blocks until the lease is lost or ctx is cancelled. On lease loss
// it invokes OnLost (if set) and returns nil. On initial-acquire timeout
// it returns ErrLeaseAcquireFailed.
func (m *HALeaseManager) Run(ctx context.Context) error {
	acqCtx, cancel := context.WithTimeout(ctx, m.AcquireTimeout)
	fence, err := m.acquireWithRetry(acqCtx)
	cancel()
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.fence = fence
	m.mu.Unlock()
	log.Printf("[ha] lease acquired by %q fence=%d", m.HolderID, fence)

	ticker := time.NewTicker(m.RenewInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := m.Store.RenewLease(ctx, fence); err != nil {
				reason := fmt.Sprintf("renew failed: %v", err)
				log.Printf("[ha] %s — self-fencing", reason)
				if m.OnLost != nil {
					m.OnLost(reason)
				}
				return nil
			}
		}
	}
}

// acquireWithRetry retries AcquireLease at RenewInterval until ctx is
// done or the call succeeds.
func (m *HALeaseManager) acquireWithRetry(ctx context.Context) (uint64, error) {
	for {
		fence, err := m.Store.AcquireLease(ctx, m.HolderID)
		if err == nil {
			return fence, nil
		}
		if !errors.Is(err, ErrLeaseUnavailable) {
			return 0, err
		}
		select {
		case <-ctx.Done():
			return 0, ErrLeaseAcquireFailed
		case <-time.After(m.RenewInterval):
		}
	}
}

// Fence returns the current lease fence (0 if not yet acquired).
func (m *HALeaseManager) Fence() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.fence
}

// TransferAndRelease hands the lease to toHolderID (use "" for plain
// release). Used by GRPCServer.Stop on SIGTERM so a standby can
// acquire within ~LeaseTTL even before the primary's TTL expires.
func (m *HALeaseManager) TransferAndRelease(ctx context.Context, toHolderID string) error {
	return m.Store.ForceLeaseTransfer(ctx, toHolderID)
}

// HAStoreSpec is the parsed CLI/config knobs the factory needs to
// construct an HAStore. Endpoints + LeaseKey are etcd-only; LeaseTTL
// is shared.
type HAStoreSpec struct {
	Backend   string // "memory" | "etcd"
	HolderID  string
	Endpoints []string
	LeaseKey  string
	LeaseTTL  time.Duration
}

// ErrUnsupportedBackend is returned by BuildHAStore when a backend is
// requested that isn't compiled into the binary (e.g. "etcd" without
// the ha_etcd build tag).
var ErrUnsupportedBackend = errors.New("ha: backend not supported in this build")
