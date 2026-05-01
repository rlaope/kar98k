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
	masterListen     string
	masterConfigPath string
)

var masterCmd = &cobra.Command{
	Use:   "master",
	Short: "Run kar as a distributed master node",
	Long: `Start kar in distributed master mode.

The master node runs the traffic controller and distributes work to
registered worker nodes via gRPC. Workers connect using 'kar worker'.

Example:
  kar master --config kar.yaml
  kar master --config kar.yaml --listen :7777`,
	RunE: runMaster,
}

func init() {
	masterCmd.Flags().StringVarP(&masterConfigPath, "config", "c", "kar.yaml", "Path to configuration file")
	masterCmd.Flags().StringVar(&masterListen, "listen", ":7777", "gRPC listen address for worker connections")
}

func runMaster(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load(masterConfigPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if masterListen != "" {
		cfg.Master.Listen = masterListen
	}

	fmt.Printf("kar master starting (config: %s, grpc: %s)\n", masterConfigPath, cfg.Master.Listen)

	d, err := daemon.New(cfg, daemon.ModeMaster)
	if err != nil {
		return fmt.Errorf("failed to create master daemon: %w", err)
	}

	if err := d.Start(); err != nil {
		return fmt.Errorf("failed to start master: %w", err)
	}

	fmt.Println("Master ready. Waiting for workers to connect...")
	fmt.Println("Use 'kar trigger' to start traffic once workers are registered.")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	fmt.Println("\nShutting down master...")
	d.Stop()
	return nil
}
