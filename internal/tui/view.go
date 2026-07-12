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
	case m.annotationOpen:
		content = m.renderAnnotationComposer(m.width, bodyH)
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
	row("1–5 / tab / shift+tab", "switch tabs")
	row("alt+1–5", "switch tabs while a text input has focus (Search/Impact/Path)")
	row("ctrl+g", "open the selection in the Graph walker")
	row("ctrl+s", "view the selected symbol's source")
	row("ctrl+o / a", "orient context / add a note to an exact selection (in-flight save is non-cancellable)")
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
	row("m", "toggle the neighborhood map (callers → node → calls)")
	b.WriteString(sectionStyle.Render("Metrics") + "\n")
	row("↑/↓ · k/j", "move the selection (also pgup/pgdn · home/end)")
	row("enter", "drill the selected symbol into Impact")
	b.WriteString(sectionStyle.Render("Impact / Search") + "\n")
	row("type", "edit the query; enter runs it")
	row("↑/↓ · pgup/pgdn", "move the result selection")
	row("enter", "run the query, or open the selected hit")
	b.WriteString(sectionStyle.Render("Path") + "\n")
	row("↑/↓", "move between FROM and TO while editing")
	row("enter", "FROM → TO · TO → find shortest path · result → Graph")
	row("↑/↓ · k/j", "inspect returned path nodes (also pgup/pgdn · home/end)")
	row("f / t · r", "edit FROM / TO · rerun the current endpoints")
	b.WriteString(sectionStyle.Render("Source / context overlay (s · ctrl+s · ctrl+o)") + "\n")
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

// renderAnnotationComposer is a compact modal editor. Every dynamic segment is
// width-clamped before the outer MaxWidth guard, so even a tiny terminal keeps
// the target, input, error, and save/cancel affordances usable without wrapping
// the rest of Studio underneath it.
func (m Model) renderAnnotationComposer(w, _ int) string {
	contentW := w - 4 // rounded border + one cell of horizontal padding
	if contentW < 1 {
		contentW = 1
	}
	target := displayName(m.annotationCenter.fqn, m.annotationCenter.sym)
	location := fmt.Sprintf("%s:%d", m.annotationCenter.file, m.annotationCenter.line)
	var b strings.Builder
	b.WriteString(titleStyle.Render(truncate("Add annotation", contentW)) + "\n")
	b.WriteString(symStyle.Render(truncate(target, contentW)) + "\n")
	b.WriteString(mutedStyle.Render(truncate(location, contentW)) + "\n\n")
	if m.annotationSaving {
		b.WriteString(mutedStyle.Render(truncate(m.spinnerGlyph()+" saving note…", contentW)))
	} else {
		b.WriteString(lipgloss.NewStyle().MaxWidth(contentW).Render(m.annotationInput.View()))
	}
	if m.annotationErr != "" {
		b.WriteString("\n" + errorStyle.Render(truncate(m.annotationErr, contentW)))
	}
	b.WriteString("\n\n" + mutedStyle.Render(truncate("enter save · esc cancel · ctrl+c quit", contentW)))
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorAccent).
		Padding(0, 1).
		Render(b.String())
	return lipgloss.NewStyle().MaxWidth(w).Render(box)
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
	// Live signal that a background daemon is keeping the index fresh automatically
	// (only shown while one is running — a quiet positive indicator, not noise).
	if chip := m.daemonChip(); chip != "" {
		right += "  " + chip
	}
	return spread(left, right, m.width)
}

