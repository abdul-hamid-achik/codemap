package app

import (
	"context"
	"fmt"
	"strconv"
)

const (
	// ExploreSchemaVersion is the stable major version of the compact
	// semantic-to-structure orientation contract.
	ExploreSchemaVersion = 1
	DefaultExploreSeeds  = 5
	MaxExploreSeeds      = 10
	DefaultExploreEdges  = 5
	MaxExploreEdges      = 20
)

// ExploreOptions bounds the semantic seeds and each seed's structural
// neighborhood. Explore intentionally omits source bodies; callers can follow
// a returned selector with source/context when one definition is worth opening.
type ExploreOptions struct {
	Seeds int
	Edges int
	Depth int
	// Processes bounds the entry-point processes attached to the report (0 = the
	// default of 3, negative = skip them).
	Processes int
}

// ExploreSeed is one semantic/name match promoted to a durable structural
// identity. A hit that cannot be joined to an indexed definition remains in
// the result without a selector or context, preserving semantic recall without
// pretending graph evidence exists.
type ExploreSeed struct {
	SemanticHit
	Selector *SymbolSelector `json:"selector,omitempty"`
}

// ExploreReport turns a broad intent query into a bounded set of exact graph
// neighborhoods. It is the orientation counterpart to ArchitectureMap: map
// starts from the whole project, while explore starts from user intent.
type ExploreReport struct {
	SchemaVersion int                   `json:"schema_version"`
	Query         string                `json:"query"`
	Project       string                `json:"project"`
	Indexed       bool                  `json:"indexed"`
	SearchMode    string                `json:"search_mode"`
	Fusion        string                `json:"fusion,omitempty"`
	Note          string                `json:"note,omitempty"`
	Seeds         []ExploreSeed         `json:"seeds"`
	Contexts      []*ContextReport      `json:"contexts"`
	Processes     []ExploreProcess      `json:"processes"` // entrypoint flows whose steps contain a joined seed; empty without entrypoints
	NotJoined     int                   `json:"not_joined,omitempty"`
	PartialErrors []ContextPartialError `json:"partial_errors,omitempty"`
}

