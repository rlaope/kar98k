package tui

import (
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// Discovery screen states
const (
	ScreenDiscoverSetup Screen = iota + 100
	ScreenDiscoverRunning
	ScreenDiscoverResult
)

// DiscoverModel is the TUI model for the discovery feature.
type DiscoverModel struct {
	screen       Screen
	width        int
	height       int
	inputs       []textinput.Model
	focusIndex   int
	err          error
	spinnerFrame int
	startTime    time.Time

	// Configuration from inputs
	TargetURL      string
	Method         string
	Protocol       string
	LatencyLimit   string // in ms
	ErrorLimit     string // percentage
	MinTPS         string
	MaxTPS         string

	// Runtime state (updated during discovery)
	CurrentTPS    float64
	P95Latency    float64
	ErrorRate     float64
	LowRange      float64
	HighRange     float64
	Progress      float64
	StatusMessage string
	IsSearching   bool

	// Result
	ResultReady    bool
	SustainedTPS   float64
	BreakingTPS    float64
	FinalP95       float64
	FinalErrorRate float64
	TestDuration   time.Duration
	StepsCompleted int
	RecBaseTPS     float64
	RecMaxTPS      float64
	RecDescription string
}

// DiscoverProgressMsg is sent to update discovery progress.
type DiscoverProgressMsg struct {
	Progress   float64
	CurrentTPS float64
	P95Latency float64
	ErrorRate  float64
	LowRange   float64
	HighRange  float64
	Status     string
}

// DiscoverCompleteMsg is sent when discovery completes.
type DiscoverCompleteMsg struct {
	SustainedTPS   float64
	BreakingTPS    float64
	P95Latency     float64
	ErrorRate      float64
	TestDuration   time.Duration
	StepsCompleted int
	RecBaseTPS     float64
	RecMaxTPS      float64
	RecDescription string
}

// DiscoverStopMsg is sent to stop discovery.
type DiscoverStopMsg struct{}

// NewDiscoverModel creates a new discovery TUI model.
func NewDiscoverModel() DiscoverModel {
	m := DiscoverModel{
		screen:       ScreenDiscoverSetup,
		Method:       "GET",
		Protocol:     "http",
		LatencyLimit: "500",
		ErrorLimit:   "5",
		MinTPS:       "10",
		MaxTPS:       "10000",
	}

	// Create text inputs (7 total for discovery)
	m.inputs = make([]textinput.Model, 7)

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

	// Latency Limit [3]
	m.inputs[3] = textinput.New()
	m.inputs[3].Placeholder = "500"
	m.inputs[3].SetValue("500")
	m.inputs[3].CharLimit = 10
	m.inputs[3].Width = 10

	// Error Limit [4]
	m.inputs[4] = textinput.New()
	m.inputs[4].Placeholder = "5"
	m.inputs[4].SetValue("5")
	m.inputs[4].CharLimit = 10
	m.inputs[4].Width = 10

	// Min TPS [5]
	m.inputs[5] = textinput.New()
	m.inputs[5].Placeholder = "10"
	m.inputs[5].SetValue("10")
	m.inputs[5].CharLimit = 10
	m.inputs[5].Width = 10

	// Max TPS [6]
	m.inputs[6] = textinput.New()
	m.inputs[6].Placeholder = "10000"
	m.inputs[6].SetValue("10000")
	m.inputs[6].CharLimit = 10
	m.inputs[6].Width = 10

	return m
}

// Init initializes the discover model.
func (m DiscoverModel) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, discoverTickCmd())
}

