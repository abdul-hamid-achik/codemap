package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/abdul-hamid-achik/codemap/internal/app"
)

// View renders the studio full-screen (alt-screen).
func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	return v
}

func (m Model) render() string {
	if m.width == 0 || m.height == 0 {
		return "codemap studio\n\nloading…"
	}
	header := m.header()
	tabs := m.tabBar()
	footer := m.footer()

	// Body fills everything between the tab bar and the footer.
	bodyH := m.height - 3 // header, tab bar, footer
	if bodyH < 3 {
		bodyH = 3
	}
	body := lipgloss.NewStyle().Width(m.width).Height(bodyH).MaxHeight(bodyH).Render(m.body(m.width, bodyH))

	return lipgloss.JoinVertical(lipgloss.Left, header, tabs, body, footer)
}

func (m Model) header() string {
	left := titleStyle.Render("codemap studio")
	right := ""
	switch {
	case m.status != nil && m.status.Registered:
		right = mutedStyle.Render(fmt.Sprintf("%s · %d nodes · %d edges · %d files",
			m.status.Project, m.status.Nodes, m.status.Edges, m.status.Files))
	case m.status != nil:
		right = mutedStyle.Render(m.status.Project + " · not indexed")
	}
	return spread(left, right, m.width)
}

func (m Model) tabBar() string {
	chips := make([]string, 0, tabCount)
	for t := tab(0); t < tabCount; t++ {
		label := fmt.Sprintf(" %d %s ", int(t)+1, t.String())
		if t == m.active {
			chips = append(chips, activeChipStyle.Render(label))
		} else {
			chips = append(chips, chipStyle.Render(label))
		}
	}
	return strings.Join(chips, " ")
}

func (m Model) footer() string {
	var hint string
	switch m.active {
	case tabGraph:
		hint = "↑/↓ select · enter → impact · ctrl+r reindex · 1-4 tabs · ctrl+c quit"
	case tabSearch:
		hint = "type · enter search/open · ↑/↓ select · ctrl+r reindex · tab · ctrl+c quit"
	case tabImpact:
		hint = "type symbol · enter run/open · ↑/↓ select · ctrl+r reindex · tab · ctrl+c quit"
	default:
		hint = "ctrl+r reindex · 1-4 tabs · ctrl+c quit"
	}
	status := m.statusMsg
	if m.errMsg != "" {
		status = errorStyle.Render(m.errMsg)
	}
	return spread(mutedStyle.Render(hint), status, m.width)
}

func (m Model) body(w, h int) string {
	switch m.active {
	case tabGraph:
		return m.renderGraph(w, h)
	case tabMetrics:
		return m.renderMetrics(w, h)
	case tabImpact:
		return m.renderImpact(w, h)
	case tabSearch:
		return m.renderSearch(w, h)
	}
	return ""
}

// ---- Graph tab: two-column call-graph explorer ----

func (m Model) renderGraph(w, h int) string {
	if !m.graphLoaded {
		return title("Graph") + "\n\n" + mutedStyle.Render("loading call graph…")
	}
	if len(m.graphHubs) == 0 {
		return title("Graph") + "\n\n" + mutedStyle.Render("no index yet — press ctrl+r to index, or run 'codemap index'")
	}
	leftW := 38
	if leftW > w/2 {
		leftW = w / 2
	}
	rightW := w - leftW - 3
	if rightW < 10 {
		rightW = 10
	}
	left := lipgloss.NewStyle().Width(leftW).Height(h).Render(m.hubList(leftW, h))
	right := lipgloss.NewStyle().Width(rightW).Height(h).Render(m.hubDetail(rightW, h))
	div := dividerStyle.Render(strings.TrimRight(strings.Repeat("│\n", h), "\n"))
	return lipgloss.JoinHorizontal(lipgloss.Top, left, " ", div, " ", right)
}

