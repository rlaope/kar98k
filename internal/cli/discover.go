package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/kar98k/internal/config"
	"github.com/kar98k/internal/discovery"
	"github.com/kar98k/internal/health"
	"github.com/kar98k/internal/tui"
	"github.com/spf13/cobra"
)

var (
	discoverURL          string
	discoverMethod       string
	discoverProtocol     string
	discoverLatencyLimit int64
	discoverErrorLimit   float64
	discoverMinTPS       float64
	discoverMaxTPS       float64
	discoverStepDuration time.Duration
	discoverHeadless     bool
)

var discoverCmd = &cobra.Command{
	Use:   "discover",
	Short: "Automatically discover system's maximum sustainable TPS",
	Long: `Discover the maximum TPS your system can handle while maintaining
acceptable latency and error rates.

Uses binary search to efficiently find the optimal TPS within the specified range.
The discovery process will:
  1. Start at minimum TPS and verify stability
  2. Use binary search to find the breaking point
  3. Report the maximum sustainable TPS with recommendations

Examples:
  kar discover --url http://localhost:8080/api/health
  kar discover --url https://api.example.com --latency-limit 200ms
  kar discover --url http://localhost:8080 --min-tps 100 --max-tps 5000`,
	RunE: runDiscover,
}

func init() {
	rootCmd.AddCommand(discoverCmd)

	discoverCmd.Flags().StringVar(&discoverURL, "url", "", "Target URL to test (required)")
	discoverCmd.Flags().StringVar(&discoverMethod, "method", "GET", "HTTP method")
	discoverCmd.Flags().StringVar(&discoverProtocol, "protocol", "http", "Protocol (http, http2, grpc)")
	discoverCmd.Flags().Int64Var(&discoverLatencyLimit, "latency-limit", 500, "P95 latency threshold in milliseconds")
	discoverCmd.Flags().Float64Var(&discoverErrorLimit, "error-limit", 5.0, "Error rate threshold in percentage")
	discoverCmd.Flags().Float64Var(&discoverMinTPS, "min-tps", 10, "Minimum TPS to start testing")
	discoverCmd.Flags().Float64Var(&discoverMaxTPS, "max-tps", 10000, "Maximum TPS to test")
	discoverCmd.Flags().DurationVar(&discoverStepDuration, "step-duration", 10*time.Second, "Duration for each TPS test step")
	discoverCmd.Flags().BoolVar(&discoverHeadless, "headless", false, "Run without TUI (print results to stdout)")
}

func runDiscover(cmd *cobra.Command, args []string) error {
	// If URL not provided via flag and not headless, use TUI
	if discoverURL == "" && !discoverHeadless {
		return runDiscoverTUI()
	}

	// Validate URL
	if discoverURL == "" {
		return fmt.Errorf("--url is required")
	}

	// Run headless discovery
	return runDiscoverHeadless()
}

func runDiscoverTUI() error {
	// Initialize logger
	if err := tui.InitLogger(); err != nil {
		return fmt.Errorf("failed to init logger: %w", err)
	}
	defer tui.CloseLogger()

	// Create runtime directory
	runtimeDir := filepath.Join(os.TempDir(), "kar98k")
	os.MkdirAll(runtimeDir, 0755)

	// Run the TUI
	m := tui.NewDiscoverModel()
	p := tea.NewProgram(m, tea.WithAltScreen())

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		p.Send(tui.DiscoverStopMsg{})
	}()

	finalModel, err := p.Run()
	if err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}

	// Get the configuration from the TUI
	model := finalModel.(tui.DiscoverModel)
	tuiConfig := model.GetConfig()

	// Check if user completed configuration
	if tuiConfig["target_url"] == "" {
		fmt.Println("\n👋 Discovery cancelled. Goodbye!")
		return nil
	}

	// Build discovery config
	cfg := buildDiscoveryConfigFromTUI(tuiConfig)

	// Run discovery with the config
	return executeDiscovery(cfg, false)
}

func runDiscoverHeadless() error {
	cfg := config.Discovery{
		TargetURL:       discoverURL,
		Method:          discoverMethod,
		Protocol:        config.Protocol(discoverProtocol),
		LatencyLimitMs:  discoverLatencyLimit,
		ErrorRateLimit:  discoverErrorLimit,
		MinTPS:          discoverMinTPS,
		MaxTPS:          discoverMaxTPS,
		StepDuration:    discoverStepDuration,
		ConvergenceRate: 0.05,
	}

	return executeDiscovery(cfg, true)
}

