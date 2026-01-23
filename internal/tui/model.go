package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Log file path
var logFile *os.File

// InitLogger initializes the log file
func InitLogger() error {
	dir := filepath.Join(os.TempDir(), "kar98k")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	var err error
	logFile, err = os.OpenFile(filepath.Join(dir, "kar98k.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	return err
}

// CloseLogger closes the log file
func CloseLogger() {
	if logFile != nil {
		logFile.Close()
	}
}

// Log writes a message to the log file
func Log(format string, args ...interface{}) {
	if logFile == nil {
		return
	}
	msg := fmt.Sprintf("[%s] %s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, args...))
	logFile.WriteString(msg)
	logFile.Sync()
}

// Screen represents different screens in the TUI
type Screen int

const (
	ScreenWelcome Screen = iota
	ScreenTargetSetup
	ScreenTrafficConfig
	ScreenPatternConfig
	ScreenReview
	ScreenRunning
	ScreenReport
)

// TimeSlot represents stats for a specific time period
type TimeSlot struct {
	Time       time.Time
	TPS        float64
	Requests   int64
	Errors     int64
	AvgLatency float64
}

// LatencyBucket represents a latency distribution bucket
type LatencyBucket struct {
	Label string
	Count int64
}

// ReportData holds all data for the final report
type ReportData struct {
	// Overall stats
	TotalRequests   int64
	TotalErrors     int64
	TotalDuration   time.Duration
	AvgTPS          float64
	PeakTPS         float64
	MinLatency      float64
	MaxLatency      float64
	AvgLatency      float64
	P50Latency      float64
	P95Latency      float64
	P99Latency      float64
	SuccessRate     float64

	// Time series data (for graph)
	TimeSlots []TimeSlot

	// Latency distribution
	LatencyDist []LatencyBucket

	// Status code distribution
	StatusCodes map[int]int64
}

// Model is the main TUI model
type Model struct {
	screen       Screen
	width        int
	height       int
	cursor       int
	inputs       []textinput.Model
	focusIndex   int
	err          error
	spinnerFrame int
	triggered    bool
	startTime    time.Time

	// Configuration state
	TargetURL      string
	TargetMethod   string
	Protocol       string
	BaseTPS        string
	MaxTPS         string
	PoissonLambda  string
	SpikeInterval  string // Alternative to lambda: "30s", "5m", "2h"
	SpikeFactor    string
	NoiseAmp       string
	Schedule       string

	// Runtime state
	CurrentTPS   float64
	RequestsSent int64
	ErrorCount   int64
	AvgLatency   float64
	IsSpiking    bool

	// Stats collection for report
	latencies     []float64
	peakTPS       float64
	timeSlots     []TimeSlot
	lastSlotTime  time.Time
	slotRequests  int64
	slotErrors    int64
	slotLatencies []float64
	statusCodes   map[int]int64

	// For event logging
	lastSpiking    bool
	lastLoggedTPS  float64
	lastErrorCount int64
	loggedStart    bool

	// Manual spike state
	ManualSpiking       bool
	ManualSpikeFactor   float64
	ManualSpikeEndTime  time.Time

	// Final report data
	Report ReportData
}

// NewModel creates a new TUI model
func NewModel() Model {
	m := Model{
		screen:        ScreenWelcome,
		TargetMethod:  "GET",
		Protocol:      "http",
		BaseTPS:       "100",
		MaxTPS:        "1000",
		PoissonLambda: "",
		SpikeInterval: "10s",
		SpikeFactor:   "3.0",
		NoiseAmp:      "0.15",
		statusCodes:   make(map[int]int64),
		latencies:     make([]float64, 0),
		timeSlots:     make([]TimeSlot, 0),
		slotLatencies: make([]float64, 0),
	}

	// Create text inputs (10 total)
	m.inputs = make([]textinput.Model, 10)

	// Target URL [0]
	m.inputs[0] = textinput.New()
	m.inputs[0].Placeholder = "http://localhost:8080/api/health"
	m.inputs[0].Focus()
	m.inputs[0].CharLimit = 256
	m.inputs[0].Width = 50

	// Method [1]
	m.inputs[1] = textinput.New()
	m.inputs[1].Placeholder = "GET"
	m.inputs[1].SetValue("GET")
	m.inputs[1].CharLimit = 10
	m.inputs[1].Width = 10

	// Protocol [2]
	m.inputs[2] = textinput.New()
	m.inputs[2].Placeholder = "http"
	m.inputs[2].SetValue("http")
	m.inputs[2].CharLimit = 10
	m.inputs[2].Width = 10

	// Base TPS [3]
	m.inputs[3] = textinput.New()
	m.inputs[3].Placeholder = "100"
	m.inputs[3].SetValue("100")
	m.inputs[3].CharLimit = 10
	m.inputs[3].Width = 10

	// Max TPS [4]
	m.inputs[4] = textinput.New()
	m.inputs[4].Placeholder = "1000"
	m.inputs[4].SetValue("1000")
	m.inputs[4].CharLimit = 10
	m.inputs[4].Width = 10

	// Spike Interval [5] - new intuitive field
	m.inputs[5] = textinput.New()
	m.inputs[5].Placeholder = "10s"
	m.inputs[5].SetValue("10s")
	m.inputs[5].CharLimit = 10
	m.inputs[5].Width = 10

	// Spike Factor [6]
	m.inputs[6] = textinput.New()
	m.inputs[6].Placeholder = "3.0"
	m.inputs[6].SetValue("3.0")
	m.inputs[6].CharLimit = 10
	m.inputs[6].Width = 10

	// Noise Amplitude [7]
	m.inputs[7] = textinput.New()
	m.inputs[7].Placeholder = "0.15"
	m.inputs[7].SetValue("0.15")
	m.inputs[7].CharLimit = 10
	m.inputs[7].Width = 10

	// Schedule [8]
	m.inputs[8] = textinput.New()
	m.inputs[8].Placeholder = "9-17:1.5, 0-5:0.3"
	m.inputs[8].CharLimit = 100
	m.inputs[8].Width = 30

	// Poisson Lambda [9] - kept for advanced users (optional)
	m.inputs[9] = textinput.New()
	m.inputs[9].Placeholder = "(optional, overrides interval)"
	m.inputs[9].CharLimit = 10
	m.inputs[9].Width = 20

	return m
}

// Init initializes the model
func (m Model) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, tickCmd())
}

// tickMsg is sent every second
type tickMsg time.Time

// StopMsg is sent when kar stop is called
type StopMsg struct{}

// SpikeMsg is sent when kar spike is called
type SpikeMsg struct {
	Factor   float64
	Duration time.Duration
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Millisecond*100, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// Update handles messages
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			// On Running screen, show report first
			if m.screen == ScreenRunning {
				Log("EVENT: Traffic generation stopped by user")
				Log("SUMMARY: Duration=%s Requests=%d Errors=%d PeakTPS=%.0f",
					time.Since(m.startTime).Round(time.Second), m.RequestsSent, m.ErrorCount, m.peakTPS)
				m.generateReport()
				m.screen = ScreenReport
				return m, nil
			}
			// On Report screen, exit
			if m.screen == ScreenReport {
				return m, tea.Quit
			}
			// On other screens, just exit
			return m, tea.Quit

		case "enter":
			return m.handleEnter()

		case "tab", "down":
			return m.handleNext()

		case "shift+tab", "up":
			return m.handlePrev()

		case "esc":
			if m.screen > ScreenWelcome {
				m.screen--
				return m, nil
			}
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tickMsg:
		m.spinnerFrame = (m.spinnerFrame + 1) % len(SpinnerFrames)
		// Only update stats on Running screen, not Report screen
		if m.triggered && m.screen == ScreenRunning {
			m.updateRunningStats()
		}
		return m, tickCmd()

	case StopMsg:
		// Handle kar stop command
		if m.screen == ScreenRunning {
			Log("EVENT: Traffic generation stopped by 'kar stop' command")
			Log("SUMMARY: Duration=%s Requests=%d Errors=%d PeakTPS=%.0f",
				time.Since(m.startTime).Round(time.Second), m.RequestsSent, m.ErrorCount, m.peakTPS)
			m.generateReport()
			m.screen = ScreenReport
			return m, nil
		}
		return m, tea.Quit

	case SpikeMsg:
		// Handle manual spike command
		if m.screen == ScreenRunning {
			factor := msg.Factor
			if factor == 0 {
				factor = 3.0 // default spike factor
			}
			duration := msg.Duration
			if duration == 0 {
				duration = 15 * time.Second // default duration
			}

			m.ManualSpiking = true
			m.ManualSpikeFactor = factor
			m.ManualSpikeEndTime = time.Now().Add(duration)

			Log("EVENT: MANUAL SPIKE triggered (factor=%.1fx, duration=%s)", factor, duration)
		}
		return m, nil
	}

	// Handle text input
	if m.screen == ScreenTargetSetup || m.screen == ScreenTrafficConfig || m.screen == ScreenPatternConfig {
		cmd := m.updateInputs(msg)
		return m, cmd
	}

	return m, nil
}

