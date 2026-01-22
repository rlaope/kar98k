package cli

import (
	"fmt"
	"os"
	"runtime"

	"github.com/charmbracelet/lipgloss"
	"github.com/kar98k/internal/tui"
	"github.com/spf13/cobra"
)

var (
	version   = "dev"
	buildTime = "unknown"
	gitCommit = "unknown"
)

// rootCmd represents the base command
var rootCmd = &cobra.Command{
	Use:   "kar",
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
  kar start       Launch interactive configuration
  kar run         Run with config file (headless)
  kar status      Check running instance status
  kar logs        View live logs
  kar stop        Stop running instance`,
}

// versionCmd shows version information
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show version information",
	Long:  `Display detailed version information about kar98k.`,
	Run: func(cmd *cobra.Command, args []string) {
		printVersion()
	},
}

func printVersion() {
	fmt.Println()
	fmt.Println(tui.Logo())
	fmt.Println()

	titleStyle := lipgloss.NewStyle().
		Foreground(tui.SkyBlue).
		Bold(true)

	valueStyle := lipgloss.NewStyle().
		Foreground(tui.White)

	dimStyle := lipgloss.NewStyle().
		Foreground(tui.LightGray)

	fmt.Println(titleStyle.Render("  Version Info"))
	fmt.Println(tui.Divider(40))
	fmt.Printf("  %s  %s\n", dimStyle.Render("Version:"), valueStyle.Render(version))
	fmt.Printf("  %s  %s\n", dimStyle.Render("Built:"), valueStyle.Render(buildTime))
	fmt.Printf("  %s  %s\n", dimStyle.Render("Commit:"), valueStyle.Render(gitCommit))
	fmt.Printf("  %s  %s\n", dimStyle.Render("Go:"), valueStyle.Render(runtime.Version()))
	fmt.Printf("  %s  %s/%s\n", dimStyle.Render("OS/Arch:"), valueStyle.Render(runtime.GOOS), valueStyle.Render(runtime.GOARCH))
	fmt.Println()
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
	rootCmd.AddCommand(versionCmd)
}

// SetVersion sets the version info
func SetVersion(v, bt string) {
	version = v
	buildTime = bt
}

// SetGitCommit sets the git commit hash
func SetGitCommit(gc string) {
	gitCommit = gc
}
