package discovery

import (
	"sync"
	"time"

	hdrhistogram "github.com/HdrHistogram/hdrhistogram-go"
)

// Histogram bounds: latencies are recorded in microseconds with three
// significant digits, matching internal/script/builtins.go. The 1µs..60s
// range covers everything from sub-ms in-memory targets to long timeouts
// without requiring resizing.
const (
	hdrMin    = 1
	hdrMax    = 60_000_000
	hdrSigFig = 3
)

// Analyzer collects and analyzes real-time latency / error metrics for
// the adaptive discovery loop. The "window" referred to here is bounded
// by ResetWindow() calls (one per discovery step) — there is no
// time-based eviction. When the discovery controller starts a new step
// it calls ResetWindow(), and percentile/avg readings via TakeSnapshot
// describe latencies recorded since that point.
type Analyzer struct {
	mu sync.RWMutex

	// Per-step rolling histogram, cleared by ResetWindow().
	window *hdrhistogram.Histogram
	// Lifetime histogram, used for the cumulative average and snapshot
	// SampleCount across discovery as a whole.
	total *hdrhistogram.Histogram

	totalRequests int64
	totalErrors   int64
}

// NewAnalyzer creates a new Analyzer.
func NewAnalyzer() *Analyzer {
	return &Analyzer{
		window: hdrhistogram.New(hdrMin, hdrMax, hdrSigFig),
		total:  hdrhistogram.New(hdrMin, hdrMax, hdrSigFig),
	}
}

// RecordLatency records a single request latency (milliseconds) and its
// error status.
func (a *Analyzer) RecordLatency(latencyMs float64, isError bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	micros := int64(latencyMs * 1000)
	if micros < hdrMin {
		micros = hdrMin
	} else if micros > hdrMax {
		micros = hdrMax
	}

	_ = a.window.RecordValue(micros)
	_ = a.total.RecordValue(micros)
	a.totalRequests++

	if isError {
		a.totalErrors++
	}
}

// GetP95Latency returns the P95 latency in the current window, in ms.
func (a *Analyzer) GetP95Latency() float64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return microsToMs(a.window.ValueAtQuantile(95))
}

// GetP99Latency returns the P99 latency in the current window, in ms.
func (a *Analyzer) GetP99Latency() float64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return microsToMs(a.window.ValueAtQuantile(99))
}

// GetAvgLatency returns the mean latency in the current window, in ms.
func (a *Analyzer) GetAvgLatency() float64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.window.TotalCount() == 0 {
		return 0
	}
	return a.window.Mean() / 1000.0
}

// GetSampleCount returns the number of samples recorded in the current window.
func (a *Analyzer) GetSampleCount() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return int(a.window.TotalCount())
}

// GetErrorRate returns the lifetime error rate as a percentage.
func (a *Analyzer) GetErrorRate() float64 {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if a.totalRequests == 0 {
		return 0
	}
	return float64(a.totalErrors) / float64(a.totalRequests) * 100
}

// GetWindowErrorRate currently mirrors GetErrorRate. We do not bucket
// errors per window — the discovery controller computes per-step error
// rates directly from request counters around runStep().
func (a *Analyzer) GetWindowErrorRate() float64 {
	return a.GetErrorRate()
}

// GetTotalRequests returns the total number of requests recorded.
func (a *Analyzer) GetTotalRequests() int64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.totalRequests
}

// GetTotalErrors returns the total number of errors recorded.
func (a *Analyzer) GetTotalErrors() int64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.totalErrors
}

// Reset clears all collected data.
func (a *Analyzer) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.window.Reset()
	a.total.Reset()
	a.totalRequests = 0
	a.totalErrors = 0
}

// ResetWindow clears the per-step window but keeps lifetime totals.
func (a *Analyzer) ResetWindow() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.window.Reset()
}

// Snapshot captures the current state of the analyzer.
type Snapshot struct {
	P95Latency    float64
	P99Latency    float64
	AvgLatency    float64
	ErrorRate     float64
	TotalRequests int64
	TotalErrors   int64
	SampleCount   int
	Timestamp     time.Time
}

// TakeSnapshot returns a point-in-time snapshot of the analyzer state.
// All fields are computed under a single lock to avoid the torn-read
// problem the slice-based implementation had.
func (a *Analyzer) TakeSnapshot() Snapshot {
	a.mu.RLock()
	defer a.mu.RUnlock()

	var errorRate float64
	if a.totalRequests > 0 {
		errorRate = float64(a.totalErrors) / float64(a.totalRequests) * 100
	}

	var avg float64
	if a.window.TotalCount() > 0 {
		avg = a.window.Mean() / 1000.0
	}

	return Snapshot{
		P95Latency:    microsToMs(a.window.ValueAtQuantile(95)),
		P99Latency:    microsToMs(a.window.ValueAtQuantile(99)),
		AvgLatency:    avg,
		ErrorRate:     errorRate,
		TotalRequests: a.totalRequests,
		TotalErrors:   a.totalErrors,
		SampleCount:   int(a.window.TotalCount()),
		Timestamp:     time.Now(),
	}
}

func microsToMs(m int64) float64 {
	return float64(m) / 1000.0
}
