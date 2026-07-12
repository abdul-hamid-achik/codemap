package tui

import (
	"fmt"

	"charm.land/lipgloss/v2"
)

var (
	colorInk    = lipgloss.Color("#E6E6E6")
	colorMuted  = lipgloss.Color("#8A8F98")
	colorAccent = lipgloss.Color("#66D9EF")
	colorBad    = lipgloss.Color("#F92672")
	colorWarn   = lipgloss.Color("#E6DB74")
	colorSelect = lipgloss.Color("#2E5E6E")
	colorBar    = lipgloss.Color("#66D9EF")
	colorSym    = lipgloss.Color("#A6E22E")

	titleStyle      = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	mutedStyle      = lipgloss.NewStyle().Foreground(colorMuted)
	errorStyle      = lipgloss.NewStyle().Foreground(colorBad)
	warnStyle       = lipgloss.NewStyle().Foreground(colorWarn)
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
	// daemonOnStyle marks the live "background daemon is watching" header indicator.
	daemonOnStyle = lipgloss.NewStyle().Foreground(colorSym)

	// scrollTrackStyle / scrollThumbStyle draw the minimal 1-column scrollbar on
	// the source/context overlay: a muted track with an accent thumb.
	scrollTrackStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#3A3F4B"))
	scrollThumbStyle = lipgloss.NewStyle().Foreground(colorAccent)

	// heat1..heat3Style form the blast-radius depth heatmap (Impact tab): depth 1 is
	// "hottest" (a direct caller, most likely affected) and fades to muted with depth.
	heat1Style = lipgloss.NewStyle().Foreground(colorBad)  // depth 1 — direct
	heat2Style = lipgloss.NewStyle().Foreground(colorWarn) // depth 2
	heat3Style = lipgloss.NewStyle().Foreground(colorBar)  // depth 3
)

// depthHeatStyle is the heatmap color for a blast-radius depth: hot (direct) →
// cool (distant). Shared by the [depth] tag and the tree-branch connectors.
func depthHeatStyle(depth int) lipgloss.Style {
	switch {
	case depth <= 1:
		return heat1Style
	case depth == 2:
		return heat2Style
	case depth == 3:
		return heat3Style
	default:
		return mutedStyle
	}
}

// depthHeat renders a blast-radius node's [depth] tag colored by how directly the
// change reaches it — a one-glance heatmap from hot (near) to cool (far).
func depthHeat(depth int) string {
	return depthHeatStyle(depth).Render(fmt.Sprintf("[%d]", depth))
}

// heatLegend is a compact key for the depth heatmap shown beside the Blast radius
// title: 1 (hot/direct) → 4+ (cool/distant).
func heatLegend() string {
	return mutedStyle.Render("heat ") + heat1Style.Render("1") + " " + heat2Style.Render("2") + " " +
		heat3Style.Render("3") + " " + mutedStyle.Render("4+")
}
