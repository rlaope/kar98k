package dashboard

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

// Stats holds real-time metrics sent to the dashboard.
type Stats struct {
	Timestamp   int64            `json:"timestamp"`
	RPS         float64          `json:"rps"`
	TotalReqs   int64            `json:"total_reqs"`
	TotalErrors int64            `json:"total_errors"`
	AvgLatency  float64          `json:"avg_latency"`
	P95Latency  float64          `json:"p95_latency"`
	P99Latency  float64          `json:"p99_latency"`
	ActiveVUs   int64            `json:"active_vus"`
	Iterations  int64            `json:"iterations"`
	ErrorRate   float64          `json:"error_rate"`
	StatusCodes map[int]int64    `json:"status_codes"`
	Checks      []CheckStat      `json:"checks"`
	Elapsed     float64          `json:"elapsed"`
}

// CheckStat is a single check result for the dashboard.
type CheckStat struct {
	Name   string  `json:"name"`
	Rate   float64 `json:"rate"`
	Passed int64   `json:"passed"`
	Failed int64   `json:"failed"`
}

// TriggerCallback is called when the dashboard triggers a test start/stop.
type TriggerCallback func(action string)

// Server is the real-time web dashboard server.
type Server struct {
	mu        sync.RWMutex
	addr      string
	clients   map[chan []byte]struct{}
	latest    Stats
	history   []Stats
	startTime time.Time
	scenario  string
	preset    string
	running   bool
	onTrigger TriggerCallback

	// forecastSrc, when set, powers /api/forecast. Daemon mode wires
	// this to a closure over controller.ForecastTimeline; script mode
	// leaves it nil so the endpoint returns 501.
	forecastSrc ForecastSource
}

// ForecastPoint is one sample on the dashboard forecast curve. Kept
// structurally compatible with pattern.SamplePoint so the daemon can
// pass either form through; the JSON encoding is what UI clients
// see.
type ForecastPoint struct {
	Time    time.Time `json:"time"`
	TPS     float64   `json:"tps"`
	Spiking bool      `json:"spiking,omitempty"`
	Phase   string    `json:"phase,omitempty"`
}

// ForecastSource produces the current forecast timeline. Server calls
// it lazily on each /api/forecast request so callers can return
// up-to-date results when config reloads.
type ForecastSource func() []ForecastPoint

// New creates a new dashboard server.
func New(addr string) *Server {
	return &Server{
		addr:      addr,
		clients:   make(map[chan []byte]struct{}),
		startTime: time.Now(),
		history:   make([]Stats, 0, 1800), // 30 min at 1/s
	}
}

// SetScenario sets the scenario metadata for display.
func (s *Server) SetScenario(name, preset string) {
	s.scenario = name
	s.preset = preset
}

// SetTriggerCallback sets the callback for start/stop from dashboard.
func (s *Server) SetTriggerCallback(cb TriggerCallback) {
	s.onTrigger = cb
}

// SetForecastSource wires the optional /api/forecast endpoint. Pass
// nil to disable the endpoint (server returns 501).
func (s *Server) SetForecastSource(src ForecastSource) {
	s.forecastSrc = src
}

// SetRunning updates the running state.
func (s *Server) SetRunning(running bool) {
	s.mu.Lock()
	s.running = running
	s.mu.Unlock()
}

// Start begins serving the dashboard.
func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/events", s.handleSSE)
	mux.HandleFunc("/api/stats", s.handleStats)
	mux.HandleFunc("/api/history", s.handleHistory)
	mux.HandleFunc("/api/start", s.handleStart)
	mux.HandleFunc("/api/stop", s.handleStop)
	mux.HandleFunc("/api/state", s.handleState)
	mux.HandleFunc("/api/forecast", s.handleForecast)

	server := &http.Server{
		Addr:    s.addr,
		Handler: mux,
	}

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("[dashboard] error: %v", err)
		}
	}()

	fmt.Printf("  Dashboard: http://localhost%s\n", s.addr)
	return nil
}

// Push sends new stats to all connected dashboard clients.
func (s *Server) Push(stats Stats) {
	s.mu.Lock()
	s.latest = stats
	s.history = append(s.history, stats)
	// Keep last 30 min
	if len(s.history) > 1800 {
		s.history = s.history[1:]
	}
	s.mu.Unlock()

	data, err := json.Marshal(stats)
	if err != nil {
		return
	}

	s.mu.RLock()
	for ch := range s.clients {
		select {
		case ch <- data:
		default:
			// Client too slow, skip
		}
	}
	s.mu.RUnlock()
}

func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	ch := make(chan []byte, 32)
	s.mu.Lock()
	s.clients[ch] = struct{}{}
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.clients, ch)
		s.mu.Unlock()
	}()

	// Send initial state
	s.mu.RLock()
	initial, _ := json.Marshal(map[string]interface{}{
		"scenario": s.scenario,
		"preset":   s.preset,
	})
	s.mu.RUnlock()
	fmt.Fprintf(w, "event: init\ndata: %s\n\n", initial)
	flusher.Flush()

	for {
		select {
		case data := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.latest)
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.history)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(dashboardHTML))
}

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	s.mu.Lock()
	s.running = true
	s.startTime = time.Now()
	s.history = s.history[:0]
	s.mu.Unlock()

	if s.onTrigger != nil {
		s.onTrigger("start")
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"started"}`))
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	s.mu.Lock()
	s.running = false
	s.mu.Unlock()

	if s.onTrigger != nil {
		s.onTrigger("stop")
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"stopped"}`))
}

// handleForecast returns the forecast timeline as JSON. Returns 501
// when no source is wired (e.g. script-mode dashboard, which has no
// pattern engine).
func (s *Server) handleForecast(w http.ResponseWriter, r *http.Request) {
	if s.forecastSrc == nil {
		http.Error(w, "forecast not configured", http.StatusNotImplemented)
		return
	}
	points := s.forecastSrc()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(points)
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"running":  s.running,
		"scenario": s.scenario,
		"preset":   s.preset,
	})
}
