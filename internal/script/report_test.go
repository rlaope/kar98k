package script

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeRunner implements Runner just enough for ExportHTML — Setup,
// Iterate, Teardown, Load, Close are unused by the report writer.
type fakeRunner struct {
	metrics  *Metrics
	scenario ScenarioConfig
}

func (r *fakeRunner) Load(string) error                  { return nil }
func (r *fakeRunner) Setup() (interface{}, error)        { return nil, nil }
func (r *fakeRunner) Iterate(int, interface{}) error     { return nil }
func (r *fakeRunner) Teardown(interface{}) error         { return nil }
func (r *fakeRunner) Scenario() *ScenarioConfig          { return &r.scenario }
func (r *fakeRunner) Metrics() *Metrics                  { return r.metrics }
func (r *fakeRunner) Close() error                       { return nil }

// newPopulatedRunner builds a runner whose metrics span several minute
// buckets with a mix of fast/slow latencies and varied status codes,
// so all three SVG renderers have something to draw.
func newPopulatedRunner(t *testing.T) *fakeRunner {
	t.Helper()
	m := newMetrics()
	// Pin StartTime so bucketFor's index math is deterministic; the
	// recordRequest path uses time.Now() under the hood, so we record
	// directly into bucket histograms here for test stability.
	m.StartTime = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for minute := 0; minute < 5; minute++ {
		bucket := m.bucketFor(m.StartTime.Add(time.Duration(minute) * time.Minute))
		// 100 fast 2xx, 5 4xx, 1 slow 5xx per minute.
		for i := 0; i < 100; i++ {
			m.Histogram.RecordValue(2_000)
			bucket.Histogram.RecordValue(2_000)
			bucket.StatusCodes[200]++
			m.StatusCodes[200]++
			m.TotalRequests++
		}
		for i := 0; i < 5; i++ {
			m.Histogram.RecordValue(15_000)
			bucket.Histogram.RecordValue(15_000)
			bucket.StatusCodes[404]++
			m.StatusCodes[404]++
			m.TotalRequests++
			m.TotalErrors++
		}
		m.Histogram.RecordValue(2_500_000) // 2.5s outlier
		bucket.Histogram.RecordValue(2_500_000)
		bucket.StatusCodes[500]++
		m.StatusCodes[500]++
		m.TotalRequests++
		m.TotalErrors++
	}
	return &fakeRunner{
		metrics:  m,
		scenario: ScenarioConfig{Name: "report-test"},
	}
}

func writeReport(t *testing.T, r Runner) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "report.html")
	if err := ExportHTML(path, r, time.Minute); err != nil {
		t.Fatalf("ExportHTML: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	return string(data)
}

// TestExportHTML_RendersAllVisualisations verifies the report includes
// every chart layer (CDF, heatmap, status area) when the runner has
// real samples. Markers via `data-*` attributes make the assertions
// resistant to cosmetic SVG tweaks.
func TestExportHTML_RendersAllVisualisations(t *testing.T) {
	r := newPopulatedRunner(t)
	html := writeReport(t, r)

	want := []string{
		`data-cdf-path="1"`,
		`data-heatmap-cell="1"`,
		`data-status-area="1"`,
		`<h2>Latency CDF</h2>`,
		`<h2>Latency Heatmap</h2>`,
		`<h2>Status Codes Over Time</h2>`,
	}
	for _, w := range want {
		if !strings.Contains(html, w) {
			t.Fatalf("report missing %q\n--\n%s", w, head(html, 4000))
		}
	}
}

// TestExportHTML_NoVisualsWhenEmpty ensures the chart sections are
// elided entirely when no requests have been recorded — the report
// should still render, just without the empty plot frames.
func TestExportHTML_NoVisualsWhenEmpty(t *testing.T) {
	r := &fakeRunner{
		metrics:  newMetrics(),
		scenario: ScenarioConfig{Name: "empty"},
	}
	html := writeReport(t, r)

	for _, marker := range []string{
		`data-cdf-path`,
		`data-heatmap-cell`,
		`data-status-area`,
		`<h2>Latency CDF</h2>`,
		`<h2>Latency Heatmap</h2>`,
		`<h2>Status Codes Over Time</h2>`,
	} {
		if strings.Contains(html, marker) {
			t.Fatalf("empty report should not contain %q", marker)
		}
	}
	if !strings.Contains(html, `<title>kar98k — empty</title>`) {
		t.Fatalf("empty report missing title")
	}
}

// TestRecordRequest_PopulatesTimeBuckets is the contract that powers
// the time-axis charts: every recorded request must show up in both
// the global histogram and the bucket that covers its wall-clock
// minute.
func TestRecordRequest_PopulatesTimeBuckets(t *testing.T) {
	m := newMetrics()
	m.recordRequest(200, 1500*time.Microsecond, nil)
	m.recordRequest(500, 3*time.Second, nil)

	if got := m.Histogram.TotalCount(); got != 2 {
		t.Fatalf("global histogram = %d, want 2", got)
	}
	if len(m.TimeBuckets) == 0 {
		t.Fatalf("expected at least one TimeBucket")
	}
	var sum int64
	for _, b := range m.TimeBuckets {
		if b == nil {
			continue
		}
		sum += b.Histogram.TotalCount()
	}
	if sum != 2 {
		t.Fatalf("sum of bucket histograms = %d, want 2", sum)
	}
	// The two requests almost certainly land in the same minute, but
	// even if a clock tick splits them across two buckets the sum must
	// hold.
}

func head(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
