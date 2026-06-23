package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/abdul-hamid-achik/codemap/internal/app"
	"github.com/abdul-hamid-achik/codemap/internal/config"
)

func testModel() Model {
	sess := &app.Session{Config: config.DefaultConfig()}
	return NewModel(context.Background(), sess, "")
}

func sized(t *testing.T, w, h int) Model {
	t.Helper()
	m := testModel()
	u, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return u.(Model)
}

func TestNewModelDefaults(t *testing.T) {
	m := testModel()
	if m.active != tabGraph {
		t.Errorf("active = %v, want Graph", m.active)
	}
	if !m.loading {
		t.Error("model should start loading")
	}
}

func TestTabCycling(t *testing.T) {
	m := testModel()
	u, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	if got := u.(Model).active; got != tabMetrics {
		t.Errorf("after tab: %v, want Metrics", got)
	}
	u, _ = u.(Model).Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}))
	if got := u.(Model).active; got != tabGraph {
		t.Errorf("after shift+tab: %v, want Graph", got)
	}
}

func TestDigitTabSwitch(t *testing.T) {
	m := testModel() // on Graph (non-input), digits switch
	u, _ := m.Update(tea.KeyPressMsg(tea.Key{Text: "4", Code: '4'}))
	if got := u.(Model).active; got != tabSearch {
		t.Errorf("after '4': %v, want Search", got)
	}
	// On an input tab, digits should type, not switch.
	u2, _ := u.(Model).Update(tea.KeyPressMsg(tea.Key{Text: "2", Code: '2'}))
	if got := u2.(Model).active; got != tabSearch {
		t.Errorf("digit on input tab switched tabs: %v", got)
	}
	if v := u2.(Model).search.Value(); v != "2" {
		t.Errorf("digit should type into search input, got %q", v)
	}
}

func TestQuitKeys(t *testing.T) {
	m := testModel()
	m.active = tabMetrics
	if _, cmd := m.Update(tea.KeyPressMsg(tea.Key{Text: "q", Code: 'q'})); cmd == nil {
		t.Error("q should quit on a non-input tab")
	}
	if _, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl})); cmd == nil {
		t.Error("ctrl+c should always quit")
	}
}

func TestGraphNavigation(t *testing.T) {
	m := sized(t, 120, 40)
	u, _ := m.Update(graphHubsMsg{hubs: []app.HotspotRef{
		{Symbol: "A", InDegree: 9}, {Symbol: "B", InDegree: 5}, {Symbol: "C", InDegree: 2},
	}})
	mm := u.(Model)
	if !mm.graphLoaded || mm.graphSel != 0 {
		t.Fatalf("after hubs: loaded=%v sel=%d", mm.graphLoaded, mm.graphSel)
	}
	// down selects the next hub and asks for its detail.
	u2, cmd := mm.Update(tea.KeyPressMsg(tea.Key{Text: "j", Code: 'j'}))
	if got := u2.(Model).graphSel; got != 1 {
		t.Errorf("after j: sel=%d, want 1", got)
	}
	if cmd == nil {
		t.Error("moving selection should request detail")
	}
	// detail message populates callers/callees.
	u3, _ := u2.(Model).Update(graphDetailMsg{symbol: "B", callers: []app.SymbolRef{{Symbol: "X"}}})
	if mm3 := u3.(Model); mm3.graphSym != "B" || len(mm3.graphCallers) != 1 {
		t.Errorf("detail not applied: %+v", mm3.graphCallers)
	}
}

func TestRenderFillsScreen(t *testing.T) {
	m := sized(t, 120, 40)
	m, _ = applyMsg(m, statusMsg{st: &app.StatusReport{
		Project: "demo", Registered: true, Nodes: 411, Edges: 1414, Files: 35,
		Kinds:     map[string]int{"method": 133, "function": 107, "type": 70},
		Languages: map[string]int{"go": 411},
	}})
	m, _ = applyMsg(m, graphHubsMsg{hubs: []app.HotspotRef{{Symbol: "Close", FQN: "graph.Store.Close", InDegree: 38, File: "x.go"}}})

	out := m.render()
	if got := lipgloss.Height(out); got != 40 {
		t.Errorf("render height = %d, want 40 (should fill the screen)", got)
	}
	if !strings.Contains(out, "graph.Store.Close") {
		t.Error("hub list should show the FQN to disambiguate same-named symbols")
	}
	for i, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w > 120 {
			t.Errorf("line %d width %d exceeds screen width 120: %q", i, w, line)
		}
	}
	if !strings.Contains(out, "codemap studio") {
		t.Error("missing title")
	}
}

func TestMetricsRendersBars(t *testing.T) {
	m := sized(t, 120, 40)
	m.active = tabMetrics
	m, _ = applyMsg(m, statusMsg{st: &app.StatusReport{
		Project: "demo", Registered: true, Nodes: 5, Edges: 3,
		Kinds: map[string]int{"function": 2, "type": 1}, Languages: map[string]int{"go": 5},
	}})
	out := m.render()
	if !strings.Contains(out, "5 nodes") {
		t.Errorf("metrics missing node count:\n%s", out)
	}
	if !strings.Contains(out, "function") || !strings.Contains(out, "█") {
		t.Error("metrics missing bar chart")
	}
}

func TestGraphEnterDrillsToImpact(t *testing.T) {
	m := sized(t, 120, 40)
	m, _ = applyMsg(m, graphHubsMsg{hubs: []app.HotspotRef{{Symbol: "Close", InDegree: 38}}})
	u, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	mm := u.(Model)
	if mm.active != tabImpact {
		t.Errorf("enter on hub: active=%v, want Impact", mm.active)
	}
	if mm.impact.Value() != "Close" {
		t.Errorf("impact input = %q, want Close", mm.impact.Value())
	}
	if cmd == nil {
		t.Error("drill-down should fire an impact command")
	}
}

func TestSearchScroll(t *testing.T) {
	m := sized(t, 120, 40)
	m.active = tabSearch
	hits := make([]app.SemanticHit, 30)
	for i := range hits {
		hits[i] = app.SemanticHit{Symbol: fmt.Sprintf("S%d", i)}
	}
	m, _ = applyMsg(m, semanticMsg{query: "x", hits: hits})
	if m.searchOffset != 0 {
		t.Fatalf("offset after results = %d, want 0", m.searchOffset)
	}
	u, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	if u.(Model).searchOffset != 1 {
		t.Errorf("after down: offset=%d, want 1", u.(Model).searchOffset)
	}
	u2, _ := u.(Model).Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	if u2.(Model).searchOffset != 0 {
		t.Errorf("after up: offset=%d, want 0", u2.(Model).searchOffset)
	}
}

func TestSemanticErrorShown(t *testing.T) {
	m := sized(t, 100, 30)
	u, _ := m.Update(semanticMsg{query: "x", err: fakeErr("ollama unreachable")})
	if u.(Model).errMsg == "" {
		t.Error("semantic error should be surfaced")
	}
}

func applyMsg(m Model, msg tea.Msg) (Model, tea.Cmd) {
	u, cmd := m.Update(msg)
	return u.(Model), cmd
}

type fakeErr string

func (e fakeErr) Error() string { return string(e) }
