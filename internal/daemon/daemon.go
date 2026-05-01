package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/kar98k/internal/config"
	"github.com/kar98k/internal/controller"
	"github.com/kar98k/internal/dashboard"
	"github.com/kar98k/internal/health"
	"github.com/kar98k/internal/pattern"
	"github.com/kar98k/internal/rpc"
	"github.com/kar98k/internal/worker"
)

const (
	SocketName = "kar98k.sock"
	PidFile    = "kar98k.pid"
	LogFile    = "kar98k.log"
)

// Mode selects the daemon's operating mode.
type Mode int

const (
	ModeSolo   Mode = iota // default: single-process, local pool
	ModeMaster             // distributed master: gRPC server + WorkerRegistry as PoolFacade
	ModeWorker             // distributed worker: no controller, no dashboard
)

// Status represents the current daemon status
type Status struct {
	Running             bool      `json:"running"`
	Triggered           bool      `json:"triggered"`
	StartTime           time.Time `json:"start_time"`
	Uptime              string    `json:"uptime"`
	CurrentTPS          float64   `json:"current_tps"`
	TargetTPS           float64   `json:"target_tps"`
	RequestsSent        int64     `json:"requests_sent"`
	ErrorCount          int64     `json:"error_count"`
	AvgLatency          float64   `json:"avg_latency_ms"`
	LatencyP95Raw       float64   `json:"latency_p95_raw_ms"`
	LatencyP99Raw       float64   `json:"latency_p99_raw_ms"`
	LatencyP95Corrected float64   `json:"latency_p95_corrected_ms"`
	LatencyP99Corrected float64   `json:"latency_p99_corrected_ms"`
	IsSpiking           bool      `json:"is_spiking"`
	SpikeKind           string    `json:"spike_kind"`    // "none" | "auto" | "manual"
	NextSpikeIn         string    `json:"next_spike_in"` // e.g. "2m13s", or "" while spiking
	TargetURL           string    `json:"target_url"`
	Protocol            string    `json:"protocol"`
	QueueDrops          int64     `json:"queue_drops"`
	QueueDropRate       float64   `json:"queue_drop_rate"`
	// Scenario fields are zero unless the loaded config defines a
	// `scenarios:` array. Total == 0 means single-pattern mode.
	ScenarioName     string `json:"scenario_name,omitempty"`
	ScenarioIndex    int    `json:"scenario_index,omitempty"`
	ScenarioTotal    int    `json:"scenario_total,omitempty"`
	ScenarioElapsed  string `json:"scenario_elapsed,omitempty"`
	ScenarioDuration string `json:"scenario_duration,omitempty"`
	ScenarioDone     bool   `json:"scenario_done,omitempty"`
}

// Command represents a command sent to the daemon
type Command struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

// Response represents a response from the daemon
type Response struct {
	Success bool        `json:"success"`
	Message string      `json:"message,omitempty"`
	Data    interface{} `json:"data,omitempty"`
}

// Daemon manages the kar98k service
type Daemon struct {
	cfg           *config.Config
	mode          Mode
	ctrl          *controller.Controller
	pool          *worker.Pool
	registry      *rpc.WorkerRegistry
	grpcServer    *rpc.GRPCServer
	checker       *health.Checker
	metrics       *health.Metrics
	engine        *pattern.Engine
	metricsServer *health.Server
	dashboard     *dashboard.Server

	// workerSnapshotFn is set by startMaster() and wired into the dashboard
	// after dashboard init in Start(). Nil in solo/worker mode.
	workerSnapshotFn func() []dashboard.WorkerRow

	status     Status
	mu         sync.RWMutex
	ctx        context.Context
	cancel     context.CancelFunc
	listener   net.Listener
	socketPath string
	logFile    *os.File
}

// GetRuntimeDir returns the runtime directory for kar98k
func GetRuntimeDir() string {
	// Use XDG_RUNTIME_DIR if available, otherwise use /tmp
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return filepath.Join(dir, "kar98k")
	}
	return filepath.Join(os.TempDir(), "kar98k")
}

// GetSocketPath returns the full path to the socket file
func GetSocketPath() string {
	return filepath.Join(GetRuntimeDir(), SocketName)
}

