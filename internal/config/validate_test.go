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

func TestValidateConfig_ScheduleHourOverlapWithoutPriorityWarns(t *testing.T) {
	cfg := goodConfig()
	cfg.Controller.Schedule = []ScheduleEntry{
		{Hours: []int{9, 10, 11, 12, 13}, TPSMultiplier: 1.5},
		{Hours: []int{12, 13}, TPSMultiplier: 2.0},
	}
	issues := ValidateConfig(cfg)

	if HasErrors(issues) {
		t.Fatalf("overlap should warn, not error: %+v", issues)
	}
	found := false
	for _, iss := range issues {
		if iss.Severity == SeverityWarning && strings.Contains(iss.Message, "no explicit priority") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected schedule overlap warning when priority is unset, got %+v", issues)
	}
}

// When at least one overlapping entry sets Priority, the override is
// intentional and should drop back to info severity.
func TestValidateConfig_ScheduleHourOverlapWithPriorityIsInfo(t *testing.T) {
	cfg := goodConfig()
	cfg.Controller.Schedule = []ScheduleEntry{
		{Hours: []int{9, 10, 11, 12, 13}, TPSMultiplier: 1.5},
		{Hours: []int{12, 13}, TPSMultiplier: 2.0, Priority: 10},
	}
	issues := ValidateConfig(cfg)

	for _, iss := range issues {
		if iss.Severity == SeverityWarning && strings.Contains(iss.Message, "no explicit priority") {
			t.Fatalf("explicit priority should suppress the warning, got %+v", iss)
		}
	}
	found := false
	for _, iss := range issues {
		if iss.Severity == SeverityInfo && strings.Contains(iss.Message, "highest priority wins") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected info-level overlap notice, got %+v", issues)
	}
}

func TestValidateConfig_ScenariosHappyPath(t *testing.T) {
	cfg := goodConfig()
	cfg.Scenarios = []Scenario{
		{Name: "warmup", Duration: 2 * time.Minute, BaseTPS: 20},
		{Name: "soak", Duration: time.Hour, BaseTPS: 100, MaxTPS: 500},
	}
	if HasErrors(ValidateConfig(cfg)) {
		t.Fatalf("expected no errors for valid scenarios, got %+v", ValidateConfig(cfg))
	}
}

func TestValidateConfig_ScenarioMissingNameIsError(t *testing.T) {
	cfg := goodConfig()
	cfg.Scenarios = []Scenario{{Duration: time.Minute}}
	if !HasErrors(ValidateConfig(cfg)) {
		t.Fatalf("missing name should be an error")
	}
}

func TestValidateConfig_ScenarioDuplicateNameIsError(t *testing.T) {
	cfg := goodConfig()
	cfg.Scenarios = []Scenario{
		{Name: "phase", Duration: time.Minute},
		{Name: "phase", Duration: time.Minute},
	}
	issues := ValidateConfig(cfg)
	if !HasErrors(issues) {
		t.Fatalf("duplicate scenario name should be an error: %+v", issues)
	}
}

func TestValidateConfig_ScenarioZeroDurationIsError(t *testing.T) {
	cfg := goodConfig()
	cfg.Scenarios = []Scenario{{Name: "instant", Duration: 0}}
	if !HasErrors(ValidateConfig(cfg)) {
		t.Fatalf("zero duration should be an error")
	}
}

func TestValidateConfig_ScenarioBaseAboveMaxIsError(t *testing.T) {
	cfg := goodConfig()
	cfg.Scenarios = []Scenario{{Name: "bad", Duration: time.Minute, BaseTPS: 200, MaxTPS: 100}}
	if !HasErrors(ValidateConfig(cfg)) {
		t.Fatalf("base_tps > max_tps should be an error")
	}
}

func TestValidateConfig_InjectStepsHappyPath(t *testing.T) {
	cfg := goodConfig()
	cfg.Scenarios = []Scenario{{
		Name:     "ramp",
		Duration: 90 * time.Second,
		Inject: []InjectStep{
			{Type: InjectNothingFor, Duration: 30 * time.Second},
			{Type: InjectRampTPS, Duration: time.Minute, From: 0, To: 100},
		},
	}}
	if HasErrors(ValidateConfig(cfg)) {
		t.Fatalf("expected no errors, got %+v", ValidateConfig(cfg))
	}
}

func TestValidateConfig_InjectDurationMismatchIsError(t *testing.T) {
	cfg := goodConfig()
	cfg.Scenarios = []Scenario{{
		Name:     "mismatch",
		Duration: time.Minute,
		Inject: []InjectStep{
			{Type: InjectConstantTPS, Duration: 30 * time.Second, TPS: 50},
		},
	}}
	if !HasErrors(ValidateConfig(cfg)) {
		t.Fatalf("inject duration mismatch should error")
	}
}

func TestValidateConfig_InjectUnknownTypeIsError(t *testing.T) {
	cfg := goodConfig()
	cfg.Scenarios = []Scenario{{
		Name:     "unknown",
		Duration: 10 * time.Second,
		Inject:   []InjectStep{{Type: "bogus", Duration: 10 * time.Second}},
	}}
	if !HasErrors(ValidateConfig(cfg)) {
		t.Fatalf("unknown inject type should error")
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
