package tui

import "github.com/charmbracelet/lipgloss"

// kar98k Sky Blue Theme
var (
	// Primary colors - Sky blue palette
	SkyBlue      = lipgloss.Color("#87CEEB")
	DeepSkyBlue  = lipgloss.Color("#00BFFF")
	LightSkyBlue = lipgloss.Color("#B0E0E6")
	DarkSkyBlue  = lipgloss.Color("#4A90D9")
	CyanAccent   = lipgloss.Color("#00CED1")

	// Neutral colors
	White     = lipgloss.Color("#FFFFFF")
	LightGray = lipgloss.Color("#B0B0B0")
	DarkGray  = lipgloss.Color("#404040")
	Black     = lipgloss.Color("#1A1A2E")

	// Status colors
	Success = lipgloss.Color("#00FF88")
	Warning = lipgloss.Color("#FFD700")
	Error   = lipgloss.Color("#FF6B6B")
	Info    = lipgloss.Color("#87CEEB")

	// Styles
	TitleStyle = lipgloss.NewStyle().
			Foreground(White).
			Background(DarkSkyBlue).
			Bold(true).
			Padding(0, 2)

	SubtitleStyle = lipgloss.NewStyle().
			Foreground(LightSkyBlue).
			Bold(true)

	LogoStyle = lipgloss.NewStyle().
			Foreground(DeepSkyBlue).
			Bold(true)

	BorderStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(SkyBlue).
			Padding(1, 2)

	ActiveBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(DeepSkyBlue).
				Padding(1, 2)

	InputStyle = lipgloss.NewStyle().
			Foreground(White).
			Background(DarkGray).
			Padding(0, 1)

	LabelStyle = lipgloss.NewStyle().
			Foreground(LightSkyBlue)

	ValueStyle = lipgloss.NewStyle().
			Foreground(White).
			Bold(true)

	SuccessStyle = lipgloss.NewStyle().
			Foreground(Success).
			Bold(true)

	WarningStyle = lipgloss.NewStyle().
			Foreground(Warning)

	ErrorStyle = lipgloss.NewStyle().
			Foreground(Error).
			Bold(true)

	InfoStyle = lipgloss.NewStyle().
			Foreground(Info)

	DimStyle = lipgloss.NewStyle().
			Foreground(LightGray)

	HighlightStyle = lipgloss.NewStyle().
			Foreground(CyanAccent).
			Bold(true)

	ButtonStyle = lipgloss.NewStyle().
			Foreground(White).
			Background(DarkSkyBlue).
			Padding(0, 2).
			MarginRight(1)

	ActiveButtonStyle = lipgloss.NewStyle().
				Foreground(Black).
				Background(DeepSkyBlue).
				Bold(true).
				Padding(0, 2).
				MarginRight(1)

	MenuItemStyle = lipgloss.NewStyle().
			Foreground(LightGray).
			PaddingLeft(2)

	ActiveMenuItemStyle = lipgloss.NewStyle().
				Foreground(White).
				Background(DarkSkyBlue).
				Bold(true).
				PaddingLeft(2)

	ProgressBarStyle = lipgloss.NewStyle().
				Foreground(DeepSkyBlue)

	ProgressEmptyStyle = lipgloss.NewStyle().
				Foreground(DarkGray)

	StatusBarStyle = lipgloss.NewStyle().
			Foreground(LightGray).
			Background(DarkGray).
			Padding(0, 1)

	HelpStyle = lipgloss.NewStyle().
			Foreground(LightGray)
)

// Logo returns the kar98k ASCII art logo with rifle
func Logo() string {
	logo := `
    ██╗  ██╗ █████╗ ██████╗  █████╗  █████╗ ██╗  ██╗
    ██║ ██╔╝██╔══██╗██╔══██╗██╔══██╗██╔══██╗██║ ██╔╝
    █████╔╝ ███████║██████╔╝╚██████║╚█████╔╝█████╔╝
    ██╔═██╗ ██╔══██║██╔══██╗ ╚═══██║██╔══██╗██╔═██╗
    ██║  ██╗██║  ██║██║  ██║ █████╔╝╚█████╔╝██║  ██╗
    ╚═╝  ╚═╝╚═╝  ╚═╝╚═╝  ╚═╝ ╚════╝  ╚════╝ ╚═╝  ╚═╝`

	rifle := `
                                 ▍▍▍▏
                   ▌▊▋▋▋▊▌▍▏▏▍▋▎▏▄▇█▋▏▌▋▎▏▍▌▊▋▋▋▋▎
                   ▂▆▃▃▃▃▄▆▄▄▇▇▆▅▅▆▆█▅▇▄▇▆▆▅▃▄▄▄▅▋                                               ▋▁▉
                   ▂█████▆▄▂▃██▇▁▃▄▅▂▂██▇▅▇▇█████▉▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▌▆█▆
                          ▋▆█▇▇▇▇▅▅▅▅▆▇█▆▃▄▅▅▅▅▅▅▅▅▄▅▅▅▅▅▅▅▅▅▅▅▅▅▅▅▅▅▅▅▅▅▅▅▅▅▅▅▅▅▅▅▅▅▅▄▄▄▅▅▅▅▅▅▅▄▄▄▄
                       ▎▊▉▃▅▇▄▄▃▃▄▄▄▄▃▄▆▅▃▃▃▃▃▃▃▃▃▃▃▃▃▃▃▃▃▃▃▃▃▃▃▃▃▃▃▃▃▃▃▅████▄▊
                     ▎▂▃▃▆███████████████████████████████████▅▃▃▃▃▃▃▃▃▃▃▃▃▃▃▉▍▏
          ▎▍▍▎     ▎▂▄▃▇███████████████▇▌▏▏▏▏▏▏▏▏▏▏▏▏▏▏▏▏▏▏▏▏
▏▎▌▋▋▋▊▉▉▁▂▁▂▇▃▍▍▊▃▃▃████▆▊▅▍▂ ▋███▇▅▁▌▎
▃▂▉▂▃▅▇█████▅▄█▇▅▂▄████▇▊▏ ▉▉▊▊▊▊▊▍▏
██████▇▆▆▅▅▅██████████▅▎
███▆▅▆▆██████████████▅
█████████████▅▁▋▋▉▃▃▃▏
██████████▄▊▎
██████▅▁▌
██▆▂▋▎
▌▏`

	return LogoStyle.Render(logo) + "\n" + LogoStyle.Render(rifle)
}

