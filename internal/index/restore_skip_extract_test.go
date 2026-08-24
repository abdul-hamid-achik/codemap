package index

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/extract"
	"github.com/abdul-hamid-achik/codemap/internal/extract/gosrc"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
	"github.com/abdul-hamid-achik/codemap/internal/snapshot"
)

// countingExtractor wraps a real extractor and counts ExtractFile invocations so
// a test can prove a code path did (or did not) re-run extraction. Language() is
// promoted from the embedded extractor, so it registers under the same language.
type countingExtractor struct {
	extract.Extractor
	calls *int64
}

func (c *countingExtractor) ExtractFile(relPath string, src []byte) (*extract.FileResult, error) {
	atomic.AddInt64(c.calls, 1)
	return c.Extractor.ExtractFile(relPath, src)
}

// TestRestoreSkipsExtraction pins the cache-restore fast path (C4): restoring a
// snapshot reproduces the index WITHOUT re-running extraction. It proves three
// things the fcheap-gated tests can't reach in minimal CI:
//
//  1. snapshot.Import (the restore app.CacheRestore calls) preserves the node
//     count — the restored index is structurally identical to the saved one.
//  2. The restore re-invokes the extractor ZERO times (the extractor call counter
//     is flat across Export→Import) — restore is pure graph insertion, not a
//     re-index in disguise.
//  3. The payoff: because Import restores the index_state content hashes, a
//     subsequent INCREMENTAL IndexProject re-extracts nothing — every file is
//     reported FilesUnchanged and FilesIndexed stays 0. This is the "skip
//     extract+embed" guarantee the cache exists for.
//
// The extractor counter is also asserted across the follow-up incremental
// index: the import-edges pass recovers specifiers with cheap scanners
// (gosrc.ImportSpecs) and must not call ExtractFile on hash-unchanged files.
func TestRestoreSkipsExtraction(t *testing.T) {
	g, err := graph.Open(filepath.Join(t.TempDir(), "graph.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	ix := New(g, nil, nil, config.IndexConfig{})
	var calls int64
	ix.Register(&countingExtractor{Extractor: gosrc.New(), calls: &calls})

	root := t.TempDir()
	files := map[string]string{
		"a.go": "package app\n\nfunc Helper() int { return 1 }\n",
		"b.go": "package app\n\nfunc Run() int { return Helper() }\n",
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	pid, err := g.UpsertProject("app", root, "go")
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	res, err := ix.IndexProject(ctx, pid, "app", root, Options{})
	if err != nil {
		t.Fatalf("IndexProject: %v", err)
	}
	if res.Nodes == 0 {
		t.Fatal("initial index produced no nodes")
	}
	nodesBefore := res.Nodes
	if atomic.LoadInt64(&calls) == 0 {
		t.Fatal("counting extractor was never invoked during the initial index")
	}

	// Export the index, then restore it the same way app.CacheRestore does
	// (snapshot.Import wipes the project and bulk-reinserts the snapshot).
	dir := t.TempDir()
	if _, err := snapshot.Export(g, nil, pid, "app", dir, "", "base-sha"); err != nil {
		t.Fatalf("Export: %v", err)
	}

	callsBeforeRestore := atomic.LoadInt64(&calls)
	if _, err := snapshot.Import(g, nil, pid, "app", dir, ""); err != nil {
		t.Fatalf("Import (restore): %v", err)
	}

	// 1. Node count is preserved by the restore.
	st, err := g.Stats(pid)
	if err != nil {
		t.Fatal(err)
	}
	if st.Nodes != nodesBefore {
		t.Errorf("node count after restore = %d, want %d (restore lost/added nodes)", st.Nodes, nodesBefore)
	}

	// 2. The restore re-invoked the extractor zero times.
	if got := atomic.LoadInt64(&calls); got != callsBeforeRestore {
		t.Errorf("extractor calls changed across restore: %d -> %d (restore must not re-extract)", callsBeforeRestore, got)
	}

	// 3. A subsequent incremental index re-extracts NOTHING: the restored
	//    index_state hashes match the unchanged files on disk, so indexFile's
	//    hash short-circuit fires for every file.
	res2, err := ix.IndexProject(ctx, pid, "app", root, Options{})
	if err != nil {
		t.Fatalf("incremental IndexProject after restore: %v", err)
	}
	if res2.FilesIndexed != 0 {
		t.Errorf("incremental index after restore reindexed %d files, want 0 (cache restore should let it skip)", res2.FilesIndexed)
	}
	if res2.FilesUnchanged != len(files) {
		t.Errorf("incremental index after restore: FilesUnchanged = %d, want %d (every restored file should be up-to-date)", res2.FilesUnchanged, len(files))
	}
	if got := atomic.LoadInt64(&calls); got != callsBeforeRestore {
		t.Errorf("incremental index after restore re-extracted: ExtractFile %d -> %d, want unchanged", callsBeforeRestore, got)
	}
	st2, err := g.Stats(pid)
	if err != nil {
		t.Fatal(err)
	}
	if st2.Nodes != nodesBefore {
		t.Errorf("node count after incremental index = %d, want %d (the no-op reindex must not change the restored graph)", st2.Nodes, nodesBefore)
	}
}
