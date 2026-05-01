package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/kar98k/internal/config"
	"github.com/kar98k/internal/daemon"
	"github.com/kar98k/internal/rpc"
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
	masterHAStore      string
	masterHAID         string
	masterHAEndpoints  string
	masterHAKey        string
	masterHATTL        time.Duration

	failoverHAStore     string
	failoverHAEndpoints string
	failoverHAKey       string
	failoverHATTL       time.Duration
	failoverTarget      string
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
	masterCmd.Flags().StringVar(&masterHAStore, "ha-store", "", "HA backend (none|memory|etcd) — default 'none' disables HA; 'etcd' requires -tags ha_etcd build")
	masterCmd.Flags().StringVar(&masterHAID, "ha-id", "", "Self-identifier for HA lease (defaults to hostname when set with --ha-store)")
	masterCmd.Flags().StringVar(&masterHAEndpoints, "ha-endpoints", "", "Comma-separated etcd endpoints (etcd backend only)")
	masterCmd.Flags().StringVar(&masterHAKey, "ha-key", "/kar98k/ha/lease", "etcd lease key (etcd backend only)")
	masterCmd.Flags().DurationVar(&masterHATTL, "ha-ttl", 5*time.Second, "HA lease TTL — failover SLA is bounded by this plus standby acquire time")

	failoverCmd.Flags().StringVar(&failoverHAStore, "ha-store", "etcd", "HA backend (memory|etcd)")
	failoverCmd.Flags().StringVar(&failoverHAEndpoints, "ha-endpoints", "", "Comma-separated etcd endpoints (etcd backend only)")
	failoverCmd.Flags().StringVar(&failoverHAKey, "ha-key", "/kar98k/ha/lease", "etcd lease key")
	failoverCmd.Flags().DurationVar(&failoverHATTL, "ha-ttl", 5*time.Second, "lease TTL")
	failoverCmd.Flags().StringVar(&failoverTarget, "target", "", "Target holder ID (empty string releases the lease)")
	masterCmd.AddCommand(failoverCmd)
}

// failoverCmd is `kar master failover` — operations command for graceful
// HA handoff. Calls store.ForceLeaseTransfer on the configured backend
// without bringing up a full master process.
//
// Phase-1 semantics: --target=<id> is BEST-EFFORT, not binding. The CLI
// writes a transient etcd lease with TTL --ha-ttl that nobody renews;
// after that TTL the key is auto-deleted and any standby running
// acquireWithRetry will pick it up. The named holder will own the
// lease only if it is the standby running --ha-id=<id>. To make
// --target binding, the named master must be reachable and its
// HALeaseManager must be running. See #72 architect note MAJOR-3 and
// the planned Phase-2 follow-up.
var failoverCmd = &cobra.Command{
	Use:   "failover",
	Short: "Force HA lease transfer to another holder (or release with --target=\"\")",
	Long: `Operator command for graceful HA failover.

Transfers the master HA lease to --target without requiring the current
primary to cooperate. Use --target="" to release the lease entirely so
any standby can pick it up.

Note: --target=<id> is best-effort in Phase 1. The named holder will
only own the lease if it has an active standby running --ha-id=<id>.`,
	RunE: runFailover,
}

func runFailover(cmd *cobra.Command, args []string) error {
	store, err := rpc.BuildHAStore(rpc.HAStoreSpec{
		Backend:   failoverHAStore,
		Endpoints: splitNonEmpty(failoverHAEndpoints),
		LeaseKey:  failoverHAKey,
		LeaseTTL:  failoverHATTL,
	})
	if err != nil {
		return fmt.Errorf("HA store: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := store.ForceLeaseTransfer(ctx, failoverTarget); err != nil {
		return fmt.Errorf("ForceLeaseTransfer: %w", err)
	}
	if failoverTarget == "" {
		fmt.Println("Lease released.")
	} else {
		fmt.Printf("Lease transferred to %q.\n", failoverTarget)
	}
	return nil
}

func splitNonEmpty(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
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

	// HA flags override config-file values when --ha-store is supplied.
	if masterHAStore != "" && masterHAStore != "none" {
		holder := masterHAID
		if holder == "" {
			h, _ := os.Hostname()
			holder = h
		}
		cfg.Master.HA = &config.HAConfig{
			Store:     masterHAStore,
			HolderID:  holder,
			Endpoints: splitNonEmpty(masterHAEndpoints),
			LeaseKey:  masterHAKey,
			TTL:       masterHATTL,
		}
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
