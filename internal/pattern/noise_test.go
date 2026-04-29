package pattern

import (
	"testing"

	"github.com/kar98k/internal/config"
)

func TestNewNoiseGenerator_Default(t *testing.T) {
	gen := NewNoiseGenerator(config.Noise{Enabled: true, Amplitude: 0.1})
	if _, ok := gen.(*Noise); !ok {
		t.Fatalf("default Type should select spring (*Noise), got %T", gen)
	}
}

func TestNewNoiseGenerator_Spring(t *testing.T) {
	gen := NewNoiseGenerator(config.Noise{Enabled: true, Type: config.NoiseTypeSpring, Amplitude: 0.1})
	if _, ok := gen.(*Noise); !ok {
		t.Fatalf("Type=spring should yield *Noise, got %T", gen)
	}
}

func TestNewNoiseGenerator_Perlin(t *testing.T) {
	gen := NewNoiseGenerator(config.Noise{Enabled: true, Type: config.NoiseTypePerlin, Amplitude: 0.1})
	if _, ok := gen.(*PerlinNoise); !ok {
		t.Fatalf("Type=perlin should yield *PerlinNoise, got %T", gen)
	}
}

func TestNewNoiseGenerator_UnknownFallsBack(t *testing.T) {
	gen := NewNoiseGenerator(config.Noise{Enabled: true, Type: "bogus", Amplitude: 0.1})
	if _, ok := gen.(*Noise); !ok {
		t.Fatalf("unknown Type should fall back to *Noise, got %T", gen)
	}
}

func TestNoise_Enabled(t *testing.T) {
	on := NewNoise(config.Noise{Enabled: true, Amplitude: 0.1})
	off := NewNoise(config.Noise{Enabled: false, Amplitude: 0.1})
	if !on.Enabled() || off.Enabled() {
		t.Fatalf("Noise.Enabled() mismatch: on=%v off=%v", on.Enabled(), off.Enabled())
	}
}

func TestPerlinNoise_Enabled(t *testing.T) {
	on := NewPerlinNoise(config.Noise{Enabled: true, Amplitude: 0.1})
	off := NewPerlinNoise(config.Noise{Enabled: false, Amplitude: 0.1})
	if !on.Enabled() || off.Enabled() {
		t.Fatalf("PerlinNoise.Enabled() mismatch: on=%v off=%v", on.Enabled(), off.Enabled())
	}
}

func TestNoise_DisabledReturnsOne(t *testing.T) {
	gen := NewNoise(config.Noise{Enabled: false, Amplitude: 0.5})
	if got := gen.Multiplier(); got != 1.0 {
		t.Fatalf("disabled spring noise should return 1.0, got %v", got)
	}
}

func TestPerlinNoise_DisabledReturnsOne(t *testing.T) {
	gen := NewPerlinNoise(config.Noise{Enabled: false, Amplitude: 0.5})
	if got := gen.Multiplier(); got != 1.0 {
		t.Fatalf("disabled perlin noise should return 1.0, got %v", got)
	}
}

func TestNoise_StaysWithinAmplitude(t *testing.T) {
	const amp = 0.2
	gen := NewNoise(config.Noise{Enabled: true, Amplitude: amp})
	for i := 0; i < 10000; i++ {
		m := gen.Multiplier()
		if m < 1.0-amp-1e-9 || m > 1.0+amp+1e-9 {
			t.Fatalf("spring noise out of bounds at iter %d: %v (allowed [%v, %v])",
				i, m, 1.0-amp, 1.0+amp)
		}
	}
}

func TestPerlinNoise_StaysWithinAmplitude(t *testing.T) {
	const amp = 0.2
	gen := NewPerlinNoise(config.Noise{Enabled: true, Amplitude: amp})
	for i := 0; i < 10000; i++ {
		m := gen.Multiplier()
		if m < 1.0-amp-1e-9 || m > 1.0+amp+1e-9 {
			t.Fatalf("perlin noise out of bounds at iter %d: %v (allowed [%v, %v])",
				i, m, 1.0-amp, 1.0+amp)
		}
	}
}
