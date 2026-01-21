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
}

// NewPoissonSpike creates a new Poisson spike generator.
func NewPoissonSpike(cfg config.Poisson) *PoissonSpike {
	p := &PoissonSpike{
		cfg: cfg,
		rng: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
	p.scheduleNextSpike()
	return p
}

// Multiplier returns the current TPS multiplier based on spike state.
func (p *PoissonSpike) Multiplier() float64 {
	if !p.cfg.Enabled {
		return 1.0
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()

	// Check if we should start a new spike
	if !p.spiking && now.After(p.nextSpikeTime) {
		p.startSpike(now)
	}

	// Check if current spike has ended
	if p.spiking && now.After(p.spikeEnd) {
		p.spiking = false
		p.scheduleNextSpike()
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
	if now.Before(p.spikePeak) {
		// Ramp-up phase: linear increase to spike factor
		elapsed := now.Sub(p.spikeStart).Seconds()
		total := p.cfg.RampUp.Seconds()
		progress := elapsed / total
		return 1.0 + (p.cfg.SpikeFactor-1.0)*progress
	}

	// Ramp-down phase: exponential decay back to 1.0
	elapsed := now.Sub(p.spikePeak).Seconds()
	total := p.cfg.RampDown.Seconds()
	progress := elapsed / total

	// Exponential decay: spike_factor * e^(-3*progress) approaches 1.0
	decay := math.Exp(-3 * progress)
	return 1.0 + (p.cfg.SpikeFactor-1.0)*decay
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
