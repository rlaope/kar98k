package script

import (
	"fmt"
	"time"

	hdrhistogram "github.com/HdrHistogram/hdrhistogram-go"
	"go.starlark.net/starlark"
)

// PhaseMetrics holds aggregated metrics for a single named phase.
type PhaseMetrics struct {
	Name          string
	StartTime     time.Time
	EndTime       time.Time
	Histogram     *hdrhistogram.Histogram
	StatusCodes   map[int]int64
	TotalRequests int64
	TotalErrors   int64
}

func newPhaseMetrics(name string, start time.Time) *PhaseMetrics {
	return &PhaseMetrics{
		Name:        name,
		StartTime:   start,
		Histogram:   hdrhistogram.New(1, 60000000, 3),
		StatusCodes: make(map[int]int64),
	}
}

// setPhase transitions the active phase on m. Caller must NOT hold m.mu —
// this method acquires it internally so it is safe to call from script builtins.
func (m *Metrics) setPhase(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()

	// Close out the previous phase's EndTime if one is active.
	if m.currentPhase != "" {
		if idx, ok := m.phaseIndex[m.currentPhase]; ok {
			m.phases[idx].EndTime = now
		}
	}

	if idx, ok := m.phaseIndex[name]; ok {
		// Re-entering a named phase: just switch currentPhase; don't reset
		// its histogram (accumulate across re-entries).
		m.currentPhase = m.phases[idx].Name
		return
	}

	pm := newPhaseMetrics(name, now)
	m.phaseIndex[name] = len(m.phases)
	m.phases = append(m.phases, pm)
	m.currentPhase = name
}

// activePhase returns the current PhaseMetrics pointer under m.mu.
// Returns nil when no phase has been set. Caller must hold m.mu.
func (m *Metrics) activePhase() *PhaseMetrics {
	if m.currentPhase == "" {
		return nil
	}
	idx, ok := m.phaseIndex[m.currentPhase]
	if !ok {
		return nil
	}
	return m.phases[idx]
}

// phaseBuiltinStarlark implements phase("name") for Starlark scripts.
func phaseBuiltinStarlark(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	rt := thread.Local("runtime").(*Runtime)
	if len(args) != 1 {
		return nil, fmt.Errorf("phase: expected 1 argument (name), got %d", len(args))
	}
	name, ok := starlark.AsString(args[0])
	if !ok {
		return nil, fmt.Errorf("phase: name must be a string, got %s", args[0].Type())
	}
	rt.metrics.setPhase(name)
	return starlark.None, nil
}
