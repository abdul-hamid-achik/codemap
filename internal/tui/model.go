// Package tui is codemap's interactive terminal UI ("studio"), built on Charm
// v2 (charm.land/bubbletea, lipgloss, bubbles). It is a thin view over
// internal/app: a tabbed explorer with Graph, Metrics, Impact, and Search.
package tui

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/abdul-hamid-achik/codemap/internal/app"
	"github.com/abdul-hamid-achik/codemap/internal/index"
)

type tab int

const (
	tabGraph tab = iota
	tabMetrics
	tabImpact
	tabSearch
	tabCount
)

func (t tab) String() string {
	switch t {
	case tabGraph:
		return "Graph"
	case tabMetrics:
		return "Metrics"
	case tabImpact:
		return "Impact"
	case tabSearch:
		return "Search"
	}
	return ""
}

// async messages
type statusMsg struct {
	st  *app.StatusReport
	err error
}
type semanticMsg struct {
	query string
	hits  []app.SemanticHit
	mode  string
	err   error
}
type impactMsg struct {
	symbol string
	rep    *app.ImpactReport
	err    error
}
type graphHubsMsg struct {
	hubs []app.HotspotRef
	err  error
}
type orphansMsg struct {
	orphans []app.SymbolRef
	err     error
}
type graphDetailMsg struct {
	symbol  string
	callers []app.SymbolRef
	callees []app.SymbolRef
	err     error
}
type indexedMsg struct {
	rep *app.IndexReport
	err error
}
type preciseDetailMsg struct {
	symbol  string
	callers []app.SymbolRef
	callees []app.SymbolRef
	err     error
}
type sourceMsg struct {
	title string
	lines []string
	err   error
}

// Model is the studio TUI state.
type Model struct {
	ctx      context.Context
	service  *app.Service
	startDir string

	active tab
	width  int
	height int

	loading   bool
	statusMsg string
	errMsg    string
	status    *app.StatusReport
	orphans   []app.SymbolRef // dead-code candidates, for the Metrics overview

	// search tab
	search      textinput.Model
	searchHits  []app.SemanticHit
	searchQuery string
	searchMode  string // "semantic" or "name"
	searchSel   int

	// impact tab
	impact       textinput.Model
	impactRep    *app.ImpactReport
	impactSymbol string
	impactSel    int

	// graph tab (call-graph explorer)
	graphLoaded  bool
	graphHubs    []app.HotspotRef
	graphSel     int
	graphSym     string
	graphCallers []app.SymbolRef
	graphCallees []app.SymbolRef
	graphPrecise bool // hub detail is showing gopls-precise relations

	// source viewer: a full-screen, scrollable view of a symbol's body, opened
	// with `s` from the Graph tab and dismissed with esc/q.
	srcView   bool
	srcTitle  string
	srcLines  []string
	srcScroll int

	// graph walking: the right pane can be focused to walk into callers/callees,
	// re-centering the explorer on any node (not just hubs). graphStack records
	// the path so backspace pops back.
	graphFocus  graphFocus
	graphRefSel int           // selection across the combined callers+callees list
	graphCenter graphCenter   // node the detail pane is currently centered on
	graphStack  []graphCenter // breadcrumb of centers walked into
}

// graphFocus is which pane of the Graph tab has keyboard focus.
type graphFocus int

const (
	focusHubs graphFocus = iota // left pane: the hub list (jump points)
	focusRefs                   // right pane: the center's callers/callees (walk)
)

// graphCenter is the node the Graph detail pane is centered on. It carries
// enough to re-fetch relations (by name) and resolve precisely (file:line).
type graphCenter struct {
	sym, fqn, file string
	line           int
}

func centerOfHub(h app.HotspotRef) graphCenter {
	return graphCenter{sym: h.Symbol, fqn: h.FQN, file: h.File, line: h.StartLine}
}

func centerOfRef(r app.SymbolRef) graphCenter {
	return graphCenter{sym: r.Symbol, fqn: r.FQN, file: r.File, line: r.StartLine}
}

// graphRefs is the combined callers-then-callees list the refs pane walks over.
func (m Model) graphRefs() []app.SymbolRef {
	return append(append([]app.SymbolRef{}, m.graphCallers...), m.graphCallees...)
}

// NewModel builds the studio model over a session.
func NewModel(ctx context.Context, sess *app.Session, startDir string) Model {
	s := textinput.New()
	s.Placeholder = `describe code by meaning, e.g. "jwt validation middleware"`
	s.Focus()

	i := textinput.New()
	i.Placeholder = "symbol name, e.g. Authenticate"

	return Model{
		ctx:      ctx,
		service:  app.NewService(sess),
		startDir: startDir,
		active:   tabGraph,
		loading:  true,
		search:   s,
		impact:   i,
	}
}

