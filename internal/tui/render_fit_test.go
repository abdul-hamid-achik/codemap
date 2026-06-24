package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/abdul-hamid-achik/codemap/internal/app"
)

// seedTabs populates every tab with realistic data (long fully-qualified names,
// multiple hubs/callers/blast nodes) so the layout is exercised the way a real
// project renders, not just the empty state.
func seedTabs(m Model) Model {
	m, _ = applyMsg(m, statusMsg{st: &app.StatusReport{
		Project: "codemap", Registered: true, Nodes: 752, Edges: 3519, Files: 44,
		Kinds:     map[string]int{"method": 232, "test": 181, "function": 185, "type": 110, "file": 44},
		Languages: map[string]int{"go": 752},
	}})
	m, _ = applyMsg(m, graphHubsMsg{hubs: []app.HotspotRef{
		{Symbol: "Close", FQN: "internal/extract/lspsrc.Extractor.Close", InDegree: 57, File: "internal/extract/lspsrc/lspsrc.go", StartLine: 90},
		{Symbol: "Close", FQN: "internal/app.Session.Close", InDegree: 56, File: "internal/app/session.go", StartLine: 80},
		{Symbol: "NewService", FQN: "internal/app.NewService", InDegree: 26, File: "internal/app/service.go", StartLine: 41},
	}})
	m, _ = applyMsg(m, graphDetailMsg{
		symbol:  "internal/extract/lspsrc.Extractor.Close",
		callers: []app.SymbolRef{{Symbol: "runIndex", FQN: "cmd/codemap/main.runIndexProjectWithLongName", File: "cmd/codemap/main.go", StartLine: 209}},
		callees: []app.SymbolRef{{Symbol: "Close", FQN: "internal/app.Session.Close", File: "internal/app/session.go", StartLine: 80}},
	})
	m, _ = applyMsg(m, orphansMsg{orphans: []app.SymbolRef{
		{Symbol: "deadFn", FQN: "internal/extract/lspsrc.someVeryLongDeadFunctionName", File: "internal/extract/lspsrc/lspsrc.go", StartLine: 300},
	}})
	m, _ = applyMsg(m, impactMsg{symbol: "BlastRadius", rep: &app.ImpactReport{
		Symbol: "BlastRadius", Project: "codemap", Found: true,
		Locations:     []app.SymbolRef{{File: "internal/graph/queries.go", StartLine: 140}},
		DirectCallers: []app.SymbolRef{{Symbol: "Impact", FQN: "internal/app.Service.Impact"}},
		BlastRadius: []app.ImpactNode{
			{Symbol: "Impact", FQN: "internal/app.Service.Impact", File: "internal/app/service.go", StartLine: 898, Depth: 1},
			{Symbol: "handleImpact", FQN: "internal/mcp.Server.handleImpactWithAReallyLongName", File: "internal/mcp/server.go", StartLine: 303, Depth: 2, Kind: "method"},
		},
		Tests: []app.ImpactNode{{Symbol: "TestBlastRadius", FQN: "internal/graph.TestBlastRadius", File: "internal/graph/graph_test.go", StartLine: 305, Kind: "test"}},
	}})
	m, _ = applyMsg(m, semanticMsg{query: "blast", mode: "name", hits: []app.SemanticHit{
		{Symbol: "BlastRadius", FQN: "internal/graph.Store.BlastRadiusForProject", File: "internal/graph/queries.go", StartLine: 140, Score: 0.82,
			Signature: "func (s *Store) BlastRadiusForProject(id int64, sym string, depth int) ([]Node, error)"},
	}})
	return m
}

// TestRenderFitsAllWidths guards "uses the whole terminal" at any size: every
// tab must fill exactly height lines and never emit a line wider than the
// terminal. A single over-wide line makes lipgloss.JoinVertical pad every other
// line to match, which blew the frame past the screen at ≤80 cols before the
// MaxWidth clamp + responsive footer (regression guard).
func TestRenderFitsAllWidths(t *testing.T) {
	sizes := [][2]int{{120, 40}, {100, 30}, {80, 24}, {72, 22}, {60, 20}, {40, 16}}
	tabs := []struct {
		name string
		t    tab
	}{{"Graph", tabGraph}, {"Metrics", tabMetrics}, {"Impact", tabImpact}, {"Search", tabSearch}}
	for _, sz := range sizes {
		w, h := sz[0], sz[1]
		for _, tb := range tabs {
			m := sized(t, w, h)
			m = seedTabs(m)
			m.active = tb.t
			out := m.render()
			if got := lipgloss.Height(out); got != h {
				t.Errorf("%s at %dx%d: height = %d, want %d (should fill the screen)", tb.name, w, h, got, h)
			}
			for i, line := range strings.Split(out, "\n") {
				if lw := lipgloss.Width(line); lw > w {
					t.Errorf("%s at %dx%d: line %d width %d exceeds terminal width %d: %q",
						tb.name, w, h, i, lw, w, line)
				}
			}
		}
	}
}

// TestFooterCompactsWhenNarrow pins the responsive footer: the rich hint shows
// when it fits, and a compact form (still ending in "? help") takes over when it
// would overflow — so key discoverability survives at 80 cols.
func TestFooterCompactsWhenNarrow(t *testing.T) {
	wide := seedTabs(sized(t, 120, 40))
	wide.active = tabMetrics
	if !strings.Contains(wide.footer(), "ctrl+r reindex") || !strings.Contains(wide.footer(), "ctrl+c quit") {
		t.Errorf("wide Metrics footer should show the rich hint:\n%s", wide.footer())
	}
	for _, tb := range []tab{tabGraph, tabMetrics, tabImpact, tabSearch} {
		m := seedTabs(sized(t, 80, 24))
		m.active = tb
		f := m.footer()
		if lipgloss.Width(f) > 80 {
			t.Errorf("%v footer at 80 cols is %d wide (should fit): %q", tb, lipgloss.Width(f), f)
		}
		if !strings.Contains(f, "? help") {
			t.Errorf("%v footer at 80 cols dropped '? help' (discoverability): %q", tb, f)
		}
	}
}
