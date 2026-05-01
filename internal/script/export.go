package script

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"math"
	"os"
"sync/atomic"
	"time"
)

// jsonReport is the full metrics payload written to JSON output.
type jsonReport struct {
	Scenario    string           `json:"scenario"`
	Duration    string           `json:"duration"`
	Requests    int64            `json:"total_requests"`
	Errors      int64            `json:"total_errors"`
	SuccessRate float64          `json:"success_rate"`
	RPS         float64          `json:"rps"`
	Latency     jsonLatency      `json:"latency"`
	Checks      []jsonCheck      `json:"checks"`
	Thresholds  []jsonThreshold  `json:"thresholds"`
	StatusCodes map[string]int64 `json:"status_codes"`
	Phases      []jsonPhase      `json:"phases,omitempty"`
}

type jsonPhase struct {
	Name       string      `json:"name"`
	DurationMs float64     `json:"duration_ms"`
	Samples    int64       `json:"samples"`
	Errors     int64       `json:"errors"`
	Latency    jsonLatency `json:"latency"`
}

type jsonLatency struct {
	Avg float64 `json:"avg_ms"`
	Min float64 `json:"min_ms"`
	Max float64 `json:"max_ms"`
	P50 float64 `json:"p50_ms"`
	P95 float64 `json:"p95_ms"`
	P99 float64 `json:"p99_ms"`
}

type jsonCheck struct {
	Name   string  `json:"name"`
	Passed int64   `json:"passed"`
	Failed int64   `json:"failed"`
	Rate   float64 `json:"rate"`
}

type jsonThreshold struct {
	Metric    string `json:"metric"`
	Condition string `json:"condition"`
	Passed    bool   `json:"passed"`
}

// ExportJSON writes full test results to a JSON file.
func ExportJSON(path string, runner Runner, elapsed time.Duration) error {
	m := runner.Metrics()
	sc := runner.Scenario()

	totalReqs := atomic.LoadInt64(&m.TotalRequests)
	totalErrs := atomic.LoadInt64(&m.TotalErrors)

	successRate := 0.0
	if totalReqs > 0 {
		successRate = math.Round(float64(totalReqs-totalErrs)/float64(totalReqs)*10000) / 100
	}

	rps := 0.0
	if elapsed.Seconds() > 0 {
		rps = math.Round(float64(totalReqs)/elapsed.Seconds()*10) / 10
	}

	m.mu.Lock()
	checks := make([]CheckResult, len(m.Checks))
	copy(checks, m.Checks)
	statusCodes := make(map[int]int64, len(m.StatusCodes))
	for k, v := range m.StatusCodes {
		statusCodes[k] = v
	}

	toMS := func(us float64) float64 { return math.Round(us/1000*100) / 100 }
	lat := jsonLatency{}
	if m.Histogram.TotalCount() > 0 {
		lat.Min = toMS(float64(m.Histogram.Min()))
		lat.Max = toMS(float64(m.Histogram.Max()))
		lat.Avg = toMS(m.Histogram.Mean())
		lat.P50 = toMS(float64(m.Histogram.ValueAtPercentile(50)))
		lat.P95 = toMS(float64(m.Histogram.ValueAtPercentile(95)))
		lat.P99 = toMS(float64(m.Histogram.ValueAtPercentile(99)))
	}

	now := time.Now()
	jsonPhases := make([]jsonPhase, 0, len(m.phases))
	for _, pm := range m.phases {
		end := pm.EndTime
		if end.IsZero() {
			end = now
		}
		pLat := jsonLatency{}
		if pm.Histogram.TotalCount() > 0 {
			pLat.Min = toMS(float64(pm.Histogram.Min()))
			pLat.Max = toMS(float64(pm.Histogram.Max()))
			pLat.Avg = toMS(pm.Histogram.Mean())
			pLat.P50 = toMS(float64(pm.Histogram.ValueAtPercentile(50)))
			pLat.P95 = toMS(float64(pm.Histogram.ValueAtPercentile(95)))
			pLat.P99 = toMS(float64(pm.Histogram.ValueAtPercentile(99)))
		}
		jsonPhases = append(jsonPhases, jsonPhase{
			Name:       pm.Name,
			DurationMs: math.Round(float64(end.Sub(pm.StartTime).Milliseconds())*10) / 10,
			Samples:    pm.TotalRequests,
			Errors:     pm.TotalErrors,
			Latency:    pLat,
		})
	}
	m.mu.Unlock()

	jsonChecks := make([]jsonCheck, 0, len(checks))
	for _, c := range checks {
		total := c.Passed + c.Failed
		rate := 0.0
		if total > 0 {
			rate = math.Round(float64(c.Passed)/float64(total)*10000) / 100
		}
		jsonChecks = append(jsonChecks, jsonCheck{
			Name:   c.Name,
			Passed: c.Passed,
			Failed: c.Failed,
			Rate:   rate,
		})
	}

	jsonThresholds := make([]jsonThreshold, 0, len(sc.Thresholds))
	for metric, condition := range sc.Thresholds {
		passed := evaluateThreshold(metric, condition, m)
		jsonThresholds = append(jsonThresholds, jsonThreshold{
			Metric:    metric,
			Condition: condition,
			Passed:    passed,
		})
	}

	// Convert status codes map to string keys for JSON
	scMap := make(map[string]int64, len(statusCodes))
	for code, count := range statusCodes {
		scMap[fmt.Sprintf("%d", code)] = count
	}

	report := jsonReport{
		Scenario:    sc.Name,
		Duration:    elapsed.Round(time.Millisecond).String(),
		Requests:    totalReqs,
		Errors:      totalErrs,
		SuccessRate: successRate,
		RPS:         rps,
		Latency:     lat,
		Checks:      jsonChecks,
		Thresholds:  jsonThresholds,
		StatusCodes: scMap,
		Phases:      jsonPhases,
	}

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling JSON report: %w", err)
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing JSON report to %s: %w", path, err)
	}

	return nil
}

