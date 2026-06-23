package vector

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/embed"
)

func prof(dims int) embed.EmbeddingProfile {
	return embed.EmbeddingProfile{Provider: "ollama", Model: "nomic-embed-text", Dimensions: dims, Distance: "cosine"}
}

func openMem(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:", prof(3))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestInsertSearch(t *testing.T) {
	s := openMem(t)
	insert := func(id int64, vec []float32, sym string) {
		if _, err := s.Insert(vec, sym+" source", NodeMeta{NodeID: id, Project: "p", File: "a.go", Symbol: sym, FQN: "p." + sym, Kind: "function", Language: "go"}); err != nil {
			t.Fatal(err)
		}
	}
	insert(1, []float32{1, 0, 0}, "A")
	insert(2, []float32{0, 1, 0}, "B")
	insert(3, []float32{0, 0, 1}, "C")

	if s.Count() != 3 {
		t.Fatalf("count = %d, want 3", s.Count())
	}

	hits, err := s.Search([]float32{1, 0, 0}, 3, "p")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	if hits[0].NodeID != 1 {
		t.Errorf("top hit NodeID = %d, want 1", hits[0].NodeID)
	}
	if hits[0].Meta.Symbol != "A" || hits[0].Meta.FQN != "p.A" {
		t.Errorf("meta not round-tripped: %+v", hits[0].Meta)
	}
}

func TestProjectFilter(t *testing.T) {
	s := openMem(t)
	if _, err := s.Insert([]float32{1, 0, 0}, "x", NodeMeta{NodeID: 1, Project: "p", File: "a.go", Symbol: "X"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Insert([]float32{1, 0, 0}, "y", NodeMeta{NodeID: 2, Project: "q", File: "b.go", Symbol: "Y"}); err != nil {
		t.Fatal(err)
	}
	hits, err := s.Search([]float32{1, 0, 0}, 10, "p")
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if h.Meta.Project != "p" {
			t.Errorf("got project %q, want only p", h.Meta.Project)
		}
	}
	if len(hits) != 1 {
		t.Errorf("hits = %d, want 1 (project filter)", len(hits))
	}
}

func TestDeleteByFile(t *testing.T) {
	s := openMem(t)
	add := func(id int64, file string) {
		if _, err := s.Insert([]float32{1, 0, 0}, "c", NodeMeta{NodeID: id, Project: "p", File: file, Symbol: "S"}); err != nil {
			t.Fatal(err)
		}
	}
	add(1, "a.go")
	add(2, "a.go")
	add(3, "b.go")

	n, err := s.DeleteByFile("p", "a.go")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("deleted = %d, want 2", n)
	}
	if s.Count() != 1 {
		t.Errorf("count = %d, want 1", s.Count())
	}
}

func TestHybridSearch(t *testing.T) {
	s := openMem(t)
	if _, err := s.Insert([]float32{1, 0, 0}, "authenticate user with jwt token", NodeMeta{NodeID: 1, Project: "p", File: "auth.go", Symbol: "Authenticate", FQN: "p.Authenticate"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Insert([]float32{0, 1, 0}, "render html template", NodeMeta{NodeID: 2, Project: "p", File: "view.go", Symbol: "Render", FQN: "p.Render"}); err != nil {
		t.Fatal(err)
	}
	hits, err := s.HybridSearch([]float32{1, 0, 0}, "jwt", 5, "p")
	if err != nil {
		t.Fatalf("hybrid search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("hybrid search returned no hits")
	}
}

func TestProfileGuardPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.veclite")

	s, err := Open(path, prof(3))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Insert([]float32{1, 0, 0}, "c", NodeMeta{NodeID: 1, Project: "p", File: "a.go", Symbol: "A"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen with the same profile: OK.
	s2, err := Open(path, prof(3))
	if err != nil {
		t.Fatalf("reopen same profile: %v", err)
	}
	_ = s2.Close()

	// Reopen with an incompatible profile: IncompatibleError.
	bad := prof(3)
	bad.Model = "different-model"
	_, err = Open(path, bad)
	var ie *embed.IncompatibleError
	if !errors.As(err, &ie) {
		t.Fatalf("err = %v, want *embed.IncompatibleError", err)
	}
}
