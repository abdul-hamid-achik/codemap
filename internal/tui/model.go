// Package tui is codemap's interactive terminal UI ("studio"), built on Charm
// v2 (charm.land/bubbletea, lipgloss, bubbles). It is a thin view over
// internal/app: a tabbed explorer with Graph, Metrics, Impact, Search, and Path.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/harmonica"

	"github.com/abdul-hamid-achik/codemap/internal/app"
	"github.com/abdul-hamid-achik/codemap/internal/daemon"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
	"github.com/abdul-hamid-achik/codemap/internal/index"
)

type tab int

const (
	tabGraph tab = iota
	tabMetrics
	tabImpact
	tabSearch
	tabPath
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
	case tabPath:
		return "Path"
	}
	return ""
}

// async messages
type statusMsg struct {
	st  *app.StatusReport
	err error
}
type stalenessMsg struct {
	st *index.Staleness
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
type pathMsg struct {
	from pathEndpoint
	to   pathEndpoint
	rep  *app.PathReport
	err  error
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
	symbol      string
	callers     []app.SymbolRef
	callees     []app.SymbolRef
	annotations []graph.Annotation
	err         error
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
	title       string
	lines       []string
	gutter      bool // line-number gutter (source); false for the context card
	highlighted bool // lines carry chroma ANSI styling → clip ANSI-aware, don't rune-truncate
	firstLine   int  // file line number of lines[0], so the gutter shows real file lines (0 = fall back to 1-based)
	err         error
}

// Model is the studio TUI state.
type Model struct {
	ctx      context.Context
	service  *app.Service
	startDir string

	active tab
	width  int
	height int

	loading    bool
	statusMsg  string
	errMsg     string
	status     *app.StatusReport
	daemon     *daemon.Info     // live background-daemon state (nil = none running); polled periodically
	stale      *index.Staleness // index drift vs the working tree (computed async; nil until known)
	orphans    []app.SymbolRef  // dead-code candidates, for the Metrics overview
	metricsSel int              // selected row in the Metrics right column (hubs+orphans)

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

	// path tab: two endpoint editors plus an ordered shortest-path result. Each
	// endpoint retains the full graphCenter when it came from a selection, so the
	// async query seam can move to selector-aware app.Path without redesigning the
	// TUI. A manually edited value intentionally degrades to its exact text/FQN.
	pathFromInput textinput.Model
	pathToInput   textinput.Model
	pathFrom      pathEndpoint
	pathTo        pathEndpoint
	pathFocus     pathFocus
	pathRep       *app.PathReport
	pathSel       int
	pathLoading   bool
	pathErr       string

	// graph tab (call-graph explorer)
	graphLoaded      bool
	graphHubs        []app.HotspotRef
	graphSel         int
	graphSym         string
	graphCallers     []app.SymbolRef
	graphCallees     []app.SymbolRef
	graphAnnotations []graph.Annotation // annotations pinned to the centered node
	graphPrecise     bool               // hub detail is showing gopls-precise relations
	graphMap         bool               // render the centered node's neighborhood as a map (toggle: m)

	showHelp bool // a full-screen keybinding overlay, toggled with `?`

	// scrollable text overlay: a full-screen pager dismissed with esc/q. Serves
	// two views — a symbol's source body (opened with `s`/ctrl+s, line-number
	// gutter on) and the context "orient" card (opened with ctrl+o, gutter off).
	srcView      bool
	srcTitle     string
	srcLines     []string
	srcScroll    int
	srcGutter    bool
	srcHighlight bool // srcLines carry chroma ANSI styling (source overlay only)
	srcFirstLine int  // file line of srcLines[0], for the gutter (0 = 1-based fallback)

	// graph walking: the right pane can be focused to walk into callers/callees,
	// re-centering the explorer on any node (not just hubs). graphStack records
	// the path so backspace pops back.
	graphFocus  graphFocus
	graphRefSel int           // selection across the combined callers+callees list
	graphCenter graphCenter   // node the detail pane is currently centered on
	graphStack  []graphCenter // breadcrumb of centers walked into

	// global navigation history (browser-style): each cross-view drill snapshots the
	// WHOLE view (active tab + both bar texts + selections + graph state + overlay) so
	// back/forward restores it exactly — including the query you'd typed. A layer above
	// graphStack, which stays the within-Graph walk (backspace).
	navHist []navState // back stack
	navFwd  []navState // forward stack (cleared when a new drill forks history)

	// Reveal animations (harmonica springs). metricsReveal scales the Metrics bar
	// fill and mapReveal grows the Graph map's branches, each 0→1 on activation; both
	// default to 1 (fully shown) so a directly-rendered model and the render tests see
	// the finished UI. revealing = the shared frame loop is live. See anim.go.
	revealSpring  harmonica.Spring
	metricsReveal float64
	metricsVel    float64
	metricsActive bool
	mapReveal     float64
	mapVel        float64
	mapActive     bool
	revealing     bool
	spinnerFrame  int // advances while busy() to animate the footer loading spinner
}

// busy reports whether an async operation is in flight — the initial load, or a
// command whose status ends in "…" (searching/analyzing/indexing/resolving). Drives
// the footer spinner and keeps the animation frame loop running until it clears.
func (m Model) busy() bool {
	return m.loading || m.pathLoading || strings.HasSuffix(m.statusMsg, "…")
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
	sym, fqn, kind, file string
	line                 int
}

// pathEndpoint is a source/target chosen for a path query. query is the exact
// text shown in the editor; center carries selector-ready identity when the
// endpoint came from a graph/search/impact/metrics/path selection.
type pathEndpoint struct {
	query  string
	center graphCenter
}

func endpointFromCenter(c graphCenter) pathEndpoint {
	return pathEndpoint{query: displayName(c.fqn, c.sym), center: c}
}

func (e pathEndpoint) serviceQuery() string {
	if e.center.fqn != "" {
		return e.center.fqn
	}
	if e.center.sym != "" {
		return e.center.sym
	}
	return strings.TrimSpace(e.query)
}

// pathFocus identifies the editable source/target field or the returned path.
type pathFocus int

const (
	focusPathFrom pathFocus = iota
	focusPathTo
	focusPathResult
)

func centerOfHub(h app.HotspotRef) graphCenter {
	return graphCenter{sym: h.Symbol, fqn: h.FQN, kind: h.Kind, file: h.File, line: h.StartLine}
}

func centerOfRef(r app.SymbolRef) graphCenter {
	return graphCenter{sym: r.Symbol, fqn: r.FQN, kind: r.Kind, file: r.File, line: r.StartLine}
}

func (c graphCenter) selector() (app.SymbolSelector, bool) {
	if c.file == "" || c.line <= 0 {
		return app.SymbolSelector{}, false
	}
	return app.SymbolSelector{File: c.file, StartLine: c.line, FQN: c.fqn, Kind: c.kind}, true
}

// navState is a snapshot of the whole studio view for the global back/forward
// history. Result payloads (hits, impact report, graph relations) are captured so
// restore is pure in-memory — no refetch, no flicker, exact selection preserved.
type navState struct {
	active                  tab
	searchQuery, searchMode string
	searchHits              []app.SemanticHit
	searchSel               int
	impactBar, impactSymbol string
	impactRep               *app.ImpactReport
	impactSel               int
	pathFromBar, pathToBar  string
	pathFrom, pathTo        pathEndpoint
	pathFocus               pathFocus
	pathRep                 *app.PathReport
	pathSel                 int
	pathErr                 string
	graphSym                string
	graphCenter             graphCenter
	graphStack              []graphCenter
	graphFocus              graphFocus
	graphRefSel, graphSel   int
	graphCallers            []app.SymbolRef
	graphCallees            []app.SymbolRef
	graphAnnotations        []graph.Annotation
	graphPrecise            bool
	srcView, srcGutter      bool
	srcTitle                string
	srcLines                []string
	srcScroll               int
}

// snapshot captures the current view into a navState.
func (m Model) snapshot() navState {
	return navState{
		active:      m.active,
		searchQuery: m.search.Value(), searchMode: m.searchMode, searchHits: m.searchHits, searchSel: m.searchSel,
		impactBar: m.impact.Value(), impactSymbol: m.impactSymbol, impactRep: m.impactRep, impactSel: m.impactSel,
		pathFromBar: m.pathFromInput.Value(), pathToBar: m.pathToInput.Value(),
		pathFrom: m.pathFrom, pathTo: m.pathTo, pathFocus: m.pathFocus, pathRep: m.pathRep, pathSel: m.pathSel, pathErr: m.pathErr,
		graphSym: m.graphSym, graphCenter: m.graphCenter,
		graphStack: append([]graphCenter(nil), m.graphStack...), // graphStack is mutated in place — copy it
		graphFocus: m.graphFocus, graphRefSel: m.graphRefSel, graphSel: m.graphSel,
		graphCallers: m.graphCallers, graphCallees: m.graphCallees, graphAnnotations: m.graphAnnotations,
		graphPrecise: m.graphPrecise,
		srcView:      m.srcView, srcGutter: m.srcGutter, srcTitle: m.srcTitle, srcLines: m.srcLines, srcScroll: m.srcScroll,
	}
}

// restore writes a navState back into the model (including the text in both bars).
func (m *Model) restore(s navState) {
	m.active = s.active
	m.search.SetValue(s.searchQuery)
	m.searchQuery, m.searchMode, m.searchHits, m.searchSel = s.searchQuery, s.searchMode, s.searchHits, s.searchSel
	m.impact.SetValue(s.impactBar)
	m.impactSymbol, m.impactRep, m.impactSel = s.impactSymbol, s.impactRep, s.impactSel
	m.pathFromInput.SetValue(s.pathFromBar)
	m.pathToInput.SetValue(s.pathToBar)
	m.pathFrom, m.pathTo, m.pathFocus, m.pathRep, m.pathSel, m.pathErr = s.pathFrom, s.pathTo, s.pathFocus, s.pathRep, s.pathSel, s.pathErr
	m.graphSym, m.graphCenter, m.graphStack = s.graphSym, s.graphCenter, s.graphStack
	m.graphFocus, m.graphRefSel, m.graphSel = s.graphFocus, s.graphRefSel, s.graphSel
	m.graphCallers, m.graphCallees, m.graphAnnotations = s.graphCallers, s.graphCallees, s.graphAnnotations
	m.graphPrecise = s.graphPrecise
	m.srcView, m.srcGutter, m.srcTitle, m.srcLines, m.srcScroll = s.srcView, s.srcGutter, s.srcTitle, s.srcLines, s.srcScroll
	m.syncFocus()
}

// pushNav records the current view onto the back stack and forks history (clears the
// forward stack). Call BEFORE mutating state for a cross-view drill.
func (m *Model) pushNav() {
	m.navHist = append(m.navHist, m.snapshot())
	m.navFwd = nil
}

// navBack restores the previous view (pushing the current onto the forward stack).
func (m Model) navBack() (tea.Model, tea.Cmd) {
	if len(m.navHist) == 0 {
		m.statusMsg = "nothing to go back to"
		return m, nil
	}
	m.navFwd = append(m.navFwd, m.snapshot())
	prev := m.navHist[len(m.navHist)-1]
	m.navHist = m.navHist[:len(m.navHist)-1]
	m.restore(prev)
	m.statusMsg = fmt.Sprintf("‹ back · %d back · %d fwd", len(m.navHist), len(m.navFwd))
	return m, nil
}

// navForward re-walks a view popped by navBack.
func (m Model) navForward() (tea.Model, tea.Cmd) {
	if len(m.navFwd) == 0 {
		m.statusMsg = "nothing forward"
		return m, nil
	}
	m.navHist = append(m.navHist, m.snapshot())
	nxt := m.navFwd[len(m.navFwd)-1]
	m.navFwd = m.navFwd[:len(m.navFwd)-1]
	m.restore(nxt)
	m.statusMsg = fmt.Sprintf("forward › · %d back · %d fwd", len(m.navHist), len(m.navFwd))
	return m, nil
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

	pf := textinput.New()
	pf.Placeholder = "source symbol or FQN, e.g. api.Handle"
	pf.SetWidth(56)
	pt := textinput.New()
	pt.Placeholder = "target symbol or FQN, e.g. store.Save"
	pt.SetWidth(56)

	return Model{
		ctx:           ctx,
		service:       app.NewService(sess),
		startDir:      startDir,
		active:        tabGraph,
		loading:       true,
		search:        s,
		impact:        i,
		pathFromInput: pf,
		pathToInput:   pt,
		pathFocus:     focusPathFrom,
		revealSpring:  newRevealSpring(),
		metricsReveal: 1, // fully shown until a tab activation triggers the grow-in
		mapReveal:     1, // fully shown until the map toggle triggers the grow-in
	}
}

// Init loads project status, the call graph, and dead-code candidates.
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.statusCmd(), m.hubsCmd(), m.orphansCmd(), m.stalenessCmd(), m.daemonCmd(), daemonTick())
}

