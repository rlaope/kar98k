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
	Safety     Safety     `yaml:"safety,omitempty"`
	// Scenarios optionally defines a sequence of phases (warmup,
	// baseline, spike-train, soak, cooldown, etc.) that the controller
	// advances through on a wall-clock timeline. When empty, the
	// controller uses the single Pattern block above as before.
	Scenarios []Scenario `yaml:"scenarios,omitempty"`
}

// Scenario is one phase of a multi-stage run. Phases execute strictly
// in declaration order and each phase's duration is consumed before the
// next phase starts.
//
// All Pattern/TPS fields override the top-level config when non-zero;
// any zero-valued field inherits from the previous phase (or the
// top-level for the first phase). This keeps simple configs short:
// declaring just a `name + duration + base_tps` is enough to bump TPS
// for one stage and let everything else flow through.
//
// When Inject is non-empty, base_tps is ignored — the inject curve
// drives the per-tick TPS within this phase. The total of all inject
// step durations must equal the phase duration.
type Scenario struct {
	Name     string        `yaml:"name"`
	Duration time.Duration `yaml:"duration"`
	BaseTPS  float64       `yaml:"base_tps,omitempty"`
	MaxTPS   float64       `yaml:"max_tps,omitempty"`
	Pattern  *Pattern      `yaml:"pattern,omitempty"`
	Inject   []InjectStep  `yaml:"inject,omitempty"`
}

// InjectStepType is the discriminator for the inject DSL. Each value
// describes how TPS evolves over the step's duration.
type InjectStepType string

const (
	// InjectNothingFor holds TPS at the engine floor (1) for Duration.
	// Equivalent to Gatling's `nothingFor(d)`.
	InjectNothingFor InjectStepType = "nothing_for"
	// InjectConstantTPS holds TPS at TPS for Duration.
	// Equivalent to Gatling's `constantUsersPerSec(n) during d`.
	InjectConstantTPS InjectStepType = "constant_tps"
	// InjectRampTPS linearly interpolates TPS from From to To over Duration.
	// Equivalent to Gatling's `rampUsersPerSec(a) to (b) during d`.
	InjectRampTPS InjectStepType = "ramp_tps"
	// InjectHeavisideTPS injects an S-curve from From to To over Duration
	// using a sigmoid 1/(1+exp(-k(t-t0))). k defaults to 6 (steep but
	// continuous). Equivalent to Gatling's `heavisideUsers(n) during d`.
	InjectHeavisideTPS InjectStepType = "heaviside_tps"
)

// InjectStep is one segment of a phase's injection curve. Field
// validity depends on Type — see InjectStepType for what each variant
// reads. The runner enforces field constraints at config-load time so
// runtime evaluation can stay branch-light.
type InjectStep struct {
	Type     InjectStepType `yaml:"type"`
	Duration time.Duration  `yaml:"duration"`
	// TPS is the target rate for constant_tps. Ignored otherwise.
	TPS float64 `yaml:"tps,omitempty"`
	// From / To are the endpoints for ramp_tps and heaviside_tps.
	From float64 `yaml:"from,omitempty"`
	To   float64 `yaml:"to,omitempty"`
	// Steepness controls how sharp the heaviside_tps sigmoid is. Larger
	// values approach a true step function. Defaults to 6 when zero.
	Steepness float64 `yaml:"steepness,omitempty"`
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
//
// When more than one entry covers the same hour, the entry with the
// highest Priority wins. Ties fall back to "later entry wins" so a
// schedule that doesn't set Priority preserves the historical
// position-based override semantics.
type ScheduleEntry struct {
	Hours         []int   `yaml:"hours"`
	TPSMultiplier float64 `yaml:"tps_multiplier"`
	Priority      int     `yaml:"priority,omitempty"`
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
	Enabled   bool      `yaml:"enabled"`
	Type      NoiseType `yaml:"type,omitempty"` // "spring" (default) or "perlin"
	Amplitude float64   `yaml:"amplitude"`
}

// NoiseType selects the noise generator algorithm.
type NoiseType string

const (
	NoiseTypeSpring NoiseType = "spring"
	NoiseTypePerlin NoiseType = "perlin"
)

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

// Safety configures the circuit breaker that pauses traffic when
// error rate or P95 latency exceeds operator-defined thresholds for a
// sustained window. When tripped the controller stops feeding the
// worker pool but keeps workers, connections, and the rate limiter
// alive — so resume is instantaneous.
type Safety struct {
	Enabled         bool          `yaml:"enabled"`
	ErrorRateAbove  float64       `yaml:"error_rate_above,omitempty"`  // % (0..100); 0 disables this check
	P95LatencyAbove time.Duration `yaml:"p95_latency_above,omitempty"` // 0 disables this check
	SustainedFor    time.Duration `yaml:"sustained_for"`               // window the breach must hold for
	ResumeAfter     time.Duration `yaml:"resume_after,omitempty"`      // 0 disables auto-resume
	Webhook         string        `yaml:"webhook,omitempty"`           // optional URL pinged on transitions
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
