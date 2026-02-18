package tui

import "github.com/charmbracelet/lipgloss"

// Color palette
var (
	colorGreen  = lipgloss.Color("#28a745")
	colorYellow = lipgloss.Color("#ffc107")
	colorRed    = lipgloss.Color("#dc3545")
	colorMuted  = lipgloss.Color("#6c757d")
	colorBorder = lipgloss.Color("#444")
	colorHeader = lipgloss.Color("#007bff")
)

// Styles
var (
	// BorderStyle is used for panels and containers.
	BorderStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBorder).
			Padding(0, 1)

	// HeaderStyle is used for section headers.
	HeaderStyle = lipgloss.NewStyle().
			Foreground(colorHeader).
			Bold(true)

	// StatusGreen indicates propagated / success.
	StatusGreen = lipgloss.NewStyle().Foreground(colorGreen)

	// StatusYellow indicates pending / in-progress.
	StatusYellow = lipgloss.NewStyle().Foreground(colorYellow)

	// StatusRed indicates error / timeout.
	StatusRed = lipgloss.NewStyle().Foreground(colorRed)

	// MutedStyle is used for secondary / de-emphasized text.
	MutedStyle = lipgloss.NewStyle().Foreground(colorMuted)
)