// daemonChip renders a compact green indicator when a background daemon is
// watching this project, including its branch when known; "" when none runs.
func (m Model) daemonChip() string {
	if m.daemon == nil {
		return ""
	}
	label := "● daemon"
	if m.daemon.Branch != "" {
		label += " " + m.daemon.Branch
	}
	return daemonOnStyle.Render(label)
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

// statusText is the right-aligned footer status: an error (if any) takes
// precedence, otherwise the status message, prefixed with an animated spinner
// while an async op is in flight (busy()).
func (m Model) statusText() string {
	if m.errMsg != "" {
		return errorStyle.Render(m.errMsg)
	}
	if m.busy() {
		return mutedStyle.Render(m.spinnerGlyph()+" ") + m.statusMsg
	}
	return m.statusMsg
}

func (m Model) footer() string {
	var hint string
	switch {
	case m.annotationOpen:
		hint = "type note · enter save · esc cancel · ctrl+c quit"
		if m.annotationSaving {
			hint = "saving note… · ctrl+c quit"
		}
		return spread(mutedStyle.Render(hint), m.statusText(), m.width)
	case m.showHelp:
		return spread(mutedStyle.Render("? / esc close"), m.statusText(), m.width)
	case m.srcView:
		hint = "↑/↓ scroll · pgup/pgdn · g/G top/bottom · esc/q close · ctrl+c quit"
		return spread(mutedStyle.Render(hint), m.statusText(), m.width)
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
			hint = "↑/↓ ref · enter re-center · a note · m map · s source · ⌫ back · ← hubs · ctrl+c quit"
			compact = "↑/↓ · enter re-center · a note · ⌫ back · ← hubs"
		} else {
			hint = "↑/↓ hub · → walk · enter → impact · a note · m map · s source · p precise · ctrl+c quit"
			compact = "↑/↓ · → walk · enter impact · a note · s src"
		}
	case tabSearch:
		hint = "type · enter search/open · ↑/↓ select · a note · ctrl+g graph · ctrl+s source · tab · ctrl+c quit"
		compact = "type · enter · ↑/↓ · a note · ctrl+g graph · tab"
	case tabImpact:
		hint = "type symbol · enter run/open · ↑/↓ select · a note · ctrl+g graph · ctrl+s source · tab · ctrl+c quit"
		compact = "type · enter · ↑/↓ · a note · ctrl+g graph · tab"
	case tabPath:
		if m.pathFocus == focusPathResult {
			hint = "↑/↓ · k/j inspect · a note · enter/ctrl+g graph · ctrl+s source · f/t edit · r rerun · ctrl+c quit"
			compact = "↑/↓ · k/j · a note · enter graph · f/t edit"
		} else {
			hint = "type endpoint · ↑/↓ field · enter next/run · alt+1–5 tabs · ctrl+c quit"
			compact = "type · ↑/↓ field · enter next/run · alt+1–5 tabs"
		}
	default: // metrics
		hint = "↑/↓ select · enter → impact · ctrl+g graph · ctrl+s source · ctrl+r reindex · ctrl+c quit"
		compact = "↑/↓ · enter impact · ctrl+g graph · ctrl+s src · ctrl+r reindex"
	}
	hint += " · ? help"
	compact += " · ? help"
	if lipgloss.Width(hint) > m.width {
		hint = compact
	}
	return spread(mutedStyle.Render(hint), m.statusText(), m.width)
}

func (m Model) body(w, h int) string {
	var content string
	switch m.active {
	case tabGraph:
		content = m.renderGraph(w, h)
	case tabMetrics:
		content = m.renderMetrics(w, h)
	case tabImpact:
		content = m.renderImpact(w, h)
	case tabSearch:
		content = m.renderSearch(w, h)
	case tabPath:
		content = m.renderPath(w, h)
	}
	if banner := m.annotationBanner(w); banner != "" {
		return banner + "\n" + content
	}
	return content
}

func (m Model) annotationBanner(w int) string {
	current, ok := m.annotationSelection()
	if !ok || !sameGraphCenter(current, m.annotationCacheAt) || len(m.annotationCache) == 0 {
		return ""
	}
	// Graph centers, Search hits, and exact Impact roots already render their
	// refreshed annotation collections inline. The banner fills the remaining
	// drill surfaces (Graph refs, blast nodes, Path rows) without duplicating notes.
	switch m.active {
	case tabGraph:
		if m.graphFocus == focusHubs && sameGraphCenter(m.graphCenter, current) {
			return ""
		}
	case tabSearch:
		return ""
	case tabImpact:
		if root, rootOK := impactRootCenter(m.impactRep); rootOK && sameGraphCenter(root, current) {
			return ""
		}
	}
	a := m.annotationCache[len(m.annotationCache)-1]
	note := strings.TrimSpace(a.Note)
	if note == "" {
		note = strings.TrimSpace(a.Data)
	}
	if note == "" {
		return ""
	}
	line := fmt.Sprintf("⟐ %s · [%s] %s", displayName(current.fqn, current.sym), a.Source, note)
	return countStyle.Render(truncate(line, w))
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
	detail := m.hubDetail(rightW, h)
	if m.graphMap {
		detail = m.hubMap(rightW, h)
	}
	left := lipgloss.NewStyle().Width(leftW).Height(h).Render(m.hubList(leftW, h))
	right := lipgloss.NewStyle().Width(rightW).Height(h).Render(detail)
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
		mark = " · gopls"
	}
	if len(m.graphStack) > 0 {
		hdr += "  " + mutedStyle.Render(fmt.Sprintf("· depth %d (⌫ back)", len(m.graphStack)))
	}
	if n := len(m.graphAnnotations); n > 0 { // at-a-glance: this node has pinned knowledge
		hdr += "  " + countStyle.Render(fmt.Sprintf("· ⟐ %d", n))
	}
	b.WriteString(hdr + "\n")
	trust := m.graphTrustLines(w)
	for _, line := range trust {
		b.WriteString(line + "\n")
	}
	b.WriteByte('\n')
	annShown := clamp(len(m.graphAnnotations), 0, 3)
	budget := (h - 9 - annShown - len(trust)) / 2
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

