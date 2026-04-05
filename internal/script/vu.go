package script

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// VUScheduler manages virtual user lifecycle and ramping.
type VUScheduler struct {
	runner     Runner
	stages     []Stage
	vus        int
	duration   time.Duration
	setupData  interface{}

	// Runtime state
	activeVUs  int64
	iterations int64
	startTime  time.Time
}

// NewVUScheduler creates a scheduler from a runner's scenario config.
func NewVUScheduler(runner Runner, vusOverride int, durationOverride time.Duration) *VUScheduler {
	s := &VUScheduler{
		runner: runner,
	}

	sc := runner.Scenario()
	s.stages = sc.Stages

	if vusOverride > 0 {
		s.vus = vusOverride
		s.stages = nil // Use flat VU count
	}
	if durationOverride > 0 {
		s.duration = durationOverride
	}

	// If no stages and no explicit VUs, default to 1 VU for 30s
	if len(s.stages) == 0 && s.vus == 0 {
		s.vus = 1
		if s.duration == 0 {
			s.duration = 30 * time.Second
		}
	}

	return s
}

// Run executes the full VU lifecycle.
func (s *VUScheduler) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Setup phase
	fmt.Println("\n  Running setup...")
	data, err := s.runner.Setup()
	if err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}
	s.setupData = data

	s.startTime = time.Now()

	// Determine total duration
	totalDuration := s.duration
	if len(s.stages) > 0 {
		totalDuration = 0
		for _, st := range s.stages {
			totalDuration += st.Duration
		}
	}

	fmt.Printf("  Duration: %s\n\n", totalDuration)

	// Run VUs
	if len(s.stages) > 0 {
		err = s.runWithStages(ctx)
	} else {
		err = s.runFlat(ctx)
	}

	// Teardown phase
	fmt.Println("\n  Running teardown...")
	if tdErr := s.runner.Teardown(s.setupData); tdErr != nil {
		fmt.Printf("  Teardown error: %v\n", tdErr)
	}

	return err
}

// runFlat runs a fixed number of VUs for a fixed duration.
func (s *VUScheduler) runFlat(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, s.duration)
	defer cancel()

	var wg sync.WaitGroup
	for i := 0; i < s.vus; i++ {
		wg.Add(1)
		go func(vuID int) {
			defer wg.Done()
			s.vuLoop(ctx, vuID)
		}(i)
	}

	// Progress reporting
	go s.reportProgress(ctx, s.duration)

	wg.Wait()
	return nil
}

// runWithStages ramps VUs according to stages.
func (s *VUScheduler) runWithStages(ctx context.Context) error {
	var totalDuration time.Duration
	for _, st := range s.stages {
		totalDuration += st.Duration
	}

	ctx, cancel := context.WithTimeout(ctx, totalDuration)
	defer cancel()

	var (
		mu        sync.Mutex
		currentVU int
		vus       []*vuHandle
	)

	// VU management
	addVUs := func(n int) {
		mu.Lock()
		defer mu.Unlock()
		for i := 0; i < n; i++ {
			currentVU++
			h := &vuHandle{cancel: make(chan struct{})}
			vus = append(vus, h)
			go func(vuID int, handle *vuHandle) {
				atomic.AddInt64(&s.activeVUs, 1)
				defer atomic.AddInt64(&s.activeVUs, -1)

				vuCtx, vuCancel := context.WithCancel(ctx)
				go func() {
					select {
					case <-handle.cancel:
						vuCancel()
					case <-vuCtx.Done():
					}
				}()

				s.vuLoop(vuCtx, vuID)
				vuCancel()
			}(currentVU, h)
		}
	}

	removeVUs := func(n int) {
		mu.Lock()
		defer mu.Unlock()
		for i := 0; i < n && len(vus) > 0; i++ {
			last := vus[len(vus)-1]
			close(last.cancel)
			vus = vus[:len(vus)-1]
		}
	}

	// Progress reporting
	go s.reportProgress(ctx, totalDuration)

	// Execute stages
	prevTarget := 0
	for _, st := range s.stages {
		target := st.Target
		diff := target - prevTarget

		if diff > 0 {
			// Gradually add VUs over the stage duration
			interval := st.Duration / time.Duration(diff)
			for i := 0; i < diff; i++ {
				select {
				case <-ctx.Done():
					removeVUs(len(vus))
					return nil
				case <-time.After(interval):
					addVUs(1)
				}
			}
		} else if diff < 0 {
			// Gradually remove VUs
			interval := st.Duration / time.Duration(-diff)
			for i := 0; i < -diff; i++ {
				select {
				case <-ctx.Done():
					removeVUs(len(vus))
					return nil
				case <-time.After(interval):
					removeVUs(1)
				}
			}
		} else {
			// Hold steady
			select {
			case <-ctx.Done():
				removeVUs(len(vus))
				return nil
			case <-time.After(st.Duration):
			}
		}

		prevTarget = target
	}

	// Clean up remaining VUs
	removeVUs(len(vus))
	return nil
}