// ---- commands ----

func (m Model) statusCmd() tea.Cmd {
	svc, dir := m.service, m.startDir
	return func() tea.Msg {
		st, err := svc.Status(dir)
		return statusMsg{st: st, err: err}
	}
}

// daemonPollInterval is how often studio re-checks whether a background daemon is
// keeping the index fresh — cheap (a sub-millisecond socket dial that fails fast
// when none is running), so it can run on a steady tick without being noticeable.
const daemonPollInterval = 4 * time.Second

type daemonMsg struct{ info *daemon.Info }
type daemonTickMsg struct{}

// daemonCmd queries the live daemon state once (off the UI goroutine).
func (m Model) daemonCmd() tea.Cmd {
	return func() tea.Msg { return daemonMsg{info: daemon.QueryStatus()} }
}

// daemonTick schedules the next daemon poll; the handler re-arms it, so the
// indicator stays live as a daemon starts or stops while studio is open.
func daemonTick() tea.Cmd {
	return tea.Tick(daemonPollInterval, func(time.Time) tea.Msg { return daemonTickMsg{} })
}

// stalenessCmd checks how far the index has drifted from the working tree, off
// the UI thread (a walk+hash) so studio startup stays instant — the indicator
// just appears once it's known. A failure is swallowed (nil = no warning).
func (m Model) stalenessCmd() tea.Cmd {
	svc, dir := m.service, m.startDir
	return func() tea.Msg {
		st, _ := svc.Staleness(dir)
		return stalenessMsg{st: st}
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
		// ca.Annotations is the queried symbol's pinned notes/data (free — already
		// gathered by Callers), so the Graph detail shows them with no extra query.
		return graphDetailMsg{symbol: sym, callers: ca.Results, callees: ce.Results, annotations: ca.Annotations}
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

// selectedCenter is the symbol currently selected on the active tab, as a
// graphCenter (sym/fqn/file/line): the selected ref/hub on Graph, the selected
// blast node (or analyzed symbol) on Impact, the selected hit on Search, the
// selected row on Metrics. It's the basis for both ctrl+s (view source) and
// ctrl+g (open in the Graph walker), so the two always act on the same target.
func (m Model) selectedCenter() (graphCenter, bool) {
	switch m.active {
	case tabGraph:
		if m.graphFocus == focusRefs {
			if refs := m.graphRefs(); m.graphRefSel < len(refs) {
				return centerOfRef(refs[m.graphRefSel]), true
			}
			return graphCenter{}, false
		}
		return m.graphCenter, m.graphCenter.sym != ""
	case tabImpact:
		if m.impactRep != nil {
			if m.impactSel < len(m.impactRep.BlastRadius) {
				n := m.impactRep.BlastRadius[m.impactSel]
				return graphCenter{sym: n.Symbol, fqn: n.FQN, kind: n.Kind, file: n.File, line: n.StartLine}, true
			}
			if len(m.impactRep.Locations) > 0 {
				l := m.impactRep.Locations[0]
				return graphCenter{sym: l.Symbol, fqn: l.FQN, kind: l.Kind, file: l.File, line: l.StartLine}, true
			}
		}
	case tabSearch:
		if m.searchSel < len(m.searchHits) {
			h := m.searchHits[m.searchSel]
			return graphCenter{sym: h.Symbol, fqn: h.FQN, kind: h.Kind, file: h.File, line: h.StartLine}, true
		}
	case tabMetrics:
		if sym, fqn, kind, file, line, ok := m.metricsItem(m.metricsSel); ok {
			return graphCenter{sym: sym, fqn: fqn, kind: kind, file: file, line: line}, true
		}
	case tabPath:
		if m.pathRep != nil && m.pathSel >= 0 && m.pathSel < len(m.pathRep.Path) {
			return centerOfRef(m.pathRep.Path[m.pathSel]), true
		}
	}
	return graphCenter{}, false
}

// sourceTarget is the selected symbol's source location (sym/file/line) — the
// subset of selectedCenter the source overlay needs.
func (m Model) sourceTarget() (sym, file string, line int, ok bool) {
	c, ok := m.selectedCenter()
	return c.sym, c.file, c.line, ok
}

// viewSource opens the source overlay for the current selection, if any.
func (m Model) viewSource() tea.Cmd {
	if c, ok := m.selectedCenter(); ok {
		return m.sourceViewCmd(c.sym, c.file, c.line)
	}
	return nil
}

// openInGraph re-centers the Graph walker on the active tab's selected symbol
// and switches to it, focused on the callers/calls pane — so any symbol found by
// Search/Impact/Metrics becomes a place to start walking the call graph, not just
// the hubs. Returns the model unchanged if nothing is selected.
func (m Model) openInGraph() (tea.Model, tea.Cmd) {
	c, ok := m.selectedCenter()
	if !ok || c.sym == "" {
		return m, nil
	}
	m.pushNav() // remember where we came from (tab + bar text) before the graph clobbers it
	m.active = tabGraph
	m.graphCenter = c
	m.graphStack = nil
	m.graphPrecise = false
	m.graphFocus = focusRefs
	m.graphRefSel = 0
	m.syncFocus()
	m.statusMsg = "graph: " + displayName(c.fqn, c.sym)
	return m, m.detailCmd(c.sym)
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
		// Syntax-highlight by the file's language; fall back to plain on an
		// unknown/unsupported language so the overlay never errors, just isn't colored.
		// (Runs here, off the UI goroutine; highlighted once, then scrolled cheaply.)
		lines, hl := highlightSource(mch.File, mch.Source)
		if !hl {
			lines = strings.Split(mch.Source, "\n")
		}
		return sourceMsg{title: title, lines: lines, gutter: true, highlighted: hl, firstLine: mch.StartLine}
	}
}

// viewContext opens the context "orient" overlay for the current selection — the
// same one-call bundle as `codemap context` / codemap_context (definition +
// callers + callees + tests + blast radius + pinned annotations), so the studio
// has the flagship orientation view the CLI and MCP already expose.
func (m Model) viewContext() tea.Cmd {
	if c, ok := m.selectedCenter(); ok && c.sym != "" {
		return m.contextViewCmd(c.sym)
	}
	return nil
}

func (m Model) contextViewCmd(sym string) tea.Cmd {
	svc, dir := m.service, m.startDir
	return func() tea.Msg {
		rep, err := svc.Context(dir, sym, 0)
		if err != nil {
			return sourceMsg{err: err}
		}
		if rep == nil || !rep.Found {
			return sourceMsg{err: fmt.Errorf("no context for %q", sym)}
		}
		title, lines := contextCard(rep)
		return sourceMsg{title: title, lines: lines, gutter: false}
	}
}

// contextCard renders a ContextReport into a scrollable card styled to match the
// rest of the studio: flush-left section headers, indented detail rows, symbol
// names highlighted and locations muted. Each line is rendered ANSI-aware by the
// overlay (lipgloss MaxWidth), so styling is safe; every label/name is kept in a
// single styled segment so substring lookups (and tests) stay intact.
func contextCard(rep *app.ContextReport) (string, []string) {
	title := rep.Symbol
	var ls []string
	add := func(s string) { ls = append(ls, s) }
	refRow := func(r app.SymbolRef) string {
		return "  " + symStyle.Render(displayName(r.FQN, r.Symbol)) + "  " +
			mutedStyle.Render(fmt.Sprintf("%s  %s:%d", r.Kind, r.File, r.StartLine))
	}
	list := func(label string, total int, rows []string) {
		add(sectionStyle.Render(fmt.Sprintf("%s (%d)", label, total)))
		if len(rows) == 0 {
			add(mutedStyle.Render("  (none)"))
		}
		for _, r := range rows {
			add(r)
		}
		if total > len(rows) {
			add(mutedStyle.Render(fmt.Sprintf("  … +%d more", total-len(rows))))
		}
		add("")
	}

	if len(rep.Definitions) > 0 {
		d := rep.Definitions[0]
		title = displayName(d.FQN, d.Symbol)
		if d.Signature != "" {
			add(symStyle.Render(d.Signature))
		} else {
			add(symStyle.Render(strings.TrimSpace(d.Kind + " " + d.Symbol)))
		}
		if d.Doc != "" {
			for _, dl := range strings.Split(strings.TrimRight(d.Doc, "\n"), "\n") {
				add(mutedStyle.Render("  " + dl))
			}
		}
		add("")
		for _, def := range rep.Definitions {
			add(mutedStyle.Render(fmt.Sprintf("  %s:%d-%d", def.File, def.StartLine, def.EndLine)))
		}
		add("")
	}
	if rep.Note != "" {
		add(errorStyle.Render("⚠ " + rep.Note))
		add("")
	}

	callers := make([]string, 0, len(rep.Callers))
	for _, c := range rep.Callers {
		callers = append(callers, refRow(c))
	}
	list("Callers", rep.CallersTotal, callers)

	callees := make([]string, 0, len(rep.Callees))
	for _, c := range rep.Callees {
		callees = append(callees, refRow(c))
	}
	list("Callees", rep.CalleesTotal, callees)

	tests := make([]string, 0, len(rep.Tests))
	for _, t := range rep.Tests {
		tests = append(tests, "  "+symStyle.Render(displayName(t.FQN, t.Symbol))+"  "+
			mutedStyle.Render(fmt.Sprintf("%s:%d", t.File, t.StartLine)))
	}
	list("Tests", rep.TestsTotal, tests)

	blast := fmt.Sprintf("Blast radius: %d transitively affected", rep.BlastRadius)
	if rep.BlastDepth > 0 { // honest about scope: it's depth-bounded, like the CLI's "(depth ≤ N)"
		blast += fmt.Sprintf(" (depth ≤ %d)", rep.BlastDepth)
	}
	add(sectionStyle.Render(blast))
	if len(rep.Annotations) > 0 {
		add("")
		add(sectionStyle.Render(fmt.Sprintf("Annotations (%d)", len(rep.Annotations))))
		for _, a := range rep.Annotations {
			note := a.Note
			if note == "" {
				note = a.Data
			}
			add("  " + countStyle.Render("["+a.Source+"]") + " " + strings.TrimRight(note, " "))
		}
	}
	return title, ls
}

func (m Model) reindexCmd() tea.Cmd {
	ctx, svc, dir := m.ctx, m.service, m.startDir
	// Preserve precision: if the project currently has precise call edges, reindex
	// with --precise so ctrl+r refreshes the call graph instead of dropping it to
	// name-based (Go) or none (TypeScript/JS/Python need --precise for any calls).
	// Embeddings are skipped either way, so a refresh never needs Ollama.
	precise := m.reindexPrecise()
	return func() tea.Msg {
		rep, err := svc.Index(ctx, dir, index.Options{Precise: precise}, false)
		return indexedMsg{rep: rep, err: err}
	}
}

// reindexPrecise reports whether the in-studio reindex (ctrl+r) should run
// --precise — it does when the project already has precise edges, so refreshing
// keeps the exact call graph the user is exploring rather than discarding it.
func (m Model) reindexPrecise() bool {
	return m.status != nil && m.status.PreciseEdges > 0
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

// pathCmd is the single async boundary for Studio path lookup. Rich endpoints
// use exact source selectors; manually typed text/FQNs retain the legacy lookup
// fallback until the person chooses a concrete indexed symbol.
func (m Model) pathCmd(from, to pathEndpoint) tea.Cmd {
	svc, dir := m.service, m.startDir
	return func() tea.Msg {
		fromSelector, toSelector, exact := exactPathSelectors(from, to)
		var (
			r   *app.PathReport
			err error
		)
		if exact {
			r, err = svc.PathBySelectors(dir, fromSelector, toSelector)
		} else {
			r, err = svc.Path(dir, from.serviceQuery(), to.serviceQuery())
		}
		return pathMsg{from: from, to: to, rep: r, err: err}
	}
}

func exactPathSelectors(from, to pathEndpoint) (app.SymbolSelector, app.SymbolSelector, bool) {
	fromSelector, fromExact := from.center.selector()
	toSelector, toExact := to.center.selector()
	return fromSelector, toSelector, fromExact && toExact
}

// pathEndpointForInput preserves a rich center only while its displayed text is
// unchanged. Once a person edits the field, the exact text becomes the endpoint
// identity (and may itself be an FQN).
func pathEndpointForInput(value string, bound pathEndpoint) pathEndpoint {
	value = strings.TrimSpace(value)
	if value == bound.query {
		return bound
	}
	return pathEndpoint{query: value, center: graphCenter{sym: value}}
}

func (m *Model) bindPathFrom(c graphCenter) {
	m.pathFrom = endpointFromCenter(c)
	m.pathFromInput.SetValue(m.pathFrom.query)
}

// ---- update ----

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resizePathInputs()
		return m, nil

	case statusMsg:
		m.loading = false
		if msg.err != nil {
			m.errMsg = msg.err.Error()
			m.statusMsg = "" // P1-14 (B35): clear "…" so busy() stops
			return m, nil
		}
		m.status = msg.st
		if msg.st != nil && msg.st.Registered {
			m.statusMsg = "ready"
		} else {
			m.statusMsg = "no index — run 'codemap index'"
		}
		return m, nil

	case daemonMsg:
		m.daemon = msg.info
		return m, nil

	case daemonTickMsg:
		return m, tea.Batch(m.daemonCmd(), daemonTick())

	case animTickMsg:
		return m, m.advanceAnim()

	case stalenessMsg:
		m.stale = msg.st
		return m, nil

	case graphHubsMsg:
		m.graphLoaded = true
		if msg.err != nil {
			m.errMsg = msg.err.Error()
			m.statusMsg = "" // P1-14 (B35)
			return m, nil
		}
		m.errMsg = "" // P1-14 (B39): clear sticky error
		m.graphHubs = msg.hubs
		m.graphSel = 0
		m.metricsSel = 0
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
			m.statusMsg = "" // P1-14 (B35)
			return m, nil
		}
		m.errMsg = ""
		m.srcView = true
		m.srcTitle = msg.title
		m.srcLines = msg.lines
		m.srcGutter = msg.gutter
		m.srcHighlight = msg.highlighted
		m.srcFirstLine = msg.firstLine
		m.srcScroll = 0
		return m, nil

	case graphDetailMsg:
		if msg.err != nil {
			m.errMsg = msg.err.Error()
			m.statusMsg = "" // P1-14 (B35)
			return m, nil
		}
		m.errMsg = "" // P1-14 (B39): clear sticky error
		m.graphSym = msg.symbol
		m.graphCallers = msg.callers
		m.graphCallees = msg.callees
		m.graphAnnotations = msg.annotations
		m.graphPrecise = false
		m.graphRefSel = 0
		return m, nil

	case preciseDetailMsg:
		if msg.err != nil {
			m.errMsg = msg.err.Error()
			m.statusMsg = "" // P1-14 (B35)
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
			m.statusMsg = ""     // P1-14 (B35)
			m.graphLoaded = true // P1-14 (B35): restore — error doesn't mean no graph
			return m, nil
		}
		m.errMsg = ""
		if msg.rep != nil {
			m.statusMsg = fmt.Sprintf("reindexed: %d files · %d nodes · %d edges",
				msg.rep.FilesIndexed, msg.rep.Nodes, msg.rep.Edges)
			if msg.rep.FilesSkipped > 0 { // e.g. a language server timed out — don't hide it
				m.statusMsg += fmt.Sprintf(" · %d skipped", msg.rep.FilesSkipped)
			}
			if msg.rep.Warning != "" { // e.g. no Go files, or embeddings unavailable
				m.statusMsg = "⚠ " + msg.rep.Warning
			}
		}
		// Refresh everything the new index affects. The reindex just made the graph
		// fresh, so clear the stale indicator immediately and re-confirm async.
		m.stale = nil
		return m, tea.Batch(m.statusCmd(), m.hubsCmd(), m.orphansCmd(), m.stalenessCmd(), m.daemonCmd())

	case semanticMsg:
		if msg.err != nil {
			m.errMsg = msg.err.Error()
			m.statusMsg = "" // P1-14 (B35)
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
			m.statusMsg = "" // P1-14 (B35)
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

	case pathMsg:
		m.pathLoading = false
		if msg.err != nil {
			m.pathErr = msg.err.Error()
			m.errMsg = msg.err.Error()
			m.statusMsg = ""
			m.pathRep = nil
			m.pathFocus = focusPathTo
			m.syncFocus()
			return m, nil
		}
		m.errMsg = ""
		m.pathErr = ""
		m.pathFrom, m.pathTo = msg.from, msg.to
		m.pathRep = msg.rep
		m.pathSel = 0
		if msg.rep == nil {
			m.pathErr = "path query returned no report"
			m.errMsg = m.pathErr
			m.statusMsg = ""
			m.pathFocus = focusPathTo
			m.syncFocus()
			return m, nil
		}
		// Selector-aware app reports retain exact endpoint identity even when the
		// two real nodes are disconnected and Path is empty.
		if sel := msg.rep.FromSelector; sel != nil {
			m.pathFrom.center = graphCenter{sym: msg.rep.From, fqn: sel.FQN, kind: sel.Kind, file: sel.File, line: sel.StartLine}
		}
		if sel := msg.rep.ToSelector; sel != nil {
			m.pathTo.center = graphCenter{sym: msg.rep.To, fqn: sel.FQN, kind: sel.Kind, file: sel.File, line: sel.StartLine}
		}
		if len(msg.rep.Path) > 0 {
			// Upgrade typed endpoints with exact selector-ready locations returned by
			// app.Path, while preserving the text the person entered.
			m.pathFrom.center = centerOfRef(msg.rep.Path[0])
			m.pathTo.center = centerOfRef(msg.rep.Path[len(msg.rep.Path)-1])
			m.pathFocus = focusPathResult
			hops := len(msg.rep.Path) - 1
			m.statusMsg = fmt.Sprintf("path found · %d hops · call graph %s", hops, msg.rep.CallGraph)
		} else {
			m.pathFocus = focusPathTo
			switch msg.rep.CallGraph {
			case app.CallGraphUnresolved:
				m.statusMsg = "path unresolved · call graph incomplete"
			case app.CallGraphNone:
				m.statusMsg = "endpoint not found"
			default:
				m.statusMsg = "no path · call graph " + msg.rep.CallGraph
			}
		}
		m.syncFocus()
		return m, nil

	case tea.KeyPressMsg:
		model, cmd := m.handleKey(msg)
		// If the key kicked off an async op (status ends in "…"), spin the footer.
		if nm, ok := model.(Model); ok && nm.busy() {
			if k := nm.kickAnim(); k != nil {
				return nm, tea.Batch(cmd, k)
			}
			return nm, cmd
		}
		return model, cmd
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "ctrl+c" {
		return m, tea.Quit
	}
	// Modal overlays capture keys until dismissed. Help takes precedence; `?`
	// works on any tab (searching for "?" isn't meaningful, so capturing it is
	// safe even where a text input is focused).
	if m.showHelp {
		if key == "?" || key == "esc" || key == "q" {
			m.showHelp = false
		}
		return m, nil
	}
	if m.srcView {
		return m.handleSourceKey(key)
	}
	if key == "?" {
		m.showHelp = true
		return m, nil
	}
	// esc steps back one global nav level — the instinctive "get me back". Yields to
	// the Graph refs pane (esc there returns to the hub list) and only fires when
	// there's history; otherwise it falls through (a no-op on Search/Impact/Metrics).
	if key == "esc" && len(m.navHist) > 0 && !(m.active == tabGraph && m.graphFocus == focusRefs) {
		return m.navBack()
	}
	switch key {
	case "alt+left": // browser-style back across tabs/drills, restoring the bar text
		return m.navBack()
	case "alt+right":
		return m.navForward()
	case "ctrl+s":
		// View the current selection's source on any tab (a modifier key so it
		// works even where a text input would otherwise capture `s`).
		return m, m.viewSource()
	case "ctrl+o":
		// Orient: the context bundle for the current selection on any tab — def +
		// callers + callees + tests + blast + annotations in one overlay.
		return m, m.viewContext()
	case "ctrl+g":
		// Open the current selection in the Graph walker (any tab) — explore a
		// search hit / blast node / hub's call neighborhood, not just the hubs.
		return m.openInGraph()
	case "ctrl+r":
		// P1-14 (B38): ignore ctrl+r while a reindex is already in flight —
		// a second concurrent reindex write pass would corrupt the graph.
		if m.statusMsg == "indexing…" {
			return m, nil
		}
		// Reindex in place (structure-only) and refresh — works on any tab.
		m.statusMsg = "indexing…"
		m.graphLoaded = false
		return m, m.reindexCmd()
	case "tab":
		return m, m.switchTab((m.active + 1) % tabCount)
	case "shift+tab":
		return m, m.switchTab((m.active + tabCount - 1) % tabCount)
	}

	// alt+1..5 switch tabs from ANY tab — including Search/Impact/Path, where a bare
	// digit is intentionally typed into the query/symbol input instead (so e.g.
	// "sha256"/"oauth2" still work). Mirrors the alt+←/→ global-nav modifiers.
	if after, ok := strings.CutPrefix(key, "alt+"); ok {
		if d := digitToTab(after); d >= 0 {
			return m, m.switchTab(tab(d))
		}
	}

	// Bare number keys switch tabs too, but only when the focused tab isn't a text
	// input (on Search/Impact/Path the digit goes into the query — use alt+# to switch).
	pathEditing := m.active == tabPath && m.pathFocus != focusPathResult
	if m.active != tabSearch && m.active != tabImpact && !pathEditing {
		if d := digitToTab(key); d >= 0 {
			return m, m.switchTab(tab(d))
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
				m.pushNav() // so back returns to this Search view with the query + hit intact
				m.active = tabImpact
				m.impact.SetValue(sym)
				m.syncFocus()
				m.impactSel = 0
				m.statusMsg = "analyzing…"
				return m, m.impactCmd(sym)
			}
			return m, nil
		case "up": // single-line input ignores up/down, so use them to move the selection
			m.searchSel = clampIdx(m.searchSel-1, len(m.searchHits))
			return m, nil
		case "down":
			m.searchSel = clampIdx(m.searchSel+1, len(m.searchHits))
			return m, nil
		case "pgup":
			m.searchSel = clampIdx(m.searchSel-m.pageStep(), len(m.searchHits))
			return m, nil
		case "pgdown":
			m.searchSel = clampIdx(m.searchSel+m.pageStep(), len(m.searchHits))
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
				m.pushNav() // so back returns to the symbol we drilled from, with its blast list
				m.impact.SetValue(sym)
				m.impactSel = 0
				m.statusMsg = "analyzing…"
				return m, m.impactCmd(sym)
			}
			return m, nil
		case "up":
			m.impactSel = clampIdx(m.impactSel-1, m.blastLen())
			return m, nil
		case "down":
			m.impactSel = clampIdx(m.impactSel+1, m.blastLen())
			return m, nil
		case "pgup":
			m.impactSel = clampIdx(m.impactSel-m.pageStep(), m.blastLen())
			return m, nil
		case "pgdown":
			m.impactSel = clampIdx(m.impactSel+m.pageStep(), m.blastLen())
			return m, nil
		}
		m.impact, cmd = m.impact.Update(msg)
		return m, cmd

	case tabGraph:
		return m.handleGraphKey(key)

	case tabPath:
		return m.handlePathKey(msg)

	default: // metrics
		return m.handleMetricsKey(key)
	}
}

