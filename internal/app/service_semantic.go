package app

import (
	"context"

	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

// SemanticHit is one semantic-search result.
type SemanticHit struct {
	Symbol      string             `json:"symbol"`
	FQN         string             `json:"fqn,omitempty"`
	Kind        string             `json:"kind"`
	File        string             `json:"file"`
	StartLine   int                `json:"start_line"`
	EndLine     int                `json:"end_line"`
	Score       float32            `json:"score"`
	Signature   string             `json:"signature,omitempty"`
	Doc         string             `json:"doc,omitempty"`
	Annotations []graph.Annotation `json:"annotations,omitempty"` // notes/data pinned to this symbol
}

// enrichHitAnnotations attaches each hit's node-annotations in one bulk query,
// matching by the hit's FQN or symbol name.
func enrichHitAnnotations(g *graph.Store, projectID int64, hits []SemanticHit) {
	if len(hits) == 0 {
		return
	}
	all, err := g.AllAnnotations(projectID)
	if err != nil || len(all) == 0 {
		return
	}
	byTarget := map[string][]graph.Annotation{}
	for _, a := range all {
		if a.Kind == graph.AnnotationNode {
			byTarget[a.Target] = append(byTarget[a.Target], a)
		}
	}
	for i := range hits {
		seen := map[int64]bool{}
		var out []graph.Annotation
		for _, t := range []string{hits[i].FQN, hits[i].Symbol} {
			for _, a := range byTarget[t] {
				if !seen[a.ID] {
					seen[a.ID] = true
					out = append(out, a)
				}
			}
		}
		hits[i].Annotations = out
	}
}

// SemanticReport is returned by Semantic / FindSymbols / Search.
type SemanticReport struct {
	Query   string        `json:"query"`
	Project string        `json:"project"`
	Mode    string        `json:"mode"`           // "semantic", "name", or "none" (no embeddings)
	Note    string        `json:"note,omitempty"` // why there are no results, when applicable
	Hits    []SemanticHit `json:"hits"`
}

// Semantic runs a meaning-based search over the project's embedded nodes.
func (svc *Service) Semantic(ctx context.Context, cwd, query string, topK int) (*SemanticReport, error) {
	if topK <= 0 {
		topK = 10
	}
	pid, name, found, err := svc.project(cwd)
	if err != nil {
		return nil, err
	}
	rep := &SemanticReport{Query: query, Project: name, Mode: "semantic", Hits: []SemanticHit{}}
	if !found {
		return rep, nil
	}

	// Structure-only projects have no vectors. Detect that up front so the answer is
	// an accurate "no embeddings" instead of an empty "no matches" — and so we skip
	// both a pointless embedder call (which would error if Ollama is down) and the
	// creation of an empty veclite file.
	if n, ok := svc.embeddedCount(name); ok && n == 0 {
		// No local embeddings — but the sibling vecgrep may have embedded this same
		// repo. Delegate to it and map its hits back onto the graph (FQN/kind), so
		// semantic search works with no codemap embed pass. Degrades to the note below.
		if hits := svc.semanticViaVecgrep(ctx, cwd, pid, query, topK); len(hits) > 0 {
			rep.Mode = "vecgrep"
			rep.Note = "semantic results via vecgrep (codemap has no local embeddings for this project)"
			rep.Hits = hits
			return rep, nil
		}
		rep.Mode = "none"
		rep.Note = "no embeddings for this project — run 'codemap index' with Ollama running, index this repo in vecgrep, or use 'codemap find' for name search"
		return rep, nil
	}

	vecs, err := svc.s.Embedder().Embed(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 {
		return rep, nil
	}
	vstore, err := svc.s.VectorsReadOnly()
	if err != nil {
		return nil, err
	}
	// Hybrid (vector + BM25 over symbol/fqn) fuses meaning with keyword matches —
	// e.g. a query that names a symbol gets a keyword boost while conceptual
	// queries still match by vector. Fall back to pure vector if the index has no
	// text index (older indexes) so search never hard-fails.
	hits, err := vstore.HybridSearch(vecs[0], query, topK, name)
	if err != nil {
		hits, err = vstore.Search(vecs[0], topK, name)
	}
	if err != nil {
		return nil, err
	}
	// Vector payloads don't store signatures or docstrings; resolve them from the
	// graph (one query) so semantic results are as self-contained as name search.
	var info map[string]graph.SymInfo
	if len(hits) > 0 {
		if g, gerr := svc.s.Graph(); gerr == nil {
			info, _ = g.SymbolInfoIndex(pid)
		}
	}
	for _, h := range hits {
		meta := info[h.Meta.FQN]
		rep.Hits = append(rep.Hits, SemanticHit{
			Symbol: h.Meta.Symbol, FQN: h.Meta.FQN, Kind: h.Meta.Kind, File: h.Meta.File,
			StartLine: h.Meta.StartLine, EndLine: h.Meta.EndLine, Score: h.Score,
			Signature: meta.Signature, Doc: meta.Doc,
		})
	}
	if g, gerr := svc.s.Graph(); gerr == nil {
		enrichHitAnnotations(g, pid, rep.Hits)
	}
	return rep, nil
}

// FindSymbols does a fast, offline name search over the indexed symbols (no
// embeddings needed).
func (svc *Service) FindSymbols(cwd, query string, limit int) (*SemanticReport, error) {
	if limit <= 0 {
		limit = 50
	}
	pid, name, found, err := svc.project(cwd)
	if err != nil {
		return nil, err
	}
	rep := &SemanticReport{Query: query, Project: name, Mode: "name", Hits: []SemanticHit{}}
	if !found {
		return rep, nil
	}
	g, _ := svc.s.Graph()
	nodes, err := g.SearchSymbols(pid, query, limit)
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		rep.Hits = append(rep.Hits, SemanticHit{
			Symbol: n.Symbol, FQN: n.FQN, Kind: n.Kind, File: n.FilePath,
			StartLine: n.StartLine, EndLine: n.EndLine, Signature: n.Signature, Doc: n.Docstring,
		})
	}
	enrichHitAnnotations(g, pid, rep.Hits)
	return rep, nil
}

// Search runs semantic search, falling back to a name search when embeddings
// are unavailable (e.g. Ollama not running, or a structure-only index) so the
// query always returns something useful.
func (svc *Service) Search(ctx context.Context, cwd, query string, topK int) (*SemanticReport, error) {
	rep, err := svc.Semantic(ctx, cwd, query, topK)
	if err == nil && rep != nil && len(rep.Hits) > 0 {
		return rep, nil
	}
	return svc.FindSymbols(cwd, query, topK)
}
