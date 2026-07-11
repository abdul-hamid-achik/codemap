package snapshot

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/embed"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
	"github.com/abdul-hamid-achik/codemap/internal/vector"
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
	if err := g.MarkCallGraphResolved(pid, "a.go", "go/types"); err != nil {
		t.Fatal(err)
	}
	annID, err := g.AddAnnotation(pid, graph.Annotation{Kind: "node", Target: "app.Helper", Source: "note", Note: "the helper"})
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	m, err := Export(g, nil, pid, "app", dir, profile, "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if m.Nodes != 3 || m.Edges != 2 || m.IndexState != 1 || m.CallGraphCoverage != 1 || m.Annotations != 1 {
		t.Fatalf("manifest = %+v, want 3 nodes / 2 edges / 1 index_state / 1 coverage / 1 annotation", m)
	}

	// Mutate the project: add a stray node + remove the annotation, so import has
	// to both wipe the stray and restore the missing annotation.
	if _, err := g.AddNode(&graph.Node{ProjectID: pid, FilePath: "stray.go", Symbol: "Stray", Kind: "function", Language: "go", StartLine: 1, EndLine: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.DeleteAnnotation(pid, annID); err != nil {
		t.Fatal(err)
	}
	if err := g.ClearCallGraphResolved(pid, "a.go"); err != nil {
		t.Fatal(err)
	}
	if err := g.MarkCallGraphResolved(pid, "stray.go", "lsp"); err != nil {
		t.Fatal(err)
	}

	if _, err := Import(g, nil, pid, "app", dir, profile); err != nil {
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
	coverage, err := g.ProjectCallGraphCoverage(pid)
	if err != nil {
		t.Fatal(err)
	}
	if len(coverage) != 1 || coverage[0].FilePath != "a.go" || coverage[0].Resolver != "go/types" {
		t.Errorf("call-graph coverage after import = %+v, want a.go via go/types", coverage)
	}

	// A mismatched embedding profile is refused (never mix models).
	if _, err := Import(g, nil, pid, "app", dir, "other:model:4:cosine"); err == nil {
		t.Errorf("import with a mismatched embedding profile should be refused")
	}
	// Re-import is idempotent for annotations (merge, not duplicate).
	if _, err := Import(g, nil, pid, "app", dir, profile); err != nil {
		t.Fatal(err)
	}
	if anns, _ := g.AllAnnotations(pid); len(anns) != 1 {
		t.Errorf("re-import duplicated annotations: %+v", anns)
	}
}

// TestImportLegacySnapshotWithoutCoverageArtifact proves the schema-v1 additive
// extension remains backward compatible. Old snapshots have neither the
// manifest count nor call_graph_coverage.jsonl; importing one succeeds and
// conservatively clears any current coverage rather than inventing precision.
func TestImportLegacySnapshotWithoutCoverageArtifact(t *testing.T) {
	g, err := graph.Open(filepath.Join(t.TempDir(), "graph.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	pid, _ := g.UpsertProject("app", "/x", "go")
	if _, err := g.AddNode(&graph.Node{
		ProjectID: pid, FilePath: "leaf.go", Symbol: "Leaf", FQN: "app.Leaf",
		Kind: graph.KindFunction, Language: "go", StartLine: 1, EndLine: 1, SourceHash: "h",
	}); err != nil {
		t.Fatal(err)
	}
	if err := g.SetFileHash(pid, "leaf.go", "h"); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if _, err := Export(g, nil, pid, "app", dir, profile, "legacy"); err != nil {
		t.Fatal(err)
	}
	// Zero coverage is omitted from the manifest; deleting the empty additive
	// artifact reproduces an export created before call-graph coverage existed.
	if err := os.Remove(filepath.Join(dir, fileCallGraph)); err != nil {
		t.Fatal(err)
	}
	if err := g.MarkCallGraphResolved(pid, "leaf.go", "go/types"); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(g, nil, pid, "app", dir, profile); err != nil {
		t.Fatalf("legacy snapshot import failed: %v", err)
	}
	resolved, err := g.CallGraphResolvedFiles(pid)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved) != 0 {
		t.Fatalf("legacy snapshot invented/retained precise coverage: %v", resolved)
	}
}

// TestImportRejectsTruncatedJsonl pins P1-17 (O62/O100): a snapshot whose
// jsonl rows disagree with its manifest counts must fail BEFORE the project
// is wiped, so the caller can reindex from scratch instead of inheriting a
// silently-corrupted index. Pre-fix the wipe ran first and the re-insert
// loop happened to read fewer rows than the manifest claimed — Import
// returned success and the project ended up half-restored.
func TestImportRejectsTruncatedJsonl(t *testing.T) {
	g, err := graph.Open(filepath.Join(t.TempDir(), "graph.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	pid, err := g.UpsertProject("app", "/x", "go")
	if err != nil {
		t.Fatal(err)
	}
	fileID, _ := g.AddNode(&graph.Node{ProjectID: pid, FilePath: "a.go", Kind: graph.KindFile, Language: "go", StartLine: 1, EndLine: 9, SourceHash: "h"})
	helperID, _ := g.AddNode(&graph.Node{ProjectID: pid, FilePath: "a.go", Symbol: "Helper", FQN: "app.Helper", Kind: "function", Language: "go", StartLine: 3, EndLine: 3})
	runID, _ := g.AddNode(&graph.Node{ProjectID: pid, FilePath: "a.go", Symbol: "Run", FQN: "app.Run", Kind: "function", Language: "go", StartLine: 5, EndLine: 7})
	_, _ = g.AddEdge(fileID, helperID, graph.EdgeDefines, 1.0)
	_, _ = g.AddEdgeProv(runID, helperID, graph.EdgeCalls, 1.0, graph.ProvName)
	_ = g.SetFileHash(pid, "a.go", "h")

	dir := t.TempDir()
	if _, err := Export(g, nil, pid, "app", dir, profile, "abc123"); err != nil {
		t.Fatal(err)
	}

	// Truncate nodes.jsonl by one row so the jsonl count disagrees with
	// the manifest. The Import must fail WITHOUT wiping the project.
	nodesPath := filepath.Join(dir, fileNodes)
	b, _ := os.ReadFile(nodesPath)
	if len(b) == 0 {
		t.Fatal("nodes.jsonl unexpectedly empty")
	}
	// Drop the last non-empty newline so we keep the file-node + Helper
	// rows but lose the Run row → 2 rows vs manifest's 3.
	cut := len(b) - 2 // drop trailing newline + something
	for cut > 0 && b[cut-1] != '\n' {
		cut--
	}
	if err := os.WriteFile(nodesPath, b[:cut], 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Import(g, nil, pid, "app", dir, profile); err == nil {
		t.Fatal("Import of a truncated snapshot must fail")
	}
	// The wipe MUST NOT have run — the project still has its three nodes.
	st, _ := g.Stats(pid)
	if st.Nodes != 3 {
		t.Errorf("truncated import wiped the project (nodes=%d, want 3) — pre-fix bug (O62/O100)", st.Nodes)
	}
}

// TestExportVectorMetaZeroed pins P1-17 (B55): the same logical vector set
// exported from two indexer runs (which assign different NodeIDs to the
// same symbol) must serialize to byte-identical vectors.jsonl, so fcheap
// can content-dedup. Pre-fix the volatile Meta.NodeID/Meta.Project broke
// the dedup promise.
func TestExportVectorMetaZeroed(t *testing.T) {
	g1, _ := graph.Open(filepath.Join(t.TempDir(), "g1.db"))
	defer g1.Close()
	v1, _ := vector.Open(":memory:", embed.EmbeddingProfile{Provider: "fake", Model: "fake", Dimensions: 4, Distance: "cosine"})
	defer v1.Close()
	pid1, _ := g1.UpsertProject("app", "/x", "go")
	// Single Helper node + one vector in run 1.
	hID1, _ := g1.AddNode(&graph.Node{ProjectID: pid1, FilePath: "a.go", Symbol: "Helper", FQN: "app.Helper", Kind: "function", Language: "go", StartLine: 3, EndLine: 3})
	_ = g1.SetFileHash(pid1, "a.go", "h")
	_, _ = v1.Insert([]float32{1, 0, 0, 0}, "Helper body", vector.NodeMeta{NodeID: hID1, Project: "app", File: "a.go", Symbol: "Helper", FQN: "app.Helper", Kind: "function", Language: "go", StartLine: 3, EndLine: 3})
	_ = v1.Sync()
	dir1 := t.TempDir()
	if _, err := Export(g1, v1, pid1, "app", dir1, profile, "s1"); err != nil {
		t.Fatal(err)
	}
	vecs1, _ := os.ReadFile(filepath.Join(dir1, fileVectors))

	// Second run: same logical data, different node id (fresh DB / autoinc).
	g2, _ := graph.Open(filepath.Join(t.TempDir(), "g2.db"))
	defer g2.Close()
	v2, _ := vector.Open(":memory:", embed.EmbeddingProfile{Provider: "fake", Model: "fake", Dimensions: 4, Distance: "cosine"})
	defer v2.Close()
	pid2, _ := g2.UpsertProject("app", "/x", "go")
	hID2, _ := g2.AddNode(&graph.Node{ProjectID: pid2, FilePath: "a.go", Symbol: "Helper", FQN: "app.Helper", Kind: "function", Language: "go", StartLine: 3, EndLine: 3})
	_ = g2.SetFileHash(pid2, "a.go", "h")
	_, _ = v2.Insert([]float32{1, 0, 0, 0}, "Helper body", vector.NodeMeta{NodeID: hID2, Project: "app", File: "a.go", Symbol: "Helper", FQN: "app.Helper", Kind: "function", Language: "go", StartLine: 3, EndLine: 3})
	_ = v2.Sync()
	dir2 := t.TempDir()
	if _, err := Export(g2, v2, pid2, "app", dir2, profile, "s1"); err != nil {
		t.Fatal(err)
	}
	vecs2, _ := os.ReadFile(filepath.Join(dir2, fileVectors))

	if string(vecs1) != string(vecs2) {
		t.Errorf("Export vectors.jsonl depends on volatile NodeID/Project (B55) — breaks fcheap dedup:\n%s\nvs\n%s", vecs1, vecs2)
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
		if _, err := Export(g, nil, pid, "app", dir, profile, "sha"); err != nil {
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

// TestVectorRoundTrip pins BD.2b: embeddings are exported and restored WITHOUT
// re-embedding, with their node ids remapped to the freshly-inserted nodes so
// search→node joins still resolve.
func TestVectorRoundTrip(t *testing.T) {
	g, err := graph.Open(filepath.Join(t.TempDir(), "g.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	v, err := vector.Open(":memory:", embed.EmbeddingProfile{Provider: "fake", Model: "fake", Dimensions: 4, Distance: "cosine"})
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	pid, _ := g.UpsertProject("app", "/x", "go")
	helperID, _ := g.AddNode(&graph.Node{ProjectID: pid, FilePath: "a.go", Symbol: "Helper", FQN: "app.Helper", Kind: "function", Language: "go", StartLine: 3, EndLine: 3})
	runID, _ := g.AddNode(&graph.Node{ProjectID: pid, FilePath: "a.go", Symbol: "Run", FQN: "app.Run", Kind: "function", Language: "go", StartLine: 5, EndLine: 5})
	if _, err := v.Insert([]float32{1, 0, 0, 0}, "Helper body", vector.NodeMeta{NodeID: helperID, Project: "app", File: "a.go", Symbol: "Helper", FQN: "app.Helper", Kind: "function", Language: "go", StartLine: 3, EndLine: 3}); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Insert([]float32{0, 1, 0, 0}, "Run body", vector.NodeMeta{NodeID: runID, Project: "app", File: "a.go", Symbol: "Run", FQN: "app.Run", Kind: "function", Language: "go", StartLine: 5, EndLine: 5}); err != nil {
		t.Fatal(err)
	}
	if err := v.Sync(); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	m, err := Export(g, v, pid, "app", dir, profile, "sha")
	if err != nil {
		t.Fatal(err)
	}
	if m.Vectors != 2 {
		t.Fatalf("manifest vectors = %d, want 2", m.Vectors)
	}

	// Import wipes + restores the graph (new node ids) AND the vectors.
	if _, err := Import(g, v, pid, "app", dir, profile); err != nil {
		t.Fatal(err)
	}
	if c, _ := v.CountByProject("app"); c != 2 {
		t.Errorf("vector count after import = %d, want 2", c)
	}
	// The restored Helper vector must point at the NEW Helper node id.
	newHelper, _ := g.FindNodesBySymbol(pid, "Helper")
	if len(newHelper) != 1 {
		t.Fatalf("expected 1 Helper node, got %d", len(newHelper))
	}
	recs, _ := v.IterByProject("app")
	foundRemapped := false
	for _, r := range recs {
		if r.Meta.Symbol == "Helper" && r.Meta.NodeID == newHelper[0].ID {
			foundRemapped = true
		}
	}
	if !foundRemapped {
		t.Errorf("restored Helper vector should point at new node id %d, got %+v", newHelper[0].ID, recs)
	}
	// And it's searchable.
	hits, _ := v.Search([]float32{1, 0, 0, 0}, 1, "app")
	if len(hits) != 1 || hits[0].Meta.Symbol != "Helper" {
		t.Errorf("search after restore = %+v, want Helper as the top hit", hits)
	}
}