func (m *Model) handleEnter() (tea.Model, tea.Cmd) {
	switch m.screen {
	case ScreenWelcome:
		m.screen = ScreenTargetSetup
		m.focusIndex = 0
		m.inputs[0].Focus()
	case ScreenTargetSetup:
		m.TargetURL = m.inputs[0].Value()
		m.TargetMethod = m.inputs[1].Value()
		m.Protocol = m.inputs[2].Value()
		m.screen = ScreenTrafficConfig
		m.focusIndex = 0
	case ScreenTrafficConfig:
		m.BaseTPS = m.inputs[3].Value()
		m.MaxTPS = m.inputs[4].Value()
		m.screen = ScreenPatternConfig
		m.focusIndex = 0
	case ScreenPatternConfig:
		m.SpikeInterval = m.inputs[5].Value()
		m.SpikeFactor = m.inputs[6].Value()
		m.NoiseAmp = m.inputs[7].Value()
		m.Schedule = m.inputs[8].Value()
		m.PoissonLambda = m.inputs[9].Value() // optional override
		m.screen = ScreenReview
		m.cursor = 0
	case ScreenReview:
		if m.cursor == 0 { // Fire!
			m.screen = ScreenRunning
			m.triggered = true
			m.startTime = time.Now()
		} else { // Back
			m.screen = ScreenTargetSetup
		}
	case ScreenRunning:
		if !m.triggered {
			m.triggered = true
			m.startTime = time.Now()
		}
	}
	return m, nil
}

func (m *Model) handleNext() (tea.Model, tea.Cmd) {
	switch m.screen {
	case ScreenTargetSetup:
		m.inputs[m.focusIndex].Blur()
		m.focusIndex = (m.focusIndex + 1) % 3
		m.inputs[m.focusIndex].Focus()
	case ScreenTrafficConfig:
		m.inputs[3+m.focusIndex].Blur()
		m.focusIndex = (m.focusIndex + 1) % 2
		m.inputs[3+m.focusIndex].Focus()
	case ScreenPatternConfig:
		m.inputs[5+m.focusIndex].Blur()
		m.focusIndex = (m.focusIndex + 1) % 5 // 5 fields now
		m.inputs[5+m.focusIndex].Focus()
	case ScreenReview:
		m.cursor = (m.cursor + 1) % 2
	}
	return m, nil
}

