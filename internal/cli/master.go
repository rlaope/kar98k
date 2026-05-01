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
	masterListen       string
	masterConfigPath   string
	masterTLSCert      string
	masterTLSKey       string
	masterTLSCA        string
	masterAuthToken    string
	masterAuthTokenEnv string
)

var masterCmd = &cobra.Command{
	Use:   "master",
	Short: "Run kar as a distributed master node",
	Long: `Start kar in distributed master mode.

The master node runs the traffic controller and distributes work to
registered worker nodes via gRPC. Workers connect using 'kar worker'.

Example:
  kar master --config kar.yaml
  kar master --config kar.yaml --listen :7777
  kar master --config kar.yaml --tls-cert server.crt --tls-key server.key --auth-token-env KAR_AUTH_TOKEN`,
	RunE: runMaster,
}

func init() {
	masterCmd.Flags().StringVarP(&masterConfigPath, "config", "c", "kar.yaml", "Path to configuration file")
	masterCmd.Flags().StringVar(&masterListen, "listen", ":7777", "gRPC listen address for worker connections")
	masterCmd.Flags().StringVar(&masterTLSCert, "tls-cert", "", "Path to TLS certificate PEM (enables TLS)")
	masterCmd.Flags().StringVar(&masterTLSKey, "tls-key", "", "Path to TLS private key PEM (required with --tls-cert)")
	masterCmd.Flags().StringVar(&masterTLSCA, "tls-client-ca", "", "Path to client CA PEM for mTLS (optional)")
	masterCmd.Flags().StringVar(&masterAuthToken, "auth-token", "", "Bearer token workers must present (prefer --auth-token-env)")
	masterCmd.Flags().StringVar(&masterAuthTokenEnv, "auth-token-env", "KAR_AUTH_TOKEN", "Env var name to read auth token from (takes precedence over --auth-token)")
}

func runMaster(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load(masterConfigPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if masterListen != "" {
		cfg.Master.Listen = masterListen
	}

	// Validate --tls-cert and --tls-key are both-or-neither.
	if (masterTLSCert != "") != (masterTLSKey != "") {
		return fmt.Errorf("--tls-cert and --tls-key must be specified together")
	}

	// CLI TLS flags override config-file values.
	if masterTLSCert != "" {
		cfg.Master.TLS = &config.TLSConfig{
			Cert:     masterTLSCert,
			Key:      masterTLSKey,
			ClientCA: masterTLSCA,
		}
	}

	// Resolve auth token: env var takes precedence over raw flag.
	token := masterAuthToken
	if masterAuthTokenEnv != "" {
		if v := os.Getenv(masterAuthTokenEnv); v != "" {
			token = v
		}
	}
	if token != "" {
		cfg.Master.AuthToken = token
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
