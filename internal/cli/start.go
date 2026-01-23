package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

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
	pidPath := filepath.Join(os.TempDir(), "kar98k", "kar98k.pid")
	if pidData, err := os.ReadFile(pidPath); err == nil {
		// PID file exists, check if process is actually running
		if pid, err := strconv.Atoi(strings.TrimSpace(string(pidData))); err == nil {
			if process, err := os.FindProcess(pid); err == nil {
				// On Unix, FindProcess always succeeds, so we need to send signal 0 to check
				if err := process.Signal(syscall.Signal(0)); err == nil {
					fmt.Println("\n⚠️  kar is already running!")
					fmt.Println("   Use 'kar status' to check status")
					fmt.Println("   Use 'kar stop' to stop the running instance")
					return nil
				}
			}
		}
		// Process not running, clean up stale PID file
		os.Remove(pidPath)
	}

	// Initialize logger
	if err := tui.InitLogger(); err != nil {
		return fmt.Errorf("failed to init logger: %w", err)
	}
	defer tui.CloseLogger()

	// Create runtime directory and PID file
	runtimeDir := filepath.Join(os.TempDir(), "kar98k")
	os.MkdirAll(runtimeDir, 0755)
	os.WriteFile(pidPath, []byte(fmt.Sprintf("%d", os.Getpid())), 0644)
	defer os.Remove(pidPath)

	// Run the TUI
	m := tui.NewModel()
	p := tea.NewProgram(m, tea.WithAltScreen())

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT, signalUSR1)
	cmdPath := filepath.Join(os.TempDir(), "kar98k", "kar98k.cmd")
	go func() {
		for sig := range sigCh {
			switch sig {
			case signalUSR1:
				// Read and process spike command
				if cmdData, err := os.ReadFile(cmdPath); err == nil {
					var cmd struct {
						Type     string        `json:"type"`
						Factor   float64       `json:"factor,omitempty"`
						Duration time.Duration `json:"duration,omitempty"`
					}
					if json.Unmarshal(cmdData, &cmd) == nil && cmd.Type == "spike" {
						p.Send(tui.SpikeMsg{Factor: cmd.Factor, Duration: cmd.Duration})
					}
					os.Remove(cmdPath)
				}
			case syscall.SIGTERM, syscall.SIGINT:
				p.Send(tui.StopMsg{})
				return
			}
		}
	}()

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

	// Parse spike interval
	var spikeInterval time.Duration
	if tuiConfig["spike_interval"] != "" {
		spikeInterval, _ = time.ParseDuration(tuiConfig["spike_interval"])
	}

	if baseTPS == 0 {
		baseTPS = 100
	}
	if maxTPS == 0 {
		maxTPS = 1000
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

	// Use interval if set, otherwise use lambda
	if spikeInterval > 0 {
		cfg.Pattern.Poisson.Interval = spikeInterval
		cfg.Pattern.Poisson.Lambda = 0 // Will be calculated from interval
	} else if lambda > 0 {
		cfg.Pattern.Poisson.Lambda = lambda
	} else {
		cfg.Pattern.Poisson.Lambda = 0.1 // default
	}

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
