package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kar98k/internal/tui"
	"github.com/spf13/cobra"
)

var (
	spikeFactor   float64
	spikeDuration string
)

var spikeCmd = &cobra.Command{
	Use:   "spike",
	Short: "Trigger a manual spike on running kar instance",
	Long: `Trigger a manual spike on the running kar instance.

Examples:
  kar spike                         # Use default spike factor
  kar spike --factor 5.0            # 5x TPS multiplier
  kar spike --duration 1m           # Spike for 1 minute
  kar spike --factor 3.0 --duration 30s`,
	RunE: runSpike,
}

func init() {
	spikeCmd.Flags().Float64VarP(&spikeFactor, "factor", "f", 0, "TPS multiplier (default: uses configured spike_factor)")
	spikeCmd.Flags().StringVarP(&spikeDuration, "duration", "d", "", "Spike duration (e.g., 30s, 1m, 5m)")
	rootCmd.AddCommand(spikeCmd)
}

// SpikeCommand represents a spike command to be sent to the running kar instance
type SpikeCommand struct {
	Type     string        `json:"type"`
	Factor   float64       `json:"factor,omitempty"`
	Duration time.Duration `json:"duration,omitempty"`
}

func runSpike(cmd *cobra.Command, args []string) error {
	pidPath := filepath.Join(os.TempDir(), "kar98k", "kar98k.pid")
	cmdPath := filepath.Join(os.TempDir(), "kar98k", "kar98k.cmd")

	// Check if kar is running
	pidData, err := os.ReadFile(pidPath)
	if err != nil {
		fmt.Println()
		fmt.Println(tui.WarningStyle.Render("  kar is not running"))
		fmt.Println(tui.DimStyle.Render("  Start kar first with: kar start"))
		fmt.Println()
		return nil
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(pidData)))
	if err != nil {
		fmt.Println()
		fmt.Println(tui.ErrorStyle.Render("  Invalid PID file"))
		fmt.Println()
		return nil
	}

	// Check if process exists
	process, err := os.FindProcess(pid)
	if err != nil {
		fmt.Println()
		fmt.Println(tui.WarningStyle.Render("  kar is not running"))
		fmt.Println()
		os.Remove(pidPath)
		return nil
	}

	// Verify process is actually running
	if err := process.Signal(syscall.Signal(0)); err != nil {
		fmt.Println()
		fmt.Println(tui.WarningStyle.Render("  kar is not running"))
		fmt.Println()
		os.Remove(pidPath)
		return nil
	}

	// Parse duration
	var duration time.Duration
	if spikeDuration != "" {
		duration, err = time.ParseDuration(spikeDuration)
		if err != nil {
			fmt.Println()
			fmt.Println(tui.ErrorStyle.Render("  Invalid duration format: " + spikeDuration))
			fmt.Println(tui.DimStyle.Render("  Use formats like: 30s, 1m, 5m, 1h"))
			fmt.Println()
			return nil
		}
	}

	// Create spike command
	spikeCmd := SpikeCommand{
		Type:     "spike",
		Factor:   spikeFactor,
		Duration: duration,
	}

	// Write command to file
	cmdData, _ := json.Marshal(spikeCmd)
	if err := os.WriteFile(cmdPath, cmdData, 0644); err != nil {
		fmt.Println()
		fmt.Println(tui.ErrorStyle.Render("  Failed to send spike command"))
		fmt.Println()
		return nil
	}

	// Send SIGUSR1 to notify kar about new command (Unix only)
	if err := process.Signal(signalUSR1); err != nil {
		fmt.Println()
		fmt.Println(tui.ErrorStyle.Render("  Failed to signal kar process"))
		fmt.Println()
		return nil
	}

	fmt.Println()
	fmt.Println(tui.SuccessStyle.Render("  " + tui.CheckMark + " Manual spike triggered!"))
	if spikeFactor > 0 {
		fmt.Println(tui.InfoStyle.Render(fmt.Sprintf("    Factor: %.1fx", spikeFactor)))
	} else {
		fmt.Println(tui.DimStyle.Render("    Factor: using configured spike_factor"))
	}
	if duration > 0 {
		fmt.Println(tui.InfoStyle.Render(fmt.Sprintf("    Duration: %s", duration)))
	} else {
		fmt.Println(tui.DimStyle.Render("    Duration: using default"))
	}
	fmt.Println()

	return nil
}