func (m *Model) handlePrev() (tea.Model, tea.Cmd) {
	switch m.screen {
	case ScreenTargetSetup:
		m.inputs[m.focusIndex].Blur()
		m.focusIndex = (m.focusIndex - 1 + 3) % 3
		m.inputs[m.focusIndex].Focus()
	case ScreenTrafficConfig:
		m.inputs[3+m.focusIndex].Blur()
		m.focusIndex = (m.focusIndex - 1 + 2) % 2
		m.inputs[3+m.focusIndex].Focus()
	case ScreenPatternConfig:
		m.inputs[5+m.focusIndex].Blur()
		m.focusIndex = (m.focusIndex - 1 + 5) % 5 // 5 fields now
		m.inputs[5+m.focusIndex].Focus()
	case ScreenReview:
		m.cursor = (m.cursor - 1 + 2) % 2
	}
	return m, nil
}

func (m *Model) updateInputs(msg tea.Msg) tea.Cmd {
	var cmds []tea.Cmd

	for i := range m.inputs {
		var cmd tea.Cmd
		m.inputs[i], cmd = m.inputs[i].Update(msg)
		cmds = append(cmds, cmd)
	}

	return tea.Batch(cmds...)
}

func (m *Model) updateRunningStats() {
	elapsed := time.Since(m.startTime).Seconds()

	// Log start event once
	if !m.loggedStart {
		m.loggedStart = true
		targetURL := m.TargetURL
		if targetURL == "" {
			targetURL = m.inputs[0].Placeholder
		}
		Log("EVENT: Traffic generation started")
		Log("CONFIG: Target=%s Method=%s Protocol=%s", targetURL, m.TargetMethod, m.Protocol)
		Log("CONFIG: BaseTPS=%s MaxTPS=%s Lambda=%s SpikeFactor=%s Noise=%s",
			m.BaseTPS, m.MaxTPS, m.PoissonLambda, m.SpikeFactor, m.NoiseAmp)
	}

	// Base TPS with small noise (±15%)
	baseTPS := 100.0
	noiseAmp := 0.15
	noise := (float64(m.spinnerFrame%20) - 10) / 10 * noiseAmp // -0.15 ~ +0.15
	m.CurrentTPS = baseTPS * (1 + noise)                       // ~85 ~ 115

	// Check if manual spike has ended
	if m.ManualSpiking && time.Now().After(m.ManualSpikeEndTime) {
		m.ManualSpiking = false
		Log("EVENT: MANUAL SPIKE END - TPS returning to normal")
	}

	// Spike: manual spike takes priority, otherwise ~6% chance for auto spike
	if m.ManualSpiking {
		m.IsSpiking = true
		m.CurrentTPS *= m.ManualSpikeFactor
	} else {
		m.IsSpiking = m.spinnerFrame%50 < 3
		if m.IsSpiking {
			m.CurrentTPS *= 3.0 // ~255 ~ 345 during spike
		}
	}

	m.RequestsSent = int64(elapsed * baseTPS)
	m.ErrorCount = int64(elapsed * 0.5)
	m.AvgLatency = 15 + float64(m.spinnerFrame%5)

	// Track peak TPS and log new peak
	if m.CurrentTPS > m.peakTPS {
		m.peakTPS = m.CurrentTPS
		Log("EVENT: New peak TPS=%.0f", m.peakTPS)
	}

	// Log spike start/end
	if m.IsSpiking && !m.lastSpiking {
		Log("EVENT: SPIKE START - TPS=%.0f (%.1fx base)", m.CurrentTPS, m.CurrentTPS/baseTPS)
	}
	if !m.IsSpiking && m.lastSpiking {
		Log("EVENT: SPIKE END - TPS returning to %.0f", m.CurrentTPS)
	}
	m.lastSpiking = m.IsSpiking

	// Log significant TPS changes (>20%)
	if m.lastLoggedTPS > 0 {
		change := (m.CurrentTPS - m.lastLoggedTPS) / m.lastLoggedTPS
		if change > 0.2 || change < -0.2 {
			Log("TPS: %.0f -> %.0f (%+.0f%%)", m.lastLoggedTPS, m.CurrentTPS, change*100)
		}
	}
	m.lastLoggedTPS = m.CurrentTPS

	// Log error increases
	newErrors := m.ErrorCount - m.lastErrorCount
	if newErrors > 5 {
		Log("WARNING: %d new errors (total: %d)", newErrors, m.ErrorCount)
	}
	m.lastErrorCount = m.ErrorCount

	// Periodic status log (every 10 seconds)
	if int(elapsed)%10 == 0 && int(elapsed) > 0 && m.spinnerFrame%10 == 0 {
		Log("STATUS: TPS=%.0f Requests=%d Errors=%d Latency=%.1fms",
			m.CurrentTPS, m.RequestsSent, m.ErrorCount, m.AvgLatency)
	}

	// Simulate latency collection (in real impl, this comes from actual requests)
	simulatedLatency := m.AvgLatency + float64(m.spinnerFrame%10) - 5
	m.latencies = append(m.latencies, simulatedLatency)
	m.slotLatencies = append(m.slotLatencies, simulatedLatency)

	// Simulate status codes
	if m.spinnerFrame%100 == 0 {
		m.statusCodes[500]++ // ~1% server error
	} else if m.spinnerFrame%50 == 0 {
		m.statusCodes[429]++ // ~2% rate limit
	} else {
		m.statusCodes[200]++
	}

	// Collect time slot data every 5 seconds
	now := time.Now()
	if m.lastSlotTime.IsZero() {
		m.lastSlotTime = now
	}

	if now.Sub(m.lastSlotTime) >= 5*time.Second {
		// Calculate slot stats
		slotAvgLatency := 0.0
		if len(m.slotLatencies) > 0 {
			sum := 0.0
			for _, l := range m.slotLatencies {
				sum += l
			}
			slotAvgLatency = sum / float64(len(m.slotLatencies))
		}

		slot := TimeSlot{
			Time:       now,
			TPS:        m.CurrentTPS,
			Requests:   m.RequestsSent - m.slotRequests,
			Errors:     m.ErrorCount - m.slotErrors,
			AvgLatency: slotAvgLatency,
		}
		m.timeSlots = append(m.timeSlots, slot)

		// Reset slot counters
		m.lastSlotTime = now
		m.slotRequests = m.RequestsSent
		m.slotErrors = m.ErrorCount
		m.slotLatencies = make([]float64, 0)
	}
}

