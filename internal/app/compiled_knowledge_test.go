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
