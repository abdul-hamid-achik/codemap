package index

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/extract"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

// fakeLSPExtractor is a hermetic stand-in for lspsrc.Extractor. It implements
// BOTH extract.Extractor (the documentSymbol-equivalent: ExtractFile) AND
// extract.CallResolver (the callHierarchy-equivalent: CallEdges) with canned
// data. Registering it on the Indexer for "typescript" lets IndexProject drive
// the REAL resolveAllPreciseEdges → resolveLSPCallEdgesWith position-mapping and
// per-file coverage logic end-to-end WITHOUT spawning a language server, touching
// the network, or hitting the exec.LookPath gate that silently skips the
// server-gated tests in indexer_lsp_test.go on a minimal CI image.
//
// The seam is exact: resolveLSPCallEdgesWith builds its resolver set from
// ix.extractors by type-asserting each to extract.CallResolver, then joins the
// returned CallEdges to graph nodes by declaration position and records
// call_graph_coverage. Everything downstream of CallEdges is production code.
type fakeLSPExtractor struct {
	lang      string
	symbols   map[string][]extract.Symbol    // relPath -> documentSymbol-equivalent nodes
	refs      map[string][]extract.Reference // relPath -> name-based references (tsscan-equivalent)
	edges     map[string][]extract.CallEdge  // relPath -> callHierarchy-equivalent outgoing calls
	failFiles map[string]bool                // relPath -> simulate a callHierarchy failure
}

func (f *fakeLSPExtractor) Language() string { return f.lang }

func (f *fakeLSPExtractor) ExtractFile(relPath string, _ []byte) (*extract.FileResult, error) {
	return &extract.FileResult{
		Path:       relPath,
		Language:   f.lang,
		Symbols:    f.symbols[relPath],
		References: f.refs[relPath],
	}, nil
}

// CallEdges returns the canned callHierarchy answer for relPath. An empty slice
// with a nil error simulates a leaf callable: the server resolved the declaration
// and found zero outgoing calls (a successful, coverage-worthy resolution). A file
// listed in failFiles returns an error, simulating a callHierarchy request the
// server could not satisfy — which must downgrade that file's coverage.
func (f *fakeLSPExtractor) CallEdges(_ context.Context, relPath string) ([]extract.CallEdge, error) {
	if f.failFiles[relPath] {
		return nil, errors.New("simulated callHierarchy failure")
	}
	return f.edges[relPath], nil
}

// sym is a tiny builder for a function symbol node at a 1-based start line,
// matching the position model resolveLSPCallEdgesWith joins against.
func sym(name string, line int) extract.Symbol {
	return extract.Symbol{
		Name: name, FQN: name, Kind: extract.KindFunction,
		Language: "typescript", StartLine: line, EndLine: line,
	}
}