// junitTestsuites is the root JUnit XML element.
type junitTestsuites struct {
	XMLName    xml.Name        `xml:"testsuites"`
	Testsuites []junitTestsuite `xml:"testsuite"`
}

type junitTestsuite struct {
	Name      string          `xml:"name,attr"`
	Tests     int             `xml:"tests,attr"`
	Failures  int             `xml:"failures,attr"`
	Time      string          `xml:"time,attr"`
	Testcases []junitTestcase `xml:"testcase"`
}

type junitTestcase struct {
	Name    string         `xml:"name,attr"`
	Time    string         `xml:"time,attr"`
	Failure *junitFailure  `xml:"failure,omitempty"`
}

type junitFailure struct {
	Message string `xml:"message,attr"`
}

// ExportJUnit writes test results as JUnit XML compatible with GitHub Actions and Jenkins.
func ExportJUnit(path string, runner Runner, elapsed time.Duration) error {
	m := runner.Metrics()
	sc := runner.Scenario()

	m.mu.Lock()
	checks := make([]CheckResult, len(m.Checks))
	copy(checks, m.Checks)
	m.mu.Unlock()

	var testcases []junitTestcase
	failures := 0

	// Each check becomes a testcase
	for _, c := range checks {
		total := c.Passed + c.Failed
		tc := junitTestcase{
			Name: fmt.Sprintf("check: %s", c.Name),
			Time: "0",
		}
		if c.Failed > 0 {
			pct := 0.0
			if total > 0 {
				pct = float64(c.Passed) / float64(total) * 100
			}
			tc.Failure = &junitFailure{
				Message: fmt.Sprintf("%.0f%% passed (%d/%d)", pct, c.Passed, total),
			}
			failures++
		}
		testcases = append(testcases, tc)
	}

	// Each threshold becomes a testcase
	for metric, condition := range sc.Thresholds {
		passed := evaluateThreshold(metric, condition, m)
		tc := junitTestcase{
			Name: fmt.Sprintf("threshold: %s %s", metric, condition),
			Time: "0",
		}
		if !passed {
			tc.Failure = &junitFailure{
				Message: fmt.Sprintf("threshold not met: %s %s", metric, condition),
			}
			failures++
		}
		testcases = append(testcases, tc)
	}

	elapsedSec := fmt.Sprintf("%.3f", elapsed.Seconds())

	suite := junitTestsuite{
		Name:      fmt.Sprintf("kar98k: %s", sc.Name),
		Tests:     len(testcases),
		Failures:  failures,
		Time:      elapsedSec,
		Testcases: testcases,
	}

	root := junitTestsuites{
		Testsuites: []junitTestsuite{suite},
	}

	out, err := xml.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling JUnit XML: %w", err)
	}

	content := []byte(xml.Header + string(out) + "\n")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return fmt.Errorf("writing JUnit report to %s: %w", path, err)
	}

	return nil
}
