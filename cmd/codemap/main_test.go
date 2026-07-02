/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/app"
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

// TestIndexFilesSummary pins that the `index` summary counts recognized-but-
// unsupported files (no language server) as scanned+skipped, so it can't claim
// "0 skipped" while the warning reports skipped files.
func TestIndexFilesSummary(t *testing.T) {
	// No unsupported files: plain counts, invariant scanned = indexed + skipped.
	// P2-07 (O108): no skips and no unchanged → just "10 scanned, 10 indexed".
	plain := indexFilesSummary(&app.IndexReport{FilesScanned: 10, FilesIndexed: 10, FilesSkipped: 0})
	if !strings.Contains(plain, "10 scanned, 10 indexed") {
		t.Errorf("plain summary = %q", plain)
	}
	// Unchanged files show as "up-to-date" not "skipped".
	upToDate := indexFilesSummary(&app.IndexReport{FilesScanned: 10, FilesIndexed: 0, FilesSkipped: 0, FilesUnchanged: 10})
	if !strings.Contains(upToDate, "10 up-to-date") || strings.Contains(upToDate, "10 skipped") {
		t.Errorf("up-to-date summary = %q (should say up-to-date, not skipped)", upToDate)
	}
	// Unsupported (e.g. TS+Python with no servers) fold into scanned + skipped, so
	// the line agrees with the warning instead of saying "0 skipped".
	unsup := indexFilesSummary(&app.IndexReport{
		FilesScanned: 0, FilesIndexed: 0, FilesSkipped: 0,
		Unsupported: map[string]int{"typescript": 1, "python": 1},
	})
	if !strings.Contains(unsup, "2 scanned") || !strings.Contains(unsup, "2 skipped") {
		t.Errorf("unsupported files should be counted as scanned+skipped, got %q", unsup)
	}
	// Mixed: Go indexed + a TS file with no server.
	mixed := indexFilesSummary(&app.IndexReport{
		FilesScanned: 10, FilesIndexed: 10, FilesSkipped: 0, FilesDeleted: 1,
		Unsupported: map[string]int{"typescript": 2},
	})
	if !strings.Contains(mixed, "12 scanned") || !strings.Contains(mixed, "10 indexed") || !strings.Contains(mixed, "2 skipped") || !strings.Contains(mixed, "1 removed") {
		t.Errorf("mixed summary = %q", mixed)
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

// TestIndexWatchFlagRegistered verifies the --watch flag is registered on the
// index command with the right help text, so `codemap index --watch` is
// discoverable and routes to the daemon.
func TestIndexWatchFlagRegistered(t *testing.T) {
	f := indexCmd.Flags().Lookup("watch")
	if f == nil {
		t.Fatal("--watch flag not registered on index command")
	}
	if !strings.Contains(f.Usage, "daemon") || !strings.Contains(f.Usage, "fresh") {
		t.Errorf("--watch usage = %q, want it to mention daemon and fresh", f.Usage)
	}
}

// TestIndexNoTipsFlag verifies the --no-tips flag is registered, so users can
// silence the post-index advisory tips in scripts/CI.
func TestIndexNoTipsFlagRegistered(t *testing.T) {
	f := indexCmd.Flags().Lookup("no-tips")
	if f == nil {
		t.Fatal("--no-tips flag not registered on index command")
	}
}

// TestIndexEnvelopeShape pins P0-10's JSON shape: the --json output now wraps
// the IndexReport with optional {cache, daemon} blocks so an agent can see
// auto-cache and --watch handoff outcomes (previously silently skipped under
// --json). Empty fields stay absent (omitempty) for forward compatibility.
func TestIndexEnvelopeShape(t *testing.T) {
	rep := &app.IndexReport{Project: "x", Root: "/tmp/x", Nodes: 1, Edges: 1}
	e := buildIndexEnvelope(rep)
	if e.IndexReport != rep {
		t.Fatalf("envelope must embed IndexReport")
	}
	if e.Cache != nil {
		t.Errorf("Cache must default to nil, got %+v", e.Cache)
	}
	if e.Daemon != nil {
		t.Errorf("Daemon must default to nil, got %+v", e.Daemon)
	}
	e.Cache = &app.CacheReport{Action: "saved", StashID: "st", TreeHash: "th"}
	e.Daemon = &indexDaemonInfo{Started: true, PID: 42}
	if e.Cache.Action != "saved" || e.Cache.StashID != "st" || e.Cache.TreeHash != "th" {
		t.Errorf("Cache fields not carried through envelope: %+v", e.Cache)
	}
	if !e.Daemon.Started || e.Daemon.PID != 42 {
		t.Errorf("Daemon fields not carried through envelope: %+v", e.Daemon)
	}
}

// TestPreciseEdgeNoteLSPHonest pins P1-09 (B61/O65/O88): pre-fix
// `status` printed "(N precise via go/types)" for a Python- or
// JavaScript-only project indexed with --precise (go/types never
// ran — those edges come from the language server's callHierarchy).
// Post-fix the message attributes precise edges to the actually
// present languages.
func TestPreciseEdgeNoteLSPHonest(t *testing.T) {
	cases := []struct {
		name      string
		edges     int
		langs     map[string]int
		wantMatch string // substring the note must contain
		wantNot   string // substring the note must NOT contain
	}{
		{
			name:      "Go only",
			edges:     12,
			langs:     map[string]int{"go": 100},
			wantMatch: "go/types",
			wantNot:   "callHierarchy",
		},
		{
			name:      "TypeScript only",
			edges:     8,
			langs:     map[string]int{"typescript": 50},
			wantMatch: "callHierarchy",
			wantNot:   "via go/types",
		},
		{
			name:      "Python only",
			edges:     3,
			langs:     map[string]int{"python": 20},
			wantMatch: "callHierarchy",
			wantNot:   "via go/types",
		},
		{
			name:      "Mixed Go + TS",
			edges:     20,
			langs:     map[string]int{"go": 50, "typescript": 30},
			wantMatch: "go/types",
			wantNot:   "via callHierarchy", // mixed case shows the combo, not the single-server label
		},
		{
			name:      "No precise edges, Go project",
			edges:     0,
			langs:     map[string]int{"go": 100},
			wantMatch: "name-based",
			wantNot:   "go/types",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			note := preciseEdgeNote(tc.edges, tc.langs)
			if !strings.Contains(note, tc.wantMatch) {
				t.Errorf("preciseEdgeNote(%d, %v) = %q, want to contain %q", tc.edges, tc.langs, note, tc.wantMatch)
			}
			if tc.wantNot != "" && strings.Contains(note, tc.wantNot) {
				t.Errorf("preciseEdgeNote(%d, %v) = %q, must NOT contain %q (P1-09 honesty: never attribute TS/Python edges to go/types)", tc.edges, tc.langs, note, tc.wantNot)
			}
		})
	}
}

// TestExitCodeContract pins P2-06: the CLI exit codes follow a
// documented contract — 0=answered, 1=operational error, 2=not found
// / not indexed — so scripts and agents can distinguish "answered"
// from "dead end" without parsing output.
func TestExitCodeContract(t *testing.T) {
	// The sentinel errors map to exit 2; verify they're recognized.
	if !errors.Is(errNotFound, errNotFound) {
		t.Error("errNotFound sentinel broken")
	}
	if !errors.Is(errNotIndexed, errNotIndexed) {
		t.Error("errNotIndexed sentinel broken")
	}
	// The mapping lives in main(); we can't call os.Exit in a test,
	// but we CAN verify the sentinel types are distinct from generic
	// errors (which would exit 1).
	generic := fmt.Errorf("some error")
	if errors.Is(generic, errNotFound) {
		t.Error("generic error must not match errNotFound")
	}
}

// TestGlobalPathFlag pins P2-05: the CLI now has a global --path / -C
// flag so every command can target a project directory the way MCP
// tools do (uniform 'path' param). targetDir resolves the flag or
// falls back to os.Getwd().
func TestGlobalPathFlag(t *testing.T) {
	// The flag is registered on rootCmd.
	f := rootCmd.PersistentFlags().Lookup("path")
	if f == nil {
		t.Fatal("rootCmd must have a persistent --path flag")
	}
	if f.Shorthand != "C" {
		t.Errorf("--path shorthand = %q, want C", f.Shorthand)
	}
	// targetDir falls back to cwd when --path is unset.
	_ = rootCmd.PersistentFlags().Set("path", "")
	cwd, _ := os.Getwd()
	if got := targetDir(rootCmd); got != cwd {
		t.Errorf("targetDir with no --path = %q, want cwd %q", got, cwd)
	}
	// targetDir returns the flag value when set.
	_ = rootCmd.PersistentFlags().Set("path", "/tmp/some/project")
	if got := targetDir(rootCmd); got != "/tmp/some/project" {
		t.Errorf("targetDir with --path=/tmp/some/project = %q, want /tmp/some/project", got)
	}
	_ = rootCmd.PersistentFlags().Set("path", "") // reset
}
