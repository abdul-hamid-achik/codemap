package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/abdul-hamid-achik/codemap/internal/app"
)

// Mouse support (bubbletea v2). Mouse reporting is enabled per-View via
// View.MouseMode (there is no program-level option in v2 — see run.go). Update
// receives tea.MouseWheelMsg / tea.MouseClickMsg, which route here:
//   - wheel scrolls the active list or the source/context overlay;
//   - a left click on the tab bar switches tabs;
//   - a left click on a Graph-hub / Search / Impact row selects it.
// The refs/metrics/path lists remain keyboard-driven (wheel still scrolls them).

// wheelStep is how many rows one wheel notch moves the selection.
const wheelStep = 3

// bodyTop is the first screen row of the body: row 0 is the header and row 1 is
// the tab bar (see render()).
const bodyTop = 2

// tabBarRow is the screen row the clickable tab chips render on.
const tabBarRow = 1

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// handleWheel moves the active selection (or scrolls the open overlay) by one
// notch. up=true scrolls toward the top of the list.
func (m Model) handleWheel(up bool) (tea.Model, tea.Cmd) {
	delta := wheelStep
	if up {
		delta = -wheelStep
	}
	if m.srcView {
		m.srcScroll = clamp(m.srcScroll+delta, 0, m.maxSrcScroll())
		return m, nil
	}
	if m.showHelp || m.annotationOpen {
		return m, nil
	}
	switch m.active {
	case tabGraph:
		if m.graphFocus == focusRefs {
			m.graphRefSel = clampIdx(m.graphRefSel+delta, len(m.graphRefs()))
			return m, nil
		}
		return m.selectHub(m.graphSel + delta)
	case tabMetrics:
		m.metricsSel = clampIdx(m.metricsSel+delta, m.metricsCount())
	case tabImpact:
		m.impactSel = clampIdx(m.impactSel+delta, m.blastLen())
	case tabSearch:
		m.searchSel = clampIdx(m.searchSel+delta, len(m.searchHits))
	case tabPath:
		if m.pathFocus == focusPathResult {
			m.pathSel = clampIdx(m.pathSel+delta, m.pathLen())
		}
	}
	return m, nil
}

// handleClick dispatches a left click: the tab bar switches tabs, the body
// selects a row. Clicks are ignored while a modal overlay is open.
func (m Model) handleClick(x, y int) (tea.Model, tea.Cmd) {
	if m.annotationOpen || m.showHelp || m.srcView {
		return m, nil
	}
	if y == tabBarRow {
		if t, ok := tabAtX(x); ok && t != m.active {
			return m, m.switchTab(t)
		}
		return m, nil
	}
	return m.clickBody(x, y)
}

// tabAtX maps a column on the tab-bar row to a tab. The chips render as
// " N Name " joined by a single space (see tabBar), so the boundaries are
// reconstructable without threading render state through the model.
func tabAtX(x int) (tab, bool) {
	cursor := 0
	for t := tab(0); t < tabCount; t++ {
		w := lipgloss.Width(fmt.Sprintf(" %d %s ", int(t)+1, t.String()))
		if x >= cursor && x < cursor+w {
			return t, true
		}
		cursor += w + 1 // + the join space
	}
	return 0, false
}

