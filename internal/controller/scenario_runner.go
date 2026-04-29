package controller

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/kar98k/internal/config"
	"github.com/kar98k/internal/pattern"
)

// ScenarioRunner advances a Controller through a sequence of phases on
// a wall-clock timeline. Each phase has a duration; when elapsed, the
// runner mutates the engine's TPS bounds and pattern config to match
// the next phase. After the final phase ends the runner stops mutating
// — the engine simply continues with the last applied phase until the
// controller is shut down.
type ScenarioRunner struct {
	scenarios []config.Scenario
	engine    *pattern.Engine
	defaults  scenarioDefaults

	mu          sync.RWMutex
	current     int
	phaseStart  time.Time
	transitions int64
	stopped     bool
}

// scenarioDefaults captures the top-level fallback so a phase that
// omits BaseTPS/MaxTPS/Pattern inherits sane values rather than zeros.
type scenarioDefaults struct {
	baseTPS float64
	maxTPS  float64
	pattern config.Pattern
}

// NewScenarioRunner creates a runner over the given scenarios. The
// engine and default values are used to seed any field a scenario
// leaves zero. NewScenarioRunner does not start ticking — the caller
// must invoke Run.
func NewScenarioRunner(
	scenarios []config.Scenario,
	engine *pattern.Engine,
	defaultBaseTPS, defaultMaxTPS float64,
	defaultPattern config.Pattern,
) *ScenarioRunner {
	return &ScenarioRunner{
		scenarios: scenarios,
		engine:    engine,
		defaults: scenarioDefaults{
			baseTPS: defaultBaseTPS,
			maxTPS:  defaultMaxTPS,
			pattern: defaultPattern,
		},
		current: -1,
	}
}

// Total returns the number of phases. Useful for status surfaces.
func (r *ScenarioRunner) Total() int {
	if r == nil {
		return 0
	}
	return len(r.scenarios)
}

// Run applies the first phase, then ticks through each phase boundary
// until the timeline ends or ctx is cancelled. It is safe to call this
// at most once per runner.
func (r *ScenarioRunner) Run(ctx context.Context) {
	if r == nil || len(r.scenarios) == 0 {
		return
	}

	r.applyPhase(0)
	timer := time.NewTimer(r.scenarios[0].Duration)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			r.markStopped()
			return
		case <-timer.C:
			next := r.nextIndex()
			if next < 0 {
				log.Printf("[scenarios] timeline complete — last phase remains active")
				r.markStopped()
				return
			}
			r.applyPhase(next)
			timer.Reset(r.scenarios[next].Duration)
		}
	}
}

// applyPhase mutates the engine to match the phase at idx and records
// the transition.
func (r *ScenarioRunner) applyPhase(idx int) {
	s := r.scenarios[idx]

	baseTPS := s.BaseTPS
	if baseTPS <= 0 {
		baseTPS = r.defaults.baseTPS
	}
	maxTPS := s.MaxTPS
	if maxTPS <= 0 {
		maxTPS = r.defaults.maxTPS
	}
	pat := r.defaults.pattern
	if s.Pattern != nil {
		pat = *s.Pattern
	}

	r.engine.SetBaseTPS(baseTPS)
	r.engine.SetMaxTPS(maxTPS)
	r.engine.ReplacePattern(pat)

	r.mu.Lock()
	r.current = idx
	r.phaseStart = time.Now()
	r.transitions++
	r.mu.Unlock()

	log.Printf("[scenarios] phase %d/%d %q — baseTPS=%.0f maxTPS=%.0f duration=%s",
		idx+1, len(r.scenarios), s.Name, baseTPS, maxTPS, s.Duration)
}

// nextIndex returns the next valid phase index, or -1 if the timeline
// is exhausted.
func (r *ScenarioRunner) nextIndex() int {
	r.mu.RLock()
	cur := r.current
	r.mu.RUnlock()
	if cur+1 >= len(r.scenarios) {
		return -1
	}
	return cur + 1
}

func (r *ScenarioRunner) markStopped() {
	r.mu.Lock()
	r.stopped = true
	r.mu.Unlock()
}

// ScenarioStatus is the read-only snapshot exposed via the controller
// Status struct. Index is 1-based for human display; zero means the
// runner has not yet started.
type ScenarioStatus struct {
	Name        string
	Index       int
	Total       int
	Elapsed     time.Duration
	Duration    time.Duration
	Transitions int64
	Done        bool
}

// Status returns the current phase snapshot. Returns the zero value
// (Total = 0) when the runner is nil or has no phases — callers can
// treat that as "scenarios mode disabled".
func (r *ScenarioRunner) Status() ScenarioStatus {
	if r == nil || len(r.scenarios) == 0 {
		return ScenarioStatus{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.current < 0 {
		return ScenarioStatus{Total: len(r.scenarios)}
	}
	s := r.scenarios[r.current]
	return ScenarioStatus{
		Name:        s.Name,
		Index:       r.current + 1,
		Total:       len(r.scenarios),
		Elapsed:     time.Since(r.phaseStart),
		Duration:    s.Duration,
		Transitions: r.transitions,
		Done:        r.stopped,
	}
}
