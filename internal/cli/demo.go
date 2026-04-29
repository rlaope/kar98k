package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/kar98k/internal/config"
	"github.com/kar98k/internal/controller"
	"github.com/kar98k/internal/health"
	"github.com/kar98k/internal/pattern"
	"github.com/kar98k/internal/tui"
	"github.com/kar98k/internal/worker"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/cobra"
)

var (
	demoDuration time.Duration
	demoQuiet    bool
)

var demoCmd = &cobra.Command{
	Use:   "demo",
	Short: "Run a self-contained traffic demo (no config needed)",
	Long: `Spin up an in-process echo server, point kar at it, and run a
short demo of irregular traffic. Zero config, zero external setup —
useful as a smoke test or to see what kar does at a glance.

The demo never touches the on-disk daemon socket or PID file, so it
won't collide with a kar daemon already running in the background.

Examples:
  kar demo                  Default 60s demo
  kar demo --duration 30s   Short demo
  kar demo --quiet          No per-second progress, just final summary`,
	RunE: runDemo,
}

func init() {
	demoCmd.Flags().DurationVar(&demoDuration, "duration", 60*time.Second, "demo runtime")
	demoCmd.Flags().BoolVar(&demoQuiet, "quiet", false, "suppress per-second progress lines")
	rootCmd.AddCommand(demoCmd)
}

func runDemo(cmd *cobra.Command, args []string) error {
	// kar demo is the first impression for new users; muting controller
	// and worker chatter keeps the output predictable. The log writer is
	// restored on the way out so callers chaining demo with other
	// commands still see logs.
	prevLog := log.Writer()
	log.SetOutput(io.Discard)
	defer log.SetOutput(prevLog)

	var served, failed int64
	ts := newDemoEcho(&served, &failed)
	defer ts.Close()

	cfg := demoConfig(ts.URL)
	// Private registry: promauto registers on the default registerer
	// by default, which would panic if the user re-runs `kar demo`
	// inside the same process (e.g. tests).
	metrics := health.NewMetricsWithRegistry(prometheus.NewRegistry())

	ctx, cancel := context.WithTimeout(context.Background(), demoDuration)
	defer cancel()

	pool := worker.NewPool(cfg.Worker, metrics)
	pool.Start(ctx)
	defer pool.Stop()

	engine := pattern.NewEngine(cfg.Pattern, cfg.Controller.BaseTPS, cfg.Controller.MaxTPS)
	// Checker is intentionally nil: the demo target lives in this process,
	// so probing it adds latency for no information. Controller.submitJobs
	// treats nil checker as "always healthy".
	ctrl := controller.NewController(cfg.Controller, cfg.Targets, engine, pool, nil, metrics)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
		}
	}()

	demoIntro(ts.URL, cfg)

	ctrl.Start(ctx)
	start := time.Now()

	if !demoQuiet {
		go demoTicker(ctx, ctrl, &served, start)
	}

	<-ctx.Done()
	ctrl.Stop()

	demoSummary(time.Since(start), &served, &failed, pool)
	return nil
}

func newDemoEcho(served, failed *int64) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(served, 1)
		switch roll := rand.Float64(); {
		case roll < 0.03:
			atomic.AddInt64(failed, 1)
			w.WriteHeader(http.StatusInternalServerError)
			return
		case roll < 0.08:
			time.Sleep(time.Duration(20+rand.Intn(80)) * time.Millisecond)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
}

func demoIntro(url string, cfg *config.Config) {
	fmt.Println()
	fmt.Println(tui.TitleStyle.Render(" KAR DEMO "))
	fmt.Println()
	fmt.Printf("  echo target:  %s\n", tui.DimStyle.Render(url))
	fmt.Printf("  base TPS:     %s\n", tui.ValueStyle.Render(fmt.Sprintf("%.0f", cfg.Controller.BaseTPS)))
	fmt.Printf("  max TPS:      %s\n", tui.ValueStyle.Render(fmt.Sprintf("%.0f", cfg.Controller.MaxTPS)))
	fmt.Printf("  duration:     %s\n", tui.ValueStyle.Render(demoDuration.String()))
	fmt.Printf("  spikes:       %s\n", tui.DimStyle.Render(fmt.Sprintf(
		"λ=%.2f, factor=%.0fx, ramp=%s/%s",
		cfg.Pattern.Poisson.Lambda,
		cfg.Pattern.Poisson.SpikeFactor,
		cfg.Pattern.Poisson.RampUp,
		cfg.Pattern.Poisson.RampDown,
	)))
	fmt.Println()
	fmt.Println(tui.DimStyle.Render("  Press Ctrl+C to stop early."))
	fmt.Println()
}

