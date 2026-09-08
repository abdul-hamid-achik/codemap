package app

import (
	"context"
	"os"
	"path/filepath"

	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/graph"
	"github.com/abdul-hamid-achik/codemap/internal/index"
)

// indexFixture indexes a tiny Go project (structure-only, no Ollama) and
// returns the project path and service.
func indexFixture(t *testing.T) (string, *Service, *Session) {
	t.Helper()
	isolate(t)

	proj := t.TempDir()
	write := func(name, src string) {
		if err := os.WriteFile(filepath.Join(proj, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("main.go", "package app\n\n// Run runs.\nfunc Run() { Helper() }\n\n// Helper helps.\nfunc Helper() {}\n")
	write("util.go", "package app\n\n// Extra does little.\nfunc Extra() { Helper() }\n")

	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	svc := NewService(sess)
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}
	return proj, svc, sess
}

func hasPath(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// TestStructuralManifestDelta pins the freshness delta contract: the manifest
// not only counts drifted files but names them, so a consumer can decide what
// to re-ingest instead of re-reading the whole export.
func TestStructuralManifestDelta(t *testing.T) {
	proj, svc, _ := indexFixture(t)

	// Drift the tree in all three directions.
	if err := os.WriteFile(filepath.Join(proj, "main.go"),
		[]byte("package app\n\nfunc Run() { Helper() }\n\nfunc Helper() {}\n\nfunc Added() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "new.go"),
		[]byte("package app\n\nfunc Fresh() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(proj, "util.go")); err != nil {
		t.Fatal(err)
	}

	rep, err := svc.StructuralManifest(proj)
	if err != nil {
		t.Fatal(err)
	}
	f := rep.Freshness
	if f.Fresh {
		t.Fatal("expected stale freshness after drift")
	}
	if f.Changed != 1 || len(f.ChangedFiles) != 1 || !hasPath(f.ChangedFiles, "main.go") {
		t.Errorf("changed delta = %d %v, want [main.go]", f.Changed, f.ChangedFiles)
	}
	if f.New != 1 || len(f.NewFiles) != 1 || !hasPath(f.NewFiles, "new.go") {
		t.Errorf("new delta = %d %v, want [new.go]", f.New, f.NewFiles)
	}
	if f.Deleted != 1 || len(f.DeletedFiles) != 1 || !hasPath(f.DeletedFiles, "util.go") {
		t.Errorf("deleted delta = %d %v, want [util.go]", f.Deleted, f.DeletedFiles)
	}

	// A fresh tree must omit the lists entirely (additive, empty when clean).
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}
	clean, err := svc.StructuralManifest(proj)
	if err != nil {
		t.Fatal(err)
	}
	if !clean.Freshness.Fresh {
		t.Fatalf("expected fresh after reindex, got %+v", clean.Freshness)
	}
	if len(clean.Freshness.ChangedFiles)+len(clean.Freshness.NewFiles)+len(clean.Freshness.DeletedFiles) != 0 {
		t.Errorf("fresh manifest should carry no delta files: %+v", clean.Freshness)
	}
}

// TestRetargetAnnotation pins the repair loop for the reindex-durable knowledge
// layer: rename a symbol, see the note go dangling, retarget it, see it resolve.
func TestRetargetAnnotation(t *testing.T) {
	proj, svc, _ := indexFixture(t)

	if _, _, _, err := svc.AnnotatePath(proj, "app.Run", "app.Helper", "note", "entry chain", ""); err != nil {
		t.Fatal(err)
	}
	id, _, _, err := svc.AnnotateNodeIdempotent(proj, "app.Extra", "vecgrep", "legacy note", "", "")
	if err != nil {
		t.Fatal(err)
	}

	// Rename app.Extra away: its annotation goes dangling, the path one stays.
	if err := os.WriteFile(filepath.Join(proj, "util.go"),
		[]byte("package app\n\nfunc Renamed() { Helper() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}
	all, err := svc.AllAnnotations(proj)
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Dangling) != 1 || all.Dangling[0] != id {
		t.Fatalf("dangling = %v, want [%d]", all.Dangling, id)
	}

	// Kind mismatch is refused.
	if _, err := svc.RetargetAnnotation(proj, id, graph.AnnotationPath, "app.Run -> app.Helper"); err == nil {
		t.Error("retargeting a node annotation with the path form must fail")
	}
	// Retarget to a live symbol resolves the note.
	matched, err := svc.RetargetAnnotation(proj, id, graph.AnnotationNode, "app.Run")
	if err != nil {
		t.Fatal(err)
	}
	if !matched {
		t.Error("retarget to app.Run should match an indexed symbol")
	}
	all, _ = svc.AllAnnotations(proj)
	if len(all.Dangling) != 0 {
		t.Errorf("dangling after retarget = %v, want none", all.Dangling)
	}
	for _, a := range all.Annotations {
		if a.ID == id && (a.Target != "app.Run" || a.Note != "legacy note") {
			t.Errorf("retargeted annotation = %+v, want target app.Run with note intact", a)
		}
	}

	// Missing id and unknown kind are errors.
	if _, err := svc.RetargetAnnotation(proj, 9999, graph.AnnotationNode, "app.Run"); err == nil {
		t.Error("expected error for missing annotation id")
	}
	if _, err := svc.RetargetAnnotation(proj, id, "weird", "x"); err == nil {
		t.Error("expected error for unknown annotation kind")
	}
}

// TestQueryHitFrequency pins the learning-from-use loop: successful searches
// bump per-symbol fan-in counters, and hotspots surface them for tie-breaking.
func TestQueryHitFrequency(t *testing.T) {
	proj, svc, _ := indexFixture(t)

	rep, err := svc.Hotspots(proj, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range rep.Hotspots {
		if h.QueryFrequency != 0 {
			t.Fatalf("unqueried hotspot %s carries frequency %d", h.Symbol, h.QueryFrequency)
		}
	}

	// Two name searches that surface Helper: distinct queries count once each.
	if _, err := svc.FindSymbols(proj, "Helper", 10); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.FindSymbols(proj, "helper", 10); err != nil {
		t.Fatal(err)
	}

	rep, err = svc.Hotspots(proj, 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, h := range rep.Hotspots {
		if h.Symbol == "Helper" {
			found = true
			if h.QueryFrequency != 2 {
				t.Errorf("Helper query_frequency = %d, want 2 (one per distinct query)", h.QueryFrequency)
			}
		}
	}
	if !found {
		t.Fatal("Helper missing from hotspots")
	}
}

// TestInconsistencies pins the three contradiction classes: dangling
// annotations, name edges surviving on precise-resolved files, and coverage
// rows for files with no indexed nodes. A clean fixture reports none of them.
func TestInconsistencies(t *testing.T) {
	proj, svc, sess := indexFixture(t)

	rep, err := svc.Inconsistencies(proj)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Dangling)+len(rep.NameEdges)+len(rep.OrphanedCoverage) != 0 {
		t.Fatalf("clean fixture reported inconsistencies: %+v", rep)
	}
	if rep.SchemaVersion != InconsistenciesSchemaVersion {
		t.Errorf("schema_version = %d, want %d", rep.SchemaVersion, InconsistenciesSchemaVersion)
	}

	g, err := sess.Graph()
	if err != nil {
		t.Fatal(err)
	}
	pid, err := g.GetProjectByName(rep.Project)
	if err != nil {
		t.Fatal(err)
	}

	// 1. A note whose symbol disappears goes dangling.
	id, _, _, err := svc.AnnotateNodeIdempotent(proj, "app.Extra", "vecgrep", "legacy", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(proj, "util.go")); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}
	rep, err = svc.Inconsistencies(proj)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Dangling) != 1 || rep.Dangling[0].ID != id {
		t.Errorf("dangling = %+v, want one entry for #%d", rep.Dangling, id)
	}

	// 2. A precise-resolved file with surviving name-based call edges.
	resolvedFile := "main.go"
	nodes, err := g.NodesInFile(pid.ID, resolvedFile)
	if err != nil {
		t.Fatal(err)
	}
	var src, dst int64
	for _, n := range nodes {
		if n.Symbol == "Run" {
			src = n.ID
		}
		if n.Symbol == "Helper" {
			dst = n.ID
		}
	}
	if src == 0 || dst == 0 {
		t.Fatalf("fixture nodes missing: src=%d dst=%d", src, dst)
	}
	if err := g.MarkCallGraphResolved(pid.ID, resolvedFile, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddEdgeProv(src, dst, graph.EdgeCalls, 1.0, graph.ProvName); err != nil {
		t.Fatal(err)
	}
	rep, err = svc.Inconsistencies(proj)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.NameEdges) != 1 || rep.NameEdges[0].FilePath != resolvedFile || rep.NameEdges[0].NameEdge < 1 {
		t.Errorf("name-edge contradictions = %+v, want one for %s", rep.NameEdges, resolvedFile)
	}

	// 3. Coverage recorded for a file with no indexed nodes.
	if err := g.MarkCallGraphResolved(pid.ID, "ghost.go", "test"); err != nil {
		t.Fatal(err)
	}
	rep, err = svc.Inconsistencies(proj)
	if err != nil {
		t.Fatal(err)
	}
	sawGhost := false
	for _, c := range rep.OrphanedCoverage {
		if c.FilePath == "ghost.go" {
			sawGhost = true
		}
	}
	if !sawGhost {
		t.Errorf("orphaned coverage = %+v, want ghost.go", rep.OrphanedCoverage)
	}

	// Retarget is the advertised repair for class 1.
	if _, err := svc.RetargetAnnotation(proj, id, graph.AnnotationNode, "app.Run"); err != nil {
		t.Fatal(err)
	}
	if rep, _ = svc.Inconsistencies(proj); len(rep.Dangling) != 0 {
		t.Errorf("dangling after retarget = %+v, want none", rep.Dangling)
	}
}

// TestReindexDeltaAttestation pins the selective re-ingestion anchor: a run
// that moves files attests from_fingerprint -> to_fingerprint with the exact
// changed/new/deleted lists; a no-op run clears the attestation; the very
// first index of a project attests nothing (no peer could have certified an
// empty index).
func TestReindexDeltaAttestation(t *testing.T) {
	proj, svc, _ := indexFixture(t)

	// First run already happened in the fixture; no peer certified anything
	// before it, so there must be no attestation.
	rep, err := svc.StructuralManifest(proj)
	if err != nil {
		t.Fatal(err)
	}
	if rep.ReindexDelta != nil {
		t.Fatalf("fresh project must not attest a delta: %+v", rep.ReindexDelta)
	}

	// No-op run: fingerprint unchanged, attestation stays absent.
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}
	if rep, _ = svc.StructuralManifest(proj); rep.ReindexDelta != nil {
		t.Fatalf("no-op run must not attest a delta: %+v", rep.ReindexDelta)
	}

	// Move all three directions, reindex, and read the attestation.
	if err := os.WriteFile(filepath.Join(proj, "main.go"),
		[]byte("package app\n\n// Run runs.\nfunc Run() { Helper() }\n\n// Helper helps.\nfunc Helper() {}\n\n// Added later.\nfunc Added() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "new.go"),
		[]byte("package app\n\n// Fresh arrives.\nfunc Fresh() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(proj, "util.go")); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}
	rep, err = svc.StructuralManifest(proj)
	if err != nil {
		t.Fatal(err)
	}
	d := rep.ReindexDelta
	if d == nil {
		t.Fatal("expected a reindex delta after a drifting run")
	}
	if d.FromFingerprint == "" || d.FromFingerprint == d.ToFingerprint {
		t.Fatalf("delta fingerprints invalid: %q -> %q", d.FromFingerprint, d.ToFingerprint)
	}
	if !hasPath(d.ChangedFiles, "main.go") || len(d.ChangedFiles) != 1 {
		t.Errorf("changed = %v, want [main.go]", d.ChangedFiles)
	}
	if !hasPath(d.NewFiles, "new.go") || len(d.NewFiles) != 1 {
		t.Errorf("new = %v, want [new.go]", d.NewFiles)
	}
	if !hasPath(d.DeletedFiles, "util.go") || len(d.DeletedFiles) != 1 {
		t.Errorf("deleted = %v, want [util.go]", d.DeletedFiles)
	}

	// A subsequent no-op run supersedes the attestation: the old
	// from_fingerprint no longer matches any export codemap would serve.
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}
	if rep, _ = svc.StructuralManifest(proj); rep.ReindexDelta != nil {
		t.Fatalf("stale attestation must be cleared after a no-op run: %+v", rep.ReindexDelta)
	}
}

// TestStructuralExportFilesFilter pins the v2 filtered-export contract: the
// filter scopes schema version, ordinals, and totals to the requested slice,
// echoes the canonical filter with a stable fingerprint, and keeps
// index_fingerprint identifying the FULL index.
func TestStructuralExportFilesFilter(t *testing.T) {
	proj, svc, _ := indexFixture(t)

	full, err := svc.StructuralExport(proj, StructuralExportOptions{Limit: MaxStructuralExportLimit})
	if err != nil {
		t.Fatal(err)
	}
	if full.SchemaVersion != StructuralExportSchemaVersion {
		t.Fatalf("unfiltered export = v%d, want v%d", full.SchemaVersion, StructuralExportSchemaVersion)
	}

	filtered, err := svc.StructuralExport(proj, StructuralExportOptions{
		Limit:       MaxStructuralExportLimit,
		FilesFilter: []string{"main.go", "./main.go", "util.go", "  "},
	})
	if err != nil {
		t.Fatal(err)
	}
	if filtered.SchemaVersion != StructuralExportFilteredSchemaVersion {
		t.Fatalf("filtered export = v%d, want v%d", filtered.SchemaVersion, StructuralExportFilteredSchemaVersion)
	}
	if got := filtered.FilesFilter; len(got) != 2 || got[0] != "main.go" || got[1] != "util.go" {
		t.Fatalf("files_filter = %v, want canonicalized sorted [main.go util.go]", got)
	}
	if filtered.FilesFilterFingerprint == "" {
		t.Fatal("filtered export must carry a filter fingerprint")
	}
	if filtered.IndexFingerprint != full.IndexFingerprint {
		t.Fatalf("filtered fingerprint must identify the full index: %q != %q",
			filtered.IndexFingerprint, full.IndexFingerprint)
	}
	// This filter covers every fixture file, so totals match; a narrow filter
	// must scope strictly below the full export.
	narrow, err := svc.StructuralExport(proj, StructuralExportOptions{
		Limit:       MaxStructuralExportLimit,
		FilesFilter: []string{"main.go"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if filtered.TotalRecords != full.TotalRecords {
		t.Errorf("total-covering filter total = %d, want %d", filtered.TotalRecords, full.TotalRecords)
	}
	if !(narrow.TotalRecords < full.TotalRecords) {
		t.Errorf("narrow filtered total %d must be smaller than full total %d", narrow.TotalRecords, full.TotalRecords)
	}
	for i, r := range filtered.Records {
		if r.Ordinal != i+1 {
			t.Errorf("filtered ordinal %d at position %d, want %d", r.Ordinal, i, i+1)
		}
		if r.File != "main.go" && r.File != "util.go" {
			t.Errorf("filtered export leaked record from %q", r.File)
		}
	}

	// A filter matching nothing is a valid, complete, empty v2 response.
	empty, err := svc.StructuralExport(proj, StructuralExportOptions{
		FilesFilter: []string{"nope/missing.go"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if empty.SchemaVersion != StructuralExportFilteredSchemaVersion || !empty.Complete || len(empty.Records) != 0 {
		t.Errorf("empty filter response = v%d complete=%t records=%d", empty.SchemaVersion, empty.Complete, len(empty.Records))
	}

	// Pagination over the filtered set: ordinals continue across pages.
	page1, err := svc.StructuralExport(proj, StructuralExportOptions{Limit: 1, FilesFilter: []string{"main.go", "util.go"}})
	if err != nil {
		t.Fatal(err)
	}
	if page1.Complete || page1.NextOffset != 1 {
		t.Fatalf("page1 complete=%t next=%d", page1.Complete, page1.NextOffset)
	}
	page2, err := svc.StructuralExport(proj, StructuralExportOptions{Offset: 1, Limit: MaxStructuralExportLimit, FilesFilter: []string{"main.go", "util.go"}})
	if err != nil {
		t.Fatal(err)
	}
	if page2.Records[0].Ordinal != 2 {
		t.Errorf("page2 first ordinal = %d, want 2", page2.Records[0].Ordinal)
	}
	if page2.FilesFilterFingerprint != page1.FilesFilterFingerprint {
		t.Error("filter fingerprint must be stable across pages")
	}
}
