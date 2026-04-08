package script

import (
	"html/template"
	"math"
	"os"
	"sort"
	"sync/atomic"
	"time"
)

const reportTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>kar98k — {{.ScenarioName}}</title>
<style>
* { box-sizing: border-box; margin: 0; padding: 0; }
body { background: #111; color: #ccc; font-family: 'Segoe UI', Roboto, monospace; font-size: 14px; padding: 24px; }
h1 { color: #87CEEB; font-size: 22px; margin-bottom: 4px; }
.meta { color: #666; font-size: 12px; margin-bottom: 24px; }
.cards { display: flex; gap: 16px; flex-wrap: wrap; margin-bottom: 24px; }
.card { background: #1a1a1a; border: 1px solid #222; border-radius: 6px; padding: 16px 20px; min-width: 140px; }
.card-label { color: #666; font-size: 11px; text-transform: uppercase; letter-spacing: 1px; margin-bottom: 6px; }
.card-value { color: #87CEEB; font-size: 24px; font-weight: bold; }
.card-value.ok { color: #5fd87d; }
.card-value.warn { color: #f0a050; }
.card-value.fail { color: #e05050; }
section { margin-bottom: 24px; }
h2 { color: #87CEEB; font-size: 14px; text-transform: uppercase; letter-spacing: 1px; margin-bottom: 10px; border-bottom: 1px solid #222; padding-bottom: 6px; }
table { width: 100%; border-collapse: collapse; }
th { text-align: left; color: #555; font-size: 11px; text-transform: uppercase; letter-spacing: 1px; padding: 6px 10px; border-bottom: 1px solid #222; }
td { padding: 7px 10px; border-bottom: 1px solid #1e1e1e; }
tr:last-child td { border-bottom: none; }
.pass { color: #5fd87d; }
.fail { color: #e05050; }
.mono { font-family: monospace; }
</style>
</head>
<body>
<h1>{{.ScenarioName}}</h1>
<div class="meta">Preset: {{.Preset}} &nbsp;|&nbsp; Duration: {{.Duration}} &nbsp;|&nbsp; {{.Timestamp}}</div>

<div class="cards">
  <div class="card">
    <div class="card-label">Total Requests</div>
    <div class="card-value">{{.TotalRequests}}</div>
  </div>
  <div class="card">
    <div class="card-label">RPS</div>
    <div class="card-value">{{printf "%.1f" .RPS}}</div>
  </div>
  <div class="card">
    <div class="card-label">Success Rate</div>
    <div class="card-value {{.SuccessClass}}">{{printf "%.1f" .SuccessRate}}%</div>
  </div>
  <div class="card">
    <div class="card-label">Avg Latency</div>
    <div class="card-value">{{.AvgLatency}}</div>
  </div>
</div>

{{if .HasLatency}}
<section>
  <h2>Latency</h2>
  <table>
    <tr><th>Metric</th><th>Value</th></tr>
    <tr><td>Min</td><td class="mono">{{.LatMin}}</td></tr>
    <tr><td>Avg</td><td class="mono">{{.LatAvg}}</td></tr>
    <tr><td>Max</td><td class="mono">{{.LatMax}}</td></tr>
    <tr><td>P50</td><td class="mono">{{.LatP50}}</td></tr>
    <tr><td>P95</td><td class="mono">{{.LatP95}}</td></tr>
    <tr><td>P99</td><td class="mono">{{.LatP99}}</td></tr>
  </table>
</section>
{{end}}

{{if .StatusCodes}}
<section>
  <h2>Status Codes</h2>
  <table>
    <tr><th>Code</th><th>Count</th></tr>
    {{range .StatusCodes}}<tr><td class="mono">{{.Code}}</td><td>{{.Count}}</td></tr>{{end}}
  </table>
</section>
{{end}}

{{if .Checks}}
<section>
  <h2>Checks</h2>
  <table>
    <tr><th>Name</th><th>Passed</th><th>Failed</th><th>Rate</th></tr>
    {{range .Checks}}
    <tr>
      <td>{{.Name}}</td>
      <td class="pass">{{.Passed}}</td>
      <td class="{{if gt .Failed 0}}fail{{else}}pass{{end}}">{{.Failed}}</td>
      <td class="{{if ge .Rate 100.0}}pass{{else}}warn{{end}}">{{printf "%.1f" .Rate}}%</td>
    </tr>
    {{end}}
  </table>
</section>
{{end}}

{{if .Thresholds}}
<section>
  <h2>Thresholds</h2>
  <table>
    <tr><th>Metric</th><th>Condition</th><th>Result</th></tr>
    {{range .Thresholds}}
    <tr>
      <td class="mono">{{.Metric}}</td>
      <td class="mono">{{.Condition}}</td>
      <td class="{{if .Passed}}pass{{else}}fail{{end}}">{{if .Passed}}PASS{{else}}FAIL{{end}}</td>
    </tr>
    {{end}}
  </table>
</section>
{{end}}

</body>
</html>`

type reportData struct {
	ScenarioName string
	Preset       string
	Duration     string
	Timestamp    string

	TotalRequests int64
	RPS           float64
	SuccessRate   float64
	SuccessClass  string
	AvgLatency    string

	HasLatency bool
	LatMin     string
	LatAvg     string
	LatMax     string
	LatP50     string
	LatP95     string
	LatP99     string

	StatusCodes []statusCodeRow
	Checks      []checkRow
	Thresholds  []thresholdRow
}

type statusCodeRow struct {
	Code  int
	Count int64
}

type checkRow struct {
	Name   string
	Passed int64
	Failed int64
	Rate   float64
}

type thresholdRow struct {
	Metric    string
	Condition string
	Passed    bool
}

// ExportHTML generates a self-contained HTML report and writes it to path.
func ExportHTML(path string, runner Runner, elapsed time.Duration) error {
	m := runner.Metrics()
	sc := runner.Scenario()

	totalReqs := atomic.LoadInt64(&m.TotalRequests)
	totalErrs := atomic.LoadInt64(&m.TotalErrors)

	successRate := float64(0)
	if totalReqs > 0 {
		successRate = float64(totalReqs-totalErrs) / float64(totalReqs) * 100
	}

	rps := float64(0)
	if elapsed.Seconds() > 0 && totalReqs > 0 {
		rps = float64(totalReqs) / elapsed.Seconds()
	}

	successClass := "ok"
	if successRate < 90 {
		successClass = "fail"
	} else if successRate < 99 {
		successClass = "warn"
	}

	m.mu.Lock()

	var statusCodes []statusCodeRow
	for code, count := range m.StatusCodes {
		statusCodes = append(statusCodes, statusCodeRow{Code: code, Count: count})
	}
	sort.Slice(statusCodes, func(i, j int) bool { return statusCodes[i].Code < statusCodes[j].Code })

	var checks []checkRow
	for _, c := range m.Checks {
		total := c.Passed + c.Failed
		rate := float64(0)
		if total > 0 {
			rate = float64(c.Passed) / float64(total) * 100
		}
		checks = append(checks, checkRow{
			Name:   c.Name,
			Passed: c.Passed,
			Failed: c.Failed,
			Rate:   rate,
		})
	}
	m.mu.Unlock()

	hasLatency := m.Histogram.TotalCount() > 0
	latMin, latAvg, latMax, latP50, latP95, latP99 := "", "", "", "", "", ""
	avgLatency := "—"
	if hasLatency {
		toSec := func(us float64) float64 { return us / 1e6 }
		latMin = fmtDuration(toSec(float64(m.Histogram.Min())))
		latAvg = fmtDuration(toSec(m.Histogram.Mean()))
		latMax = fmtDuration(toSec(float64(m.Histogram.Max())))
		latP50 = fmtDuration(toSec(float64(m.Histogram.ValueAtPercentile(50))))
		latP95 = fmtDuration(toSec(float64(m.Histogram.ValueAtPercentile(95))))
		latP99 = fmtDuration(toSec(float64(m.Histogram.ValueAtPercentile(99))))
		avgLatency = latAvg
	}

	var thresholds []thresholdRow
	if len(sc.Thresholds) > 0 {
		// Sort threshold keys for stable output
		keys := make([]string, 0, len(sc.Thresholds))
		for k := range sc.Thresholds {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, metric := range keys {
			condition := sc.Thresholds[metric]
			passed := evaluateThreshold(metric, condition, m)
			thresholds = append(thresholds, thresholdRow{
				Metric:    metric,
				Condition: condition,
				Passed:    passed,
			})
		}
	}

	scenarioName := sc.Name
	if scenarioName == "" {
		scenarioName = "Unnamed Scenario"
	}
	preset := sc.Chaos.Preset
	if preset == "" {
		preset = "moderate"
	}

	data := reportData{
		ScenarioName:  scenarioName,
		Preset:        preset,
		Duration:      elapsed.Round(time.Millisecond).String(),
		Timestamp:     time.Now().Format("2006-01-02 15:04:05"),
		TotalRequests: totalReqs,
		RPS:           rps,
		SuccessRate:   successRate,
		SuccessClass:  successClass,
		AvgLatency:    avgLatency,
		HasLatency:    hasLatency,
		LatMin:        latMin,
		LatAvg:        latAvg,
		LatMax:        latMax,
		LatP50:        latP50,
		LatP95:        latP95,
		LatP99:        latP99,
		StatusCodes:   statusCodes,
		Checks:        checks,
		Thresholds:    thresholds,
	}

	tmpl, err := template.New("report").Parse(reportTemplate)
	if err != nil {
		return err
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	return tmpl.Execute(f, data)
}

func reportPercentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(p/100*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
