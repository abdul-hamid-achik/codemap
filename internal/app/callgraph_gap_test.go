package app

import (
	"strings"
	"testing"
)

// TestCallGraphGapWording pins that user-facing notes stay truthful now that
// TS/JS/Vue carry partial name-based call candidates: they say "partial" (and
// what is covered), Python still says "not available", and the machine
// classification (noNameBasedCallLang → call_graph "unresolved") is unchanged.
func TestCallGraphGapWording(t *testing.T) {
	for _, lang := range []string{"typescript", "javascript", "vue"} {
		head, state := callGraphGap(lang)
		if !strings.Contains(head, "partial") || !strings.Contains(head, "same-file calls and imported bindings") {
			t.Errorf("%s head = %q, want the partial-candidates wording", lang, head)
		}
		if strings.Contains(head, "not available") || strings.Contains(state, "unresolved (not absent)") {
			t.Errorf("%s wording must not claim there are no call edges: %q / %q", lang, head, state)
		}
		if !noNameBasedCallLang(lang) {
			t.Errorf("%s must keep the unresolved call_graph classification", lang)
		}
	}
	head, state := callGraphGap("python")
	if !strings.Contains(head, "not available for python") || !strings.Contains(state, "unresolved") {
		t.Errorf("python wording = %q / %q, want the not-available wording", head, state)
	}
}
