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
	content := m.body(m.width, bodyH)
	switch {
	case m.showHelp:
		content = renderHelp()
	case m.srcView:
		content = m.renderSource(m.width, bodyH)
	}
	body := lipgloss.NewStyle().Width(m.width).Height(bodyH).MaxHeight(bodyH).Render(content)

	return lipgloss.JoinVertical(lipgloss.Left, header, tabs, body, footer)
}

// renderHelp is the full-screen keybinding overlay (toggled with `?`).
func renderHelp() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("codemap studio — keys") + "\n\n")
	row := func(k, d string) {
		b.WriteString("  " + symStyle.Render(padRight(k, 26)) + mutedStyle.Render(d) + "\n")
	}
	b.WriteString(sectionStyle.Render("Global") + "\n")
	row("1–4 / tab / shift+tab", "switch tabs")
	row("ctrl+s", "view the selected symbol's source")
	row("ctrl+r", "reindex (structure-only) and refresh")
	row("? / esc", "toggle this help")
	row("ctrl+c", "quit (q also quits on Graph/Metrics)")
	b.WriteString("\n" + sectionStyle.Render("Graph") + "\n")
	row("↑/↓ · pgup/pgdn · home/end", "move the hub selection")
	row("→/l · ←/h", "focus the callers/calls pane · back to hubs")
	row("enter", "hubs → Impact · refs → re-center (walk)")
	row("backspace", "step back along the walk")
	row("s · p", "view source · precise relations (gopls)")
	b.WriteString("\n" + sectionStyle.Render("Metrics / Impact / Search") + "\n")
	row("↑/↓ · pgup/pgdn", "move the selection")
	row("enter", "drill the selection into Impact")
	row("type (Impact/Search)", "edit the query; enter runs it")
	return b.String()
}

