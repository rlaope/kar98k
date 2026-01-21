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
	"github.com/kar98k/internal/health"
	"github.com/kar98k/internal/pattern"
	"github.com/kar98k/internal/worker"
)

const (
	SocketName = "kar98k.sock"
	PidFile    = "kar98k.pid"
	LogFile    = "kar98k.log"
)

// Status represents the current daemon status
type Status struct {
	Running      bool      `json:"running"`
	Triggered    bool      `json:"triggered"`
	StartTime    time.Time `json:"start_time"`
	Uptime       string    `json:"uptime"`
	CurrentTPS   float64   `json:"current_tps"`
	TargetTPS    float64   `json:"target_tps"`
	RequestsSent int64     `json:"requests_sent"`
	ErrorCount   int64     `json:"error_count"`
	AvgLatency   float64   `json:"avg_latency_ms"`
	IsSpiking    bool      `json:"is_spiking"`
	TargetURL    string    `json:"target_url"`
	Protocol     string    `json:"protocol"`
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
	cfg        *config.Config
	ctrl       *controller.Controller
	pool       *worker.Pool
	checker    *health.Checker
	metrics    *health.Metrics
	engine     *pattern.Engine
	metricsServer *health.Server

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

// New creates a new daemon instance
func New(cfg *config.Config) (*Daemon, error) {
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
	d.log("Starting kar98k daemon...")

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

	// Initialize components
	d.metrics = health.NewMetrics()
	d.engine = pattern.NewEngine(d.cfg.Pattern, d.cfg.Controller.BaseTPS, d.cfg.Controller.MaxTPS)
	d.pool = worker.NewPool(d.cfg.Worker, d.metrics)
	d.checker = health.NewChecker(d.cfg.Health, d.cfg.Targets, d.metrics)
	d.ctrl = controller.NewController(d.cfg.Controller, d.cfg.Targets, d.engine, d.pool, d.checker, d.metrics)

	// Start metrics server
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

	// Accept connections
	go d.acceptConnections()

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

	d.pool.Start(d.ctx)
	d.checker.Start(d.ctx)
	d.ctrl.Start(d.ctx)
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
	}

	return status
}

// Stop stops the daemon
func (d *Daemon) Stop() {
	d.log("Stopping daemon...")

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