// View renders the TUI
func (m Model) View() string {
	switch m.screen {
	case ScreenWelcome:
		return m.viewWelcome()
	case ScreenTargetSetup:
		return m.viewTargetSetup()
	case ScreenTrafficConfig:
		return m.viewTrafficConfig()
	case ScreenPatternConfig:
		return m.viewPatternConfig()
	case ScreenReview:
		return m.viewReview()
	case ScreenRunning:
		return m.viewRunning()
	case ScreenReport:
		return m.viewReport()
	default:
		return ""
	}
}

func (m Model) viewWelcome() string {
	var b strings.Builder

	b.WriteString("\n\n")
	b.WriteString(Logo())
	b.WriteString("\n\n")
	b.WriteString(Tagline())
	b.WriteString("\n\n\n")

	content := lipgloss.JoinVertical(lipgloss.Center,
		HighlightStyle.Render("Welcome, Operator."),
		"",
		DimStyle.Render("kar98k is ready to generate high-intensity"),
		DimStyle.Render("irregular traffic patterns for your targets."),
		"",
		"",
		ActiveButtonStyle.Render(" "+Crosshair+" START CONFIGURATION "),
		"",
		"",
		HelpStyle.Render("Press ENTER to begin • Press Q to quit"),
	)

	box := BorderStyle.Width(60).Render(content)
	b.WriteString(lipgloss.Place(m.width, m.height-10, lipgloss.Center, lipgloss.Center, box))

	return b.String()
}

func (m Model) viewTargetSetup() string {
	var b strings.Builder

	b.WriteString("\n")
	b.WriteString(m.renderHeader("TARGET CONFIGURATION", "1/4"))
	b.WriteString("\n\n")

	content := lipgloss.JoinVertical(lipgloss.Left,
		LabelStyle.Render("Target URL"),
		m.renderInput(0, m.focusIndex == 0),
		DimStyle.Render("  The endpoint to test. Include full path."),
		DimStyle.Render("  ex) http://localhost:8080/api/health"),
		"",
		LabelStyle.Render("HTTP Method"),
		m.renderInput(1, m.focusIndex == 1),
		DimStyle.Render("  GET: read data, POST: create, PUT: update, DELETE: remove"),
		"",
		LabelStyle.Render("Protocol"),
		m.renderInput(2, m.focusIndex == 2),
		DimStyle.Render("  http: HTTP/1.1, http2: HTTP/2, grpc: gRPC protocol"),
	)

	box := BorderStyle.Width(65).Render(content)
	b.WriteString(lipgloss.Place(m.width, m.height-15, lipgloss.Center, lipgloss.Top, box))

	b.WriteString("\n\n")
	b.WriteString(lipgloss.Place(m.width, 0, lipgloss.Center, lipgloss.Top,
		HelpStyle.Render("TAB: next field • ENTER: continue • ESC: back")))

	return b.String()
}

func (m Model) viewTrafficConfig() string {
	var b strings.Builder

	b.WriteString("\n")
	b.WriteString(m.renderHeader("TRAFFIC CONFIGURATION", "2/4"))
	b.WriteString("\n\n")

	content := lipgloss.JoinVertical(lipgloss.Left,
		LabelStyle.Render("Base TPS (Transactions Per Second)"),
		m.renderInput(3, m.focusIndex == 0),
		DimStyle.Render("  Normal traffic rate. This is your baseline load."),
		DimStyle.Render("  ex) 100 = 100 requests/sec (6,000 req/min)"),
		DimStyle.Render("  ex) 500 = 500 requests/sec (30,000 req/min)"),
		"",
		LabelStyle.Render("Max TPS"),
		m.renderInput(4, m.focusIndex == 1),
		DimStyle.Render("  Upper limit during spike events."),
		DimStyle.Render("  ex) Base=100, Max=1000 -> spikes can reach 10x"),
		DimStyle.Render("  ex) Base=100, Max=300  -> spikes capped at 3x"),
	)

	box := BorderStyle.Width(65).Render(content)
	b.WriteString(lipgloss.Place(m.width, m.height-15, lipgloss.Center, lipgloss.Top, box))

	b.WriteString("\n\n")
	b.WriteString(lipgloss.Place(m.width, 0, lipgloss.Center, lipgloss.Top,
		HelpStyle.Render("TAB: next field • ENTER: continue • ESC: back")))

	return b.String()
}