type vuHandle struct {
	cancel chan struct{}
}

// vuLoop runs iterations until context is cancelled.
func (s *VUScheduler) vuLoop(ctx context.Context, vuID int) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if err := s.runner.Iterate(vuID, s.setupData); err != nil {
			// Log but continue
			_ = err
		}
		atomic.AddInt64(&s.iterations, 1)
	}
}

// reportProgress prints progress every 2 seconds.
func (s *VUScheduler) reportProgress(ctx context.Context, totalDuration time.Duration) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			elapsed := time.Since(s.startTime)
			iters := atomic.LoadInt64(&s.iterations)
			vus := atomic.LoadInt64(&s.activeVUs)
			m := s.runner.Metrics()

			pct := float64(elapsed) / float64(totalDuration) * 100
			if pct > 100 {
				pct = 100
			}

			fmt.Printf("\r  [%5.1f%%] VUs: %d | Iterations: %d | Requests: %d | Errors: %d | Elapsed: %s",
				pct, vus, iters, atomic.LoadInt64(&m.TotalRequests), atomic.LoadInt64(&m.TotalErrors),
				elapsed.Round(time.Second))
		}
	}
}

// PrintReport prints the final test report.
func PrintReport(runner Runner, elapsed time.Duration) {
	m := runner.Metrics()
	sc := runner.Scenario()

	fmt.Println("\n")
	fmt.Println("  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Printf("  TEST REPORT: %s\n", sc.Name)
	fmt.Println("  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	totalReqs := atomic.LoadInt64(&m.TotalRequests)
	totalErrs := atomic.LoadInt64(&m.TotalErrors)
	successRate := float64(0)
	if totalReqs > 0 {
		successRate = float64(totalReqs-totalErrs) / float64(totalReqs) * 100
	}

	fmt.Printf("\n  Requests:     %d\n", totalReqs)
	fmt.Printf("  Errors:       %d\n", totalErrs)
	fmt.Printf("  Success Rate: %.1f%%\n", successRate)
	fmt.Printf("  Duration:     %s\n", elapsed.Round(time.Millisecond))

	if totalReqs > 0 {
		rps := float64(totalReqs) / elapsed.Seconds()
		fmt.Printf("  RPS:          %.1f\n", rps)
	}

	// Latency stats
	m.mu.Lock()
	durations := make([]float64, len(m.Durations))
	copy(durations, m.Durations)
	m.mu.Unlock()

	if len(durations) > 0 {
		sort.Float64s(durations)

		avg := 0.0
		for _, d := range durations {
			avg += d
		}
		avg /= float64(len(durations))

		fmt.Printf("\n  Latency:\n")
		fmt.Printf("    Min:  %s\n", fmtDuration(durations[0]))
		fmt.Printf("    Avg:  %s\n", fmtDuration(avg))
		fmt.Printf("    Max:  %s\n", fmtDuration(durations[len(durations)-1]))
		fmt.Printf("    P50:  %s\n", fmtDuration(percentile(durations, 50)))
		fmt.Printf("    P95:  %s\n", fmtDuration(percentile(durations, 95)))
		fmt.Printf("    P99:  %s\n", fmtDuration(percentile(durations, 99)))
	}

	// Status codes
	m.mu.Lock()
	if len(m.StatusCodes) > 0 {
		fmt.Printf("\n  Status Codes:\n")
		for code, count := range m.StatusCodes {
			fmt.Printf("    %d: %d\n", code, count)
		}
	}

	// Checks
	if len(m.Checks) > 0 {
		fmt.Printf("\n  Checks:\n")
		for _, c := range m.Checks {
			total := c.Passed + c.Failed
			pct := float64(c.Passed) / float64(total) * 100
			mark := "✓"
			if c.Failed > 0 {
				mark = "✗"
			}
			fmt.Printf("    %s %s: %.0f%% (%d/%d)\n", mark, c.Name, pct, c.Passed, total)
		}
	}
	m.mu.Unlock()

	// Thresholds
	if len(sc.Thresholds) > 0 {
		fmt.Printf("\n  Thresholds:\n")
		for metric, condition := range sc.Thresholds {
			// Simple threshold evaluation
			passed := evaluateThreshold(metric, condition, m, durations)
			mark := "✓"
			if !passed {
				mark = "✗"
			}
			fmt.Printf("    %s %s: %s\n", mark, metric, condition)
		}
	}

	fmt.Println()
}

func percentile(sorted []float64, p float64) float64 {
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

func fmtDuration(seconds float64) string {
	d := time.Duration(seconds * float64(time.Second))
	if d < time.Millisecond {
		return fmt.Sprintf("%.0fµs", float64(d)/float64(time.Microsecond))
	}
	if d < time.Second {
		return fmt.Sprintf("%.1fms", float64(d)/float64(time.Millisecond))
	}
	return fmt.Sprintf("%.2fs", seconds)
}

func evaluateThreshold(metric, condition string, m *Metrics, durations []float64) bool {
	// Basic threshold evaluation for common patterns
	switch {
	case metric == "http_req_failed":
		totalReqs := atomic.LoadInt64(&m.TotalRequests)
		totalErrs := atomic.LoadInt64(&m.TotalErrors)
		if totalReqs == 0 {
			return true
		}
		errorRate := float64(totalErrs) / float64(totalReqs)
		return parseAndCompare(errorRate, condition)

	case metric == "checks":
		var totalPassed, totalAll int64
		m.mu.Lock()
		for _, c := range m.Checks {
			totalPassed += c.Passed
			totalAll += c.Passed + c.Failed
		}
		m.mu.Unlock()
		if totalAll == 0 {
			return true
		}
		rate := float64(totalPassed) / float64(totalAll)
		return parseAndCompare(rate, condition)

	default:
		// Duration-based thresholds like http_req_duration{p95}
		if len(durations) == 0 {
			return true
		}
		if contains(metric, "p95") {
			p95 := percentile(durations, 95) * 1000 // to ms
			return parseAndCompareDuration(p95, condition)
		}
		if contains(metric, "p99") {
			p99 := percentile(durations, 99) * 1000
			return parseAndCompareDuration(p99, condition)
		}
		return true
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func parseAndCompare(value float64, condition string) bool {
	var op string
	var threshold float64
	if _, err := fmt.Sscanf(condition, "%s %f", &op, &threshold); err != nil {
		return true
	}
	switch op {
	case "<":
		return value < threshold
	case "<=":
		return value <= threshold
	case ">":
		return value > threshold
	case ">=":
		return value >= threshold
	default:
		return true
	}
}

func parseAndCompareDuration(valueMs float64, condition string) bool {
	var op string
	var thresholdStr string
	if _, err := fmt.Sscanf(condition, "%s %s", &op, &thresholdStr); err != nil {
		return true
	}
	// Parse duration like "500ms"
	d, err := time.ParseDuration(thresholdStr)
	if err != nil {
		return true
	}
	thresholdMs := float64(d) / float64(time.Millisecond)

	switch op {
	case "<":
		return valueMs < thresholdMs
	case "<=":
		return valueMs <= thresholdMs
	case ">":
		return valueMs > thresholdMs
	case ">=":
		return valueMs >= thresholdMs
	default:
		return true
	}
}
