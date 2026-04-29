package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kar98k/internal/daemon"
	"github.com/kar98k/internal/tui"
	"github.com/spf13/cobra"
)

var (
	doctorJSON          bool
	doctorMetricsAddr   string
	doctorMemSampleGap  time.Duration
	doctorRateTolerance float64
)

// CheckStatus is a doctor-local severity. Distinct from
// config.Severity because doctor needs an explicit "ok" tier and a
// "skip" tier for checks that couldn't run (e.g. daemon down).
type CheckStatus string

const (
	CheckOK   CheckStatus = "ok"
	CheckWarn CheckStatus = "warn"
	CheckFail CheckStatus = "fail"
	CheckSkip CheckStatus = "skip"
)

// CheckResult is one row in the doctor punch list.
type CheckResult struct {
	Name       string      `json:"name"`
	Status     CheckStatus `json:"status"`
	Message    string      `json:"message"`
	Suggestion string      `json:"suggestion,omitempty"`
}

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Run a one-shot health check across the kar98k daemon",
	Long: `Probe the running kar98k daemon and produce a punch-list health
report. Useful when something feels off (TPS lower than configured,
queue saturating, memory creeping) and you want a single command to
point at the cause.

Exits non-zero on any failed check, so this is safe to run from cron
or CI.

Examples:
  kar doctor
  kar doctor --json
  kar doctor --metrics-addr localhost:9090`,
	RunE: runDoctor,
}

func init() {
	doctorCmd.Flags().BoolVar(&doctorJSON, "json", false,
		"emit machine-readable JSON instead of the human punch list")
	doctorCmd.Flags().StringVar(&doctorMetricsAddr, "metrics-addr", "localhost:9090",
		"Prometheus /metrics endpoint to scrape for runtime stats")
	doctorCmd.Flags().DurationVar(&doctorMemSampleGap, "mem-gap", 2*time.Second,
		"interval between the two memory samples used to detect growth")
	doctorCmd.Flags().Float64Var(&doctorRateTolerance, "rate-tolerance", 0.05,
		"acceptable fractional drift between target TPS and actual TPS (0.05 = 5%)")
	rootCmd.AddCommand(doctorCmd)
}

func runDoctor(cmd *cobra.Command, args []string) error {
	results := []CheckResult{}

	// Liveness — gate every other daemon-side check on this one.
	resp, err := daemon.SendCommand(daemon.Command{Type: "status"})
	if err != nil {
		results = append(results, CheckResult{
			Name:       "daemon",
			Status:     CheckFail,
			Message:    fmt.Sprintf("not reachable: %v", err),
			Suggestion: "start it with: kar start",
		})
		emitDoctor(results)
		os.Exit(1)
	}

	statusData, _ := json.Marshal(resp.Data)
	var st daemon.Status
	_ = json.Unmarshal(statusData, &st)

	results = append(results, livenessCheck(st))

	if st.Triggered {
		results = append(results, rateAccuracyCheck(st))
		results = append(results, queueDropCheck(st))
	} else {
		results = append(results, CheckResult{
			Name:    "rate-accuracy",
			Status:  CheckSkip,
			Message: "daemon is armed but trigger has not been pulled",
		})
		results = append(results, CheckResult{
			Name:    "queue-drops",
			Status:  CheckSkip,
			Message: "no traffic to drop yet",
		})
	}

	results = append(results, targetHealthCheck())
	results = append(results, memTrendCheck())
	results = append(results, diskCheck())

	emitDoctor(results)
	if hasFailures(results) {
		os.Exit(1)
	}
	return nil
}

func livenessCheck(st daemon.Status) CheckResult {
	pidPath := daemon.GetPidPath()
	pid, _ := os.ReadFile(pidPath)
	return CheckResult{
		Name:    "daemon",
		Status:  CheckOK,
		Message: fmt.Sprintf("reachable (pid %s, uptime %s)", strings.TrimSpace(string(pid)), st.Uptime),
	}
}

