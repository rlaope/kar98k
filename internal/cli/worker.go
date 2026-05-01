package cli

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kar98k/internal/daemon"
	"github.com/kar98k/internal/rpc"
	"github.com/spf13/cobra"
)

var (
	workerMasterAddr       string
	workerSelfAddr         string
	workerTLSCert          string
	workerTLSClientKey     string
	workerTLSCA            string
	workerTLSServerName    string
	workerAuthToken        string
	workerAuthTokenEnv     string
	workerReconnectBackoff time.Duration
	workerReconnectMax     int
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
  kar worker --master master.internal:7777 --worker-addr worker1.internal:0
  kar worker --master master.internal:7777 --tls-ca ca.crt --auth-token-env KAR_AUTH_TOKEN`,
	RunE: runWorker,
}

func init() {
	workerCmd.Flags().StringVar(&workerMasterAddr, "master", "", "Master node gRPC address (required)")
	workerCmd.Flags().StringVar(&workerSelfAddr, "worker-addr", defaultWorkerAddr(), "Worker's own address advertised to master")
	workerCmd.Flags().StringVar(&workerTLSCert, "tls-cert", "", "Path to client certificate PEM for mTLS")
	workerCmd.Flags().StringVar(&workerTLSClientKey, "tls-client-key", "", "Path to client private key PEM for mTLS (required with --tls-cert)")
	workerCmd.Flags().StringVar(&workerTLSCA, "tls-ca", "", "Path to CA certificate PEM to verify master")
	workerCmd.Flags().StringVar(&workerTLSServerName, "tls-server-name", "", "TLS server name override")
	workerCmd.Flags().StringVar(&workerAuthToken, "auth-token", "", "Bearer token to present to master (prefer --auth-token-env)")
	workerCmd.Flags().StringVar(&workerAuthTokenEnv, "auth-token-env", "KAR_AUTH_TOKEN", "Env var name to read auth token from (takes precedence over --auth-token)")
	workerCmd.Flags().DurationVar(&workerReconnectBackoff, "reconnect-max-backoff", 30*time.Second, "Maximum backoff between reconnect attempts")
	workerCmd.Flags().IntVar(&workerReconnectMax, "reconnect-max-attempts", 0, "Max consecutive failed reconnects before exit (0=unlimited)")
	_ = workerCmd.MarkFlagRequired("master")
}

func runWorker(cmd *cobra.Command, args []string) error {
	fmt.Printf("kar worker starting (master: %s, self: %s)\n", workerMasterAddr, workerSelfAddr)

	opts, err := buildClientOptions()
	if err != nil {
		return fmt.Errorf("TLS config: %w", err)
	}
	opts.BackoffMax = workerReconnectBackoff
	opts.MaxAttempts = workerReconnectMax

	wd := daemon.NewWorkerDaemon(workerMasterAddr, workerSelfAddr, opts)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	runErr := make(chan error, 1)
	go func() { runErr <- wd.Run() }()

	select {
	case <-sigCh:
		fmt.Println("\nDraining worker...")
		wd.Stop()
		return nil
	case err := <-runErr:
		if err != nil {
			return fmt.Errorf("worker exited: %w", err)
		}
		return nil
	}
}

// buildClientOptions constructs a ClientOptions from CLI flags.
// Returns zero-value options (plaintext, no auth) when no TLS/auth flags are set.
func buildClientOptions() (rpc.ClientOptions, error) {
	// Resolve auth token: env var takes precedence over raw flag.
	token := workerAuthToken
	if workerAuthTokenEnv != "" {
		if v := os.Getenv(workerAuthTokenEnv); v != "" {
			token = v
		}
	}

	// Validate --tls-cert and --tls-client-key are both-or-neither.
	if (workerTLSCert != "") != (workerTLSClientKey != "") {
		return rpc.ClientOptions{}, fmt.Errorf("--tls-cert and --tls-client-key must be specified together")
	}

	opts := rpc.ClientOptions{AuthToken: token}

	if workerTLSCA == "" && workerTLSCert == "" && workerTLSServerName == "" {
		return opts, nil
	}

	tc := &tls.Config{MinVersion: tls.VersionTLS12}
	if workerTLSServerName != "" {
		tc.ServerName = workerTLSServerName
	}
	if workerTLSCA != "" {
		caPEM, err := os.ReadFile(workerTLSCA)
		if err != nil {
			return opts, fmt.Errorf("read CA %s: %w", workerTLSCA, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return opts, fmt.Errorf("parse CA %s: no valid PEM block", workerTLSCA)
		}
		tc.RootCAs = pool
	}
	if workerTLSCert != "" {
		cert, err := tls.LoadX509KeyPair(workerTLSCert, workerTLSClientKey)
		if err != nil {
			return opts, fmt.Errorf("load client cert/key: %w", err)
		}
		tc.Certificates = []tls.Certificate{cert}
	}
	opts.TLSConfig = tc
	return opts, nil
}

func defaultWorkerAddr() string {
	host, err := os.Hostname()
	if err != nil {
		return "localhost:0"
	}
	return host + ":0"
}
