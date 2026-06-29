package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/harmonica"
)

// Studio uses spring-physics reveals (charmbracelet/harmonica) for two surfaces:
// the Metrics bar charts grow in when you land on the tab, and the Graph
// neighborhood map's branches grow in when you toggle it on. Both are driven by a
// single self-rescheduling frame tick that STOPS the moment every active spring
// settles, so an idle studio costs nothing (no perpetual 60fps loop).

const animFPS = 60

// animTickMsg is one animation frame. animTick schedules the next one.
type animTickMsg struct{}

func animTick() tea.Cmd {
	return tea.Tick(time.Second/animFPS, func(time.Time) tea.Msg { return animTickMsg{} })
}

// newRevealSpring tunes a reveal: a brisk angular frequency with light damping
// (<1) so the value overshoots slightly and settles in ~0.5s — lively but not
// bouncy. Update() is stateless: the caller holds pos+vel and feeds them back.
func newRevealSpring() harmonica.Spring {
	return harmonica.NewSpring(harmonica.FPS(animFPS), 6.5, 0.7)
}

// kickAnim ensures exactly one frame loop is running; returns the tick cmd, or nil
// when a loop is already live (it just picks up the newly-activated spring).
func (m *Model) kickAnim() tea.Cmd {
	if m.revealing {
		return nil
	}
	m.revealing = true
	return animTick()
}

// startMetricsReveal restarts the Metrics bar grow-in.
func (m *Model) startMetricsReveal() tea.Cmd {
	m.metricsReveal, m.metricsVel, m.metricsActive = 0, 0, true
	return m.kickAnim()
}

// startMapReveal restarts the Graph neighborhood-map grow-in.
func (m *Model) startMapReveal() tea.Cmd {
	m.mapReveal, m.mapVel, m.mapActive = 0, 0, true
	return m.kickAnim()
}

// spinnerFrames are the braille frames of the async-loading spinner.
var spinnerFrames = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")

// spinnerFPSDiv slows the 60fps frame loop to a ~10fps spinner (legible, not a blur).
const spinnerFPSDiv = 6

// spinnerGlyph is the current spinner frame; shown in the footer while busy().
func (m Model) spinnerGlyph() string {
	return string(spinnerFrames[(m.spinnerFrame/spinnerFPSDiv)%len(spinnerFrames)])
}

// advanceAnim steps every active spring one frame, advances the loading spinner
// while busy, and reschedules the tick until everything is idle. Returns nil
// (ending the loop) once no spring is animating and nothing is loading.
func (m *Model) advanceAnim() tea.Cmd {
	if m.metricsActive {
		m.metricsReveal, m.metricsVel = m.revealSpring.Update(m.metricsReveal, m.metricsVel, 1)
		if revealSettled(m.metricsReveal, m.metricsVel) {
			m.metricsReveal, m.metricsVel, m.metricsActive = 1, 0, false
		}
	}
	if m.mapActive {
		m.mapReveal, m.mapVel = m.revealSpring.Update(m.mapReveal, m.mapVel, 1)
		if revealSettled(m.mapReveal, m.mapVel) {
			m.mapReveal, m.mapVel, m.mapActive = 1, 0, false
		}
	}
	if m.busy() {
		m.spinnerFrame++
	}
	if !m.metricsActive && !m.mapActive && !m.busy() {
		m.revealing = false
		return nil
	}
	return animTick()
}

func revealSettled(pos, vel float64) bool {
	return absf(1-pos) < 0.004 && absf(vel) < 0.004
}

func absf(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func clamp01(x float64) float64 {
	switch {
	case x < 0:
		return 0
	case x > 1:
		return 1
	default:
		return x
	}
}
