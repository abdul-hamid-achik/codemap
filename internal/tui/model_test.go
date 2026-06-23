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

func TestSearchSelect(t *testing.T) {
	m := sized(t, 120, 40)
	m.active = tabSearch
	hits := make([]app.SemanticHit, 30)
	for i := range hits {
		hits[i] = app.SemanticHit{Symbol: fmt.Sprintf("S%d", i)}
	}
	m, _ = applyMsg(m, semanticMsg{query: "x", hits: hits})
	if m.searchSel != 0 {
		t.Fatalf("selection after results = %d, want 0", m.searchSel)
	}
	u, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	if u.(Model).searchSel != 1 {
		t.Errorf("after down: sel=%d, want 1", u.(Model).searchSel)
	}
	u2, _ := u.(Model).Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	if u2.(Model).searchSel != 0 {
		t.Errorf("after up: sel=%d, want 0", u2.(Model).searchSel)
	}
}

func TestSearchDrillToImpact(t *testing.T) {
	m := sized(t, 120, 40)
	m.active = tabSearch
	m.search.SetValue("auth")
	m, _ = applyMsg(m, semanticMsg{query: "auth", hits: []app.SemanticHit{{Symbol: "A"}, {Symbol: "B"}}})
	m, _ = applyMsg(m, tea.KeyPressMsg(tea.Key{Code: tea.KeyDown})) // select B
	// query unchanged → enter drills the selected hit into Impact
	u, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	mm := u.(Model)
	if mm.active != tabImpact {
		t.Errorf("enter on unchanged query: active=%v, want Impact", mm.active)
	}
	if mm.impact.Value() != "B" {
		t.Errorf("drilled symbol = %q, want B", mm.impact.Value())
	}
	if cmd == nil {
		t.Error("drill should fire an impact command")
	}
}

func TestSearchEnterRunsWhenQueryChanged(t *testing.T) {
	m := sized(t, 120, 40)
	m.active = tabSearch
	m, _ = applyMsg(m, semanticMsg{query: "old", hits: []app.SemanticHit{{Symbol: "A"}}})
	m.search.SetValue("new") // edited query
	u, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if u.(Model).active != tabSearch {
		t.Error("editing the query then enter should run a search, not drill")
	}
	if cmd == nil {
		t.Error("enter on a changed query should fire a search")
	}
}

func TestImpactDrillIntoBlastNode(t *testing.T) {
	m := sized(t, 120, 40)
	m.active = tabImpact
	m.impact.SetValue("Foo")
	rep := &app.ImpactReport{
		Symbol: "Foo", Found: true,
		BlastRadius: []app.ImpactNode{{Symbol: "A", Depth: 1}, {Symbol: "B", Depth: 1}},
	}
	m, _ = applyMsg(m, impactMsg{symbol: "Foo", rep: rep})
	m, _ = applyMsg(m, tea.KeyPressMsg(tea.Key{Code: tea.KeyDown})) // select B
	// value unchanged ("Foo") → enter drills the selected blast node
	u, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	mm := u.(Model)
	if mm.impact.Value() != "B" {
		t.Errorf("drilled symbol = %q, want B", mm.impact.Value())
	}
	if cmd == nil {
		t.Error("drill should fire an impact command")
	}
}

func TestGraphPreciseToggle(t *testing.T) {
	m := sized(t, 120, 40)
	m, _ = applyMsg(m, graphHubsMsg{hubs: []app.HotspotRef{{Symbol: "Close", FQN: "graph.Store.Close", File: "internal/graph/store.go", StartLine: 95, InDegree: 45}}})
	// p requests a precise recompute for the selected hub.
	u, cmd := m.Update(tea.KeyPressMsg(tea.Key{Text: "p", Code: 'p'}))
	if cmd == nil {
		t.Error("p should fire a precise-detail command")
	}
	if !strings.Contains(u.(Model).statusMsg, "precise") {
		t.Errorf("status = %q, want a 'resolving precise' note", u.(Model).statusMsg)
	}
	// precise results mark the detail as precise.
	u2, _ := u.(Model).Update(preciseDetailMsg{symbol: "Close", callers: []app.SymbolRef{{Symbol: "X"}}})
	mm := u2.(Model)
	if !mm.graphPrecise || len(mm.graphCallers) != 1 {
		t.Errorf("precise detail not applied: precise=%v callers=%d", mm.graphPrecise, len(mm.graphCallers))
	}
	// navigating away reverts to fast by-name detail.
	u3, _ := mm.Update(graphDetailMsg{symbol: "Close", callers: []app.SymbolRef{{Symbol: "X"}, {Symbol: "Y"}}})
	if u3.(Model).graphPrecise {
		t.Error("graphPrecise should reset after a by-name detail update")
	}
}