func (m Model) viewPatternConfig() string {
	var b strings.Builder

	b.WriteString("\n")
	b.WriteString(m.renderHeader("PATTERN CONFIGURATION", "3/4"))
	b.WriteString("\n\n")

	content := lipgloss.JoinVertical(lipgloss.Left,
		LabelStyle.Render("Spike Interval (time between spikes)"),
		m.renderInput(5, m.focusIndex == 0),
		DimStyle.Render("  How often spikes occur. Use Go duration format."),
		DimStyle.Render("  ex) 10s = spike every ~10 seconds"),
		DimStyle.Render("  ex) 5m  = spike every ~5 minutes"),
		DimStyle.Render("  ex) 2h  = spike every ~2 hours"),
		"",
		LabelStyle.Render("Spike Factor (TPS multiplier)"),
		m.renderInput(6, m.focusIndex == 1),
		DimStyle.Render("  TPS multiplier when spike occurs."),
		DimStyle.Render("  ex) 2.0 = 2x during spike (100 -> 200 TPS)"),
		DimStyle.Render("  ex) 5.0 = 5x during spike (100 -> 500 TPS)"),
		"",
		LabelStyle.Render("Noise Amplitude"),
		m.renderInput(7, m.focusIndex == 2),
		DimStyle.Render("  Random fluctuation around base TPS."),
		DimStyle.Render("  ex) 0.1  = +/-10% (90~110 when base=100)"),
		"",
		LabelStyle.Render("Schedule (optional)"),
		m.renderInput(8, m.focusIndex == 3),
		DimStyle.Render("  Time-based TPS multiplier. Format: hour-hour:factor"),
		DimStyle.Render("  ex) 9-18:1.5  = 1.5x during 9AM-6PM"),
		"",
		LabelStyle.Render("Lambda (advanced, optional)"),
		m.renderInput(9, m.focusIndex == 4),
		DimStyle.Render("  Overrides interval. Events per second."),
		DimStyle.Render("  ex) 0.001 = spike every ~1000 sec (~17 min)"),
	)

	box := BorderStyle.Width(65).Render(content)
	b.WriteString(lipgloss.Place(m.width, m.height-26, lipgloss.Center, lipgloss.Top, box))

	b.WriteString("\n\n")
	b.WriteString(lipgloss.Place(m.width, 0, lipgloss.Center, lipgloss.Top,
		HelpStyle.Render("TAB: next field • ENTER: continue • ESC: back")))

	return b.String()
}

func (m Model) viewReview() string {
	var b strings.Builder

	b.WriteString("\n")
	b.WriteString(m.renderHeader("REVIEW & FIRE", "4/4"))
	b.WriteString("\n\n")

	targetURL := m.TargetURL
	if targetURL == "" {
		targetURL = m.inputs[0].Placeholder
	}

	// Show interval or lambda depending on what's set
	intervalStr := m.SpikeInterval
	if m.PoissonLambda != "" {
		intervalStr = "λ=" + m.PoissonLambda
	}

	configSummary := lipgloss.JoinVertical(lipgloss.Left,
		SubtitleStyle.Render("Target"),
		fmt.Sprintf("  %s %s %s", LabelStyle.Render("URL:"), ValueStyle.Render(targetURL), ""),
		fmt.Sprintf("  %s %s  %s %s", LabelStyle.Render("Method:"), ValueStyle.Render(m.TargetMethod), LabelStyle.Render("Protocol:"), ValueStyle.Render(m.Protocol)),
		"",
		SubtitleStyle.Render("Traffic"),
		fmt.Sprintf("  %s %s TPS  %s %s TPS", LabelStyle.Render("Base:"), ValueStyle.Render(m.BaseTPS), LabelStyle.Render("Max:"), ValueStyle.Render(m.MaxTPS)),
		"",
		SubtitleStyle.Render("Pattern"),
		fmt.Sprintf("  %s %s  %s %sx", LabelStyle.Render("Interval:"), ValueStyle.Render(intervalStr), LabelStyle.Render("Spike:"), ValueStyle.Render(m.SpikeFactor)),
		fmt.Sprintf("  %s ±%s%%", LabelStyle.Render("Noise:"), ValueStyle.Render(m.NoiseAmp)),
	)

	box := BorderStyle.Width(60).Render(configSummary)
	b.WriteString(lipgloss.Place(m.width, 0, lipgloss.Center, lipgloss.Top, box))

	b.WriteString("\n\n")

	// Fire button
	var fireBtn, backBtn string
	if m.cursor == 0 {
		fireBtn = ActiveButtonStyle.Render(" " + Crosshair + " PULL TRIGGER ")
		backBtn = ButtonStyle.Render(" ← BACK ")
	} else {
		fireBtn = ButtonStyle.Render(" " + TriggerReady + " PULL TRIGGER ")
		backBtn = ActiveButtonStyle.Render(" ← BACK ")
	}

	buttons := lipgloss.JoinHorizontal(lipgloss.Center, fireBtn, "  ", backBtn)
	b.WriteString(lipgloss.Place(m.width, 0, lipgloss.Center, lipgloss.Top, buttons))

	b.WriteString("\n\n\n")
	b.WriteString(lipgloss.Place(m.width, 0, lipgloss.Center, lipgloss.Top,
		HelpStyle.Render("TAB: switch button • ENTER: select • ESC: back")))

	return b.String()
}

