package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/index"
)

func TestExplorePromotesNameFallbackHitsToExactContexts(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	files := map[string]string{
		"go.mod":         "module example.com/explore\n\ngo 1.25\n",
		"left/left.go":   "package left\n\nfunc Shared() {}\nfunc CallLeft() { Shared() }\n",
		"right/right.go": "package right\n\nfunc Shared() {}\nfunc CallRight() { Shared() }\n",
	}
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	sess.Config.Vecgrep.Enabled = false
	svc := NewService(sess)
	if _, err := svc.Index(context.Background(), root, index.Options{NoLSP: true}, false); err != nil {
		t.Fatal(err)
	}

	rep, err := svc.Explore(context.Background(), root, "Shared", ExploreOptions{Seeds: 2, Edges: 1, Depth: 2})
	if err != nil {
		t.Fatal(err)
	}
	if rep.SchemaVersion != 1 || !rep.Indexed || rep.SearchMode != "name" {
		t.Fatalf("explore identity/search = %+v", rep)
	}
	if len(rep.Seeds) != 2 || len(rep.Contexts) != 2 || rep.NotJoined != 0 {
		t.Fatalf("explore cardinality = seeds:%d contexts:%d not_joined:%d", len(rep.Seeds), len(rep.Contexts), rep.NotJoined)
	}
	seen := map[string]bool{}
	for _, seed := range rep.Seeds {
		if seed.Selector == nil || seed.Selector.File == "" || seed.Selector.FQN == "" {
			t.Fatalf("seed lacks durable selector: %+v", seed)
		}
		seen[seed.Selector.File] = true
	}
	if !seen["left/left.go"] || !seen["right/right.go"] {
		t.Fatalf("duplicate names crossed files: %v", seen)
	}
	for _, ctxRep := range rep.Contexts {
		if !ctxRep.Found || ctxRep.Selector == nil || len(ctxRep.Definitions) != 1 {
			t.Fatalf("context is not exact: %+v", ctxRep)
		}
		if !ctxRep.Definitions[0].SourceOmitted || ctxRep.Definitions[0].Source != "" {
			t.Fatalf("explore must stay source-light: %+v", ctxRep.Definitions[0])
		}
		if len(ctxRep.Callers) > 1 || len(ctxRep.Callees) > 1 || len(ctxRep.References) > 1 || len(ctxRep.Tests) > 1 {
			t.Fatalf("edge cap not applied: %+v", ctxRep)
		}
	}
}

func TestExploreBoundsAndUnindexedContract(t *testing.T) {
	for _, opts := range []ExploreOptions{
		{Seeds: MaxExploreSeeds + 1},
		{Edges: MaxExploreEdges + 1},
		{Depth: 11},
	} {
		if _, err := normalizeExploreOptions(opts); err == nil {
			t.Fatalf("normalizeExploreOptions(%+v) should fail", opts)
		}
	}

	isolate(t)
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	rep, err := NewService(sess).Explore(context.Background(), t.TempDir(), "auth flow", ExploreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Indexed || rep.SchemaVersion != 1 || rep.Seeds == nil || rep.Contexts == nil {
		t.Fatalf("unindexed explore = %+v", rep)
	}
}

func TestBoundExploreContextRecomputesReferenceTruncationAndDropsMemories(t *testing.T) {
	rep := &ContextReport{
		References:      make([]ReferenceSite, 4),
		ReferencesTotal: 9,
		Memories:        []MemoryNote{{Content: "transient"}},
	}
	boundExploreContext(rep, 2)
	if len(rep.References) != 2 || rep.ReferencesTruncated != 7 {
		t.Fatalf("references len/truncated = %d/%d, want 2/7", len(rep.References), rep.ReferencesTruncated)
	}
	if len(rep.Memories) != 0 {
		t.Fatalf("explore should not carry transient memories: %+v", rep.Memories)
	}
}
