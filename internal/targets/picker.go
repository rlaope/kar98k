package targets

import (
	"math/rand"
	"sync"
	"time"

	"github.com/kar98k/internal/config"
)

// Picker performs weighted random selection across a fixed target set.
// It is safe for concurrent use; the embedded RNG is mutex-guarded.
//
// The set is captured at construction time — callers that need to mutate
// targets must build a new Picker.
type Picker struct {
	targets     []config.Target
	totalWeight int

	mu  sync.Mutex
	rng *rand.Rand
}

// New builds a Picker over the supplied targets, summing positive
// weights into the cumulative distribution. Targets with zero or
// negative weight are kept in the slice but contribute nothing to
// selection — Pick will never return them.
func New(targets []config.Target) *Picker {
	return NewWithSeed(targets, time.Now().UnixNano())
}

// NewWithSeed is New with a caller-supplied RNG seed. Tests use this
// to get deterministic selection sequences.
func NewWithSeed(targets []config.Target, seed int64) *Picker {
	total := 0
	for _, t := range targets {
		if t.Weight > 0 {
			total += t.Weight
		}
	}
	return &Picker{
		targets:     targets,
		totalWeight: total,
		rng:         rand.New(rand.NewSource(seed)),
	}
}

// Pick returns a pointer to one target chosen by weight, or nil when
// the target set is empty or carries no positive weight.
//
// The returned pointer addresses the Picker's internal slice — callers
// must not mutate the underlying Target.
func (p *Picker) Pick() *config.Target {
	if p == nil || p.totalWeight <= 0 || len(p.targets) == 0 {
		return nil
	}

	p.mu.Lock()
	r := p.rng.Intn(p.totalWeight)
	p.mu.Unlock()

	cumulative := 0
	for i := range p.targets {
		w := p.targets[i].Weight
		if w <= 0 {
			continue
		}
		cumulative += w
		if r < cumulative {
			return &p.targets[i]
		}
	}
	// Why: numerical safety net — totalWeight is computed under the
	// same loop semantics, so r < totalWeight should always hit above.
	for i := range p.targets {
		if p.targets[i].Weight > 0 {
			return &p.targets[i]
		}
	}
	return nil
}

// Len returns the number of targets in the underlying set, including
// zero-weight entries.
func (p *Picker) Len() int {
	if p == nil {
		return 0
	}
	return len(p.targets)
}
