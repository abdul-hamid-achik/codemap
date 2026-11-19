package index

import (
	"context"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/config"
)

// TestMinifiedBundlesSkippedAndReported pins the vendored-bundle backstop:
// *.min.js is already excluded by the default walk excludes, but the
// minified-family check also covers .min.css / .min.mjs / .map and survives a
// project that replaces the default exclude list. A minified file under the
// byte cap is skipped (no nodes), listed in the report's minified field, and
// its hash recorded so staleness doesn't flag it as new; the sibling plain
// file still indexes.
func TestMinifiedBundlesSkippedAndReported(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "app.css", ".btn { color: red; }\n")
	writeFile(t, dir, "vendor.min.css", ".btn{color:red}.card{margin:0}")

	g, v := newStores(t)
	pid, _ := g.UpsertProject("min", dir, "css")
	cfg := config.DefaultConfig().Index
	cfg.Exclude = nil // simulate a project that dropped the default excludes
	ix := New(g, v, fakeEmbedder{dims: 4}, cfg)
	res, err := ix.IndexProject(context.Background(), pid, "min", dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Minified) != 1 || res.Minified[0] != "vendor.min.css" {
		t.Errorf("Minified = %v, want [vendor.min.css]", res.Minified)
	}
	nodes, err := g.ProjectNodes(pid)
	if err != nil {
		t.Fatal(err)
	}
	minSyms := 0
	for _, n := range nodes {
		if n.FilePath == "vendor.min.css" {
			minSyms++
		}
	}
	if minSyms != 0 {
		t.Errorf("vendor.min.css contributed %d nodes, want 0", minSyms)
	}
	btn, err := g.FindNodesBySymbol(pid, ".btn")
	if err != nil {
		t.Fatal(err)
	}
	sawApp := false
	for _, n := range btn {
		if n.FilePath == "app.css" {
			sawApp = true
		}
	}
	if !sawApp {
		t.Error("app.css selector btn missing")
	}
	// A second no-drift run must not re-flag the bundle as new (hash recorded).
	res2, err := ix.IndexProject(context.Background(), pid, "min", dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res2.Minified != nil {
		t.Errorf("second run Minified = %v, want nil (unchanged file hash-skipped)", res2.Minified)
	}
}

func TestIsMinifiedBundle(t *testing.T) {
	for _, yes := range []string{"a/b.min.js", "JQuery.MIN.JS", "x.min.mjs", "y.min.css", "z.min.js.map"} {
		if !isMinifiedBundle(yes) {
			t.Errorf("isMinifiedBundle(%q) = false, want true", yes)
		}
	}
	for _, no := range []string{"a/b.js", "b.min.ts", "bundle.js", "main.mjs"} {
		if isMinifiedBundle(no) {
			t.Errorf("isMinifiedBundle(%q) = true, want false", no)
		}
	}
}