// hubMap renders the centered node's call-graph neighborhood as a diagram: the
// callers flow down into a boxed focal node, which flows down into its callees — a
// compact "you are here" map of the local graph, toggled from the list detail with
// `m`. Robust to any width (names truncate; the box spans the pane).
func (m Model) hubMap(w, h int) string {
	if m.graphSym == "" {
		return mutedStyle.Render("select a hub")
	}
	hdr := title("Neighborhood map")
	if len(m.graphStack) > 0 {
		hdr += "  " + mutedStyle.Render(fmt.Sprintf("· depth %d (⌫ back)", len(m.graphStack)))
	}

	trust := m.graphTrustLines(w)
	budget := (h - 12 - len(trust)) / 2
	if budget < 1 {
		budget = 1
	}

	var b strings.Builder
	b.WriteString(hdr + "\n") // already styled; the frame's MaxWidth clamps any overflow
	for _, line := range trust {
		b.WriteString(line + "\n")
	}
	b.WriteByte('\n')

	// Callers flow down into the node.
	b.WriteString(mutedStyle.Render(truncate(fmt.Sprintf("  ┌ called by (%d)", len(m.graphCallers)), w)) + "\n")
	writeMapRefs(&b, m.graphCallers, budget, w, m.mapReveal)
	b.WriteString(titleStyle.Render("  ▼") + "\n")

	// The boxed focal node. boxW stays ≤ w (renderGraph guarantees w ≥ 10) so the
	// box can never exceed the pane; innerW ≥ 1 keeps the repeat counts valid.
	boxW := w - 1
	if boxW < 3 {
		boxW = 3
	}
	innerW := boxW - 2
	if innerW < 1 {
		innerW = 1
	}
	name := truncate(" ◆ "+displayName(m.graphCenter.fqn, m.graphCenter.sym), innerW-1)
	pad := innerW - lipgloss.Width(name)
	if pad < 0 {
		pad = 0
	}
	b.WriteString(titleStyle.Render("╭"+strings.Repeat("─", innerW)+"╮") + "\n")
	b.WriteString(titleStyle.Render("│") + symStyle.Render(name) + strings.Repeat(" ", pad) + titleStyle.Render("│") + "\n")
	b.WriteString(titleStyle.Render("╰"+strings.Repeat("─", innerW)+"╯") + "\n")

	// The node flows down into its callees.
	b.WriteString(titleStyle.Render("  ▲") + "\n")
	b.WriteString(mutedStyle.Render(truncate(fmt.Sprintf("  └ calls (%d)", len(m.graphCallees)), w)) + "\n")
	writeMapRefs(&b, m.graphCallees, budget, w, m.mapReveal)

	b.WriteString("\n" + mutedStyle.Render(truncate("  m: list · ↑↓→ walk · enter: re-center", w)))
	return b.String()
}