func rateAccuracyCheck(st daemon.Status) CheckResult {
	if st.TargetTPS <= 0 {
		return CheckResult{
			Name:    "rate-accuracy",
			Status:  CheckSkip,
			Message: "target TPS is zero",
		}
	}
	drift := math.Abs(st.CurrentTPS-st.TargetTPS) / st.TargetTPS
	if drift <= doctorRateTolerance {
		return CheckResult{
			Name: "rate-accuracy",
			Status: CheckOK,
			Message: fmt.Sprintf("%.1f%% drift (target %.0f TPS, actual %.0f TPS)",
				drift*100, st.TargetTPS, st.CurrentTPS),
		}
	}
	severity := CheckWarn
	if drift > doctorRateTolerance*5 {
		severity = CheckFail
	}
	return CheckResult{
		Name:   "rate-accuracy",
		Status: severity,
		Message: fmt.Sprintf("%.1f%% drift (target %.0f TPS, actual %.0f TPS)",
			drift*100, st.TargetTPS, st.CurrentTPS),
		Suggestion: "increase pool_size or queue_size, or check target latency",
	}
}

func queueDropCheck(st daemon.Status) CheckResult {
	rate := st.QueueDropRate
	switch {
	case rate <= 0.001: // <0.1%
		return CheckResult{
			Name:    "queue-drops",
			Status:  CheckOK,
			Message: fmt.Sprintf("%d total drops (%.3f%% sustained)", st.QueueDrops, rate*100),
		}
	case rate <= 0.01: // <1%
		return CheckResult{
			Name:    "queue-drops",
			Status:  CheckWarn,
			Message: fmt.Sprintf("%d total drops (%.2f%% sustained)", st.QueueDrops, rate*100),
		}
	default:
		return CheckResult{
			Name: "queue-drops",
			Status: CheckFail,
			Message: fmt.Sprintf("%d total drops (%.2f%% sustained, > 1%% warn threshold)",
				st.QueueDrops, rate*100),
			Suggestion: "raise worker.queue_size; the worker log emits a recommended size",
		}
	}
}

// scrapeMetrics fetches and parses kar98k's /metrics endpoint. Returns
// a map of metric-name → first sample value found. Lines with labels
// are reduced to the metric base-name, so this is intentionally
// minimal — the doctor only needs scalar gauges/counters.
func scrapeMetrics() (map[string]float64, error) {
	url := "http://" + doctorMetricsAddr + "/metrics"
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	out := make(map[string]float64)
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Strip label braces if present: "metric{...} value"
		nameEnd := strings.IndexAny(line, " {")
		if nameEnd < 0 {
			continue
		}
		name := line[:nameEnd]
		valStart := strings.LastIndexByte(line, ' ')
		if valStart < 0 || valStart == nameEnd {
			continue
		}
		v, err := strconv.ParseFloat(line[valStart+1:], 64)
		if err != nil {
			continue
		}
		// Keep first sample per name — sufficient for scalars.
		if _, ok := out[name]; !ok {
			out[name] = v
		}
	}
	return out, nil
}

func targetHealthCheck() CheckResult {
	url := "http://" + doctorMetricsAddr + "/metrics"
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return CheckResult{
			Name:    "target-health",
			Status:  CheckSkip,
			Message: fmt.Sprintf("metrics endpoint unreachable: %v", err),
		}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	healthy, unhealthy := 0, 0
	for _, line := range strings.Split(string(body), "\n") {
		if !strings.HasPrefix(line, "kar98k_target_health{") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) != 2 {
			continue
		}
		v, err := strconv.ParseFloat(parts[1], 64)
		if err != nil {
			continue
		}
		if v == 1 {
			healthy++
		} else {
			unhealthy++
		}
	}

	switch {
	case healthy == 0 && unhealthy == 0:
		return CheckResult{
			Name:    "target-health",
			Status:  CheckSkip,
			Message: "no target health samples yet",
		}
	case unhealthy == 0:
		return CheckResult{
			Name:    "target-health",
			Status:  CheckOK,
			Message: fmt.Sprintf("all %d targets healthy", healthy),
		}
	default:
		return CheckResult{
			Name: "target-health",
			Status: CheckFail,
			Message: fmt.Sprintf("%d unhealthy / %d healthy", unhealthy, healthy),
			Suggestion: "check the target URL and the daemon log for the failing health probe",
		}
	}
}

