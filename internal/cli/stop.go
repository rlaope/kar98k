package cli

import (
	"bufio"
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

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the kar daemon",
	Long: `Stop the running kar daemon gracefully.
This will drain in-flight requests before shutting down.`,
	RunE: runStop,
}

func init() {
	rootCmd.AddCommand(stopCmd)
}

func runStop(cmd *cobra.Command, args []string) error {
	pidPath := filepath.Join(os.TempDir(), "kar98k", "kar98k.pid")
	logPath := filepath.Join(os.TempDir(), "kar98k", "kar98k.log")

	// Read PID file
	pidData, err := os.ReadFile(pidPath)
	if err != nil {
		fmt.Println()
		fmt.Println(tui.WarningStyle.Render("  kar is not running"))
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

	fmt.Println()
	fmt.Println(tui.InfoStyle.Render("  Stopping kar (PID: " + strconv.Itoa(pid) + ")..."))

	// Send SIGTERM
	if err := process.Signal(syscall.SIGTERM); err != nil {
		// Process already finished, clean up PID file
		os.Remove(pidPath)
		fmt.Println(tui.SuccessStyle.Render("  " + tui.CheckMark + " kar stopped (was already finished)"))
		fmt.Println()
		showLastSummary(logPath)
		return nil
	}

	// Wait for process to exit (max 5 seconds)
	for i := 0; i < 50; i++ {
		time.Sleep(100 * time.Millisecond)
		if _, err := os.Stat(pidPath); os.IsNotExist(err) {
			break
		}
	}

	fmt.Println(tui.SuccessStyle.Render("  " + tui.CheckMark + " kar stopped"))
	fmt.Println()

	// Show last summary from log
	showLastSummary(logPath)

	return nil
}

// showLastSummary reads the log file and displays the last SUMMARY line
func showLastSummary(logPath string) {
	file, err := os.Open(logPath)
	if err != nil {
		return
	}
	defer file.Close()

	var lastSummary string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "SUMMARY:") {
			lastSummary = line
		}
	}

	if lastSummary != "" {
		fmt.Println(tui.SubtitleStyle.Render("  Last Session Summary:"))
		// Parse and format summary
		if idx := strings.Index(lastSummary, "SUMMARY:"); idx != -1 {
			summary := lastSummary[idx+9:]
			parts := strings.Fields(summary)
			for _, part := range parts {
				kv := strings.Split(part, "=")
				if len(kv) == 2 {
					fmt.Printf("    %s: %s\n",
						tui.LabelStyle.Render(kv[0]),
						tui.ValueStyle.Render(kv[1]))
				}
			}
		}
		fmt.Println()
	}
}
