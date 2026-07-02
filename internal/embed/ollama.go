package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// OllamaProvider embeds text via a local Ollama server's /api/embed endpoint.
type OllamaProvider struct {
	BaseURL  string
	Model    string
	Dims     int
	Distance string
	Client   *http.Client
	// dimsOnce guards the lazy-write of Dims from the first Embed
	// response. Pre-fix the write was a plain int store, which
	// -race flagged when two parallel indexer workers called Embed
	// concurrently with Dims=0 — both inferred the same value but
	// the unsynchronized write raced. sync.Once is enough: every
	// successful response carries the same per-model dimensionality,
	// so the value never needs to converge from multiple writers.
	dimsOnce sync.Once
}

// NewOllama returns an Ollama provider. dims may be 0 to infer from the first
// response; distance defaults to cosine.
func NewOllama(baseURL, model string, dims int, distance string) *OllamaProvider {
	if distance == "" {
		distance = "cosine"
	}
	return &OllamaProvider{
		BaseURL:  strings.TrimRight(baseURL, "/"),
		Model:    model,
		Dims:     dims,
		Distance: distance,
		Client:   &http.Client{Timeout: 60 * time.Second},
	}
}

// Profile implements Provider.
func (o *OllamaProvider) Profile() EmbeddingProfile {
	return EmbeddingProfile{Provider: "ollama", Model: o.Model, Dimensions: o.Dims, Distance: o.Distance}
}

type embedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

// Embed implements Provider, sending all texts in one batched request.
// Embed sends the texts to Ollama verbatim — deliberately WITHOUT nomic-embed-text's
// "search_document:" / "search_query:" task prefixes. The omission looks like a bug
// (Nomic's docs recommend the prefixes), but measured on nomic-embed-text the prefixes
// raise the similarity of UNRELATED pairs more than relevant ones, shrinking the gap
// that ranking depends on: relevant/unrelated cosine went 0.708/0.350 (sep 0.358)
// unprefixed → 0.729/0.418 (sep 0.311) prefixed. Less separation = worse retrieval, so
// we don't prefix. (codemap also fuses BM25 over symbol/fqn in HybridSearch, which a
// query-side prefix can't help.) If you switch default models, re-measure before adding
// prefixes.
func (o *OllamaProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	body, err := json.Marshal(embedRequest{Model: o.Model, Input: texts})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.BaseURL+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama embed request: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama embed: status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}

	var er embedResponse
	if err := json.Unmarshal(data, &er); err != nil {
		return nil, fmt.Errorf("decode ollama response: %w", err)
	}
	if len(er.Embeddings) != len(texts) {
		return nil, fmt.Errorf("ollama returned %d embeddings for %d inputs", len(er.Embeddings), len(texts))
	}
	// B47: lazy-write of Dims is now guarded by sync.Once so
	// parallel indexer workers calling Embed with Dims=0 don't
	// race the int store. Pre-fix this was a plain `o.Dims = ...`
	// which -race flagged.
	if o.Dims == 0 && len(er.Embeddings) > 0 && len(er.Embeddings[0]) > 0 {
		dim := len(er.Embeddings[0])
		o.dimsOnce.Do(func() { o.Dims = dim })
	}
	return er.Embeddings, nil
}

// Available checks that the Ollama server is reachable and the model is pulled.
func (o *OllamaProvider) Available(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.BaseURL+"/api/tags", nil)
	if err != nil {
		return err
	}
	resp, err := o.Client.Do(req)
	if err != nil {
		return fmt.Errorf("ollama not reachable at %s: %w", o.BaseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ollama /api/tags: status %d", resp.StatusCode)
	}
	var tags struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return err
	}
	for _, m := range tags.Models {
		if m.Name == o.Model || strings.HasPrefix(m.Name, o.Model+":") {
			return nil
		}
	}
	return fmt.Errorf("ollama model %q not pulled (run: ollama pull %s)", o.Model, o.Model)
}