// Init loads project status, the call graph, and dead-code candidates.
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.statusCmd(), m.hubsCmd(), m.orphansCmd())
}

// ---- commands ----

func (m Model) statusCmd() tea.Cmd {
	svc, dir := m.service, m.startDir
	return func() tea.Msg {
		st, err := svc.Status(dir)
		return statusMsg{st: st, err: err}
	}
}

func (m Model) hubsCmd() tea.Cmd {
	svc, dir := m.service, m.startDir
	return func() tea.Msg {
		r, err := svc.Hotspots(dir, 200)
		if err != nil {
			return graphHubsMsg{err: err}
		}
		return graphHubsMsg{hubs: r.Hotspots}
	}
}

func (m Model) orphansCmd() tea.Cmd {
	svc, dir := m.service, m.startDir
	return func() tea.Msg {
		r, err := svc.Orphans(dir, 50)
		if err != nil {
			return orphansMsg{err: err}
		}
		return orphansMsg{orphans: r.Orphans}
	}
}

func (m Model) detailCmd(sym string) tea.Cmd {
	svc, dir := m.service, m.startDir
	return func() tea.Msg {
		ca, err := svc.Callers(dir, sym)
		if err != nil {
			return graphDetailMsg{symbol: sym, err: err}
		}
		ce, err := svc.Callees(dir, sym)
		if err != nil {
			return graphDetailMsg{symbol: sym, err: err}
		}
		return graphDetailMsg{symbol: sym, callers: ca.Results, callees: ce.Results}
	}
}

func (m Model) semanticCmd(q string) tea.Cmd {
	ctx, svc, dir := m.ctx, m.service, m.startDir
	return func() tea.Msg {
		r, err := svc.Search(ctx, dir, q, 50) // semantic, falling back to name search
		if err != nil {
			return semanticMsg{query: q, err: err}
		}
		return semanticMsg{query: q, hits: r.Hits, mode: r.Mode}
	}
}

func (m Model) preciseDetailCmd(c graphCenter) tea.Cmd {
	ctx, svc, dir := m.ctx, m.service, m.startDir
	return func() tea.Msg {
		callers, callees, err := svc.PreciseRelationsAt(ctx, dir, c.sym, c.file, c.line)
		return preciseDetailMsg{symbol: c.sym, callers: callers, callees: callees, err: err}
	}
}

// sourceTarget is the symbol whose source `s`/ctrl+s should show, based on the
// active tab and selection: the selected ref/hub on Graph, the selected blast
// node (or analyzed symbol) on Impact, the selected hit on Search.
func (m Model) sourceTarget() (sym, file string, line int, ok bool) {
	switch m.active {
	case tabGraph:
		if m.graphFocus == focusRefs {
			if refs := m.graphRefs(); m.graphRefSel < len(refs) {
				r := refs[m.graphRefSel]
				return r.Symbol, r.File, r.StartLine, true
			}
			return "", "", 0, false
		}
		if m.graphCenter.sym != "" {
			return m.graphCenter.sym, m.graphCenter.file, m.graphCenter.line, true
		}
	case tabImpact:
		if m.impactRep != nil {
			if m.impactSel < len(m.impactRep.BlastRadius) {
				n := m.impactRep.BlastRadius[m.impactSel]
				return n.Symbol, n.File, n.StartLine, true
			}
			if len(m.impactRep.Locations) > 0 {
				l := m.impactRep.Locations[0]
				return l.Symbol, l.File, l.StartLine, true
			}
		}
	case tabSearch:
		if m.searchSel < len(m.searchHits) {
			h := m.searchHits[m.searchSel]
			return h.Symbol, h.File, h.StartLine, true
		}
	}
	return "", "", 0, false
}

// viewSource opens the source overlay for the current selection, if any.
func (m Model) viewSource() tea.Cmd {
	if sym, file, line, ok := m.sourceTarget(); ok {
		return m.sourceViewCmd(sym, file, line)
	}
	return nil
}

