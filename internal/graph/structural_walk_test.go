package graph

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestWalkProjectStructuralSymbolsIsOrderedAndExcludesFiles(t *testing.T) {
	g, err := Open(filepath.Join(t.TempDir(), "graph.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = g.Close() })
	pid, err := g.UpsertProject("walk", t.TempDir(), "go")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []Node{
		{ProjectID: pid, FilePath: "z.go", Kind: KindFile, Language: "go", StartLine: 1, EndLine: 4, SourceHash: "z"},
		{ProjectID: pid, FilePath: "z.go", Symbol: "Zed", FQN: "walk.Zed", Kind: KindFunction, Language: "go", StartLine: 3, EndLine: 3, SourceHash: "z"},
		{ProjectID: pid, FilePath: "a.go", Symbol: "Beta", FQN: "walk.Beta", Kind: KindFunction, Language: "go", StartLine: 5, EndLine: 5, SourceHash: "a"},
		{ProjectID: pid, FilePath: "a.go", Symbol: "Alpha", FQN: "walk.Alpha", Kind: KindFunction, Language: "go", StartLine: 3, EndLine: 3, SourceHash: "a"},
	} {
		n := n
		if _, err := g.AddNode(&n); err != nil {
			t.Fatal(err)
		}
	}

	var got []string
	if err := g.WalkProjectStructuralSymbols(pid, func(n Node) error {
		got = append(got, n.FQN)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want := []string{"walk.Alpha", "walk.Beta", "walk.Zed"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("walk order = %v, want %v", got, want)
	}

	sentinel := errors.New("stop")
	err = g.WalkProjectStructuralSymbols(pid, func(Node) error { return sentinel })
	if !errors.Is(err, sentinel) || !strings.Contains(err.Error(), "visit structural symbol") {
		t.Fatalf("callback error = %v, want wrapped sentinel", err)
	}
}

func TestWalkProjectStructuralSymbolsUsesPortablePathOrder(t *testing.T) {
	g, err := Open(filepath.Join(t.TempDir(), "graph.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = g.Close() })
	pid, err := g.UpsertProject("portable-walk", t.TempDir(), "go")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []Node{
		// Raw SQLite ordering puts '/' before '\\'. Contract ordering treats
		// both as separators, so a/alpha.go must precede a/zeta.go on every OS.
		{ProjectID: pid, FilePath: "a/zeta.go", Symbol: "Zeta", FQN: "walk.Zeta", Kind: KindFunction, Language: "go", StartLine: 1, EndLine: 1, SourceHash: "z"},
		{ProjectID: pid, FilePath: `a\alpha.go`, Symbol: "Alpha", FQN: "walk.Alpha", Kind: KindFunction, Language: "go", StartLine: 1, EndLine: 1, SourceHash: "a"},
	} {
		n := n
		if _, err := g.AddNode(&n); err != nil {
			t.Fatal(err)
		}
	}

	var got []string
	if err := g.WalkProjectStructuralSymbols(pid, func(n Node) error {
		got = append(got, CanonicalStructuralPath(n.FilePath))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want := []string{"a/alpha.go", "a/zeta.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("portable walk order = %v, want %v", got, want)
	}
	if got := CanonicalStructuralPath(`pkg\nested\target.go`); got != "pkg/nested/target.go" {
		t.Fatalf("canonical legacy path = %q", got)
	}
}
