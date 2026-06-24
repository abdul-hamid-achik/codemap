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

	frame := lipgloss.JoinVertical(lipgloss.Left, header, tabs, body, footer)
	// Hard guarantee that nothing exceeds the terminal width at any size: a single
	// over-wide line (e.g. a long footer hint or a two-column body on a narrow
	// terminal) would otherwise make JoinVertical pad every line to match and blow
	// the whole frame past the screen. MaxWidth is ANSI-aware, so it clips styled
	// content cleanly.
	return lipgloss.NewStyle().MaxWidth(m.width).Render(frame)
}

// renderHelp is the full-screen keybinding overlay (toggled with `?`).
func renderHelp() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("codemap studio — keys") + "\n\n")
	row := func(k, d string) {
		b.WriteString("  " + symStyle.Render(padRight(k, 24)) + mutedStyle.Render(d) + "\n")
	}
	b.WriteString(sectionStyle.Render("Global") + "\n")
	row("1–4 / tab / shift+tab", "switch tabs")
	row("ctrl+g", "open the selection in the Graph walker")
	row("ctrl+s", "view the selected symbol's source")
	row("ctrl+o", "orient: context card (callers/callees/tests/blast)")
	row("ctrl+r", "reindex and refresh (keeps the project's precision)")
	row("alt+← / alt+→", "back / forward (global history — restores the bar you came from)")
	row("esc", "step back one level (also closes help/overlay)")
	row("?", "toggle this help")
	row("ctrl+c", "quit (q also quits on Graph/Metrics)")
	b.WriteString("\n" + sectionStyle.Render("Graph") + "\n")
	row("↑/↓ · k/j", "move the hub selection (also pgup/pgdn · home/end)")
	row("→/l · ←/h", "focus the callers/calls pane · back to hubs")
	row("enter", "hubs → Impact · refs → re-center (walk)")
	row("backspace", "step back along the walk")
	row("s · p", "view source · precise relations (gopls)")
	b.WriteString("\n" + sectionStyle.Render("Metrics") + "\n")
	row("↑/↓ · k/j", "move the selection (also pgup/pgdn · home/end)")
	row("enter", "drill the selected symbol into Impact")
	b.WriteString("\n" + sectionStyle.Render("Impact / Search") + "\n")
	row("type", "edit the query; enter runs it")
	row("↑/↓ · pgup/pgdn", "move the result selection")
	row("enter", "run the query, or open the selected hit")
	b.WriteString("\n" + sectionStyle.Render("Source / context overlay (s · ctrl+s · ctrl+o)") + "\n")
	row("↑/↓ · k/j", "scroll (also pgup/pgdn · g/G top & bottom)")
	row("esc / q", "close the overlay")
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
		if m.srcGutter { // source body: line numbers; the context card has none
			ln := i + 1
			if m.srcFirstLine > 0 { // show real file line numbers, not 1-based within the def
				ln = i + m.srcFirstLine
			}
			gutter := mutedStyle.Render(fmt.Sprintf("%4d ", ln))
			if m.srcHighlight { // chroma ANSI — clip ANSI-aware (rune truncate would mangle escapes)
				b.WriteString(gutter + lipgloss.NewStyle().MaxWidth(w-5).Render(m.srcLines[i]) + "\n")
			} else {
				b.WriteString(gutter + truncate(m.srcLines[i], w-5) + "\n")
			}
		} else {
			// the context card carries lipgloss styling, so clip ANSI-aware
			// (rune-based truncate would miscount the escape codes).
			b.WriteString(lipgloss.NewStyle().MaxWidth(w).Render(m.srcLines[i]) + "\n")
		}
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
	// Surface index drift so you know the graph is behind the code — ctrl+r refreshes.
	if m.stale != nil && m.stale.Any() {
		right += "  " + warnStyle.Render(fmt.Sprintf("⚠ stale %d/%d/%d (ctrl+r)",
			m.stale.Changed, m.stale.New, m.stale.Deleted))
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
	// Each tab has a rich hint and a compact fallback. The rich one shows on
	// normal-width terminals; when it wouldn't fit, the compact one (terser
	// labels, drops the universal ctrl+c — `?` documents everything) is used so
	// the footer still fits ~80 cols with `? help` intact. Below that, the
	// render() MaxWidth clamp is the final backstop.
	var compact string
	switch m.active {
	case tabGraph:
		if m.graphFocus == focusRefs {
			hint = "↑/↓ ref · enter re-center · s source · ⌫ back · ← hubs · ctrl+c quit"
			compact = "↑/↓ · enter re-center · s src · ⌫ back · ← hubs"
		} else {
			hint = "↑/↓ hub · → walk · enter → impact · s source · p precise · ctrl+c quit"
			compact = "↑/↓ · → walk · enter impact · s src · p precise"
		}
	case tabSearch:
		hint = "type · enter search/open · ↑/↓ select · ctrl+g graph · ctrl+s source · tab · ctrl+c quit"
		compact = "type · enter · ↑/↓ · ctrl+g graph · ctrl+s src · tab"
	case tabImpact:
		hint = "type symbol · enter run/open · ↑/↓ select · ctrl+g graph · ctrl+s source · tab · ctrl+c quit"
		compact = "type · enter · ↑/↓ · ctrl+g graph · ctrl+s src · tab"
	default: // metrics
		hint = "↑/↓ select · enter → impact · ctrl+g graph · ctrl+s source · ctrl+r reindex · ctrl+c quit"
		compact = "↑/↓ · enter impact · ctrl+g graph · ctrl+s src · ctrl+r reindex"
	}
	hint += " · ? help"
	compact += " · ? help"
	if lipgloss.Width(hint) > m.width {
		hint = compact
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
		return m.emptyGraphHint()
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
	n := len(m.graphHubs)
	b.WriteString(title(fmt.Sprintf("Hubs (%d)", n)) + "\n")
	avail := h - 1 // minus the title line
	if avail < 1 {
		avail = 1
	}
	// When the list overflows the window, reserve two lines for the ▲/▼ "N more"
	// scroll indicators so you can tell there are hubs above/below — otherwise use
	// the full height. Mirrors the Metrics tab's lists.
	budget := avail
	if n > avail {
		if budget = avail - 2; budget < 1 {
			budget = 1
		}
	}
	start := windowStart(m.graphSel, budget, n)
	end := clamp(start+budget, 0, n)
	if start > 0 {
		b.WriteString(mutedStyle.Render(fmt.Sprintf("  ▲ %d more", start)) + "\n")
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
	if end < n {
		b.WriteString(mutedStyle.Render(fmt.Sprintf("  ▼ %d more", n-end)) + "\n")
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
	} else if m.status != nil && m.status.PreciseEdges > 0 {
		// Relations come from the --precise (go/types) index, so they're already exact.
		hdr += "  " + countStyle.Render("precise · index")
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

// notIndexedHint is the shared cold-start message: when the project hasn't been
// indexed yet, every tab shows it instead of inviting an action that can't
// succeed (e.g. Impact/Search prompting for input that would only return nothing).
func notIndexedHint(tabName string) string {
	return title(tabName) + "\n\n" + mutedStyle.Render("no index yet — press ctrl+r to index, or run 'codemap index'")
}

// emptyGraphHint explains an empty hub list. It distinguishes "not indexed at
// all" from "indexed but no call graph" — the latter is the normal state for a
// TypeScript project indexed *without* --precise (TS call edges come only from
// the precise pass), where the generic "no index yet" message would be wrong and
// confusing (the project IS indexed; it just has no resolved calls to rank).
func (m Model) emptyGraphHint() string {
	if m.status == nil {
		return title("Graph") + "\n\n" + mutedStyle.Render("loading…")
	}
	if !m.status.Registered || m.status.Nodes == 0 {
		return notIndexedHint("Graph")
	}
	// Keep each line short so it doesn't soft-wrap inside the body (which would
	// split the server name and read awkwardly).
	lines := []string{fmt.Sprintf("indexed — %d nodes — but no call graph yet (no resolved calls to rank).", m.status.Nodes), ""}
	if m.status.Languages["typescript"] > 0 {
		// ctrl+r reindexes structure-only, which won't add TS call edges; --precise will.
		lines = append(lines,
			"TypeScript call edges come only from the precise pass.",
			"Reindex with 'codemap index --precise' (needs typescript-language-server).")
	} else {
		lines = append(lines, "Reindex with 'codemap index --precise' to resolve calls exactly.")
	}
	return title("Graph") + "\n\n" + mutedStyle.Render(strings.Join(lines, "\n"))
}

func (m Model) renderMetrics(w, h int) string {
	if m.status == nil {
		return title("Metrics") + "\n\n" + mutedStyle.Render("loading…")
	}
	if !m.status.Registered {
		return notIndexedHint("Metrics")
	}

	vec := "no embeddings — name search only"
	if m.status.Vectors > 0 {
		vec = fmt.Sprintf("%d embedded · semantic search ready", m.status.Vectors)
	}
	edges := fmt.Sprintf("%d edges", m.status.Edges)
	if m.status.PreciseEdges > 0 { // go/types-resolved; vs name-based default
		edges = fmt.Sprintf("%d edges (%d precise)", m.status.Edges, m.status.PreciseEdges)
	}
	// Truncate the plain count text (with an ellipsis) before styling so the
	// header fits a narrow terminal — `title("Metrics")` is 7 wide plus 3 spaces.
	countTxt := fmt.Sprintf("%d nodes · %s · %d files · %s", m.status.Nodes, edges, m.status.Files, vec)
	header := title("Metrics") + "   " + countStyle.Render(truncate(countTxt, clamp(w-10, 8, 240)))

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
	anyShared := false
	for i, hub := range m.graphHubs {
		mark, nameBudget := "", w-8
		if hub.SharedName > 1 { // in-degree inflated by name-based fan-out across same-named defs
			anyShared = true
			mark = fmt.Sprintf("  ⚠×%d", hub.SharedName)
			nameBudget = w - 14
		}
		hubRows[i] = fmt.Sprintf("  %4d  %s%s", hub.InDegree, truncate(displayName(hub.FQN, hub.Symbol), nameBudget), mark)
	}
	hubSel := -1
	if m.metricsSel < nHubs {
		hubSel = m.metricsSel
	}
	hubTitle := fmt.Sprintf("Top hubs — most referenced (%d)", nHubs)
	if anyShared {
		hubTitle += "  ⚠=name-inflated"
	}
	metricBlock(&b, hubTitle, hubRows, hubSel, budget, w)

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
		// Every row is clipped to the column width — not just the selected one — so
		// a long FQN or file path can't push the column (and the whole frame) past
		// its allotment on a narrow terminal.
		if i == localSel {
			b.WriteString(selectedStyle.Width(w).Render(truncate(rows[i], w)) + "\n")
		} else {
			b.WriteString(truncate(rows[i], w) + "\n")
		}
	}
	if end < len(rows) {
		b.WriteString(mutedStyle.Render(fmt.Sprintf("  ▼ %d more\n", len(rows)-end)))
	}
}

// ---- Impact tab ----

func (m Model) renderImpact(w, h int) string {
	if m.status != nil && !m.status.Registered {
		return notIndexedHint("Impact")
	}
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
		if rep.Untested && rep.Resolution == "" { // don't flag "untested" when the call graph is just unresolved
			cover += "   " + errorStyle.Render("⚠ untested")
		}
		b.WriteString("\n" + countStyle.Render(cover) + "\n")
		if rep.Resolution != "" { // TS/JS/Python without --precise: the empty counts are unresolved, not absent
			b.WriteString(errorStyle.Render("⚠ ") + mutedStyle.Render(truncate(rep.Resolution, w-2)) + "\n")
		}
		if rep.Note != "" { // ambiguous name: the counts above merge same-named defs
			b.WriteString(errorStyle.Render("⚠ ") + mutedStyle.Render(truncate(rep.Note, w-2)) + "\n")
		}
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
			b.WriteString(mutedStyle.Render(fmt.Sprintf("  ▲ %d more\n", start)))
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
			b.WriteString(mutedStyle.Render(fmt.Sprintf("  ▼ %d more", len(br)-end)) + "\n")
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
	if m.status != nil && !m.status.Registered {
		return notIndexedHint("Search")
	}
	var b strings.Builder
	hdr := title("Search")
	if m.searchMode != "" {
		badge := m.searchMode + " mode"
		if m.searchMode == "name" && m.status != nil && m.status.Vectors == 0 {
			badge += " (no embeddings)" // why it isn't semantic; see Metrics for how to enable
		}
		if n := len(m.searchHits); n > 0 { // how many matches, so you needn't scroll to find out
			unit := "results"
			if n == 1 {
				unit = "result"
			}
			badge += fmt.Sprintf(" · %d %s", n, unit)
		}
		hdr += "  " + countStyle.Render(badge)
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
			b.WriteString(mutedStyle.Render(fmt.Sprintf("  ▲ %d more\n", start)))
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
			b.WriteString(mutedStyle.Render(fmt.Sprintf("  ▼ %d more", len(hits)-end)) + "\n")
		}
		if m.searchSel < len(hits) {
			sel := hits[m.searchSel]
			if p := detailPreview(sel.Signature, sel.Doc, w); p != "" {
				b.WriteString("\n" + p)
			}
			for i, a := range sel.Annotations { // the selected hit's pinned notes/data
				if i >= 2 {
					break
				}
				line := a.Source
				if a.Note != "" {
					line += ": " + a.Note
				}
				if a.Data != "" {
					line += "  " + strings.Join(strings.Fields(a.Data), " ")
				}
				b.WriteString("\n" + countStyle.Render("⟐ ") + truncate(line, w-2))
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