func (m Model) sourceViewCmd(sym, file string, line int) tea.Cmd {
	svc, dir := m.service, m.startDir
	return func() tea.Msg {
		rep, err := svc.Source(dir, sym)
		if err != nil {
			return sourceMsg{err: err}
		}
		// Prefer the match at the exact file:line; fall back to the first.
		var mch *app.SourceMatch
		for i := range rep.Matches {
			if rep.Matches[i].File == file && rep.Matches[i].StartLine == line {
				mch = &rep.Matches[i]
				break
			}
		}
		if mch == nil && len(rep.Matches) > 0 {
			mch = &rep.Matches[0]
		}
		if mch == nil {
			return sourceMsg{err: fmt.Errorf("no source for %q", sym)}
		}
		title := fmt.Sprintf("%s  %s:%d-%d", displayName(mch.FQN, mch.Symbol), mch.File, mch.StartLine, mch.EndLine)
		return sourceMsg{title: title, lines: strings.Split(mch.Source, "\n")}
	}
}

func (m Model) reindexCmd() tea.Cmd {
	ctx, svc, dir := m.ctx, m.service, m.startDir
	return func() tea.Msg {
		// Structure-only: fast and needs no Ollama, so a refresh always works.
		rep, err := svc.Index(ctx, dir, index.Options{}, false)
		return indexedMsg{rep: rep, err: err}
	}
}

func (m Model) impactCmd(sym string) tea.Cmd {
	svc, dir := m.service, m.startDir
	return func() tea.Msg {
		r, err := svc.Impact(dir, sym, 3)
		if err != nil {
			return impactMsg{symbol: sym, err: err}
		}
		return impactMsg{symbol: sym, rep: r}
	}
}