// handlePathKey drives the two-stage human workflow: edit FROM/TO, run the
// asynchronous app.Path query, then inspect/walk the ordered result. Arrow keys
// move between editors; once a result is focused both arrows and vim j/k navigate.
func (m Model) handlePathKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.pathLoading {
		return m, nil // one lookup at a time; global tab/quit keys were handled above
	}

	if m.pathFocus == focusPathResult {
		switch key {
		case "q":
			return m, tea.Quit
		case "up", "k":
			m.pathSel = clampIdx(m.pathSel-1, m.pathLen())
		case "down", "j":
			m.pathSel = clampIdx(m.pathSel+1, m.pathLen())
		case "pgup":
			m.pathSel = clampIdx(m.pathSel-m.pageStep(), m.pathLen())
		case "pgdown":
			m.pathSel = clampIdx(m.pathSel+m.pageStep(), m.pathLen())
		case "home", "g":
			m.pathSel = 0
		case "end", "G":
			m.pathSel = clampIdx(m.pathLen()-1, m.pathLen())
		case "f", "e", "i":
			m.pathFocus = focusPathFrom
			m.syncFocus()
		case "t":
			m.pathFocus = focusPathTo
			m.syncFocus()
		case "r":
			return m.startPathLookup()
		case "enter":
			return m.openInGraph()
		}
		return m, nil
	}

	switch key {
	case "up":
		m.pathFocus = focusPathFrom
		m.syncFocus()
		return m, nil
	case "down":
		m.pathFocus = focusPathTo
		m.syncFocus()
		return m, nil
	case "enter":
		if m.pathFocus == focusPathFrom {
			m.pathFocus = focusPathTo
			m.syncFocus()
			return m, nil
		}
		return m.startPathLookup()
	}

	var cmd tea.Cmd
	if m.pathFocus == focusPathFrom {
		m.pathFromInput, cmd = m.pathFromInput.Update(msg)
	} else {
		m.pathToInput, cmd = m.pathToInput.Update(msg)
	}
	return m, cmd
}

