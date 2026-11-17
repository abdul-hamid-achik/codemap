package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/index"
)

// A structure-only index of a project that has never had local vectors must
// not open the global veclite store just to discover there is nothing to
// clean up. veclite loads its whole snapshot (every project's records) into
// memory on open, so the maintenance open dominated peak RSS (multi-GB on a
// large shared store) even for a no-op incremental. Here the store file is
// garbage: if the index path opens it, the run fails; if it skips it, the
// structural index succeeds.
func TestStructureOnlyIndexSkipsVectorStoreWhenProjectHasNoVectors(t *testing.T) {
	isolate(t)

	vecPath := config.VeclitePath()
	if err := os.MkdirAll(filepath.Dir(vecPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(vecPath, []byte("not a veclite database"), 0o644); err != nil {
		t.Fatal(err)
	}

	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "main.go"),
		[]byte("package app\n\nfunc Run() { Helper() }\n\nfunc Helper() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	svc := NewService(sess)
	if _, err := svc.Init(proj, false); err != nil {
		t.Fatal(err)
	}

	rep, err := svc.Index(context.Background(), proj, index.Options{}, false)
	if err != nil {
		t.Fatalf("structure-only index opened the (corrupt) vector store: %v", err)
	}
	if rep.Embedded {
		t.Error("expected structure-only (Embedded=false)")
	}
	if rep.Nodes < 3 {
		t.Errorf("nodes = %d, want >= 3", rep.Nodes)
	}
	if sess.vectorsMaintenance != nil {
		t.Error("maintenance vector store was opened for a project with no local vectors")
	}
}

// embeddedCount is the existence pre-flight behind cache save/export and
// branch snapshot/restore. It must answer from graph-side vec_id evidence:
// opening veclite read-only to count loads every project's records, which
// made the post-index cache auto-save cost gigabytes on a large store.
func TestEmbeddedCountDoesNotOpenVectorStore(t *testing.T) {
	isolate(t)

	vecPath := config.VeclitePath()
	if err := os.MkdirAll(filepath.Dir(vecPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(vecPath, []byte("not a veclite database"), 0o644); err != nil {
		t.Fatal(err)
	}

	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "main.go"),
		[]byte("package app\n\nfunc Run() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	svc := NewService(sess)
	ir, err := svc.Init(proj, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}

	if n, ok := svc.embeddedCount(ir.Project); !ok || n != 0 {
		t.Fatalf("embeddedCount = (%d, %v), want known 0 without opening veclite", n, ok)
	}
	if sess.vectorsRO != nil {
		t.Error("read-only vector store was opened just to count")
	}
}

// A no-op structure-only reindex of a project that HAS local vectors must skip
// the vector-store open: nothing was replaced or deleted, so there is nothing
// to clean, and opening veclite (which loads every project's records and
// re-serializes them on close) dominated no-op wall time on a large shared
// store. A run that WILL touch vector records — changed or deleted files, or a
// Reindex wipe — must still open the store for cleanup. The poisoned store
// file makes the open observable: skipped → run succeeds, attempted → it fails.
func TestStructureOnlyNoOpSkipsVectorOpenButChangedStillCleans(t *testing.T) {
	isolate(t)

	proj := t.TempDir()
	mainPath := filepath.Join(proj, "main.go")
	if err := os.WriteFile(mainPath,
		[]byte("package app\n\nfunc Run() { Helper() }\n\nfunc Helper() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	svc := NewService(sess)
	if _, err := svc.Init(proj, false); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}
	// Simulate a project with local vectors: mark a node's vec_id graph-side
	// (what every query joins on), then poison the legacy store file.
	g, err := sess.Graph()
	if err != nil {
		t.Fatal(err)
	}
	rep0, err := svc.Status(proj)
	if err != nil {
		t.Fatal(err)
	}
	p, err := g.GetProjectByName(rep0.Project)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := g.FindNodesBySymbol(p.ID, "Run")
	if err != nil || len(nodes) == 0 {
		t.Fatalf("Run node not found: %v %v", nodes, err)
	}
	if _, err := g.SetNodeVecIDs(p.ID, map[int64]string{nodes[0].ID: "vec-run"}); err != nil {
		t.Fatal(err)
	}
	vecPath := config.VeclitePath()
	if err := os.MkdirAll(filepath.Dir(vecPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(vecPath, []byte("not a veclite database"), 0o644); err != nil {
		t.Fatal(err)
	}

	// No-op structure-only reindex: nothing changed or deleted → the poisoned
	// store must never be opened.
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatalf("no-op structure-only reindex opened the (corrupt) vector store: %v", err)
	}
	if sess.vectorsMaintenance != nil {
		t.Error("maintenance vector store was opened for a no-op run")
	}

	// A changed file replaces its nodes — the old vectors must be cleaned, so
	// the open (and its failure on the poisoned file) is required.
	if err := os.WriteFile(mainPath,
		[]byte("package app\n\nfunc Run() { Helper() }\n\nfunc Helper() {}\n\nfunc Extra() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err == nil {
		t.Fatal("changed-file run should have opened the corrupt vector store for cleanup and failed")
	}

	// Restore a valid state: clear the vec_ids and rewrite the file so the
	// remaining assertions start from a clean, unchanged tree. A Reindex wipe
	// with vec_ids present must also open for cleanup.
	if err := g.ClearProjectVecIDsByName(rep0.Project); err != nil {
		t.Fatal(err)
	}
	if _, err := g.SetNodeVecIDs(p.ID, map[int64]string{nodes[0].ID: "vec-run"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mainPath,
		[]byte("package app\n\nfunc Run() { Helper() }\n\nfunc Helper() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatalf("re-sync after change failed unexpectedly: %v", err)
	}
	if _, err := svc.Index(context.Background(), proj, index.Options{Reindex: true}, false); err == nil {
		t.Fatal("Reindex wipe with vec_ids present should have opened the corrupt vector store and failed")
	}
}
