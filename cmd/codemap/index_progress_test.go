package main

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestProgressModelFileMsg(t *testing.T) {
	m := newProgressModel()
	u, cmd := m.Update(fileMsg{done: 5, total: 10, file: "internal/app/service.go"})
	pm := u.(progressModel)
	if pm.done != 5 || pm.total != 10 || pm.file != "internal/app/service.go" {
		t.Errorf("fileMsg not applied: %+v", pm)
	}
	if cmd == nil {
		t.Error("fileMsg should return a SetPercent animation cmd")
	}
	if v := pm.View(); v.Content == "" {
		t.Error("View should render a bar while in progress")
	}
}

func TestProgressModelDoneQuits(t *testing.T) {
	m := newProgressModel()
	u, cmd := m.Update(doneMsg{})
	pm := u.(progressModel)
	if !pm.finished {
		t.Error("doneMsg should mark finished")
	}
	if cmd == nil {
		t.Fatal("doneMsg should return a quit cmd")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("doneMsg cmd should yield tea.QuitMsg, got %T", cmd())
	}
	// View must clear when finished so the result summary prints on a clean line.
	if v := pm.View(); v.Content != "" {
		t.Errorf("finished View should be empty, got %q", v.Content)
	}
}

func TestProgressModelZeroTotalNoPanic(t *testing.T) {
	m := newProgressModel()
	u, _ := m.Update(fileMsg{done: 0, total: 0, file: "x"})
	_ = u.(progressModel).View() // must not divide-by-zero or panic
}

func TestProgressModelCtrlCQuits(t *testing.T) {
	m := newProgressModel()
	_, cmd := m.Update(tea.KeyPressMsg(tea.Key{Mod: tea.ModCtrl, Code: 'c'}))
	if cmd == nil {
		t.Fatal("ctrl+c should return a quit cmd")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("ctrl+c cmd should yield tea.QuitMsg, got %T", cmd())
	}
}
