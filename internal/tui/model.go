package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Screen represents different screens in the TUI
type Screen int

const (
	ScreenWelcome Screen = iota
	ScreenTargetSetup
	ScreenTrafficConfig
	ScreenPatternConfig
	ScreenReview
	ScreenRunning
)

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
	TargetURL     string
	TargetMethod  string
	Protocol      string
	BaseTPS       string
	MaxTPS        string
	PoissonLambda string
	SpikeFactor   string
	NoiseAmp      string
	Schedule      string

	// Runtime state
	CurrentTPS  float64
	RequestsSent int64
	ErrorCount   int64
	AvgLatency   float64
	IsSpiking    bool
}

// NewModel creates a new TUI model
func NewModel() Model {
	m := Model{
		screen:        ScreenWelcome,
		TargetMethod:  "GET",
		Protocol:      "http",
		BaseTPS:       "100",
		MaxTPS:        "1000",
		PoissonLambda: "0.1",
		SpikeFactor:   "3.0",
		NoiseAmp:      "0.15",
	}

	// Create text inputs
	m.inputs = make([]textinput.Model, 9)

	// Target URL
	m.inputs[0] = textinput.New()
	m.inputs[0].Placeholder = "http://localhost:8080/api/health"
	m.inputs[0].Focus()
	m.inputs[0].CharLimit = 256
	m.inputs[0].Width = 50

	// Method
	m.inputs[1] = textinput.New()
	m.inputs[1].Placeholder = "GET"
	m.inputs[1].SetValue("GET")
	m.inputs[1].CharLimit = 10
	m.inputs[1].Width = 10

	// Protocol
	m.inputs[2] = textinput.New()
	m.inputs[2].Placeholder = "http"
	m.inputs[2].SetValue("http")
	m.inputs[2].CharLimit = 10
	m.inputs[2].Width = 10

	// Base TPS
	m.inputs[3] = textinput.New()
	m.inputs[3].Placeholder = "100"
	m.inputs[3].SetValue("100")
	m.inputs[3].CharLimit = 10
	m.inputs[3].Width = 10

	// Max TPS
	m.inputs[4] = textinput.New()
	m.inputs[4].Placeholder = "1000"
	m.inputs[4].SetValue("1000")
	m.inputs[4].CharLimit = 10
	m.inputs[4].Width = 10

	// Poisson Lambda
	m.inputs[5] = textinput.New()
	m.inputs[5].Placeholder = "0.1"
	m.inputs[5].SetValue("0.1")
	m.inputs[5].CharLimit = 10
	m.inputs[5].Width = 10

	// Spike Factor
	m.inputs[6] = textinput.New()
	m.inputs[6].Placeholder = "3.0"
	m.inputs[6].SetValue("3.0")
	m.inputs[6].CharLimit = 10
	m.inputs[6].Width = 10

	// Noise Amplitude
	m.inputs[7] = textinput.New()
	m.inputs[7].Placeholder = "0.15"
	m.inputs[7].SetValue("0.15")
	m.inputs[7].CharLimit = 10
	m.inputs[7].Width = 10

	// Schedule
	m.inputs[8] = textinput.New()
	m.inputs[8].Placeholder = "9-17:1.5, 0-5:0.3"
	m.inputs[8].CharLimit = 100
	m.inputs[8].Width = 30

	return m
}

// Init initializes the model
func (m Model) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, tickCmd())
}

// tickMsg is sent every second
type tickMsg time.Time

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
			if m.screen == ScreenRunning && m.triggered {
				m.triggered = false
				return m, nil
			}
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
		if m.triggered {
			// Simulate updating stats
			m.updateRunningStats()
		}
		return m, tickCmd()
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
		m.PoissonLambda = m.inputs[5].Value()
		m.SpikeFactor = m.inputs[6].Value()
		m.NoiseAmp = m.inputs[7].Value()
		m.Schedule = m.inputs[8].Value()
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
		m.focusIndex = (m.focusIndex + 1) % 4
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
		m.focusIndex = (m.focusIndex - 1 + 4) % 4
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
	m.CurrentTPS = 100 + 50*float64(m.spinnerFrame%10)
	m.RequestsSent = int64(elapsed * 100)
	m.ErrorCount = int64(elapsed * 0.5)
	m.AvgLatency = 15 + float64(m.spinnerFrame%5)
	m.IsSpiking = m.spinnerFrame%20 < 5
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
		"",
		LabelStyle.Render("HTTP Method"),
		m.renderInput(1, m.focusIndex == 1),
		"",
		LabelStyle.Render("Protocol (http/http2/grpc)"),
		m.renderInput(2, m.focusIndex == 2),
	)

	box := BorderStyle.Width(60).Render(content)
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
		DimStyle.Render("  The baseline traffic rate"),
		"",
		LabelStyle.Render("Max TPS"),
		m.renderInput(4, m.focusIndex == 1),
		DimStyle.Render("  Maximum TPS cap during spikes"),
	)

	box := BorderStyle.Width(60).Render(content)
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
		LabelStyle.Render("Poisson Lambda (spike frequency)"),
		m.renderInput(5, m.focusIndex == 0),
		DimStyle.Render("  Average spikes per second (e.g., 0.1)"),
		"",
		LabelStyle.Render("Spike Factor (TPS multiplier)"),
		m.renderInput(6, m.focusIndex == 1),
		DimStyle.Render("  How much TPS increases during spikes"),
		"",
		LabelStyle.Render("Noise Amplitude"),
		m.renderInput(7, m.focusIndex == 2),
		DimStyle.Render("  Random fluctuation range (0.15 = ±15%)"),
		"",
		LabelStyle.Render("Schedule (optional: hour-range:multiplier)"),
		m.renderInput(8, m.focusIndex == 3),
		DimStyle.Render("  e.g., 9-17:1.5, 0-5:0.3"),
	)

	box := BorderStyle.Width(60).Render(content)
	b.WriteString(lipgloss.Place(m.width, m.height-18, lipgloss.Center, lipgloss.Top, box))

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

	configSummary := lipgloss.JoinVertical(lipgloss.Left,
		SubtitleStyle.Render("Target"),
		fmt.Sprintf("  %s %s %s", LabelStyle.Render("URL:"), ValueStyle.Render(targetURL), ""),
		fmt.Sprintf("  %s %s  %s %s", LabelStyle.Render("Method:"), ValueStyle.Render(m.TargetMethod), LabelStyle.Render("Protocol:"), ValueStyle.Render(m.Protocol)),
		"",
		SubtitleStyle.Render("Traffic"),
		fmt.Sprintf("  %s %s TPS  %s %s TPS", LabelStyle.Render("Base:"), ValueStyle.Render(m.BaseTPS), LabelStyle.Render("Max:"), ValueStyle.Render(m.MaxTPS)),
		"",
		SubtitleStyle.Render("Pattern"),
		fmt.Sprintf("  %s %s  %s %sx", LabelStyle.Render("Lambda:"), ValueStyle.Render(m.PoissonLambda), LabelStyle.Render("Spike:"), ValueStyle.Render(m.SpikeFactor)),
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
	if m.IsSpiking {
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
		"poisson_lambda": m.PoissonLambda,
		"spike_factor":   m.SpikeFactor,
		"noise_amp":      m.NoiseAmp,
		"schedule":       m.Schedule,
	}
}