// LogoWithWidth returns the logo with rifle, but hides rifle if terminal is too narrow
func LogoWithWidth(width int) string {
	logo := `
    ██╗  ██╗ █████╗ ██████╗  █████╗  █████╗ ██╗  ██╗
    ██║ ██╔╝██╔══██╗██╔══██╗██╔══██╗██╔══██╗██║ ██╔╝
    █████╔╝ ███████║██████╔╝╚██████║╚█████╔╝█████╔╝
    ██╔═██╗ ██╔══██║██╔══██╗ ╚═══██║██╔══██╗██╔═██╗
    ██║  ██╗██║  ██║██║  ██║ █████╔╝╚█████╔╝██║  ██╗
    ╚═╝  ╚═╝╚═╝  ╚═╝╚═╝  ╚═╝ ╚════╝  ╚════╝ ╚═╝  ╚═╝`

	// Rifle needs ~100 chars width to display properly
	if width < 100 {
		return LogoStyle.Render(logo)
	}

	rifle := `
                                 ▍▍▍▏
                   ▌▊▋▋▋▊▌▍▏▏▍▋▎▏▄▇█▋▏▌▋▎▏▍▌▊▋▋▋▋▎
                   ▂▆▃▃▃▃▄▆▄▄▇▇▆▅▅▆▆█▅▇▄▇▆▆▅▃▄▄▄▅▋                                               ▋▁▉
                   ▂█████▆▄▂▃██▇▁▃▄▅▂▂██▇▅▇▇█████▉▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▍▌▆█▆
                          ▋▆█▇▇▇▇▅▅▅▅▆▇█▆▃▄▅▅▅▅▅▅▅▅▄▅▅▅▅▅▅▅▅▅▅▅▅▅▅▅▅▅▅▅▅▅▅▅▅▅▅▅▅▅▅▅▅▅▅▄▄▄▅▅▅▅▅▅▅▄▄▄▄
                       ▎▊▉▃▅▇▄▄▃▃▄▄▄▄▃▄▆▅▃▃▃▃▃▃▃▃▃▃▃▃▃▃▃▃▃▃▃▃▃▃▃▃▃▃▃▃▃▃▃▅████▄▊
                     ▎▂▃▃▆███████████████████████████████████▅▃▃▃▃▃▃▃▃▃▃▃▃▃▃▉▍▏
          ▎▍▍▎     ▎▂▄▃▇███████████████▇▌▏▏▏▏▏▏▏▏▏▏▏▏▏▏▏▏▏▏▏▏
▏▎▌▋▋▋▊▉▉▁▂▁▂▇▃▍▍▊▃▃▃████▆▊▅▍▂ ▋███▇▅▁▌▎
▃▂▉▂▃▅▇█████▅▄█▇▅▂▄████▇▊▏ ▉▉▊▊▊▊▊▍▏
██████▇▆▆▅▅▅██████████▅▎
███▆▅▆▆██████████████▅
█████████████▅▁▋▋▉▃▃▃▏
██████████▄▊▎
██████▅▁▌
██▆▂▋▎
▌▏`

	return LogoStyle.Render(logo) + "\n" + LogoStyle.Render(rifle)
}

// MiniLogo returns a smaller logo
func MiniLogo() string {
	return LogoStyle.Render("⌖ kar98k")
}

// Tagline returns the project tagline
func Tagline() string {
	return DimStyle.Render("High-Intensity Irregular Traffic Simulation")
}

// Divider returns a horizontal divider
func Divider(width int) string {
	line := ""
	for i := 0; i < width; i++ {
		line += "─"
	}
	return DimStyle.Render(line)
}

// ProgressBar renders a progress bar
func ProgressBar(percent float64, width int) string {
	filled := int(float64(width) * percent)
	empty := width - filled

	bar := ""
	for i := 0; i < filled; i++ {
		bar += "="
	}
	for i := 0; i < empty; i++ {
		bar += "-"
	}

	return ProgressBarStyle.Render(bar[:filled]) + ProgressEmptyStyle.Render(bar[filled:])
}

// Spinner frames for loading animation (ASCII compatible)
var SpinnerFrames = []string{"|", "/", "-", "\\", "|", "/", "-", "\\", "|", "/"}

// Bullet points
const (
	BulletPoint   = "●"
	ArrowRight    = "→"
	ArrowUp       = "↑"
	ArrowDown     = "↓"
	CheckMark     = "✓"
	CrossMark     = "✗"
	WarningSign   = "⚠"
	InfoSign      = "ℹ"
	Crosshair     = "⌖"
	TriggerPulled = "◉"
	TriggerReady  = "○"
)