// ---- update ----

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case statusMsg:
		m.loading = false
		if msg.err != nil {
			m.errMsg = msg.err.Error()
			return m, nil
		}
		m.status = msg.st
		if msg.st != nil && msg.st.Registered {
			m.statusMsg = "ready"
		} else {
			m.statusMsg = "no index — run 'codemap index'"
		}
		return m, nil

	case graphHubsMsg:
		m.graphLoaded = true
		if msg.err != nil {
			m.errMsg = msg.err.Error()
			return m, nil
		}
		m.graphHubs = msg.hubs
		m.graphSel = 0
		m.graphFocus = focusHubs
		m.graphStack = nil
		if len(msg.hubs) > 0 {
			m.graphCenter = centerOfHub(msg.hubs[0])
			return m, m.detailCmd(msg.hubs[0].Symbol)
		}
		return m, nil

	case orphansMsg:
		if msg.err == nil {
			m.orphans = msg.orphans
		}
		return m, nil

	case sourceMsg:
		if msg.err != nil {
			m.errMsg = msg.err.Error()
			return m, nil
		}
		m.errMsg = ""
		m.srcView = true
		m.srcTitle = msg.title
		m.srcLines = msg.lines
		m.srcScroll = 0
		return m, nil

	case graphDetailMsg:
		if msg.err != nil {
			m.errMsg = msg.err.Error()
			return m, nil
		}
		m.graphSym = msg.symbol
		m.graphCallers = msg.callers
		m.graphCallees = msg.callees
		m.graphPrecise = false
		m.graphRefSel = 0
		return m, nil

	case preciseDetailMsg:
		if msg.err != nil {
			m.errMsg = msg.err.Error()
			return m, nil
		}
		m.errMsg = ""
		m.graphSym = msg.symbol
		m.graphCallers = msg.callers
		m.graphCallees = msg.callees
		m.graphPrecise = true
		m.graphRefSel = 0
		m.statusMsg = fmt.Sprintf("precise via gopls: %d callers, %d callees", len(msg.callers), len(msg.callees))
		return m, nil

	case indexedMsg:
		if msg.err != nil {
			m.errMsg = msg.err.Error()
			return m, nil
		}
		m.errMsg = ""
		if msg.rep != nil {
			m.statusMsg = fmt.Sprintf("reindexed: %d files · %d nodes · %d edges",
				msg.rep.FilesIndexed, msg.rep.Nodes, msg.rep.Edges)
		}
		// Refresh everything the new index affects.
		return m, tea.Batch(m.statusCmd(), m.hubsCmd(), m.orphansCmd())

	case semanticMsg:
		if msg.err != nil {
			m.errMsg = msg.err.Error()
			m.searchHits = nil
			return m, nil
		}
		m.errMsg = ""
		m.searchHits = msg.hits
		m.searchQuery = msg.query
		m.searchMode = msg.mode
		m.searchSel = 0
		m.statusMsg = fmt.Sprintf("%d %s matches for %q", len(msg.hits), msg.mode, msg.query)
		return m, nil

	case impactMsg:
		if msg.err != nil {
			m.errMsg = msg.err.Error()
			m.impactRep = nil
			return m, nil
		}
		m.errMsg = ""
		m.impactRep = msg.rep
		m.impactSymbol = msg.symbol
		m.impactSel = 0
		if msg.rep != nil {
			m.statusMsg = fmt.Sprintf("%s: %d callers, %d blast, %d tests",
				msg.symbol, len(msg.rep.DirectCallers), len(msg.rep.BlastRadius), len(msg.rep.Tests))
		}
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "ctrl+c" {
		return m, tea.Quit
	}
	// The source viewer is a modal overlay: it captures navigation keys until
	// dismissed, so handle it before anything else.
	if m.srcView {
		return m.handleSourceKey(key)
	}
	switch key {
	case "ctrl+s":
		// View the current selection's source on any tab (a modifier key so it
		// works even where a text input would otherwise capture `s`).
		return m, m.viewSource()
	case "ctrl+r":
		// Reindex in place (structure-only) and refresh — works on any tab.
		m.statusMsg = "indexing…"
		m.graphLoaded = false
		return m, m.reindexCmd()
	case "tab":
		m.active = (m.active + 1) % tabCount
		m.syncFocus()
		return m, m.onActivate()
	case "shift+tab":
		m.active = (m.active + tabCount - 1) % tabCount
		m.syncFocus()
		return m, m.onActivate()
	}

	// Number keys switch tabs, but only when the focused tab isn't a text input.
	if m.active != tabSearch && m.active != tabImpact {
		if d := digitToTab(key); d >= 0 {
			m.active = tab(d)
			m.syncFocus()
			return m, m.onActivate()
		}
	}

	var cmd tea.Cmd
	switch m.active {
	case tabSearch:
		switch key {
		case "enter":
			q := m.search.Value()
			if q == "" {
				return m, nil
			}
			if q != m.searchQuery {
				// query edited → run a new search
				m.statusMsg = "searching…"
				m.searchSel = 0
				return m, m.semanticCmd(q)
			}
			// query unchanged → drill the selected hit into Impact
			if m.searchSel < len(m.searchHits) {
				sym := m.searchHits[m.searchSel].Symbol
				m.active = tabImpact
				m.impact.SetValue(sym)
				m.syncFocus()
				m.impactSel = 0
				m.statusMsg = "analyzing…"
				return m, m.impactCmd(sym)
			}
			return m, nil
		case "up": // single-line input ignores up/down, so use them to move the selection
			if m.searchSel > 0 {
				m.searchSel--
			}
			return m, nil
		case "down":
			if m.searchSel < len(m.searchHits)-1 {
				m.searchSel++
			}
			return m, nil
		}
		m.search, cmd = m.search.Update(msg)
		return m, cmd

	case tabImpact:
		switch key {
		case "enter":
			s := m.impact.Value()
			if s == "" {
				return m, nil
			}
			if s != m.impactSymbol {
				// new symbol typed → analyze it
				m.statusMsg = "analyzing…"
				m.impactSel = 0
				return m, m.impactCmd(s)
			}
			// query unchanged → drill the selected blast-radius node (recursive)
			if m.impactRep != nil && m.impactSel < len(m.impactRep.BlastRadius) {
				sym := m.impactRep.BlastRadius[m.impactSel].Symbol
				m.impact.SetValue(sym)
				m.impactSel = 0
				m.statusMsg = "analyzing…"
				return m, m.impactCmd(sym)
			}
			return m, nil
		case "up":
			if m.impactSel > 0 {
				m.impactSel--
			}
			return m, nil
		case "down":
			if m.impactRep != nil && m.impactSel < len(m.impactRep.BlastRadius)-1 {
				m.impactSel++
			}
			return m, nil
		}
		m.impact, cmd = m.impact.Update(msg)
		return m, cmd

	case tabGraph:
		return m.handleGraphKey(key)

	default: // metrics
		if key == "q" {
			return m, tea.Quit
		}
	}
	return m, nil
}

