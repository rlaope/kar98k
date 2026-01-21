package pattern

import (
	"math"
	"math/rand"
	"sync"
	"time"

	"github.com/kar98k/internal/config"
)

// Noise generates micro fluctuations in traffic rate.
type Noise struct {
	cfg config.Noise
	rng *rand.Rand
	mu  sync.Mutex

	// Smoothing state for Perlin-like noise
	currentValue float64
	targetValue  float64
	velocity     float64
}

// NewNoise creates a new noise generator.
func NewNoise(cfg config.Noise) *Noise {
	n := &Noise{
		cfg:          cfg,
		rng:          rand.New(rand.NewSource(time.Now().UnixNano())),
		currentValue: 0,
		targetValue:  0,
		velocity:     0,
	}
	return n
}

// Multiplier returns the current noise multiplier.
// The multiplier oscillates smoothly around 1.0 within the amplitude range.
func (n *Noise) Multiplier() float64 {
	if !n.cfg.Enabled {
		return 1.0
	}

	n.mu.Lock()
	defer n.mu.Unlock()

	// Update target periodically
	if n.rng.Float64() < 0.1 { // 10% chance to change target
		n.targetValue = (n.rng.Float64()*2 - 1) * n.cfg.Amplitude
	}

	// Smooth interpolation using spring-damper system
	const (
		springConstant  = 0.1
		dampingConstant = 0.3
	)

	// Calculate spring force
	force := springConstant * (n.targetValue - n.currentValue)

	// Apply damping
	n.velocity = n.velocity*dampingConstant + force

	// Update position
	n.currentValue += n.velocity

	// Clamp to amplitude bounds
	if n.currentValue > n.cfg.Amplitude {
		n.currentValue = n.cfg.Amplitude
		n.velocity = 0
	}
	if n.currentValue < -n.cfg.Amplitude {
		n.currentValue = -n.cfg.Amplitude
		n.velocity = 0
	}

	return 1.0 + n.currentValue
}

// PerlinNoise provides a more sophisticated noise generator
// using simplified Perlin noise for smoother fluctuations.
type PerlinNoise struct {
	cfg       config.Noise
	startTime time.Time
	mu        sync.Mutex
}

// NewPerlinNoise creates a Perlin-based noise generator.
func NewPerlinNoise(cfg config.Noise) *PerlinNoise {
	return &PerlinNoise{
		cfg:       cfg,
		startTime: time.Now(),
	}
}

// Multiplier returns the current noise multiplier using Perlin noise.
func (p *PerlinNoise) Multiplier() float64 {
	if !p.cfg.Enabled {
		return 1.0
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Time-based noise with multiple octaves
	t := time.Since(p.startTime).Seconds()

	// Simplified multi-octave noise
	noise := p.octaveNoise(t, 3, 0.5)

	// Scale to amplitude
	return 1.0 + noise*p.cfg.Amplitude
}

// octaveNoise generates multi-octave noise for smoother output.
func (p *PerlinNoise) octaveNoise(t float64, octaves int, persistence float64) float64 {
	total := 0.0
	frequency := 0.1
	amplitude := 1.0
	maxValue := 0.0

	for i := 0; i < octaves; i++ {
		total += p.smoothNoise(t*frequency) * amplitude
		maxValue += amplitude
		amplitude *= persistence
		frequency *= 2
	}

	return total / maxValue
}

// smoothNoise generates a smooth noise value at time t.
func (p *PerlinNoise) smoothNoise(t float64) float64 {
	// Use sine waves with different frequencies for pseudo-random noise
	return math.Sin(t*1.0) * 0.5 +
		math.Sin(t*2.3) * 0.25 +
		math.Sin(t*4.1) * 0.125
}
