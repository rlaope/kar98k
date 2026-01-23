package pattern

import (
	"math"
	"math/rand"
	"sync"
	"time"

	"github.com/kar98k/internal/config"
)

// PoissonSpike generates traffic spikes using Poisson distribution.
type PoissonSpike struct {
	cfg config.Poisson
	rng *rand.Rand
	mu  sync.Mutex

	// Spike state
	spiking       bool
	spikeStart    time.Time
	spikePeak     time.Time
	spikeEnd      time.Time
	nextSpikeTime time.Time

	// Manual spike state
	manualSpike       bool
	manualSpikeFactor float64
	manualSpikeDuration time.Duration
}

// NewPoissonSpike creates a new Poisson spike generator.
func NewPoissonSpike(cfg config.Poisson) *PoissonSpike {
	// If interval is set, convert to lambda (lambda = 1/interval_seconds)
	if cfg.Interval > 0 {
		cfg.Lambda = 1.0 / cfg.Interval.Seconds()
	}

	p := &PoissonSpike{
		cfg: cfg,
		rng: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
	p.scheduleNextSpike()
	return p
}

// TriggerManualSpike triggers a manual spike with optional custom factor and duration.
// If factor is 0, uses the configured spike_factor.
// If duration is 0, uses the configured ramp_up + ramp_down.
func (p *PoissonSpike) TriggerManualSpike(factor float64, duration time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if factor == 0 {
		factor = p.cfg.SpikeFactor
	}
	if duration == 0 {
		duration = p.cfg.RampUp + p.cfg.RampDown
	}

	p.manualSpike = true
	p.manualSpikeFactor = factor
	p.manualSpikeDuration = duration

	now := time.Now()
	p.spiking = true
	p.spikeStart = now
	p.spikePeak = now.Add(duration / 3)        // 1/3 for ramp up
	p.spikeEnd = now.Add(duration)
}

// IsManualSpike returns whether a manual spike is currently active.
func (p *PoissonSpike) IsManualSpike() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.manualSpike && p.spiking
}

// Multiplier returns the current TPS multiplier based on spike state.
func (p *PoissonSpike) Multiplier() float64 {
	if !p.cfg.Enabled && !p.manualSpike {
		return 1.0
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()

	// Check if current spike has ended
	if p.spiking && now.After(p.spikeEnd) {
		p.spiking = false
		p.manualSpike = false
		p.scheduleNextSpike()
	}

	// Check if we should start a new automatic spike (only if not in manual spike)
	if p.cfg.Enabled && !p.spiking && !p.manualSpike && now.After(p.nextSpikeTime) {
		p.startSpike(now)
	}

	if !p.spiking {
		return 1.0
	}

	return p.calculateSpikeMultiplier(now)
}

// startSpike initiates a new spike event.
func (p *PoissonSpike) startSpike(now time.Time) {
	p.spiking = true
	p.spikeStart = now
	p.spikePeak = now.Add(p.cfg.RampUp)
	p.spikeEnd = p.spikePeak.Add(p.cfg.RampDown)
}

// calculateSpikeMultiplier computes the multiplier based on spike phase.
func (p *PoissonSpike) calculateSpikeMultiplier(now time.Time) float64 {
	// Use manual spike factor if this is a manual spike
	spikeFactor := p.cfg.SpikeFactor
	if p.manualSpike && p.manualSpikeFactor > 0 {
		spikeFactor = p.manualSpikeFactor
	}

	if now.Before(p.spikePeak) {
		// Ramp-up phase: linear increase to spike factor
		elapsed := now.Sub(p.spikeStart).Seconds()
		total := p.spikePeak.Sub(p.spikeStart).Seconds()
		if total == 0 {
			total = 1
		}
		progress := elapsed / total
		return 1.0 + (spikeFactor-1.0)*progress
	}

	// Ramp-down phase: exponential decay back to 1.0
	elapsed := now.Sub(p.spikePeak).Seconds()
	total := p.spikeEnd.Sub(p.spikePeak).Seconds()
	if total == 0 {
		total = 1
	}
	progress := elapsed / total

	// Exponential decay: spike_factor * e^(-3*progress) approaches 1.0
	decay := math.Exp(-3 * progress)
	return 1.0 + (spikeFactor-1.0)*decay
}

// scheduleNextSpike calculates when the next spike should occur.
// Uses inverse transform sampling: t = -ln(U) / lambda
func (p *PoissonSpike) scheduleNextSpike() {
	// Generate exponentially distributed inter-arrival time
	u := p.rng.Float64()
	if u == 0 {
		u = 1e-10 // Avoid log(0)
	}
	interval := -math.Log(u) / p.cfg.Lambda

	// Clamp interval to configured bounds
	minSec := p.cfg.MinInterval.Seconds()
	maxSec := p.cfg.MaxInterval.Seconds()

	if interval < minSec {
		interval = minSec
	}
	if interval > maxSec {
		interval = maxSec
	}

	p.nextSpikeTime = time.Now().Add(time.Duration(interval * float64(time.Second)))
}

// NextSpikeIn returns the duration until the next spike.
func (p *PoissonSpike) NextSpikeIn() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.spiking {
		return 0
	}
	return time.Until(p.nextSpikeTime)
}

// IsSpiking returns whether a spike is currently active.
func (p *PoissonSpike) IsSpiking() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.spiking
}