func executeDiscovery(cfg config.Discovery, headless bool) error {
	if !headless {
		fmt.Println("\n🔍 Starting Adaptive Load Discovery...")
		fmt.Printf("   Target: %s %s\n", cfg.Method, cfg.TargetURL)
		fmt.Printf("   Limits: P95 < %dms, Error < %.1f%%\n", cfg.LatencyLimitMs, cfg.ErrorRateLimit)
		fmt.Printf("   Range:  %.0f - %.0f TPS\n\n", cfg.MinTPS, cfg.MaxTPS)
	}

	// Create metrics
	metrics := health.NewMetrics()

	// Create context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create and run discovery controller
	controller := discovery.NewController(cfg, metrics)

	// Set up progress callback for headless mode
	if headless {
		controller.SetProgressCallback(func(progress float64, currentTPS float64, p95 float64, errRate float64, status string) {
			fmt.Printf("\r[%.0f%%] TPS: %.0f | P95: %.0fms | Errors: %.1f%% | %s",
				progress, currentTPS, p95, errRate, status)
		})
	}

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		fmt.Println("\n\n⚠️  Discovery interrupted")
		controller.Stop()
		cancel()
	}()

	// Run discovery
	if err := controller.Start(ctx); err != nil {
		return fmt.Errorf("failed to start discovery: %w", err)
	}

	// Wait for completion
	for controller.GetState() == discovery.StateRunning {
		time.Sleep(100 * time.Millisecond)
	}

	// Get result
	result := controller.GetResult()
	if result == nil {
		return fmt.Errorf("discovery did not complete successfully")
	}

	// Print results
	printDiscoveryResult(result)

	return nil
}

func buildDiscoveryConfigFromTUI(tuiConfig map[string]string) config.Discovery {
	latencyLimit, _ := strconv.ParseInt(tuiConfig["latency_limit"], 10, 64)
	errorLimit, _ := strconv.ParseFloat(tuiConfig["error_limit"], 64)
	minTPS, _ := strconv.ParseFloat(tuiConfig["min_tps"], 64)
	maxTPS, _ := strconv.ParseFloat(tuiConfig["max_tps"], 64)

	if latencyLimit == 0 {
		latencyLimit = 500
	}
	if errorLimit == 0 {
		errorLimit = 5.0
	}
	if minTPS == 0 {
		minTPS = 10
	}
	if maxTPS == 0 {
		maxTPS = 10000
	}

	return config.Discovery{
		TargetURL:       tuiConfig["target_url"],
		Method:          tuiConfig["method"],
		Protocol:        config.Protocol(tuiConfig["protocol"]),
		LatencyLimitMs:  latencyLimit,
		ErrorRateLimit:  errorLimit,
		MinTPS:          minTPS,
		MaxTPS:          maxTPS,
		StepDuration:    10 * time.Second,
		ConvergenceRate: 0.05,
	}
}

func printDiscoveryResult(r *discovery.Result) {
	fmt.Println()
	fmt.Println()
	fmt.Println(strings.Repeat("─", 60))
	fmt.Println()
	fmt.Println(tui.SuccessStyle.Render("  ✓ DISCOVERY COMPLETE"))
	fmt.Println()
	fmt.Println(strings.Repeat("─", 60))
	fmt.Println()
	fmt.Println("  Your system can handle:")
	fmt.Println()
	fmt.Printf("    %s  %s\n",
		tui.LabelStyle.Render("Sustained TPS:"),
		tui.HighlightStyle.Render(fmt.Sprintf("%.0f", r.SustainedTPS)))
	fmt.Printf("    %s  %s\n",
		tui.LabelStyle.Render("Breaking Point:"),
		tui.WarningStyle.Render(fmt.Sprintf("%.0f TPS", r.BreakingTPS)))
	fmt.Println()
	fmt.Println("  At sustained load:")
	fmt.Println()
	fmt.Printf("    %s  %.0fms\n", tui.LabelStyle.Render("P95 Latency:"), r.P95Latency)
	fmt.Printf("    %s  %.1f%%\n", tui.LabelStyle.Render("Error Rate:"), r.ErrorRate)
	fmt.Println()
	fmt.Println(strings.Repeat("─", 60))
	fmt.Println()
	fmt.Println("  Recommendation:")
	fmt.Println()
	fmt.Printf("    Set %s to %s (80%% of sustained)\n",
		tui.LabelStyle.Render("BaseTPS"),
		tui.SuccessStyle.Render(fmt.Sprintf("%.0f", r.Recommendation.BaseTPS)))
	fmt.Printf("    Set %s to %s (safe spike limit)\n",
		tui.LabelStyle.Render("MaxTPS"),
		tui.SuccessStyle.Render(fmt.Sprintf("%.0f", r.Recommendation.MaxTPS)))
	fmt.Println()
	fmt.Println(strings.Repeat("─", 60))
	fmt.Println()
	fmt.Printf("  Test completed in %s (%d steps)\n",
		r.TestDuration.Round(time.Second), r.StepsCompleted)
	fmt.Println()
}
