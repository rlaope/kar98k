package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Load reads and parses a YAML configuration file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	cfg := DefaultConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	if err := validate(cfg); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return cfg, nil
}

// validate checks the configuration for errors.
func validate(cfg *Config) error {
	if len(cfg.Targets) == 0 {
		return fmt.Errorf("at least one target is required")
	}

	for i, t := range cfg.Targets {
		if t.Name == "" {
			return fmt.Errorf("target[%d]: name is required", i)
		}
		if t.URL == "" {
			return fmt.Errorf("target[%d]: url is required", i)
		}
		if t.Protocol == "" {
			cfg.Targets[i].Protocol = ProtocolHTTP
		}
		if t.Method == "" {
			cfg.Targets[i].Method = "GET"
		}
		if t.Weight <= 0 {
			cfg.Targets[i].Weight = 100
		}
		if t.Timeout <= 0 {
			cfg.Targets[i].Timeout = 30_000_000_000 // 30s in nanoseconds
		}
	}

	if cfg.Controller.BaseTPS <= 0 {
		return fmt.Errorf("controller.base_tps must be positive")
	}
	if cfg.Controller.MaxTPS < cfg.Controller.BaseTPS {
		return fmt.Errorf("controller.max_tps must be >= base_tps")
	}

	if cfg.Pattern.Poisson.Enabled {
		if cfg.Pattern.Poisson.Lambda <= 0 {
			return fmt.Errorf("pattern.poisson.lambda must be positive")
		}
		if cfg.Pattern.Poisson.SpikeFactor < 1 {
			return fmt.Errorf("pattern.poisson.spike_factor must be >= 1")
		}
	}

	if cfg.Pattern.Noise.Enabled {
		if cfg.Pattern.Noise.Amplitude < 0 || cfg.Pattern.Noise.Amplitude > 1 {
			return fmt.Errorf("pattern.noise.amplitude must be between 0 and 1")
		}
	}

	if cfg.Worker.PoolSize <= 0 {
		return fmt.Errorf("worker.pool_size must be positive")
	}

	return nil
}
