package cli

import (
	"fmt"
	"time"

	"github.com/kar98k/internal/daemon"
	"github.com/kar98k/internal/tui"
	"github.com/spf13/cobra"
)

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the kar98k daemon",
	Long: `Stop the running kar98k daemon gracefully.
This will drain in-flight requests before shutting down.`,
	RunE: runStop,
}

func init() {
	rootCmd.AddCommand(stopCmd)
}

func runStop(cmd *cobra.Command, args []string) error {
	if !daemon.IsRunning() {
		fmt.Println()
		fmt.Println(tui.WarningStyle.Render("  kar98k is not running"))
		fmt.Println()
		return nil
	}

	fmt.Println()
	fmt.Print(tui.InfoStyle.Render("  Stopping kar98k"))

	resp, err := daemon.SendCommand(daemon.Command{Type: "stop"})
	if err != nil {
		// Connection closed means daemon stopped
		fmt.Println()
		fmt.Println(tui.SuccessStyle.Render("  " + tui.CheckMark + " kar98k stopped"))
		fmt.Println()
		return nil
	}

	if resp.Success {
		// Wait a moment for daemon to fully stop
		time.Sleep(500 * time.Millisecond)
		fmt.Println()
		fmt.Println(tui.SuccessStyle.Render("  " + tui.CheckMark + " kar98k stopped"))
		fmt.Println()
	} else {
		fmt.Println()
		fmt.Println(tui.ErrorStyle.Render("  " + resp.Message))
		fmt.Println()
	}

	return nil
}
