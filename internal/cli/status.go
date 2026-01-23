package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/kar98k/internal/daemon"
	"github.com/kar98k/internal/tui"
	"github.com/spf13/cobra"
)

var (
	statusJSON  bool
	statusWatch bool
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show kar status",
	Long: `Display the current status of the kar daemon.

Examples:
  kar status          Show current status
  kar status -w       Watch status (refresh every second)
  kar status --json   Output as JSON`,
	RunE: runStatus,
}

func init() {
	statusCmd.Flags().BoolVar(&statusJSON, "json", false, "Output as JSON")
	statusCmd.Flags().BoolVarP(&statusWatch, "watch", "w", false, "Watch mode (refresh every second)")
	rootCmd.AddCommand(statusCmd)
}

func runStatus(cmd *cobra.Command, args []string) error {
	if statusWatch {
		return watchStatus()
	}

	return showStatus()
}

func showStatus() error {
	resp, err := daemon.SendCommand(daemon.Command{Type: "status"})
	if err != nil {
		fmt.Println()
		fmt.Println(tui.ErrorStyle.Render("  ✗ kar is not running"))
		fmt.Println()
		fmt.Println(tui.DimStyle.Render("  Start with: kar start"))
		fmt.Println()
		return nil
	}

	if statusJSON {
		output, _ := json.MarshalIndent(resp.Data, "", "  ")
		fmt.Println(string(output))
		return nil
	}

	// Parse status
	statusData, _ := json.Marshal(resp.Data)
	var status daemon.Status
	json.Unmarshal(statusData, &status)

	printStatus(status)
	return nil
}

func watchStatus() error {
	// Clear screen
	fmt.Print("\033[H\033[2J")

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		// Move cursor to top
		fmt.Print("\033[H")

		resp, err := daemon.SendCommand(daemon.Command{Type: "status"})
		if err != nil {
			fmt.Println(tui.ErrorStyle.Render("Connection lost. Daemon may have stopped."))
			return nil
		}

		statusData, _ := json.Marshal(resp.Data)
		var status daemon.Status
		json.Unmarshal(statusData, &status)

		printStatus(status)
		fmt.Println()
		fmt.Println(tui.DimStyle.Render("Press Ctrl+C to exit watch mode"))

		<-ticker.C
	}
}

func printStatus(status daemon.Status) {
	fmt.Println()

	// Header
	header := lipgloss.JoinHorizontal(lipgloss.Center,
		tui.MiniLogo(),
		"  ",
		tui.TitleStyle.Render(" STATUS "),
	)
	fmt.Println(header)
	fmt.Println()

	// Status indicator
	var statusIcon, statusText string
	if status.Triggered {
		statusIcon = tui.SuccessStyle.Render(tui.TriggerPulled)
		statusText = tui.SuccessStyle.Render("FIRING")
	} else if status.Running {
		statusIcon = tui.WarningStyle.Render(tui.TriggerReady)
		statusText = tui.WarningStyle.Render("ARMED (waiting for trigger)")
	} else {
		statusIcon = tui.ErrorStyle.Render(tui.CrossMark)
		statusText = tui.ErrorStyle.Render("STOPPED")
	}

	fmt.Printf("  %s %s\n", statusIcon, statusText)
	fmt.Println()

	// Stats box
	var content strings.Builder

	// TPS
	content.WriteString(tui.SubtitleStyle.Render("Traffic"))
	content.WriteString("\n")
	tpsBar := tui.ProgressBar(status.CurrentTPS/status.TargetTPS, 30)
	content.WriteString(fmt.Sprintf("  TPS: %s / %.0f  %s\n",
		tui.ValueStyle.Render(fmt.Sprintf("%.0f", status.CurrentTPS)),
		status.TargetTPS,
		tpsBar,
	))

	if status.IsSpiking {
		content.WriteString(tui.WarningStyle.Render("  ⚡ SPIKE ACTIVE\n"))
	}
	content.WriteString("\n")

	// Metrics
	content.WriteString(tui.SubtitleStyle.Render("Metrics"))
	content.WriteString("\n")
	content.WriteString(fmt.Sprintf("  Requests:  %s\n", tui.ValueStyle.Render(fmt.Sprintf("%d", status.RequestsSent))))
	content.WriteString(fmt.Sprintf("  Errors:    %s\n", tui.ErrorStyle.Render(fmt.Sprintf("%d", status.ErrorCount))))
	content.WriteString(fmt.Sprintf("  Latency:   %s\n", tui.ValueStyle.Render(fmt.Sprintf("%.1fms", status.AvgLatency))))
	content.WriteString("\n")

	// Target
	content.WriteString(tui.SubtitleStyle.Render("Target"))
	content.WriteString("\n")
	content.WriteString(fmt.Sprintf("  %s %s\n", tui.LabelStyle.Render(status.Protocol), tui.DimStyle.Render(status.TargetURL)))
	content.WriteString("\n")

	// Uptime
	content.WriteString(tui.SubtitleStyle.Render("Uptime"))
	content.WriteString("\n")
	content.WriteString(fmt.Sprintf("  %s\n", tui.ValueStyle.Render(status.Uptime)))

	box := tui.BorderStyle.Width(50).Render(content.String())
	fmt.Println(box)
}

// Trigger command
var triggerCmd = &cobra.Command{
	Use:   "trigger",
	Short: "Pull the trigger to start traffic generation",
	Long:  `Send the trigger signal to start generating traffic.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := daemon.SendCommand(daemon.Command{Type: "trigger"})
		if err != nil {
			return fmt.Errorf("daemon not running: %w", err)
		}

		if resp.Success {
			fmt.Println()
			fmt.Println(tui.SuccessStyle.Render("  " + tui.TriggerPulled + " Trigger pulled! Traffic flowing..."))
			fmt.Println()
		} else {
			fmt.Println(tui.ErrorStyle.Render("  " + resp.Message))
		}

		return nil
	},
}

// Pause command
var pauseCmd = &cobra.Command{
	Use:   "pause",
	Short: "Pause traffic generation",
	Long:  `Pause traffic generation without stopping the daemon.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := daemon.SendCommand(daemon.Command{Type: "pause"})
		if err != nil {
			return fmt.Errorf("daemon not running: %w", err)
		}

		if resp.Success {
			fmt.Println()
			fmt.Println(tui.WarningStyle.Render("  " + tui.TriggerReady + " Traffic paused"))
			fmt.Println()
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(triggerCmd)
	rootCmd.AddCommand(pauseCmd)
}
