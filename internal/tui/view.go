package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// View renders the studio (alt-screen).
func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	return v
}

func (m Model) render() string {
	if m.width == 0 {
		return "codemap studio\n\nloading…"
	}
	return lipgloss.JoinVertical(lipgloss.Left,
		m.header(),
		m.tabBar(),
		"",
		m.body(),
		"",
		m.footer(),
	)
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

func (m Model) body() string {
	switch m.active {
	case tabGraph:
		return m.renderGraph()
	case tabMetrics:
		return m.renderMetrics()
	case tabImpact:
		return m.renderImpact()
	case tabSearch:
		return m.renderSearch()
	}
	return ""
}

func (m Model) footer() string {
	keys := mutedStyle.Render("tab/shift+tab switch · enter run · ctrl+c quit")
	status := m.statusMsg
	if m.errMsg != "" {
		status = errorStyle.Render(m.errMsg)
	}
	return spread(keys, status, m.width)
}

func (m Model) renderGraph() string {
	return panel("Graph", mutedStyle.Render(
		"node-link code map — coming in a later iteration.\nFor now, use Metrics, Impact, and Search."))
}

func (m Model) renderMetrics() string {
	if m.status == nil {
		return panel("Metrics", mutedStyle.Render("loading…"))
	}
	if !m.status.Registered {
		return panel("Metrics", mutedStyle.Render("no index yet — run 'codemap index' in this project"))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "nodes %d   edges %d   files %d\n\n", m.status.Nodes, m.status.Edges, m.status.Files)
	b.WriteString(sectionStyle.Render("By kind") + "\n")
	b.WriteString(barChart(m.status.Kinds))
	b.WriteString("\n" + sectionStyle.Render("By language") + "\n")
	b.WriteString(barChart(m.status.Languages))
	return panel("Metrics", b.String())
}

func (m Model) renderImpact() string {
	var b strings.Builder
	b.WriteString("Callers of: " + m.impact.View() + "\n\n")
	switch {
	case m.impactSymbol == "":
		b.WriteString(mutedStyle.Render("type a symbol and press enter to see what calls it"))
	case len(m.impactRefs) == 0:
		b.WriteString(mutedStyle.Render("no callers found for " + m.impactSymbol))
	default:
		for _, r := range m.impactRefs {
			fmt.Fprintf(&b, "  %s  %s\n", symStyle.Render(r.Symbol),
				mutedStyle.Render(fmt.Sprintf("%s:%d", r.File, r.StartLine)))
		}
	}
	return panel("Impact", b.String())
}

func (m Model) renderSearch() string {
	var b strings.Builder
	b.WriteString(m.search.View() + "\n\n")
	switch {
	case m.searchQuery == "":
		b.WriteString(mutedStyle.Render("semantic search — needs an embedded index (codemap index)"))
	case len(m.searchHits) == 0:
		b.WriteString(mutedStyle.Render("no matches"))
	default:
		for _, h := range m.searchHits {
			fmt.Fprintf(&b, "  %.2f  %s  %s\n", h.Score, symStyle.Render(h.Symbol),
				mutedStyle.Render(fmt.Sprintf("%s:%d", h.File, h.StartLine)))
		}
	}
	return panel("Search", b.String())
}

// ---- helpers ----

func panel(title, body string) string {
	return panelTitleStyle.Render(title) + "\n\n" + body
}

func spread(left, right string, width int) string {
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return left
	}
	return left + strings.Repeat(" ", gap) + right
}

func barChart(counts map[string]int) string {
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
		fmt.Fprintf(&b, "  %-12s %s %d\n", k, bar(counts[k], max, 24), counts[k])
	}
	return b.String()
}

func bar(n, max, width int) string {
	if max <= 0 {
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
