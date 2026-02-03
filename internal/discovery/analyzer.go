package discovery

import (
	"sort"
	"sync"
	"time"
)

// Analyzer collects and analyzes real-time metrics for discovery.
type Analyzer struct {
	mu sync.RWMutex

	// Sliding window for latencies (in milliseconds)
	latencies []float64
	// Sliding window timestamps
	timestamps []time.Time

	// Request counts
	totalRequests int64
	totalErrors   int64

	// Window duration for analysis
	windowDuration time.Duration

	// Maximum samples to keep
	maxSamples int
}

// NewAnalyzer creates a new Analyzer with a sliding window.
func NewAnalyzer(windowDuration time.Duration) *Analyzer {
	return &Analyzer{
		latencies:      make([]float64, 0, 10000),
		timestamps:     make([]time.Time, 0, 10000),
		windowDuration: windowDuration,
		maxSamples:     100000, // Keep at most 100k samples
	}
}

// RecordLatency records a single request latency.
func (a *Analyzer) RecordLatency(latencyMs float64, isError bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := time.Now()

	a.latencies = append(a.latencies, latencyMs)
	a.timestamps = append(a.timestamps, now)
	a.totalRequests++

	if isError {
		a.totalErrors++
	}

	// Trim old samples if we have too many
	if len(a.latencies) > a.maxSamples {
		// Remove oldest 10%
		trimCount := a.maxSamples / 10
		a.latencies = a.latencies[trimCount:]
		a.timestamps = a.timestamps[trimCount:]
	}
}

// GetP95Latency returns the P95 latency from the sliding window.
func (a *Analyzer) GetP95Latency() float64 {
	a.mu.RLock()
	defer a.mu.RUnlock()

	windowLatencies := a.getWindowLatencies()
	if len(windowLatencies) == 0 {
		return 0
	}

	return percentile(windowLatencies, 95)
}

// GetP99Latency returns the P99 latency from the sliding window.
func (a *Analyzer) GetP99Latency() float64 {
	a.mu.RLock()
	defer a.mu.RUnlock()

	windowLatencies := a.getWindowLatencies()
	if len(windowLatencies) == 0 {
		return 0
	}

	return percentile(windowLatencies, 99)
}

// GetErrorRate returns the error rate as a percentage.
func (a *Analyzer) GetErrorRate() float64 {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if a.totalRequests == 0 {
		return 0
	}

	return float64(a.totalErrors) / float64(a.totalRequests) * 100
}

// GetWindowErrorRate returns the error rate within the sliding window.
func (a *Analyzer) GetWindowErrorRate() float64 {
	a.mu.RLock()
	defer a.mu.RUnlock()

	windowLatencies := a.getWindowLatencies()
	if len(windowLatencies) == 0 {
		return 0
	}

	// For now, return total error rate
	// In a more sophisticated implementation, we'd track errors per window
	return float64(a.totalErrors) / float64(a.totalRequests) * 100
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

// GetAvgLatency returns the average latency from the sliding window.
func (a *Analyzer) GetAvgLatency() float64 {
	a.mu.RLock()
	defer a.mu.RUnlock()

	windowLatencies := a.getWindowLatencies()
	if len(windowLatencies) == 0 {
		return 0
	}

	var sum float64
	for _, l := range windowLatencies {
		sum += l
	}

	return sum / float64(len(windowLatencies))
}

// GetSampleCount returns the number of samples in the current window.
func (a *Analyzer) GetSampleCount() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.getWindowLatencies())
}

// Reset clears all collected data.
func (a *Analyzer) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.latencies = a.latencies[:0]
	a.timestamps = a.timestamps[:0]
	a.totalRequests = 0
	a.totalErrors = 0
}

// ResetWindow clears only the window data but keeps total counts.
func (a *Analyzer) ResetWindow() {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.latencies = a.latencies[:0]
	a.timestamps = a.timestamps[:0]
}

// getWindowLatencies returns latencies within the sliding window.
// Must be called with lock held.
func (a *Analyzer) getWindowLatencies() []float64 {
	if len(a.latencies) == 0 {
		return nil
	}

	cutoff := time.Now().Add(-a.windowDuration)
	startIdx := 0

	// Find the first index within the window
	for i, ts := range a.timestamps {
		if ts.After(cutoff) {
			startIdx = i
			break
		}
	}

	if startIdx >= len(a.latencies) {
		return nil
	}

	return a.latencies[startIdx:]
}

// percentile calculates the p-th percentile of the data.
func percentile(data []float64, p float64) float64 {
	if len(data) == 0 {
		return 0
	}

	// Make a copy to avoid modifying the original
	sorted := make([]float64, len(data))
	copy(sorted, data)
	sort.Float64s(sorted)

	index := int(float64(len(sorted)-1) * p / 100)
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}

	return sorted[index]
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
func (a *Analyzer) TakeSnapshot() Snapshot {
	return Snapshot{
		P95Latency:    a.GetP95Latency(),
		P99Latency:    a.GetP99Latency(),
		AvgLatency:    a.GetAvgLatency(),
		ErrorRate:     a.GetErrorRate(),
		TotalRequests: a.GetTotalRequests(),
		TotalErrors:   a.GetTotalErrors(),
		SampleCount:   a.GetSampleCount(),
		Timestamp:     time.Now(),
	}
}