// TestResolveLSPCallEdgesHermetic proves the LSP precise-resolution pass —
// position mapping, external-callee skipping, leaf-file coverage, and the
// autoUpgrade invariant (an EMPTY precise answer never erases existing
// name-based candidates) — using a fake CallResolver instead of a real language
// server. It mirrors the assertions of the server-gated
// TestIndexTypeScriptCallEdges but NEVER skips: there is no LookPath gate.
func TestResolveLSPCallEdgesHermetic(t *testing.T) {
	dir := t.TempDir()
	// callee.ts: a leaf callable (no outgoing calls) — must still be marked covered.
	writeFile(t, dir, "callee.ts", "export function callee() { return 1; }\n")
	// caller.ts: calls callee() and one external dependency.
	writeFile(t, dir, "caller.ts", "import { callee } from \"./callee\";\n\nexport function caller() { return callee(); }\n")
	// target.ts: the callee of a NAME-BASED candidate edge (below).
	writeFile(t, dir, "target.ts", "export function target() { return 2; }\n")
	// user.ts: carries a name-based candidate call (tsscan-equivalent reference)
	// but an EMPTY precise answer — the autoUpgrade invariant case.
	writeFile(t, dir, "user.ts", "import { target } from \"./target\";\n\nexport function user() { return target(); }\n")

	fake := &fakeLSPExtractor{
		lang: "typescript",
		symbols: map[string][]extract.Symbol{
			"callee.ts": {sym("callee", 1)},
			"caller.ts": {sym("caller", 3)},
			"target.ts": {sym("target", 1)},
			"user.ts":   {sym("user", 3)},
		},
		refs: map[string][]extract.Reference{
			// A name-based candidate (the tsscan layer's job in production): it
			// becomes a ProvName call edge user -> target during reference
			// resolution, which the precise pass must NOT erase even though
			// user.ts's precise answer below is empty.
			"user.ts": {{From: "user", To: "target", Kind: extract.RefCalls, Qualified: false}},
		},
		edges: map[string][]extract.CallEdge{
			// caller.ts: one internal precise edge (caller:3 -> callee:1) plus one
			// external callee that must be skipped (no graph node).
			"caller.ts": {
				{FromFQN: "caller", FromFile: "caller.ts", FromLine: 3, ToFile: "callee.ts", ToLine: 1, External: false},
				{FromFQN: "caller", FromFile: "caller.ts", FromLine: 3, ToFile: "", ToLine: 0, External: true},
			},
			// callee.ts, target.ts, user.ts: empty (leaf / empty precise answer).
		},
	}

	g, _ := newStores(t)
	pid, err := g.UpsertProject("ts-hermetic", dir, "typescript")
	if err != nil {
		t.Fatal(err)
	}
	ix := New(g, nil, nil, config.DefaultConfig().Index)
	ix.Register(fake) // pre-registered => walk routes .ts here, registerLSP never spawns a server

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := ix.IndexProject(ctx, pid, "ts-hermetic", dir, Options{Precise: true})
	if err != nil {
		t.Fatal(err)
	}

	// The fake was routed (no server was needed): all four .ts files indexed.
	if res.Languages["typescript"] != 4 {
		t.Fatalf("expected 4 typescript files indexed via the fake extractor, got languages %v", res.Languages)
	}
	if res.MissingServers["typescript"] != "" {
		t.Fatalf("no server should be reported missing when a fake extractor is registered, got %v", res.MissingServers)
	}

	// (1) Position mapping: the internal callHierarchy edge joined caller:3 ->
	// callee:1 and was written ProvPrecise. Mirrors TestIndexTypeScriptCallEdges.
	if res.PreciseUpgraded < 1 {
		t.Errorf("PreciseUpgraded = %d, want >= 1 (caller -> callee precise edge)", res.PreciseUpgraded)
	}
	callers, err := g.Callers(pid, "callee")
	if err != nil {
		t.Fatal(err)
	}
	var callerFound bool
	for _, c := range callers {
		if c.Symbol == "caller" {
			callerFound = true
		}
	}
	if !callerFound {
		t.Errorf("callers of callee should include caller (precise edge), got %+v", callers)
	}

	// The caller -> callee call edge is provenance='precise' (not name-based).
	callerID := nodeIDBySymbol(t, g, pid, "caller")
	calleeID := nodeIDBySymbol(t, g, pid, "callee")
	if prov := callEdgeProvenance(t, g, pid, callerID, calleeID); prov != graph.ProvPrecise {
		t.Errorf("caller -> callee call edge provenance = %q, want %q", prov, graph.ProvPrecise)
	}

	// (2) External callee skipped: the dependency call had no node to join.
	if res.PreciseSkipped < 1 {
		t.Errorf("PreciseSkipped = %d, want >= 1 (external callee skipped)", res.PreciseSkipped)
	}

	// (3) autoUpgrade invariant: user.ts's precise answer was EMPTY, yet its
	// pre-existing name-based candidate edge user -> target survives, and user
	// still shows up as a caller of target.
	userID := nodeIDBySymbol(t, g, pid, "user")
	targetID := nodeIDBySymbol(t, g, pid, "target")
	if prov := callEdgeProvenance(t, g, pid, userID, targetID); prov != graph.ProvName {
		t.Errorf("user -> target edge provenance = %q, want %q (empty precise answer must not erase name-based candidates)", prov, graph.ProvName)
	}
	targetCallers, err := g.Callers(pid, "target")
	if err != nil {
		t.Fatal(err)
	}
	var userFound bool
	for _, c := range targetCallers {
		if c.Symbol == "user" {
			userFound = true
		}
	}
	if !userFound {
		t.Errorf("callers of target should still include user (surviving name-based edge), got %+v", targetCallers)
	}

	// (4) Per-file coverage: every resolved file has a call_graph_coverage row
	// with resolver "lsp" — INCLUDING the leaf files (callee.ts, target.ts) whose
	// precise answer was zero edges, AND user.ts whose precise answer was empty.
	coverage, err := g.ProjectCallGraphCoverage(pid)
	if err != nil {
		t.Fatal(err)
	}
	resolverByFile := map[string]string{}
	for _, e := range coverage {
		resolverByFile[e.FilePath] = e.Resolver
	}
	for _, file := range []string{"callee.ts", "caller.ts", "target.ts", "user.ts"} {
		if resolverByFile[file] != "lsp" {
			t.Errorf("coverage[%s] resolver = %q, want %q (leaf/empty files must still be marked covered)", file, resolverByFile[file], "lsp")
		}
	}
}