// GetPidPath returns the full path to the pid file
func GetPidPath() string {
	return filepath.Join(GetRuntimeDir(), PidFile)
}

// GetLogPath returns the full path to the log file
func GetLogPath() string {
	return filepath.Join(GetRuntimeDir(), LogFile)
}

// New creates a new daemon instance operating in the given mode.
func New(cfg *config.Config, mode Mode) (*Daemon, error) {
	runtimeDir := GetRuntimeDir()
	if err := os.MkdirAll(runtimeDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create runtime directory: %w", err)
	}

	logFile, err := os.OpenFile(GetLogPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	d := &Daemon{
		cfg:        cfg,
		mode:       mode,
		ctx:        ctx,
		cancel:     cancel,
		socketPath: GetSocketPath(),
		logFile:    logFile,
		status: Status{
			Running: true,
		},
	}

	return d, nil
}

// Start starts the daemon
func (d *Daemon) Start() error {
	d.log("Starting kar98k daemon (mode=%d)...", d.mode)

	// Write PID file
	if err := os.WriteFile(GetPidPath(), []byte(fmt.Sprintf("%d", os.Getpid())), 0644); err != nil {
		return fmt.Errorf("failed to write pid file: %w", err)
	}

	// Remove existing socket if present
	os.Remove(d.socketPath)

	// Create Unix socket
	var err error
	d.listener, err = net.Listen("unix", d.socketPath)
	if err != nil {
		return fmt.Errorf("failed to create socket: %w", err)
	}

	d.metrics = health.NewMetrics()
	d.engine = pattern.NewEngine(d.cfg.Pattern, d.cfg.Controller.BaseTPS, d.cfg.Controller.MaxTPS)

	if d.mode == ModeMaster {
		if err := d.startMaster(); err != nil {
			return err
		}
	} else {
		d.startSolo()
	}

	// Optional web dashboard
	if d.cfg.Dashboard.Enabled {
		addr := d.cfg.Dashboard.Address
		if addr == "" {
			addr = ":7000"
		}
		d.dashboard = dashboard.New(addr)
		d.dashboard.SetForecastSource(d.buildForecast)
		if d.workerSnapshotFn != nil {
			d.dashboard.SetWorkerSource(d.workerSnapshotFn)
		}
		d.dashboard.Start()
	}

	// Metrics server
	if d.cfg.Metrics.Enabled {
		d.metricsServer = health.NewServer(d.cfg.Metrics)
		go func() {
			if err := d.metricsServer.Start(); err != nil {
				d.log("Metrics server error: %v", err)
			}
		}()
	}

	d.status.StartTime = time.Now()
	if len(d.cfg.Targets) > 0 {
		d.status.TargetURL = d.cfg.Targets[0].URL
		d.status.Protocol = string(d.cfg.Targets[0].Protocol)
	}

	d.log("Daemon started, waiting for trigger...")
	go d.acceptConnections()
	return nil
}

// startSolo initialises the single-process (default) path.
func (d *Daemon) startSolo() {
	d.pool = worker.NewPool(d.cfg.Worker, d.metrics)
	d.checker = health.NewChecker(d.cfg.Health, d.cfg.Targets, d.metrics)
	d.ctrl = controller.NewController(d.cfg.Controller, d.cfg.Targets, d.engine, d.pool, d.checker, d.metrics, &controller.LocalSubmitter{})
	d.ctrl.AttachScenarios(d.cfg.Scenarios, d.cfg.Pattern)
	d.ctrl.AttachSafety(d.cfg.Safety, d.pool)
}

// startMaster initialises the distributed-master path: gRPC server +
// WorkerRegistry as PoolFacade; no local pool or health checker.
func (d *Daemon) startMaster() error {
	d.registry = rpc.NewWorkerRegistry()
	d.checker = health.NewChecker(d.cfg.Health, d.cfg.Targets, d.metrics)
	d.ctrl = controller.NewController(d.cfg.Controller, d.cfg.Targets, d.engine, d.registry, d.checker, d.metrics, controller.NoopSubmitter{})
	d.ctrl.AttachScenarios(d.cfg.Scenarios, d.cfg.Pattern)

	listen := d.cfg.Master.Listen
	if listen == "" {
		listen = ":7777"
	}
	grpcSrv, err := rpc.NewGRPCServer(listen, d.registry)
	if err != nil {
		return fmt.Errorf("gRPC server: %w", err)
	}
	d.grpcServer = grpcSrv
	go func() {
		if err := grpcSrv.Serve(); err != nil {
			d.log("gRPC server stopped: %v", err)
		}
	}()
	d.log("gRPC master listening on %s", listen)

	// Wire worker snapshot into dashboard when it starts (after this func returns).
	// We store the closure now; dashboard.SetWorkerSource is called in Start() after
	// startMaster() completes, so d.dashboard may still be nil here — we defer via
	// a hook applied in Start() after dashboard init.
	d.workerSnapshotFn = func() []dashboard.WorkerRow {
		rows := d.registry.Snapshot()
		out := make([]dashboard.WorkerRow, len(rows))
		for i, r := range rows {
			out[i] = dashboard.WorkerRow{
				ID:             r.ID,
				Addr:           r.Addr,
				LastBeatAgoSec: r.LastBeatAgoSec,
				CurrentTPS:     r.CurrentTPS,
				Drops:          r.Drops,
				ErrorRate:      r.ErrorRate,
			}
		}
		return out
	}
	return nil
}

// Trigger starts traffic generation
func (d *Daemon) Trigger() {
	d.mu.Lock()
	if d.status.Triggered {
		d.mu.Unlock()
		return
	}
	d.status.Triggered = true
	d.mu.Unlock()

	d.log("Trigger pulled! Starting traffic generation...")
	d.log("Target: %s (%s)", d.status.TargetURL, d.status.Protocol)
	d.log("Base TPS: %.0f, Max TPS: %.0f", d.cfg.Controller.BaseTPS, d.cfg.Controller.MaxTPS)

	if d.pool != nil {
		d.pool.Start(d.ctx)
	}
	d.checker.Start(d.ctx)
	d.ctrl.Start(d.ctx)

	// Start event monitoring
	go d.monitorEvents()
}

// Pause pauses traffic generation
func (d *Daemon) Pause() {
	d.mu.Lock()
	d.status.Triggered = false
	d.mu.Unlock()

	d.log("Traffic generation paused")
}

// GetStatus returns the current status
func (d *Daemon) GetStatus() Status {
	d.mu.RLock()
	defer d.mu.RUnlock()

	status := d.status
	if status.StartTime.IsZero() == false {
		status.Uptime = time.Since(status.StartTime).Round(time.Second).String()
	}

	// Get real-time stats from controller if running
	if d.ctrl != nil && status.Triggered {
		ctrlStatus := d.ctrl.GetStatus()
		status.CurrentTPS = ctrlStatus.PatternStatus.BaseTPS
		status.IsSpiking = ctrlStatus.PatternStatus.PoissonSpiking
		status.QueueDrops = ctrlStatus.QueueDrops
		status.QueueDropRate = ctrlStatus.QueueDropRate
		status.SpikeKind = string(ctrlStatus.PatternStatus.SpikeKind)
		if !ctrlStatus.PatternStatus.PoissonSpiking && ctrlStatus.PatternStatus.NextSpikeIn > 0 {
			status.NextSpikeIn = ctrlStatus.PatternStatus.NextSpikeIn.Round(time.Second).String()
		}
		status.LatencyP95Raw = ctrlStatus.LatencyP95Raw
		status.LatencyP99Raw = ctrlStatus.LatencyP99Raw
		status.LatencyP95Corrected = ctrlStatus.LatencyP95Corrected
		status.LatencyP99Corrected = ctrlStatus.LatencyP99Corrected

		if sc := ctrlStatus.Scenario; sc.Total > 0 {
			status.ScenarioName = sc.Name
			status.ScenarioIndex = sc.Index
			status.ScenarioTotal = sc.Total
			status.ScenarioElapsed = sc.Elapsed.Round(time.Second).String()
			status.ScenarioDuration = sc.Duration.Round(time.Second).String()
			status.ScenarioDone = sc.Done
		}
	}

	return status
}

// Stop stops the daemon
func (d *Daemon) Stop() {
	d.log("Stopping daemon...")

	// gRPC server must stop before controller so in-flight RPCs finish.
	// Order: stop accepting RPCs → stop registry sweeper → cancel context
	// → tear down controller (whose goroutines observe the cancel).
	if d.grpcServer != nil {
		d.grpcServer.Stop()
	}
	if d.registry != nil {
		d.registry.Stop()
	}

	d.cancel()

	if d.ctrl != nil {
		d.ctrl.Stop()
	}
	if d.checker != nil {
		d.checker.Stop()
	}
	if d.pool != nil {
		d.pool.Drain(d.cfg.Controller.ShutdownTimeout)
		d.pool.Stop()
	}
	if d.metricsServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		d.metricsServer.Stop(ctx)
	}
	if d.listener != nil {
		d.listener.Close()
	}

	os.Remove(d.socketPath)
	os.Remove(GetPidPath())

	if d.logFile != nil {
		d.logFile.Close()
	}

	d.log("Daemon stopped")
}

