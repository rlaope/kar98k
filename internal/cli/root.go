package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	version   = "dev"
	buildTime = "unknown"
)

// rootCmd represents the base command
var rootCmd = &cobra.Command{
	Use:   "kar98k",
	Short: "High-Intensity Irregular Traffic Simulation",
	Long: `
    ██╗  ██╗ █████╗ ██████╗  █████╗  █████╗ ██╗  ██╗
    ██║ ██╔╝██╔══██╗██╔══██╗██╔══██╗██╔══██╗██║ ██╔╝
    █████╔╝ ███████║██████╔╝╚██████║╚█████╔╝█████╔╝
    ██╔═██╗ ██╔══██║██╔══██╗ ╚═══██║██╔══██╗██╔═██╗
    ██║  ██╗██║  ██║██║  ██║ █████╔╝╚█████╔╝██║  ██╗
    ╚═╝  ╚═╝╚═╝  ╚═╝╚═╝  ╚═╝ ╚════╝  ╚════╝ ╚═╝  ╚═╝

kar98k is a high-intensity irregular traffic simulation service.
Generate realistic, unpredictable traffic patterns for load testing.

Get started:
  kar98k start       Launch interactive configuration
  kar98k run         Run with config file (headless)
  kar98k status      Check running instance status
  kar98k logs        View live logs
  kar98k stop        Stop running instance`,
	Version: fmt.Sprintf("%s (built %s)", version, buildTime),
}

// Execute runs the root command
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.CompletionOptions.DisableDefaultCmd = true
}

// SetVersion sets the version info
func SetVersion(v, bt string) {
	version = v
	buildTime = bt
	rootCmd.Version = fmt.Sprintf("%s (built %s)", version, buildTime)
}