func memTrendCheck() CheckResult {
	first, err := scrapeMetrics()
	if err != nil {
		return CheckResult{
			Name:    "memory",
			Status:  CheckSkip,
			Message: fmt.Sprintf("metrics endpoint unreachable: %v", err),
		}
	}
	a, ok1 := first["go_memstats_alloc_bytes"]
	if !ok1 {
		return CheckResult{
			Name:    "memory",
			Status:  CheckSkip,
			Message: "go_memstats_alloc_bytes missing from /metrics",
		}
	}

	time.Sleep(doctorMemSampleGap)

	second, err := scrapeMetrics()
	if err != nil {
		return CheckResult{
			Name:    "memory",
			Status:  CheckSkip,
			Message: fmt.Sprintf("second scrape failed: %v", err),
		}
	}
	b, ok2 := second["go_memstats_alloc_bytes"]
	if !ok2 {
		return CheckResult{
			Name:    "memory",
			Status:  CheckSkip,
			Message: "go_memstats_alloc_bytes disappeared between scrapes",
		}
	}

	// Note: heap allocation oscillates between GC cycles. Treat a >25%
	// jump in the gap as warn-worthy; the doctor isn't a profiler.
	if a == 0 {
		return CheckResult{
			Name:    "memory",
			Status:  CheckSkip,
			Message: "alloc_bytes baseline is zero",
		}
	}
	delta := (b - a) / a
	humanA := humanBytes(a)
	humanB := humanBytes(b)
	switch {
	case delta < -0.5:
		// huge dip → very recent GC, not informative
		return CheckResult{
			Name:    "memory",
			Status:  CheckOK,
			Message: fmt.Sprintf("steady (%s now, was %s — GC just ran)", humanB, humanA),
		}
	case delta > 0.25:
		return CheckResult{
			Name:       "memory",
			Status:     CheckWarn,
			Message:    fmt.Sprintf("growing (%s → %s in %s, +%.0f%%)", humanA, humanB, doctorMemSampleGap, delta*100),
			Suggestion: "let it run a bit longer; if growth is sustained, check for leaks (run with --memprofile)",
		}
	default:
		return CheckResult{
			Name:    "memory",
			Status:  CheckOK,
			Message: fmt.Sprintf("steady (%s, %+.0f%% over %s)", humanB, delta*100, doctorMemSampleGap),
		}
	}
}

func diskCheck() CheckResult {
	dir := daemon.GetRuntimeDir()
	var total int64
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			// Don't bail on the walk just because one entry is gone.
			return nil
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	if err != nil {
		return CheckResult{
			Name:    "disk",
			Status:  CheckSkip,
			Message: fmt.Sprintf("could not walk %s: %v", dir, err),
		}
	}
	switch {
	case total > 1024*1024*1024: // > 1 GiB
		return CheckResult{
			Name: "disk",
			Status: CheckWarn,
			Message: fmt.Sprintf("runtime dir %s = %s", dir, humanBytes(float64(total))),
			Suggestion: "consider rotating the daemon log",
		}
	default:
		return CheckResult{
			Name:    "disk",
			Status:  CheckOK,
			Message: fmt.Sprintf("runtime dir %s = %s", dir, humanBytes(float64(total))),
		}
	}
}

func humanBytes(b float64) string {
	const (
		KiB = 1024.0
		MiB = KiB * 1024
		GiB = MiB * 1024
	)
	switch {
	case b >= GiB:
		return fmt.Sprintf("%.1f GiB", b/GiB)
	case b >= MiB:
		return fmt.Sprintf("%.1f MiB", b/MiB)
	case b >= KiB:
		return fmt.Sprintf("%.1f KiB", b/KiB)
	default:
		return fmt.Sprintf("%.0f B", b)
	}
}

func hasFailures(rs []CheckResult) bool {
	for _, r := range rs {
		if r.Status == CheckFail {
			return true
		}
	}
	return false
}

func emitDoctor(rs []CheckResult) {
	if doctorJSON {
		out := struct {
			Checks []CheckResult `json:"checks"`
			OK     bool          `json:"ok"`
		}{
			Checks: rs,
			OK:     !hasFailures(rs),
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out)
		return
	}

	fmt.Println()
	fmt.Println(tui.SubtitleStyle.Render("  kar doctor"))
	fmt.Println()
	for _, r := range rs {
		var icon string
		var styled func(...string) string
		switch r.Status {
		case CheckOK:
			icon = "✓"
			styled = tui.SuccessStyle.Render
		case CheckWarn:
			icon = "!"
			styled = tui.WarningStyle.Render
		case CheckFail:
			icon = "✗"
			styled = tui.ErrorStyle.Render
		default:
			icon = "·"
			styled = tui.DimStyle.Render
		}
		fmt.Printf("  %s %s — %s\n",
			styled(icon),
			tui.LabelStyle.Render(r.Name),
			r.Message)
		if r.Suggestion != "" {
			fmt.Println(tui.DimStyle.Render("      ↳ " + r.Suggestion))
		}
	}
	fmt.Println()
}
