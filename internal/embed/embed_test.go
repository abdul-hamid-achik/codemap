package embed

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProfileCompatible(t *testing.T) {
	a := EmbeddingProfile{Provider: "ollama", Model: "nomic-embed-text", Dimensions: 768, Distance: "cosine"}
	b := a
	if !a.Compatible(b) {
		t.Error("identical profiles should be compatible")
	}
	b.Model = "other"
	if a.Compatible(b) {
		t.Error("different model should be incompatible")
	}
	if err := CheckCompatible(a, b); err == nil {
		t.Error("CheckCompatible should error on mismatch")
	}
	if err := CheckCompatible(a, a); err != nil {
		t.Errorf("CheckCompatible should pass on match: %v", err)
	}
}

func TestOllamaEmbed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			t.Errorf("path = %q, want /api/embed", r.URL.Path)
		}
		var req embedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Model != "nomic-embed-text" {
			t.Errorf("model = %q", req.Model)
		}
		out := embedResponse{Embeddings: make([][]float32, len(req.Input))}
		for i := range req.Input {
			out.Embeddings[i] = []float32{0.1, 0.2, 0.3}
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	defer srv.Close()

	p := NewOllama(srv.URL, "nomic-embed-text", 0, "cosine")
	vecs, err := p.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 2 {
		t.Fatalf("got %d vectors, want 2", len(vecs))
	}
	if len(vecs[0]) != 3 {
		t.Errorf("vector dim = %d, want 3", len(vecs[0]))
	}
	if p.Dims != 3 {
		t.Errorf("Dims = %d, want inferred 3", p.Dims)
	}
	if got := p.Profile().Dimensions; got != 3 {
		t.Errorf("profile dims = %d, want 3", got)
	}
}

func TestOllamaEmbedEmpty(t *testing.T) {
	p := NewOllama("http://unused", "m", 0, "")
	vecs, err := p.Embed(context.Background(), nil)
	if err != nil || vecs != nil {
		t.Errorf("empty input: vecs=%v err=%v", vecs, err)
	}
}

func TestOllamaEmbedError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	p := NewOllama(srv.URL, "m", 0, "")
	if _, err := p.Embed(context.Background(), []string{"x"}); err == nil {
		t.Error("expected error on 500")
	}
}

func TestOllamaCountMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(embedResponse{Embeddings: [][]float32{{1}}})
	}))
	defer srv.Close()
	p := NewOllama(srv.URL, "m", 0, "")
	if _, err := p.Embed(context.Background(), []string{"a", "b"}); err == nil {
		t.Error("expected error when embedding count != input count")
	}
}

func TestOllamaAvailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Errorf("path = %q, want /api/tags", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"models":[{"name":"nomic-embed-text:latest"},{"name":"llama3:8b"}]}`))
	}))
	defer srv.Close()

	if err := NewOllama(srv.URL, "nomic-embed-text", 768, "cosine").Available(context.Background()); err != nil {
		t.Errorf("model present should be available: %v", err)
	}
	if err := NewOllama(srv.URL, "missing-model", 768, "cosine").Available(context.Background()); err == nil {
		t.Error("missing model should error")
	}
}
