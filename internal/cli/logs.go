package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/kar98k/internal/daemon"
	"github.com/kar98k/internal/tui"
	"github.com/spf13/cobra"
)

var (
	logsFollow bool
	logsTail   int
)

var logsCmd = &cobra.Command{
	Use:   "logs",
	Short: "View kar logs",
	Long: `View logs from the kar daemon.

Examples:
  kar logs          Show recent logs
  kar logs -f       Follow logs in real-time
  kar logs -n 50    Show last 50 lines`,
	RunE: runLogs,
}

func init() {
	logsCmd.Flags().BoolVarP(&logsFollow, "follow", "f", false, "Follow log output")
	logsCmd.Flags().IntVarP(&logsTail, "tail", "n", 20, "Number of lines to show")
	rootCmd.AddCommand(logsCmd)
}

func runLogs(cmd *cobra.Command, args []string) error {
	logPath := daemon.GetLogPath()

	// Check if log file exists
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		fmt.Println()
		fmt.Println(tui.WarningStyle.Render("  No logs found"))
		fmt.Println(tui.DimStyle.Render("  kar may not have been started yet"))
		fmt.Println()
		return nil
	}

	file, err := os.Open(logPath)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}
	defer file.Close()

	fmt.Println()
	fmt.Println(tui.TitleStyle.Render(" kar logs "))
	fmt.Println(tui.DimStyle.Render(fmt.Sprintf(" %s", logPath)))
	fmt.Println(tui.Divider(50))
	fmt.Println()

	if logsFollow {
		return followLogs(file)
	}

	return tailLogs(file, logsTail)
}

func tailLogs(file *os.File, n int) error {
	// Read all lines
	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	// Get last n lines
	start := 0
	if len(lines) > n {
		start = len(lines) - n
	}

	for _, line := range lines[start:] {
		printLogLine(line)
	}

	fmt.Println()
	return nil
}

func followLogs(file *os.File) error {
	// Seek to end
	file.Seek(0, io.SeekEnd)

	reader := bufio.NewReader(file)

	fmt.Println(tui.DimStyle.Render("Waiting for new logs... (Ctrl+C to exit)"))
	fmt.Println()

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			return err
		}

		printLogLine(strings.TrimRight(line, "\n"))
	}
}

func printLogLine(line string) {
	// Parse and colorize log line
	// Format: [2006-01-02 15:04:05] message

	if strings.Contains(line, "error") || strings.Contains(line, "Error") || strings.Contains(line, "failed") {
		fmt.Println(tui.ErrorStyle.Render(line))
	} else if strings.Contains(line, "warn") || strings.Contains(line, "Warn") {
		fmt.Println(tui.WarningStyle.Render(line))
	} else if strings.Contains(line, "Starting") || strings.Contains(line, "Trigger") {
		fmt.Println(tui.SuccessStyle.Render(line))
	} else {
		fmt.Println(tui.DimStyle.Render(line))
	}
}
