package tui

import "github.com/charmbracelet/lipgloss"

// Color palette.
var (
	colorPrimary   = lipgloss.Color("39")  // blue
	colorSecondary = lipgloss.Color("243") // gray
	colorSuccess   = lipgloss.Color("42")  // green
	colorWarning   = lipgloss.Color("214") // orange
	colorError     = lipgloss.Color("196") // red
	colorMuted     = lipgloss.Color("240") // dim gray
	colorHighlight = lipgloss.Color("212") // pink
)

// Reusable styles.
var (
	styleTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPrimary).
			MarginBottom(1)

	styleSubtitle = lipgloss.NewStyle().
			Foreground(colorSecondary).
			Bold(true)

	styleLabel = lipgloss.NewStyle().
			Foreground(colorMuted).
			Width(14)

	styleValue = lipgloss.NewStyle().
			Foreground(lipgloss.Color("252"))

	styleSuccess = lipgloss.NewStyle().
			Foreground(colorSuccess)

	styleWarning = lipgloss.NewStyle().
			Foreground(colorWarning)

	styleError = lipgloss.NewStyle().
			Foreground(colorError)

	styleHighlight = lipgloss.NewStyle().
			Foreground(colorHighlight).
			Bold(true)

	styleMuted = lipgloss.NewStyle().
			Foreground(colorMuted)

	styleSelectedRow = lipgloss.NewStyle().
				Background(lipgloss.Color("237")).
				Foreground(lipgloss.Color("252")).
				Bold(true)

	styleNormalRow = lipgloss.NewStyle().
			Foreground(lipgloss.Color("250"))

	styleHeader = lipgloss.NewStyle().
			Foreground(colorPrimary).
			Bold(true).
			Underline(true)

	styleTab = lipgloss.NewStyle().
			Foreground(colorMuted).
			Padding(0, 2)

	styleActiveTab = lipgloss.NewStyle().
			Foreground(colorPrimary).
			Bold(true).
			Padding(0, 2).
			Underline(true)
)
