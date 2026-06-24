package main

import (
	"strings"
	"testing"
)

// TestPreciseEdgeNote pins the engine-aware status phrasing: precise edges are
// go/types for Go but callHierarchy for TypeScript, and a TS project without
// --precise has no call edges at all (not "name-based").
func TestPreciseEdgeNote(t *testing.T) {
	cases := []struct {
		name      string
		precise   int
		languages map[string]int
		want      string // substring that must appear
		absent    string // substring that must NOT appear ("" = no check)
	}{
		{"go precise", 1272, map[string]int{"go": 657}, "precise via go/types", "callHierarchy"},
		{"ts precise", 3, map[string]int{"typescript": 6}, "precise via callHierarchy", "go/types"},
		{"mixed precise", 10, map[string]int{"go": 4, "typescript": 6}, "go/types + callHierarchy", ""},
		{"go name-based", 0, map[string]int{"go": 657}, "name-based", "TypeScript"},
		{"ts no call graph", 0, map[string]int{"typescript": 6}, "no call graph yet", "name-based"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := preciseEdgeNote(c.precise, c.languages)
			if !strings.Contains(got, c.want) {
				t.Errorf("preciseEdgeNote(%d, %v) = %q, want substring %q", c.precise, c.languages, got, c.want)
			}
			if c.absent != "" && strings.Contains(got, c.absent) {
				t.Errorf("preciseEdgeNote(%d, %v) = %q, should not contain %q", c.precise, c.languages, got, c.absent)
			}
		})
	}
}

// TestCapList pins the impact list-capping: hubs can have hundreds of
// dependents, so the human-facing `impact` lists are bounded (the full set is in
// --json). Under the cap nothing is elided; over it, the remainder is reported.
func TestCapList(t *testing.T) {
	xs := []int{1, 2, 3, 4, 5}
	if shown, more := capList(xs, 10); len(shown) != 5 || more != 0 {
		t.Errorf("under cap: got %v more=%d, want all 5 and 0", shown, more)
	}
	if shown, more := capList(xs, 5); len(shown) != 5 || more != 0 {
		t.Errorf("exactly at cap: got %v more=%d, want all 5 and 0", shown, more)
	}
	shown, more := capList(xs, 3)
	if len(shown) != 3 || more != 2 {
		t.Errorf("over cap: got %v more=%d, want first 3 and 2 more", shown, more)
	}
	if shown[0] != 1 || shown[2] != 3 {
		t.Errorf("cap should keep the leading (nearest) items, got %v", shown)
	}
	if shown, more := capList([]int{}, 3); len(shown) != 0 || more != 0 {
		t.Errorf("empty: got %v more=%d, want empty and 0", shown, more)
	}
}

// TestSemanticSearchAlias guards the `search` alias for `semantic` — it matches
// the studio "Search" tab so users moving between the TUI and CLI aren't tripped.
func TestSemanticSearchAlias(t *testing.T) {
	var found bool
	for _, a := range semanticCmd.Aliases {
		if a == "search" {
			found = true
		}
	}
	if !found {
		t.Errorf("semantic command should alias \"search\", got aliases %v", semanticCmd.Aliases)
	}
}

// TestPreciseTips pins the language-aware --precise onboarding hints: Go gets the
// "refine name-based edges" tip; the LSP languages (which have no call graph
// without --precise) get the "no call graph yet" tip; a Go project without the
// toolchain gets no Go tip.
func TestPreciseTips(t *testing.T) {
	has := func(tips []string, sub string) bool {
		for _, tp := range tips {
			if strings.Contains(tp, sub) {
				return true
			}
		}
		return false
	}

	goTips := preciseTips(map[string]int{"go": 10}, true)
	if !has(goTips, "name-based") || len(goTips) != 1 {
		t.Errorf("Go project should get exactly the go/types tip, got %v", goTips)
	}
	if len(preciseTips(map[string]int{"go": 10}, false)) != 0 {
		t.Error("Go project without the go toolchain should get no tip (not actionable)")
	}

	ts := preciseTips(map[string]int{"typescript": 4}, true)
	if !has(ts, "no call graph for typescript") || has(ts, "name-based") {
		t.Errorf("TS-only project should get the LSP call-graph tip only, got %v", ts)
	}

	mixed := preciseTips(map[string]int{"go": 3, "javascript": 5, "python": 2}, true)
	if !has(mixed, "name-based") || !has(mixed, "javascript/python") {
		t.Errorf("mixed project should get both the Go tip and the JS/Python tip, got %v", mixed)
	}

	if len(preciseTips(map[string]int{}, true)) != 0 {
		t.Error("empty project should get no tips")
	}
}
