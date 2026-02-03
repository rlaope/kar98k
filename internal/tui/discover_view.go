package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// viewSetup renders the discovery setup screen.
func (m DiscoverModel) viewSetup() string {
	var b strings.Builder

	b.WriteString("\n")
	b.WriteString(m.renderHeader("DISCOVER CONFIGURATION", "1/2"))
	b.WriteString("\n\n")

	content := lipgloss.JoinVertical(lipgloss.Left,
		SubtitleStyle.Render("Target"),
		"",
		LabelStyle.Render("Target URL"),
		m.renderInput(0, m.focusIndex == 0),
		DimStyle.Render("  The endpoint to test for maximum sustainable TPS."),
		"",
		lipgloss.JoinHorizontal(lipgloss.Top,
			lipgloss.JoinVertical(lipgloss.Left,
				LabelStyle.Render("Method"),
				m.renderInput(1, m.focusIndex == 1),
			),
			"  ",
			lipgloss.JoinVertical(lipgloss.Left,
				LabelStyle.Render("Protocol"),
				m.renderInput(2, m.focusIndex == 2),
			),
		),
		"",
		SubtitleStyle.Render("Thresholds"),
		"",
		lipgloss.JoinHorizontal(lipgloss.Top,
			lipgloss.JoinVertical(lipgloss.Left,
				LabelStyle.Render("P95 Latency Limit (ms)"),
				m.renderInput(3, m.focusIndex == 3),
				DimStyle.Render("  Max acceptable P95 latency"),
			),
			"  ",
			lipgloss.JoinVertical(lipgloss.Left,
				LabelStyle.Render("Error Rate Limit (%)"),
				m.renderInput(4, m.focusIndex == 4),
				DimStyle.Render("  Max acceptable error rate"),
			),
		),
		"",
		SubtitleStyle.Render("Search Range"),
		"",
		lipgloss.JoinHorizontal(lipgloss.Top,
			lipgloss.JoinVertical(lipgloss.Left,
				LabelStyle.Render("Min TPS"),
				m.renderInput(5, m.focusIndex == 5),
				DimStyle.Render("  Starting point"),
			),
			"  ",
			lipgloss.JoinVertical(lipgloss.Left,
				LabelStyle.Render("Max TPS"),
				m.renderInput(6, m.focusIndex == 6),
				DimStyle.Render("  Upper bound to test"),
			),
		),
	)

	box := BorderStyle.Width(70).Render(content)
	b.WriteString(lipgloss.Place(m.width, m.height-20, lipgloss.Center, lipgloss.Top, box))

	b.WriteString("\n\n")
	b.WriteString(lipgloss.Place(m.width, 0, lipgloss.Center, lipgloss.Top,
		ActiveButtonStyle.Render(" "+Crosshair+" START DISCOVERY ")))

	b.WriteString("\n\n")
	b.WriteString(lipgloss.Place(m.width, 0, lipgloss.Center, lipgloss.Top,
		HelpStyle.Render("TAB: next field • ENTER: start • ESC/Q: quit")))

	return b.String()
}

