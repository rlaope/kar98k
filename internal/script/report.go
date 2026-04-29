package script

import (
	"bytes"
	"fmt"
	"html/template"
	"math"
	"os"
	"sort"
	"sync/atomic"
	"time"

	hdrhistogram "github.com/HdrHistogram/hdrhistogram-go"
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
.chart { background: #181818; border: 1px solid #222; border-radius: 6px; padding: 12px; }
.chart svg { display: block; width: 100%; height: auto; font-family: monospace; }
.chart-caption { color: #666; font-size: 11px; margin-top: 6px; }
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

{{if .CDFSVG}}
<section>
  <h2>Latency CDF</h2>
  <div class="chart">{{.CDFSVG}}</div>
  <div class="chart-caption">x: latency (log) &nbsp;·&nbsp; y: percentile · drawn from full HdrHistogram</div>
</section>
{{end}}

{{if .HeatmapSVG}}
<section>
  <h2>Latency Heatmap</h2>
  <div class="chart">{{.HeatmapSVG}}</div>
  <div class="chart-caption">columns: 1-minute time buckets &nbsp;·&nbsp; rows: log latency bins &nbsp;·&nbsp; brighter = more requests</div>
</section>
{{end}}

{{if .StatusAreaSVG}}
<section>
  <h2>Status Codes Over Time</h2>
  <div class="chart">{{.StatusAreaSVG}}</div>
  <div class="chart-caption">stacked area &nbsp;·&nbsp; green=2xx · blue=3xx · orange=4xx · red=5xx · grey=other</div>
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

	CDFSVG        template.HTML
	HeatmapSVG    template.HTML
	StatusAreaSVG template.HTML

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

	cdfSVG := buildCDFSVG(m.Histogram)
	heatmapSVG := buildHeatmapSVG(m.TimeBuckets)
	statusAreaSVG := buildStatusAreaSVG(m.TimeBuckets)
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
		CDFSVG:        template.HTML(cdfSVG),
		HeatmapSVG:    template.HTML(heatmapSVG),
		StatusAreaSVG: template.HTML(statusAreaSVG),
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

// buildCDFSVG renders the latency cumulative distribution as an
// embedded SVG path. x is log10(latency µs), y is percentile (0..100).
// The percentile points are sampled densely near the tail (99..99.99)
// so long-tail behaviour is visible — that's the whole reason the CDF
// is more useful than the percentile table.
func buildCDFSVG(h *hdrhistogram.Histogram) string {
	if h == nil || h.TotalCount() == 0 {
		return ""
	}

	const (
		w, hgt    = 720, 220
		pl, pr    = 56, 24
		pt, pb    = 14, 32
		plotW     = w - pl - pr
		plotH     = hgt - pt - pb
	)

	pcts := make([]float64, 0, 60)
	for p := 1.0; p <= 90.0; p += 5 {
		pcts = append(pcts, p)
	}
	for p := 90.0; p < 99.0; p += 1 {
		pcts = append(pcts, p)
	}
	for p := 99.0; p < 99.9; p += 0.1 {
		pcts = append(pcts, p)
	}
	for p := 99.9; p <= 99.99; p += 0.01 {
		pcts = append(pcts, p)
	}

	maxV := math.Max(1.0, float64(h.Max()))
	minV := math.Max(1.0, float64(h.Min()))
	logMin, logMax := math.Log10(minV), math.Log10(maxV)
	if logMax-logMin < 0.001 {
		logMax = logMin + 1 // avoid divide-by-zero on degenerate runs
	}

	var path bytes.Buffer
	for i, p := range pcts {
		v := float64(h.ValueAtPercentile(p))
		if v < 1 {
			v = 1
		}
		x := pl + int(float64(plotW)*(math.Log10(v)-logMin)/(logMax-logMin))
		y := pt + int(float64(plotH)*(1-p/100))
		if i == 0 {
			fmt.Fprintf(&path, "M%d %d", x, y)
		} else {
			fmt.Fprintf(&path, " L%d %d", x, y)
		}
	}

	gridLines := []float64{50, 90, 95, 99, 99.9}

	var b bytes.Buffer
	fmt.Fprintf(&b, `<svg viewBox="0 0 %d %d" xmlns="http://www.w3.org/2000/svg">`, w, hgt)
	fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="%d" fill="#0e0e0e" stroke="#222"/>`, pl, pt, plotW, plotH)
	for _, g := range gridLines {
		gy := pt + int(float64(plotH)*(1-g/100))
		fmt.Fprintf(&b, `<line x1="%d" x2="%d" y1="%d" y2="%d" stroke="#222" stroke-dasharray="2,3"/>`, pl, pl+plotW, gy, gy)
		fmt.Fprintf(&b, `<text x="%d" y="%d" fill="#666" font-size="10" text-anchor="end">%g%%</text>`, pl-6, gy+3, g)
	}
	for _, decade := range axisDecades(logMin, logMax) {
		gx := pl + int(float64(plotW)*(decade-logMin)/(logMax-logMin))
		fmt.Fprintf(&b, `<line x1="%d" x2="%d" y1="%d" y2="%d" stroke="#222" stroke-dasharray="2,3"/>`, gx, gx, pt, pt+plotH)
		fmt.Fprintf(&b, `<text x="%d" y="%d" fill="#666" font-size="10" text-anchor="middle">%s</text>`, gx, pt+plotH+14, fmtDurationFromMicros(math.Pow(10, decade)))
	}
	fmt.Fprintf(&b, `<path data-cdf-path="1" d="%s" fill="none" stroke="#87CEEB" stroke-width="1.6"/>`, path.String())
	fmt.Fprintf(&b, `<text x="%d" y="%d" fill="#666" font-size="10">latency (log scale)</text>`, pl, hgt-6)
	b.WriteString(`</svg>`)
	return b.String()
}

// axisDecades returns evenly-spaced log10 tick positions inside the
// [lo, hi] window, picking integer decades when the span is wide
// enough and falling back to evenly subdivided ticks otherwise.
func axisDecades(lo, hi float64) []float64 {
	if hi-lo >= 1 {
		var out []float64
		for d := math.Ceil(lo); d <= math.Floor(hi); d++ {
			out = append(out, d)
		}
		if len(out) == 0 {
			out = []float64{lo, hi}
		}
		return out
	}
	return []float64{lo, (lo + hi) / 2, hi}
}

// buildHeatmapSVG renders a time × latency heatmap. Columns are
// 1-minute buckets, rows are log-spaced latency bins. Each cell's
// fill alpha tracks the bucket's request count for that latency band,
// so visual brightness corresponds to where requests clustered.
func buildHeatmapSVG(buckets []*TimeBucket) string {
	if len(buckets) == 0 {
		return ""
	}
	totalSamples := int64(0)
	for _, b := range buckets {
		if b != nil {
			totalSamples += b.Histogram.TotalCount()
		}
	}
	if totalSamples == 0 {
		return ""
	}

	const (
		rows   = 12
		w      = 720
		hgt    = 220
		pl, pr = 56, 16
		pt, pb = 14, 24
	)
	plotW := w - pl - pr
	plotH := hgt - pt - pb

	cellW := math.Max(1, float64(plotW)/float64(len(buckets)))
	rowH := float64(plotH) / float64(rows)

	// Latency bins are log-spaced from 1ms to 60s, matching the practical
	// range of HTTP load tests. Anything <1ms collapses into row 0,
	// anything >60s into the top row.
	binEdges := make([]float64, rows+1)
	const minMs, maxMs = 1.0, 60000.0
	logMin, logMax := math.Log10(minMs*1000), math.Log10(maxMs*1000) // micros
	for i := 0; i <= rows; i++ {
		binEdges[i] = math.Pow(10, logMin+(logMax-logMin)*float64(i)/float64(rows))
	}

	type cell struct {
		col, row int
		count    int64
	}
	var cells []cell
	var maxCellCount int64
	for col, b := range buckets {
		if b == nil {
			continue
		}
		counts := make([]int64, rows)
		for _, br := range b.Histogram.Distribution() {
			if br.Count == 0 {
				continue
			}
			micros := float64(br.From+br.To) / 2
			row := 0
			for r := 0; r < rows; r++ {
				if micros < binEdges[r+1] {
					row = r
					break
				}
				row = rows - 1
			}
			counts[row] += br.Count
		}
		for r, c := range counts {
			if c == 0 {
				continue
			}
			if c > maxCellCount {
				maxCellCount = c
			}
			cells = append(cells, cell{col: col, row: r, count: c})
		}
	}

	if maxCellCount == 0 {
		return ""
	}

	var b bytes.Buffer
	fmt.Fprintf(&b, `<svg viewBox="0 0 %d %d" xmlns="http://www.w3.org/2000/svg">`, w, hgt)
	fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="%d" fill="#0e0e0e" stroke="#222"/>`, pl, pt, plotW, plotH)

	for _, c := range cells {
		x := pl + int(float64(c.col)*cellW)
		// Row 0 = fastest = bottom of plot.
		y := pt + int(float64(rows-1-c.row)*rowH)
		intensity := math.Log1p(float64(c.count)) / math.Log1p(float64(maxCellCount))
		fmt.Fprintf(&b,
			`<rect data-heatmap-cell="1" x="%d" y="%d" width="%d" height="%d" fill="#87CEEB" fill-opacity="%.3f"/>`,
			x, y, int(math.Ceil(cellW)), int(math.Ceil(rowH))+1, intensity,
		)
	}

	// Y-axis labels at the binEdges boundaries.
	for r := 0; r <= rows; r += 3 {
		y := pt + int(float64(rows-r)*rowH)
		label := fmtDurationFromMicros(binEdges[r])
		fmt.Fprintf(&b, `<text x="%d" y="%d" fill="#666" font-size="10" text-anchor="end">%s</text>`, pl-6, y+3, label)
	}

	fmt.Fprintf(&b, `<text x="%d" y="%d" fill="#666" font-size="10">time →</text>`, pl, hgt-6)
	b.WriteString(`</svg>`)
	return b.String()
}

// buildStatusAreaSVG renders a stacked area of 2xx/3xx/4xx/5xx/other
// counts per time bucket. Each bucket is drawn as a single column
// segmented by family — equivalent to a stacked bar chart, just with
// connected polygons so trends across time are easier to read.
func buildStatusAreaSVG(buckets []*TimeBucket) string {
	if len(buckets) == 0 {
		return ""
	}

	categories := []struct {
		label string
		fill  string
		match func(int) bool
	}{
		{"2xx", "#5fd87d", func(c int) bool { return c >= 200 && c < 300 }},
		{"3xx", "#87CEEB", func(c int) bool { return c >= 300 && c < 400 }},
		{"4xx", "#f0a050", func(c int) bool { return c >= 400 && c < 500 }},
		{"5xx", "#e05050", func(c int) bool { return c >= 500 && c < 600 }},
		{"other", "#666", func(c int) bool { return c < 200 || c >= 600 || c == 0 }},
	}

	type series struct {
		fill   string
		counts []int64
	}
	allSeries := make([]series, len(categories))
	for i, cat := range categories {
		allSeries[i] = series{fill: cat.fill, counts: make([]int64, len(buckets))}
		for j, b := range buckets {
			if b == nil {
				continue
			}
			for code, n := range b.StatusCodes {
				if cat.match(code) {
					allSeries[i].counts[j] += n
				}
			}
		}
	}

	totals := make([]int64, len(buckets))
	var maxTotal int64
	for j := range buckets {
		for _, s := range allSeries {
			totals[j] += s.counts[j]
		}
		if totals[j] > maxTotal {
			maxTotal = totals[j]
		}
	}
	if maxTotal == 0 {
		return ""
	}

	const (
		w, hgt = 720, 200
		pl, pr = 56, 16
		pt, pb = 14, 28
	)
	plotW := w - pl - pr
	plotH := hgt - pt - pb
	cellW := math.Max(1, float64(plotW)/float64(len(buckets)))

	var b bytes.Buffer
	fmt.Fprintf(&b, `<svg viewBox="0 0 %d %d" xmlns="http://www.w3.org/2000/svg">`, w, hgt)
	fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="%d" fill="#0e0e0e" stroke="#222"/>`, pl, pt, plotW, plotH)

	for j := 0; j < len(buckets); j++ {
		if totals[j] == 0 {
			continue
		}
		x := pl + int(float64(j)*cellW)
		yTop := float64(pt + plotH)
		for _, s := range allSeries {
			if s.counts[j] == 0 {
				continue
			}
			h := float64(plotH) * float64(s.counts[j]) / float64(maxTotal)
			yTop -= h
			fmt.Fprintf(&b,
				`<rect data-status-area="1" x="%d" y="%.1f" width="%d" height="%.1f" fill="%s"/>`,
				x, yTop, int(math.Ceil(cellW)), h, s.fill,
			)
		}
	}

	// Legend along the bottom.
	lx := pl
	for _, cat := range categories {
		fmt.Fprintf(&b, `<rect x="%d" y="%d" width="10" height="10" fill="%s"/>`, lx, hgt-18, cat.fill)
		fmt.Fprintf(&b, `<text x="%d" y="%d" fill="#666" font-size="10">%s</text>`, lx+14, hgt-9, cat.label)
		lx += 60
	}

	fmt.Fprintf(&b, `<text x="%d" y="%d" fill="#666" font-size="10" text-anchor="end">peak %d/min</text>`, w-pr, pt-2, maxTotal)
	b.WriteString(`</svg>`)
	return b.String()
}

// fmtDurationFromMicros pretty-prints a microsecond value with the
// most readable unit; reused by both axis labels.
func fmtDurationFromMicros(us float64) string {
	if us < 1 {
		us = 1
	}
	switch {
	case us >= 1e6:
		return fmt.Sprintf("%.1fs", us/1e6)
	case us >= 1e3:
		return fmt.Sprintf("%.0fms", us/1e3)
	default:
		return fmt.Sprintf("%.0fµs", us)
	}
}
