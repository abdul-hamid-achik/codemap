package index

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/config"
)

// assertCacheMatchesFullRebuild fails unless the incrementally-maintained cache
// is identical to a from-scratch buildNodeIndex — the core P7 invariant that the
// cache never goes stale across incremental syncs.
func assertCacheMatchesFullRebuild(t *testing.T, ix *Indexer, pid int64) {
	t.Helper()
	if ix.cachedNI == nil {
		t.Fatal("cached node index is nil; expected it to be populated after IndexFiles")
	}
	fresh, err := ix.buildNodeIndex(pid)
	if err != nil {
		t.Fatal(err)
	}
	got, want := ix.cachedNI, fresh
	if len(got.nodes) != len(want.nodes) {
		t.Fatalf("cached nodes = %d, want %d (full rebuild)", len(got.nodes), len(want.nodes))
	}
	if len(got.fqnTo) != len(want.fqnTo) {
		t.Fatalf("cached fqnTo = %d entries, want %d", len(got.fqnTo), len(want.fqnTo))
	}
	for k, v := range want.fqnTo {
		if got.fqnTo[k] != v {
			t.Fatalf("cached fqnTo[%q] = %d, want %d", k, got.fqnTo[k], v)
		}
	}
	if len(got.dirOf) != len(want.dirOf) {
		t.Fatalf("cached dirOf = %d entries, want %d", len(got.dirOf), len(want.dirOf))
	}
	for k, v := range want.dirOf {
		if got.dirOf[k] != v {
			t.Fatalf("cached dirOf[%d] = %q, want %q", k, got.dirOf[k], v)
		}
	}
	if len(got.symTo) != len(want.symTo) {
		t.Fatalf("cached symTo = %d symbols, want %d", len(got.symTo), len(want.symTo))
	}
	for sym, wantIDs := range want.symTo {
		gotIDs := got.symTo[sym]
		if len(gotIDs) != len(wantIDs) {
			t.Fatalf("cached symTo[%q] = %d ids, want %d", sym, len(gotIDs), len(wantIDs))
		}
		gs := append([]int64(nil), gotIDs...)
		ws := append([]int64(nil), wantIDs...)
		sort.Slice(gs, func(i, j int) bool { return gs[i] < gs[j] })
		sort.Slice(ws, func(i, j int) bool { return ws[i] < ws[j] })
		for i := range ws {
			if gs[i] != ws[i] {
				t.Fatalf("cached symTo[%q] ids differ: got %v, want %v", sym, gs, ws)
			}
		}
	}
}

// TestCachedNodeIndexNeverStale exercises the incrementally-maintained node
// index (P7) across modify/add/delete syncs and an invalidation, asserting after
// each that the cache is byte-for-byte equivalent to a full rebuild. This
// includes the inbound-source expansion (editing a.go also re-indexes files that
// call into it), so the touched-file bookkeeping is covered end to end.
func TestCachedNodeIndexNeverStale(t *testing.T) {
	g, v := newStores(t)
	dir := setupProject(t)
	pid, _ := g.UpsertProject("app", dir, "go")

	ix := New(g, v, fakeEmbedder{dims: 4}, config.DefaultConfig().Index)
	if _, err := ix.IndexProject(context.Background(), pid, "app", dir, Options{}); err != nil {
		t.Fatal(err)
	}
	// IndexProject builds the node index but does not populate the incremental
	// cache; the first IndexFiles call builds it fully.
	if ix.cachedNI != nil {
		t.Fatal("cache should be nil before the first incremental IndexFiles")
	}

	// (1) Modify a file in place (adds a symbol) → first IndexFiles builds the
	// cache fully. b.go calls into a.go, so it is inbound-expanded and re-indexed
	// too — both must land in the cache consistently.
	writeFile(t, dir, "a.go", "package app\n\nfunc Helper() {}\n\nfunc Run() {\n\tHelper()\n}\n\nfunc Added() {}\n")
	if _, err := ix.IndexFiles(context.Background(), pid, "app", dir, []string{"a.go"}, Options{}); err != nil {
		t.Fatal(err)
	}
	assertCacheMatchesFullRebuild(t, ix, pid)

	// (2) Add a brand-new file → incremental refresh folds its nodes in.
	writeFile(t, dir, "c.go", "package app\n\nfunc Brand() { Run() }\n")
	if _, err := ix.IndexFiles(context.Background(), pid, "app", dir, []string{"c.go"}, Options{}); err != nil {
		t.Fatal(err)
	}
	assertCacheMatchesFullRebuild(t, ix, pid)

	// (3) Delete a file → incremental refresh drops its nodes.
	if err := os.Remove(filepath.Join(dir, "b.go")); err != nil {
		t.Fatal(err)
	}
	if _, err := ix.IndexFiles(context.Background(), pid, "app", dir, []string{"b.go"}, Options{}); err != nil {
		t.Fatal(err)
	}
	assertCacheMatchesFullRebuild(t, ix, pid)

	// (4) Invalidate (simulating a full reindex by another Indexer) → the cache
	// is dropped and the next IndexFiles rebuilds it from scratch, consistent.
	ix.InvalidateNodeIndex()
	if ix.cachedNI != nil {
		t.Fatal("InvalidateNodeIndex must drop the cache")
	}
	writeFile(t, dir, "a.go", "package app\n\nfunc Helper() {}\n\nfunc Run() {\n\tHelper()\n}\n")
	if _, err := ix.IndexFiles(context.Background(), pid, "app", dir, []string{"a.go"}, Options{}); err != nil {
		t.Fatal(err)
	}
	assertCacheMatchesFullRebuild(t, ix, pid)
}
