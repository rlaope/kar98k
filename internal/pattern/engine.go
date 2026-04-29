package pattern

import (
	"sync"
	"time"

	"github.com/kar98k/internal/config"
)

// Engine combines all traffic pattern generators.
type Engine struct {
	poisson *PoissonSpike
	noise   NoiseGenerator
	baseTPS float64
	maxTPS  float64
	mu      sync.RWMutex
}

// NewEngine creates a new pattern engine.
func NewEngine(cfg config.Pattern, baseTPS, maxTPS float64) *Engine {
	return &Engine{
		poisson: NewPoissonSpike(cfg.Poisson),
		noise:   NewNoiseGenerator(cfg.Noise),
		baseTPS: baseTPS,
		maxTPS:  maxTPS,
	}
}

// CalculateTPS computes the current target TPS based on all pattern generators.
func (e *Engine) CalculateTPS(scheduleMultiplier float64) float64 {
	e.mu.RLock()
	baseTPS := e.baseTPS
	maxTPS := e.maxTPS
	e.mu.RUnlock()

	// Start with base TPS and apply schedule multiplier
	tps := baseTPS * scheduleMultiplier

	// Apply Poisson spike multiplier
	poissonMult := e.poisson.Multiplier()
	tps *= poissonMult

	// Apply noise multiplier
	noiseMult := e.noise.Multiplier()
	tps *= noiseMult

	// Clamp to max TPS
	if tps > maxTPS {
		tps = maxTPS
	}

	// Ensure minimum TPS of 1
	if tps < 1 {
		tps = 1
	}

	return tps
}

// SetBaseTPS updates the base TPS value.
func (e *Engine) SetBaseTPS(tps float64) {
	e.mu.Lock()
	e.baseTPS = tps
	e.mu.Unlock()
}

// SetMaxTPS updates the maximum TPS value.
func (e *Engine) SetMaxTPS(tps float64) {
	e.mu.Lock()
	e.maxTPS = tps
	e.mu.Unlock()
}

// ReplacePattern hot-swaps the Poisson and noise generators while the
// engine keeps running. Used by the scenario runner so multi-stage
// scripts (warmup → baseline → spike-train → cooldown) can reshape
// the traffic curve without tearing down the engine.
func (e *Engine) ReplacePattern(cfg config.Pattern) {
	poisson := NewPoissonSpike(cfg.Poisson)
	noise := NewNoiseGenerator(cfg.Noise)
	e.mu.Lock()
	e.poisson = poisson
	e.noise = noise
	e.mu.Unlock()
}

// GetBaseTPS returns the current base TPS.
func (e *Engine) GetBaseTPS() float64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.baseTPS
}

// GetMaxTPS returns the current max TPS.
func (e *Engine) GetMaxTPS() float64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.maxTPS
}

// IsSpiking returns whether a Poisson spike is active.
func (e *Engine) IsSpiking() bool {
	return e.poisson.IsSpiking()
}

// TriggerManualSpike triggers a manual spike with optional custom factor and duration.
func (e *Engine) TriggerManualSpike(factor float64, duration time.Duration) {
	e.poisson.TriggerManualSpike(factor, duration)
}

// IsManualSpike returns whether a manual spike is currently active.
func (e *Engine) IsManualSpike() bool {
	return e.poisson.IsManualSpike()
}

// SpikeKind describes whether a spike is currently active and how it
// was triggered. "none" — no spike active. "auto" — Poisson-scheduled
// spike. "manual" — operator-triggered via TriggerManualSpike.
type SpikeKind string

const (
	SpikeKindNone   SpikeKind = "none"
	SpikeKindAuto   SpikeKind = "auto"
	SpikeKindManual SpikeKind = "manual"
)

// Status returns the current status of all pattern generators.
type Status struct {
	BaseTPS           float64
	MaxTPS            float64
	CurrentTPS        float64
	PoissonEnabled    bool
	PoissonSpiking    bool
	PoissonMultiplier float64
	NoiseEnabled      bool
	NoiseMultiplier   float64

	// SpikeKind reflects whether a spike is currently active and its
	// origin (auto vs manual). Useful for distinguishing "spiking now
	// (manual)" from "spiking now (auto)" in user-facing surfaces.
	SpikeKind SpikeKind
	// NextSpikeIn is the duration until the next scheduled (auto)
	// spike. Zero while a spike is currently active.
	NextSpikeIn time.Duration
}

// GetStatus returns the current status of the pattern engine.
func (e *Engine) GetStatus() Status {
	e.mu.RLock()
	defer e.mu.RUnlock()

	// Calculate current TPS (with schedule multiplier = 1.0)
	currentTPS := e.baseTPS * e.poisson.Multiplier() * e.noise.Multiplier()
	if currentTPS > e.maxTPS {
		currentTPS = e.maxTPS
	}

	spiking := e.poisson.IsSpiking()
	kind := SpikeKindNone
	switch {
	case spiking && e.poisson.IsManualSpike():
		kind = SpikeKindManual
	case spiking:
		kind = SpikeKindAuto
	}

	return Status{
		BaseTPS:           e.baseTPS,
		MaxTPS:            e.maxTPS,
		CurrentTPS:        currentTPS,
		PoissonEnabled:    e.poisson.cfg.Enabled,
		PoissonSpiking:    spiking,
		PoissonMultiplier: e.poisson.Multiplier(),
		NoiseEnabled:      e.noise.Enabled(),
		NoiseMultiplier:   e.noise.Multiplier(),
		SpikeKind:         kind,
		NextSpikeIn:       e.poisson.NextSpikeIn(),
	}
}
