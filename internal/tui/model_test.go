package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/abdul-hamid-achik/codemap/internal/app"
	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/daemon"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
	"github.com/abdul-hamid-achik/codemap/internal/index"
)

func testModel() Model {
	sess := &app.Session{Config: config.DefaultConfig()}
	return NewModel(context.Background(), sess, "")
}

// studioSelectorFixture builds two same-named methods with exact edges. Only
// Left.Shared's file has precise-coverage evidence; the project still contains
// precise edges for both sides, which lets tests prove selected-node badges do
// not inherit project-wide precision.
func studioSelectorFixture(t *testing.T) (Model, graphCenter, graphCenter) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("CODEMAP_DATA", filepath.Join(home, "data"))
	t.Setenv("CODEMAP_CONFIG", "")
	t.Setenv("XDG_DATA_HOME", "")

	proj := t.TempDir()
	files := map[string]string{
		"left.go":  "package app\n\ntype Left struct{}\nfunc (Left) Shared() { leftOnly() }\nfunc leftOnly() {}\nfunc CallLeft() { Left{}.Shared() }\n",
		"right.go": "package app\n\ntype Right struct{}\nfunc (Right) Shared() { rightOnly() }\nfunc rightOnly() {}\nfunc CallRight() { Right{}.Shared() }\n",
	}
	for name, source := range files {
		if err := os.WriteFile(filepath.Join(proj, name), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	sess, err := app.Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	sess.Config.Vecgrep.Enabled = false
	g, err := sess.Graph()
	if err != nil {
		t.Fatal(err)
	}
	pid, err := g.UpsertProject(config.DeriveProjectName(proj), proj, "go")
	if err != nil {
		t.Fatal(err)
	}
	add := func(file, symbol, fqn, kind string, line int, signature string) int64 {
		t.Helper()
		id, addErr := g.AddNode(&graph.Node{
			ProjectID: pid, FilePath: file, Symbol: symbol, FQN: fqn, Kind: kind,
			Language: "go", StartLine: line, EndLine: line, Signature: signature, SourceHash: fqn,
		})
		if addErr != nil {
			t.Fatal(addErr)
		}
		return id
	}
	leftID := add("left.go", "Shared", "app.Left.Shared", graph.KindMethod, 4, "func (Left) Shared()")
	rightID := add("right.go", "Shared", "app.Right.Shared", graph.KindMethod, 4, "func (Right) Shared()")
	leftOnlyID := add("left.go", "leftOnly", "app.leftOnly", graph.KindFunction, 5, "func leftOnly()")
	rightOnlyID := add("right.go", "rightOnly", "app.rightOnly", graph.KindFunction, 5, "func rightOnly()")
	callLeftID := add("left.go", "CallLeft", "app.CallLeft", graph.KindFunction, 6, "func CallLeft()")
	callRightID := add("right.go", "CallRight", "app.CallRight", graph.KindFunction, 6, "func CallRight()")
	for _, edge := range [][2]int64{
		{callLeftID, leftID}, {callRightID, rightID},
		{leftID, leftOnlyID}, {rightID, rightOnlyID},
	} {
		if _, err := g.AddEdgeProv(edge[0], edge[1], graph.EdgeCalls, 1, graph.ProvPrecise); err != nil {
			t.Fatal(err)
		}
	}
	if err := g.MarkCallGraphResolved(pid, "left.go", "test"); err != nil {
		t.Fatal(err)
	}

	m := NewModel(context.Background(), sess, proj)
	m.width, m.height = 120, 40
	m.status = &app.StatusReport{Registered: true, PreciseEdges: 4}
	left := graphCenter{sym: "Shared", fqn: "app.Left.Shared", kind: graph.KindMethod, file: "left.go", line: 4}
	right := graphCenter{sym: "Shared", fqn: "app.Right.Shared", kind: graph.KindMethod, file: "right.go", line: 4}
	return m, left, right
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

func TestPathTabKeepsDigitsInEndpointInput(t *testing.T) {
	m := testModel()
	m.graphCenter = graphCenter{} // no source to auto-seed
	m.graphSym = ""
	m, _ = applyMsg(m, tea.KeyPressMsg(tea.Key{Code: '5', Mod: tea.ModAlt}))
	if m.active != tabPath || m.pathFocus != focusPathFrom {
		t.Fatalf("alt+5 should open Path at FROM, got tab=%v focus=%v", m.active, m.pathFocus)
	}
	m, _ = applyMsg(m, tea.KeyPressMsg(tea.Key{Text: "2", Code: '2'}))
	if m.active != tabPath || m.pathFromInput.Value() != "2" {
		t.Fatalf("bare digit on Path should type, got tab=%v FROM=%q", m.active, m.pathFromInput.Value())
	}
	m.pathFocus = focusPathResult
	m.syncFocus()
	m, _ = applyMsg(m, tea.KeyPressMsg(tea.Key{Text: "2", Code: '2'}))
	if m.active != tabMetrics {
		t.Fatalf("bare digit with Path result focused should switch tabs, got %v", m.active)
	}
}

// TestAltDigitTabSwitch: on an input tab a BARE digit types into the query, but
// alt+digit still switches tabs (and leaves the typed query intact).
func TestAltDigitTabSwitch(t *testing.T) {
	m := testModel()
	m, _ = applyMsg(m, tea.KeyPressMsg(tea.Key{Text: "4", Code: '4'})) // Graph: 4 → Search
	if m.active != tabSearch {
		t.Fatalf("setup: want Search, got %v", m.active)
	}
	m, _ = applyMsg(m, tea.KeyPressMsg(tea.Key{Text: "2", Code: '2'})) // bare digit types
	if m.search.Value() != "2" {
		t.Fatalf("setup: bare digit should type into search, got %q", m.search.Value())
	}
	// alt+3 switches to Impact from the input tab (real alt+digit has no Text).
	m, _ = applyMsg(m, tea.KeyPressMsg(tea.Key{Code: '3', Mod: tea.ModAlt}))
	if m.active != tabImpact {
		t.Errorf("alt+3 should switch to Impact from Search, got %v", m.active)
	}
	if m.search.Value() != "2" {
		t.Errorf("alt+digit must not edit the input; search query now %q", m.search.Value())
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

func TestGraphHubListScrollIndicators(t *testing.T) {
	m := sized(t, 80, 12) // small height so the 30-hub list overflows the window
	hubs := make([]app.HotspotRef, 30)
	for i := range hubs {
		hubs[i] = app.HotspotRef{Symbol: fmt.Sprintf("H%d", i), InDegree: 30 - i}
	}
	m, _ = applyMsg(m, graphHubsMsg{hubs: hubs})
	out := m.render()
	if !strings.Contains(out, "Hubs (30)") {
		t.Errorf("hub list title should show the total count:\n%s", out)
	}
	// graphSel starts at 0 (top): a ▼ more-below indicator, no ▲ more-above.
	if !strings.Contains(out, "▼") || !strings.Contains(out, "more") {
		t.Errorf("an overflowing hub list should show a ▼ 'N more' indicator at the top:\n%s", out)
	}
	if strings.Contains(out, "▲") {
		t.Errorf("at the top there should be no ▲ more-above indicator:\n%s", out)
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

func TestSearchHeaderShowsResultCount(t *testing.T) {
	m := sized(t, 120, 40)
	m.active = tabSearch
	m, _ = applyMsg(m, semanticMsg{query: "x", mode: "name", hits: []app.SemanticHit{
		{Symbol: "A"}, {Symbol: "B"}, {Symbol: "C"},
	}})
	out := m.render()
	if !strings.Contains(out, "name mode") || !strings.Contains(out, "3 results") {
		t.Errorf("search header should show the mode and result count:\n%s", out)
	}
}

func TestPathTabSeedsRichSourceFromSelection(t *testing.T) {
	m := sized(t, 120, 40)
	m.graphSym = "Run"
	m.graphCenter = graphCenter{sym: "Run", fqn: "app.Run", kind: "function", file: "app.go", line: 12}
	m, _ = applyMsg(m, tea.KeyPressMsg(tea.Key{Code: '5', Mod: tea.ModAlt}))
	if m.active != tabPath || m.pathFocus != focusPathTo {
		t.Fatalf("Path should open with seeded FROM and TO focused, got tab=%v focus=%v", m.active, m.pathFocus)
	}
	if m.pathFromInput.Value() != "app.Run" || m.pathFrom.center.file != "app.go" || m.pathFrom.center.line != 12 {
		t.Fatalf("seeded endpoint lost selector identity: input=%q endpoint=%+v", m.pathFromInput.Value(), m.pathFrom)
	}
	selector, ok := m.pathFrom.center.selector()
	if !ok || selector.File != "app.go" || selector.StartLine != 12 || selector.FQN != "app.Run" || selector.Kind != "function" {
		t.Fatalf("center→selector projection = %+v ok=%v", selector, ok)
	}
}

func TestPathSelectorRoutingRequiresTwoRichEndpoints(t *testing.T) {
	from := pathEndpoint{query: "app.Run", center: graphCenter{sym: "Run", fqn: "app.Run", kind: "function", file: "a.go", line: 12}}
	to := pathEndpoint{query: "app.Helper", center: graphCenter{sym: "Helper", fqn: "app.Helper", kind: "function", file: "a.go", line: 3}}
	fromSelector, toSelector, exact := exactPathSelectors(from, to)
	if !exact || fromSelector.FQN != "app.Run" || fromSelector.Kind != "function" || toSelector.StartLine != 3 {
		t.Fatalf("rich endpoints should route to PathBySelectors: from=%+v to=%+v exact=%v", fromSelector, toSelector, exact)
	}
	typed := pathEndpointForInput("Helper", pathEndpoint{})
	if _, _, exact := exactPathSelectors(from, typed); exact {
		t.Fatal("a manually typed endpoint without file:line must retain the legacy/FQN fallback")
	}

	// A selector-aware disconnected answer has no Path nodes to upgrade from, so
	// the report selectors themselves must preserve exact endpoints for reruns.
	m := sized(t, 100, 28)
	m.active = tabPath
	rep := &app.PathReport{
		From: "Run", To: "Helper", CallGraph: app.CallGraphResolved,
		FromSelector: &app.SymbolSelector{File: "a.go", StartLine: 12, FQN: "app.Run", Kind: "function"},
		ToSelector:   &app.SymbolSelector{File: "a.go", StartLine: 3, FQN: "app.Helper", Kind: "function"},
	}
	m, _ = applyMsg(m, pathMsg{from: typed, to: typed, rep: rep})
	if _, _, exact := exactPathSelectors(m.pathFrom, m.pathTo); !exact {
		t.Fatal("disconnected selector report should retain exact endpoints for a rerun")
	}
}

func TestPathWorkflowRunsAsyncAndInspectsResult(t *testing.T) {
	m := sized(t, 120, 40)
	m.loading = false
	m.active = tabPath
	m.pathFocus = focusPathFrom
	m.syncFocus()
	m.pathFromInput.SetValue("app.Top")

	// Enter advances FROM → TO without blocking or querying.
	m, cmd := applyMsg(m, tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if cmd != nil || m.pathFocus != focusPathTo {
		t.Fatalf("FROM enter should focus TO only, cmd=%v focus=%v", cmd != nil, m.pathFocus)
	}
	m.pathToInput.SetValue("app.Helper")
	m, cmd = applyMsg(m, tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if cmd == nil || !m.pathLoading || m.statusMsg != "finding path…" {
		t.Fatalf("TO enter should start async lookup, cmd=%v loading=%v status=%q", cmd != nil, m.pathLoading, m.statusMsg)
	}

	from, to := m.pathFrom, m.pathTo
	rep := &app.PathReport{
		From: "Top", To: "Helper", Found: true, CallGraph: app.CallGraphName,
		Path: []app.SymbolRef{
			{Symbol: "Top", FQN: "app.Top", Kind: "function", File: "a.go", StartLine: 8},
			{Symbol: "Run", FQN: "app.Run", Kind: "function", File: "a.go", StartLine: 5},
			{Symbol: "Helper", FQN: "app.Helper", Kind: "function", File: "a.go", StartLine: 3},
		},
	}
	m, _ = applyMsg(m, pathMsg{from: from, to: to, rep: rep})
	if m.pathLoading || m.pathFocus != focusPathResult || m.pathSel != 0 {
		t.Fatalf("path result should focus first node, loading=%v focus=%v sel=%d", m.pathLoading, m.pathFocus, m.pathSel)
	}
	out := m.render()
	for _, want := range []string{"call_graph: name", "confidence: medium", "Top → Helper", "2 hops", "│ calls"} {
		if !strings.Contains(out, want) {
			t.Errorf("Path result missing %q:\n%s", want, out)
		}
	}

	// Arrow and vim navigation both inspect the ordered nodes, and selection is
	// available to global source/context/Graph actions as a full graphCenter.
	m, _ = applyMsg(m, tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	m, _ = applyMsg(m, tea.KeyPressMsg(tea.Key{Text: "j", Code: 'j'}))
	if m.pathSel != 2 {
		t.Fatalf("down+j pathSel=%d, want target node 2", m.pathSel)
	}
	c, ok := m.selectedCenter()
	if !ok || c.fqn != "app.Helper" || c.file != "a.go" || c.line != 3 {
		t.Fatalf("selected path node = %+v ok=%v", c, ok)
	}
	m, cmd = applyMsg(m, tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if m.active != tabGraph || m.graphCenter.fqn != "app.Helper" || cmd == nil {
		t.Fatalf("enter should open selected node in Graph, tab=%v center=%+v cmd=%v", m.active, m.graphCenter, cmd != nil)
	}
	// Browser back restores the complete Path task, including endpoints/result.
	m, _ = applyMsg(m, tea.KeyPressMsg(tea.Key{Code: tea.KeyLeft, Mod: tea.ModAlt}))
	if m.active != tabPath || m.pathRep == nil || m.pathSel != 2 || m.pathToInput.Value() != "app.Helper" {
		t.Fatalf("back should restore Path exactly: tab=%v sel=%d to=%q rep=%v", m.active, m.pathSel, m.pathToInput.Value(), m.pathRep != nil)
	}
}

func TestPathStatesStayDistinct(t *testing.T) {
	base := sized(t, 100, 28)
	base.active = tabPath
	base.pathFocus = focusPathFrom
	base.syncFocus()
	if out := base.render(); !strings.Contains(out, "Choose two endpoints") {
		t.Fatalf("initial Path state should instruct the user:\n%s", out)
	}

	from := pathEndpoint{query: "A", center: graphCenter{sym: "A"}}
	to := pathEndpoint{query: "B", center: graphCenter{sym: "B"}}
	for _, tc := range []struct {
		name string
		rep  *app.PathReport
		want []string
	}{
		{name: "resolved disconnected", rep: &app.PathReport{From: "A", To: "B", CallGraph: app.CallGraphResolved}, want: []string{"call_graph: resolved", "confidence: high", "No path found"}},
		{name: "unresolved", rep: &app.PathReport{From: "A", To: "B", CallGraph: app.CallGraphUnresolved, Resolution: "call graph not available for typescript"}, want: []string{"call_graph: unresolved", "confidence: low", "Path unknown", "typescript"}},
		{name: "missing endpoint", rep: &app.PathReport{From: "A", To: "Missing", CallGraph: app.CallGraphNone, Note: `"Missing" is not a symbol`}, want: []string{"call_graph: none", "Endpoint not found", "not a symbol"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := applyMsg(base, pathMsg{from: from, to: to, rep: tc.rep})
			out := m.render()
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("state missing %q:\n%s", want, out)
				}
			}
		})
	}

	loading := base
	loading.pathLoading = true
	if out := loading.render(); !strings.Contains(out, "finding shortest path") {
		t.Fatalf("loading state missing:\n%s", out)
	}
	failed, _ := applyMsg(base, pathMsg{from: from, to: to, err: fakeErr("graph unavailable")})
	if out := failed.render(); !strings.Contains(out, "Path lookup failed") || !strings.Contains(out, "graph unavailable") {
		t.Fatalf("error state missing:\n%s", out)
	}
}

// TestGlobalNavBackForward pins FIX.md §2: drilling Search→Impact records global
// history, alt+← returns to the exact Search view WITH the original query in the bar
// and the prior hit highlighted, alt+→ re-walks forward, esc also steps back, and a
// new drill clears the forward stack.
func TestGlobalNavBackForward(t *testing.T) {
	m := sized(t, 120, 40)
	m.active = tabSearch
	m.search.SetValue("auth")
	m, _ = applyMsg(m, semanticMsg{query: "auth", hits: []app.SemanticHit{{Symbol: "A"}, {Symbol: "B"}}})
	m, _ = applyMsg(m, tea.KeyPressMsg(tea.Key{Code: tea.KeyDown})) // select B (searchSel=1)
	m, _ = applyMsg(m, tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if m.active != tabImpact || m.impact.Value() != "B" {
		t.Fatalf("drill: active=%v impact=%q, want Impact/B", m.active, m.impact.Value())
	}
	if len(m.navHist) != 1 {
		t.Fatalf("navHist=%d after drill, want 1", len(m.navHist))
	}

	// alt+← → back to the exact Search view, bar text + selection restored.
	m, _ = applyMsg(m, tea.KeyPressMsg(tea.Key{Code: tea.KeyLeft, Mod: tea.ModAlt}))
	if m.active != tabSearch {
		t.Errorf("back: active=%v, want Search", m.active)
	}
	if m.search.Value() != "auth" {
		t.Errorf("back: search bar=%q, want %q (restored)", m.search.Value(), "auth")
	}
	if m.searchSel != 1 {
		t.Errorf("back: searchSel=%d, want 1 (B still highlighted)", m.searchSel)
	}
	if len(m.navFwd) != 1 {
		t.Errorf("navFwd=%d after back, want 1", len(m.navFwd))
	}

	// alt+→ → forward to the Impact view.
	m, _ = applyMsg(m, tea.KeyPressMsg(tea.Key{Code: tea.KeyRight, Mod: tea.ModAlt}))
	if m.active != tabImpact || m.impact.Value() != "B" {
		t.Errorf("forward: active=%v impact=%q, want Impact/B", m.active, m.impact.Value())
	}

	// esc also steps back.
	m, _ = applyMsg(m, tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	if m.active != tabSearch {
		t.Errorf("esc-back: active=%v, want Search", m.active)
	}

	// a new drill (pushNav) forks history → forward stack cleared.
	m.pushNav()
	if len(m.navFwd) != 0 {
		t.Errorf("a new drill should clear the forward stack, got %d", len(m.navFwd))
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

func TestGraphPreciseIndexAware(t *testing.T) {
	m := sized(t, 120, 40) // default tab is Graph
	m, _ = applyMsg(m, statusMsg{st: &app.StatusReport{Registered: true, PreciseEdges: 50}})
	m, _ = applyMsg(m, graphHubsMsg{hubs: []app.HotspotRef{{Symbol: "Close", FQN: "graph.Store.Close", File: "x.go", StartLine: 95, InDegree: 45}}})
	m, _ = applyMsg(m, graphDetailMsg{symbol: "Close", callers: []app.SymbolRef{{Symbol: "X"}}, callGraph: app.CallGraphResolved})

	// The selected relation report, rather than the project total, says these
	// relations are exact and safe to treat as high-confidence.
	for _, want := range []string{"call_graph: resolved", "confidence: high"} {
		if !strings.Contains(m.render(), want) {
			t.Errorf("graph detail should show selected-node evidence %q:\n%s", want, m.render())
		}
	}
	// Pressing p must NOT spawn the redundant gopls recompute; it informs instead.
	u, cmd := m.Update(tea.KeyPressMsg(tea.Key{Text: "p", Code: 'p'}))
	if cmd != nil {
		t.Error("p should not spawn gopls when the index is already precise")
	}
	if !strings.Contains(u.(Model).statusMsg, "already precise") {
		t.Errorf("p status = %q, want an 'already precise' note", u.(Model).statusMsg)
	}
}

func TestStudioExactSelectorKeepsSameNamedMethodsSeparate(t *testing.T) {
	m, left, _ := studioSelectorFixture(t)
	m.graphCenter = left

	detail, ok := m.detailCmd(left)().(graphDetailMsg)
	if !ok {
		t.Fatal("detail command returned the wrong message type")
	}
	if detail.err != nil {
		t.Fatal(detail.err)
	}
	if detail.callGraph != app.CallGraphResolved {
		t.Fatalf("Left.Shared call_graph = %q, want resolved", detail.callGraph)
	}
	if len(detail.callers) != 1 || detail.callers[0].Symbol != "CallLeft" {
		t.Fatalf("Left.Shared callers merged Right.Shared: %+v", detail.callers)
	}
	if len(detail.callees) != 1 || detail.callees[0].Symbol != "leftOnly" {
		t.Fatalf("Left.Shared callees merged Right.Shared: %+v", detail.callees)
	}

	contextMsg, ok := m.contextViewCmd(left)().(sourceMsg)
	if !ok {
		t.Fatal("context command returned the wrong message type")
	}
	if contextMsg.err != nil {
		t.Fatal(contextMsg.err)
	}
	contextBody := contextMsg.title + "\n" + strings.Join(contextMsg.lines, "\n")
	for _, want := range []string{"app.Left.Shared", "CallLeft", "leftOnly"} {
		if !strings.Contains(contextBody, want) {
			t.Errorf("Left.Shared context missing %q:\n%s", want, contextBody)
		}
	}
	for _, unwanted := range []string{"app.Right.Shared", "CallRight", "rightOnly"} {
		if strings.Contains(contextBody, unwanted) {
			t.Errorf("Left.Shared context merged %q from Right.Shared:\n%s", unwanted, contextBody)
		}
	}

	source, ok := m.sourceViewCmd(left)().(sourceMsg)
	if !ok {
		t.Fatal("source command returned the wrong message type")
	}
	if source.err != nil {
		t.Fatal(source.err)
	}
	sourceBody := strings.Join(source.lines, "\n")
	if !strings.Contains(sourceBody, "leftOnly") || strings.Contains(sourceBody, "rightOnly") {
		t.Fatalf("Left.Shared source was not exact:\n%s", sourceBody)
	}
}

func TestGraphBadgeUsesSelectedNodeCoverageInPartiallyPreciseProject(t *testing.T) {
	m, _, right := studioSelectorFixture(t)
	m.graphCenter = right

	detail, ok := m.detailCmd(right)().(graphDetailMsg)
	if !ok {
		t.Fatal("detail command returned the wrong message type")
	}
	if detail.err != nil {
		t.Fatal(detail.err)
	}
	if detail.callGraph != app.CallGraphName {
		t.Fatalf("uncovered Right.Shared call_graph = %q, want name", detail.callGraph)
	}
	m, _ = applyMsg(m, detail)
	out := m.hubDetail(100, 30)
	for _, want := range []string{"call_graph: name", "confidence: medium"} {
		if !strings.Contains(out, want) {
			t.Errorf("uncovered selected node should show %q despite project precise edges:\n%s", want, out)
		}
	}
	if strings.Contains(out, "confidence: high") {
		t.Errorf("uncovered selected node inherited project-wide precision:\n%s", out)
	}

	// A partially covered project must still allow a one-off precise lookup for
	// an uncovered selected file; the global PreciseEdges count cannot suppress p.
	u, cmd := m.Update(tea.KeyPressMsg(tea.Key{Text: "p", Code: 'p'}))
	if cmd == nil || !strings.Contains(u.(Model).statusMsg, "resolving precise") {
		t.Fatalf("p should resolve the uncovered selection, cmd=%v status=%q", cmd != nil, u.(Model).statusMsg)
	}

	// Resolution and note are relation evidence too, and must survive the async
	// message seam into the detail view instead of being silently discarded.
	m, _ = applyMsg(m, graphDetailMsg{
		symbol: "Shared", callGraph: app.CallGraphUnresolved,
		resolution: "typescript coverage missing", note: "selected file was skipped",
	})
	out = m.hubDetail(100, 30)
	for _, want := range []string{"call_graph: unresolved", "confidence: low", "typescript coverage missing", "selected file was skipped"} {
		if !strings.Contains(out, want) {
			t.Errorf("selected relation evidence missing %q:\n%s", want, out)
		}
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

// TestReindexPreservesPrecision verifies ctrl+r reindexes with --precise exactly
// when the project already has precise edges, so refreshing keeps the call graph
// (TS/JS/Python have no call graph without --precise) instead of dropping it.
func TestReindexPreservesPrecision(t *testing.T) {
	m := sized(t, 120, 40)
	if m.reindexPrecise() {
		t.Error("with no status, reindex should default to structure-only (not precise)")
	}
	m, _ = applyMsg(m, statusMsg{st: &app.StatusReport{Registered: true, Edges: 5}}) // name-based
	if m.reindexPrecise() {
		t.Error("a name-based project should reindex structure-only")
	}
	m, _ = applyMsg(m, statusMsg{st: &app.StatusReport{Registered: true, Edges: 5, PreciseEdges: 3}})
	if !m.reindexPrecise() {
		t.Error("a project with precise edges should reindex --precise to keep its call graph")
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

// TestIndexedMsgReportsSkipped verifies an in-studio reindex doesn't hide skipped
// files (e.g. a language server that timed out) behind a clean count.
func TestIndexedMsgReportsSkipped(t *testing.T) {
	m := sized(t, 120, 40)
	u, _ := m.Update(indexedMsg{rep: &app.IndexReport{FilesIndexed: 8, FilesSkipped: 2, Nodes: 10, Edges: 7}})
	if got := u.(Model).statusMsg; !strings.Contains(got, "8 files") || !strings.Contains(got, "2 skipped") {
		t.Errorf("reindex status should report skipped files, got %q", got)
	}
}

func TestIndexedMsgShowsWarning(t *testing.T) {
	m := sized(t, 120, 40)
	u, _ := m.Update(indexedMsg{rep: &app.IndexReport{Warning: "no Go files to index (codemap v0.1 indexes Go); skipped 2 typescript"}})
	if got := u.(Model).statusMsg; !strings.Contains(got, "no Go files") {
		t.Errorf("reindex warning should surface in studio, got %q", got)
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

func TestMetricsShowsPreciseState(t *testing.T) {
	base := app.StatusReport{Project: "d", Registered: true, Nodes: 5, Edges: 3,
		Kinds: map[string]int{"function": 2}, Languages: map[string]int{"go": 5}}

	m := sized(t, 120, 40)
	m.active = tabMetrics
	precise := base
	precise.PreciseEdges = 3
	m, _ = applyMsg(m, statusMsg{st: &precise})
	if !strings.Contains(m.render(), "3 precise") {
		t.Errorf("metrics should show precise edge count when PreciseEdges > 0:\n%s", m.render())
	}

	m2 := sized(t, 120, 40)
	m2.active = tabMetrics
	m2, _ = applyMsg(m2, statusMsg{st: &base}) // PreciseEdges == 0
	if strings.Contains(m2.render(), "precise") {
		t.Error("metrics should not claim precise edges on a name-based index")
	}
}

func TestMetricsShowsEmbeddingState(t *testing.T) {
	base := app.StatusReport{Project: "d", Registered: true, Nodes: 5, Edges: 3,
		Kinds: map[string]int{"function": 2}, Languages: map[string]int{"go": 5}}

	m := sized(t, 120, 40)
	m.active = tabMetrics
	embedded := base
	embedded.Vectors = 5
	m, _ = applyMsg(m, statusMsg{st: &embedded})
	if !strings.Contains(m.render(), "semantic search ready") {
		t.Error("metrics should show semantic-search-ready when vectors > 0")
	}

	m2 := sized(t, 120, 40)
	m2.active = tabMetrics
	m2, _ = applyMsg(m2, statusMsg{st: &base}) // Vectors == 0
	if !strings.Contains(m2.render(), "no embeddings") {
		t.Error("metrics should note no embeddings when vectors == 0")
	}
}

func TestMetricsDashboardShowsHubsAndDeadCode(t *testing.T) {
	m := sized(t, 120, 40)
	m.active = tabMetrics
	m, _ = applyMsg(m, statusMsg{st: &app.StatusReport{
		Project: "demo", Registered: true, Nodes: 5, Edges: 3,
		Kinds: map[string]int{"function": 2}, Languages: map[string]int{"go": 5},
	}})
	m, _ = applyMsg(m, graphHubsMsg{hubs: []app.HotspotRef{{Symbol: "Hub", FQN: "p.Hub", InDegree: 9}}})
	m, _ = applyMsg(m, orphansMsg{orphans: []app.SymbolRef{{Symbol: "Dead", FQN: "p.Dead", File: "d.go", StartLine: 3}}})

	out := m.render()
	for _, want := range []string{"Top hubs", "p.Hub", "Dead-code candidates", "p.Dead", "By kind"} {
		if !strings.Contains(out, want) {
			t.Errorf("metrics dashboard missing %q:\n%s", want, out)
		}
	}
	for i, line := range strings.Split(out, "\n") {
		if wd := lipgloss.Width(line); wd > 120 {
			t.Errorf("metrics line %d width %d exceeds 120: %q", i, wd, line)
		}
	}
}

func TestMetricsFlagsInflatedHubs(t *testing.T) {
	m := sized(t, 120, 40)
	m.active = tabMetrics
	m, _ = applyMsg(m, statusMsg{st: &app.StatusReport{
		Project: "demo", Registered: true, Nodes: 5, Edges: 3,
		Kinds: map[string]int{"method": 2}, Languages: map[string]int{"go": 5},
	}})
	m, _ = applyMsg(m, graphHubsMsg{hubs: []app.HotspotRef{
		{Symbol: "Close", FQN: "p.T.Close", InDegree: 71, SharedName: 6}, // inflated by name collision
		{Symbol: "Unique", FQN: "p.Unique", InDegree: 4},                 // genuine hub
	}})
	out := m.render()
	if !strings.Contains(out, "⚠×6") {
		t.Errorf("metrics should mark a name-inflated hub with ⚠×N:\n%s", out)
	}
	if !strings.Contains(out, "⚠=name-inflated") {
		t.Errorf("metrics should explain the ⚠ marker via a legend:\n%s", out)
	}
	// Width must still be respected with the marker present.
	for i, line := range strings.Split(out, "\n") {
		if wd := lipgloss.Width(line); wd > 120 {
			t.Errorf("metrics line %d width %d exceeds 120: %q", i, wd, line)
		}
	}
}

func TestMetricsNavigationDrills(t *testing.T) {
	m := sized(t, 120, 40)
	m.active = tabMetrics
	m, _ = applyMsg(m, statusMsg{st: &app.StatusReport{
		Project: "d", Registered: true, Nodes: 5, Edges: 3,
		Kinds: map[string]int{"function": 2}, Languages: map[string]int{"go": 5},
	}})
	m, _ = applyMsg(m, graphHubsMsg{hubs: []app.HotspotRef{{Symbol: "Hub", FQN: "p.Hub", InDegree: 9}}})
	m, _ = applyMsg(m, orphansMsg{orphans: []app.SymbolRef{{Symbol: "Dead", FQN: "p.Dead", File: "d.go", StartLine: 3}}})

	// selection starts at the first hub; down moves into the orphans list.
	m, _ = applyMsg(m, tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	if m.metricsSel != 1 {
		t.Fatalf("metricsSel = %d, want 1 (into orphans)", m.metricsSel)
	}
	if sym, file, line, ok := m.sourceTarget(); !ok || sym != "Dead" || file != "d.go" || line != 3 {
		t.Errorf("metrics sourceTarget = (%q,%q,%d,%v), want Dead/d.go/3/true", sym, file, line, ok)
	}
	// enter drills the selected dead-code candidate into Impact.
	u, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	mm := u.(Model)
	if mm.active != tabImpact || mm.impact.Value() != "Dead" {
		t.Errorf("enter on metrics: active=%v value=%q, want Impact/Dead", mm.active, mm.impact.Value())
	}
	if cmd == nil {
		t.Error("enter should fire an impact command")
	}
}

func TestImpactDepthHeatmap(t *testing.T) {
	// Each depth tier renders its [N] tag in a distinct heat color, hot→cool.
	if !strings.Contains(depthHeat(1), "[1]") || !strings.Contains(depthHeat(5), "[5]") {
		t.Error("depthHeat should tag the depth number")
	}
	if !strings.Contains(depthHeat(1), "249;38;114") { // colorBad — hottest
		t.Errorf("depth 1 should be the hot color, got %q", depthHeat(1))
	}
	if !strings.Contains(depthHeat(3), "102;217;239") { // colorBar — cool
		t.Errorf("depth 3 should be the cool color, got %q", depthHeat(3))
	}
	if depthHeat(1) == depthHeat(4) {
		t.Error("a near and a far node should not share a heat color")
	}
	// The Impact tab shows the heat legend beside the Blast radius title.
	m := sized(t, 100, 30)
	m.active = tabImpact
	m.impact.SetValue("X")
	m, _ = applyMsg(m, impactMsg{symbol: "X", rep: &app.ImpactReport{
		Symbol: "X", Found: true,
		BlastRadius: []app.ImpactNode{{Symbol: "A", Depth: 1}, {Symbol: "B", Depth: 4}},
	}})
	if out := m.render(); !strings.Contains(out, "heat ") {
		t.Errorf("Impact tab should show the depth-heat legend:\n%s", out)
	}
}

func TestImpactNamesCoveringTests(t *testing.T) {
	m := sized(t, 120, 40)
	m.active = tabImpact
	m.impact.SetValue("Foo")
	rep := &app.ImpactReport{
		Symbol: "Foo", Found: true,
		BlastRadius: []app.ImpactNode{{Symbol: "TestFoo", Kind: "test", Depth: 1}},
		Tests:       []app.ImpactNode{{Symbol: "TestFoo", FQN: "app.TestFoo", Kind: "test"}},
	}
	m, _ = applyMsg(m, impactMsg{symbol: "Foo", rep: rep})
	out := m.render()
	if !strings.Contains(out, "covered by") || !strings.Contains(out, "app.TestFoo") {
		t.Errorf("impact should name the covering tests:\n%s", out)
	}
}

func TestGraphMapToggle(t *testing.T) {
	m := sized(t, 120, 40)
	m, _ = applyMsg(m, graphHubsMsg{hubs: []app.HotspotRef{{Symbol: "Authenticate", FQN: "auth.Authenticate", InDegree: 12}}})
	m, _ = applyMsg(m, graphDetailMsg{symbol: "Authenticate",
		callers: []app.SymbolRef{{Symbol: "Login", FQN: "api.Login", File: "api.go", StartLine: 10}},
		callees: []app.SymbolRef{{Symbol: "parseJWT", FQN: "auth.parseJWT", File: "jwt.go", StartLine: 5}},
	})
	// Default: the list detail (not the map).
	if out := m.render(); !strings.Contains(out, "Called by (1)") || strings.Contains(out, "Neighborhood map") {
		t.Fatalf("default Graph detail should be the list view, not the map:\n%s", out)
	}
	// m toggles the neighborhood map: boxed focal node + caller/callee branches.
	m, _ = applyMsg(m, tea.KeyPressMsg(tea.Key{Text: "m", Code: 'm'}))
	if !m.graphMap {
		t.Fatal("m should toggle the neighborhood map on")
	}
	out := m.render()
	for _, want := range []string{"Neighborhood map", "auth.Authenticate", "called by (1)", "calls (1)", "╭", "╰"} {
		if !strings.Contains(out, want) {
			t.Errorf("map view should contain %q:\n%s", want, out)
		}
	}
	// Width is still respected with the box present.
	for i, line := range strings.Split(out, "\n") {
		if wd := lipgloss.Width(line); wd > 120 {
			t.Errorf("map line %d width %d exceeds 120: %q", i, wd, line)
		}
	}
	// m toggles back to the list.
	m, _ = applyMsg(m, tea.KeyPressMsg(tea.Key{Text: "m", Code: 'm'}))
	if m.graphMap || !strings.Contains(m.render(), "Called by (1)") {
		t.Error("m should toggle back to the list detail")
	}
}

func TestMapRevealLifecycle(t *testing.T) {
	m := sized(t, 120, 40)
	m, _ = applyMsg(m, graphHubsMsg{hubs: []app.HotspotRef{{Symbol: "Authenticate", FQN: "auth.Authenticate", InDegree: 12}}})
	m, _ = applyMsg(m, graphDetailMsg{symbol: "Authenticate",
		callers: []app.SymbolRef{{Symbol: "Login", FQN: "api.LoginHandler", File: "api.go", StartLine: 10}},
		callees: []app.SymbolRef{{Symbol: "parseJWT", FQN: "auth.parseJWT", File: "jwt.go", StartLine: 5}},
	})
	m, cmd := applyMsg(m, tea.KeyPressMsg(tea.Key{Text: "m", Code: 'm'}))
	if !m.mapActive || m.mapReveal != 0 || cmd == nil {
		t.Fatalf("toggling the map on should start the grow-in (active=%v reveal=%v cmd=%v)", m.mapActive, m.mapReveal, cmd != nil)
	}
	// At reveal 0 the branch lines haven't grown in yet (but the structure has).
	if out := m.render(); strings.Contains(out, "api.LoginHandler") || !strings.Contains(out, "Neighborhood map") {
		t.Errorf("at reveal 0 the map structure shows but the caller branch hasn't grown in yet:\n%s", out)
	}
	// Drive frames until the spring settles.
	for i := 0; i < 600 && m.mapActive; i++ {
		m, _ = applyMsg(m, animTickMsg{})
	}
	if m.mapActive || absf(1-m.mapReveal) > 0.01 {
		t.Fatalf("map reveal never settled (active=%v reveal=%v)", m.mapActive, m.mapReveal)
	}
	// Fully revealed → the caller branch is now drawn.
	if !strings.Contains(m.render(), "api.LoginHandler") {
		t.Errorf("after settling, the map should show the caller branch")
	}
}

func TestGraphPaneShowsAnnotations(t *testing.T) {
	m := sized(t, 120, 40)
	m, _ = applyMsg(m, graphHubsMsg{hubs: []app.HotspotRef{{Symbol: "Run", FQN: "p.Run", InDegree: 5}}})
	m, _ = applyMsg(m, graphDetailMsg{
		symbol:      "Run",
		callers:     []app.SymbolRef{{Symbol: "Top"}},
		annotations: []graph.Annotation{{ID: 1, Kind: "node", Target: "p.Run", Source: "postgres", Note: "hot path"}},
	})
	out := m.render()
	for _, want := range []string{"⟐ 1", "postgres: hot path"} {
		if !strings.Contains(out, want) {
			t.Errorf("graph detail should surface annotations (%q):\n%s", want, out)
		}
	}
}

func TestImpactPaneShowsAnnotations(t *testing.T) {
	m := sized(t, 120, 40)
	m.active = tabImpact
	m.impact.SetValue("Foo")
	rep := &app.ImpactReport{
		Symbol: "Foo", Found: true,
		BlastRadius: []app.ImpactNode{{Symbol: "Caller", Depth: 1}},
		Annotations: []graph.Annotation{{ID: 1, Kind: "node", Target: "Foo", Source: "postgres", Note: "hot in prod"}},
	}
	m, _ = applyMsg(m, impactMsg{symbol: "Foo", rep: rep})
	out := m.render()
	if !strings.Contains(out, "hot in prod") {
		t.Errorf("impact pane should surface pinned annotations inline:\n%s", out)
	}
}

func TestImpactPaneWarnsOnAmbiguousName(t *testing.T) {
	m := sized(t, 120, 40)
	m.active = tabImpact
	m.impact.SetValue("Close")
	rep := &app.ImpactReport{
		Symbol: "Close", Found: true,
		Locations:   []app.SymbolRef{{Symbol: "Close", File: "a.go"}, {Symbol: "Close", File: "b.go"}},
		BlastRadius: []app.ImpactNode{{Symbol: "Caller", Depth: 1}},
		Note:        `"Close" matches 2 definitions (name-based) — direct callers, blast radius, and covering tests below merge all of them; for one exact method use callers/callees --lsp`,
	}
	m, _ = applyMsg(m, impactMsg{symbol: "Close", rep: rep})
	if out := m.render(); !strings.Contains(out, "matches 2 definitions") {
		t.Errorf("impact pane should warn when the name is ambiguous:\n%s", out)
	}
}

func TestImpactShowsSignaturePreview(t *testing.T) {
	m := sized(t, 120, 40)
	m.active = tabImpact
	m.impact.SetValue("Foo")
	rep := &app.ImpactReport{
		Symbol: "Foo", Found: true,
		BlastRadius: []app.ImpactNode{
			{Symbol: "Run", Depth: 1, Signature: "func Run(x int) error", Doc: "Run executes the pipeline.\nMore detail."},
		},
	}
	m, _ = applyMsg(m, impactMsg{symbol: "Foo", rep: rep})
	out := m.render()
	if !strings.Contains(out, "func Run(x int) error") {
		t.Errorf("impact should preview the selected node's signature:\n%s", out)
	}
	if !strings.Contains(out, "Run executes the pipeline.") {
		t.Errorf("impact should preview the docstring's first line:\n%s", out)
	}
	if strings.Contains(out, "More detail.") {
		t.Error("only the docstring's first line should show in the preview")
	}
}

func TestImpactHeaderShowsAnalyzedSymbol(t *testing.T) {
	m := sized(t, 120, 40)
	m.active = tabImpact
	m.impact.SetValue("Authenticate")
	rep := &app.ImpactReport{
		Symbol: "Authenticate", Found: true,
		Locations: []app.SymbolRef{{
			Symbol: "Authenticate", FQN: "auth.Authenticate", File: "auth.go", StartLine: 42,
			Signature: "func Authenticate(tok string) (Claims, error)", Doc: "Authenticate validates a jwt.",
		}},
		BlastRadius: []app.ImpactNode{{Symbol: "Login", Depth: 1}},
	}
	m, _ = applyMsg(m, impactMsg{symbol: "Authenticate", rep: rep})
	out := m.render()
	for _, want := range []string{"func Authenticate(tok string) (Claims, error)", "Authenticate validates a jwt."} {
		if !strings.Contains(out, want) {
			t.Errorf("impact header should show the analyzed symbol's %q:\n%s", want, out)
		}
	}
}

func TestSearchPaneMarksAnnotated(t *testing.T) {
	m := sized(t, 120, 40)
	m.active = tabSearch
	m.search.SetValue("x")
	m, _ = applyMsg(m, semanticMsg{query: "x", mode: "name", hits: []app.SemanticHit{
		{Symbol: "Plain"},
		{Symbol: "Pinned", Annotations: []graph.Annotation{{ID: 1, Kind: "node", Target: "Pinned", Source: "note"}}},
	}})
	out := m.render()
	if !strings.Contains(out, "⟐") {
		t.Errorf("search list should mark annotated hits with ⟐:\n%s", out)
	}
}

func TestColdStartTabsHintToIndex(t *testing.T) {
	for _, tc := range []struct {
		name   string
		active tab
	}{
		{"Impact", tabImpact},
		{"Search", tabSearch},
		{"Path", tabPath},
	} {
		m := sized(t, 120, 40)
		m.active = tc.active
		m, _ = applyMsg(m, statusMsg{st: &app.StatusReport{Project: "p", Registered: false}})
		out := m.render()
		if !strings.Contains(out, "no index yet") {
			t.Errorf("%s tab on an unindexed project should hint to index instead of inviting input, got:\n%s", tc.name, out)
		}
	}
}

// TestGraphEmptyStateDistinguishesNoCallGraph verifies the Graph tab tells the
// truth when there are no hubs: "no index yet" only when genuinely unindexed,
// but "indexed, no call graph — reindex with --precise" once symbols exist (the
// normal state for a TypeScript project indexed without --precise, where the old
// blanket "no index" message was misleading).
func TestGraphEmptyStateDistinguishesNoCallGraph(t *testing.T) {
	loadEmptyHubs := func(m Model) Model {
		m, _ = applyMsg(m, graphHubsMsg{hubs: nil}) // graphLoaded=true, no hubs
		return m
	}

	// Unindexed → the index hint.
	m := sized(t, 120, 40) // starts on Graph
	m, _ = applyMsg(m, statusMsg{st: &app.StatusReport{Project: "p", Registered: false}})
	m = loadEmptyHubs(m)
	if out := m.render(); !strings.Contains(out, "no index yet") {
		t.Errorf("unindexed Graph should hint to index, got:\n%s", out)
	}

	// Indexed TypeScript, no call edges → name --precise and the TS server, NOT "no index".
	m = sized(t, 120, 40)
	m, _ = applyMsg(m, statusMsg{st: &app.StatusReport{
		Project: "ts", Registered: true, Nodes: 12, Edges: 5,
		Languages: map[string]int{"typescript": 4},
	}})
	m = loadEmptyHubs(m)
	out := m.render()
	if strings.Contains(out, "no index yet") {
		t.Errorf("indexed-but-no-calls Graph must not claim 'no index yet', got:\n%s", out)
	}
	if !strings.Contains(out, "--precise") || !strings.Contains(out, "typescript-language-server") {
		t.Errorf("TypeScript empty-graph hint should point at --precise + the TS server, got:\n%s", out)
	}

	// Indexed Go-only, no call edges (trivial project) → still --precise, no TS server line.
	m = sized(t, 120, 40)
	m, _ = applyMsg(m, statusMsg{st: &app.StatusReport{
		Project: "g", Registered: true, Nodes: 3, Edges: 1,
		Languages: map[string]int{"go": 3},
	}})
	m = loadEmptyHubs(m)
	out = m.render()
	if !strings.Contains(out, "--precise") {
		t.Errorf("Go empty-graph hint should still suggest --precise, got:\n%s", out)
	}
	if strings.Contains(out, "typescript-language-server") {
		t.Errorf("Go-only project should not mention the TypeScript server, got:\n%s", out)
	}
}

func TestSearchNameModeNoEmbeddingsHint(t *testing.T) {
	m := sized(t, 120, 40)
	m.active = tabSearch
	m, _ = applyMsg(m, statusMsg{st: &app.StatusReport{Registered: true, Vectors: 0}}) // structure-only
	m, _ = applyMsg(m, semanticMsg{query: "x", mode: "name", hits: []app.SemanticHit{{Symbol: "A"}}})
	if !strings.Contains(m.render(), "no embeddings") {
		t.Error("name-mode search with no embeddings should hint why it isn't semantic")
	}
}

func TestSearchPreviewShowsSelectedAnnotations(t *testing.T) {
	m := sized(t, 120, 40)
	m.active = tabSearch
	m.search.SetValue("x")
	m, _ = applyMsg(m, semanticMsg{query: "x", mode: "name", hits: []app.SemanticHit{
		{Symbol: "Pinned", Annotations: []graph.Annotation{{ID: 1, Kind: "node", Target: "Pinned", Source: "postgres", Note: "hot path"}}},
	}})
	out := m.render() // searchSel defaults to 0 (the annotated hit)
	if !strings.Contains(out, "postgres: hot path") {
		t.Errorf("search preview should show the selected hit's annotations:\n%s", out)
	}
}

func TestSearchShowsSignaturePreview(t *testing.T) {
	m := sized(t, 120, 40)
	m.active = tabSearch
	m.search.SetValue("auth")
	m, _ = applyMsg(m, semanticMsg{query: "auth", mode: "name", hits: []app.SemanticHit{
		{Symbol: "Authenticate", Signature: "func Authenticate(tok string) bool"},
	}})
	out := m.render()
	if !strings.Contains(out, "func Authenticate(tok string) bool") {
		t.Errorf("search should preview the selected hit's signature:\n%s", out)
	}
}

func TestSourceTargetAcrossTabs(t *testing.T) {
	// Search: ctrl+s targets the selected hit.
	m := sized(t, 100, 30)
	m.active = tabSearch
	m, _ = applyMsg(m, semanticMsg{query: "x", mode: "name", hits: []app.SemanticHit{
		{Symbol: "A", File: "a.go", StartLine: 3}, {Symbol: "B", File: "b.go", StartLine: 9},
	}})
	m, _ = applyMsg(m, tea.KeyPressMsg(tea.Key{Code: tea.KeyDown})) // select B
	sym, file, line, ok := m.sourceTarget()
	if !ok || sym != "B" || file != "b.go" || line != 9 {
		t.Errorf("search source target = (%q,%q,%d,%v), want B/b.go/9/true", sym, file, line, ok)
	}

	// Impact: ctrl+s targets the selected blast node.
	mi := sized(t, 100, 30)
	mi.active = tabImpact
	mi.impact.SetValue("Foo")
	mi, _ = applyMsg(mi, impactMsg{symbol: "Foo", rep: &app.ImpactReport{
		Symbol: "Foo", Found: true,
		BlastRadius: []app.ImpactNode{{Symbol: "Caller", File: "c.go", StartLine: 12}},
	}})
	if sym, file, line, ok := mi.sourceTarget(); !ok || sym != "Caller" || file != "c.go" || line != 12 {
		t.Errorf("impact source target = (%q,%q,%d,%v), want Caller/c.go/12/true", sym, file, line, ok)
	}
}

// TestOpenInGraphFromSearch verifies ctrl+g re-centers the Graph walker on the
// active tab's selection and switches to it — making any search hit (or impact
// node, or metrics row) a starting point for walking the call graph, not just
// the hubs.
func TestOpenInGraphFromSearch(t *testing.T) {
	m := sized(t, 120, 40)
	m.active = tabSearch
	m, _ = applyMsg(m, semanticMsg{query: "auth", mode: "name", hits: []app.SemanticHit{
		{Symbol: "validateToken", FQN: "auth.validateToken", File: "auth.go", StartLine: 4},
	}})

	m2, cmd := applyMsg(m, tea.KeyPressMsg(tea.Key{Code: 'g', Mod: tea.ModCtrl}))
	if m2.active != tabGraph {
		t.Fatalf("ctrl+g should switch to the Graph tab, got %v", m2.active)
	}
	if m2.graphCenter.sym != "validateToken" || m2.graphCenter.fqn != "auth.validateToken" {
		t.Errorf("graph should be centered on the search hit, got %+v", m2.graphCenter)
	}
	if m2.graphFocus != focusRefs {
		t.Error("ctrl+g should focus the callers/calls pane, ready to walk")
	}
	if len(m2.graphStack) != 0 {
		t.Error("opening from another tab should start a fresh walk (empty stack)")
	}
	if cmd == nil {
		t.Error("ctrl+g should fire a detail load for the centered symbol")
	}
}

func TestHeaderShowsStaleness(t *testing.T) {
	m := sized(t, 120, 40)
	m, _ = applyMsg(m, statusMsg{st: &app.StatusReport{
		Project: "demo", Registered: true, Nodes: 10, Edges: 5, Files: 3,
	}})
	// Before staleness is known (async), no warning.
	if strings.Contains(m.render(), "stale") {
		t.Error("header should not warn before staleness is known")
	}
	// Drift reported → header warns and points at ctrl+r.
	m, _ = applyMsg(m, stalenessMsg{st: &index.Staleness{Changed: 2, New: 1}})
	out := m.render()
	if !strings.Contains(out, "stale") || !strings.Contains(out, "ctrl+r") {
		t.Errorf("header should warn about a stale index and mention ctrl+r:\n%s", out)
	}
	// A fresh index (all zeros) → no warning.
	m, _ = applyMsg(m, stalenessMsg{st: &index.Staleness{}})
	if strings.Contains(m.render(), "stale") {
		t.Error("header should not warn when the index is fresh")
	}
}

func TestHeaderShowsDaemon(t *testing.T) {
	m := sized(t, 120, 40)
	m, _ = applyMsg(m, statusMsg{st: &app.StatusReport{
		Project: "demo", Registered: true, Nodes: 10, Edges: 5, Files: 3,
	}})
	// No daemon running → no indicator.
	if strings.Contains(m.render(), "● daemon") {
		t.Error("header should not show a daemon indicator when none is running")
	}
	// Daemon running → green indicator carrying the branch.
	m, _ = applyMsg(m, daemonMsg{info: &daemon.Info{PID: 123, Watching: true, Branch: "main", ProjectName: "demo"}})
	out := m.render()
	if !strings.Contains(out, "● daemon") || !strings.Contains(out, "main") {
		t.Errorf("header should show the live daemon indicator + branch:\n%s", out)
	}
	// Daemon stops (a later poll returns nil) → indicator disappears.
	m, _ = applyMsg(m, daemonMsg{info: nil})
	if strings.Contains(m.render(), "● daemon") {
		t.Error("header should drop the daemon indicator once the daemon stops")
	}
}

func TestHelpOverlay(t *testing.T) {
	m := sized(t, 120, 40)
	m.active = tabSearch // a tab with a focused text input
	m, _ = applyMsg(m, tea.KeyPressMsg(tea.Key{Text: "?", Code: '?'}))
	if !m.showHelp {
		t.Fatal("? should open the help overlay")
	}
	out := m.render()
	// Document every key that actually works, where it works: the vim aliases
	// (k/j) and the Source-view scroll mode were undocumented before, and
	// Metrics was wrongly grouped with the text-input tabs (which have no k/j).
	for _, want := range []string{"Global", "Graph", "Metrics", "Impact / Search", "Path",
		"re-center", "precise", "k/j", "home/end", "Source / context", "ctrl+o", "orient"} {
		if !strings.Contains(out, want) {
			t.Errorf("help overlay should document %q:\n%s", want, out)
		}
	}
	// ? closes it, and never reached the search input.
	m, _ = applyMsg(m, tea.KeyPressMsg(tea.Key{Text: "?", Code: '?'}))
	if m.showHelp {
		t.Error("? should close the help overlay")
	}
	if m.search.Value() != "" {
		t.Errorf("'?' should toggle help, not type into the search input (got %q)", m.search.Value())
	}
}

func TestSourceViewerScrollAndClose(t *testing.T) {
	m := sized(t, 80, 12) // small height so the content is scrollable
	lines := make([]string, 30)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i)
	}
	m, _ = applyMsg(m, sourceMsg{title: "pkg.Foo  foo.go:1-30", lines: lines})
	if !m.srcView {
		t.Fatal("sourceMsg should open the source viewer")
	}
	out := m.render()
	if !strings.Contains(out, "pkg.Foo  foo.go:1-30") || !strings.Contains(out, "line 0") {
		t.Errorf("viewer should show the title and first lines:\n%s", out)
	}
	if !strings.Contains(out, "of 30)") {
		t.Errorf("viewer should show a scroll-position indicator:\n%s", out)
	}
	if m.maxSrcScroll() <= 0 {
		t.Fatalf("expected scrollable content, maxScroll=%d", m.maxSrcScroll())
	}

	// scrolling down advances the window; a number key must NOT switch tabs.
	m, _ = applyMsg(m, tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	m, _ = applyMsg(m, tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	if m.srcScroll != 2 {
		t.Errorf("srcScroll = %d, want 2", m.srcScroll)
	}
	m, _ = applyMsg(m, tea.KeyPressMsg(tea.Key{Text: "2", Code: '2'}))
	if m.active != tabGraph {
		t.Error("number keys should be captured by the viewer, not switch tabs")
	}

	// q closes the viewer.
	m, _ = applyMsg(m, tea.KeyPressMsg(tea.Key{Text: "q", Code: 'q'}))
	if m.srcView {
		t.Error("q should dismiss the source viewer")
	}
}

func TestHighlightSource(t *testing.T) {
	// A known language highlights: per-line ANSI, line count preserved.
	lines, ok := highlightSource("svc.go", "package main\n\nfunc Run() int { return 42 }\n")
	if !ok {
		t.Fatal("Go source should be highlightable")
	}
	if len(lines) < 3 {
		t.Errorf("want >=3 lines for 3 source lines, got %d", len(lines))
	}
	if !strings.Contains(strings.Join(lines, "\n"), "\x1b[") {
		t.Error("highlighted output should carry ANSI color escapes")
	}
}

// TestSourceOverlayFileLineGutter pins FIX.md §3: the source overlay's gutter shows
// real file line numbers (from firstLine), not 1-based within-def numbers.
func TestSourceOverlayFileLineGutter(t *testing.T) {
	m := sized(t, 100, 20)
	m, _ = applyMsg(m, sourceMsg{title: "x.F  x.go:10-12",
		lines: []string{"func F() {", "  return", "}"}, gutter: true, firstLine: 10})
	out := m.render()
	for _, want := range []string{"  10 ", "  12 "} {
		if !strings.Contains(out, want) {
			t.Errorf("gutter should show file line %q (firstLine=10):\n%s", want, out)
		}
	}
	if strings.Contains(out, "   1 func F") {
		t.Error("gutter should NOT show 1-based within-def numbers when firstLine is set")
	}
}

func TestContextCardAndOverlay(t *testing.T) {
	rep := &app.ContextReport{
		Symbol: "Foo", Found: true,
		Definitions: []app.SourceMatch{{Symbol: "Foo", Kind: "func", File: "a.go",
			StartLine: 10, EndLine: 20, Signature: "func Foo() error", Doc: "Foo does things."}},
		Callers:      []app.SymbolRef{{Symbol: "Bar", Kind: "func", File: "b.go", StartLine: 3}},
		CallersTotal: 5, // capped: 1 shown, "+4 more"
		CalleesTotal: 0, // empty: "(none)"
		Tests:        []app.ImpactNode{{Symbol: "TestFoo", File: "a_test.go", StartLine: 1}},
		TestsTotal:   1,
		BlastRadius:  7,
		BlastDepth:   3,
		Annotations:  []graph.Annotation{{Source: "note", Note: "watch this"}},
	}
	title, lines := contextCard(rep)
	if title != "Foo" {
		t.Errorf("card title = %q, want Foo", title)
	}
	body := strings.Join(lines, "\n")
	for _, want := range []string{
		"func Foo() error", "Foo does things.", "a.go:10-20",
		"Callers (5)", "Bar", "+4 more",
		"Callees (0)", "(none)",
		"Tests (1)", "TestFoo",
		"Blast radius: 7", "depth ≤ 3", "Annotations (1)", "watch this",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("context card missing %q:\n%s", want, body)
		}
	}

	// The overlay shows the card (no line-number gutter) and ctrl+o is wired.
	m := sized(t, 100, 30)
	m, _ = applyMsg(m, sourceMsg{title: title, lines: lines, gutter: false})
	if !m.srcView || m.srcGutter {
		t.Fatalf("context overlay should open with gutter off (srcView=%v gutter=%v)", m.srcView, m.srcGutter)
	}
	out := m.render()
	if !strings.Contains(out, "Callers (5)") {
		t.Errorf("overlay should render the context card:\n%s", out)
	}
	if strings.Contains(out, "  10 ") && strings.Contains(out, "  11 ") {
		t.Error("context overlay should not render a line-number gutter")
	}
}

func TestGraphHubPageNavigation(t *testing.T) {
	m := sized(t, 120, 40) // pageStep = clamp(40-6,1,40) = 34
	hubs := make([]app.HotspotRef, 50)
	for i := range hubs {
		hubs[i] = app.HotspotRef{Symbol: fmt.Sprintf("H%d", i)}
	}
	m, _ = applyMsg(m, graphHubsMsg{hubs: hubs})

	// pgdown jumps by a page and loads the landed hub's detail.
	u, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgDown}))
	mm := u.(Model)
	if mm.graphSel != 34 {
		t.Errorf("pgdown graphSel = %d, want 34 (one page)", mm.graphSel)
	}
	if cmd == nil {
		t.Error("page jump should load the landed hub's detail")
	}
	// pgdown again clamps to the last hub.
	mm, _ = applyMsg(mm, tea.KeyPressMsg(tea.Key{Code: tea.KeyPgDown}))
	if mm.graphSel != 49 {
		t.Errorf("pgdown clamp = %d, want 49", mm.graphSel)
	}
	// home jumps to the top.
	mm, _ = applyMsg(mm, tea.KeyPressMsg(tea.Key{Code: tea.KeyHome}))
	if mm.graphSel != 0 {
		t.Errorf("home graphSel = %d, want 0", mm.graphSel)
	}
}

func TestSearchPageNavigation(t *testing.T) {
	m := sized(t, 100, 30) // pageStep = 24
	m.active = tabSearch
	hits := make([]app.SemanticHit, 40)
	for i := range hits {
		hits[i] = app.SemanticHit{Symbol: fmt.Sprintf("S%d", i)}
	}
	m, _ = applyMsg(m, semanticMsg{query: "x", hits: hits})
	m, _ = applyMsg(m, tea.KeyPressMsg(tea.Key{Code: tea.KeyPgDown}))
	if m.searchSel != 24 {
		t.Errorf("pgdown searchSel = %d, want 24", m.searchSel)
	}
	m, _ = applyMsg(m, tea.KeyPressMsg(tea.Key{Code: tea.KeyPgUp}))
	if m.searchSel != 0 {
		t.Errorf("pgup searchSel = %d, want 0", m.searchSel)
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
