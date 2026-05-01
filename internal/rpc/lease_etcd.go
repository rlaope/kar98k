//go:build ha_etcd

package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

// EtcdStore is the production HA backend (build-tagged ha_etcd). It uses
// etcd lease + a transactional Put for fencing: AcquireLease grants a
// fresh lease and atomically claims the key only when no live holder
// exists. The fence is the etcd ModRevision of the key after our Put —
// strictly monotonic across the cluster's lifetime, satisfying the
// Kleppmann fencing-token invariant.
//
// Renew is performed via clientv3.KeepAliveOnce on the lease so the
// failure mode is symmetric with InMemoryStore: a denied renew (lease
// expired or revoked elsewhere) returns ErrFenceConflict and the
// HALeaseManager self-fences.
type EtcdStore struct {
	client   *clientv3.Client
	leaseKey string
	ttl      time.Duration

	// mu guards leaseID and myFence — they are written by AcquireLease /
	// ReleaseLease and read by RenewLease, which all run from independent
	// goroutines (Run's renew ticker vs Stop's TransferAndRelease).
	mu      sync.Mutex
	leaseID clientv3.LeaseID
	myFence int64
}

// leaseValue is the JSON payload stored at the etcd lease key. The
// holder field is the active holder's ID; LastTransferredBy /
// LastTransferredAt record audit metadata for failover analysis.
type leaseValue struct {
	Holder            string    `json:"holder"`
	LastTransferredBy string    `json:"last_transferred_by,omitempty"`
	LastTransferredAt time.Time `json:"last_transferred_at,omitempty"`
}

// NewEtcdStore builds an etcd-backed HAStore. Endpoints + key + TTL
// come from HAStoreSpec; the dial timeout is fixed at 5 s.
func NewEtcdStore(endpoints []string, leaseKey string, ttl time.Duration) (*EtcdStore, error) {
	if leaseKey == "" {
		leaseKey = "/kar98k/ha/lease"
	}
	if ttl <= 0 {
		ttl = 5 * time.Second
	}
	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   endpoints,
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("etcd dial: %w", err)
	}
	return &EtcdStore{
		client:   cli,
		leaseKey: leaseKey,
		ttl:      ttl,
	}, nil
}

// Close releases the underlying etcd client.
func (s *EtcdStore) Close() error { return s.client.Close() }

// AcquireLease grants a fresh etcd lease and atomically claims the key
// when no live holder exists. The fence returned is the key's
// ModRevision after our Put — strictly monotonic across cluster
// lifetime.
func (s *EtcdStore) AcquireLease(ctx context.Context, holderID string) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	// Grant a fresh lease for our claim.
	gr, err := s.client.Grant(ctx, int64(s.ttl.Seconds()))
	if err != nil {
		return 0, fmt.Errorf("etcd grant: %w", err)
	}

	// Read prior holder for audit metadata.
	gresp, err := s.client.Get(ctx, s.leaseKey)
	if err != nil {
		return 0, fmt.Errorf("etcd get: %w", err)
	}
	var prevHolder string
	if len(gresp.Kvs) > 0 {
		var prev leaseValue
		if jerr := json.Unmarshal(gresp.Kvs[0].Value, &prev); jerr == nil {
			prevHolder = prev.Holder
		}
	}

	val, err := json.Marshal(leaseValue{
		Holder:            holderID,
		LastTransferredBy: prevHolder,
		LastTransferredAt: time.Now().UTC(),
	})
	if err != nil {
		return 0, fmt.Errorf("encode lease value: %w", err)
	}

	// Txn: only claim when key is missing OR existing key has been freed
	// (no associated lease — etcd auto-deletes when lease expires, so an
	// existing key implies a live holder).
	txn := s.client.Txn(ctx).
		If(clientv3.Compare(clientv3.CreateRevision(s.leaseKey), "=", 0)).
		Then(clientv3.OpPut(s.leaseKey, string(val), clientv3.WithLease(gr.ID))).
		Else()
	tr, err := txn.Commit()
	if err != nil {
		return 0, fmt.Errorf("etcd txn: %w", err)
	}
	if !tr.Succeeded {
		// Someone else holds it.
		return 0, ErrLeaseUnavailable
	}

	// Read back to capture ModRevision (our fence).
	gresp2, err := s.client.Get(ctx, s.leaseKey)
	if err != nil {
		return 0, fmt.Errorf("etcd get post-acquire: %w", err)
	}
	if len(gresp2.Kvs) == 0 {
		return 0, fmt.Errorf("etcd: key vanished immediately after acquire")
	}
	fence := gresp2.Kvs[0].ModRevision
	s.mu.Lock()
	s.leaseID = gr.ID
	s.myFence = fence
	s.mu.Unlock()
	return uint64(fence), nil
}

