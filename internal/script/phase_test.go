package script

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSetPhase_Transitions verifies that setPhase creates PhaseMetrics entries
// and updates currentPhase correctly.
func TestSetPhase_Transitions(t *testing.T) {
	m := newMetrics()

	m.setPhase("warmup")
	if m.currentPhase != "warmup" {
		t.Fatalf("currentPhase = %q, want %q", m.currentPhase, "warmup")
	}
	if len(m.phases) != 1 {
		t.Fatalf("len(phases) = %d, want 1", len(m.phases))
	}
	if m.phases[0].Name != "warmup" {
		t.Fatalf("phases[0].Name = %q, want %q", m.phases[0].Name, "warmup")
	}

	m.setPhase("ramp")
	if m.currentPhase != "ramp" {
		t.Fatalf("currentPhase = %q, want %q", m.currentPhase, "ramp")
	}
	if len(m.phases) != 2 {
		t.Fatalf("len(phases) = %d, want 2", len(m.phases))
	}
	// Previous phase EndTime should be set.
	if m.phases[0].EndTime.IsZero() {
		t.Fatal("phases[0].EndTime not set after transition to ramp")
	}
}

// TestSetPhase_ReEntry verifies that re-entering a named phase reuses the
// existing PhaseMetrics (accumulates samples) rather than creating a new one.
func TestSetPhase_ReEntry(t *testing.T) {
	m := newMetrics()
	m.setPhase("a")
	m.setPhase("b")
	m.setPhase("a") // re-enter a
	if len(m.phases) != 2 {
		t.Fatalf("re-entry created extra phase; len(phases) = %d, want 2", len(m.phases))
	}
	if m.currentPhase != "a" {
		t.Fatalf("currentPhase = %q, want %q", m.currentPhase, "a")
	}
}

// TestRecordRequest_RoutesToActivePhase verifies that samples recorded while a
// phase is active accumulate in that phase's histogram.
func TestRecordRequest_RoutesToActivePhase(t *testing.T) {
	m := newMetrics()
	m.setPhase("a")
	m.recordRequest(200, 2*time.Millisecond, nil)
	m.recordRequest(200, 4*time.Millisecond, nil)

	m.mu.Lock()
	pm := m.phases[m.phaseIndex["a"]]
	m.mu.Unlock()

	if pm.TotalRequests != 2 {
		t.Fatalf("phase a TotalRequests = %d, want 2", pm.TotalRequests)
	}
	if pm.Histogram.TotalCount() != 2 {
		t.Fatalf("phase a histogram count = %d, want 2", pm.Histogram.TotalCount())
	}
}

// TestRecordRequest_PhaseCutover verifies that after switching phases, new
// samples go to the new phase and old phase totals stay frozen.
func TestRecordRequest_PhaseCutover(t *testing.T) {
	m := newMetrics()
	m.setPhase("a")
	m.recordRequest(200, 1*time.Millisecond, nil)

	m.setPhase("b")
	m.recordRequest(200, 5*time.Millisecond, nil)
	m.recordRequest(500, 10*time.Millisecond, nil)

	m.mu.Lock()
	pa := m.phases[m.phaseIndex["a"]]
	pb := m.phases[m.phaseIndex["b"]]
	m.mu.Unlock()

	if pa.TotalRequests != 1 {
		t.Fatalf("phase a TotalRequests = %d, want 1", pa.TotalRequests)
	}
	if pb.TotalRequests != 2 {
		t.Fatalf("phase b TotalRequests = %d, want 2", pb.TotalRequests)
	}
	if pb.TotalErrors != 1 {
		t.Fatalf("phase b TotalErrors = %d, want 1", pb.TotalErrors)
	}
}

// TestNoPhase_RecordRequestNoOp verifies that recordRequest before any
// phase() call does not panic and does not create phase entries.
func TestNoPhase_RecordRequestNoOp(t *testing.T) {
	m := newMetrics()
	m.recordRequest(200, 1*time.Millisecond, nil)
	m.mu.Lock()
	n := len(m.phases)
	m.mu.Unlock()
	if n != 0 {
		t.Fatalf("expected 0 phases without setPhase call, got %d", n)
	}
}

// TestExportJSON_ContainsPhases verifies that ExportJSON includes phase
// aggregates when phases have been set.
func TestExportJSON_ContainsPhases(t *testing.T) {
	m := newMetrics()
	m.setPhase("warmup")
	m.recordRequest(200, 2*time.Millisecond, nil)
	m.setPhase("peak")
	m.recordRequest(200, 8*time.Millisecond, nil)
	m.recordRequest(500, 50*time.Millisecond, nil)

	r := &fakeRunner{metrics: m, scenario: ScenarioConfig{Name: "phase-json-test"}}
	dir := t.TempDir()
	path := filepath.Join(dir, "out.json")
	if err := ExportJSON(path, r, 10*time.Second); err != nil {
		t.Fatalf("ExportJSON: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read json: %v", err)
	}

	var result struct {
		Phases []struct {
			Name    string  `json:"name"`
			Samples int64   `json:"samples"`
			Errors  int64   `json:"errors"`
		} `json:"phases"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(result.Phases) != 2 {
		t.Fatalf("len(phases) = %d, want 2; JSON:\n%s", len(result.Phases), raw)
	}
	if result.Phases[0].Name != "warmup" {
		t.Fatalf("phases[0].Name = %q, want %q", result.Phases[0].Name, "warmup")
	}
	if result.Phases[0].Samples != 1 {
		t.Fatalf("phases[0].Samples = %d, want 1", result.Phases[0].Samples)
	}
	if result.Phases[1].Name != "peak" {
		t.Fatalf("phases[1].Name = %q, want %q", result.Phases[1].Name, "peak")
	}
	if result.Phases[1].Samples != 2 {
		t.Fatalf("phases[1].Samples = %d, want 2", result.Phases[1].Samples)
	}
	if result.Phases[1].Errors != 1 {
		t.Fatalf("phases[1].Errors = %d, want 1", result.Phases[1].Errors)
	}
}

// TestExportHTML_ContainsByPhaseSection verifies that the HTML report includes
// the "By Phase" header and phase names when phases have been recorded.
func TestExportHTML_ContainsByPhaseSection(t *testing.T) {
	m := newMetrics()
	m.setPhase("warmup")
	m.recordRequest(200, 2*time.Millisecond, nil)
	m.setPhase("ramp")
	m.recordRequest(200, 10*time.Millisecond, nil)

	r := &fakeRunner{metrics: m, scenario: ScenarioConfig{Name: "phase-html-test"}}
	html := writeReport(t, r)

	for _, want := range []string{
		`<h2>By Phase</h2>`,
		`warmup`,
		`ramp`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("report missing %q\n--\n%s", want, head(html, 6000))
		}
	}
}

// TestExportHTML_NoPhaseSectionWhenEmpty verifies that the "By Phase" section
// is elided when no phase() calls were made.
func TestExportHTML_NoPhaseSectionWhenEmpty(t *testing.T) {
	m := newMetrics()
	m.recordRequest(200, 1*time.Millisecond, nil)

	r := &fakeRunner{metrics: m, scenario: ScenarioConfig{Name: "no-phase"}}
	html := writeReport(t, r)

	if strings.Contains(html, `<h2>By Phase</h2>`) {
		t.Fatal("report should not contain By Phase section when no phases set")
	}
}
