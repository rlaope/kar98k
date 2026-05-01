package cli

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/kar98k/internal/daemon"
	"github.com/spf13/cobra"
)

var (
	workerMasterAddr string
	workerSelfAddr   string
)

var workerCmd = &cobra.Command{
	Use:   "worker",
	Short: "Run kar as a distributed worker node",
	Long: `Start kar in distributed worker mode.

The worker connects to a master node, receives TPS assignments, and
executes requests against the targets propagated from master config.
No controller, no dashboard — pure execution engine.

Example:
  kar worker --master 192.168.1.10:7777
  kar worker --master master.internal:7777 --worker-addr worker1.internal:0`,
	RunE: runWorker,
}

func init() {
	workerCmd.Flags().StringVar(&workerMasterAddr, "master", "", "Master node gRPC address (required)")
	workerCmd.Flags().StringVar(&workerSelfAddr, "worker-addr", defaultWorkerAddr(), "Worker's own address advertised to master")
	_ = workerCmd.MarkFlagRequired("master")
}

func runWorker(cmd *cobra.Command, args []string) error {
	fmt.Printf("kar worker starting (master: %s, self: %s)\n", workerMasterAddr, workerSelfAddr)

	wd := daemon.NewWorkerDaemon(workerMasterAddr, workerSelfAddr)
	if err := wd.Start(); err != nil {
		return fmt.Errorf("failed to start worker: %w", err)
	}

	fmt.Println("Worker registered. Running...")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	fmt.Println("\nDraining worker...")
	wd.Stop()
	return nil
}

func defaultWorkerAddr() string {
	host, err := os.Hostname()
	if err != nil {
		return "localhost:0"
	}
	return host + ":0"
}
