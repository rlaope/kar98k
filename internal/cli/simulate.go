package cli

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/kar98k/internal/config"
	"github.com/kar98k/internal/controller"
	"github.com/kar98k/internal/pattern"
	"github.com/kar98k/internal/tui"
	"github.com/spf13/cobra"
)

var (
	simulateConfigPath string
	simulateDuration   time.Duration
	simulateResolution time.Duration
	simulateSeed       int64
	simulateFormat     string
	simulateStartFlag  string
)

var simulateCmd = &cobra.Command{
	Use:   "simulate",
	Short: "Forecast traffic curve from config without sending requests",
	Long: `Run a dry-run simulation of the configured traffic pattern.

simulate evaluates the pattern engine across a window without sending
any requests. It produces a deterministic timeline (given --seed) of
target TPS, schedule multiplier, and spike events — useful for
sanity-checking a config before pointing it at production targets.

Examples:
  kar simulate --config configs/kar98k.yaml
  kar simulate --duration 24h --resolution 5m --format csv > forecast.csv
  kar simulate --seed 42 --format json | jq '.[].tps'`,
	RunE: runSimulate,
}

func init() {
	simulateCmd.Flags().StringVar(&simulateConfigPath, "config", "configs/kar98k.yaml", "config file path")
	simulateCmd.Flags().DurationVar(&simulateDuration, "duration", 24*time.Hour, "simulation window length")
	simulateCmd.Flags().DurationVar(&simulateResolution, "resolution", 5*time.Minute, "sample interval")
	simulateCmd.Flags().Int64Var(&simulateSeed, "seed", 0, "Poisson seed (0 = wall clock)")
	simulateCmd.Flags().StringVar(&simulateFormat, "format", "text", "output format: text|csv|json")
	simulateCmd.Flags().StringVar(&simulateStartFlag, "start", "", "RFC3339 start time (default: top of current hour)")
	rootCmd.AddCommand(simulateCmd)
}

