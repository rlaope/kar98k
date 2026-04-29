package config

import (
	"strings"
	"testing"
	"time"
)

// goodConfig returns a minimal Config that passes ValidateConfig.
func goodConfig() *Config {
	cfg := DefaultConfig()
	cfg.Targets = []Target{{Name: "api", URL: "http://localhost:8080/health", Protocol: ProtocolHTTP, Method: "GET", Weight: 100}}
	return cfg
}

func TestValidateConfig_Valid(t *testing.T) {
	if got := ValidateConfig(goodConfig()); HasErrors(got) {
		t.Fatalf("expected no errors, got %+v", got)
	}
}

func TestValidateConfig_NoTargetsIsError(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Targets = nil
	issues := ValidateConfig(cfg)
	if !HasErrors(issues) {
		t.Fatalf("expected error for empty targets, got %+v", issues)
	}
}

func TestValidateConfig_DuplicateTargetName(t *testing.T) {
	cfg := goodConfig()
	cfg.Targets = append(cfg.Targets, Target{Name: "api", URL: "http://x"})
	issues := ValidateConfig(cfg)

	found := false
	for _, iss := range issues {
		if iss.Severity == SeverityError && strings.Contains(iss.Message, "duplicate name") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected duplicate-name error, got %+v", issues)
	}
}

func TestValidateConfig_MaxLessThanBaseTPS(t *testing.T) {
	cfg := goodConfig()
	cfg.Controller.BaseTPS = 1000
	cfg.Controller.MaxTPS = 100
	issues := ValidateConfig(cfg)

	if !HasErrors(issues) {
		t.Fatalf("expected error when max_tps < base_tps")
	}
}

func TestValidateConfig_HighLambdaWarns(t *testing.T) {
	cfg := goodConfig()
	cfg.Pattern.Poisson.Lambda = 10 // 10 spikes/sec — likely a typo
	issues := ValidateConfig(cfg)

	if HasErrors(issues) {
		t.Fatalf("high lambda should warn, not error: %+v", issues)
	}
	found := false
	for _, iss := range issues {
		if iss.Severity == SeverityWarning && strings.Contains(iss.Path, "lambda") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected lambda warning, got %+v", issues)
	}
}

func TestValidateConfig_NoiseAmplitudeBounds(t *testing.T) {
	cfg := goodConfig()
	cfg.Pattern.Noise.Amplitude = 1.5 // > 1: would multiply TPS by negative
	if !HasErrors(ValidateConfig(cfg)) {
		t.Fatalf("amplitude > 1 should be an error")
	}

	cfg.Pattern.Noise.Amplitude = -0.1
	if !HasErrors(ValidateConfig(cfg)) {
		t.Fatalf("amplitude < 0 should be an error")
	}
}

func TestValidateConfig_UnknownNoiseTypeWarns(t *testing.T) {
	cfg := goodConfig()
	cfg.Pattern.Noise.Type = "bogus"
	issues := ValidateConfig(cfg)

	if HasErrors(issues) {
		t.Fatalf("unknown noise type should warn, not error: %+v", issues)
	}
	found := false
	for _, iss := range issues {
		if iss.Severity == SeverityWarning && strings.Contains(iss.Path, "noise.type") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected noise.type warning, got %+v", issues)
	}
}

func TestValidateConfig_ScheduleHourOverlapInfo(t *testing.T) {
	cfg := goodConfig()
	cfg.Controller.Schedule = []ScheduleEntry{
		{Hours: []int{9, 10, 11, 12, 13}, TPSMultiplier: 1.5},
		{Hours: []int{12, 13}, TPSMultiplier: 2.0},
	}
	issues := ValidateConfig(cfg)

	if HasErrors(issues) {
		t.Fatalf("overlap is info-only, not error: %+v", issues)
	}
	found := false
	for _, iss := range issues {
		if iss.Severity == SeverityInfo && strings.Contains(iss.Message, "later wins") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected schedule overlap info, got %+v", issues)
	}
}

func TestValidateConfig_HourOutOfRangeIsError(t *testing.T) {
	cfg := goodConfig()
	cfg.Controller.Schedule = []ScheduleEntry{{Hours: []int{25}, TPSMultiplier: 1.0}}
	if !HasErrors(ValidateConfig(cfg)) {
		t.Fatalf("hour 25 should be an error")
	}
}

func TestValidateConfig_QueueSmallerThanPoolWarns(t *testing.T) {
	cfg := goodConfig()
	cfg.Worker.PoolSize = 100
	cfg.Worker.QueueSize = 10
	issues := ValidateConfig(cfg)

	if HasErrors(issues) {
		t.Fatalf("queue < pool should warn, not error: %+v", issues)
	}
	found := false
	for _, iss := range issues {
		if iss.Severity == SeverityWarning && strings.Contains(iss.Path, "queue_size") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected queue_size warning, got %+v", issues)
	}
}

func TestValidateConfig_PoissonMinMaxIntervalSwapped(t *testing.T) {
	cfg := goodConfig()
	cfg.Pattern.Poisson.MinInterval = 10 * time.Minute
	cfg.Pattern.Poisson.MaxInterval = 1 * time.Minute
	if !HasErrors(ValidateConfig(cfg)) {
		t.Fatalf("min_interval > max_interval should be an error")
	}
}

func TestHasErrors(t *testing.T) {
	if HasErrors(nil) {
		t.Fatalf("nil slice should not have errors")
	}
	if HasErrors([]Issue{{Severity: SeverityWarning}}) {
		t.Fatalf("warnings only should not have errors")
	}
	if !HasErrors([]Issue{{Severity: SeverityError}}) {
		t.Fatalf("error severity should be reported")
	}
}