func TestReindexKey(t *testing.T) {
	m := sized(t, 120, 40)
	m.graphLoaded = true
	u, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: 'r', Mod: tea.ModCtrl}))
	if u.(Model).graphLoaded {
		t.Error("ctrl+r should reset graphLoaded to force a refresh")
	}
	if cmd == nil {
		t.Error("ctrl+r should fire a reindex command")
	}
}

func TestIndexedMsgRefreshes(t *testing.T) {
	m := sized(t, 120, 40)
	u, cmd := m.Update(indexedMsg{rep: &app.IndexReport{FilesIndexed: 3, Nodes: 10, Edges: 7}})
	if cmd == nil {
		t.Error("indexedMsg should trigger a status+hubs refresh")
	}
	if !strings.Contains(u.(Model).statusMsg, "reindexed") {
		t.Errorf("status = %q, want a 'reindexed' summary", u.(Model).statusMsg)
	}
}

func TestGraphWalkRecenterAndBack(t *testing.T) {
	m := sized(t, 120, 40)
	m, _ = applyMsg(m, graphHubsMsg{hubs: []app.HotspotRef{
		{Symbol: "Close", FQN: "graph.Store.Close", File: "store.go", StartLine: 95, InDegree: 38},
	}})
	m, _ = applyMsg(m, graphDetailMsg{symbol: "Close",
		callers: []app.SymbolRef{{Symbol: "indexFile", FQN: "index.indexFile", File: "indexer.go", StartLine: 12}},
		callees: []app.SymbolRef{{Symbol: "WipeProject", FQN: "graph.Store.WipeProject", File: "store.go", StartLine: 40}},
	})
	if m.graphCenter.sym != "Close" {
		t.Fatalf("initial center = %q, want Close", m.graphCenter.sym)
	}

	// → moves focus into the refs pane (the caller list).
	m, _ = applyMsg(m, tea.KeyPressMsg(tea.Key{Code: tea.KeyRight}))
	if m.graphFocus != focusRefs {
		t.Fatalf("after right: focus = %v, want refs", m.graphFocus)
	}
	// enter re-centers on the selected caller (refs[0] = first caller).
	u, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	mm := u.(Model)
	if mm.graphCenter.sym != "indexFile" {
		t.Errorf("after re-center: center = %q, want indexFile", mm.graphCenter.sym)
	}
	if len(mm.graphStack) != 1 || mm.graphStack[0].sym != "Close" {
		t.Errorf("breadcrumb not pushed: %+v", mm.graphStack)
	}
	if cmd == nil {
		t.Error("re-center should fetch the new center's relations")
	}

	// backspace pops back to the previous center.
	u2, cmd2 := mm.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace}))
	mm2 := u2.(Model)
	if mm2.graphCenter.sym != "Close" {
		t.Errorf("after back: center = %q, want Close", mm2.graphCenter.sym)
	}
	if len(mm2.graphStack) != 0 {
		t.Errorf("breadcrumb should be empty after back: %+v", mm2.graphStack)
	}
	if cmd2 == nil {
		t.Error("back should re-fetch the previous center's relations")
	}
}

func TestGraphWalkSecondCalleeIndex(t *testing.T) {
	// graphRefSel spans callers then callees: index len(callers) selects the
	// first callee. Verify re-centering picks the right node across the boundary.
	m := sized(t, 120, 40)
	m, _ = applyMsg(m, graphHubsMsg{hubs: []app.HotspotRef{{Symbol: "A", InDegree: 3}}})
	m, _ = applyMsg(m, graphDetailMsg{symbol: "A",
		callers: []app.SymbolRef{{Symbol: "C1"}},
		callees: []app.SymbolRef{{Symbol: "E1"}, {Symbol: "E2"}},
	})
	m, _ = applyMsg(m, tea.KeyPressMsg(tea.Key{Code: tea.KeyRight})) // focus refs, sel 0 (C1)
	m, _ = applyMsg(m, tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))  // sel 1 → E1 (first callee)
	u, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if got := u.(Model).graphCenter.sym; got != "E1" {
		t.Errorf("re-centered on %q, want E1 (first callee, across the caller/callee boundary)", got)
	}
}

func TestGraphEnterStillDrillsFromHubFocus(t *testing.T) {
	// Walking must not break the existing enter→Impact behavior on the hub pane.
	m := sized(t, 120, 40)
	m, _ = applyMsg(m, graphHubsMsg{hubs: []app.HotspotRef{{Symbol: "Close", InDegree: 9}}})
	u, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if u.(Model).active != tabImpact {
		t.Errorf("enter on hub focus: active=%v, want Impact", u.(Model).active)
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