// graphTrustLines renders trust evidence for the currently selected node. The
// source is the node's RelationReport, not project-wide precise-edge totals: a
// partially precise project can therefore show an uncovered file honestly.
func (m Model) graphTrustLines(w int) []string {
	if m.graphCallGraph == "" {
		return nil
	}
	label, confidence := m.graphCallGraph, "unknown"
	style := mutedStyle
	switch m.graphCallGraph {
	case app.CallGraphResolved:
		confidence, style = "high", countStyle
	case app.CallGraphName:
		confidence, style = "medium", warnStyle
	case app.CallGraphUnresolved:
		confidence, style = "low", errorStyle
	case app.CallGraphNone:
		label = app.CallGraphNone
	}
	via := ""
	if m.graphCallGraph == app.CallGraphResolved && m.graphPrecise {
		via = " · gopls"
	}
	lines := []string{style.Render(truncate(fmt.Sprintf("call_graph: %s · confidence: %s%s", label, confidence, via), w))}
	if m.graphResolution != "" {
		lines = append(lines, errorStyle.Render(truncate("⚠ "+m.graphResolution, w)))
	}
	if m.graphNote != "" && m.graphNote != m.graphResolution {
		lines = append(lines, mutedStyle.Render(truncate("note: "+m.graphNote, w)))
	}
	return lines
}

