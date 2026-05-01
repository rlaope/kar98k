//go:build ha_etcd

package rpc

import (
	"fmt"
	"time"
)

// BuildHAStore constructs an HAStore from spec. The ha_etcd build adds
// support for the "etcd" backend; "memory" and "" still resolve to
// InMemoryStore for tests / mixed deployments.
func BuildHAStore(spec HAStoreSpec) (HAStore, error) {
	ttl := spec.LeaseTTL
	if ttl <= 0 {
		ttl = 5 * time.Second
	}
	switch spec.Backend {
	case "", "memory":
		return NewInMemoryStore(ttl), nil
	case "etcd":
		if len(spec.Endpoints) == 0 {
			return nil, fmt.Errorf("etcd backend requires --ha-endpoints")
		}
		return NewEtcdStore(spec.Endpoints, spec.LeaseKey, ttl)
	default:
		return nil, fmt.Errorf("%w: unknown backend %q", ErrUnsupportedBackend, spec.Backend)
	}
}