func (m Model) startPathLookup() (tea.Model, tea.Cmd) {
	from := pathEndpointForInput(m.pathFromInput.Value(), m.pathFrom)
	to := pathEndpointForInput(m.pathToInput.Value(), m.pathTo)
	if from.serviceQuery() == "" {
		m.pathErr = "choose a source symbol"
		m.errMsg = ""
		m.statusMsg = ""
		m.pathFocus = focusPathFrom
		m.syncFocus()
		return m, nil
	}
	if to.serviceQuery() == "" {
		m.pathErr = "choose a target symbol"
		m.errMsg = ""
		m.statusMsg = ""
		m.pathFocus = focusPathTo
		m.syncFocus()
		return m, nil
	}
	m.pathFrom, m.pathTo = from, to
	m.pathErr = ""
	m.errMsg = ""
	m.pathLoading = true
	m.statusMsg = "finding path…"
	m.syncFocus()
	return m, m.pathCmd(from, to)
}

func (m Model) pathLen() int {
	if m.pathRep == nil {
		return 0
	}
	return len(m.pathRep.Path)
}

// metricsCount is the number of selectable rows in the Metrics right column
// (top hubs followed by dead-code candidates).
func (m Model) metricsCount() int { return len(m.graphHubs) + len(m.orphans) }

