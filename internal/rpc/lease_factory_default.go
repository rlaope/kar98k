//go:build !ha_etcd

package rpc

import (
	"fmt"
	"time"
)

// BuildHAStore constructs an HAStore from spec. The default build only
// supports "memory" (and treats "" as memory for convenience). Production
// deployments needing the etcd backend must build with `-tags ha_etcd`,
// which swaps in a different BuildHAStore that handles "etcd".
func BuildHAStore(spec HAStoreSpec) (HAStore, error) {
	ttl := spec.LeaseTTL
	if ttl <= 0 {
		ttl = 5 * time.Second
	}
	switch spec.Backend {
	case "", "memory":
		return NewInMemoryStore(ttl), nil
	case "etcd":
		return nil, fmt.Errorf("%w: rebuild with -tags ha_etcd to enable the etcd backend", ErrUnsupportedBackend)
	default:
		return nil, fmt.Errorf("%w: unknown backend %q", ErrUnsupportedBackend, spec.Backend)
	}
}
