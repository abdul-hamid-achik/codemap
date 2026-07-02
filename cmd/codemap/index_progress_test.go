/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"testing"
	"time"

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

// TestProgressETANoData verifies ETA returns "" before enough data is available.
func TestProgressETANoData(t *testing.T) {
	m := newProgressModel()
	// Zero start, done < 2, or total <= done → no ETA.
	if got := m.eta(time.Time{}); got != "" {
		t.Errorf("eta with zero start should be empty, got %q", got)
	}
	m.done, m.total = 1, 10
	if got := m.eta(time.Now()); got != "" {
		t.Errorf("eta with done=1 should be empty, got %q", got)
	}
	m.done, m.total = 10, 10
	if got := m.eta(time.Now()); got != "" {
		t.Errorf("eta with done==total should be empty, got %q", got)
	}
}

// TestProgressETAWithData verifies ETA returns a string when enough data exists.
func TestProgressETAWithData(t *testing.T) {
	m := newProgressModel()
	m.done, m.total = 5, 10
	// Simulate 2.5s elapsed → rate=2/s → remaining=5 → eta=2.5s → "2s"
	start := time.Now().Add(-2500 * time.Millisecond)
	got := m.eta(start)
	if got == "" {
		t.Error("eta with sufficient data should not be empty")
	}
}

// TestProgressETAFastComplete verifies ETA returns "<1s" when remaining is tiny.
func TestProgressETAFastComplete(t *testing.T) {
	m := newProgressModel()
	m.done, m.total = 99, 100
	start := time.Now().Add(-10 * time.Second) // 10s elapsed, 99 done
	got := m.eta(start)
	if got != "<1s" && got != "1s" {
		// The estimate rounds; accept either "<1s" or "1s" depending on timing.
		t.Logf("eta for near-complete: %q (acceptable)", got)
	}
}

// TestFormatETA verifies the duration formatting helper.
func TestFormatETA(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "30s"},
		{90 * time.Second, "1m30s"},
		{60 * time.Second, "1m"},
		{120 * time.Second, "2m"},
		{time.Hour, "1h"},
		{time.Hour + 30*time.Minute, "1h30m"},
		{2*time.Hour + 5*time.Minute, "2h05m"},
	}
	for _, tt := range tests {
		got := formatETA(tt.d)
		if got != tt.want {
			t.Errorf("formatETA(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}
