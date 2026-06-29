package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/index"
)

// readOrderProj indexes a tiny program: main() calls helper(); Hub() is called by
// A() and B() (a 2-caller hub); A/B are exported roots with no callers.
func readOrderProj(t *testing.T) (*Service, string) {
	t.Helper()
	isolate(t)
	proj := t.TempDir()
	files := map[string]string{
		"main.go": "package main\n\nfunc helper() {}\n\nfunc main() {\n\thelper()\n}\n",
		"hub.go":  "package main\n\nfunc Hub() {}\n\nfunc A() { Hub() }\n\nfunc B() { Hub() }\n",
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(proj, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	svc := NewService(sess)
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}
	return svc, proj
}

func findEntry(es []ReadEntry, sym string) *ReadEntry {
	for i := range es {
		if es[i].Symbol == sym {
			return &es[i]
		}
	}
	return nil
}

func TestReadOrderRanksEntrypointFirst(t *testing.T) {
	svc, proj := readOrderProj(t)
	rep, err := svc.ReadOrder(proj, ReadOrderOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Indexed || len(rep.Entries) == 0 {
		t.Fatalf("expected ranked entries, got %+v", rep)
	}
	if rep.Entries[0].Symbol != "main" {
		t.Errorf("main() should rank first in the reading order, got %q", rep.Entries[0].Symbol)
	}
	if !rep.Entries[0].Entrypoint {
		t.Errorf("main() should be flagged as an entrypoint")
	}
	// Hub is reached by A and B → in-degree 2, surfaced as a central symbol.
	if h := findEntry(rep.Entries, "Hub"); h == nil {
		t.Errorf("the Hub should appear in the reading order: %+v", rep.Entries)
	} else if h.InDegree != 2 {
		t.Errorf("Hub in-degree = %d, want 2", h.InDegree)
	}
	// Ranks are dense and ascending from 1.
	for i, e := range rep.Entries {
		if e.Rank != i+1 {
			t.Errorf("entry %d has rank %d, want %d", i, e.Rank, i+1)
		}
	}
}

func TestReadOrderQueryFilters(t *testing.T) {
	svc, proj := readOrderProj(t)
	rep, err := svc.ReadOrder(proj, ReadOrderOpts{Query: "Hub"})
	if err != nil {
		t.Fatal(err)
	}
	if findEntry(rep.Entries, "Hub") == nil {
		t.Errorf("query 'Hub' should match Hub: %+v", rep.Entries)
	}
	if findEntry(rep.Entries, "main") != nil {
		t.Errorf("query 'Hub' should not return main: %+v", rep.Entries)
	}
}

func TestReadOrderTopCap(t *testing.T) {
	svc, proj := readOrderProj(t)
	rep, err := svc.ReadOrder(proj, ReadOrderOpts{Top: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Entries) != 1 || rep.Entries[0].Symbol != "main" {
		t.Errorf("--top 1 should yield only main, got %+v", rep.Entries)
	}
}

func TestReadOrderQueryNoMatch(t *testing.T) {
	svc, proj := readOrderProj(t)
	rep, err := svc.ReadOrder(proj, ReadOrderOpts{Query: "zzznomatch"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Entries) != 0 {
		t.Errorf("a no-match query should yield zero entries, got %d", len(rep.Entries))
	}
	if rep.Note == "" {
		t.Errorf("an empty ranking should carry an explanatory note")
	}
}

func TestReadOrderExcludesLeaves(t *testing.T) {
	svc, proj := readOrderProj(t)
	rep, err := svc.ReadOrder(proj, ReadOrderOpts{})
	if err != nil {
		t.Fatal(err)
	}
	// helper() is lowercase (not exported), in a non-entry file, called once → it
	// scores > 0 (it has a caller) so it MAY appear; but a callerless unexported
	// function would score 0 and be dropped. Assert the entrypoint/hub ranking holds
	// and no zero-score entry slipped in.
	for _, e := range rep.Entries {
		if e.Score <= 0 {
			t.Errorf("no entry should have score <= 0, got %+v", e)
		}
	}
}

func TestReadOrderUnindexed(t *testing.T) {
	isolate(t)
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	rep, err := NewService(sess).ReadOrder(t.TempDir(), ReadOrderOpts{})
	if err != nil {
		t.Fatalf("unindexed project must not error: %v", err)
	}
	if rep.Indexed || len(rep.Entries) != 0 {
		t.Errorf("unindexed → {indexed:false, entries:[]}, got %+v", rep)
	}
}