func (m Model) viewRunning() string {
	var b strings.Builder

	b.WriteString("\n")

	// Header with status
	var statusIcon, statusText string
	if m.triggered {
		statusIcon = SuccessStyle.Render(TriggerPulled)
		statusText = SuccessStyle.Render("FIRING")
	} else {
		statusIcon = WarningStyle.Render(TriggerReady)
		statusText = WarningStyle.Render("PAUSED")
	}

	header := lipgloss.JoinHorizontal(lipgloss.Center,
		MiniLogo(),
		"  ",
		statusIcon,
		" ",
		statusText,
	)
	b.WriteString(lipgloss.Place(m.width, 0, lipgloss.Center, lipgloss.Top, header))
	b.WriteString("\n\n")

	// Stats
	elapsed := time.Since(m.startTime)
	if !m.triggered {
		elapsed = 0
	}

	// TPS gauge
	tpsPercent := m.CurrentTPS / 1000.0
	if tpsPercent > 1 {
		tpsPercent = 1
	}

	var spikeIndicator string
	if m.ManualSpiking {
		spikeIndicator = HighlightStyle.Render(fmt.Sprintf(" ◉ MANUAL SPIKE (%.1fx)", m.ManualSpikeFactor))
	} else if m.IsSpiking {
		spikeIndicator = WarningStyle.Render(" ⚡ SPIKE")
	}

	stats := lipgloss.JoinVertical(lipgloss.Left,
		SubtitleStyle.Render("Current TPS")+spikeIndicator,
		fmt.Sprintf("  %s %s", ValueStyle.Render(fmt.Sprintf("%.0f", m.CurrentTPS)), DimStyle.Render("/ 1000")),
		"  "+ProgressBar(tpsPercent, 40),
		"",
		lipgloss.JoinHorizontal(lipgloss.Top,
			lipgloss.JoinVertical(lipgloss.Left,
				LabelStyle.Render("Requests Sent"),
				ValueStyle.Render(fmt.Sprintf("  %d", m.RequestsSent)),
			),
			"    ",
			lipgloss.JoinVertical(lipgloss.Left,
				LabelStyle.Render("Errors"),
				ErrorStyle.Render(fmt.Sprintf("  %d", m.ErrorCount)),
			),
			"    ",
			lipgloss.JoinVertical(lipgloss.Left,
				LabelStyle.Render("Avg Latency"),
				ValueStyle.Render(fmt.Sprintf("  %.1fms", m.AvgLatency)),
			),
		),
		"",
		LabelStyle.Render("Elapsed Time"),
		ValueStyle.Render(fmt.Sprintf("  %s", elapsed.Round(time.Second))),
	)

	targetURL := m.TargetURL
	if targetURL == "" {
		targetURL = m.inputs[0].Placeholder
	}

	targetInfo := lipgloss.JoinVertical(lipgloss.Left,
		SubtitleStyle.Render("Target"),
		DimStyle.Render(fmt.Sprintf("  %s %s", m.TargetMethod, targetURL)),
	)

	content := lipgloss.JoinVertical(lipgloss.Left, stats, "", Divider(50), "", targetInfo)
	box := ActiveBorderStyle.Width(60).Render(content)

	b.WriteString(lipgloss.Place(m.width, 0, lipgloss.Center, lipgloss.Top, box))

	// Live indicator
	b.WriteString("\n\n")
	spinner := InfoStyle.Render(SpinnerFrames[m.spinnerFrame])
	if m.triggered {
		b.WriteString(lipgloss.Place(m.width, 0, lipgloss.Center, lipgloss.Top,
			spinner+" "+DimStyle.Render("Traffic flowing...")))
	} else {
		b.WriteString(lipgloss.Place(m.width, 0, lipgloss.Center, lipgloss.Top,
			DimStyle.Render("Press ENTER to resume")))
	}

	b.WriteString("\n\n")
	b.WriteString(lipgloss.Place(m.width, 0, lipgloss.Center, lipgloss.Top,
		HelpStyle.Render("ENTER: pause/resume • Q: stop and exit")))

	return b.String()
}

func (m Model) renderHeader(title, step string) string {
	header := lipgloss.JoinHorizontal(lipgloss.Center,
		MiniLogo(),
		"  ",
		TitleStyle.Render(" "+title+" "),
		"  ",
		DimStyle.Render("Step "+step),
	)
	return lipgloss.Place(m.width, 0, lipgloss.Center, lipgloss.Top, header)
}

func (m Model) renderInput(index int, focused bool) string {
	if focused {
		return ActiveBorderStyle.Padding(0, 1).Render(m.inputs[index].View())
	}
	return BorderStyle.Padding(0, 1).Render(m.inputs[index].View())
}

// GetConfig returns the current configuration from the TUI
func (m Model) GetConfig() map[string]string {
	targetURL := m.TargetURL
	if targetURL == "" {
		targetURL = m.inputs[0].Placeholder
	}

	return map[string]string{
		"target_url":     targetURL,
		"target_method":  m.TargetMethod,
		"protocol":       m.Protocol,
		"base_tps":       m.BaseTPS,
		"max_tps":        m.MaxTPS,
		"spike_interval": m.SpikeInterval,
		"poisson_lambda": m.PoissonLambda,
		"spike_factor":   m.SpikeFactor,
		"noise_amp":      m.NoiseAmp,
		"schedule":       m.Schedule,
	}
}

// generateReport calculates final report statistics
func (m *Model) generateReport() {
	r := &m.Report

	r.TotalRequests = m.RequestsSent
	r.TotalErrors = m.ErrorCount
	r.TotalDuration = time.Since(m.startTime)
	r.PeakTPS = m.peakTPS
	r.TimeSlots = m.timeSlots
	r.StatusCodes = m.statusCodes

	// Calculate average TPS
	if r.TotalDuration.Seconds() > 0 {
		r.AvgTPS = float64(r.TotalRequests) / r.TotalDuration.Seconds()
	}

	// Calculate success rate
	if r.TotalRequests > 0 {
		r.SuccessRate = float64(r.TotalRequests-r.TotalErrors) / float64(r.TotalRequests) * 100
	}

	// Calculate latency stats
	if len(m.latencies) > 0 {
		sorted := make([]float64, len(m.latencies))
		copy(sorted, m.latencies)
		sortFloat64s(sorted)

		r.MinLatency = sorted[0]
		r.MaxLatency = sorted[len(sorted)-1]

		// Average
		sum := 0.0
		for _, l := range sorted {
			sum += l
		}
		r.AvgLatency = sum / float64(len(sorted))

		// Percentiles
		r.P50Latency = percentile(sorted, 50)
		r.P95Latency = percentile(sorted, 95)
		r.P99Latency = percentile(sorted, 99)

		// Latency distribution buckets
		r.LatencyDist = calculateLatencyDist(sorted)
	}
}