func (m Model) hubList(w, h int) string {
	var b strings.Builder
	b.WriteString(title("Hubs") + "\n")
	rows := h - 1
	if rows < 1 {
		rows = 1
	}
	start := 0
	if m.graphSel >= rows {
		start = m.graphSel - rows + 1
	}
	end := start + rows
	if end > len(m.graphHubs) {
		end = len(m.graphHubs)
	}
	for i := start; i < end; i++ {
		hub := m.graphHubs[i]
		line := truncate(fmt.Sprintf("%4d  %s", hub.InDegree, displayName(hub.FQN, hub.Symbol)), w)
		if i == m.graphSel {
			b.WriteString(selectedStyle.Width(w).Render(line))
		} else {
			b.WriteString(line)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func (m Model) hubDetail(w, h int) string {
	if m.graphSym == "" {
		return mutedStyle.Render("select a hub")
	}
	var b strings.Builder
	header := m.graphSym
	if m.graphSel < len(m.graphHubs) {
		header = displayName(m.graphHubs[m.graphSel].FQN, m.graphHubs[m.graphSel].Symbol)
	}
	b.WriteString(symStyle.Render(header) + "\n\n")
	budget := (h - 5) / 2
	if budget < 1 {
		budget = 1
	}
	b.WriteString(title(fmt.Sprintf("Called by (%d)", len(m.graphCallers))) + "\n")
	b.WriteString(refLines(m.graphCallers, budget, w))
	b.WriteString("\n")
	b.WriteString(title(fmt.Sprintf("Calls (%d)", len(m.graphCallees))) + "\n")
	b.WriteString(refLines(m.graphCallees, budget, w))
	return b.String()
}

// ---- Metrics tab ----

func (m Model) renderMetrics(w, h int) string {
	if m.status == nil {
		return title("Metrics") + "\n\n" + mutedStyle.Render("loading…")
	}
	if !m.status.Registered {
		return title("Metrics") + "\n\n" + mutedStyle.Render("no index yet — press ctrl+r to index, or run 'codemap index'")
	}
	barW := w - 44
	barW = clamp(barW, 16, 100)

	var b strings.Builder
	b.WriteString(title("Metrics") + "   ")
	b.WriteString(countStyle.Render(fmt.Sprintf("%d nodes · %d edges · %d files", m.status.Nodes, m.status.Edges, m.status.Files)))
	b.WriteString("\n\n")
	b.WriteString(sectionStyle.Render("By kind") + "\n")
	b.WriteString(barChart(m.status.Kinds, barW))
	b.WriteString("\n" + sectionStyle.Render("By language") + "\n")
	b.WriteString(barChart(m.status.Languages, barW))

	if len(m.graphHubs) > 0 {
		b.WriteString("\n" + sectionStyle.Render("Top hubs (most referenced)") + "\n")
		n := h - lipgloss.Height(b.String()) - 1
		n = clamp(n, 0, 15)
		for _, hub := range firstN(m.graphHubs, n) {
			fmt.Fprintf(&b, "  %4d  %s %s\n", hub.InDegree, padRight(truncate(displayName(hub.FQN, hub.Symbol), 32), 32),
				mutedStyle.Render(truncate(hub.File, w-44)))
		}
	}
	return b.String()
}

// ---- Impact tab ----

func (m Model) renderImpact(w, h int) string {
	var b strings.Builder
	b.WriteString(title("Impact") + "   " + m.impact.View() + "\n\n")
	rep := m.impactRep
	switch {
	case rep == nil:
		b.WriteString(mutedStyle.Render("type a symbol and press enter — see callers, blast radius, and which tests cover it"))
	case !rep.Found:
		b.WriteString(mutedStyle.Render(fmt.Sprintf("symbol %q not found", m.impactSymbol)))
	default:
		for _, l := range rep.Locations {
			b.WriteString(mutedStyle.Render("defined  ") + symStyle.Render(displayName(l.FQN, l.Symbol)) + "  " +
				mutedStyle.Render(fmt.Sprintf("%s:%d", l.File, l.StartLine)) + "\n")
		}
		cover := fmt.Sprintf("%d direct callers · %d in blast radius · %d covering tests",
			len(rep.DirectCallers), len(rep.BlastRadius), len(rep.Tests))
		if rep.Untested {
			cover += "   " + errorStyle.Render("⚠ untested")
		}
		b.WriteString("\n" + countStyle.Render(cover) + "\n\n")
		b.WriteString(sectionStyle.Render("Blast radius") + "\n")
		br := rep.BlastRadius
		budget := clamp(h-11, 1, 40)
		start := windowStart(m.impactSel, budget, len(br))
		end := clamp(start+budget, 0, len(br))
		if start > 0 {
			b.WriteString(mutedStyle.Render(fmt.Sprintf("  ▲ %d more above\n", start)))
		}
		for i := start; i < end; i++ {
			n := br[i]
			name := truncate(displayName(n.FQN, n.Symbol), 32)
			loc := truncate(fmt.Sprintf("%s:%d", n.File, n.StartLine), w-44)
			test := ""
			if n.Kind == "test" {
				test = " ✓"
			}
			if i == m.impactSel {
				plain := fmt.Sprintf(" ▸[%d] %s %s%s", n.Depth, padRight(name, 32), loc, test)
				b.WriteString(selectedStyle.Width(w).Render(truncate(plain, w)) + "\n")
			} else {
				marker := "  "
				if n.Kind == "test" {
					marker = symStyle.Render("✓ ")
				}
				fmt.Fprintf(&b, " %s[%d] %s %s\n", marker, n.Depth, padRight(name, 32), mutedStyle.Render(loc))
			}
		}
		if end < len(br) {
			b.WriteString(mutedStyle.Render(fmt.Sprintf("  ▼ %d more below", len(br)-end)))
		}
	}
	return b.String()
}

// ---- Search tab ----

func (m Model) renderSearch(w, h int) string {
	var b strings.Builder
	b.WriteString(title("Search") + "   " + m.search.View() + "\n\n")
	switch {
	case m.searchQuery == "":
		b.WriteString(mutedStyle.Render("semantic search by meaning — needs an embedded index (codemap index)"))
	case len(m.searchHits) == 0:
		b.WriteString(mutedStyle.Render("no matches"))
	default:
		hits := m.searchHits
		budget := clamp(h-6, 1, 50)
		start := windowStart(m.searchSel, budget, len(hits))
		end := clamp(start+budget, 0, len(hits))
		if start > 0 {
			b.WriteString(mutedStyle.Render(fmt.Sprintf("  ▲ %d more above\n", start)))
		}
		for i := start; i < end; i++ {
			hit := hits[i]
			name := truncate(displayName(hit.FQN, hit.Symbol), 32)
			loc := truncate(fmt.Sprintf("%s:%d", hit.File, hit.StartLine), w-48)
			if i == m.searchSel {
				plain := fmt.Sprintf(" ▸ %.3f  %s %s", hit.Score, padRight(name, 32), loc)
				b.WriteString(selectedStyle.Width(w).Render(truncate(plain, w)) + "\n")
			} else {
				fmt.Fprintf(&b, "   %s  %s %s\n", countStyle.Render(fmt.Sprintf("%.3f", hit.Score)),
					symStyle.Render(padRight(name, 32)), mutedStyle.Render(loc))
			}
		}
		if end < len(hits) {
			b.WriteString(mutedStyle.Render(fmt.Sprintf("  ▼ %d more below", len(hits)-end)))
		}
	}
	return b.String()
}

// ---- helpers ----

func title(s string) string { return panelTitleStyle.Render(s) }

// displayName prefers the fully-qualified name (which distinguishes same-named
// symbols across packages, e.g. graph.Store.Close vs app.Session.Close).
func displayName(fqn, symbol string) string {
	if fqn != "" {
		return fqn
	}
	return symbol
}

func refLines(refs []app.SymbolRef, budget, w int) string {
	if len(refs) == 0 {
		return mutedStyle.Render("  (none)") + "\n"
	}
	var b strings.Builder
	for _, r := range firstN(refs, budget) {
		b.WriteString("  " + truncate(fmt.Sprintf("%s  %s:%d", displayName(r.FQN, r.Symbol), r.File, r.StartLine), w-2) + "\n")
	}
	if len(refs) > budget {
		fmt.Fprintf(&b, "  %s\n", mutedStyle.Render(fmt.Sprintf("… +%d more", len(refs)-budget)))
	}
	return b.String()
}

func spread(left, right string, width int) string {
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return left
	}
	return left + strings.Repeat(" ", gap) + right
}

func barChart(counts map[string]int, barW int) string {
	if len(counts) == 0 {
		return mutedStyle.Render("  (none)") + "\n"
	}
	keys := make([]string, 0, len(counts))
	max := 0
	for k, v := range counts {
		keys = append(keys, k)
		if v > max {
			max = v
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if counts[keys[i]] != counts[keys[j]] {
			return counts[keys[i]] > counts[keys[j]]
		}
		return keys[i] < keys[j]
	})
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "  %s %s %d\n", padRight(k, 12), bar(counts[k], max, barW), counts[k])
	}
	return b.String()
}

