package tui

import "charm.land/lipgloss/v2"

var (
	colorInk    = lipgloss.Color("#E6E6E6")
	colorMuted  = lipgloss.Color("#8A8F98")
	colorAccent = lipgloss.Color("#66D9EF")
	colorBad    = lipgloss.Color("#F92672")
	colorSelect = lipgloss.Color("#2E5E6E")
	colorBar    = lipgloss.Color("#66D9EF")
	colorSym    = lipgloss.Color("#A6E22E")

	titleStyle      = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	mutedStyle      = lipgloss.NewStyle().Foreground(colorMuted)
	errorStyle      = lipgloss.NewStyle().Foreground(colorBad)
	sectionStyle    = lipgloss.NewStyle().Bold(true).Foreground(colorInk)
	symStyle        = lipgloss.NewStyle().Foreground(colorSym)
	barStyle        = lipgloss.NewStyle().Foreground(colorBar)
	panelTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	chipStyle       = lipgloss.NewStyle().Foreground(colorMuted).Background(lipgloss.Color("#252932"))
	activeChipStyle = lipgloss.NewStyle().Foreground(colorInk).Background(colorSelect).Bold(true)
	selectedStyle   = lipgloss.NewStyle().Foreground(colorInk).Background(colorSelect)
	// dimSelectedStyle marks the selection in a pane that doesn't currently have
	// keyboard focus (e.g. the hub list while you're walking the refs pane).
	dimSelectedStyle = lipgloss.NewStyle().Foreground(colorInk).Background(lipgloss.Color("#23323A"))
	dividerStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#3A3F4B"))
	countStyle       = lipgloss.NewStyle().Foreground(colorMuted)
)