// sortFloat64s sorts a slice of float64 in ascending order
func sortFloat64s(arr []float64) {
	for i := 0; i < len(arr); i++ {
		for j := i + 1; j < len(arr); j++ {
			if arr[j] < arr[i] {
				arr[i], arr[j] = arr[j], arr[i]
			}
		}
	}
}

// percentile calculates the p-th percentile of sorted data
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	index := int(float64(len(sorted)-1) * p / 100)
	return sorted[index]
}

// calculateLatencyDist creates latency distribution buckets
func calculateLatencyDist(sorted []float64) []LatencyBucket {
	buckets := []LatencyBucket{
		{Label: "<10ms", Count: 0},
		{Label: "10-25ms", Count: 0},
		{Label: "25-50ms", Count: 0},
		{Label: "50-100ms", Count: 0},
		{Label: "100-250ms", Count: 0},
		{Label: ">250ms", Count: 0},
	}

	for _, l := range sorted {
		switch {
		case l < 10:
			buckets[0].Count++
		case l < 25:
			buckets[1].Count++
		case l < 50:
			buckets[2].Count++
		case l < 100:
			buckets[3].Count++
		case l < 250:
			buckets[4].Count++
		default:
			buckets[5].Count++
		}
	}

	return buckets
}

// viewReport renders the final report screen
func (m Model) viewReport() string {
	var b strings.Builder
	r := m.Report

	b.WriteString("\n")

	// Header
	header := lipgloss.JoinHorizontal(lipgloss.Center,
		MiniLogo(),
		"  ",
		TitleStyle.Render(" TEST REPORT "),
		"  ",
		SuccessStyle.Render(CheckMark+" COMPLETED"),
	)
	b.WriteString(lipgloss.Place(m.width, 0, lipgloss.Center, lipgloss.Top, header))
	b.WriteString("\n\n")

	// Overview section
	overview := lipgloss.JoinVertical(lipgloss.Left,
		SubtitleStyle.Render("Overview"),
		"",
		fmt.Sprintf("  %s %s", LabelStyle.Render("Duration:"), ValueStyle.Render(r.TotalDuration.Round(time.Second).String())),
		fmt.Sprintf("  %s %s", LabelStyle.Render("Total Requests:"), ValueStyle.Render(fmt.Sprintf("%d", r.TotalRequests))),
		fmt.Sprintf("  %s %s", LabelStyle.Render("Success Rate:"), m.coloredSuccessRate(r.SuccessRate)),
		fmt.Sprintf("  %s %s / %s", LabelStyle.Render("TPS (avg/peak):"), ValueStyle.Render(fmt.Sprintf("%.1f", r.AvgTPS)), HighlightStyle.Render(fmt.Sprintf("%.1f", r.PeakTPS))),
	)

	// Latency section
	latency := lipgloss.JoinVertical(lipgloss.Left,
		SubtitleStyle.Render("Latency Distribution"),
		"",
		fmt.Sprintf("  %s %s", LabelStyle.Render("Min:"), ValueStyle.Render(fmt.Sprintf("%.2fms", r.MinLatency))),
		fmt.Sprintf("  %s %s", LabelStyle.Render("Avg:"), ValueStyle.Render(fmt.Sprintf("%.2fms", r.AvgLatency))),
		fmt.Sprintf("  %s %s", LabelStyle.Render("Max:"), WarningStyle.Render(fmt.Sprintf("%.2fms", r.MaxLatency))),
		"",
		fmt.Sprintf("  %s %s", LabelStyle.Render("P50:"), ValueStyle.Render(fmt.Sprintf("%.2fms", r.P50Latency))),
		fmt.Sprintf("  %s %s", LabelStyle.Render("P95:"), ValueStyle.Render(fmt.Sprintf("%.2fms", r.P95Latency))),
		fmt.Sprintf("  %s %s", LabelStyle.Render("P99:"), WarningStyle.Render(fmt.Sprintf("%.2fms", r.P99Latency))),
	)

	// Latency histogram
	histogram := m.renderLatencyHistogram(r.LatencyDist)

	// Status codes section
	statusSection := m.renderStatusCodes(r.StatusCodes)

	// Time series mini-chart
	timeChart := m.renderTimeChart(r.TimeSlots)

	// Layout
	leftCol := lipgloss.JoinVertical(lipgloss.Left, overview, "", Divider(30), "", latency)
	rightCol := lipgloss.JoinVertical(lipgloss.Left, histogram, "", statusSection)

	topSection := lipgloss.JoinHorizontal(lipgloss.Top,
		BorderStyle.Width(35).Render(leftCol),
		"  ",
		BorderStyle.Width(35).Render(rightCol),
	)

	b.WriteString(lipgloss.Place(m.width, 0, lipgloss.Center, lipgloss.Top, topSection))
	b.WriteString("\n\n")

	// Time chart (full width)
	if len(r.TimeSlots) > 0 {
		chartBox := BorderStyle.Width(72).Render(timeChart)
		b.WriteString(lipgloss.Place(m.width, 0, lipgloss.Center, lipgloss.Top, chartBox))
		b.WriteString("\n\n")
	}

	// Help
	b.WriteString(lipgloss.Place(m.width, 0, lipgloss.Center, lipgloss.Top,
		HelpStyle.Render("Press Q to exit")))

	return b.String()
}