func discoverTickCmd() tea.Cmd {
	return tea.Tick(time.Millisecond*100, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// Update handles messages for the discover model.
func (m DiscoverModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			if m.screen == ScreenDiscoverRunning {
				// Stop discovery and show results
				m.screen = ScreenDiscoverResult
				return m, nil
			}
			if m.screen == ScreenDiscoverResult {
				return m, tea.Quit
			}
			return m, tea.Quit

		case "enter":
			return m.handleEnter()

		case "tab", "down":
			return m.handleNext()

		case "shift+tab", "up":
			return m.handlePrev()

		case "esc":
			if m.screen == ScreenDiscoverSetup {
				return m, tea.Quit
			}
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tickMsg:
		m.spinnerFrame = (m.spinnerFrame + 1) % len(SpinnerFrames)
		return m, discoverTickCmd()

	case DiscoverProgressMsg:
		m.Progress = msg.Progress
		m.CurrentTPS = msg.CurrentTPS
		m.P95Latency = msg.P95Latency
		m.ErrorRate = msg.ErrorRate
		m.LowRange = msg.LowRange
		m.HighRange = msg.HighRange
		m.StatusMessage = msg.Status
		return m, nil

	case DiscoverCompleteMsg:
		m.ResultReady = true
		m.SustainedTPS = msg.SustainedTPS
		m.BreakingTPS = msg.BreakingTPS
		m.FinalP95 = msg.P95Latency
		m.FinalErrorRate = msg.ErrorRate
		m.TestDuration = msg.TestDuration
		m.StepsCompleted = msg.StepsCompleted
		m.RecBaseTPS = msg.RecBaseTPS
		m.RecMaxTPS = msg.RecMaxTPS
		m.RecDescription = msg.RecDescription
		m.screen = ScreenDiscoverResult
		return m, nil

	case DiscoverStopMsg:
		m.screen = ScreenDiscoverResult
		return m, nil
	}

	// Handle text input
	if m.screen == ScreenDiscoverSetup {
		cmd := m.updateInputs(msg)
		return m, cmd
	}

	return m, nil
}

func (m *DiscoverModel) handleEnter() (tea.Model, tea.Cmd) {
	switch m.screen {
	case ScreenDiscoverSetup:
		// Save configuration
		m.TargetURL = m.inputs[0].Value()
		m.Method = m.inputs[1].Value()
		m.Protocol = m.inputs[2].Value()
		m.LatencyLimit = m.inputs[3].Value()
		m.ErrorLimit = m.inputs[4].Value()
		m.MinTPS = m.inputs[5].Value()
		m.MaxTPS = m.inputs[6].Value()

		// Validate URL
		if m.TargetURL == "" {
			m.TargetURL = m.inputs[0].Placeholder
		}

		// Start discovery
		m.screen = ScreenDiscoverRunning
		m.startTime = time.Now()
		m.IsSearching = true
		m.Progress = 0
		m.StatusMessage = "Initializing..."

	case ScreenDiscoverResult:
		return m, tea.Quit
	}
	return m, nil
}

func (m *DiscoverModel) handleNext() (tea.Model, tea.Cmd) {
	if m.screen == ScreenDiscoverSetup {
		m.inputs[m.focusIndex].Blur()
		m.focusIndex = (m.focusIndex + 1) % len(m.inputs)
		m.inputs[m.focusIndex].Focus()
	}
	return m, nil
}

func (m *DiscoverModel) handlePrev() (tea.Model, tea.Cmd) {
	if m.screen == ScreenDiscoverSetup {
		m.inputs[m.focusIndex].Blur()
		m.focusIndex = (m.focusIndex - 1 + len(m.inputs)) % len(m.inputs)
		m.inputs[m.focusIndex].Focus()
	}
	return m, nil
}

func (m *DiscoverModel) updateInputs(msg tea.Msg) tea.Cmd {
	var cmds []tea.Cmd

	for i := range m.inputs {
		var cmd tea.Cmd
		m.inputs[i], cmd = m.inputs[i].Update(msg)
		cmds = append(cmds, cmd)
	}

	return tea.Batch(cmds...)
}

// View renders the discover TUI.
func (m DiscoverModel) View() string {
	switch m.screen {
	case ScreenDiscoverSetup:
		return m.viewSetup()
	case ScreenDiscoverRunning:
		return m.viewRunning()
	case ScreenDiscoverResult:
		return m.viewResult()
	default:
		return ""
	}
}

// GetConfig returns the discovery configuration from the TUI.
func (m DiscoverModel) GetConfig() map[string]string {
	targetURL := m.TargetURL
	if targetURL == "" {
		targetURL = m.inputs[0].Placeholder
	}

	return map[string]string{
		"target_url":    targetURL,
		"method":        m.Method,
		"protocol":      m.Protocol,
		"latency_limit": m.LatencyLimit,
		"error_limit":   m.ErrorLimit,
		"min_tps":       m.MinTPS,
		"max_tps":       m.MaxTPS,
	}
}

// GetElapsed returns the elapsed time since discovery started.
func (m DiscoverModel) GetElapsed() time.Duration {
	if m.startTime.IsZero() {
		return 0
	}
	return time.Since(m.startTime)
}

// renderInput renders an input field with focus state.
func (m DiscoverModel) renderInput(index int, focused bool) string {
	if focused {
		return ActiveBorderStyle.Padding(0, 1).Render(m.inputs[index].View())
	}
	return BorderStyle.Padding(0, 1).Render(m.inputs[index].View())
}
