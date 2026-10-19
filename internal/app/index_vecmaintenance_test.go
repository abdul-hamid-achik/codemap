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
