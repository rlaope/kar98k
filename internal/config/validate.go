package config

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"time"
)

// Severity classifies how seriously an issue should be treated.
// Errors fail validation (non-zero exit). Warnings flag likely
// mistakes that don't outright break the config. Info is for things
// the operator should be aware of (e.g. "later schedule entries
// override earlier ones at this hour").
type Severity string

const (
	SeverityInfo    Severity = "info"
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

// Issue is a single finding from semantic validation.
type Issue struct {
	Path       string   `json:"path"`
	Severity   Severity `json:"severity"`
	Message    string   `json:"message"`
	Suggestion string   `json:"suggestion,omitempty"`
}

// HasErrors reports whether the issue list contains any error-severity entries.
func HasErrors(issues []Issue) bool {
	for _, iss := range issues {
		if iss.Severity == SeverityError {
			return true
		}
	}
	return false
}

// poissonLambdaWarn is the threshold beyond which a Poisson lambda is
// almost certainly a misconfiguration. lambda=0.1 ⇒ ~6 spikes/min,
// already aggressive for a 24/7 simulator. lambda=10 ⇒ ~10 spikes/sec,
// which is what the issue's "10 spikes/sec, likely typo" example
// describes.
const poissonLambdaWarn = 0.1

// ValidateConfig runs structural and semantic checks against an
// already-parsed Config and returns every issue it finds, rather than
// short-circuiting on the first error like the loader's private
// validate() does. This is the surface used by `kar validate`.
func ValidateConfig(cfg *Config) []Issue {
	var out []Issue

	out = append(out, validateTargets(cfg)...)
	out = append(out, validateController(cfg)...)
	out = append(out, validatePattern(cfg)...)
	out = append(out, validateWorker(cfg)...)
	out = append(out, validateSchedule(cfg)...)

	return out
}

func validateTargets(cfg *Config) []Issue {
	var out []Issue
	if len(cfg.Targets) == 0 {
		return []Issue{{
			Path:     "targets",
			Severity: SeverityError,
			Message:  "at least one target is required",
		}}
	}

	seen := make(map[string]int)
	for i, t := range cfg.Targets {
		path := fmt.Sprintf("targets[%d]", i)
		if t.Name == "" {
			out = append(out, Issue{Path: path + ".name", Severity: SeverityError, Message: "name is required"})
		} else if prev, ok := seen[t.Name]; ok {
			out = append(out, Issue{
				Path:     path + ".name",
				Severity: SeverityError,
				Message:  fmt.Sprintf("duplicate name %q (also at targets[%d])", t.Name, prev),
			})
		} else {
			seen[t.Name] = i
		}
		if t.URL == "" {
			out = append(out, Issue{Path: path + ".url", Severity: SeverityError, Message: "url is required"})
		} else if _, err := url.Parse(t.URL); err != nil {
			out = append(out, Issue{
				Path:     path + ".url",
				Severity: SeverityError,
				Message:  fmt.Sprintf("invalid URL: %v", err),
			})
		}
		if t.Weight < 0 {
			out = append(out, Issue{
				Path:     path + ".weight",
				Severity: SeverityError,
				Message:  "weight must be non-negative",
			})
		}
		if t.Timeout < 0 {
			out = append(out, Issue{
				Path:     path + ".timeout",
				Severity: SeverityError,
				Message:  "timeout must be non-negative",
			})
		}
	}
	return out
}

func validateController(cfg *Config) []Issue {
	var out []Issue
	if cfg.Controller.BaseTPS <= 0 {
		out = append(out, Issue{
			Path:     "controller.base_tps",
			Severity: SeverityError,
			Message:  "base_tps must be positive",
		})
	}
	if cfg.Controller.MaxTPS < cfg.Controller.BaseTPS {
		out = append(out, Issue{
			Path:     "controller.max_tps",
			Severity: SeverityError,
			Message: fmt.Sprintf("max_tps (%.0f) must be >= base_tps (%.0f)",
				cfg.Controller.MaxTPS, cfg.Controller.BaseTPS),
		})
	}
	return out
}

func validatePattern(cfg *Config) []Issue {
	var out []Issue
	p := cfg.Pattern.Poisson
	if p.Enabled {
		if p.Lambda <= 0 && p.Interval <= 0 {
			out = append(out, Issue{
				Path:     "pattern.poisson.lambda",
				Severity: SeverityError,
				Message:  "either lambda or interval must be set when Poisson is enabled",
			})
		}
		if p.Lambda > poissonLambdaWarn {
			out = append(out, Issue{
				Path:     "pattern.poisson.lambda",
				Severity: SeverityWarning,
				Message: fmt.Sprintf("lambda=%.4g implies ~%.1f spikes/sec, likely a typo",
					p.Lambda, p.Lambda),
				Suggestion: fmt.Sprintf("for occasional spikes, lambda <= %.2f is more typical", poissonLambdaWarn),
			})
		}
		if p.SpikeFactor < 1 {
			out = append(out, Issue{
				Path:     "pattern.poisson.spike_factor",
				Severity: SeverityError,
				Message:  "spike_factor must be >= 1",
			})
		}
		if p.MinInterval > 0 && p.MaxInterval > 0 && p.MinInterval > p.MaxInterval {
			out = append(out, Issue{
				Path:     "pattern.poisson",
				Severity: SeverityError,
				Message: fmt.Sprintf("min_interval (%s) must be <= max_interval (%s)",
					p.MinInterval, p.MaxInterval),
			})
		}
	}

	n := cfg.Pattern.Noise
	if n.Enabled {
		switch {
		case n.Amplitude < 0:
			out = append(out, Issue{
				Path:     "pattern.noise.amplitude",
				Severity: SeverityError,
				Message:  "amplitude must be >= 0",
			})
		case n.Amplitude > 1:
			out = append(out, Issue{
				Path:     "pattern.noise.amplitude",
				Severity: SeverityError,
				Message: fmt.Sprintf("amplitude %.2f > 1 would multiply TPS by a negative factor",
					n.Amplitude),
				Suggestion: "amplitude is a fraction (0..1); 0.10 means ±10%",
			})
		}
		if n.Type != "" && n.Type != NoiseTypeSpring && n.Type != NoiseTypePerlin {
			out = append(out, Issue{
				Path:     "pattern.noise.type",
				Severity: SeverityWarning,
				Message: fmt.Sprintf("unknown noise type %q (will fall back to %q)",
					n.Type, NoiseTypeSpring),
				Suggestion: fmt.Sprintf("use %q or %q", NoiseTypeSpring, NoiseTypePerlin),
			})
		}
	}
	return out
}

func validateWorker(cfg *Config) []Issue {
	var out []Issue
	if cfg.Worker.PoolSize <= 0 {
		out = append(out, Issue{
			Path:     "worker.pool_size",
			Severity: SeverityError,
			Message:  "pool_size must be positive",
		})
	}
	if cfg.Worker.QueueSize <= 0 {
		out = append(out, Issue{
			Path:     "worker.queue_size",
			Severity: SeverityError,
			Message:  "queue_size must be positive",
		})
	}
	// A queue smaller than the pool means workers will starve on
	// burst arrivals; surface it as info, not an error.
	if cfg.Worker.QueueSize > 0 && cfg.Worker.PoolSize > 0 &&
		cfg.Worker.QueueSize < cfg.Worker.PoolSize {
		out = append(out, Issue{
			Path:     "worker.queue_size",
			Severity: SeverityWarning,
			Message: fmt.Sprintf("queue_size (%d) < pool_size (%d) leaves no headroom for bursts",
				cfg.Worker.QueueSize, cfg.Worker.PoolSize),
		})
	}
	return out
}

func validateSchedule(cfg *Config) []Issue {
	if len(cfg.Controller.Schedule) == 0 {
		return nil
	}

	var out []Issue
	// Detect hours that appear in more than one entry. Current
	// semantics is "later wins" — surface that as info so the operator
	// can make it explicit (see #48 for explicit priority).
	hourEntries := make(map[int][]int) // hour → entry indices
	for i, e := range cfg.Controller.Schedule {
		for _, h := range e.Hours {
			if h < 0 || h > 23 {
				out = append(out, Issue{
					Path:     fmt.Sprintf("controller.schedule[%d].hours", i),
					Severity: SeverityError,
					Message:  fmt.Sprintf("hour %d out of range (must be 0..23)", h),
				})
				continue
			}
			hourEntries[h] = append(hourEntries[h], i)
		}
		if e.TPSMultiplier <= 0 {
			out = append(out, Issue{
				Path:     fmt.Sprintf("controller.schedule[%d].tps_multiplier", i),
				Severity: SeverityWarning,
				Message:  "tps_multiplier <= 0 disables traffic for this window",
			})
		}
	}

	overlapping := make(map[string]bool)
	for h, idxs := range hourEntries {
		if len(idxs) <= 1 {
			continue
		}
		sort.Ints(idxs)
		key := fmt.Sprintf("%v", idxs)
		if overlapping[key] {
			continue
		}
		overlapping[key] = true
		out = append(out, Issue{
			Path:     "controller.schedule",
			Severity: SeverityInfo,
			Message: fmt.Sprintf("hour %d appears in entries %v; later wins (consider explicit priority — see #48)",
				h, idxs),
		})
	}
	return out
}

// CheckReachability does a best-effort HTTP GET against every HTTP/HTTP2
// target and reports whether each responded. Skipped for gRPC targets
// (no generic reachability probe). Each target gets at most `timeout`;
// the overall call is bounded by `ctx`.
func CheckReachability(ctx context.Context, cfg *Config, timeout time.Duration) []Issue {
	var out []Issue
	client := &http.Client{Timeout: timeout}

	for i, t := range cfg.Targets {
		path := fmt.Sprintf("targets[%d] %s", i, t.Name)
		if t.Protocol == ProtocolGRPC {
			out = append(out, Issue{
				Path:     path,
				Severity: SeverityInfo,
				Message:  "gRPC reachability check skipped (no generic probe)",
			})
			continue
		}

		method := t.Method
		if method == "" {
			method = "GET"
		}
		req, err := http.NewRequestWithContext(ctx, method, t.URL, nil)
		if err != nil {
			out = append(out, Issue{
				Path:     path,
				Severity: SeverityError,
				Message:  fmt.Sprintf("invalid request: %v", err),
			})
			continue
		}
		for k, v := range t.Headers {
			req.Header.Set(k, v)
		}

		start := time.Now()
		resp, err := client.Do(req)
		elapsed := time.Since(start).Round(time.Millisecond)

		if err != nil {
			out = append(out, Issue{
				Path:     path,
				Severity: SeverityError,
				Message:  fmt.Sprintf("unreachable: %v", err),
			})
			continue
		}
		resp.Body.Close()

		sev := SeverityInfo
		switch {
		case resp.StatusCode >= 500:
			sev = SeverityError
		case resp.StatusCode >= 400:
			sev = SeverityWarning
		}
		out = append(out, Issue{
			Path:     path,
			Severity: sev,
			Message:  fmt.Sprintf("HTTP %d (%s)", resp.StatusCode, elapsed),
		})
	}
	return out
}
