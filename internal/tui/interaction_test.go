package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/abdul-hamid-achik/codemap/internal/app"
)

// searchModel builds a settled Search tab with n hits at a known size.
func searchModel(t *testing.T, w, h, n int) Model {
	t.Helper()
	m := sized(t, w, h)
	m, _ = applyMsg(m, statusMsg{st: &app.StatusReport{Registered: true}})
	m.active = tabSearch
	hits := make([]app.SemanticHit, n)
	for i := range hits {
		hits[i] = app.SemanticHit{Symbol: fmt.Sprintf("S%d", i), File: "f.go", StartLine: i + 1}
	}
	m, _ = applyMsg(m, semanticMsg{query: "q", mode: "name", hits: hits})
	m.search.SetValue("q") // settle: input matches the last-run query
	return m
}

func TestMouseWheelScrollsSelection(t *testing.T) {
	m := searchModel(t, 100, 30, 8)
	if m.searchSel != 0 {
		t.Fatalf("fresh search selection = %d, want 0", m.searchSel)
	}
	m, _ = applyMsg(m, tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if m.searchSel != wheelStep {
		t.Errorf("wheel down moved selection to %d, want %d", m.searchSel, wheelStep)
	}
	m, _ = applyMsg(m, tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if m.searchSel != 0 {
		t.Errorf("wheel up should return to 0, got %d", m.searchSel)
	}
}

func TestMouseClickSwitchesTab(t *testing.T) {
	m := seedTabs(sized(t, 120, 40))
	// The tab bar renders " 1 Graph "(9) " 2 Metrics "(11) …; column 12 is inside
	// the Metrics chip.
	m, _ = applyMsg(m, tea.MouseClickMsg{X: 12, Y: tabBarRow, Button: tea.MouseLeft})
	if m.active != tabMetrics {
		t.Errorf("click on the Metrics chip switched to %v, want Metrics", m.active)
	}
}

func TestMouseClickSelectsSearchRow(t *testing.T) {
	m := searchModel(t, 100, 30, 6)
	// budget = clamp((30-3)-9)=18 ≥ 6 → start 0; first row at bodyTop+2 = 4.
	m, _ = applyMsg(m, tea.MouseClickMsg{X: 5, Y: 6, Button: tea.MouseLeft})
	if m.searchSel != 2 {
		t.Errorf("click at y=6 selected row %d, want 2", m.searchSel)
	}
	// A click below the last row is a no-op (keeps the selection).
	m, _ = applyMsg(m, tea.MouseClickMsg{X: 5, Y: 99, Button: tea.MouseLeft})
	if m.searchSel != 2 {
		t.Errorf("off-list click changed selection to %d, want 2", m.searchSel)
	}
}

func TestMouseClickSelectsImpactRow(t *testing.T) {
	m := sized(t, 120, 40)
	m.active = tabImpact
	m.impact.SetValue("X")
	br := make([]app.ImpactNode, 5)
	for i := range br {
		br[i] = app.ImpactNode{Symbol: fmt.Sprintf("N%d", i), File: "f.go", StartLine: i + 1, Depth: i + 1}
	}
	m, _ = applyMsg(m, impactMsg{symbol: "X", rep: &app.ImpactReport{Symbol: "X", Found: true, BlastRadius: br}})
	// No locations/tests/annotations → lead lines = 6, first blast row at y=8.
	m, _ = applyMsg(m, tea.MouseClickMsg{X: 4, Y: 11, Button: tea.MouseLeft})
	if m.impactSel != 3 {
		t.Errorf("click at y=11 selected blast node %d, want 3", m.impactSel)
	}
}

func TestMouseClickSelectsGraphHub(t *testing.T) {
	m := seedTabs(sized(t, 120, 40))
	m.active = tabGraph
	// Hubs list: title at bodyTop(2), first hub at y=3; y=4 is hub index 1.
	m, _ = applyMsg(m, tea.MouseClickMsg{X: 5, Y: 4, Button: tea.MouseLeft})
	if m.graphSel != 1 {
		t.Errorf("click at y=4 selected hub %d, want 1", m.graphSel)
	}
	if m.graphFocus != focusHubs {
		t.Error("clicking the hub list should focus the hub pane")
	}
	// A click in the right (refs) pane is ignored for selection.
	m, _ = applyMsg(m, tea.MouseClickMsg{X: 90, Y: 4, Button: tea.MouseLeft})
	if m.graphSel != 1 {
		t.Errorf("refs-pane click changed hub selection to %d, want 1", m.graphSel)
	}
}

func TestYankSetsClipboardAndStatus(t *testing.T) {
	m := searchModel(t, 100, 30, 3)
	m, cmd := applyMsg(m, tea.KeyPressMsg(tea.Key{Text: "y", Code: 'y'}))
	if !strings.Contains(m.statusMsg, "yanked f.go:1") {
		t.Errorf("y should report the yanked location, got %q", m.statusMsg)
	}
	if cmd == nil {
		t.Error("y should return a clipboard (OSC52) command")
	}
	if m.search.Value() != "q" {
		t.Errorf("y must not type into a settled query, got %q", m.search.Value())
	}
}

func TestYankTypesWhenEditing(t *testing.T) {
	m := searchModel(t, 100, 30, 3)
	m.search.SetValue("qq") // now the input differs from the last-run query → editing
	m, _ = applyMsg(m, tea.KeyPressMsg(tea.Key{Text: "y", Code: 'y'}))
	if !strings.Contains(m.search.Value(), "y") || len(m.search.Value()) != 3 {
		t.Errorf("y on an edited query should type into it, got %q", m.search.Value())
	}
}

func TestEditorNoEditorIsNoop(t *testing.T) {
	t.Setenv("EDITOR", "")
	t.Setenv("VISUAL", "")
	m := searchModel(t, 100, 30, 3)
	m, cmd := applyMsg(m, tea.KeyPressMsg(tea.Key{Text: "e", Code: 'e'}))
	if cmd != nil {
		t.Error("e with no $EDITOR/$VISUAL should not launch anything")
	}
	if !strings.Contains(m.statusMsg, "no $EDITOR") {
		t.Errorf("e with no editor should explain, got %q", m.statusMsg)
	}
}

func TestEditorLaunchesWhenSet(t *testing.T) {
	t.Setenv("EDITOR", "true")
	m := searchModel(t, 100, 30, 3)
	m, cmd := applyMsg(m, tea.KeyPressMsg(tea.Key{Text: "e", Code: 'e'}))
	if cmd == nil {
		t.Error("e with $EDITOR set should return an exec command")
	}
	if !strings.Contains(m.statusMsg, "opening f.go:1") {
		t.Errorf("e should report the file being opened, got %q", m.statusMsg)
	}
}

func TestEditorCommandConventions(t *testing.T) {
	cases := []struct {
		editor   string
		wantName string
		wantArgs []string
	}{
		{"vim", "vim", []string{"+42", "/x/f.go"}},
		{"nvim", "nvim", []string{"+42", "/x/f.go"}},
		{"code -w", "code", []string{"-w", "-g", "/x/f.go:42"}},
		{"subl", "subl", []string{"/x/f.go:42"}},
		{"goland", "goland", []string{"--line", "42", "/x/f.go"}},
	}
	for _, c := range cases {
		cmd := editorCommand(c.editor, "/x/f.go", 42)
		gotName := cmd.Args[0]
		gotArgs := cmd.Args[1:]
		if gotName != c.wantName || strings.Join(gotArgs, " ") != strings.Join(c.wantArgs, " ") {
			t.Errorf("editorCommand(%q) = %s %v, want %s %v", c.editor, gotName, gotArgs, c.wantName, c.wantArgs)
		}
	}
}

func TestUniformQuit(t *testing.T) {
	q := tea.KeyPressMsg(tea.Key{Text: "q", Code: 'q'})
	// Non-input tabs quit.
	for _, tb := range []tab{tabGraph, tabMetrics} {
		m := testModel()
		m.active = tb
		if _, cmd := m.Update(q); cmd == nil {
			t.Errorf("q should quit on %v", tb)
		}
	}
	// A focused text field types q instead of quitting (the value grows).
	m := searchModel(t, 100, 30, 2)
	m, _ = applyMsg(m, q)
	if !strings.Contains(m.search.Value(), "q") || len(m.search.Value()) != 2 {
		t.Errorf("q should type into the Search query, got %q", m.search.Value())
	}
	// A focused Path result quits.
	pm := seedTabs(sized(t, 120, 40))
	pm.active = tabPath
	pm.pathFocus = focusPathResult
	if _, c := pm.Update(q); c == nil {
		t.Error("q should quit on a focused Path result")
	}
}

func TestReindexFromColdEmptyTab(t *testing.T) {
	m := testModel() // loading, no status, no data
	m.active = tabSearch
	m, cmd := applyMsg(m, tea.KeyPressMsg(tea.Key{Code: 'r', Mod: tea.ModCtrl}))
	if m.statusMsg != "indexing…" {
		t.Errorf("ctrl+r on a cold tab should start indexing, status = %q", m.statusMsg)
	}
	if cmd == nil {
		t.Error("ctrl+r on a cold tab should schedule a reindex command")
	}
}

func TestScrollbarColumn(t *testing.T) {
	// Everything fits → no scrollbar.
	if scrollbarColumn(10, 8, 0, 10) != nil {
		t.Error("a list that fits should have no scrollbar")
	}
	// Overflow → a thumb sized to the visible fraction, moving with start.
	top := scrollbarColumn(10, 40, 0, 10)
	if len(top) != 10 {
		t.Fatalf("scrollbar height = %d, want 10", len(top))
	}
	countThumb := func(cells []string) int {
		n := 0
		for _, c := range cells {
			if strings.Contains(c, "█") {
				n++
			}
		}
		return n
	}
	if countThumb(top) == 0 {
		t.Error("an overflowing list should render a thumb")
	}
	// At the top the thumb includes the first cell; scrolled to the end it includes the last.
	if !strings.Contains(top[0], "█") {
		t.Error("at scroll 0 the thumb should sit at the top")
	}
	bottom := scrollbarColumn(10, 40, 30, 10) // start == maxStart
	if !strings.Contains(bottom[len(bottom)-1], "█") {
		t.Error("scrolled to the end, the thumb should reach the bottom")
	}
}

func TestSourceOverlayShowsScrollbar(t *testing.T) {
	m := sized(t, 100, 20)
	lines := make([]string, 200)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i)
	}
	m, _ = applyMsg(m, sourceMsg{title: "big.go", lines: lines, gutter: true, firstLine: 1})
	out := m.render()
	if !strings.Contains(out, "█") {
		t.Errorf("an overflowing source overlay should draw a scrollbar thumb:\n%s", out)
	}
}

func TestBlastRowTreeIndent(t *testing.T) {
	m := sized(t, 120, 40)
	shallow := m.blastRow(app.ImpactNode{Symbol: "A", File: "a.go", StartLine: 1, Depth: 1}, false, 120)
	deep := m.blastRow(app.ImpactNode{Symbol: "B", File: "b.go", StartLine: 2, Depth: 3}, false, 120)
	if !strings.Contains(deep, "└") {
		t.Errorf("a depth-3 node should render a tree branch connector:\n%q", deep)
	}
	if strings.Contains(shallow, "└") {
		t.Errorf("a depth-1 node roots the tree (no └ connector):\n%q", shallow)
	}
	// The deep node is indented past the shallow one (more leading spaces before B).
	if strings.Index(deep, "B") <= strings.Index(shallow, "A") {
		t.Errorf("depth-3 node should be indented deeper than depth-1:\nshallow=%q\ndeep=%q", shallow, deep)
	}
}