// TestResolveLSPCallEdgesDowngradesFailedFile proves a file whose callHierarchy
// resolution FAILS is left UNCOVERED (no call_graph_coverage row) while a
// sibling that resolves cleanly is still marked covered — the per-file coverage
// degradation the gated tests can't reach deterministically. Hermetic: no server.
func TestResolveLSPCallEdgesDowngradesFailedFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "good.ts", "export function good() { return 1; }\n")
	writeFile(t, dir, "bad.ts", "export function bad() { return 2; }\n")

	fake := &fakeLSPExtractor{
		lang: "typescript",
		symbols: map[string][]extract.Symbol{
			"good.ts": {sym("good", 1)},
			"bad.ts":  {sym("bad", 1)},
		},
		edges: map[string][]extract.CallEdge{
			"good.ts": {}, // leaf, resolves cleanly -> covered
			// bad.ts deliberately absent from edges but present in failFiles below.
		},
	}
	fake.failFiles = map[string]bool{"bad.ts": true}

	g, _ := newStores(t)
	pid, err := g.UpsertProject("ts-degrade", dir, "typescript")
	if err != nil {
		t.Fatal(err)
	}
	ix := New(g, nil, nil, config.DefaultConfig().Index)
	ix.Register(fake)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := ix.IndexProject(ctx, pid, "ts-degrade", dir, Options{Precise: true}); err != nil {
		t.Fatal(err)
	}

	coverage, err := g.ProjectCallGraphCoverage(pid)
	if err != nil {
		t.Fatal(err)
	}
	resolverByFile := map[string]string{}
	for _, e := range coverage {
		resolverByFile[e.FilePath] = e.Resolver
	}
	if resolverByFile["good.ts"] != "lsp" {
		t.Errorf("coverage[good.ts] resolver = %q, want lsp (clean file must be covered)", resolverByFile["good.ts"])
	}
	if _, covered := resolverByFile["bad.ts"]; covered {
		t.Errorf("coverage[bad.ts] = %q, want UNCOVERED (callHierarchy failure must downgrade coverage)", resolverByFile["bad.ts"])
	}
}

// nodeIDBySymbol returns the graph node id for a unique symbol, failing the test
// if it is absent.
func nodeIDBySymbol(t *testing.T, g *graph.Store, pid int64, symbol string) int64 {
	t.Helper()
	nodes, err := g.FindNodesBySymbol(pid, symbol)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) == 0 {
		t.Fatalf("no node found for symbol %q", symbol)
	}
	return nodes[0].ID
}

// callEdgeProvenance returns the provenance of the calls edge from->to, or "" if
// no such edge exists.
func callEdgeProvenance(t *testing.T, g *graph.Store, pid int64, from, to int64) string {
	t.Helper()
	edges, err := g.ProjectEdges(pid)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range edges {
		if e.EdgeType == graph.EdgeCalls && e.SourceID == from && e.TargetID == to {
			return e.Provenance
		}
	}
	return ""
}