// handleGraphKey drives the call-graph explorer. The left pane (focusHubs)
// browses hubs as jump points; the right pane (focusRefs) walks the centered
// node's callers/callees, re-centering on enter so you can traverse the graph.
func (m Model) handleGraphKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "q":
		return m, tea.Quit
	case "p":
		// recompute the centered node's relations precisely via gopls
		if m.graphCenter.sym != "" {
			m.statusMsg = "resolving precise (gopls)…"
			return m, m.preciseDetailCmd(m.graphCenter)
		}
		return m, nil
	case "s":
		// view the selected node's source code in a scrollable overlay.
		return m, m.viewSource()
	case "left", "h":
		m.graphFocus = focusHubs
		return m, nil
	case "right", "l":
		if len(m.graphRefs()) > 0 {
			m.graphFocus = focusRefs
			m.graphRefSel = 0
		}
		return m, nil
	case "esc":
		m.graphFocus = focusHubs
		return m, nil
	case "backspace":
		// pop back to the previous center along the walk.
		if n := len(m.graphStack); n > 0 {
			prev := m.graphStack[n-1]
			m.graphStack = m.graphStack[:n-1]
			m.graphCenter = prev
			m.graphPrecise = false
			m.statusMsg = "← " + displayName(prev.fqn, prev.sym)
			return m, m.detailCmd(prev.sym)
		}
		return m, nil
	}

	if m.graphFocus == focusRefs {
		return m.handleGraphRefsKey(key)
	}

	// focusHubs: browse hubs (jump points).
	switch key {
	case "up", "k":
		if m.graphSel > 0 {
			m.graphSel--
			m.graphPrecise = false
			m.graphCenter = centerOfHub(m.graphHubs[m.graphSel])
			return m, m.detailCmd(m.graphHubs[m.graphSel].Symbol)
		}
	case "down", "j":
		if m.graphSel < len(m.graphHubs)-1 {
			m.graphSel++
			m.graphPrecise = false
			m.graphCenter = centerOfHub(m.graphHubs[m.graphSel])
			return m, m.detailCmd(m.graphHubs[m.graphSel].Symbol)
		}
	case "enter":
		// drill the centered hub into full impact analysis
		if len(m.graphHubs) > 0 {
			sym := m.graphHubs[m.graphSel].Symbol
			m.active = tabImpact
			m.impact.SetValue(sym)
			m.syncFocus()
			m.impactSel = 0
			m.statusMsg = "analyzing…"
			return m, m.impactCmd(sym)
		}
	}
	return m, nil
}

// handleGraphRefsKey walks the centered node's callers/callees.
func (m Model) handleGraphRefsKey(key string) (tea.Model, tea.Cmd) {
	refs := m.graphRefs()
	switch key {
	case "up", "k":
		if m.graphRefSel > 0 {
			m.graphRefSel--
		}
	case "down", "j":
		if m.graphRefSel < len(refs)-1 {
			m.graphRefSel++
		}
	case "enter":
		// re-center the explorer on the selected ref (walk the graph).
		if m.graphRefSel < len(refs) {
			r := refs[m.graphRefSel]
			m.graphStack = append(m.graphStack, m.graphCenter)
			m.graphCenter = centerOfRef(r)
			m.graphPrecise = false
			m.graphRefSel = 0
			m.statusMsg = "→ " + displayName(r.FQN, r.Symbol)
			return m, m.detailCmd(r.Symbol)
		}
	}
	return m, nil
}

// srcViewport is the number of source lines visible at once (body height minus
// the title line and a separator).
func (m Model) srcViewport() int {
	vp := m.height - 3 - 2 // header/tabbar/footer, then viewer title + blank
	if vp < 1 {
		vp = 1
	}
	return vp
}

func (m Model) maxSrcScroll() int {
	if n := len(m.srcLines) - m.srcViewport(); n > 0 {
		return n
	}
	return 0
}

// handleSourceKey scrolls the source overlay or dismisses it.
func (m Model) handleSourceKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc", "q", "s":
		m.srcView = false
	case "up", "k":
		if m.srcScroll > 0 {
			m.srcScroll--
		}
	case "down", "j":
		if m.srcScroll < m.maxSrcScroll() {
			m.srcScroll++
		}
	case "pgup", "b":
		m.srcScroll = clamp(m.srcScroll-m.srcViewport(), 0, m.maxSrcScroll())
	case "pgdown", "f", " ":
		m.srcScroll = clamp(m.srcScroll+m.srcViewport(), 0, m.maxSrcScroll())
	case "home", "g":
		m.srcScroll = 0
	case "end", "G":
		m.srcScroll = m.maxSrcScroll()
	}
	return m, nil
}

// onActivate fires the data load a tab needs on first view.
func (m Model) onActivate() tea.Cmd {
	if m.active == tabGraph && !m.graphLoaded {
		return m.hubsCmd()
	}
	return nil
}

func digitToTab(key string) int {
	switch key {
	case "1":
		return int(tabGraph)
	case "2":
		return int(tabMetrics)
	case "3":
		return int(tabImpact)
	case "4":
		return int(tabSearch)
	}
	return -1
}

// syncFocus focuses the active tab's input and blurs the others.
func (m *Model) syncFocus() {
	switch m.active {
	case tabSearch:
		m.search.Focus()
		m.impact.Blur()
	case tabImpact:
		m.impact.Focus()
		m.search.Blur()
	default:
		m.search.Blur()
		m.impact.Blur()
	}
}
