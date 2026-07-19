package app

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/index"
	"github.com/abdul-hamid-achik/codemap/internal/snapshot"
)

// installFcheapStub vendors a tiny POSIX-sh fcheap on PATH (mirroring the tvault /
// language-server stubs in secret_impact_test.go and context_planner_test.go) so
// the cache save→restore round-trip runs hermetically, without the real fcheap
// binary. The stub implements just the subcommands codemap shells out to —
// save / restore / drop / list — storing snapshot directories under the
// --stash-dir fcheapRun appends (which the test points at a temp dir via
// snapshot.FcheapStashDir). It returns the JSON shapes snapshot/fcheap.go parses:
// save → {"id":...}, restore → {"status":"restored","verified":true}.
func installFcheapStub(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	stub := filepath.Join(bin, "fcheap")
	script := `#!/bin/sh
# Hermetic fcheap stub for codemap cache tests. Stores each saved snapshot
# directory under --stash-dir keyed by a counter-derived stash id.
cmd="$1"
shift

# Locate "--stash-dir <dir>" anywhere in the args.
stashdir=""
prev=""
for a in "$@"; do
  if [ "$prev" = "--stash-dir" ]; then
    stashdir="$a"
  fi
  prev="$a"
done
if [ -z "$stashdir" ]; then
  stashdir="."
fi
mkdir -p "$stashdir" 2>/dev/null || true

case "$cmd" in
save)
  src="$1"
  n=$(cat "$stashdir/.count" 2>/dev/null)
  if [ -z "$n" ]; then n=0; fi
  n=$((n + 1))
  echo "$n" > "$stashdir/.count"
  id="stash-$n"
  mkdir -p "$stashdir/$id"
  cp -R "$src/." "$stashdir/$id/" || exit 1
  printf '{"id":"%s"}\n' "$id"
  ;;
restore)
  id="$1"
  to=""
  prev=""
  for a in "$@"; do
    if [ "$prev" = "--to" ]; then
      to="$a"
    fi
    prev="$a"
  done
  if [ -z "$to" ] || [ ! -d "$stashdir/$id" ]; then
    printf '{"status":"error","verified":false}\n'
    exit 1
  fi
  mkdir -p "$to" 2>/dev/null || true
  cp -R "$stashdir/$id/." "$to/" || exit 1
  printf '{"status":"restored","verified":true}\n'
  ;;
drop)
  id="$1"
  rm -rf "$stashdir/$id" 2>/dev/null || true
  printf '{}\n'
  ;;
list)
  printf '[]\n'
  ;;
*)
  exit 1
  ;;
esac
`
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestCacheSaveRestoreHitPathHermetic is the hermetic counterpart to the
// fcheap-gated TestCacheSaveRestoreDropHitPath: it exercises the SAME cache
// save→restore HIT round-trip but against a vendored fcheap stub, so it runs (not
// skips) in minimal CI where the real fcheap binary is absent. This is the C4
// coverage gap — the restore HIT path and the "index is usable afterward"
// guarantee were previously only reachable with the real binary installed.
func TestCacheSaveRestoreHitPathHermetic(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a small POSIX fcheap stub")
	}
	installFcheapStub(t)
	if !snapshot.FcheapAvailable() {
		t.Fatal("fcheap stub not found on PATH after install")
	}
	snapshot.FcheapStashDir = t.TempDir()
	t.Cleanup(func() { snapshot.FcheapStashDir = "" })

	svc, root, cleanup := setupCacheProject(t)
	defer cleanup()

	ctx := context.Background()
	if _, err := svc.Index(ctx, root, index.Options{}, false); err != nil {
		t.Fatalf("Index: %v", err)
	}
	g, err := svc.s.Graph()
	if err != nil {
		t.Fatal(err)
	}
	_, projectName, err := svc.resolveProject(root)
	if err != nil {
		t.Fatal(err)
	}
	p, err := g.GetProjectByName(projectName)
	if err != nil {
		t.Fatal(err)
	}
	nodesBefore, err := g.Stats(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if nodesBefore.Nodes == 0 {
		t.Fatal("index produced no nodes; nothing to cache")
	}
	// Seed precise coverage so the restore has something to carry back (proves the
	// snapshot round-trips call_graph_coverage, not just nodes).
	if err := g.MarkCallGraphResolved(p.ID, "main.go", "go/types"); err != nil {
		t.Fatal(err)
	}

	// 1. CacheSave against the stub must produce a real stash id + tree hash and
	//    record a pointer entry keyed by the working-tree hash.
	stashID, treeHash, err := svc.CacheSave(ctx, root)
	if err != nil {
		t.Fatalf("CacheSave: %v", err)
	}
	if stashID == "" {
		t.Fatal("CacheSave returned an empty stash id with the fcheap stub on PATH")
	}
	if treeHash == "" {
		t.Fatal("CacheSave returned an empty tree hash")
	}

	// Drop the coverage row so the DB no longer has it; the restore must bring it
	// back from the snapshot (otherwise a "hit" that lost coverage would pass).
	if err := g.ClearCallGraphResolved(p.ID, "main.go"); err != nil {
		t.Fatal(err)
	}

	// 2. CacheRestore on the UNCHANGED tree must HIT (restored=true) against the
	//    stub — the path that skips in minimal CI without real fcheap.
	restored, restoredID, err := svc.CacheRestore(ctx, root)
	if err != nil {
		t.Fatalf("CacheRestore: %v", err)
	}
	if !restored {
		t.Fatal("CacheRestore should hit on an unchanged tree with the fcheap stub present")
	}
	if restoredID != stashID {
		t.Errorf("restored stash id = %q, want %q", restoredID, stashID)
	}

	// 3. The restored index is usable: node count is preserved and the graph is
	//    queryable (the saved function symbol resolves), and the precise coverage
	//    survived the round-trip.
	nodesAfter, err := g.Stats(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if nodesAfter.Nodes != nodesBefore.Nodes {
		t.Errorf("node count after restore = %d, want %d (restore lost/added nodes)", nodesAfter.Nodes, nodesBefore.Nodes)
	}
	if found, _ := g.FindNodesBySymbol(p.ID, "main"); len(found) == 0 {
		t.Error("restored index is not queryable: symbol 'main' not found after restore")
	}
	coverage, err := g.ProjectCallGraphCoverage(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(coverage) != 1 || coverage[0].FilePath != "main.go" || coverage[0].Resolver != "go/types" {
		t.Fatalf("cache restore lost precise coverage: %+v", coverage)
	}

	// 4. Edit a file on disk WITHOUT reindexing → the working-tree hash drifts →
	//    CacheRestore must MISS (P0-01: the lookup key is disk-derived).
	writeGoFile(t, filepath.Join(root, "a.go"),
		"package a\n\nfunc main() { _ = 1 /* cache miss after edit */ }\n")
	restored, _, err = svc.CacheRestore(ctx, root)
	if err != nil {
		t.Fatalf("CacheRestore after edit: %v", err)
	}
	if restored {
		t.Error("CacheRestore should miss after a working-tree edit (P0-01)")
	}

	// 5. CacheDrop by stash id removes the entry; CacheList then reports none.
	dropped, err := svc.CacheDrop(ctx, root, stashID, false)
	if err != nil {
		t.Fatalf("CacheDrop: %v", err)
	}
	if dropped != 1 {
		t.Errorf("CacheDrop want 1, got %d", dropped)
	}
	rep, err := svc.CacheList(ctx, root, false)
	if err != nil {
		t.Fatalf("CacheList after drop: %v", err)
	}
	if len(rep.Entries) != 0 {
		t.Errorf("CacheList after drop want 0 entries, got %d", len(rep.Entries))
	}
}
