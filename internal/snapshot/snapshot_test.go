package snapshot

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

const profile = "fake:fake:4:cosine"

func TestRoundTrip(t *testing.T) {
	g, err := graph.Open(filepath.Join(t.TempDir(), "graph.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	pid, err := g.UpsertProject("app", "/x", "go")
	if err != nil {
		t.Fatal(err)
	}

	// A tiny graph: a file node + two functions, a defines + a calls edge.
	fileID, _ := g.AddNode(&graph.Node{ProjectID: pid, FilePath: "a.go", Kind: graph.KindFile, Language: "go", StartLine: 1, EndLine: 9, SourceHash: "h"})
	helperID, _ := g.AddNode(&graph.Node{ProjectID: pid, FilePath: "a.go", Symbol: "Helper", FQN: "app.Helper", Kind: "function", Language: "go", StartLine: 3, EndLine: 3})
	runID, _ := g.AddNode(&graph.Node{ProjectID: pid, FilePath: "a.go", Symbol: "Run", FQN: "app.Run", Kind: "function", Language: "go", StartLine: 5, EndLine: 7})
	if _, err := g.AddEdge(fileID, helperID, graph.EdgeDefines, 1.0); err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddEdgeProv(runID, helperID, graph.EdgeCalls, 1.0, graph.ProvName); err != nil {
		t.Fatal(err)
	}
	if err := g.SetFileHash(pid, "a.go", "h"); err != nil {
		t.Fatal(err)
	}
	annID, err := g.AddAnnotation(pid, graph.Annotation{Kind: "node", Target: "app.Helper", Source: "note", Note: "the helper"})
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	m, err := Export(g, pid, "app", dir, profile, "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if m.Nodes != 3 || m.Edges != 2 || m.IndexState != 1 || m.Annotations != 1 {
		t.Fatalf("manifest = %+v, want 3 nodes / 2 edges / 1 index_state / 1 annotation", m)
	}

	// Mutate the project: add a stray node + remove the annotation, so import has
	// to both wipe the stray and restore the missing annotation.
	if _, err := g.AddNode(&graph.Node{ProjectID: pid, FilePath: "stray.go", Symbol: "Stray", Kind: "function", Language: "go", StartLine: 1, EndLine: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.DeleteAnnotation(pid, annID); err != nil {
		t.Fatal(err)
	}

	if _, err := Import(g, pid, "app", dir, profile); err != nil {
		t.Fatal(err)
	}

	// The stray node is gone; the snapshot's three nodes are restored.
	st, _ := g.Stats(pid)
	if st.Nodes != 3 {
		t.Errorf("after import nodes = %d, want 3 (stray wiped)", st.Nodes)
	}
	// The call edge survived id-remapping: Run still calls Helper.
	callers, _ := g.Callers(pid, "Helper")
	foundRun := false
	for _, n := range callers {
		if n.Symbol == "Run" {
			foundRun = true
		}
	}
	if !foundRun {
		t.Errorf("after import, Helper's callers should include Run, got %+v", callers)
	}
	// The annotation was restored from the snapshot (it had been deleted).
	if anns, _ := g.AllAnnotations(pid); len(anns) != 1 || anns[0].Note != "the helper" {
		t.Errorf("annotations after import = %+v, want 1 'the helper'", anns)
	}
	// index_state restored.
	if h, _ := g.FileHash(pid, "a.go"); h != "h" {
		t.Errorf("index_state file hash = %q, want h", h)
	}

	// A mismatched embedding profile is refused (never mix models).
	if _, err := Import(g, pid, "app", dir, "other:model:4:cosine"); err == nil {
		t.Errorf("import with a mismatched embedding profile should be refused")
	}

	// Re-import is idempotent for annotations (merge, not duplicate).
	if _, err := Import(g, pid, "app", dir, profile); err != nil {
		t.Fatal(err)
	}
	if anns, _ := g.AllAnnotations(pid); len(anns) != 1 {
		t.Errorf("re-import duplicated annotations: %+v", anns)
	}
}

func TestExportDeterministic(t *testing.T) {
	// Export the SAME logical graph with the two functions inserted in opposite
	// orders (so their DB ids differ); the serialized output must be byte-identical
	// — proving it's keyed on content + node position, not the volatile DB id (the
	// property fcheap's content-dedup relies on).
	export := func(firstZeta bool) string {
		g, err := graph.Open(filepath.Join(t.TempDir(), "g.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer g.Close()
		pid, _ := g.UpsertProject("app", "/x", "go")
		zeta := &graph.Node{ProjectID: pid, FilePath: "z.go", Symbol: "Zeta", Kind: "function", Language: "go", StartLine: 2, EndLine: 2}
		alpha := &graph.Node{ProjectID: pid, FilePath: "a.go", Symbol: "Alpha", Kind: "function", Language: "go", StartLine: 1, EndLine: 1}
		var zid, aid int64
		if firstZeta {
			zid, _ = g.AddNode(zeta)
			aid, _ = g.AddNode(alpha)
		} else {
			aid, _ = g.AddNode(alpha)
			zid, _ = g.AddNode(zeta)
		}
		if _, err := g.AddEdgeProv(zid, aid, graph.EdgeCalls, 1.0, graph.ProvName); err != nil { // Zeta calls Alpha
			t.Fatal(err)
		}
		dir := t.TempDir()
		if _, err := Export(g, pid, "app", dir, profile, "sha"); err != nil {
			t.Fatal(err)
		}
		nb, _ := os.ReadFile(filepath.Join(dir, fileNodes))
		eb, _ := os.ReadFile(filepath.Join(dir, fileEdges))
		return string(nb) + "|" + string(eb)
	}
	if export(true) != export(false) {
		t.Errorf("Export output depends on insertion order — not deterministic (breaks fcheap dedup)")
	}
}
