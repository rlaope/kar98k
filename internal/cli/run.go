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
	configPath   string
	daemonMode   bool
	autoTrigger  bool
)

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run kar98k with a config file (headless mode)",
	Long: `Run kar98k in headless mode using a YAML configuration file.
This is useful for server deployments or CI/CD pipelines.

Example:
  kar98k run --config kar98k.yaml
  kar98k run --config kar98k.yaml --trigger`,
	RunE: runRun,
}

func init() {
	runCmd.Flags().StringVarP(&configPath, "config", "c", "kar98k.yaml", "Path to configuration file")
	runCmd.Flags().BoolVarP(&daemonMode, "daemon", "d", false, "Run as background daemon")
	runCmd.Flags().BoolVarP(&autoTrigger, "trigger", "t", false, "Auto-trigger on start")
	rootCmd.AddCommand(runCmd)
}

func runRun(cmd *cobra.Command, args []string) error {
	// Check if already running
	if daemon.IsRunning() && !daemonMode {
		fmt.Println("\n⚠️  kar98k is already running!")
		return nil
	}

	// Load config
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	fmt.Printf("⌖ kar98k starting (config: %s)\n", configPath)
	fmt.Printf("  Targets: %d\n", len(cfg.Targets))
	fmt.Printf("  Base TPS: %.0f\n", cfg.Controller.BaseTPS)
	fmt.Printf("  Max TPS: %.0f\n", cfg.Controller.MaxTPS)
	fmt.Println()

	// Create daemon
	d, err := daemon.New(cfg)
	if err != nil {
		return fmt.Errorf("failed to create daemon: %w", err)
	}

	// Start daemon
	if err := d.Start(); err != nil {
		return fmt.Errorf("failed to start: %w", err)
	}

	// Auto-trigger if requested
	if autoTrigger {
		fmt.Println("🔫 Auto-triggering...")
		d.Trigger()
	} else {
		fmt.Println("⏸  Waiting for trigger...")
		fmt.Println("   Use 'kar98k trigger' to start traffic")
	}

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	<-sigCh
	fmt.Println("\n🛑 Shutting down...")
	d.Stop()

	return nil
}
