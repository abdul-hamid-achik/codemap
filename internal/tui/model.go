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
type callersMsg struct {
	symbol string
	refs   []app.SymbolRef
	err    error
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

	search      textinput.Model
	searchHits  []app.SemanticHit
	searchQuery string

	impact       textinput.Model
	impactRefs   []app.SymbolRef
	impactSymbol string
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
		active:   tabMetrics,
		loading:  true,
		search:   s,
		impact:   i,
	}
}

// Init loads the project status asynchronously.
func (m Model) Init() tea.Cmd { return m.statusCmd() }

func (m Model) statusCmd() tea.Cmd {
	svc, dir := m.service, m.startDir
	return func() tea.Msg {
		st, err := svc.Status(dir)
		return statusMsg{st: st, err: err}
	}
}

func (m Model) semanticCmd(q string) tea.Cmd {
	ctx, svc, dir := m.ctx, m.service, m.startDir
	return func() tea.Msg {
		r, err := svc.Semantic(ctx, dir, q, 20)
		if err != nil {
			return semanticMsg{query: q, err: err}
		}
		return semanticMsg{query: q, hits: r.Hits}
	}
}

func (m Model) callersCmd(sym string) tea.Cmd {
	svc, dir := m.service, m.startDir
	return func() tea.Msg {
		r, err := svc.Callers(dir, sym)
		if err != nil {
			return callersMsg{symbol: sym, err: err}
		}
		return callersMsg{symbol: sym, refs: r.Results}
	}
}

// Update handles messages and key input.
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

	case semanticMsg:
		if msg.err != nil {
			m.errMsg = msg.err.Error()
			m.searchHits = nil
			return m, nil
		}
		m.errMsg = ""
		m.searchHits = msg.hits
		m.searchQuery = msg.query
		m.statusMsg = fmt.Sprintf("%d matches for %q", len(msg.hits), msg.query)
		return m, nil

	case callersMsg:
		if msg.err != nil {
			m.errMsg = msg.err.Error()
			m.impactRefs = nil
			return m, nil
		}
		m.errMsg = ""
		m.impactRefs = msg.refs
		m.impactSymbol = msg.symbol
		m.statusMsg = fmt.Sprintf("%d callers of %q", len(msg.refs), msg.symbol)
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "tab":
		m.active = (m.active + 1) % tabCount
		m.syncFocus()
		return m, nil
	case "shift+tab":
		m.active = (m.active + tabCount - 1) % tabCount
		m.syncFocus()
		return m, nil
	}

	var cmd tea.Cmd
	switch m.active {
	case tabSearch:
		if msg.String() == "enter" {
			q := m.search.Value()
			if q == "" {
				return m, nil
			}
			m.statusMsg = "searching…"
			return m, m.semanticCmd(q)
		}
		m.search, cmd = m.search.Update(msg)
		return m, cmd
	case tabImpact:
		if msg.String() == "enter" {
			s := m.impact.Value()
			if s == "" {
				return m, nil
			}
			return m, m.callersCmd(s)
		}
		m.impact, cmd = m.impact.Update(msg)
		return m, cmd
	default:
		if msg.String() == "q" {
			return m, tea.Quit
		}
	}
	return m, nil
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
