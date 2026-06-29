package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/abdul-hamid-achik/codemap/internal/app"
)

func TestBarRevealFill(t *testing.T) {
	// reveal=1 → the full bar; n==max fills every cell.
	full := bar(2, 2, 8, 1.0)
	if got := strings.Count(full, "█"); got != 8 {
		t.Errorf("bar(2,2,8,1.0) = %d full blocks, want 8", got)
	}
	// reveal=0 → nothing filled yet (the start of the grow-in).
	if got := strings.Count(bar(2, 2, 8, 0.0), "█"); got != 0 {
		t.Errorf("bar at reveal 0 should have no full blocks, got %d", got)
	}
	// Half the reveal of a full-length bar ≈ half the cells.
	if got := strings.Count(bar(2, 2, 8, 0.5), "█"); got != 4 {
		t.Errorf("bar(2,2,8,0.5) = %d full blocks, want ~4", got)
	}
	// Width is preserved at every reveal so the column stays aligned.
	for _, r := range []float64{0, 0.3, 0.5, 1.0} {
		if w := lipgloss.Width(bar(3, 4, 10, r)); w != 10 {
			t.Errorf("bar width at reveal %.1f = %d, want 10", r, w)
		}
	}
}

func TestBarNonzeroSliver(t *testing.T) {
	const glyphs = "█▏▎▍▌▋▊▉"
	// A tiny but nonzero count always shows at least a sliver (the nonzero floor).
	if !strings.ContainsAny(bar(1, 200, 8, 1.0), glyphs) {
		t.Errorf("a nonzero count should render at least a partial-block sliver")
	}
	// A true zero renders nothing filled.
	if strings.ContainsAny(bar(0, 200, 8, 1.0), glyphs) {
		t.Errorf("a zero count should render no filled glyphs")
	}
}

// TestKickAnimSingleLoop pins the invariant that two concurrent reveals share ONE
// frame loop (no double 60fps ticking) and that `revealing` flips false exactly once.
func TestKickAnimSingleLoop(t *testing.T) {
	m := testModel()
	m.loading = false // simulate the initial load having completed (else busy() keeps the loop alive)
	if cmd := m.startMapReveal(); cmd == nil || !m.revealing {
		t.Fatal("starting the map reveal should kick the frame loop")
	}
	// Activating Metrics while the map loop runs must NOT schedule a second tick.
	if cmd := m.startMetricsReveal(); cmd != nil {
		t.Error("a second reveal while the loop is live must not schedule another tick")
	}
	if !m.mapActive || !m.metricsActive {
		t.Fatalf("both reveals should be active (map=%v metrics=%v)", m.mapActive, m.metricsActive)
	}
	// Drive frames; both settle and the loop stops exactly once.
	for i := 0; i < 600 && m.revealing; i++ {
		m.advanceAnim()
	}
	if m.revealing || m.mapActive || m.metricsActive {
		t.Errorf("after settling, all anim flags should clear (revealing=%v map=%v metrics=%v)", m.revealing, m.mapActive, m.metricsActive)
	}
}

func TestLoadingSpinner(t *testing.T) {
	m := testModel()
	m.loading = false
	if m.busy() {
		t.Error("a loaded, idle model is not busy")
	}
	m.statusMsg = "searching…"
	if !m.busy() {
		t.Error("a status ending in … means busy")
	}
	// The glyph advances with the frame counter.
	m.spinnerFrame = 0
	g0 := m.spinnerGlyph()
	m.spinnerFrame = spinnerFPSDiv
	if g0 == m.spinnerGlyph() {
		t.Error("the spinner glyph should advance with spinnerFrame")
	}
	// A key that kicks off an async op spins the footer (busy + a tick scheduled).
	// Wide terminal so the rich hint AND the right-aligned status both fit.
	ms := sized(t, 150, 30)
	ms, _ = applyMsg(ms, statusMsg{st: &app.StatusReport{Registered: true, Vectors: 1}})
	ms.active = tabSearch
	ms.search.SetValue("auth")
	ms, cmd := applyMsg(ms, tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if !ms.busy() {
		t.Errorf("enter on a fresh query should set a searching status, got %q", ms.statusMsg)
	}
	if cmd == nil {
		t.Error("a busy-triggering key should also schedule the spinner tick")
	}
	if out := ms.footer(); !strings.ContainsAny(out, string(spinnerFrames)) {
		t.Errorf("the footer should show a spinner glyph while busy:\n%s", out)
	}
}

func TestSparkline(t *testing.T) {
	if sparkline(nil) != "" {
		t.Error("empty input should yield an empty sparkline")
	}
	s := sparkline([]int{8, 4, 2, 1})
	rs := []rune(s)
	if len(rs) != 4 {
		t.Fatalf("sparkline of 4 values = %d cells, want 4", len(rs))
	}
	if rs[0] != '█' {
		t.Errorf("the max value should map to the tallest block █, got %q", string(rs[0]))
	}
	if rs[0] == rs[3] {
		t.Error("a descending distribution should not render all-equal cells")
	}
}

func TestSortedValuesDescending(t *testing.T) {
	got := sortedValues(map[string]int{"a": 1, "b": 7, "c": 3})
	want := []int{7, 3, 1}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sortedValues = %v, want %v", got, want)
		}
	}
}

// TestMetricsRevealLifecycle: switching to Metrics resets the reveal to 0 and the
// spring drives it back to 1, then the frame loop stops (idle-cheap).
func TestMetricsRevealLifecycle(t *testing.T) {
	m := testModel()
	m.loading = false // post-initial-load steady state (else busy() keeps the loop alive)
	if m.metricsReveal != 1 {
		t.Fatalf("a fresh model should show full bars (reveal=1), got %v", m.metricsReveal)
	}
	// Press "2" → Metrics; the reveal restarts and a frame tick is scheduled.
	m, cmd := applyMsg(m, tea.KeyPressMsg(tea.Key{Text: "2", Code: '2'}))
	if m.active != tabMetrics {
		t.Fatalf("'2' should switch to Metrics, got %v", m.active)
	}
	if !m.revealing || m.metricsReveal != 0 {
		t.Fatalf("activating Metrics should restart the reveal (revealing=%v reveal=%v)", m.revealing, m.metricsReveal)
	}
	if cmd == nil {
		t.Fatal("activating Metrics should schedule an animation frame")
	}
	// Drive frames until the spring settles (bounded so a stuck spring fails loudly).
	settled := false
	for i := 0; i < 600; i++ {
		var c tea.Cmd
		m, c = applyMsg(m, animTickMsg{})
		if !m.revealing {
			settled = true
			if c != nil {
				t.Error("a settled animation should stop scheduling frames")
			}
			break
		}
	}
	if !settled {
		t.Fatalf("reveal never settled (reveal=%v vel=%v)", m.metricsReveal, m.metricsVel)
	}
	if absf(1-m.metricsReveal) > 0.01 {
		t.Errorf("settled reveal should be ~1, got %v", m.metricsReveal)
	}
}
