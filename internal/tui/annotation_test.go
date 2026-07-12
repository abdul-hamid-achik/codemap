package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/abdul-hamid-achik/codemap/internal/app"
	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

func annotationKey(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Text: string(r), Code: r})
}

func typeAnnotation(t *testing.T, m Model, text string) Model {
	t.Helper()
	for _, r := range text {
		m, _ = applyMsg(m, annotationKey(r))
	}
	return m
}

func settledSearch(m Model, hits []app.SemanticHit, selected int) Model {
	m.loading = false
	m.active = tabSearch
	m.search.SetValue("shared")
	m.searchQuery = "shared"
	m.searchHits = hits
	m.searchSel = selected
	m.syncFocus()
	return m
}

func TestAnnotationComposerOpenTypeBackspaceCancelAndModalRouting(t *testing.T) {
	m, left, right := studioSelectorFixture(t)
	m = settledSearch(m, []app.SemanticHit{
		{Symbol: left.sym, FQN: left.fqn, Kind: left.kind, File: left.file, StartLine: left.line},
		{Symbol: right.sym, FQN: right.fqn, Kind: right.kind, File: right.file, StartLine: right.line},
	}, 1)

	m, _ = applyMsg(m, annotationKey('a'))
	if !m.annotationOpen || !sameGraphCenter(m.annotationCenter, right) || !m.annotationInput.Focused() {
		t.Fatalf("composer did not capture the exact selected definition: open=%v center=%+v", m.annotationOpen, m.annotationCenter)
	}
	m = typeAnnotation(t, m, "watch")
	m, _ = applyMsg(m, tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace}))
	if got := m.annotationInput.Value(); got != "watc" {
		t.Fatalf("composer edit = %q, want watc", got)
	}

	// Modal routing: printable global keys stay in the note, while tab/ctrl+r do
	// not switch views or launch work underneath the composer.
	m, _ = applyMsg(m, annotationKey('?'))
	m, _ = applyMsg(m, annotationKey('q'))
	selection := m.searchSel
	m, _ = applyMsg(m, tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	m, _ = applyMsg(m, tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	m, cmd := applyMsg(m, tea.KeyPressMsg(tea.Key{Code: 'r', Mod: tea.ModCtrl}))
	if cmd != nil || m.active != tabSearch || m.searchSel != selection || m.showHelp || m.statusMsg == "indexing…" {
		t.Fatalf("composer leaked a global shortcut: active=%v help=%v status=%q cmd=%v", m.active, m.showHelp, m.statusMsg, cmd != nil)
	}
	if got := m.annotationInput.Value(); got != "watc?q" {
		t.Fatalf("printable modal keys should edit the note, got %q", got)
	}

	m, _ = applyMsg(m, tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	if m.annotationOpen || !m.search.Focused() || m.searchSel != 1 || m.search.Value() != "shared" {
		t.Fatalf("cancel did not restore the prior exact Search state: %+v", m)
	}
}

func TestAnnotationRequiresExactSelection(t *testing.T) {
	m := sized(t, 80, 24)
	m.loading = false
	m.active = tabGraph
	m.graphCenter = graphCenter{sym: "Shared", fqn: "app.Shared"} // visible, but no file:line selector
	m.graphSym = "Shared"
	m, cmd := applyMsg(m, annotationKey('a'))
	if cmd != nil || m.annotationOpen || !strings.Contains(m.statusMsg, "exact indexed symbol") {
		t.Fatalf("inexact Graph selection should be rejected: open=%v status=%q cmd=%v", m.annotationOpen, m.statusMsg, cmd != nil)
	}

	// An ambiguous Impact root (two definitions, no exact selector) is likewise
	// blocked instead of silently attaching to Locations[0].
	m.active = tabImpact
	m.impactSymbol = "Shared"
	m.impact.SetValue("Shared")
	m.impactRep = &app.ImpactReport{Symbol: "Shared", Found: true, Locations: []app.SymbolRef{
		{Symbol: "Shared", FQN: "app.Left.Shared", File: "left.go", StartLine: 4},
		{Symbol: "Shared", FQN: "app.Right.Shared", File: "right.go", StartLine: 4},
	}}
	m.syncFocus()
	m, _ = applyMsg(m, annotationKey('a'))
	if m.annotationOpen || !strings.Contains(m.statusMsg, "exact indexed symbol") {
		t.Fatalf("ambiguous Impact root should be rejected: open=%v status=%q", m.annotationOpen, m.statusMsg)
	}
}

func TestAnnotationShortcutAcrossExactDrillStates(t *testing.T) {
	exact := graphCenter{sym: "Shared", fqn: "app.Right.Shared", kind: graph.KindMethod, file: "right.go", line: 4}
	cases := []struct {
		name  string
		model func() Model
	}{
		{
			name: "Graph",
			model: func() Model {
				m := sized(t, 80, 24)
				m.loading, m.active, m.graphCenter, m.graphSym = false, tabGraph, exact, exact.sym
				return m
			},
		},
		{
			name: "Search",
			model: func() Model {
				return settledSearch(sized(t, 80, 24), []app.SemanticHit{{Symbol: exact.sym, FQN: exact.fqn, Kind: exact.kind, File: exact.file, StartLine: exact.line}}, 0)
			},
		},
		{
			name: "Impact",
			model: func() Model {
				m := sized(t, 80, 24)
				m.loading, m.active, m.impactSymbol = false, tabImpact, exact.sym
				m.impact.SetValue(exact.sym)
				m.impactRep = &app.ImpactReport{Symbol: exact.sym, Found: true, Selector: &app.SymbolSelector{File: exact.file, StartLine: exact.line, FQN: exact.fqn, Kind: exact.kind}}
				m.syncFocus()
				return m
			},
		},
		{
			name: "Path",
			model: func() Model {
				m := sized(t, 80, 24)
				m.loading, m.active, m.pathFocus = false, tabPath, focusPathResult
				m.pathRep = &app.PathReport{Found: true, Path: []app.SymbolRef{{Symbol: exact.sym, FQN: exact.fqn, Kind: exact.kind, File: exact.file, StartLine: exact.line}}}
				return m
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := applyMsg(tc.model(), annotationKey('a'))
			if !m.annotationOpen || !sameGraphCenter(m.annotationCenter, exact) {
				t.Fatalf("%s did not open on the exact selection: open=%v center=%+v", tc.name, m.annotationOpen, m.annotationCenter)
			}
		})
	}
}

func TestAnnotationShortcutDoesNotStealEditorTyping(t *testing.T) {
	exactHit := app.SemanticHit{Symbol: "Shared", FQN: "app.Shared", Kind: graph.KindMethod, File: "shared.go", StartLine: 4}

	search := settledSearch(sized(t, 80, 24), []app.SemanticHit{exactHit}, 0)
	search.search.SetValue("dirty")
	search, _ = applyMsg(search, annotationKey('a'))
	if search.annotationOpen || search.search.Value() != "dirtya" {
		t.Fatalf("dirty Search input lost the letter a: open=%v value=%q", search.annotationOpen, search.search.Value())
	}

	impact := sized(t, 80, 24)
	impact.loading, impact.active, impact.impactSymbol = false, tabImpact, "Shared"
	impact.impact.SetValue("dirty")
	impact.impactRep = &app.ImpactReport{Symbol: "Shared", Found: true, Selector: &app.SymbolSelector{File: "shared.go", StartLine: 4, FQN: "app.Shared", Kind: graph.KindMethod}}
	impact.syncFocus()
	impact, _ = applyMsg(impact, annotationKey('a'))
	if impact.annotationOpen || impact.impact.Value() != "dirtya" {
		t.Fatalf("dirty Impact input lost the letter a: open=%v value=%q", impact.annotationOpen, impact.impact.Value())
	}

	path := sized(t, 80, 24)
	path.loading, path.active, path.pathFocus = false, tabPath, focusPathFrom
	path.pathFromInput.SetValue("pkg.")
	path.syncFocus()
	path, _ = applyMsg(path, annotationKey('a'))
	if path.annotationOpen || path.pathFromInput.Value() != "pkg.a" {
		t.Fatalf("Path editor lost the letter a: open=%v value=%q", path.annotationOpen, path.pathFromInput.Value())
	}
}

func TestAnnotationSaveUsesCapturedFQNAndRefreshesExactSameName(t *testing.T) {
	m, left, right := studioSelectorFixture(t)
	m = settledSearch(m, []app.SemanticHit{
		{Symbol: left.sym, FQN: left.fqn, Kind: left.kind, File: left.file, StartLine: left.line},
		{Symbol: right.sym, FQN: right.fqn, Kind: right.kind, File: right.file, StartLine: right.line},
	}, 1)
	m, _ = applyMsg(m, annotationKey('a'))
	m = typeAnnotation(t, m, "right side only")

	updated, saveCmd := m.handleAnnotationKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = updated.(Model)
	if saveCmd == nil || !m.annotationSaving {
		t.Fatal("enter should start one asynchronous annotation save")
	}
	op := m.annotationOp
	// Double Enter and Esc are ignored after the non-cancellable write starts.
	updated, duplicate := m.handleAnnotationKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = updated.(Model)
	if duplicate != nil || m.annotationOp != op {
		t.Fatalf("double enter scheduled a duplicate save: op=%d want=%d", m.annotationOp, op)
	}
	updated, _ = m.handleAnnotationKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	m = updated.(Model)
	if !m.annotationOpen || !m.annotationSaving {
		t.Fatal("Esc must not pretend to cancel a write already in flight")
	}

	msg := saveCmd()
	m, _ = applyMsg(m, msg)
	if m.annotationOpen || m.annotationSaving || m.errMsg != "" || !strings.Contains(m.statusMsg, "saved") {
		t.Fatalf("save completion state = open:%v saving:%v status:%q err:%q", m.annotationOpen, m.annotationSaving, m.statusMsg, m.errMsg)
	}
	if len(m.searchHits[0].Annotations) != 0 || len(m.searchHits[1].Annotations) != 1 {
		t.Fatalf("same-name refresh crossed definitions: left=%+v right=%+v", m.searchHits[0].Annotations, m.searchHits[1].Annotations)
	}
	a := m.searchHits[1].Annotations[0]
	if a.Target != right.fqn || a.Target == left.fqn || a.Source != "studio" || a.Note != "right side only" {
		t.Fatalf("saved annotation lost exact target/provenance: %+v", a)
	}
	leftRep, err := m.service.NodeAnnotations(m.startDir, left.fqn)
	if err != nil {
		t.Fatal(err)
	}
	rightRep, err := m.service.NodeAnnotations(m.startDir, right.fqn)
	if err != nil {
		t.Fatal(err)
	}
	if len(leftRep.Annotations) != 0 || len(rightRep.Annotations) != 1 {
		t.Fatalf("stored same-name annotations = left:%+v right:%+v", leftRep.Annotations, rightRep.Annotations)
	}
	if out := m.render(); !strings.Contains(out, "right side only") || !strings.Contains(out, "app.Right.Shared") {
		t.Fatalf("refreshed exact annotation is not visible:\n%s", out)
	}
}

func TestAnnotationWhitespaceLimitAndSaveErrorRetry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEMAP_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	badData := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(badData, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEMAP_DATA", badData)
	sess := &app.Session{Config: config.DefaultConfig()}
	m := NewModel(context.Background(), sess, t.TempDir())
	m.loading, m.active = false, tabSearch
	m.search.SetValue("shared")
	m.searchQuery = "shared"
	m.searchHits = []app.SemanticHit{{Symbol: "Shared", FQN: "app.Shared", Kind: graph.KindMethod, File: "shared.go", StartLine: 4}}
	m.syncFocus()
	m, _ = applyMsg(m, annotationKey('a'))

	m.annotationInput.SetValue("   ")
	updated, cmd := m.handleAnnotationKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = updated.(Model)
	if cmd != nil || !m.annotationOpen || m.annotationSaving || m.annotationErr == "" {
		t.Fatalf("whitespace note should stay editable: open=%v saving=%v err=%q", m.annotationOpen, m.annotationSaving, m.annotationErr)
	}
	m.annotationInput.SetValue(strings.Repeat("x", 600))
	if got := len([]rune(m.annotationInput.Value())); got != 500 {
		t.Fatalf("annotation input length = %d, want 500-char bound", got)
	}
	m.annotationInput.SetValue("keep this draft")
	updated, cmd = m.handleAnnotationKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("valid note should schedule save")
	}
	m, _ = applyMsg(m, cmd())
	if !m.annotationOpen || m.annotationSaving || m.annotationInput.Value() != "keep this draft" || !m.annotationInput.Focused() {
		t.Fatalf("save error should retain a focused retry draft: open=%v saving=%v value=%q focus=%v", m.annotationOpen, m.annotationSaving, m.annotationInput.Value(), m.annotationInput.Focused())
	}
	if !strings.Contains(m.errMsg, "annotation failed") {
		t.Fatalf("save error not surfaced: %q", m.errMsg)
	}
}

func TestAnnotationRefreshFailureAndAsyncCorrelation(t *testing.T) {
	left := graphCenter{sym: "Shared", fqn: "app.Left.Shared", kind: graph.KindMethod, file: "left.go", line: 4}
	right := graphCenter{sym: "Shared", fqn: "app.Right.Shared", kind: graph.KindMethod, file: "right.go", line: 4}
	m := settledSearch(sized(t, 80, 24), []app.SemanticHit{
		{Symbol: left.sym, FQN: left.fqn, Kind: left.kind, File: left.file, StartLine: left.line},
		{Symbol: right.sym, FQN: right.fqn, Kind: right.kind, File: right.file, StartLine: right.line},
	}, 1)
	m.annotationOpen, m.annotationSaving, m.annotationCenter, m.annotationOp = true, true, right, 2
	m.annotationInput.SetValue("new draft")
	m.syncFocus()

	// An older operation must report success but leave the newer modal/draft and
	// current Right selection untouched.
	m, _ = applyMsg(m, annotationSavedMsg{
		op: 1, center: left, target: left.fqn, id: 10, matched: true,
		annotations: []graph.Annotation{{Target: left.fqn, Note: "left note"}},
	})
	if !m.annotationOpen || !m.annotationSaving || m.annotationInput.Value() != "new draft" || len(m.searchHits[1].Annotations) != 0 {
		t.Fatalf("stale async result overwrote current composer/selection: %+v", m)
	}

	// A post-write refresh failure is a saved warning, closes the matching
	// composer, and cannot be retried into a duplicate write.
	m, _ = applyMsg(m, annotationSavedMsg{
		op: 2, center: right, target: right.fqn, id: 11, matched: true,
		refreshErr: fakeErr("readback unavailable"),
	})
	if m.annotationOpen || m.annotationSaving || !strings.Contains(m.errMsg, "annotation saved, but refresh failed") {
		t.Fatalf("refresh failure should be a terminal saved warning: open=%v saving=%v err=%q", m.annotationOpen, m.annotationSaving, m.errMsg)
	}

	m.annotationOpen, m.annotationSaving, m.annotationCenter, m.annotationOp = true, true, right, 3
	m, _ = applyMsg(m, annotationSavedMsg{op: 3, center: right, target: right.fqn, id: 12, matched: false})
	if !strings.Contains(m.statusMsg, "dangling") || m.errMsg != "" {
		t.Fatalf("matched:false should warn without pretending the write failed: status=%q err=%q", m.statusMsg, m.errMsg)
	}
}

func TestAnnotationComposerFitsNarrowTerminals(t *testing.T) {
	for _, size := range [][2]int{{32, 12}, {40, 16}} {
		w, h := size[0], size[1]
		m := sized(t, w, h)
		m.loading = false
		m.annotationOpen = true
		m.annotationCenter = graphCenter{
			sym: "ExtremelyLongSameNamedMethod", fqn: "example.really.long.package.Type.ExtremelyLongSameNamedMethod",
			kind: graph.KindMethod, file: "a/very/long/path/to/source_file.go", line: 12345,
		}
		m.annotationInput.SetValue("a long note that scrolls safely inside the editor")
		m.annotationErr = "an intentionally long validation error that must not overflow the terminal"
		m.syncFocus()
		out := m.render()
		if got := lipgloss.Height(out); got != h {
			t.Errorf("composer at %dx%d height=%d, want %d", w, h, got, h)
		}
		for i, line := range strings.Split(out, "\n") {
			if got := lipgloss.Width(line); got > w {
				t.Errorf("composer at %dx%d line %d width=%d exceeds %d: %q", w, h, i, got, w, line)
			}
		}
		if !strings.Contains(out, "Add annotation") || !strings.Contains(out, "enter save") {
			t.Errorf("composer at %dx%d lost core affordances:\n%s", w, h, out)
		}
	}
}

func TestAnnotationDiscoverabilityInHelpAndFooter(t *testing.T) {
	m := settledSearch(sized(t, 120, 30), []app.SemanticHit{{
		Symbol: "Shared", FQN: "app.Shared", Kind: graph.KindMethod, File: "shared.go", StartLine: 4,
	}}, 0)
	if footer := m.footer(); !strings.Contains(footer, "a note") {
		t.Fatalf("settled Search footer should advertise annotations: %s", footer)
	}
	m.showHelp = true
	help := renderHelp()
	for _, want := range []string{"ctrl+o / a", "add a note", "non-cancellable"} {
		if !strings.Contains(help, want) {
			t.Fatalf("help should document %q:\n%s", want, help)
		}
	}
}
