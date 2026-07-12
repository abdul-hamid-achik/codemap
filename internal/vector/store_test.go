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

// TestHybridSearchWeighted pins F7: HybridSearchWeighted's per-channel
// weights actually move the ranking. Record 1 is the vector-nearest hit but
// has no BM25 overlap with the query text; record 2 is the BM25-best hit
// (heavy "jwt" repetition) but is far from the query vector. A strong text
// weight should rank record 2 above record 1, and vice versa for a strong
// vector weight — a direct, DB-free assertion through the codemap wrapper.
func TestHybridSearchWeighted(t *testing.T) {
	s := openMem(t)
	if _, err := s.Insert([]float32{1, 0, 0}, "render html template", NodeMeta{NodeID: 1, Project: "p", File: "view.go", Symbol: "Render", FQN: "p.Render"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Insert([]float32{0, 1, 0}, "jwt jwt jwt jwt jwt authentication token", NodeMeta{NodeID: 2, Project: "p", File: "auth.go", Symbol: "Authenticate", FQN: "p.Authenticate"}); err != nil {
		t.Fatal(err)
	}

	query := []float32{1, 0, 0}

	// veclite's RRF uses a fixed k=60 constant, so with only two candidate
	// records the rank-1-vs-rank-2 gap is small (1/61 vs 1/62); the weight
	// ratio has to be large enough to swing the fused ranking despite that.
	// 0.1/10.0 (100x) does so decisively in both directions.
	textHeavy, err := s.HybridSearchWeighted(query, "jwt", 5, "p", 0.1, 10.0)
	if err != nil {
		t.Fatalf("hybrid search (text-heavy): %v", err)
	}
	if len(textHeavy) < 2 {
		t.Fatalf("text-heavy search: got %d hits, want 2", len(textHeavy))
	}
	if textHeavy[0].NodeID != 2 {
		t.Errorf("text-heavy top hit NodeID = %d, want 2 (BM25-favored record)", textHeavy[0].NodeID)
	}

	vectorHeavy, err := s.HybridSearchWeighted(query, "jwt", 5, "p", 10.0, 0.1)
	if err != nil {
		t.Fatalf("hybrid search (vector-heavy): %v", err)
	}
	if len(vectorHeavy) < 2 {
		t.Fatalf("vector-heavy search: got %d hits, want 2", len(vectorHeavy))
	}
	if vectorHeavy[0].NodeID != 1 {
		t.Errorf("vector-heavy top hit NodeID = %d, want 1 (vector-nearest record)", vectorHeavy[0].NodeID)
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
