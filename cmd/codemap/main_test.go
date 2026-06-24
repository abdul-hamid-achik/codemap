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