func (d *Daemon) acceptConnections() {
	for {
		conn, err := d.listener.Accept()
		if err != nil {
			select {
			case <-d.ctx.Done():
				return
			default:
				d.log("Accept error: %v", err)
				continue
			}
		}
		go d.handleConnection(conn)
	}
}

func (d *Daemon) handleConnection(conn net.Conn) {
	defer conn.Close()

	decoder := json.NewDecoder(conn)
	encoder := json.NewEncoder(conn)

	var cmd Command
	if err := decoder.Decode(&cmd); err != nil {
		encoder.Encode(Response{Success: false, Message: err.Error()})
		return
	}

	var resp Response

	switch cmd.Type {
	case "status":
		resp = Response{Success: true, Data: d.GetStatus()}

	case "trigger":
		d.Trigger()
		resp = Response{Success: true, Message: "Trigger pulled!"}

	case "pause":
		d.Pause()
		resp = Response{Success: true, Message: "Traffic paused"}

	case "resume":
		// Force-clear an open circuit breaker. Idempotent: a no-op
		// when the breaker is already closed or safety is disabled.
		if d.ctrl != nil {
			d.ctrl.ManualResume()
		}
		resp = Response{Success: true, Message: "Resume signalled (clears any tripped circuit breaker)"}

	case "stop":
		resp = Response{Success: true, Message: "Stopping daemon..."}
		encoder.Encode(resp)
		go func() {
			time.Sleep(100 * time.Millisecond)
			d.Stop()
			os.Exit(0)
		}()
		return

	default:
		resp = Response{Success: false, Message: "Unknown command: " + cmd.Type}
	}

	encoder.Encode(resp)
}