func bar(n, max, width int) string {
	if max <= 0 || width <= 0 {
		return ""
	}
	filled := n * width / max
	if filled < 1 && n > 0 {
		filled = 1
	}
	if filled > width {
		filled = width
	}
	return barStyle.Render(strings.Repeat("█", filled)) + strings.Repeat(" ", width-filled)
}

func padRight(s string, w int) string {
	if lipgloss.Width(s) >= w {
		return s
	}
	return s + strings.Repeat(" ", w-lipgloss.Width(s))
}

func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	r := []rune(s)
	if w == 1 {
		return "…"
	}
	if len(r) > w-1 {
		r = r[:w-1]
	}
	return string(r) + "…"
}

func maxStart(n, budget int) int {
	if n > budget {
		return n - budget
	}
	return 0
}

// windowStart returns a scroll offset that keeps the selected index visible
// within a window of the given budget.
func windowStart(sel, budget, n int) int {
	if budget >= n || sel < budget {
		return 0
	}
	start := sel - budget + 1
	if start > maxStart(n, budget) {
		start = maxStart(n, budget)
	}
	return start
}

func clamp(n, lo, hi int) int {
	if n < lo {
		return lo
	}
	if n > hi {
		return hi
	}
	return n
}

func firstN[T any](s []T, n int) []T {
	if n < 0 {
		n = 0
	}
	if n > len(s) {
		n = len(s)
	}
	return s[:n]
}