func demoTicker(ctx context.Context, ctrl *controller.Controller, served *int64, start time.Time) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	var prev int64
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			total := atomic.LoadInt64(served)
			delta := total - prev
			prev = total
			elapsed := now.Sub(start).Truncate(time.Second)
			st := ctrl.GetStatus()
			marker := ""
			if st.PatternStatus.PoissonSpiking {
				marker = " " + tui.WarningStyle.Render("⚡spike")
			}
			fmt.Printf("  [%6s] %s req/s   total %s   drops %d%s\n",
				elapsed,
				tui.ValueStyle.Render(fmt.Sprintf("%4d", delta)),
				tui.ValueStyle.Render(fmt.Sprintf("%6d", total)),
				st.QueueDrops,
				marker,
			)
		}
	}
}

func demoSummary(elapsed time.Duration, served, failed *int64, pool *worker.Pool) {
	total := atomic.LoadInt64(served)
	errs := atomic.LoadInt64(failed)
	rps := 0.0
	if elapsed.Seconds() > 0 {
		rps = float64(total) / elapsed.Seconds()
	}
	errPct := 0.0
	if total > 0 {
		errPct = 100 * float64(errs) / float64(total)
	}

	fmt.Println()
	fmt.Println(tui.SubtitleStyle.Render("  Result"))
	fmt.Printf("    served:    %s requests in %s\n",
		tui.ValueStyle.Render(fmt.Sprintf("%d", total)),
		tui.DimStyle.Render(elapsed.Truncate(time.Millisecond).String()))
	fmt.Printf("    actual:    %s req/s\n", tui.ValueStyle.Render(fmt.Sprintf("%.1f", rps)))
	fmt.Printf("    errors:    %s\n",
		tui.ErrorStyle.Render(fmt.Sprintf("%d (%.2f%%)", errs, errPct)))
	if drops := pool.TotalDrops(); drops > 0 {
		fmt.Printf("    drops:     %s\n",
			tui.WarningStyle.Render(fmt.Sprintf("%d (%.2f%% sustained)", drops, pool.DropRate()*100)))
	}
	fmt.Printf("    P95/P99:   %s / %s\n",
		tui.ValueStyle.Render(fmt.Sprintf("%.1fms", pool.LatencyPercentile(95, false))),
		tui.ValueStyle.Render(fmt.Sprintf("%.1fms", pool.LatencyPercentile(99, false))))
	fmt.Println()
	fmt.Println(tui.DimStyle.Render("  Tip: try `kar simulate` to forecast a real config without running it."))
	fmt.Println()
}

// demoConfig synthesises a config aimed at a single in-process echo
// target. Spike settings are tuned so a 60s demo shows at least one
// visible spike on average (lambda 0.1 ≈ one event every 10s).
func demoConfig(url string) *config.Config {
	cfg := config.DefaultConfig()
	cfg.Targets = []config.Target{{
		Name:     "demo-echo",
		URL:      url,
		Protocol: config.ProtocolHTTP,
		Method:   "GET",
		Weight:   1,
		Timeout:  2 * time.Second,
	}}
	cfg.Controller.BaseTPS = 30
	cfg.Controller.MaxTPS = 200
	cfg.Controller.RampUpDuration = 2 * time.Second
	cfg.Controller.Schedule = nil
	cfg.Pattern.Poisson.Enabled = true
	cfg.Pattern.Poisson.Lambda = 0.1
	cfg.Pattern.Poisson.SpikeFactor = 3
	cfg.Pattern.Poisson.MinInterval = 5 * time.Second
	cfg.Pattern.Poisson.MaxInterval = 20 * time.Second
	cfg.Pattern.Poisson.RampUp = 1 * time.Second
	cfg.Pattern.Poisson.RampDown = 2 * time.Second
	cfg.Pattern.Noise.Enabled = false
	cfg.Worker.PoolSize = 32
	cfg.Worker.QueueSize = 4096
	return cfg
}