func (d *Daemon) log(format string, args ...interface{}) {
	msg := fmt.Sprintf("[%s] %s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, args...))
	if d.logFile != nil {
		d.logFile.WriteString(msg)
		d.logFile.Sync() // Flush immediately for tail -f
	}
}

// buildForecast is the ForecastSource the dashboard calls on every
// /api/forecast request. It walks the next 24h of the loaded config
// at 5-minute resolution starting from the top of the current hour
// — same parameters `kar simulate` defaults to so the two surfaces
// agree on the curve.
//
// Lazy on purpose: the closure captures *d.cfg, so config reloads
// (when supported) flow through without restarting the dashboard.
func (d *Daemon) buildForecast() []dashboard.ForecastPoint {
	sched := controller.NewScheduler(d.cfg.Controller.Schedule)
	start := time.Now().Truncate(time.Hour)
	pts := controller.ForecastTimeline(d.cfg, sched, start, 24*time.Hour, 5*time.Minute, 0)
	out := make([]dashboard.ForecastPoint, 0, len(pts))
	for _, p := range pts {
		out = append(out, dashboard.ForecastPoint{
			Time:    p.Time,
			TPS:     p.TPS,
			Spiking: p.Spiking,
			Phase:   p.Phase,
		})
	}
	return out
}

// pushDashboard ships the daemon's current Status to the dashboard
// server (when running). Translation is shallow — we map the daemon
// surface fields onto dashboard.Stats and let the existing UI render.
func (d *Daemon) pushDashboard() {
	if d.dashboard == nil {
		return
	}
	st := d.GetStatus()
	d.dashboard.Push(dashboard.Stats{
		Timestamp:   time.Now().Unix(),
		RPS:         st.CurrentTPS,
		TotalReqs:   st.RequestsSent,
		TotalErrors: st.ErrorCount,
		AvgLatency:  st.AvgLatency,
		P95Latency:  st.LatencyP95Raw,
		P99Latency:  st.LatencyP99Raw,
		ErrorRate:   safeErrorRate(st.RequestsSent, st.ErrorCount),
		Elapsed:     time.Since(st.StartTime).Seconds(),
	})
}

func safeErrorRate(reqs, errs int64) float64 {
	if reqs <= 0 {
		return 0
	}
	return float64(errs) / float64(reqs) * 100
}

// monitorEvents monitors and logs traffic events
func (d *Daemon) monitorEvents() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	var lastSpiking bool
	var lastTPS float64
	var lastErrorCount int64
	var peakTPS float64
	var totalRequests int64

	d.log("EVENT: Traffic generation started")

	for {
		select {
		case <-d.ctx.Done():
			d.log("EVENT: Traffic generation stopped")
			d.log("SUMMARY: Peak TPS=%.0f, Total Requests=%d", peakTPS, totalRequests)
			return
		case <-ticker.C:
			if d.ctrl == nil {
				continue
			}

			status := d.ctrl.GetStatus()
			currentTPS := status.PatternStatus.CurrentTPS
			isSpiking := status.PatternStatus.PoissonSpiking

			// Mirror current daemon Status onto the dashboard, when one
			// is configured. Cheap (returns immediately when disabled).
			d.pushDashboard()

			// Track peak TPS
			if currentTPS > peakTPS {
				peakTPS = currentTPS
				d.log("EVENT: New peak TPS=%.0f", peakTPS)
			}

			// Detect spike start
			if isSpiking && !lastSpiking {
				d.log("EVENT: SPIKE START - TPS jumping from %.0f to %.0f (%.1fx)",
					lastTPS, currentTPS, currentTPS/lastTPS)
			}

			// Detect spike end
			if !isSpiking && lastSpiking {
				d.log("EVENT: SPIKE END - TPS returning to %.0f", currentTPS)
			}

			// Log significant TPS changes (>20%)
			if lastTPS > 0 {
				change := (currentTPS - lastTPS) / lastTPS
				if change > 0.2 || change < -0.2 {
					d.log("TPS: %.0f -> %.0f (%+.1f%%)", lastTPS, currentTPS, change*100)
				}
			}

			// Detect error spike
			currentErrors := d.status.ErrorCount
			if currentErrors-lastErrorCount > 10 {
				d.log("WARNING: Error spike detected - %d new errors in last second",
					currentErrors-lastErrorCount)
			}

			// Periodic status (every 10 seconds)
			if time.Now().Second()%10 == 0 {
				d.log("STATUS: TPS=%.0f, Requests=%d, Errors=%d, Workers=%d, Queue=%d, Drops=%d (%.2f%%)",
					currentTPS,
					totalRequests,
					d.status.ErrorCount,
					status.ActiveWorkers,
					status.QueueSize,
					status.QueueDrops,
					status.QueueDropRate*100)
			}

			lastSpiking = isSpiking
			lastTPS = currentTPS
			lastErrorCount = currentErrors
			totalRequests++
		}
	}
}

// IsRunning checks if a daemon is already running
func IsRunning() bool {
	conn, err := net.Dial("unix", GetSocketPath())
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// SendCommand sends a command to the running daemon
func SendCommand(cmd Command) (*Response, error) {
	conn, err := net.Dial("unix", GetSocketPath())
	if err != nil {
		return nil, fmt.Errorf("daemon not running: %w", err)
	}
	defer conn.Close()

	encoder := json.NewEncoder(conn)
	decoder := json.NewDecoder(conn)

	if err := encoder.Encode(cmd); err != nil {
		return nil, fmt.Errorf("failed to send command: %w", err)
	}

	var resp Response
	if err := decoder.Decode(&resp); err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	return &resp, nil
}
