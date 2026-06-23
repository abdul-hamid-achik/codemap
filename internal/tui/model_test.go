package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/abdul-hamid-achik/codemap/internal/app"
	"github.com/abdul-hamid-achik/codemap/internal/config"
)

func testModel() Model {
	sess := &app.Session{Config: config.DefaultConfig()}
	return NewModel(context.Background(), sess, "")
}

func TestNewModelDefaults(t *testing.T) {
	m := testModel()
	if m.active != tabMetrics {
		t.Errorf("active = %v, want Metrics", m.active)
	}
	if !m.loading {
		t.Error("model should start in loading state")
	}
}

func TestTabCycling(t *testing.T) {
	m := testModel()
	m.active = tabGraph

	u, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	if got := u.(Model).active; got != tabMetrics {
		t.Errorf("after tab: %v, want Metrics", got)
	}
	u, _ = u.(Model).Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}))
	if got := u.(Model).active; got != tabGraph {
		t.Errorf("after shift+tab: %v, want Graph", got)
	}
}

func TestQuitKeyOnNonInputTab(t *testing.T) {
	m := testModel()
	m.active = tabMetrics
	if _, cmd := m.Update(tea.KeyPressMsg(tea.Key{Text: "q", Code: 'q'})); cmd == nil {
		t.Error("q should quit on a non-input tab")
	}
	if _, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl})); cmd == nil {
		t.Error("ctrl+c should always quit")
	}
}

func TestSearchTabAcceptsTyping(t *testing.T) {
	m := testModel()
	m.active = tabSearch
	u, _ := m.Update(tea.KeyPressMsg(tea.Key{Text: "j", Code: 'j'}))
	if v := u.(Model).search.Value(); v != "j" {
		t.Errorf("search input = %q, want j (typing routed to input)", v)
	}
}

func TestWindowSizeAndRender(t *testing.T) {
	m := testModel()
	u, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	out := u.(Model).render()
	if !strings.Contains(out, "codemap studio") {
		t.Errorf("render missing title:\n%s", out)
	}
	if !strings.Contains(out, "Metrics") {
		t.Error("render missing tab bar")
	}
}

func TestStatusMsgRendersMetrics(t *testing.T) {
	m := testModel()
	u, _ := m.Update(statusMsg{st: &app.StatusReport{
		Project: "demo", Registered: true, Nodes: 5, Edges: 3, Files: 2,
		Kinds: map[string]int{"function": 2, "type": 1}, Languages: map[string]int{"go": 5},
	}})
	mm := u.(Model)
	if mm.loading {
		t.Error("loading should clear after status")
	}
	mm.width = 100
	out := mm.render()
	if !strings.Contains(out, "nodes 5") {
		t.Errorf("metrics missing node count:\n%s", out)
	}
	if !strings.Contains(out, "function") {
		t.Errorf("metrics missing kinds bar chart:\n%s", out)
	}
}

func TestSemanticErrorShown(t *testing.T) {
	m := testModel()
	u, _ := m.Update(semanticMsg{query: "x", err: errFake})
	if u.(Model).errMsg == "" {
		t.Error("semantic error should be surfaced")
	}
}

var errFake = fakeErr("ollama unreachable")

type fakeErr string

func (e fakeErr) Error() string { return string(e) }