// coloredSuccessRate returns success rate with appropriate color
func (m Model) coloredSuccessRate(rate float64) string {
	rateStr := fmt.Sprintf("%.2f%%", rate)
	switch {
	case rate >= 99:
		return SuccessStyle.Render(rateStr)
	case rate >= 95:
		return WarningStyle.Render(rateStr)
	default:
		return ErrorStyle.Render(rateStr)
	}
}

// renderLatencyHistogram renders a horizontal bar chart of latency distribution
func (m Model) renderLatencyHistogram(dist []LatencyBucket) string {
	if len(dist) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString(SubtitleStyle.Render("Latency Histogram"))
	b.WriteString("\n\n")

	// Find max for scaling
	maxCount := int64(1)
	for _, bucket := range dist {
		if bucket.Count > maxCount {
			maxCount = bucket.Count
		}
	}

	barWidth := 20
	for _, bucket := range dist {
		barLen := int(float64(bucket.Count) / float64(maxCount) * float64(barWidth))
		if barLen == 0 && bucket.Count > 0 {
			barLen = 1
		}

		bar := ""
		for i := 0; i < barLen; i++ {
			bar += "="
		}
		for i := barLen; i < barWidth; i++ {
			bar += "-"
		}

		b.WriteString(fmt.Sprintf("  %9s %s %d\n",
			LabelStyle.Render(bucket.Label),
			ProgressBarStyle.Render(bar[:barLen])+ProgressEmptyStyle.Render(bar[barLen:]),
			bucket.Count))
	}

	return b.String()
}

// renderStatusCodes renders status code distribution
func (m Model) renderStatusCodes(codes map[int]int64) string {
	if len(codes) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString(SubtitleStyle.Render("Status Codes"))
	b.WriteString("\n\n")

	// Common status codes to check
	checkCodes := []int{200, 201, 204, 400, 401, 403, 404, 429, 500, 502, 503}

	for _, code := range checkCodes {
		count, exists := codes[code]
		if !exists || count == 0 {
			continue
		}

		var style lipgloss.Style
		switch {
		case code >= 200 && code < 300:
			style = SuccessStyle
		case code >= 400 && code < 500:
			style = WarningStyle
		default:
			style = ErrorStyle
		}

		b.WriteString(fmt.Sprintf("  %s %s\n",
			style.Render(fmt.Sprintf("%d:", code)),
			ValueStyle.Render(fmt.Sprintf("%d", count))))
	}

	return b.String()
}

// renderTimeChart renders a time-series table with detailed stats
func (m Model) renderTimeChart(slots []TimeSlot) string {
	if len(slots) == 0 {
		return DimStyle.Render("No time series data collected (test was too short)")
	}

	var b strings.Builder
	b.WriteString(SubtitleStyle.Render("Timeline Summary (5s intervals)"))
	b.WriteString("\n\n")

	// Calculate stats
	var totalReqs, totalErrs int64
	var minTPS, maxTPS, sumTPS float64
	minTPS = slots[0].TPS
	maxTPS = slots[0].TPS

	for _, slot := range slots {
		totalReqs += slot.Requests
		totalErrs += slot.Errors
		sumTPS += slot.TPS
		if slot.TPS < minTPS {
			minTPS = slot.TPS
		}
		if slot.TPS > maxTPS {
			maxTPS = slot.TPS
		}
	}
	avgTPS := sumTPS / float64(len(slots))

	// Summary stats
	b.WriteString(fmt.Sprintf("  %s %.0f  %s %.0f  %s %.0f\n",
		LabelStyle.Render("Min TPS:"), minTPS,
		LabelStyle.Render("Avg TPS:"), avgTPS,
		LabelStyle.Render("Max TPS:"), maxTPS))
	b.WriteString("\n")

	// Table header
	b.WriteString(DimStyle.Render("  Time       TPS     Reqs    Errs   Latency\n"))
	b.WriteString(DimStyle.Render("  " + strings.Repeat("-", 48) + "\n"))

	// Show last 8 slots (most recent data)
	startIdx := 0
	if len(slots) > 8 {
		startIdx = len(slots) - 8
	}

	for i, slot := range slots[startIdx:] {
		timeStart := (startIdx + i) * 5
		timeEnd := timeStart + 5

		// Format time as MM:SS
		timeStr := fmt.Sprintf("%02d:%02d-%02d:%02d",
			timeStart/60, timeStart%60,
			timeEnd/60, timeEnd%60)

		// Spike indicator
		spikeMarker := " "
		if slot.TPS > avgTPS*1.5 {
			spikeMarker = WarningStyle.Render("*")
		}

		// Error highlight
		errStr := fmt.Sprintf("%d", slot.Errors)
		if slot.Errors > 0 {
			errStr = ErrorStyle.Render(errStr)
		}

		b.WriteString(fmt.Sprintf("  %s %s%6.0f  %6d  %6s  %6.1fms\n",
			DimStyle.Render(timeStr),
			spikeMarker,
			slot.TPS,
			slot.Requests,
			errStr,
			slot.AvgLatency))
	}

	if startIdx > 0 {
		b.WriteString(DimStyle.Render(fmt.Sprintf("\n  ... and %d earlier intervals\n", startIdx)))
	}

	b.WriteString("\n")
	b.WriteString(DimStyle.Render("  * = spike detected (>1.5x avg TPS)"))

	return b.String()
}