func runSimulate(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load(simulateConfigPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	start := time.Now().Truncate(time.Hour)
	if simulateStartFlag != "" {
		start, err = time.Parse(time.RFC3339, simulateStartFlag)
		if err != nil {
			return fmt.Errorf("parse --start: %w", err)
		}
	}

	var pts []pattern.SamplePoint
	sched := controller.NewScheduler(cfg.Controller.Schedule)

	if len(cfg.Scenarios) == 0 {
		// No scenarios: original behavior, unchanged.
		pts = pattern.SimulateTimeline(
			cfg.Pattern,
			cfg.Controller.BaseTPS,
			cfg.Controller.MaxTPS,
			sched.GetMultiplierForHour,
			start,
			simulateDuration,
			simulateResolution,
			simulateSeed,
		)
	} else {
		pts = simulateScenarios(cfg, sched, start)
	}

	switch simulateFormat {
	case "csv":
		return printSimulateCSV(pts)
	case "json":
		return printSimulateJSON(pts)
	case "text", "":
		printSimulateText(cfg, pts)
		return nil
	default:
		return fmt.Errorf("unknown --format %q (text|csv|json)", simulateFormat)
	}
}

// scenariosTotalDuration is the sum of every phase's Duration; used by
// the header so the printed window matches what was actually simulated
// rather than the --duration flag default.
func scenariosTotalDuration(scenarios []config.Scenario) time.Duration {
	var total time.Duration
	for _, s := range scenarios {
		total += s.Duration
	}
	return total
}

// simulateScenarios walks the scenario list and returns a timeline that
// reflects each phase's inject curve (or base_tps) with Poisson and
// noise overlays applied on top. It does not call time.Now().
func simulateScenarios(cfg *config.Config, sched *controller.Scheduler, start time.Time) []pattern.SamplePoint {
	// Compute total scenario duration and warn if --duration disagrees.
	var totalDuration time.Duration
	for _, sc := range cfg.Scenarios {
		totalDuration += sc.Duration
	}
	if cmd := simulateDuration; cmd != 24*time.Hour {
		// User explicitly set --duration; warn if it differs from scenario total.
		if cmd != totalDuration {
			fmt.Fprintf(os.Stderr, "warning: --duration %s ignored when scenarios are present; using scenario total %s\n",
				cmd, totalDuration)
		}
	}

	// Pre-generate Poisson spike events for the full window so that
	// spike timing is coherent across phase boundaries.
	patCfg := cfg.Pattern
	seed := simulateSeed
	if seed == 0 {
		seed = start.UnixNano()
	}

	// We call SimulateTimeline per phase with a flat baseTPS equal to
	// the inject value at each sample, then override TPS in the returned
	// points. Simpler: build points directly mirroring SimulateTimeline's
	// inner loop, calling InjectTPSAt for the TPS base.

	var out []pattern.SamplePoint
	cursor := start

	for _, sc := range cfg.Scenarios {
		// Resolve effective pattern for this phase.
		phasePat := patCfg
		if sc.Pattern != nil {
			phasePat = *sc.Pattern
		}

		// Resolve base and max TPS for this phase.
		phaseBaseTPS := cfg.Controller.BaseTPS
		if sc.BaseTPS > 0 {
			phaseBaseTPS = sc.BaseTPS
		}
		phaseMaxTPS := cfg.Controller.MaxTPS
		if sc.MaxTPS > 0 {
			phaseMaxTPS = sc.MaxTPS
		}

		phaseEnd := cursor.Add(sc.Duration)

		// Generate Poisson events for this phase window only (same math
		// as SimulateTimeline uses).
		phasePts := pattern.SimulateTimeline(
			phasePat,
			phaseBaseTPS,
			phaseMaxTPS,
			sched.GetMultiplierForHour,
			cursor,
			sc.Duration,
			simulateResolution,
			seed,
		)

		for i, p := range phasePts {
			p.Phase = sc.Name

			if len(sc.Inject) > 0 {
				// Inject curve drives TPS; Poisson and schedule overlays
				// are still applied on top.
				localOffset := p.Time.Sub(cursor)
				injectBase := controller.InjectTPSAt(sc.Inject, localOffset)

				// Apply schedule and Poisson multipliers from the already-
				// computed point (SimulateTimeline already set them).
				tps := injectBase * p.ScheduleMult * p.PoissonMult
				if phaseMaxTPS > 0 && tps > phaseMaxTPS {
					tps = phaseMaxTPS
				}
				if tps < 1 {
					tps = 1
				}
				p.TPS = tps
			}

			// Skip the last point of each phase (it equals the first
			// point of the next phase) except for the final phase.
			isLastPhase := sc.Name == cfg.Scenarios[len(cfg.Scenarios)-1].Name
			isLastPoint := i == len(phasePts)-1
			if isLastPoint && !isLastPhase && p.Time.Equal(phaseEnd) {
				continue
			}

			out = append(out, p)
		}

		cursor = phaseEnd
		// Vary seed per phase so Poisson events differ between phases.
		seed++
	}

	return out
}

func printSimulateText(cfg *config.Config, pts []pattern.SamplePoint) {
	fmt.Println()
	fmt.Println(tui.TitleStyle.Render(" SIMULATE / FORECAST "))
	fmt.Println()

	fmt.Printf("  base TPS:    %s\n", tui.ValueStyle.Render(fmt.Sprintf("%.0f", cfg.Controller.BaseTPS)))
	fmt.Printf("  max TPS:     %s\n", tui.ValueStyle.Render(fmt.Sprintf("%.0f", cfg.Controller.MaxTPS)))
	// When scenarios drive the timeline, prefer reporting the scenario
	// total over the --duration flag default — otherwise a 5-minute
	// scenarios run still claims a 24h window in the header.
	displayDuration := simulateDuration
	if total := scenariosTotalDuration(cfg.Scenarios); total > 0 {
		displayDuration = total
	}
	fmt.Printf("  duration:    %s\n", tui.ValueStyle.Render(displayDuration.String()))
	fmt.Printf("  resolution:  %s\n", tui.ValueStyle.Render(simulateResolution.String()))
	if len(cfg.Scenarios) > 0 {
		fmt.Printf("  phases:      %s\n", tui.DimStyle.Render(fmt.Sprintf("%d (driven by scenarios)", len(cfg.Scenarios))))
	}
	fmt.Printf("  spikes:      %s\n", tui.DimStyle.Render(spikeStatusLine(cfg.Pattern.Poisson)))
	fmt.Println()

	if len(pts) == 0 {
		fmt.Println(tui.DimStyle.Render("  (no sample points — duration too short?)"))
		return
	}

	var sum, peak float64
	var peakAt, minAt time.Time
	minTPS := math.MaxFloat64
	spikeCount := 0
	prevSpiking := false
	for _, p := range pts {
		sum += p.TPS
		if p.TPS > peak {
			peak = p.TPS
			peakAt = p.Time
		}
		if p.TPS < minTPS {
			minTPS = p.TPS
			minAt = p.Time
		}
		if p.Spiking && !prevSpiking {
			spikeCount++
		}
		prevSpiking = p.Spiking
	}
	avg := sum / float64(len(pts))
	estReq := sum * simulateResolution.Seconds()

	fmt.Println(tui.SubtitleStyle.Render("  Forecast"))
	fmt.Printf("    avg TPS:    %s\n", tui.ValueStyle.Render(fmt.Sprintf("%.1f", avg)))
	fmt.Printf("    peak:       %s at %s\n",
		tui.ValueStyle.Render(fmt.Sprintf("%.0f", peak)),
		tui.DimStyle.Render(peakAt.Format("2006-01-02 15:04")))
	fmt.Printf("    trough:     %s at %s\n",
		tui.ValueStyle.Render(fmt.Sprintf("%.0f", minTPS)),
		tui.DimStyle.Render(minAt.Format("2006-01-02 15:04")))
	fmt.Printf("    spikes:     %s events over window\n",
		tui.ValueStyle.Render(fmt.Sprintf("%d", spikeCount)))
	fmt.Printf("    est. total: %s requests\n",
		tui.ValueStyle.Render(humanCount(estReq)))
	fmt.Println()

	fmt.Println(tui.SubtitleStyle.Render("  Curve (low → high, time L→R)"))
	fmt.Println(simulateSparkline(pts, 60))
	fmt.Println()
}

// simulateSparkline buckets the timeline into `width` cells and picks
// a unicode block height per cell from the bin's mean TPS. The output
// is a single line meant to give a visual feel for the day shape, not
// a precise plot.
//
// When scenarios are present, a vertical bar │ is inserted at each
// phase boundary bin so the phase transitions are visible.
func simulateSparkline(pts []pattern.SamplePoint, width int) string {
	if len(pts) == 0 || width <= 0 {
		return ""
	}
	bins := make([]float64, width)
	counts := make([]int, width)
	// phaseBoundary[i] = true means bin i starts a new phase.
	phaseBoundary := make([]bool, width)
	prevPhase := ""
	for i, p := range pts {
		idx := i * width / len(pts)
		if idx >= width {
			idx = width - 1
		}
		bins[idx] += p.TPS
		counts[idx]++
		if p.Phase != "" && p.Phase != prevPhase && i > 0 {
			phaseBoundary[idx] = true
		}
		if p.Phase != "" {
			prevPhase = p.Phase
		}
	}
	var maxv float64
	for i := range bins {
		if counts[i] > 0 {
			bins[i] /= float64(counts[i])
		}
		if bins[i] > maxv {
			maxv = bins[i]
		}
	}
	glyphs := []rune(" ▁▂▃▄▅▆▇█")
	out := make([]rune, 0, width+2)
	out = append(out, ' ', ' ')
	for i, v := range bins {
		if phaseBoundary[i] {
			out = append(out, '│')
			continue
		}
		idx := 0
		if maxv > 0 {
			idx = int(v / maxv * float64(len(glyphs)-1))
		}
		if idx >= len(glyphs) {
			idx = len(glyphs) - 1
		}
		out = append(out, glyphs[idx])
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Render(string(out))
}

func humanCount(v float64) string {
	switch {
	case v >= 1e9:
		return fmt.Sprintf("%.1fB", v/1e9)
	case v >= 1e6:
		return fmt.Sprintf("%.1fM", v/1e6)
	case v >= 1e3:
		return fmt.Sprintf("%.1fK", v/1e3)
	default:
		return strconv.FormatFloat(v, 'f', 0, 64)
	}
}

func spikeStatusLine(p config.Poisson) string {
	if !p.Enabled {
		return "disabled"
	}
	return fmt.Sprintf("λ=%.4f, factor=%.1fx, ramp=%s/%s",
		p.Lambda, p.SpikeFactor, p.RampUp, p.RampDown)
}

func printSimulateCSV(pts []pattern.SamplePoint) error {
	w := csv.NewWriter(os.Stdout)
	defer w.Flush()
	if err := w.Write([]string{"time", "hour", "schedule_mult", "poisson_mult", "noise_mult", "tps", "spiking", "phase"}); err != nil {
		return err
	}
	for _, p := range pts {
		if err := w.Write([]string{
			p.Time.Format(time.RFC3339),
			strconv.Itoa(p.Hour),
			strconv.FormatFloat(p.ScheduleMult, 'f', 4, 64),
			strconv.FormatFloat(p.PoissonMult, 'f', 4, 64),
			strconv.FormatFloat(p.NoiseMult, 'f', 4, 64),
			strconv.FormatFloat(p.TPS, 'f', 1, 64),
			strconv.FormatBool(p.Spiking),
			p.Phase,
		}); err != nil {
			return err
		}
	}
	return nil
}

func printSimulateJSON(pts []pattern.SamplePoint) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(pts)
}
