package app

import (
	"context"
	"strings"

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
	Selector    *SymbolSelector    `json:"selector,omitempty"`    // ready durable selector for chaining into context/source/impact (grep/symbol_at already carry one)
	MatchedIn   string             `json:"matched_in,omitempty"`  // "symbol"|"fqn"|"docstring" — no-embeddings fallback only (FindSymbols)
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
	Mode    string        `json:"mode"`             // "semantic", "vecgrep", "name", or "none"
	Fusion  string        `json:"fusion,omitempty"` // hybrid-search weighting used: "identifier", "natural_language", or "balanced" (empty when no fusion happened, e.g. a pure-vector fallback)
	Note    string        `json:"note,omitempty"`   // why there are no results, when applicable
	Hits    []SemanticHit `json:"hits"`
}

// fusionWeights resolves the vector/text weight pair for query given the
// resolved semantic.fusion config, and the profile name to surface in the
// report.
func (svc *Service) fusionWeights(query, fusionOverride string) (profile string, vectorWeight, textWeight float64) {
	cfg := svc.s.Config.Semantic
	fusion := fusionOverride
	if fusion == "" {
		fusion = cfg.Fusion
	}
	if fusion == "balanced" {
		return "balanced", 1.0, 1.0
	}
	switch classifyQuery(query) {
	case shapeIdentifier:
		w := cfg.FusionWeights.Identifier
		return "identifier", w.Vector, w.Text
	default:
		w := cfg.FusionWeights.NaturalLanguage
		return "natural_language", w.Vector, w.Text
	}
}

// Semantic runs a meaning-based search over the project's embedded nodes using
// the configured backend/fusion. Per-call overrides are available via
// SemanticWith (used by the MCP surface so an agent can force a backend/fusion
// without mutating the server's shared config).
func (svc *Service) Semantic(ctx context.Context, cwd, query string, topK int) (*SemanticReport, error) {
	return svc.SemanticWith(ctx, cwd, query, topK, "", "")
}

// SemanticWith is Semantic with explicit per-call backend/fusion overrides; an
// empty override falls back to the configured value.
func (svc *Service) SemanticWith(ctx context.Context, cwd, query string, topK int, backendOverride, fusionOverride string) (*SemanticReport, error) {
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
	backend := strings.ToLower(strings.TrimSpace(backendOverride))
	if backend == "" {
		backend = strings.ToLower(strings.TrimSpace(svc.s.Config.Semantic.Backend))
	}
	if backend == "" {
		backend = "fallback"
	}
	if backend == "vecgrep" {
		hits, searchErr := svc.semanticViaVecgrepStrict(ctx, cwd, pid, query, topK)
		if searchErr != nil {
			return nil, searchErr
		}
		rep.Mode = "vecgrep"
		rep.Note = "semantic results delegated to vecgrep (configured semantic owner)"
		if len(hits) == 0 {
			rep.Note = "vecgrep completed the semantic query with no matches"
		}
		rep.Hits = hits
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
		if backend == "fallback" {
			if hits := svc.semanticViaVecgrep(ctx, cwd, pid, query, topK); len(hits) > 0 {
				rep.Mode = "vecgrep"
				rep.Note = "semantic results via vecgrep (codemap has no local embeddings for this project)"
				rep.Hits = hits
				return rep, nil
			}
		}
		rep.Mode = "none"
		if backend == "local" {
			rep.Note = "no local embeddings for this project — run 'codemap index' with Ollama running, set semantic.backend to fallback/vecgrep, or use 'codemap find'"
		} else {
			rep.Note = "no embeddings for this project — run 'codemap index' with Ollama running, index this repo in vecgrep, or use 'codemap find' for name search"
		}
		return rep, nil
	}

	emb := svc.s.Embedder()
	vecs, err := func() ([][]float32, error) {
		if qe, ok := emb.(interface {
			QueryEmbed(context.Context, []string) ([][]float32, error)
		}); ok {
			return qe.QueryEmbed(ctx, []string{query})
		}
		return emb.Embed(ctx, []string{query})
	}()
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
	// queries still match by vector. The fusion weighting adapts to the query's
	// shape (identifier vs natural-language), or stays equal-weighted under
	// semantic.fusion: balanced. Fall back to pure vector if the index has no
	// text index (older indexes) so search never hard-fails.
	profile, vw, tw := svc.fusionWeights(query, fusionOverride)
	rep.Fusion = profile
	hits, err := vstore.HybridSearchWeighted(vecs[0], query, topK, name, vw, tw)
	if err != nil {
		hits, err = vstore.Search(vecs[0], topK, name)
		rep.Fusion = "" // pure-vector fallback path — no fusion happened
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
		hit := SemanticHit{
			Symbol: h.Meta.Symbol, FQN: h.Meta.FQN, Kind: h.Meta.Kind, File: h.Meta.File,
			StartLine: h.Meta.StartLine, EndLine: h.Meta.EndLine, Score: h.Score,
			Signature: meta.Signature, Doc: meta.Doc,
		}
		if h.Meta.File != "" {
			hit.Selector = &SymbolSelector{File: h.Meta.File, StartLine: h.Meta.StartLine, FQN: h.Meta.FQN, Kind: h.Meta.Kind}
		}
		rep.Hits = append(rep.Hits, hit)
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
	matches, err := g.SearchSymbols(pid, query, limit)
	if err != nil {
		return nil, err
	}
	for _, m := range matches {
		n := m.Node
		hit := SemanticHit{
			Symbol: n.Symbol, FQN: n.FQN, Kind: n.Kind, File: n.FilePath,
			StartLine: n.StartLine, EndLine: n.EndLine, Signature: n.Signature, Doc: n.Docstring,
			MatchedIn: m.MatchedIn,
		}
		if n.FilePath != "" {
			hit.Selector = selectorForNode(n)
		}
		rep.Hits = append(rep.Hits, hit)
	}
	enrichHitAnnotations(g, pid, rep.Hits)
	return rep, nil
}

// Search runs semantic search, falling back to a name search when embeddings
// are unavailable (e.g. Ollama not running, or a structure-only index) so the
// query always returns something useful.
func (svc *Service) Search(ctx context.Context, cwd, query string, topK int) (*SemanticReport, error) {
	rep, err := svc.Semantic(ctx, cwd, query, topK)
	explicitVecgrep := strings.EqualFold(strings.TrimSpace(svc.s.Config.Semantic.Backend), "vecgrep")
	if err == nil && rep != nil {
		if len(rep.Hits) > 0 || explicitVecgrep {
			// A valid zero-hit response from an explicit owner is authoritative;
			// falling through to names would silently switch retrieval semantics.
			return rep, nil
		}
	}
	// Search is the convenience semantic→name floor used by Explore and the
	// studio. Preserve that degradation for local/fallback mode, including an
	// unavailable embedder. An explicitly selected vecgrep owner is different:
	// its execution/contract errors are observable by design and must not be
	// hidden behind a name match from a different retrieval path.
	if err != nil && explicitVecgrep {
		return nil, err
	}
	return svc.FindSymbols(cwd, query, topK)
}