// RenewLease keeps the etcd lease alive AND verifies the key still has
// our ModRevision. The Txn check closes the split-brain window where a
// remote ForceLeaseTransfer rewrote the key under a different lease —
// the original lease can still be keep-alived, but the key it pointed
// to is gone, so we must self-fence. Returns ErrFenceConflict on any
// failure path (expired lease, lost connection, key replaced).
func (s *EtcdStore) RenewLease(ctx context.Context, fence uint64) error {
	s.mu.Lock()
	leaseID := s.leaseID
	myFence := s.myFence
	s.mu.Unlock()

	if uint64(myFence) != fence {
		return ErrFenceConflict
	}

	// First, verify the key still belongs to us by ModRevision.
	tr, err := s.client.Txn(ctx).
		If(clientv3.Compare(clientv3.ModRevision(s.leaseKey), "=", myFence)).
		Then(clientv3.OpGet(s.leaseKey)).
		Commit()
	if err != nil {
		return ErrFenceConflict
	}
	if !tr.Succeeded {
		return ErrFenceConflict
	}

	// Then keep the lease alive. KeepAliveOnce failure means etcd has
	// already revoked our lease (TTL expired or explicit Revoke).
	if _, err := s.client.KeepAliveOnce(ctx, leaseID); err != nil {
		return ErrFenceConflict
	}
	return nil
}

// ReleaseLease deletes the key (and revokes the lease) when fence
// matches.
func (s *EtcdStore) ReleaseLease(ctx context.Context, fence uint64) error {
	s.mu.Lock()
	leaseID := s.leaseID
	myFence := s.myFence
	s.mu.Unlock()

	if uint64(myFence) != fence {
		return ErrFenceConflict
	}
	// Txn-conditioned delete: only delete if ModRevision still matches.
	txn := s.client.Txn(ctx).
		If(clientv3.Compare(clientv3.ModRevision(s.leaseKey), "=", myFence)).
		Then(clientv3.OpDelete(s.leaseKey)).
		Else()
	tr, err := txn.Commit()
	if err != nil {
		return fmt.Errorf("etcd release txn: %w", err)
	}
	if !tr.Succeeded {
		return ErrFenceConflict
	}
	if _, err := s.client.Revoke(ctx, leaseID); err != nil {
		// Best-effort revoke; key already gone.
	}
	s.mu.Lock()
	s.leaseID = 0
	s.myFence = 0
	s.mu.Unlock()
	return nil
}

// ForceLeaseTransfer overrides the key without requiring a fence.
// Passing "" deletes the key (releases the lease).
func (s *EtcdStore) ForceLeaseTransfer(ctx context.Context, toHolderID string) error {
	// Read existing key + lease so we can revoke the prior lease (closing
	// the split-brain window per #72 architect MAJOR-2).
	gresp, err := s.client.Get(ctx, s.leaseKey)
	if err != nil {
		return fmt.Errorf("etcd get: %w", err)
	}
	var prevHolder string
	var prevLease clientv3.LeaseID
	if len(gresp.Kvs) > 0 {
		var prev leaseValue
		if jerr := json.Unmarshal(gresp.Kvs[0].Value, &prev); jerr == nil {
			prevHolder = prev.Holder
		}
		prevLease = clientv3.LeaseID(gresp.Kvs[0].Lease)
	}

	if toHolderID == "" {
		if _, err := s.client.Delete(ctx, s.leaseKey); err != nil {
			return fmt.Errorf("etcd delete: %w", err)
		}
		if prevLease != 0 {
			// Best-effort revoke so the prior holder's KeepAliveOnce fails
			// immediately rather than waiting for TTL.
			_, _ = s.client.Revoke(ctx, prevLease)
		}
		return nil
	}

	gr, err := s.client.Grant(ctx, int64(s.ttl.Seconds()))
	if err != nil {
		return fmt.Errorf("etcd grant: %w", err)
	}
	val, err := json.Marshal(leaseValue{
		Holder:            toHolderID,
		LastTransferredBy: prevHolder,
		LastTransferredAt: time.Now().UTC(),
	})
	if err != nil {
		return fmt.Errorf("encode lease value: %w", err)
	}
	if _, err := s.client.Put(ctx, s.leaseKey, string(val), clientv3.WithLease(gr.ID)); err != nil {
		return fmt.Errorf("etcd put: %w", err)
	}
	if prevLease != 0 {
		// Revoke the previous lease so the original holder's KeepAliveOnce
		// fails on the next renew tick (≤ RenewInterval), eliminating the
		// LeaseTTL-bounded split-brain window.
		_, _ = s.client.Revoke(ctx, prevLease)
	}
	return nil
}

// ReadCurrentHolder returns the holder ID and ModRevision (fence) of
// the live key, or ("", 0, nil) when nobody holds it.
func (s *EtcdStore) ReadCurrentHolder(ctx context.Context) (string, uint64, error) {
	resp, err := s.client.Get(ctx, s.leaseKey)
	if err != nil {
		return "", 0, fmt.Errorf("etcd get: %w", err)
	}
	if len(resp.Kvs) == 0 {
		return "", 0, nil
	}
	var v leaseValue
	if err := json.Unmarshal(resp.Kvs[0].Value, &v); err != nil {
		return "", 0, fmt.Errorf("decode lease value: %w", err)
	}
	return v.Holder, uint64(resp.Kvs[0].ModRevision), nil
}