// viewRunning renders the discovery running screen.
func (m DiscoverModel) viewRunning() string {
	var b strings.Builder

	b.WriteString("\n")

	// Header with status
	statusIcon := InfoStyle.Render("◉")
	statusText := InfoStyle.Render("DISCOVERING")

	header := lipgloss.JoinHorizontal(lipgloss.Center,
		MiniLogo(),
		"  ",
		statusIcon,
		" ",
		statusText,
	)
	b.WriteString(lipgloss.Place(m.width, 0, lipgloss.Center, lipgloss.Top, header))
	b.WriteString("\n\n")

	// Current TPS being tested
	tpsDisplay := fmt.Sprintf("%.0f", m.CurrentTPS)
	if m.CurrentTPS >= 1000 {
		tpsDisplay = fmt.Sprintf("%.1fk", m.CurrentTPS/1000)
	}

	// Progress bar
	progressPercent := m.Progress / 100
	if progressPercent > 1 {
		progressPercent = 1
	}

	// Latency status
	latencyStatus := SuccessStyle.Render(CheckMark)
	latencyLimit := m.LatencyLimit
	if latencyLimit == "" {
		latencyLimit = "500"
	}
	latencyLimitVal := parseFloat(latencyLimit, 500)
	if m.P95Latency > latencyLimitVal {
		latencyStatus = ErrorStyle.Render(CrossMark)
	}

	// Error rate status
	errorStatus := SuccessStyle.Render(CheckMark)
	errorLimit := m.ErrorLimit
	if errorLimit == "" {
		errorLimit = "5"
	}
	errorLimitVal := parseFloat(errorLimit, 5)
	if m.ErrorRate > errorLimitVal {
		errorStatus = ErrorStyle.Render(CrossMark)
	}

	// Search range display
	rangeDisplay := fmt.Sprintf("[%.0f - %.0f]", m.LowRange, m.HighRange)

	content := lipgloss.JoinVertical(lipgloss.Left,
		SubtitleStyle.Render("Current TPS"),
		lipgloss.JoinHorizontal(lipgloss.Center,
			ValueStyle.Render("  "+tpsDisplay),
			"  ",
			DimStyle.Render("[searching...]"),
		),
		"  "+ProgressBar(progressPercent, 45),
		"",
		lipgloss.JoinHorizontal(lipgloss.Top,
			lipgloss.JoinVertical(lipgloss.Left,
				LabelStyle.Render("P95 Latency"),
				lipgloss.JoinHorizontal(lipgloss.Center,
					ValueStyle.Render(fmt.Sprintf("  %.0fms", m.P95Latency)),
					" ",
					latencyStatus,
					" ",
					DimStyle.Render(fmt.Sprintf("(limit: %sms)", latencyLimit)),
				),
			),
		),
		"",
		lipgloss.JoinHorizontal(lipgloss.Top,
			lipgloss.JoinVertical(lipgloss.Left,
				LabelStyle.Render("Error Rate"),
				lipgloss.JoinHorizontal(lipgloss.Center,
					ValueStyle.Render(fmt.Sprintf("  %.1f%%", m.ErrorRate)),
					" ",
					errorStatus,
					" ",
					DimStyle.Render(fmt.Sprintf("(limit: %s%%)", errorLimit)),
				),
			),
		),
		"",
		Divider(50),
		"",
		LabelStyle.Render("Search Range"),
		ValueStyle.Render("  "+rangeDisplay),
		"",
		LabelStyle.Render("Progress"),
		"  "+ProgressBar(progressPercent, 35)+fmt.Sprintf("  %.0f%%", m.Progress),
		"",
		LabelStyle.Render("Elapsed"),
		ValueStyle.Render("  "+m.GetElapsed().Round(time.Second).String()),
	)

	if m.StatusMessage != "" {
		content = lipgloss.JoinVertical(lipgloss.Left,
			content,
			"",
			DimStyle.Render("  "+m.StatusMessage),
		)
	}

	box := ActiveBorderStyle.Width(60).Render(content)
	b.WriteString(lipgloss.Place(m.width, 0, lipgloss.Center, lipgloss.Top, box))

	// Spinner
	b.WriteString("\n\n")
	spinner := InfoStyle.Render(SpinnerFrames[m.spinnerFrame])
	b.WriteString(lipgloss.Place(m.width, 0, lipgloss.Center, lipgloss.Top,
		spinner+" "+DimStyle.Render("Binary search in progress...")))

	b.WriteString("\n\n")
	b.WriteString(lipgloss.Place(m.width, 0, lipgloss.Center, lipgloss.Top,
		HelpStyle.Render("Q: stop and show results")))

	return b.String()
}

