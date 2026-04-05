package cli

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/kar98k/internal/config"
	"github.com/kar98k/internal/daemon"
	"github.com/spf13/cobra"
)

var (
	qsTPS      float64
	qsPreset   string
)

var quickstartCmd = &cobra.Command{
	Use:   "quickstart <url>",
	Short: "Start load testing with one command",
	Long: `Start generating traffic with sensible defaults. Just provide a URL.

Presets control spike intensity:
  gentle     - Low noise, rare spikes (good for first-time testing)
  moderate   - Balanced traffic patterns (default)
  aggressive - Frequent spikes, high variance (stress testing)

Examples:
  kar quickstart http://localhost:8080/health
  kar quickstart http://localhost:8080/api/users --tps 200
  kar quickstart http://localhost:8080/api --preset aggressive`,
	Args: cobra.ExactArgs(1),
	RunE: runQuickstart,
}

func init() {
	quickstartCmd.Flags().Float64Var(&qsTPS, "tps", 50, "Base TPS (transactions per second)")
	quickstartCmd.Flags().StringVar(&qsPreset, "preset", "moderate", "Traffic preset: gentle, moderate, aggressive")
	rootCmd.AddCommand(quickstartCmd)
}

type preset struct {
	SpikeFactor float64
	Lambda      float64
	MinInterval string
	MaxInterval string
	Noise       float64
	MaxTPS      float64 // multiplier of base TPS
}

var presets = map[string]preset{
	"gentle": {
		SpikeFactor: 1.5,
		Lambda:      0.003, // ~1 spike per 5 minutes
		MinInterval: "3m",
		MaxInterval: "10m",
		Noise:       0.05,
		MaxTPS:      3,
	},
	"moderate": {
		SpikeFactor: 2.0,
		Lambda:      0.005, // ~1 spike per 3 minutes
		MinInterval: "2m",
		MaxInterval: "8m",
		Noise:       0.10,
		MaxTPS:      5,
	},
	"aggressive": {
		SpikeFactor: 3.0,
		Lambda:      0.01, // ~1 spike per 2 minutes
		MinInterval: "1m",
		MaxInterval: "5m",
		Noise:       0.15,
		MaxTPS:      10,
	},
}

func runQuickstart(cmd *cobra.Command, args []string) error {
	targetURL := args[0]

	p, ok := presets[qsPreset]
	if !ok {
		return fmt.Errorf("unknown preset %q (use: gentle, moderate, aggressive)", qsPreset)
	}

	if daemon.IsRunning() {
		fmt.Println("\n⚠️  kar is already running!")
		fmt.Println("   Use 'kar stop' first.")
		return nil
	}

	cfg := config.DefaultConfig()
	cfg.Targets = []config.Target{
		{
			Name:     "quickstart",
			URL:      targetURL,
			Protocol: config.ProtocolHTTP,
			Method:   "GET",
			Weight:   100,
		},
	}

	cfg.Controller.BaseTPS = qsTPS
	cfg.Controller.MaxTPS = qsTPS * p.MaxTPS
	cfg.Pattern.Poisson.Lambda = p.Lambda
	cfg.Pattern.Poisson.SpikeFactor = p.SpikeFactor
	cfg.Pattern.Noise.Amplitude = p.Noise

	fmt.Println()
	fmt.Printf("⌖ kar quickstart\n")
	fmt.Printf("  Target:  %s\n", targetURL)
	fmt.Printf("  TPS:     %.0f (max: %.0f)\n", qsTPS, qsTPS*p.MaxTPS)
	fmt.Printf("  Preset:  %s\n", qsPreset)
	fmt.Printf("  Spikes:  %.1fx every ~%s\n", p.SpikeFactor, humanInterval(p.Lambda))
	fmt.Printf("  Noise:   ±%.0f%%\n", p.Noise*100)
	fmt.Println()

	d, err := daemon.New(cfg)
	if err != nil {
		return fmt.Errorf("failed to create daemon: %w", err)
	}

	if err := d.Start(); err != nil {
		return fmt.Errorf("failed to start: %w", err)
	}

	fmt.Println("🔫 Firing!")
	d.Trigger()

	fmt.Println()
	fmt.Println("   📊 Status:  kar status -w")
	fmt.Println("   📜 Logs:    kar logs -f")
	fmt.Println("   🛑 Stop:    kar stop")
	fmt.Println()
	fmt.Println("Press Ctrl+C to stop.")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	fmt.Println("\n🛑 Shutting down...")
	d.Stop()

	return nil
}

func humanInterval(lambda float64) string {
	if lambda <= 0 {
		return "never"
	}
	seconds := 1.0 / lambda
	if seconds >= 3600 {
		return fmt.Sprintf("%.0fh", seconds/3600)
	}
	if seconds >= 60 {
		return fmt.Sprintf("%.0fm", seconds/60)
	}
	return fmt.Sprintf("%.0fs", seconds)
}