// metricsItem resolves a combined-list index to its symbol (hubs first, then
// orphans).
func (m Model) metricsItem(i int) (sym, fqn, kind, file string, line int, ok bool) {
	if i < 0 {
		return "", "", "", "", 0, false
	}
	if i < len(m.graphHubs) {
		h := m.graphHubs[i]
		return h.Symbol, h.FQN, h.Kind, h.File, h.StartLine, true
	}
	if j := i - len(m.graphHubs); j < len(m.orphans) {
		o := m.orphans[j]
		return o.Symbol, o.FQN, o.Kind, o.File, o.StartLine, true
	}
	return "", "", "", "", 0, false
}

// handleMetricsKey navigates the dashboard's hubs/dead-code lists and drills the
// selection into Impact (enter) — ctrl+s (handled globally) reads its source.
func (m Model) handleMetricsKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "q":
		return m, tea.Quit
	case "o":
		// orient: the context card for the selected row (mirrors ctrl+o globally).
		return m, m.viewContext()
	case "up", "k":
		m.metricsSel = clampIdx(m.metricsSel-1, m.metricsCount())
	case "down", "j":
		m.metricsSel = clampIdx(m.metricsSel+1, m.metricsCount())
	case "pgup":
		m.metricsSel = clampIdx(m.metricsSel-m.pageStep(), m.metricsCount())
	case "pgdown":
		m.metricsSel = clampIdx(m.metricsSel+m.pageStep(), m.metricsCount())
	case "home":
		m.metricsSel = 0
	case "end":
		m.metricsSel = clampIdx(m.metricsCount()-1, m.metricsCount())
	case "enter":
		if sym, _, _, _, _, ok := m.metricsItem(m.metricsSel); ok {
			m.pushNav() // so back returns to the Metrics dashboard with this row selected
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

// pageStep is roughly one screenful of list rows, for pgup/pgdn jumps.
func (m Model) pageStep() int { return clamp(m.height-6, 1, 40) }

// blastLen is the number of selectable blast-radius rows on the Impact tab.
func (m Model) blastLen() int {
	if m.impactRep == nil {
		return 0
	}
	return len(m.impactRep.BlastRadius)
}

// clampIdx keeps a selection index within [0, n-1] (0 when the list is empty).
func clampIdx(i, n int) int {
	if n <= 0 || i < 0 {
		return 0
	}
	if i > n-1 {
		return n - 1
	}
	return i
}

// selectHub moves the Graph hub selection to idx (clamped) and loads its detail.
func (m Model) selectHub(idx int) (tea.Model, tea.Cmd) {
	idx = clampIdx(idx, len(m.graphHubs))
	if len(m.graphHubs) == 0 || idx == m.graphSel {
		return m, nil
	}
	m.graphSel = idx
	m.graphPrecise = false
	m.graphCenter = centerOfHub(m.graphHubs[idx])
	return m, m.detailCmd(m.graphHubs[idx].Symbol)
}

// handleGraphKey drives the call-graph explorer. The left pane (focusHubs)
// browses hubs as jump points; the right pane (focusRefs) walks the centered
// node's callers/callees, re-centering on enter so you can traverse the graph.
func (m Model) handleGraphKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "q":
		return m, tea.Quit
	case "p":
		// recompute the centered node's relations precisely via gopls — but on a
		// project indexed with --precise the stored edges are already exact, so the
		// gopls round-trip is redundant; say so instead of spawning it.
		if m.graphCenter.sym != "" {
			if m.status != nil && m.status.PreciseEdges > 0 {
				m.statusMsg = "already precise — these relations are from the --precise index"
				return m, nil
			}
			m.statusMsg = "resolving precise (gopls)…"
			return m, m.preciseDetailCmd(m.graphCenter)
		}
		return m, nil
	case "s":
		// view the selected node's source code in a scrollable overlay.
		return m, m.viewSource()
	case "m":
		// toggle the centered node's neighborhood map (callers → node → callees);
		// grow its branches in with a spring when switching it on.
		m.graphMap = !m.graphMap
		if m.graphMap {
			return m, m.startMapReveal()
		}
		return m, nil
	case "o":
		// orient: the context card for the selected node (mirrors ctrl+o globally).
		return m, m.viewContext()
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
		return m.selectHub(m.graphSel - 1)
	case "down", "j":
		return m.selectHub(m.graphSel + 1)
	case "pgup":
		return m.selectHub(m.graphSel - m.pageStep())
	case "pgdown":
		return m.selectHub(m.graphSel + m.pageStep())
	case "home":
		return m.selectHub(0)
	case "end":
		return m.selectHub(len(m.graphHubs) - 1)
	case "enter":
		// drill the centered hub into full impact analysis
		if len(m.graphHubs) > 0 {
			sym := m.graphHubs[m.graphSel].Symbol
			m.pushNav() // so back returns to the Graph tab as it was
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
		m.graphRefSel = clampIdx(m.graphRefSel-1, len(refs))
	case "down", "j":
		m.graphRefSel = clampIdx(m.graphRefSel+1, len(refs))
	case "pgup":
		m.graphRefSel = clampIdx(m.graphRefSel-m.pageStep(), len(refs))
	case "pgdown":
		m.graphRefSel = clampIdx(m.graphRefSel+m.pageStep(), len(refs))
	case "home":
		m.graphRefSel = 0
	case "end":
		m.graphRefSel = clampIdx(len(refs)-1, len(refs))
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

// onTabActivate runs onActivate and, when the new tab is Metrics, also triggers the
// spring grow-in of its bar charts — so every switch to Metrics replays the reveal.
func (m *Model) onTabActivate() tea.Cmd {
	cmd := m.onActivate()
	if m.active == tabMetrics {
		if r := m.startMetricsReveal(); r != nil {
			return tea.Batch(cmd, r)
		}
	}
	return cmd
}

// switchTab centralizes focus changes and gives Path a task-oriented entry:
// when its FROM field is empty, carry the current rich selection across as the
// source endpoint and put the cursor directly in TO.
func (m *Model) switchTab(next tab) tea.Cmd {
	selected, hasSelection := m.selectedCenter()
	m.active = next
	if next == tabPath && m.pathFromInput.Value() == "" {
		if hasSelection && selected.sym != "" {
			m.bindPathFrom(selected)
			m.pathFocus = focusPathTo
		} else {
			m.pathFocus = focusPathFrom
		}
	}
	m.syncFocus()
	return m.onTabActivate()
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
	case "5":
		return int(tabPath)
	}
	return -1
}

// syncFocus focuses the active tab's input and blurs the others.
func (m *Model) syncFocus() {
	m.search.Blur()
	m.impact.Blur()
	m.pathFromInput.Blur()
	m.pathToInput.Blur()
	switch m.active {
	case tabSearch:
		m.search.Focus()
	case tabImpact:
		m.impact.Focus()
	case tabPath:
		switch m.pathFocus {
		case focusPathFrom:
			m.pathFromInput.Focus()
		case focusPathTo:
			m.pathToInput.Focus()
		}
	}
}

func (m *Model) resizePathInputs() {
	w := m.width - 11 // label + indent; stacked fields use the remaining width
	if w < 1 {
		w = 1
	}
	if w > 100 {
		w = 100
	}
	m.pathFromInput.SetWidth(w)
	m.pathToInput.SetWidth(w)
}
