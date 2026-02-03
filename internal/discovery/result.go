package discovery

import "time"

// Result holds the discovery test results.
type Result struct {
	// SustainedTPS is the maximum TPS that can be maintained stably.
	SustainedTPS float64

	// BreakingTPS is the TPS at which the system starts to degrade.
	BreakingTPS float64

	// P95Latency is the P95 latency at sustained TPS (in milliseconds).
	P95Latency float64

	// ErrorRate is the error rate at sustained TPS (percentage).
	ErrorRate float64

	// TestDuration is the total duration of the discovery test.
	TestDuration time.Duration

	// StepsCompleted is the number of binary search steps completed.
	StepsCompleted int

	// Recommendation provides suggested configuration values.
	Recommendation Recommendation
}

// Recommendation provides suggested TPS configuration values.
type Recommendation struct {
	// BaseTPS is the recommended base TPS (80% of sustained).
	BaseTPS float64

	// MaxTPS is the recommended max TPS (safe spike limit).
	MaxTPS float64

	// Description provides a human-readable recommendation.
	Description string
}

// StepResult holds the result of a single TPS step test.
type StepResult struct {
	// TPS is the TPS tested in this step.
	TPS float64

	// P95Latency is the P95 latency during this step (in milliseconds).
	P95Latency float64

	// ErrorRate is the error rate during this step (percentage).
	ErrorRate float64

	// Stable indicates if the system was stable at this TPS.
	Stable bool

	// Duration is how long this step ran.
	Duration time.Duration

	// TotalRequests is the total requests made during this step.
	TotalRequests int64

	// TotalErrors is the total errors during this step.
	TotalErrors int64
}

// NewResult creates a new Result with recommendations based on discovered values.
func NewResult(sustainedTPS, breakingTPS, p95Latency, errorRate float64, duration time.Duration, steps int) *Result {
	r := &Result{
		SustainedTPS:   sustainedTPS,
		BreakingTPS:    breakingTPS,
		P95Latency:     p95Latency,
		ErrorRate:      errorRate,
		TestDuration:   duration,
		StepsCompleted: steps,
	}

	// Generate recommendations
	r.Recommendation = Recommendation{
		BaseTPS: sustainedTPS * 0.8, // 80% of sustained for safety margin
		MaxTPS:  breakingTPS * 0.9,  // 90% of breaking point for spike limit
	}

	if r.Recommendation.MaxTPS < r.Recommendation.BaseTPS*2 {
		r.Recommendation.MaxTPS = r.Recommendation.BaseTPS * 2
	}

	r.Recommendation.Description = generateDescription(r)

	return r
}

func generateDescription(r *Result) string {
	return "Set BaseTPS to " + formatTPS(r.Recommendation.BaseTPS) +
		" (80% of sustained) and MaxTPS to " + formatTPS(r.Recommendation.MaxTPS) +
		" (safe spike limit)"
}

func formatTPS(tps float64) string {
	if tps >= 1000 {
		return formatFloat(tps/1000, 1) + "k"
	}
	return formatFloat(tps, 0)
}

func formatFloat(f float64, decimals int) string {
	if decimals == 0 {
		return intToString(int(f))
	}
	whole := int(f)
	frac := int((f - float64(whole)) * 10)
	return intToString(whole) + "." + intToString(frac)
}

func intToString(i int) string {
	if i == 0 {
		return "0"
	}
	if i < 0 {
		return "-" + intToString(-i)
	}
	s := ""
	for i > 0 {
		s = string(rune('0'+i%10)) + s
		i /= 10
	}
	return s
}