// writeMapRefs renders a relation branch of the neighborhood map — one "│ name"
// spine line per ref, windowed to budget with a "+N more" tail. reveal∈[0,1] grows
// the branch in (fewer lines while the spring climbs); the structure around it (the
// box, the "called by"/"calls" headers) always renders, so reveal never hides it.
func writeMapRefs(b *strings.Builder, refs []app.SymbolRef, budget, w int, reveal float64) {
	if len(refs) == 0 {
		b.WriteString(mutedStyle.Render(truncate("  │   (none)", w)) + "\n")
		return
	}
	shown, more := refs, 0
	if len(refs) > budget {
		shown, more = refs[:budget], len(refs)-budget
	}
	visible := len(shown)
	if reveal < 1 {
		visible = int(float64(len(shown)) * clamp01(reveal))
	}
	nameW := w - 4
	if nameW < 1 {
		nameW = 1
	}
	for i := 0; i < visible; i++ {
		r := shown[i]
		b.WriteString(mutedStyle.Render("  │ ") + truncate(displayName(r.FQN, r.Symbol), nameW) + "\n")
	}
	if more > 0 && reveal >= 1 {
		b.WriteString(mutedStyle.Render(truncate(fmt.Sprintf("  │   … +%d more", more), w)) + "\n")
	}
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
	b.WriteString(barChart(m.status.Kinds, barW, m.metricsReveal))
	b.WriteString("\n" + sectionStyle.Render("By language") + "\n")
	b.WriteString(barChart(m.status.Languages, barW, m.metricsReveal))
	// A compact sparkline of the kind distribution — the shape of the codebase at a
	// glance, beneath the labelled bars.
	if spark := sparkline(sortedValues(m.status.Kinds)); spark != "" {
		b.WriteString("\n" + countStyle.Render("shape  ") + barStyle.Render(spark) + "\n")
	}
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
		b.WriteString(sectionStyle.Render("Blast radius") + "  " + heatLegend() + "\n")
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
				// The [depth] tag is colored by the heatmap — hot (direct) → cool (far).
				fmt.Fprintf(&b, " %s%s %s %s\n", marker, depthHeat(n.Depth), padRight(name, 32), mutedStyle.Render(loc))
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

// ---- Path tab ----

// renderPath is a task-oriented shortest-path workflow rather than a graph
// algorithm in the view: people choose FROM/TO, an async command calls app.Path,
// and this pane renders the report's ordered nodes plus its confidence evidence.
func (m Model) renderPath(w, h int) string {
	if m.status != nil && !m.status.Registered {
		return notIndexedHint("Path")
	}
	var b strings.Builder
	b.WriteString(title("Path") + "   " + mutedStyle.Render("shortest call chain") + "\n")
	b.WriteString(m.pathInputRow("FROM", m.pathFromInput.View(), m.pathFocus == focusPathFrom, w) + "\n")
	b.WriteString(m.pathInputRow("TO", m.pathToInput.View(), m.pathFocus == focusPathTo, w) + "\n")
	if m.pathFocus == focusPathResult {
		b.WriteString(mutedStyle.Render(truncate("  result focused · ↑/↓ or k/j inspect · enter opens Graph · f/t edits endpoints", w)) + "\n")
	} else {
		b.WriteString(mutedStyle.Render(truncate("  ↑/↓ moves fields · enter runs · selected endpoints or unique FQNs stay exact", w)) + "\n")
	}
	b.WriteByte('\n')

	switch {
	case m.pathLoading:
		b.WriteString(mutedStyle.Render(truncate(m.spinnerGlyph()+" finding shortest path through the indexed graph…", w)))
		return b.String()
	case m.pathErr != "":
		b.WriteString(errorStyle.Render("Path lookup failed") + "\n")
		b.WriteString(mutedStyle.Render(truncate(m.pathErr, w)))
		return b.String()
	case m.pathRep == nil:
		b.WriteString(sectionStyle.Render("Choose two endpoints") + "\n")
		b.WriteString(mutedStyle.Render(truncate("Start from the current selection (when available), or type a symbol/FQN.", w)) + "\n")
		b.WriteString(mutedStyle.Render(truncate("Studio asks the shared codemap call graph for the shortest path; it never guesses from text.", w)))
		return b.String()
	}

	rep := m.pathRep
	b.WriteString(pathEvidence(rep.CallGraph) + "\n")
	if rep.Stale {
		b.WriteString(warnStyle.Render("⚠ stale index — ctrl+r before relying on this path") + "\n")
	}
	if rep.Resolution != "" {
		b.WriteString(errorStyle.Render("⚠ ") + mutedStyle.Render(truncate(rep.Resolution, w-2)) + "\n")
	}
	if rep.Note != "" && rep.Note != rep.Resolution {
		b.WriteString(warnStyle.Render("⚠ ") + mutedStyle.Render(truncate(rep.Note, w-2)) + "\n")
	}

	if !rep.Found {
		b.WriteByte('\n')
		switch rep.CallGraph {
		case app.CallGraphUnresolved:
			b.WriteString(errorStyle.Render("Path unknown") + "\n")
			b.WriteString(mutedStyle.Render(truncate("The indexed call graph is incomplete, so an empty path is not evidence that the endpoints are disconnected.", w)))
		case app.CallGraphNone:
			b.WriteString(sectionStyle.Render("Endpoint not found") + "\n")
			b.WriteString(mutedStyle.Render(truncate("Edit FROM or TO; choose an existing selection or a unique FQN for an exact endpoint.", w)))
		default:
			b.WriteString(sectionStyle.Render("No path found") + "\n")
			b.WriteString(mutedStyle.Render(truncate(fmt.Sprintf("%s and %s are both indexed, but this graph contains no directed path between them.", rep.From, rep.To), w)))
		}
		return b.String()
	}

	hops := len(rep.Path) - 1
	pathTitle := truncate(fmt.Sprintf("%s → %s  ·  %d hops", rep.From, rep.To, hops), w)
	b.WriteString("\n" + sectionStyle.Render(pathTitle) + "\n")
	// Header/inputs/evidence consume roughly 10 lines; keep the selected row in a
	// bounded window so the layout remains useful after any terminal resize.
	budget := clamp((h-13)/2, 1, 20)
	start := windowStart(m.pathSel, budget, len(rep.Path))
	end := clamp(start+budget, 0, len(rep.Path))
	if start > 0 {
		b.WriteString(mutedStyle.Render(fmt.Sprintf("  ▲ %d earlier nodes\n", start)))
	}
	for i := start; i < end; i++ {
		n := rep.Path[i]
		name := displayName(n.FQN, n.Symbol)
		loc := fmt.Sprintf("%s:%d", n.File, n.StartLine)
		plain := fmt.Sprintf(" %2d  %-9s  %s  %s", i, n.Kind, name, loc)
		if i == m.pathSel && m.pathFocus == focusPathResult {
			b.WriteString(selectedStyle.Width(w).Render(truncate(" ▸"+plain, w)) + "\n")
		} else {
			contentW := w - 19 // fixed indent/index/kind/separators
			if contentW < 2 {
				contentW = 2
			}
			locW := contentW / 3
			if locW < 1 {
				locW = 1
			}
			nameW := contentW - locW
			if nameW < 1 {
				nameW = 1
			}
			kind := padRight(truncate(n.Kind, 9), 9)
			b.WriteString("  " + countStyle.Render(fmt.Sprintf("%2d", i)) + "  " +
				mutedStyle.Render(kind) + "  " + symStyle.Render(truncate(name, nameW)) + "  " +
				mutedStyle.Render(truncate(loc, locW)) + "\n")
		}
		if i < end-1 {
			b.WriteString(mutedStyle.Render("       │ calls") + "\n")
		}
	}
	if end < len(rep.Path) {
		b.WriteString(mutedStyle.Render(fmt.Sprintf("  ▼ %d later nodes", len(rep.Path)-end)) + "\n")
	}
	if m.pathSel < len(rep.Path) {
		if p := detailPreview(rep.Path[m.pathSel].Signature, rep.Path[m.pathSel].Doc, w); p != "" {
			b.WriteString("\n" + p)
		}
	}
	return b.String()
}

func (m Model) pathInputRow(label, input string, focused bool, w int) string {
	chip := chipStyle.Render(" " + label + " ")
	if focused {
		chip = activeChipStyle.Render("▸ " + label + " ")
	}
	return lipgloss.NewStyle().MaxWidth(w).Render("  " + chip + " " + input)
}

func pathEvidence(callGraph string) string {
	label, confidence := callGraph, "unknown"
	style := mutedStyle
	switch callGraph {
	case app.CallGraphResolved:
		confidence, style = "high", symStyle
	case app.CallGraphName:
		confidence, style = "medium", warnStyle
	case app.CallGraphUnresolved:
		confidence, style = "low", errorStyle
	case app.CallGraphNone, "":
		label = app.CallGraphNone
	}
	return style.Render(fmt.Sprintf("call_graph: %s · confidence: %s", label, confidence))
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

// partialBlocks indexes 1/8-width left block glyphs: index e ∈ 0..7 fills e/8 of a
// cell. Index 0 is empty; index 8 (a full cell) is rendered as "█" by the caller.
const partialBlocks = " ▏▎▍▌▋▊▉"

// sparkBlocks are the 8 vertical levels for one-line sparklines (low → high).
const sparkBlocks = "▁▂▃▄▅▆▇█"

func barChart(counts map[string]int, barW int, reveal float64) string {
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
		fmt.Fprintf(&b, "  %s %s %d\n", padRight(k, 12), bar(counts[k], max, barW, reveal), counts[k])
	}
	return b.String()
}

// bar renders a horizontal bar that is reveal∈[0,1] of its true length, using
// 1/8-cell partial blocks so the spring grow-in (anim.go) looks smooth rather than
// stepping one whole cell at a time. reveal=1 yields the bar's full length. The
// returned string is always exactly width cells wide so the column stays aligned.
func bar(n, max, width int, reveal float64) string {
	if max <= 0 || width <= 0 {
		return ""
	}
	frac := float64(n) / float64(max)
	if frac > 1 {
		frac = 1
	}
	eighths := int(frac*float64(width)*clamp01(reveal)*8 + 0.5)
	if n > 0 && reveal > 0 && eighths == 0 {
		eighths = 1 // a nonzero count always shows at least a sliver once revealed
	}
	if eighths > width*8 {
		eighths = width * 8
	}
	full := eighths / 8
	part := eighths % 8
	var fill strings.Builder
	fill.WriteString(strings.Repeat("█", full))
	cells := full
	if part > 0 {
		fill.WriteRune([]rune(partialBlocks)[part])
		cells++
	}
	return barStyle.Render(fill.String()) + strings.Repeat(" ", width-cells)
}

// sortedValues returns a map's values sorted descending — the magnitudes a
// sparkline plots, independent of label.
func sortedValues(counts map[string]int) []int {
	vals := make([]int, 0, len(counts))
	for _, v := range counts {
		vals = append(vals, v)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(vals)))
	return vals
}

// sparkline renders values as a one-line bar graph using 1/8-height block glyphs,
// each cell scaled to the max. Empty input yields "".
func sparkline(vals []int) string {
	if len(vals) == 0 {
		return ""
	}
	max := 0
	for _, v := range vals {
		if v > max {
			max = v
		}
	}
	rs := []rune(sparkBlocks)
	if max <= 0 {
		return strings.Repeat(string(rs[0]), len(vals))
	}
	var b strings.Builder
	for _, v := range vals {
		idx := (v*(len(rs)-1)*2 + max) / (max * 2) // round to nearest level
		if idx < 0 {
			idx = 0
		}
		if idx > len(rs)-1 {
			idx = len(rs) - 1
		}
		b.WriteRune(rs[idx])
	}
	return b.String()
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
