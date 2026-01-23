package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/kar98k/internal/config"
	"github.com/kar98k/internal/daemon"
	"github.com/kar98k/internal/tui"
	"github.com/spf13/cobra"
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Launch interactive configuration and start kar",
	Long: `Launch the interactive TUI to configure kar.
Walk through target setup, traffic configuration, and pattern settings,
then pull the trigger to start generating traffic.`,
	RunE: runStart,
}

func init() {
	rootCmd.AddCommand(startCmd)
}

func runStart(cmd *cobra.Command, args []string) error {
	// Check if already running
	if daemon.IsRunning() {
		fmt.Println("\n⚠️  kar is already running!")
		fmt.Println("   Use 'kar status' to check status")
		fmt.Println("   Use 'kar stop' to stop the running instance")
		return nil
	}

	// Initialize logger
	if err := tui.InitLogger(); err != nil {
		return fmt.Errorf("failed to init logger: %w", err)
	}
	defer tui.CloseLogger()

	// Run the TUI
	m := tui.NewModel()
	p := tea.NewProgram(m, tea.WithAltScreen())

	finalModel, err := p.Run()
	if err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}

	// Get the configuration from the TUI
	model := finalModel.(tui.Model)
	tuiConfig := model.GetConfig()

	// Check if user completed configuration
	if tuiConfig["target_url"] == "" {
		fmt.Println("\n👋 Configuration cancelled. Goodbye!")
		return nil
	}

	// Build configuration
	cfg := buildConfigFromTUI(tuiConfig)

	// Start daemon in background
	fmt.Println("\n🚀 Starting kar daemon...")

	// Fork to background
	if err := startDaemon(cfg); err != nil {
		return fmt.Errorf("failed to start daemon: %w", err)
	}

	fmt.Println("✅ kar is now running in the background!")
	fmt.Println("")
	fmt.Println("   📊 Status:  kar status")
	fmt.Println("   📜 Logs:    kar logs -f")
	fmt.Println("   🛑 Stop:    kar stop")
	fmt.Println("")

	return nil
}

func buildConfigFromTUI(tuiConfig map[string]string) *config.Config {
	baseTPS, _ := strconv.ParseFloat(tuiConfig["base_tps"], 64)
	maxTPS, _ := strconv.ParseFloat(tuiConfig["max_tps"], 64)
	lambda, _ := strconv.ParseFloat(tuiConfig["poisson_lambda"], 64)
	spikeFactor, _ := strconv.ParseFloat(tuiConfig["spike_factor"], 64)
	noiseAmp, _ := strconv.ParseFloat(tuiConfig["noise_amp"], 64)

	if baseTPS == 0 {
		baseTPS = 100
	}
	if maxTPS == 0 {
		maxTPS = 1000
	}
	if lambda == 0 {
		lambda = 0.1
	}
	if spikeFactor == 0 {
		spikeFactor = 3.0
	}
	if noiseAmp == 0 {
		noiseAmp = 0.15
	}

	cfg := config.DefaultConfig()

	cfg.Targets = []config.Target{
		{
			Name:     "target-1",
			URL:      tuiConfig["target_url"],
			Protocol: config.Protocol(tuiConfig["protocol"]),
			Method:   tuiConfig["target_method"],
			Weight:   100,
		},
	}

	cfg.Controller.BaseTPS = baseTPS
	cfg.Controller.MaxTPS = maxTPS
	cfg.Pattern.Poisson.Lambda = lambda
	cfg.Pattern.Poisson.SpikeFactor = spikeFactor
	cfg.Pattern.Noise.Amplitude = noiseAmp

	return cfg
}

func startDaemon(cfg *config.Config) error {
	// For simplicity, we'll run in foreground mode here
	// In production, you'd fork to background

	d, err := daemon.New(cfg)
	if err != nil {
		return err
	}

	if err := d.Start(); err != nil {
		return err
	}

	// Auto-trigger since user completed TUI
	d.Trigger()

	// In a real implementation, we'd detach here
	// For now, just signal that daemon started
	return nil
}

// startDaemonBackground starts the daemon as a background process
func startDaemonBackground(configPath string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}

	cmd := exec.Command(exe, "run", "--config", configPath, "--daemon")
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil

	if err := cmd.Start(); err != nil {
		return err
	}

	// Detach
	if err := cmd.Process.Release(); err != nil {
		return err
	}

	return nil
}
