package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/kar98k/internal/dashboard"
	"github.com/kar98k/internal/script"
	"github.com/kar98k/internal/tui"
	"github.com/spf13/cobra"
)

var (
	scriptVUs        int
	scriptDuration   string
	scriptPreset     string
	scriptDashboard  bool
	scriptDashPort   string
)

var scriptCmd = &cobra.Command{
	Use:   "script <file>",
	Short: "Run a load test script (polyglot: .star, .js, .py, .rb, ...)",
	Long: `Run a code-based load test with kar98k's chaos traffic patterns.

Supported languages:
  .star     Starlark (Python-like syntax, built-in)
  .js       JavaScript (ES5+, built-in)
  .py       Python (requires python3)
  .rb       Ruby (requires ruby)
  .lua      Lua (requires lua)

Examples:
  kar script test.star
  kar script test.js --vus 50 --duration 2m
  kar script test.py --preset aggressive`,
	Args: cobra.ExactArgs(1),
	RunE: runScript,
}

func init() {
	scriptCmd.Flags().IntVar(&scriptVUs, "vus", 0, "Override number of virtual users")
	scriptCmd.Flags().StringVar(&scriptDuration, "duration", "", "Override test duration (e.g., 30s, 5m)")
	scriptCmd.Flags().StringVar(&scriptPreset, "preset", "", "Override chaos preset (gentle, moderate, aggressive)")
	scriptCmd.Flags().BoolVar(&scriptDashboard, "dashboard", false, "Enable real-time web dashboard")
	scriptCmd.Flags().StringVar(&scriptDashPort, "dash-port", ":8888", "Dashboard listen address")
	rootCmd.AddCommand(scriptCmd)
}

func runScript(cmd *cobra.Command, args []string) error {
	scriptPath := args[0]

	// Check file exists
	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		return fmt.Errorf("script not found: %s", scriptPath)
	}

	// Detect language and create runner
	lang := script.DetectLanguage(scriptPath)
	ext := strings.ToLower(filepath.Ext(scriptPath))

	fmt.Println()
	fmt.Println(tui.LogoWithWidth(80))
	fmt.Println()
	fmt.Printf("  ⌖ Script: %s (%s)\n", scriptPath, langName(lang, ext))

	runner, err := script.NewRunner(scriptPath)
	if err != nil {
		return fmt.Errorf("creating runner: %w", err)
	}
	defer runner.Close()

	// Load script
	fmt.Println("  Loading script...")
	if err := runner.Load(scriptPath); err != nil {
		return fmt.Errorf("loading script: %w", err)
	}

	// Apply CLI overrides
	sc := runner.Scenario()
	if scriptPreset != "" {
		// Override will be applied through scenario config
		fmt.Printf("  Preset:   %s (override)\n", scriptPreset)
	}

	if sc.Name != "" {
		fmt.Printf("  Scenario: %s\n", sc.Name)
	}
	fmt.Printf("  Chaos:    %s (spike: %.1fx, noise: ±%.0f%%)\n",
		sc.Chaos.Preset, sc.Chaos.SpikeFactor, sc.Chaos.NoiseAmplitude*100)

	// Parse duration override
	var durationOverride time.Duration
	if scriptDuration != "" {
		d, err := time.ParseDuration(scriptDuration)
		if err != nil {
			return fmt.Errorf("invalid duration %q: %w", scriptDuration, err)
		}
		durationOverride = d
	}

	// Create VU scheduler
	scheduler := script.NewVUScheduler(runner, scriptVUs, durationOverride)

	// Start dashboard if enabled
	if scriptDashboard {
		dash := dashboard.New(scriptDashPort)
		dash.SetScenario(sc.Name, sc.Chaos.Preset)
		if err := dash.Start(); err != nil {
			return fmt.Errorf("starting dashboard: %w", err)
		}
		scheduler.SetDashboard(&dashAdapter{dash: dash})
	}

	// Handle signals
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\n\n  Stopping...")
		cancel()
	}()

	// Run
	startTime := time.Now()
	if err := scheduler.Run(ctx); err != nil {
		fmt.Printf("\n  Error: %v\n", err)
	}
	elapsed := time.Since(startTime)

	// Print report
	script.PrintReport(runner, elapsed)

	return nil
}

func langName(lang script.Language, ext string) string {
	switch lang {
	case script.LangStarlark:
		return "Starlark"
	case script.LangJS:
		return "JavaScript"
	case script.LangExternal:
		switch ext {
		case ".py":
			return "Python"
		case ".rb":
			return "Ruby"
		case ".lua":
			return "Lua"
		default:
			return "External"
		}
	}
	return "Unknown"
}

// dashAdapter bridges dashboard.Server to script.DashboardPusher.
type dashAdapter struct {
	dash *dashboard.Server
}

func (a *dashAdapter) Push(stats interface{}) {
	data, err := json.Marshal(stats)
	if err != nil {
		return
	}
	var s dashboard.Stats
	if json.Unmarshal(data, &s) == nil {
		a.dash.Push(s)
	}
}