// renderSource is the full-screen, scrollable source overlay (opened with `s`).
func (m Model) renderSource(w, h int) string {
	var b strings.Builder
	vp := h - 2
	if vp < 1 {
		vp = 1
	}
	n := len(m.srcLines)
	start := clamp(m.srcScroll, 0, n)
	end := clamp(start+vp, 0, n)
	hdr := symStyle.Render(truncate(m.srcTitle, w-22))
	if n > vp { // scrollable → show where we are
		hdr += "  " + mutedStyle.Render(fmt.Sprintf("(lines %d–%d of %d)", start+1, end, n))
	}
	b.WriteString(hdr + "\n\n")
	for i := start; i < end; i++ {
		gutter := mutedStyle.Render(fmt.Sprintf("%4d ", i+1))
		b.WriteString(gutter + truncate(m.srcLines[i], w-5) + "\n")
	}
	return b.String()
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
	switch {
	case m.showHelp:
		return spread(mutedStyle.Render("? / esc close"), m.statusMsg, m.width)
	case m.srcView:
		hint = "↑/↓ scroll · pgup/pgdn · g/G top/bottom · esc/q close · ctrl+c quit"
		status := m.statusMsg
		if m.errMsg != "" {
			status = errorStyle.Render(m.errMsg)
		}
		return spread(mutedStyle.Render(hint), status, m.width)
	}
	switch m.active {
	case tabGraph:
		if m.graphFocus == focusRefs {
			hint = "↑/↓ ref · enter re-center · s source · ⌫ back · ← hubs · ctrl+c quit"
		} else {
			hint = "↑/↓ hub · → walk · enter → impact · s source · p precise · ctrl+c quit"
		}
	case tabSearch:
		hint = "type · enter search/open · ↑/↓ select · ctrl+s source · tab · ctrl+c quit"
	case tabImpact:
		hint = "type symbol · enter run/open · ↑/↓ select · ctrl+s source · tab · ctrl+c quit"
	default: // metrics
		hint = "↑/↓ select · enter → impact · ctrl+s source · ctrl+r reindex · ctrl+c quit"
	}
	hint += " · ? help"
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
		switch {
		case i == m.graphSel && m.graphFocus == focusHubs:
			b.WriteString(selectedStyle.Width(w).Render(line))
		case i == m.graphSel:
			b.WriteString(dimSelectedStyle.Width(w).Render(line))
		default:
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
	hdr := symStyle.Render(displayName(m.graphCenter.fqn, m.graphCenter.sym))
	mark := ""
	if m.graphPrecise {
		hdr += "  " + countStyle.Render("precise · gopls")
		mark = " · gopls"
	}
	if len(m.graphStack) > 0 {
		hdr += "  " + mutedStyle.Render(fmt.Sprintf("· depth %d (⌫ back)", len(m.graphStack)))
	}
	if n := len(m.graphAnnotations); n > 0 { // at-a-glance: this node has pinned knowledge
		hdr += "  " + countStyle.Render(fmt.Sprintf("· ⟐ %d", n))
	}
	b.WriteString(hdr + "\n\n")
	annShown := clamp(len(m.graphAnnotations), 0, 3)
	budget := (h - 9 - annShown) / 2
	if budget < 1 {
		budget = 1
	}
	b.WriteString(title(fmt.Sprintf("Called by (%d)%s", len(m.graphCallers), mark)) + "\n")
	b.WriteString(m.refBlock(m.graphCallers, 0, budget, w))
	b.WriteString("\n")
	b.WriteString(title(fmt.Sprintf("Calls (%d)%s", len(m.graphCallees), mark)) + "\n")
	b.WriteString(m.refBlock(m.graphCallees, len(m.graphCallers), budget, w))
	if m.graphFocus == focusRefs {
		if refs := m.graphRefs(); m.graphRefSel < len(refs) {
			if p := detailPreview(refs[m.graphRefSel].Signature, refs[m.graphRefSel].Doc, w); p != "" {
				b.WriteString("\n" + p)
			}
		}
	}
	for i, a := range m.graphAnnotations { // pinned notes/data on the centered node
		if i >= annShown {
			b.WriteString(mutedStyle.Render(fmt.Sprintf("  ⟐ +%d more", len(m.graphAnnotations)-annShown)))
			break
		}
		line := a.Source
		if a.Note != "" {
			line += ": " + a.Note
		}
		if a.Data != "" {
			line += "  " + strings.Join(strings.Fields(a.Data), " ")
		}
		b.WriteString(countStyle.Render("⟐ ") + truncate(line, w-2) + "\n")
	}
	return b.String()
}

// refBlock renders one relation list (callers or callees). base is the block's
// offset into the combined refs list so the right pane's selection (graphRefSel)
// highlights the correct row when focused, with windowing to keep it visible.
func (m Model) refBlock(refs []app.SymbolRef, base, budget, w int) string {
	if len(refs) == 0 {
		return mutedStyle.Render("  (none)") + "\n"
	}
	localSel := -1
	if m.graphFocus == focusRefs && m.graphRefSel >= base && m.graphRefSel < base+len(refs) {
		localSel = m.graphRefSel - base
	}
	if budget < 1 {
		budget = 1
	}
	start := 0
	if localSel >= 0 {
		start = windowStart(localSel, budget, len(refs))
	}
	end := clamp(start+budget, 0, len(refs))
	var b strings.Builder
	if start > 0 {
		b.WriteString(mutedStyle.Render(fmt.Sprintf("  ▲ %d more\n", start)))
	}
	for i := start; i < end; i++ {
		r := refs[i]
		line := fmt.Sprintf("%s  %s:%d", displayName(r.FQN, r.Symbol), r.File, r.StartLine)
		if i == localSel {
			b.WriteString(selectedStyle.Width(w).Render(truncate(" ▸ "+line, w)) + "\n")
		} else {
			b.WriteString("  " + truncate(line, w-2) + "\n")
		}
	}
	if end < len(refs) {
		b.WriteString(mutedStyle.Render(fmt.Sprintf("  ▼ %d more\n", len(refs)-end)))
	}
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

	header := title("Metrics") + "   " +
		countStyle.Render(fmt.Sprintf("%d nodes · %d edges · %d files", m.status.Nodes, m.status.Edges, m.status.Files))

	// Two columns under the header: distributions on the left, the graph's
	// extremes (most-referenced hubs vs unreferenced dead-code) on the right.
	colH := h - 2
	if colH < 1 {
		colH = 1
	}
	leftW := w / 2
	if leftW > 52 {
		leftW = 52
	}
	rightW := w - leftW - 3
	if rightW < 16 {
		rightW = 16
	}

	left := lipgloss.NewStyle().Width(leftW).Height(colH).Render(m.metricsBars(leftW))
	right := lipgloss.NewStyle().Width(rightW).Height(colH).Render(m.metricsLists(rightW, colH))
	div := dividerStyle.Render(strings.TrimRight(strings.Repeat("│\n", colH), "\n"))
	cols := lipgloss.JoinHorizontal(lipgloss.Top, left, " ", div, " ", right)
	return header + "\n\n" + cols
}

func (m Model) metricsBars(w int) string {
	// line = 2-space indent + 12-char label + space + bar + space + count digits,
	// so keep the bar narrow enough that the count doesn't wrap the column.
	barW := clamp(w-22, 8, 80)
	var b strings.Builder
	b.WriteString(sectionStyle.Render("By kind") + "\n")
	b.WriteString(barChart(m.status.Kinds, barW))
	b.WriteString("\n" + sectionStyle.Render("By language") + "\n")
	b.WriteString(barChart(m.status.Languages, barW))
	return b.String()
}

// metricsLists shows the two ends of the call graph: the load-bearing hubs and
// the dead-code candidates. Rows are selectable (metricsSel spans both lists,
// hubs first), with windowing so the selection stays visible.
func (m Model) metricsLists(w, h int) string {
	var b strings.Builder
	budget := clamp((h-4)/2, 1, 30)
	nHubs := len(m.graphHubs)

	hubRows := make([]string, nHubs)
	for i, hub := range m.graphHubs {
		hubRows[i] = fmt.Sprintf("  %4d  %s", hub.InDegree, truncate(displayName(hub.FQN, hub.Symbol), w-8))
	}
	hubSel := -1
	if m.metricsSel < nHubs {
		hubSel = m.metricsSel
	}
	metricBlock(&b, fmt.Sprintf("Top hubs — most referenced (%d)", nHubs), hubRows, hubSel, budget, w)

	b.WriteString("\n")
	orphRows := make([]string, len(m.orphans))
	for i, o := range m.orphans {
		orphRows[i] = fmt.Sprintf("  %s  %s", truncate(displayName(o.FQN, o.Symbol), w-14),
			fmt.Sprintf("%s:%d", o.File, o.StartLine))
	}
	orphSel := -1
	if m.metricsSel >= nHubs && m.metricsSel < nHubs+len(m.orphans) {
		orphSel = m.metricsSel - nHubs
	}
	metricBlock(&b, fmt.Sprintf("Dead-code candidates — no callers (%d)", len(m.orphans)), orphRows, orphSel, budget, w)
	return b.String()
}

// metricBlock renders a titled list of plain rows, highlighting localSel (or -1)
// and windowing to keep it visible within budget lines.
func metricBlock(b *strings.Builder, title string, rows []string, localSel, budget, w int) {
	b.WriteString(sectionStyle.Render(title) + "\n")
	if len(rows) == 0 {
		b.WriteString(mutedStyle.Render("  (none)") + "\n")
		return
	}
	start := 0
	if localSel >= 0 {
		start = windowStart(localSel, budget, len(rows))
	}
	end := clamp(start+budget, 0, len(rows))
	if start > 0 {
		b.WriteString(mutedStyle.Render(fmt.Sprintf("  ▲ %d more\n", start)))
	}
	for i := start; i < end; i++ {
		if i == localSel {
			b.WriteString(selectedStyle.Width(w).Render(truncate(rows[i], w)) + "\n")
		} else {
			b.WriteString(rows[i] + "\n")
		}
	}
	if end < len(rows) {
		b.WriteString(mutedStyle.Render(fmt.Sprintf("  ▼ %d more\n", len(rows)-end)))
	}
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
			b.WriteString(mutedStyle.Render("defined  ") + symStyle.Render(displayName(l.FQN, l.Symbol)) +
				mutedStyle.Render(fmt.Sprintf("  %s:%d", l.File, l.StartLine)) + "\n")
			if sig := strings.Join(strings.Fields(l.Signature), " "); sig != "" {
				b.WriteString("  " + countStyle.Render(truncate(sig, w-2)) + "\n")
			}
		}
		if d := docFirstLine(firstDoc(rep.Locations)); d != "" {
			b.WriteString("  " + mutedStyle.Render(truncate(d, w-2)) + "\n")
		}
		cover := fmt.Sprintf("%d direct callers · %d in blast radius · %d covering tests",
			len(rep.DirectCallers), len(rep.BlastRadius), len(rep.Tests))
		if rep.Untested {
			cover += "   " + errorStyle.Render("⚠ untested")
		}
		b.WriteString("\n" + countStyle.Render(cover) + "\n")
		// Name the covering tests outright (the "what do I run" answer), not just
		// the ✓ markers buried in the blast radius below.
		reserve := 14
		if len(rep.Tests) > 0 {
			names := make([]string, len(rep.Tests))
			for i, t := range rep.Tests {
				names[i] = displayName(t.FQN, t.Symbol)
			}
			b.WriteString(mutedStyle.Render("covered by ") + symStyle.Render(truncate(strings.Join(names, ", "), w-12)) + "\n")
			reserve++
		}
		for _, a := range rep.Annotations { // pinned notes/data, inline
			line := a.Source
			if a.Note != "" {
				line += ": " + a.Note
			}
			if a.Data != "" {
				line += "  " + strings.Join(strings.Fields(a.Data), " ")
			}
			b.WriteString(countStyle.Render("⟐ ") + truncate(line, w-2) + "\n")
			reserve++
		}
		b.WriteString("\n")
		b.WriteString(sectionStyle.Render("Blast radius") + "\n")
		br := rep.BlastRadius
		budget := clamp(h-reserve, 1, 40)
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
			b.WriteString(mutedStyle.Render(fmt.Sprintf("  ▼ %d more below", len(br)-end)) + "\n")
		}
		if m.impactSel < len(br) {
			if p := detailPreview(br[m.impactSel].Signature, br[m.impactSel].Doc, w); p != "" {
				b.WriteString("\n" + p)
			}
		}
	}
	return b.String()
}

