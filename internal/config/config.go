package config

import "time"

// Config is the root configuration structure.
type Config struct {
	Targets    []Target   `yaml:"targets"`
	Controller Controller `yaml:"controller"`
	Pattern    Pattern    `yaml:"pattern"`
	Worker     Worker     `yaml:"worker"`
	Health     Health     `yaml:"health"`
	Metrics    Metrics    `yaml:"metrics"`
}

// Target defines a single target endpoint.
type Target struct {
	Name     string            `yaml:"name"`
	URL      string            `yaml:"url"`
	Protocol Protocol          `yaml:"protocol"`
	Method   string            `yaml:"method"`
	Headers  map[string]string `yaml:"headers,omitempty"`
	Body     string            `yaml:"body,omitempty"`
	Weight   int               `yaml:"weight"`
	Timeout  time.Duration     `yaml:"timeout"`
}

// Protocol represents the supported protocols.
type Protocol string

const (
	ProtocolHTTP  Protocol = "http"
	ProtocolHTTP2 Protocol = "http2"
	ProtocolGRPC  Protocol = "grpc"
)

// Controller configures the pulse controller.
type Controller struct {
	BaseTPS         float64           `yaml:"base_tps"`
	MaxTPS          float64           `yaml:"max_tps"`
	RampUpDuration  time.Duration     `yaml:"ramp_up_duration"`
	Schedule        []ScheduleEntry   `yaml:"schedule,omitempty"`
	ShutdownTimeout time.Duration     `yaml:"shutdown_timeout"`
}

// ScheduleEntry defines a time-of-day TPS multiplier.
type ScheduleEntry struct {
	Hours         []int   `yaml:"hours"`
	TPSMultiplier float64 `yaml:"tps_multiplier"`
}

// Pattern configures the traffic pattern engine.
type Pattern struct {
	Poisson Poisson `yaml:"poisson"`
	Noise   Noise   `yaml:"noise"`
}

// Poisson configures Poisson spike generation.
type Poisson struct {
	Enabled     bool          `yaml:"enabled"`
	Lambda      float64       `yaml:"lambda"`                // Events per second (e.g., 0.1 = every 10s)
	Interval    time.Duration `yaml:"interval,omitempty"`    // Alternative to lambda: direct interval (e.g., "2h")
	SpikeFactor float64       `yaml:"spike_factor"`
	MinInterval time.Duration `yaml:"min_interval"`
	MaxInterval time.Duration `yaml:"max_interval"`
	RampUp      time.Duration `yaml:"ramp_up"`
	RampDown    time.Duration `yaml:"ramp_down"`
}

// Noise configures micro fluctuations.
type Noise struct {
	Enabled   bool    `yaml:"enabled"`
	Amplitude float64 `yaml:"amplitude"`
}

// Worker configures the worker pool.
type Worker struct {
	PoolSize       int           `yaml:"pool_size"`
	QueueSize      int           `yaml:"queue_size"`
	MaxIdleConns   int           `yaml:"max_idle_conns"`
	IdleConnTimeout time.Duration `yaml:"idle_conn_timeout"`
}

// Health configures the health checker.
type Health struct {
	Enabled  bool          `yaml:"enabled"`
	Interval time.Duration `yaml:"interval"`
	Timeout  time.Duration `yaml:"timeout"`
}

// Metrics configures Prometheus metrics.
type Metrics struct {
	Enabled bool   `yaml:"enabled"`
	Address string `yaml:"address"`
	Path    string `yaml:"path"`
}

// Discovery configures the adaptive load discovery feature.
type Discovery struct {
	TargetURL       string        `yaml:"target_url"`
	Method          string        `yaml:"method"`
	Protocol        Protocol      `yaml:"protocol"`
	LatencyLimitMs  int64         `yaml:"latency_limit_ms"`  // P95 latency threshold (default: 500ms)
	ErrorRateLimit  float64       `yaml:"error_rate_limit"`  // Error rate threshold (default: 5%)
	MinTPS          float64       `yaml:"min_tps"`           // Starting TPS (default: 10)
	MaxTPS          float64       `yaml:"max_tps"`           // Upper bound (default: 10000)
	StepDuration    time.Duration `yaml:"step_duration"`     // Duration per TPS step (default: 10s)
	ConvergenceRate float64       `yaml:"convergence_rate"`  // Binary search convergence (default: 0.05 = 5%)
}

// DefaultConfig returns a configuration with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		Controller: Controller{
			BaseTPS:         100,
			MaxTPS:          1000,
			RampUpDuration:  30 * time.Second,
			ShutdownTimeout: 30 * time.Second,
		},
		Pattern: Pattern{
			Poisson: Poisson{
				Enabled:     true,
				Lambda:      0.0083,              // ~2분마다 스파이크 (1/120)
				SpikeFactor: 2.0,                 // 2배 증가 (기존 3.0에서 하향)
				MinInterval: 1 * time.Minute,    // 최소 1분 간격
				MaxInterval: 10 * time.Minute,   // 최대 10분 간격
				RampUp:      5 * time.Second,
				RampDown:    10 * time.Second,
			},
			Noise: Noise{
				Enabled:   true,
				Amplitude: 0.10,                  // 10% 노이즈 (기존 15%에서 하향)
			},
		},
		Worker: Worker{
			PoolSize:        1000,
			QueueSize:       10000,
			MaxIdleConns:    100,
			IdleConnTimeout: 90 * time.Second,
		},
		Health: Health{
			Enabled:  true,
			Interval: 10 * time.Second,
			Timeout:  5 * time.Second,
		},
		Metrics: Metrics{
			Enabled: true,
			Address: ":9090",
			Path:    "/metrics",
		},
	}
}

// DefaultDiscovery returns a Discovery config with sensible defaults.
func DefaultDiscovery() Discovery {
	return Discovery{
		Method:          "GET",
		Protocol:        ProtocolHTTP,
		LatencyLimitMs:  500,
		ErrorRateLimit:  5.0,
		MinTPS:          10,
		MaxTPS:          10000,
		StepDuration:    10 * time.Second,
		ConvergenceRate: 0.05,
	}
}