// Explore searches by intent (semantic when available, name search plus a BM25 lexical floor
// otherwise), joins every usable hit to a durable selector, then assembles a
// compact Context bundle for each exact definition. No source bodies are read.
func (svc *Service) Explore(ctx context.Context, cwd, query string, opts ExploreOptions) (*ExploreReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	opts, err := normalizeExploreOptions(opts)
	if err != nil {
		return nil, err
	}
	_, project, indexed, err := svc.project(cwd)
	if err != nil {
		return nil, err
	}
	rep := &ExploreReport{
		SchemaVersion: ExploreSchemaVersion,
		Query:         query,
		Project:       project,
		Indexed:       indexed,
		Seeds:         []ExploreSeed{},
		Contexts:      []*ContextReport{},
		Processes:     []ExploreProcess{},
	}
	if !indexed {
		return rep, nil
	}

	// Ask for a surplus because a file-chunk backend can return several hits
	// inside the same definition AND routinely ranks declarative chunks (docs
	// sections and YAML keys repeat a task's exact words) above the code — the
	// code-first reorder below needs the code hits in the surplus to promote
	// them. The public seeds remain deduplicated and capped to the requested
	// number of exact definitions.
	searchTop := opts.Seeds * 6
	search, err := svc.Search(ctx, cwd, query, searchTop)
	if err != nil {
		return nil, err
	}
	// Code-first seeds: docs sections and YAML keys repeat a task's exact
	// words, so a code-intent query ("make indexing faster") routinely ranked
	// them above the implementation. Orientation wants exact code definitions,
	// so code-kind hits from the surplus fill the seeds first; declarative hits
	// fill only the remainder (and queries whose hits are all docs are
	// unchanged). This ordering feeds task-context too, which joins explore's
	// seeds.
	search.Hits = preferCodeHits(search.Hits)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rep.Project = search.Project
	rep.SearchMode = search.Mode
	rep.Fusion = search.Fusion
	rep.Note = search.Note

	seen := map[string]bool{}
	selectors := make([]SymbolSelector, 0, opts.Seeds)
	for _, hit := range search.Hits {
		if len(rep.Seeds) == opts.Seeds {
			break
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		seed := ExploreSeed{SemanticHit: hit}
		if hit.File != "" && hit.StartLine > 0 {
			selector := SymbolSelector{File: hit.File, StartLine: hit.StartLine, FQN: hit.FQN, Kind: hit.Kind}
			resolved, resolveErr := svc.resolveSourceSelector(cwd, selector)
			if resolveErr != nil {
				rep.PartialErrors = append(rep.PartialErrors, ContextPartialError{
					Symbol: hit.Symbol, Component: "join", Error: boundedErrorText(resolveErr),
				})
			} else if resolved.found {
				seed.Selector = selectorForNode(resolved.node)
			}
		}
		key := exploreSeedKey(seed)
		if seen[key] {
			continue
		}
		seen[key] = true
		if seed.Selector == nil {
			rep.NotJoined++
		} else {
			selectors = append(selectors, *seed.Selector)
		}
		rep.Seeds = append(rep.Seeds, seed)
	}
	if len(selectors) > 0 {
		batch, batchErr := svc.contextBatchWithContext(ctx, cwd, nil, selectors, opts.Depth, true, false)
		if batchErr != nil {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			rep.PartialErrors = append(rep.PartialErrors, ContextPartialError{Component: "contexts", Error: boundedErrorText(batchErr)})
		} else {
			for _, ctxRep := range batch.Results {
				boundExploreContext(ctxRep, opts.Edges)
				rep.Contexts = append(rep.Contexts, ctxRep)
			}
			rep.PartialErrors = append(rep.PartialErrors, batch.PartialErrors...)
		}
	}
	if opts.Processes > 0 && len(selectors) > 0 {
		procs, procErr := svc.exploreProcesses(ctx, cwd, rep.Seeds, opts.Processes)
		if procErr != nil {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			rep.PartialErrors = append(rep.PartialErrors, ContextPartialError{Component: "processes", Error: boundedErrorText(procErr)})
		} else if len(procs) > 0 {
			rep.Processes = procs
		}
	}
	return rep, nil
}

func exploreSeedKey(seed ExploreSeed) string {
	if seed.Selector != nil {
		s := seed.Selector
		return s.File + "\x00" + strconv.Itoa(s.StartLine) + "\x00" + s.FQN + "\x00" + s.Kind
	}
	return seed.File + "\x00" + strconv.Itoa(seed.StartLine) + "\x00" + strconv.Itoa(seed.EndLine) + "\x00" + seed.Symbol
}

// preferCodeHits stably reorders search hits so code-symbol kinds come before
// declarative ones (sections, keys, tables, selectors), preserving the backend's
// rank within each tier. All-declarative or all-code results pass through
// unchanged.
func preferCodeHits(hits []SemanticHit) []SemanticHit {
	if len(hits) < 2 {
		return hits
	}
	code := make([]SemanticHit, 0, len(hits))
	other := make([]SemanticHit, 0, len(hits))
	for _, h := range hits {
		if isCodeHitKind(h.Kind) {
			code = append(code, h)
		} else {
			other = append(other, h)
		}
	}
	if len(code) == 0 || len(other) == 0 {
		return hits
	}
	return append(code, other...)
}

// isCodeHitKind reports whether a hit kind is an exact code definition that
// joins to a real structural neighborhood (callers/callees/tests).
func isCodeHitKind(kind string) bool {
	switch kind {
	case "function", "method", "type", "class", "test", "variable":
		return true
	}
	return false
}

func normalizeExploreOptions(opts ExploreOptions) (ExploreOptions, error) {
	if opts.Seeds == 0 {
		opts.Seeds = DefaultExploreSeeds
	}
	if opts.Seeds < 1 || opts.Seeds > MaxExploreSeeds {
		return opts, fmt.Errorf("explore seeds must be between 1 and %d", MaxExploreSeeds)
	}
	if opts.Edges == 0 {
		opts.Edges = DefaultExploreEdges
	}
	if opts.Edges < 1 || opts.Edges > MaxExploreEdges {
		return opts, fmt.Errorf("explore edges must be between 1 and %d", MaxExploreEdges)
	}
	if opts.Depth == 0 {
		opts.Depth = 2
	}
	if opts.Depth < 1 || opts.Depth > 10 {
		return opts, fmt.Errorf("explore depth must be between 1 and 10")
	}
	if opts.Processes == 0 {
		opts.Processes = DefaultExploreProcesses
	}
	if opts.Processes > MaxExploreProcesses {
		return opts, fmt.Errorf("explore processes must be at most %d", MaxExploreProcesses)
	}
	return opts, nil
}

func boundExploreContext(rep *ContextReport, limit int) {
	if rep == nil {
		return
	}
	rep.Callers = capSlice(rep.Callers, limit)
	rep.Callees = capSlice(rep.Callees, limit)
	rep.References = capSlice(rep.References, limit)
	rep.Tests = capSlice(rep.Tests, limit)
	rep.ReferencesTruncated = rep.ReferencesTotal - len(rep.References)
	if rep.ReferencesTruncated < 0 {
		rep.ReferencesTruncated = 0
	}
	// Explore is a compact structural neighborhood. Memory recall is disabled
	// before assembly; clear these defensively if a future Context path adds
	// another enrichment source.
	rep.Memories = nil
}