// ---- Search tab ----

func (m Model) renderSearch(w, h int) string {
	var b strings.Builder
	hdr := title("Search")
	if m.searchMode != "" {
		hdr += "  " + countStyle.Render(m.searchMode+" mode")
	}
	b.WriteString(hdr + "   " + m.search.View() + "\n\n")
	switch {
	case m.searchQuery == "":
		b.WriteString(mutedStyle.Render("search by meaning (semantic, needs an embedded index) or by name — type and press enter"))
	case len(m.searchHits) == 0:
		b.WriteString(mutedStyle.Render("no matches"))
	default:
		hits := m.searchHits
		budget := clamp(h-9, 1, 50)
		start := windowStart(m.searchSel, budget, len(hits))
		end := clamp(start+budget, 0, len(hits))
		if start > 0 {
			b.WriteString(mutedStyle.Render(fmt.Sprintf("  ▲ %d more above\n", start)))
		}
		for i := start; i < end; i++ {
			hit := hits[i]
			ann := "  "
			if len(hit.Annotations) > 0 { // pinned-knowledge marker
				ann = "⟐ "
			}
			name := truncate(displayName(hit.FQN, hit.Symbol), 32)
			loc := truncate(fmt.Sprintf("%s:%d", hit.File, hit.StartLine), w-50)
			if i == m.searchSel {
				plain := fmt.Sprintf(" ▸ %s%.3f  %s %s", ann, hit.Score, padRight(name, 32), loc)
				b.WriteString(selectedStyle.Width(w).Render(truncate(plain, w)) + "\n")
			} else {
				fmt.Fprintf(&b, "   %s%s  %s %s\n", ann, countStyle.Render(fmt.Sprintf("%.3f", hit.Score)),
					symStyle.Render(padRight(name, 32)), mutedStyle.Render(loc))
			}
		}
		if end < len(hits) {
			b.WriteString(mutedStyle.Render(fmt.Sprintf("  ▼ %d more below", len(hits)-end)) + "\n")
		}
		if m.searchSel < len(hits) {
			if p := detailPreview(hits[m.searchSel].Signature, hits[m.searchSel].Doc, w); p != "" {
				b.WriteString("\n" + p)
			}
		}
	}
	return b.String()
}