// viewResult renders the discovery result screen.
func (m DiscoverModel) viewResult() string {
	var b strings.Builder

	b.WriteString("\n")

	// Header
	header := lipgloss.JoinHorizontal(lipgloss.Center,
		MiniLogo(),
		"  ",
		SuccessStyle.Render(CheckMark),
		" ",
		SuccessStyle.Render("DISCOVERY COMPLETE"),
	)
	b.WriteString(lipgloss.Place(m.width, 0, lipgloss.Center, lipgloss.Top, header))
	b.WriteString("\n\n")

	// Format TPS values
	sustainedDisplay := formatTPSDisplay(m.SustainedTPS)
	breakingDisplay := formatTPSDisplay(m.BreakingTPS)
	recBaseDisplay := formatTPSDisplay(m.RecBaseTPS)
	recMaxDisplay := formatTPSDisplay(m.RecMaxTPS)

	// Main results
	results := lipgloss.JoinVertical(lipgloss.Left,
		SubtitleStyle.Render("Your system can handle:"),
		"",
		lipgloss.JoinHorizontal(lipgloss.Center,
			LabelStyle.Render("  Sustained TPS    "),
			HighlightStyle.Render(sustainedDisplay),
		),
		lipgloss.JoinHorizontal(lipgloss.Center,
			LabelStyle.Render("  Breaking Point   "),
			WarningStyle.Render(breakingDisplay+" TPS"),
		),
		"",
		SubtitleStyle.Render("At sustained load:"),
		"",
		lipgloss.JoinHorizontal(lipgloss.Center,
			LabelStyle.Render("  P95 Latency      "),
			ValueStyle.Render(fmt.Sprintf("%.0fms", m.FinalP95)),
		),
		lipgloss.JoinHorizontal(lipgloss.Center,
			LabelStyle.Render("  Error Rate       "),
			ValueStyle.Render(fmt.Sprintf("%.1f%%", m.FinalErrorRate)),
		),
		"",
		Divider(50),
		"",
		SubtitleStyle.Render("Recommendation:"),
		"",
		lipgloss.JoinHorizontal(lipgloss.Center,
			LabelStyle.Render("  Set BaseTPS to "),
			SuccessStyle.Render(recBaseDisplay),
			DimStyle.Render(" (80% of sustained)"),
		),
		lipgloss.JoinHorizontal(lipgloss.Center,
			LabelStyle.Render("  Set MaxTPS to  "),
			SuccessStyle.Render(recMaxDisplay),
			DimStyle.Render(" (safe spike limit)"),
		),
	)

	box := BorderStyle.Width(60).Render(results)
	b.WriteString(lipgloss.Place(m.width, 0, lipgloss.Center, lipgloss.Top, box))

	// Test summary
	b.WriteString("\n\n")
	summary := lipgloss.JoinHorizontal(lipgloss.Center,
		DimStyle.Render("Test completed in "),
		ValueStyle.Render(m.TestDuration.Round(time.Second).String()),
		DimStyle.Render(fmt.Sprintf(" (%d steps)", m.StepsCompleted)),
	)
	b.WriteString(lipgloss.Place(m.width, 0, lipgloss.Center, lipgloss.Top, summary))

	b.WriteString("\n\n")
	b.WriteString(lipgloss.Place(m.width, 0, lipgloss.Center, lipgloss.Top,
		HelpStyle.Render("Press ENTER or Q to exit")))

	return b.String()
}

// renderHeader renders a screen header.
func (m DiscoverModel) renderHeader(title, step string) string {
	header := lipgloss.JoinHorizontal(lipgloss.Center,
		MiniLogo(),
		"  ",
		TitleStyle.Render(" "+title+" "),
		"  ",
		DimStyle.Render("Step "+step),
	)
	return lipgloss.Place(m.width, 0, lipgloss.Center, lipgloss.Top, header)
}

// Helper functions

func formatTPSDisplay(tps float64) string {
	if tps >= 1000 {
		return fmt.Sprintf("%.1fk", tps/1000)
	}
	return fmt.Sprintf("%.0f", tps)
}

func parseFloat(s string, defaultVal float64) float64 {
	if s == "" {
		return defaultVal
	}
	var val float64
	_, err := fmt.Sscanf(s, "%f", &val)
	if err != nil {
		return defaultVal
	}
	return val
}