// clickBody selects the clicked row in the active tab's primary list. The row
// geometry mirrors each renderer's lead lines and windowing exactly; a click
// that lands off a row is a no-op.
func (m Model) clickBody(x, y int) (tea.Model, tea.Cmd) {
	banner := boolToInt(m.annotationBanner(m.width) != "")
	switch m.active {
	case tabSearch:
		n := len(m.searchHits)
		if n == 0 {
			return m, nil
		}
		budget := clamp(m.bodyHeight()-9, 1, 50)
		start := windowStart(m.searchSel, budget, n)
		end := clamp(start+budget, 0, n)
		first := bodyTop + banner + 2 + boolToInt(start > 0)
		if idx, ok := rowIndexAt(y, first, start, end-start); ok {
			m.searchSel = idx
		}
		return m, nil
	case tabImpact:
		rep := m.impactRep
		if rep == nil || !rep.Found || len(rep.BlastRadius) == 0 {
			return m, nil
		}
		budget := clamp(m.bodyHeight()-impactReserve(rep), 1, 40)
		start := windowStart(m.impactSel, budget, len(rep.BlastRadius))
		end := clamp(start+budget, 0, len(rep.BlastRadius))
		first := bodyTop + banner + m.impactLeadLines(rep) + boolToInt(start > 0)
		if idx, ok := rowIndexAt(y, first, start, end-start); ok {
			m.impactSel = idx
		}
		return m, nil
	case tabGraph:
		if !m.graphLoaded || len(m.graphHubs) == 0 {
			return m, nil
		}
		leftW := 38
		if leftW > m.width/2 {
			leftW = m.width / 2
		}
		if x >= leftW { // the refs pane stays keyboard-driven
			return m, nil
		}
		n := len(m.graphHubs)
		budget := m.graphHubBudget()
		start := windowStart(m.graphSel, budget, n)
		end := clamp(start+budget, 0, n)
		first := bodyTop + banner + 1 + boolToInt(start > 0)
		if idx, ok := rowIndexAt(y, first, start, end-start); ok {
			m.graphFocus = focusHubs
			return m.selectHub(idx)
		}
	}
	return m, nil
}

// rowIndexAt maps a screen row to a list index within a rendered window: first
// is the screen row of the item at index start, and visible is how many rows the
// window draws. Returns false when the row is outside the drawn rows.
func rowIndexAt(y, first, start, visible int) (int, bool) {
	k := y - first
	if k < 0 || k >= visible {
		return 0, false
	}
	return start + k, true
}

// bodyHeight is the number of rows the body occupies (render() reserves the
// header, tab bar, and footer).
func (m Model) bodyHeight() int {
	h := m.height - 3
	if h < 3 {
		h = 3
	}
	return h
}

// graphHubBudget is the number of hub rows the left pane draws — it mirrors
// hubList's window sizing so a click maps to the right hub.
func (m Model) graphHubBudget() int {
	avail := m.bodyHeight() - 1 // minus the "Hubs (N)" title
	if avail < 1 {
		avail = 1
	}
	if n := len(m.graphHubs); n > avail {
		if b := avail - 2; b >= 1 { // two lines reserved for ▲/▼ "N more"
			return b
		}
		return 1
	}
	return avail
}

// impactReserve mirrors renderImpact's blast-radius budget reserve.
func impactReserve(rep *app.ImpactReport) int {
	r := 14
	if len(rep.Tests) > 0 {
		r++
	}
	r += len(rep.Annotations)
	return r
}

// impactLeadLines is the number of content rows renderImpact writes before the
// blast-radius list (the definition block, coverage line, resolution/note,
// covering tests, inline annotations, and the "Blast radius" heading). A click's
// row is offset from the body top by exactly this many lines, so the mapping
// stays exact as the header grows or shrinks.
func (m Model) impactLeadLines(rep *app.ImpactReport) int {
	lines := 2 // "Impact  <input>" then a blank line
	for _, l := range rep.Locations {
		lines++ // the "defined …" row
		if strings.Join(strings.Fields(l.Signature), " ") != "" {
			lines++
		}
	}
	if docFirstLine(firstDoc(rep.Locations)) != "" {
		lines++
	}
	lines += 2 // blank line + the coverage summary
	if rep.Resolution != "" {
		lines++
	}
	if rep.Note != "" {
		lines++
	}
	if len(rep.Tests) > 0 {
		lines++ // the "covered by …" line
	}
	lines += len(rep.Annotations)
	lines += 2 // blank line + the "Blast radius" heading
	return lines
}
