// Package tui is codemap's interactive terminal UI ("studio"), built on Charm
// v2 (charm.land/bubbletea, lipgloss, bubbles). It is a thin view over
// internal/app: a tabbed explorer with Graph, Metrics, Impact, and Search.
package tui

import (
	"context"
	"fmt"

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

	// search tab
	search       textinput.Model
	searchHits   []app.SemanticHit
	searchQuery  string
	searchOffset int

	// impact tab
	impact       textinput.Model
	impactRep    *app.ImpactReport
	impactSymbol string
	impactOffset int

	// graph tab (call-graph explorer)
	graphLoaded  bool
	graphHubs    []app.HotspotRef
	graphSel     int
	graphSym     string
	graphCallers []app.SymbolRef
	graphCallees []app.SymbolRef
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

// Init loads project status and the call graph.
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.statusCmd(), m.hubsCmd())
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
		r, err := svc.Semantic(ctx, dir, q, 50)
		if err != nil {
			return semanticMsg{query: q, err: err}
		}
		return semanticMsg{query: q, hits: r.Hits}
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
		if len(msg.hubs) > 0 {
			return m, m.detailCmd(msg.hubs[0].Symbol)
		}
		return m, nil

	case graphDetailMsg:
		if msg.err != nil {
			m.errMsg = msg.err.Error()
			return m, nil
		}
		m.graphSym = msg.symbol
		m.graphCallers = msg.callers
		m.graphCallees = msg.callees
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
		return m, tea.Batch(m.statusCmd(), m.hubsCmd())

	case semanticMsg:
		if msg.err != nil {
			m.errMsg = msg.err.Error()
			m.searchHits = nil
			return m, nil
		}
		m.errMsg = ""
		m.searchHits = msg.hits
		m.searchQuery = msg.query
		m.searchOffset = 0
		m.statusMsg = fmt.Sprintf("%d matches for %q", len(msg.hits), msg.query)
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
		m.impactOffset = 0
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
	switch key {
	case "ctrl+c":
		return m, tea.Quit
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
			m.statusMsg = "searching…"
			m.searchOffset = 0
			return m, m.semanticCmd(q)
		case "up": // single-line input ignores up/down, so use them to scroll results
			if m.searchOffset > 0 {
				m.searchOffset--
			}
			return m, nil
		case "down":
			if m.searchOffset < len(m.searchHits)-1 {
				m.searchOffset++
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
			m.statusMsg = "analyzing…"
			m.impactOffset = 0
			return m, m.impactCmd(s)
		case "up":
			if m.impactOffset > 0 {
				m.impactOffset--
			}
			return m, nil
		case "down":
			if m.impactRep != nil && m.impactOffset < len(m.impactRep.BlastRadius)-1 {
				m.impactOffset++
			}
			return m, nil
		}
		m.impact, cmd = m.impact.Update(msg)
		return m, cmd

	case tabGraph:
		switch key {
		case "up", "k":
			if m.graphSel > 0 {
				m.graphSel--
				return m, m.detailCmd(m.graphHubs[m.graphSel].Symbol)
			}
		case "down", "j":
			if m.graphSel < len(m.graphHubs)-1 {
				m.graphSel++
				return m, m.detailCmd(m.graphHubs[m.graphSel].Symbol)
			}
		case "enter":
			// drill into the selected hub's full impact analysis
			if len(m.graphHubs) > 0 {
				sym := m.graphHubs[m.graphSel].Symbol
				m.active = tabImpact
				m.impact.SetValue(sym)
				m.syncFocus()
				m.impactOffset = 0
				m.statusMsg = "analyzing…"
				return m, m.impactCmd(sym)
			}
		case "q":
			return m, tea.Quit
		}
		return m, nil

	default: // metrics
		if key == "q" {
			return m, tea.Quit
		}
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
