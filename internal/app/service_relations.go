package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/abdul-hamid-achik/codemap/internal/extract/lspsrc"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
	"github.com/abdul-hamid-achik/codemap/internal/lsp"
)

// SymbolRef is a lightweight reference to a graph node (for query results).
// Signature and Doc let callers understand each result without a file read.
type SymbolRef struct {
	Symbol    string `json:"symbol"`
	FQN       string `json:"fqn,omitempty"`
	Kind      string `json:"kind"`
	File      string `json:"file"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Signature string `json:"signature,omitempty"`
	Doc       string `json:"doc,omitempty"`
}

func nodeToRef(n graph.Node) SymbolRef {
	return SymbolRef{Symbol: n.Symbol, FQN: n.FQN, Kind: n.Kind, File: n.FilePath,
		StartLine: n.StartLine, EndLine: n.EndLine, Signature: n.Signature, Doc: n.Docstring}
}

// RelationReport is returned by Callers/Callees.
type RelationReport struct {
	Symbol      string               `json:"symbol"`
	Selector    *SymbolSelector      `json:"selector,omitempty"` // exact selected definition; absent on a name-union query
	Project     string               `json:"project"`
	Found       bool                 `json:"found"` // whether the symbol exists in the index — distinguishes a typo from a real symbol with no callers/callees (both yield empty Results)
	Results     []SymbolRef          `json:"results"`
	Note        string               `json:"note,omitempty"`        // set when precise resolution fell back to name-based
	Candidates  []AmbiguityCandidate `json:"candidates,omitempty"`  // the merged definition set behind Note; re-query with candidates[i].selector
	Resolution  string               `json:"resolution,omitempty"`  // human sentence set when the call graph is unresolved (TS/JS/Python without --precise) — results are unavailable, not absent
	CallGraph   string               `json:"call_graph"`            // stable machine enum: resolved|name|unresolved|none
	Annotations []graph.Annotation   `json:"annotations,omitempty"` // notes/data pinned to the queried symbol
}

// validSymbol reports whether s is a non-blank symbol identifier. P1-04:
// the service seam must reject blank symbols before any graph query — a
// blank input was matching every file node (which are stored with
// Symbol=""), giving a confidently-wrong "Found:true" for every file.
// Read queries return a graceful Found:false + note; write queries
// return an error so the agent sees a real problem.
func validSymbol(s string) bool {
	return strings.TrimSpace(s) != ""
}

func (svc *Service) Callers(cwd, symbol string) (*RelationReport, error) {
	if !validSymbol(symbol) {
		return &RelationReport{Found: false, Resolution: "none", CallGraph: CallGraphNone, Note: "supply a non-empty symbol name (a blank symbol would match every file node)"}, nil
	}
	rep, err := svc.relation(cwd, symbol, (*graph.Store).Callers)
	if err != nil {
		return nil, err
	}
	return svc.autoUpgradeRelation(rep, cwd, symbol, true), nil
}

// Callees returns the functions/methods that symbol calls.
func (svc *Service) Callees(cwd, symbol string) (*RelationReport, error) {
	if !validSymbol(symbol) {
		return &RelationReport{Found: false, Resolution: "none", CallGraph: CallGraphNone, Note: "supply a non-empty symbol name (a blank symbol would match every file node)"}, nil
	}
	rep, err := svc.relation(cwd, symbol, (*graph.Store).Callees)
	if err != nil {
		return nil, err
	}
	return svc.autoUpgradeRelation(rep, cwd, symbol, false), nil
}

// autoUpgradeRelation fills in a callers/callees result for an LSP-language symbol
// whose call graph isn't indexed (base.Resolution set by callGraphUnavailable) by
// driving a scoped, on-demand callHierarchy for just that symbol — so TS/JS/Python
// callers/callees resolve without a full `index --precise`. It's a no-op when the
// call graph is already available (Go, or a precise index), so those queries pay no
// latency. If the language server is absent or fails, the honest "run --precise"
// note is kept unchanged. wantCallers selects the direction.
//
// P1-06 (B1): preciseRelations distinguishes three outcomes, not two — genuinely
// resolved (err == nil, regardless of whether the results are empty: a FOUND
// symbol with a prepared call hierarchy that the server reports has zero
// incoming/outgoing calls is an honest zero) from a soft miss (err ==
// errPreciseUnresolved: the project isn't registered, the symbol's live position
// couldn't be found in the server's documentSymbol response, or the server
// prepared no call-hierarchy item for it) from a hard failure (any other err: the
// server is missing, unreachable, or errored). Only the first may claim
// resolution here; both the soft-miss and the hard-failure branches fall into the
// same `err != nil` case below and keep base's honest "unresolved" note — a
// soft miss must never be reported as "resolved on demand" with an empty result,
// which is a confidently-wrong answer ("no callers, resolved") where the truth is
// "could not resolve".
func (svc *Service) autoUpgradeRelation(base *RelationReport, cwd, symbol string, wantCallers bool) *RelationReport {
	if base.Resolution == "" {
		return base // call graph available — nothing to upgrade, no server spawn
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	callers, callees, _, err := svc.preciseRelations(ctx, cwd, symbol, "", 0)
	if err != nil {
		return base // soft miss (errPreciseUnresolved) or hard failure — keep the honest "unresolved" note
	}
	results := callees
	if wantCallers {
		results = callers
	}
	base.Results = nonNil(results)
	base.Found = true
	base.Resolution = ""               // resolved on demand; the "run --precise" note no longer applies
	base.CallGraph = CallGraphResolved // on-demand callHierarchy resolved the edges
	base.Note = "resolved on demand via the language server's callHierarchy (no --precise needed)"
	return base
}

// canonicalSymbol resolves a user query that may be qualified ("pkg.Sym",
// "pkg.Type.Method", "Type.Method") — the form hotspots/orphans/find DISPLAY — to the
// bare symbol the graph keys on, so a name can be copied straight from one command
// into another (the agent workflow chains hotspots/orphans -> context/impact). It
// rewrites ONLY when the input isn't already a bare symbol, so plain-name queries are
// untouched and a genuine typo is returned unchanged (still reported "no symbol named").
func canonicalSymbol(g *graph.Store, projectID int64, input string) string {
	if !strings.Contains(input, ".") {
		return input
	}
	if defs, err := g.FindNodesBySymbol(projectID, input); err == nil && len(defs) > 0 {
		return input // literally indexed under this dotted name
	}
	if bare, ok, _ := g.ResolveQualifiedName(projectID, input); ok {
		return bare
	}
	return input
}

func (svc *Service) relation(cwd, symbol string, query func(*graph.Store, int64, string) ([]graph.Node, error)) (*RelationReport, error) {
	g, err := svc.s.Graph()
	if err != nil {
		return nil, err
	}
	_, name, err := svc.resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	rep := &RelationReport{Symbol: symbol, Project: name, Results: []SymbolRef{}, CallGraph: CallGraphNone}
	p, err := g.GetProjectByName(name)
	if errors.Is(err, graph.ErrNotFound) {
		return rep, nil
	}
	if err != nil {
		return nil, err
	}
	symbol = canonicalSymbol(g, p.ID, symbol) // accept the qualified form hotspots/orphans print
	rep.Symbol = symbol
	nodes, err := query(g, p.ID, symbol)
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		rep.Results = append(rep.Results, nodeToRef(n))
	}
	// Name-keyed lookup unions same-named definitions, so flag it (mirrors impact)
	// and point at the right fix for the current index — precise reindex on a
	// name-based graph, or a more specific name when edges are already exact.
	// The same lookup tells us whether the symbol exists at all, so an empty
	// Results set can be reported as "no such symbol" rather than "no callers".
	defs, derr := g.FindNodesBySymbol(p.ID, symbol)
	rep.Found = len(rep.Results) > 0 || (derr == nil && len(defs) > 0)
	// call_graph: the stable enum. defs (the matching definitions) classify
	// resolution; empty defs on an unknown symbol stays "none".
	rep.CallGraph = svc.callGraphStatus(g, p.ID, defs)
	if derr == nil && len(defs) > 1 {
		rep.Candidates = candidatesFromNodes(defs)
		if rep.CallGraph == CallGraphResolved {
			rep.Note = fmt.Sprintf("%q matches %d definitions — each resolved precisely, but these results still merge all of them; query a more specific name to separate them", symbol, len(defs))
		} else {
			rep.Note = fmt.Sprintf("%q matches %d definitions (name-based) — these results merge all of them; reindex with 'codemap index --precise' for exact per-method edges, or use callers/callees --precise for one method", symbol, len(defs))
		}
	}
	// Empty results for a no-name-based-call language on a non-precise index mean
	// "unresolved", not "no callers" — flag it instead of a confident empty.
	if lang, yes := svc.callGraphUnavailable(g, p.ID, defs); yes {
		rep.Resolution = fmt.Sprintf("call graph not available for %s without precise indexing — callers/callees are unresolved (not absent); run 'codemap index --precise'", lang)
	}
	rep.Annotations = symbolAnnotations(g, p.ID, symbol)
	return rep, nil
}

// symbolAnnotations returns the annotations pinned to a symbol — matched by the
// query name or any of its resolved definition FQNs/symbols.
func symbolAnnotations(g *graph.Store, projectID int64, symbol string) []graph.Annotation {
	candidates := []string{symbol}
	if locs, err := g.FindNodesBySymbol(projectID, symbol); err == nil {
		for _, n := range locs {
			candidates = append(candidates, n.FQN, n.Symbol)
		}
	}
	return nodeAnnotationsFor(g, projectID, candidates...)
}

// symbolAnnotationsByName resolves a symbol's annotations given the project name
// (used by the precise/gopls path, which carries the name rather than the pid).
func (svc *Service) symbolAnnotationsByName(name, symbol string) []graph.Annotation {
	g, err := svc.s.Graph()
	if err != nil {
		return nil
	}
	p, err := g.GetProjectByName(name)
	if err != nil {
		return nil
	}
	return symbolAnnotations(g, p.ID, symbol)
}

// PreciseCallers computes exact callers of a symbol using the language server's
// callHierarchy (gopls for Go; typescript-language-server / pyright for the LSP
// languages) — no by-name inflation, and scoped to the one symbol so it needs no
// `index --precise` reindex. Falls back to name-based results if the server is
// unavailable.
func (svc *Service) PreciseCallers(ctx context.Context, cwd, symbol string) (*RelationReport, error) {
	c, _, project, err := svc.preciseRelations(ctx, cwd, symbol, "", 0)
	if err != nil {
		return svc.preciseFallback(cwd, symbol, err, svc.Callers)
	}
	// preciseRelations succeeding means gopls resolved the symbol, so it exists.
	return &RelationReport{Symbol: symbol, Project: project, Found: true, Results: nonNil(c),
		CallGraph:   CallGraphResolved, // language-server callHierarchy resolved the edges exactly
		Annotations: svc.symbolAnnotationsByName(project, symbol)}, nil
}

// preciseFallback degrades to name-based results when the language server can't
// resolve precisely (e.g. gopls can't form a workspace view in a restricted
// environment, or the project isn't a buildable module), attaching a note so the
// caller knows the results are name-based. Far better than failing a query with a
// raw "jsonrpc error: no views". If name-based resolution itself errors, that's
// surfaced instead.
func (svc *Service) preciseFallback(cwd, symbol string, cause error, nameBased func(cwd, symbol string) (*RelationReport, error)) (*RelationReport, error) {
	rep, err := nameBased(cwd, symbol)
	if err != nil {
		return nil, err
	}
	rep.Note = fmt.Sprintf("precise resolution unavailable (%v) — showing name-based results", cause)
	return rep, nil
}

// PreciseCallees computes exact callees of a symbol using the language server's
// callHierarchy (gopls for Go; typescript-language-server / pyright for the LSP
// languages), scoped to the one symbol.
func (svc *Service) PreciseCallees(ctx context.Context, cwd, symbol string) (*RelationReport, error) {
	_, ce, project, err := svc.preciseRelations(ctx, cwd, symbol, "", 0)
	if err != nil {
		return svc.preciseFallback(cwd, symbol, err, svc.Callees)
	}
	// preciseRelations succeeding means gopls resolved the symbol, so it exists.
	return &RelationReport{Symbol: symbol, Project: project, Found: true, Results: nonNil(ce),
		CallGraph:   CallGraphResolved, // language-server callHierarchy resolved the edges exactly
		Annotations: svc.symbolAnnotationsByName(project, symbol)}, nil
}

// PreciseRelationsAt returns both exact callers and callees of the symbol whose
// declaration is at file:line (to disambiguate same-named symbols), in one gopls
// session. Used by the studio precise toggle.
func (svc *Service) PreciseRelationsAt(ctx context.Context, cwd, symbol, file string, line int) (callers, callees []SymbolRef, err error) {
	c, ce, _, err := svc.preciseRelations(ctx, cwd, symbol, file, line)
	return c, ce, err
}

// lspServerFor returns the language server that resolves precise call edges for a
// codemap language, drawn from the same registry the indexer uses (gopls for Go;
// the DefaultServers entry for TypeScript/JavaScript/Python). filePath refines the
// LSP languageId for JSX/TSX, which typescript-language-server only parses (and
// whose <Component/> usages it resolves as calls) under the *react ids. ok is
// false for a language codemap can't resolve precisely.
func lspServerFor(lang, filePath string) (cmd string, args []string, langID string, ok bool) {
	if lang == "go" {
		return "gopls", nil, "go", true
	}
	for _, spec := range lspsrc.DefaultServers {
		for _, b := range spec.Langs {
			if b.Lang != lang {
				continue
			}
			id := b.LangID
			switch strings.ToLower(filepath.Ext(filePath)) {
			case ".tsx":
				id = "typescriptreact"
			case ".jsx":
				id = "javascriptreact"
			}
			return spec.Cmd, spec.Args, id, true
		}
	}
	return "", nil, "", false
}

// errPreciseUnresolved marks a "soft miss" from preciseRelations: the graph/LSP
// plumbing itself worked (no transport or process error), but the symbol
// couldn't be pinned down precisely — the project isn't registered, the live
// file no longer has a documentSymbol matching the queried name at the expected
// position, or the server prepared no call-hierarchy item for it. It is
// deliberately distinct from a nil error (see preciseRelations) so a caller can
// tell "the server told us, concretely, that this symbol has zero calls" from
// "we couldn't even ask the question" — only the former may claim resolution
// (P1-06 / B1).
var errPreciseUnresolved = errors.New("precise: symbol not resolved by the language server")

// preciseRelations resolves the symbol's node via the graph (preferring the one
// at hintFile:hintLine), then drives the matching language server (gopls for Go;
// typescript-language-server / pyright for the LSP languages) through
// documentSymbol → prepareCallHierarchy → incoming + outgoing in a single session.
// Scoped to the one queried symbol, so it works on demand without a full
// `index --precise` reindex.
//
// Return contract (P1-06 / B1): err == nil means genuinely resolved — even when
// callers/callees come back empty, that's the server's explicit, honest answer
// for a symbol it FOUND and prepared a call hierarchy for ("resolved-genuinely-
// zero"). err == errPreciseUnresolved means a soft miss — the symbol could not
// be located/prepared at all, so the caller must NOT treat empty results as a
// real answer. Any other err is a hard failure (server missing, spawn/transport
// error). Callers (autoUpgradeRelation, autoUpgradeSelectorRelation,
// PreciseCallers/Callees, preciseRelationBySelector) already branch on
// `err != nil` alone, so both the soft-miss and hard-failure cases are handled
// identically today — the sentinel exists so the soft-miss branch is explicit,
// named, and independently testable rather than accidentally sharing the "nil
// error, empty results" shape genuine resolution uses.
func (svc *Service) preciseRelations(ctx context.Context, cwd, symbol, hintFile string, hintLine int) (callers, callees []SymbolRef, project string, err error) {
	g, err := svc.s.Graph()
	if err != nil {
		return nil, nil, "", err
	}
	if _, project, err = svc.resolveProject(cwd); err != nil {
		return nil, nil, project, err
	}
	p, err := g.GetProjectByName(project)
	if errors.Is(err, graph.ErrNotFound) {
		return nil, nil, project, errPreciseUnresolved // soft miss: project never indexed
	}
	if err != nil {
		return nil, nil, project, err
	}

	symbol = canonicalSymbol(g, p.ID, symbol) // accept the qualified form hotspots/orphans print
	nodes, err := g.FindNodesBySymbol(p.ID, symbol)
	if err != nil {
		return nil, nil, project, err
	}
	var node *graph.Node
	for i := range nodes {
		if _, _, _, ok := lspServerFor(nodes[i].Language, nodes[i].FilePath); !ok {
			continue // a language codemap can't resolve precisely
		}
		if hintFile != "" && nodes[i].FilePath == hintFile && (hintLine == 0 || nodes[i].StartLine == hintLine) {
			node = &nodes[i] // exact match for the requested declaration
			break
		}
		if node == nil {
			node = &nodes[i] // first precise-capable node as fallback
		}
	}
	if node == nil {
		return nil, nil, project, fmt.Errorf("no precise-resolvable symbol named %q (precise resolution supports Go, TypeScript, JavaScript, Python)", symbol)
	}
	cmd, args, langID, _ := lspServerFor(node.Language, node.FilePath)
	if _, err := exec.LookPath(cmd); err != nil {
		return nil, nil, project, fmt.Errorf("%s not found on PATH (required for precise %s resolution)", cmd, node.Language)
	}

	root := p.Path
	absFile := filepath.Join(root, node.FilePath)
	src, err := os.ReadFile(absFile)
	if err != nil {
		return nil, nil, project, err
	}

	cl, err := lsp.Spawn(ctx, cmd, args...)
	if err != nil {
		return nil, nil, project, err
	}
	defer func() { _ = cl.Close() }()
	if err := cl.Initialize(ctx, root); err != nil {
		return nil, nil, project, err
	}
	uri, _ := lsp.URI(absFile)
	if err := cl.DidOpen(uri, langID, string(src)); err != nil {
		return nil, nil, project, err
	}
	// callHierarchy needs the whole workspace analyzed; wait for gopls to load.
	cl.WaitReady(ctx, 20*time.Second)

	syms, err := cl.DocumentSymbols(ctx, uri)
	if err != nil {
		return nil, nil, project, err
	}
	pos, ok := findSymbolPos(syms, symbol, node.StartLine)
	if !ok {
		return nil, nil, project, errPreciseUnresolved // soft miss: no live documentSymbol matches the queried name/position
	}
	items, err := cl.PrepareCallHierarchy(ctx, uri, pos)
	if err != nil {
		return nil, nil, project, err
	}
	if len(items) == 0 {
		return nil, nil, project, errPreciseUnresolved // soft miss: server prepared no call-hierarchy item here
	}

	in, err := cl.IncomingCalls(ctx, items[0])
	if err != nil {
		return nil, nil, project, err
	}
	out, err := cl.OutgoingCalls(ctx, items[0])
	if err != nil {
		return nil, nil, project, err
	}
	for _, c := range in {
		callers = append(callers, itemToRef(c.From, root))
	}
	for _, c := range out {
		callees = append(callees, itemToRef(c.To, root))
	}
	return callers, callees, project, nil
}

func nonNil(s []SymbolRef) []SymbolRef {
	if s == nil {
		return []SymbolRef{}
	}
	return s
}

func itemToRef(item lsp.CallHierarchyItem, root string) SymbolRef {
	return SymbolRef{
		Symbol:    symbolBase(item.Name),
		File:      uriToRel(item.URI, root),
		StartLine: item.Range.Start.Line + 1,
	}
}

// findSymbolPos returns the selection-range start of a symbol by name, preferring
// the declaration whose range starts at wantLine (1-based).
func findSymbolPos(syms []lsp.DocumentSymbol, name string, wantLine int) (lsp.Position, bool) {
	var best lsp.Position
	found := false
	var walk func([]lsp.DocumentSymbol)
	walk = func(ss []lsp.DocumentSymbol) {
		for _, s := range ss {
			// gopls names methods like "(*Store).AddNode"; match the base name.
			if symbolBase(s.Name) == name {
				if s.Range.Start.Line+1 == wantLine {
					best = s.SelectionRange.Start
					found = true
					return
				}
				if !found {
					best = s.SelectionRange.Start
					found = true
				}
			}
			walk(s.Children)
		}
	}
	walk(syms)
	return best, found
}

// symbolBase strips a method's receiver prefix: "(*Store).AddNode" -> "AddNode".
func symbolBase(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i+1:]
	}
	return name
}

func uriToRel(uri, root string) string {
	// P1-02: pre-fix did TrimPrefix(uri, "file://") which dropped
	// percent-encoded chars in the language server's response URI;
	// a path with a space never matched the project root. PathFromURI
	// decodes before filepath.Rel.
	p, err := lsp.PathFromURI(uri)
	if err != nil || p == "" {
		return strings.TrimPrefix(uri, "file://")
	}
	if rel, err := filepath.Rel(root, p); err == nil {
		return rel
	}
	return p
}