// ---- helpers ----

func title(s string) string { return panelTitleStyle.Render(s) }

// detailPreview renders the selected item's signature and the first line of its
// docstring, so a pane is self-contained — you see what a symbol is AND what it
// does without opening the file. Multi-line signatures are collapsed; empty when
// there's neither. Returns up to two lines (signature, then a muted doc line).
func detailPreview(sig, doc string, w int) string {
	sig = strings.Join(strings.Fields(sig), " ")
	var lines []string
	if sig != "" {
		lines = append(lines, mutedStyle.Render("⟩ ")+symStyle.Render(truncate(sig, w-2)))
	}
	if d := docFirstLine(doc); d != "" {
		lines = append(lines, mutedStyle.Render(truncate("  "+d, w)))
	}
	return strings.Join(lines, "\n")
}

// docFirstLine returns the first non-empty line of a docstring, trimmed.
func docFirstLine(doc string) string {
	for _, ln := range strings.Split(doc, "\n") {
		if ln = strings.TrimSpace(ln); ln != "" {
			return ln
		}
	}
	return ""
}

// firstDoc returns the first non-empty docstring among the given refs.
func firstDoc(refs []app.SymbolRef) string {
	for _, r := range refs {
		if r.Doc != "" {
			return r.Doc
		}
	}
	return ""
}

// displayName prefers the fully-qualified name (which distinguishes same-named
// symbols across packages, e.g. graph.Store.Close vs app.Session.Close).
func displayName(fqn, symbol string) string {
	if fqn != "" {
		return fqn
	}
	return symbol
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
