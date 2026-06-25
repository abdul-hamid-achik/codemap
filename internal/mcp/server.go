// Package mcp exposes codemap over the Model Context Protocol (stdio). It is a
// THIN layer: every handler resolves arguments and delegates to internal/app,
// then returns JSON for the agent. It uses the official go-sdk, whose
// StdioTransport emits newline-delimited JSON-RPC (required by Claude Code /
// Codex / OpenCode — Content-Length framing makes them report "failed to
// connect"). LSP's Content-Length framing must never leak in here.
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/abdul-hamid-achik/codemap/internal/app"
	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/daemon"
	"github.com/abdul-hamid-achik/codemap/internal/index"
	"github.com/abdul-hamid-achik/codemap/internal/version"
)

const instructions = `codemap is a local code knowledge graph: code structure (calls, types,
tests) fused with semantic vectors, queried offline. Index a project once with codemap_index,
then query it — until you do, query tools return {"indexed": false} with a hint to index first.
Every tool takes an optional "path" (project dir; defaults to cwd) and returns JSON.

Find code:
- codemap_semantic — by meaning ("jwt validation middleware"); needs an embedded index.
- codemap_find — by name; fast and offline (no embeddings).

Understand a symbol:
- codemap_impact (flagship) — definition sites, direct callers, the transitive blast radius
  (everything a change affects), and which tests cover it. One call replaces many file reads.
- codemap_callers / codemap_callees — who calls a symbol / what it calls.
- codemap_source — a symbol's source code; codemap_symbols — a file's outline.
Results carry each symbol's signature and docstring, so you rarely need to open files.

Survey a codebase: codemap_hotspots (hubs), codemap_orphans (dead-code candidates),
codemap_status (index size), codemap_projects (what's indexed).

Accuracy: the graph is name-based, so a cross-package method call (x.Foo()) matches every method
named Foo — codemap_callers/codemap_impact note when a name is ambiguous and codemap_hotspots flags
the inflation. The graph-wide fix is to re-run codemap_index with precise:true — the unified
exact-resolution pass: go/types for Go, language-server callHierarchy for the LSP languages
(TypeScript, JavaScript, Python). It makes EVERY query exact (callers, callees, impact, hotspots,
path) with no per-call flag. The LSP languages have NO name-based call edges, so for a TS/JS/Python
project precise:true is what gives codemap_callers/impact/hotspots/path a call graph at all. For a
one-off exact answer on Go without reindexing, pass precise:true to codemap_callers/codemap_callees
(gopls; Go only — on the LSP languages, reindex with codemap_index precise:true instead). Precise
resolution degrades to name-based (with a "note" in the result) when the toolchain or module is
unavailable, so precise:true is never a hard failure. Likewise
codemap_semantic returns a "note" (not an error) when the project has no embeddings — fall back to
codemap_find.

Call codemap_docs for the full guide (workflow, every tool, accuracy, and how codemap fits the
local toolchain) — useful when wiring codemap into a harness.`

// Server wraps the go-sdk MCP server over a codemap session.
type Server struct {
	svc *app.Service
	srv *sdkmcp.Server
}

// NewServer builds an MCP server backed by the given session.
func NewServer(sess *app.Session) *Server {
	s := &Server{svc: app.NewService(sess)}
	s.srv = sdkmcp.NewServer(
		&sdkmcp.Implementation{Name: "codemap", Version: version.Version},
		&sdkmcp.ServerOptions{Instructions: instructions},
	)
	s.register()
	return s
}

// Run serves over stdio until the context is cancelled.
func (s *Server) Run(ctx context.Context) error {
	return s.serve(ctx, &sdkmcp.StdioTransport{})
}

// serve runs over an arbitrary transport (used by tests with an in-memory one).
func (s *Server) serve(ctx context.Context, t sdkmcp.Transport) error {
	return s.srv.Run(ctx, t)
}

// ---- tool inputs ----

type pathInput struct {
	Path string `json:"path,omitempty" jsonschema:"project directory; defaults to the server working directory"`
}

type indexInput struct {
	Path    string `json:"path,omitempty" jsonschema:"project directory; defaults to cwd"`
	Reindex bool   `json:"reindex,omitempty" jsonschema:"wipe and rebuild the whole index"`
	NoEmbed bool   `json:"no_embed,omitempty" jsonschema:"skip semantic embeddings (structure only)"`
	Precise bool   `json:"precise,omitempty" jsonschema:"resolve call edges exactly (Go via go/types, needs the go toolchain; TypeScript/JavaScript/Python via language-server callHierarchy) — eliminates same-named over-matching and gives the LSP languages a call graph"`
}

type semanticInput struct {
	Query string `json:"query" jsonschema:"natural-language description of the code to find"`
	Path  string `json:"path,omitempty" jsonschema:"project directory; defaults to cwd"`
	TopK  int    `json:"top_k,omitempty" jsonschema:"maximum results (default 10)"`
}

type symbolQueryInput struct {
	Symbol  string `json:"symbol" jsonschema:"the symbol name to look up"`
	Path    string `json:"path,omitempty" jsonschema:"project directory; defaults to cwd"`
	Precise bool   `json:"precise,omitempty" jsonschema:"use the language server (gopls) for exact results (Go) — slower, but not inflated by same-named symbols"`
}

type impactInput struct {
	Symbol string `json:"symbol" jsonschema:"the symbol to analyze"`
	Path   string `json:"path,omitempty" jsonschema:"project directory; defaults to cwd"`
	Depth  int    `json:"depth,omitempty" jsonschema:"max hops for the blast radius (default 3)"`
}

type relatedFilesInput struct {
	File string `json:"file" jsonschema:"project-relative file path to find related files for"`
	Path string `json:"path,omitempty" jsonschema:"project directory; defaults to cwd"`
}

type symbolAtInput struct {
	File string `json:"file" jsonschema:"project-relative file path"`
	Line int    `json:"line" jsonschema:"1-based line number to resolve to its enclosing symbol"`
	Path string `json:"path,omitempty" jsonschema:"project directory; defaults to cwd"`
}

type secretImpactInput struct {
	Keys  []string `json:"keys" jsonschema:"secret key NAMES to analyze (e.g. STRIPE_KEY) — names only, never values"`
	Path  string   `json:"path,omitempty" jsonschema:"project directory; defaults to cwd"`
	Depth int      `json:"depth,omitempty" jsonschema:"max hops for each key's blast radius (default 3)"`
}

type limitInput struct {
	Path string `json:"path,omitempty" jsonschema:"project directory; defaults to cwd"`
	Top  int    `json:"top,omitempty" jsonschema:"maximum results"`
}

type pathQueryInput struct {
	From string `json:"from" jsonschema:"the starting symbol"`
	To   string `json:"to" jsonschema:"the destination symbol"`
	Path string `json:"path,omitempty" jsonschema:"project directory; defaults to cwd"`
}

type symbolsInput struct {
	File string `json:"file" jsonschema:"the file to list symbols for (relative to the project, or absolute)"`
	Path string `json:"path,omitempty" jsonschema:"project directory; defaults to cwd"`
}

type findInput struct {
	Query string `json:"query" jsonschema:"substring to match against symbol names and fully-qualified names"`
	Path  string `json:"path,omitempty" jsonschema:"project directory; defaults to cwd"`
	Top   int    `json:"top,omitempty" jsonschema:"maximum results"`
}

type sourceInput struct {
	Symbol string `json:"symbol" jsonschema:"the symbol whose source code to return"`
	Path   string `json:"path,omitempty" jsonschema:"project directory; defaults to cwd"`
}

type contextInput struct {
	Symbol string `json:"symbol" jsonschema:"the symbol to gather full context for"`
	Path   string `json:"path,omitempty" jsonschema:"project directory; defaults to cwd"`
	Depth  int    `json:"depth,omitempty" jsonschema:"max hops for the blast-radius count (default 3)"`
}

// emptyInput is for tools that take no arguments (e.g. codemap_projects).
type emptyInput struct{}

type docsInput struct {
	Topic string `json:"topic,omitempty" jsonschema:"optional section: overview, workflow, commands, annotations, accuracy, ecosystem; empty returns the full guide"`
}

type annotateInput struct {
	Symbol string `json:"symbol,omitempty" jsonschema:"symbol (FQN) to annotate; use this OR from+to"`
	From   string `json:"from,omitempty" jsonschema:"path start symbol (with 'to') to annotate a call path"`
	To     string `json:"to,omitempty" jsonschema:"path end symbol (with 'from')"`
	Source string `json:"source,omitempty" jsonschema:"source label: note (default), mongosh, postgres, vidtrace, …"`
	Note   string `json:"note,omitempty" jsonschema:"free-form note text"`
	Data   string `json:"data,omitempty" jsonschema:"opaque data payload, e.g. JSON from a DB query — stored as-is"`
	Path   string `json:"path,omitempty" jsonschema:"project directory; defaults to cwd"`
}

type annotationsInput struct {
	Symbol string `json:"symbol,omitempty" jsonschema:"list annotations on this symbol; omit symbol/from/to for all"`
	From   string `json:"from,omitempty" jsonschema:"with 'to', list annotations on the call path from→to"`
	To     string `json:"to,omitempty" jsonschema:"with 'from', the path end symbol"`
	Path   string `json:"path,omitempty" jsonschema:"project directory; defaults to cwd"`
}

type unannotateInput struct {
	ID   int64  `json:"id" jsonschema:"id of the annotation to remove (from codemap_annotate's result or codemap_annotations)"`
	Path string `json:"path,omitempty" jsonschema:"project directory; defaults to cwd"`
}

// doctorInput is empty: doctor inspects the environment, not a project.
type doctorInput struct{}

type branchSwitchInput struct {
	Path string `json:"path,omitempty" jsonschema:"repository directory; defaults to cwd"`
	To   string `json:"to,omitempty" jsonschema:"branch to switch the index to; defaults to the current git branch"`
	From string `json:"from,omitempty" jsonschema:"branch being left; defaults to the last active branch"`
}

type cacheInput struct {
	Path string `json:"path,omitempty" jsonschema:"project directory; defaults to cwd"`
}

type cacheListInput struct {
	Path string `json:"path,omitempty" jsonschema:"project directory; defaults to cwd"`
}

type cacheDropInput struct {
	StashID string `json:"stash_id" jsonschema:"fcheap stash ID to remove from the cache vault"`
	Path    string `json:"path,omitempty" jsonschema:"project directory; defaults to cwd"`
	All     bool   `json:"all,omitempty" jsonschema:"drop ALL cached indexes for this project (not just one)"`
}

func (s *Server) register() {
	sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
		Name:        "codemap_init",
		Description: "Register a project directory with codemap.",
	}, s.handleInit)
	sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
		Name:        "codemap_index",
		Description: "Index (or reindex) a project: extract its code graph and embed nodes. Incremental by default.",
	}, s.handleIndex)
	sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
		Name:        "codemap_status",
		Description: "Show index statistics for a project (nodes, edges, languages, kinds) AND index freshness: a 'stale' field counts files changed/new/deleted since the last index. Check it first — if stale is non-zero, call codemap_index before trusting query results, which are computed from the indexed snapshot, not live files. A 'daemon' object is present when a background daemon is auto-reindexing the project (so stale is unlikely to drift).",
	}, s.handleStatus)
	sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
		Name:        "codemap_semantic",
		Description: "Semantic search across the code graph: find code by meaning, ranked by similarity.",
	}, s.handleSemantic)
	sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
		Name:        "codemap_callers",
		Description: "List the functions/methods that call a given symbol. Pass precise=true for exact, gopls-resolved callers (Go) instead of the fast name-based graph.",
	}, s.handleCallers)
	sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
		Name:        "codemap_callees",
		Description: "List the functions/methods that a given symbol calls. Pass precise=true for exact, gopls-resolved callees (Go).",
	}, s.handleCallees)
	sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
		Name:        "codemap_impact",
		Description: "Impact analysis for a symbol: definition sites, direct callers, the transitive blast radius (everything affected by a change), and which tests cover those paths. The flagship query — one call replaces many file reads.",
	}, s.handleImpact)
	sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
		Name:        "codemap_related_files",
		Description: "Files structurally related to a file via the call/test graph: the files of its callers, its callees, and the tests covering its symbols, each with a reason (caller|callee|test) and a confidence. Graph-accurate alternative to import-text heuristics; returns {indexed:false} when the project isn't indexed.",
	}, s.handleRelatedFiles)
	sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
		Name:        "codemap_symbol_at",
		Description: "Resolve a file:line position to its enclosing symbol (FQN, kind, line range). The entry point for joining external file:line results (search hits, stack traces, diffs) onto the code graph. resolution is exact|enclosing|none.",
	}, s.handleSymbolAt)
	sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
		Name:        "codemap_secret_impact",
		Description: "Code blast radius of rotating secret keys: for each key NAME, the symbols that read it (os.Getenv/os.environ/process.env), the transitive callers affected, and the covering tests (untested=true is a loud warning). Operates on key NAMES only — never reads, requests, or returns secret values. Pairs with tinyvault's value-free key inventory. blast radius is name-based unless the index is precise (precise:false note); reindex --precise for exact figures.",
	}, s.handleSecretImpact)
	sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
		Name:        "codemap_hotspots",
		Description: "List the most-referenced symbols (hubs) in a project.",
	}, s.handleHotspots)
	sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
		Name:        "codemap_orphans",
		Description: "List functions/methods with no callers (dead-code candidates). Follows functions wired by value (handlers like cobra RunE / mux.HandleFunc) and excludes methods that implement well-known stdlib interfaces (error/Stringer/Unwrap/json.Marshaler), so those aren't falsely flagged; still blind to custom-interface-dispatch/reflection callers, so treat results as candidates.",
	}, s.handleOrphans)
	sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
		Name:        "codemap_path",
		Description: "Find the shortest call path between two symbols.",
	}, s.handlePath)
	sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
		Name:        "codemap_symbols",
		Description: "List the symbols defined in a file (functions, types, methods, tests) with line ranges — a structured alternative to reading the file.",
	}, s.handleSymbols)
	sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
		Name:        "codemap_find",
		Description: "Find symbols by name (case-insensitive substring over names/FQNs). Fast and offline — no embeddings needed, unlike codemap_semantic.",
	}, s.handleFind)
	sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
		Name:        "codemap_source",
		Description: "Return a symbol's source code (the implementation behind its signature), read from the indexed file's line range — so you can read a definition without opening the whole file.",
	}, s.handleSource)
	sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
		Name:        "codemap_context",
		Description: "Everything about a symbol in ONE call: its definition(s) with source, who calls it, what it calls, the tests covering it, the blast-radius size, and any pinned annotations. Prefer this when orienting on an unfamiliar symbol — it replaces separate codemap_source + codemap_callers + codemap_callees + codemap_impact round-trips. The callers/callees/tests lists are capped to keep the bundle small; callers_total/callees_total/tests_total give the true counts — call codemap_callers/codemap_callees/codemap_impact for a complete list when a total exceeds what's shown.",
	}, s.handleContext)
	sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
		Name:        "codemap_projects",
		Description: "List every project registered with codemap and its index size (nodes, edges, files) — discover what's indexed. Queries target one project at a time (via path/cwd).",
	}, s.handleProjects)
	sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
		Name:        "codemap_docs",
		Description: "Learn how to use codemap effectively: an agent guide covering the index-first workflow, which tool to use for what, the accuracy model (when to pass precise:true), and how codemap fits the local toolchain. Optional 'topic' (overview/workflow/commands/annotations/accuracy/ecosystem).",
	}, s.handleDocs)
	sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
		Name:        "codemap_annotate",
		Description: "Pin a note and/or external data (e.g. DB rows from mongosh/postgres, or a finding) to a symbol (pass 'symbol') or a call path (pass 'from'+'to'). 'data' is stored opaquely. Annotations persist across reindex — use them as the harness's knowledge layer over the graph.",
	}, s.handleAnnotate)
	sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
		Name:        "codemap_annotations",
		Description: "List annotations: all in the project (no args), on a 'symbol', or on a 'from'→'to' call path. A 'dangling' list flags annotation ids whose target no longer matches an indexed symbol (renamed/removed) — prune them with codemap_unannotate or re-add.",
	}, s.handleAnnotations)
	sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
		Name:        "codemap_unannotate",
		Description: "Remove an annotation by 'id' (from codemap_annotate's result or codemap_annotations) — so the knowledge layer can be corrected and pruned, not only appended to.",
	}, s.handleUnannotate)
	sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
		Name:        "codemap_doctor",
		Description: "Check the environment codemap runs in — go toolchain, gopls, the language servers (TypeScript/JavaScript, Python), and Ollama embeddings — each with a present/missing flag and an install hint. Use it to diagnose why a language isn't being indexed or why semantic search is unavailable.",
	}, s.handleDoctor)
	sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
		Name:        "codemap_branch_status",
		Description: "Show the git branch/commit state used to key per-branch index snapshots (read-only): current branch, HEAD sha, detached, and the stable repo/branch keys.",
	}, s.handleBranchStatus)
	sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
		Name:        "codemap_branch_switch",
		Description: "Switch the code index to a git branch: snapshots the current branch's index into fcheap and restores the target branch's snapshot (or reindexes when stale/absent), so the graph follows the working tree with no full reindex. Defaults 'to' to the current git branch; a non-git dir or detached HEAD is a no-op.",
	}, s.handleBranchSwitch)
	sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
		Name:        "codemap_cache_save",
		Description: "Save the current project index (graph + vectors) to the fcheap stash vault as a content-addressed cache entry. The cache key is a tree hash of all indexed file paths+content hashes, so two branches with identical code share one entry. Best-effort — returns a note if fcheap isn't available.",
	}, s.handleCacheSave)
	sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
		Name:        "codemap_cache_restore",
		Description: "Restore a project index from a matching fcheap cache entry (same tree hash + embedding profile), skipping extraction and embedding entirely. Returns the restored stash ID and tree hash, or a 'no matching cache' note.",
	}, s.handleCacheRestore)
	sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
		Name:        "codemap_cache_list",
		Description: "List cached indexes for a project in the fcheap vault, with stash IDs, tree hashes, and dates.",
	}, s.handleCacheList)
	sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
		Name:        "codemap_cache_drop",
		Description: "Remove a cached index from the fcheap vault by stash_id, or all cached indexes for the project when all=true. Frees disk space when indexes are no longer needed.",
	}, s.handleCacheDrop)
}

// ---- handlers (thin: resolve path, call Service, return JSON) ----

func (s *Server) handleInit(_ context.Context, _ *sdkmcp.CallToolRequest, in pathInput) (*sdkmcp.CallToolResult, any, error) {
	rep, err := s.svc.Init(cwdOf(in.Path), false)
	return result(rep, err)
}

func (s *Server) handleIndex(ctx context.Context, _ *sdkmcp.CallToolRequest, in indexInput) (*sdkmcp.CallToolResult, any, error) {
	rep, err := s.svc.Index(ctx, cwdOf(in.Path), index.Options{Reindex: in.Reindex, Precise: in.Precise}, !in.NoEmbed)
	return result(rep, err)
}

func (s *Server) handleStatus(_ context.Context, _ *sdkmcp.CallToolRequest, in pathInput) (*sdkmcp.CallToolResult, any, error) {
	rep, err := s.svc.Status(cwdOf(in.Path))
	// Surface index drift (changed/new/deleted files since indexing) so the agent
	// knows whether to call codemap_index before trusting query results.
	if err == nil && rep != nil && rep.Registered {
		if st, sErr := s.svc.Staleness(cwdOf(in.Path)); sErr == nil {
			rep.Stale = st
		}
	}
	if err != nil || rep == nil {
		return result(rep, err)
	}
	// Attach live background-daemon state (nil if none is running) so the agent
	// knows whether the index is being kept fresh automatically.
	return result(daemon.AttachStatus(rep), nil)
}

func (s *Server) handleBranchStatus(ctx context.Context, _ *sdkmcp.CallToolRequest, in pathInput) (*sdkmcp.CallToolResult, any, error) {
	st, err := s.svc.BranchStatus(ctx, cwdOf(in.Path))
	return result(st, err)
}

func (s *Server) handleBranchSwitch(ctx context.Context, _ *sdkmcp.CallToolRequest, in branchSwitchInput) (*sdkmcp.CallToolResult, any, error) {
	root := cwdOf(in.Path)
	to := in.To
	if to == "" {
		if st, serr := s.svc.BranchStatus(ctx, root); serr == nil {
			to = st.Branch
		}
	}
	if to == "" {
		return result(nil, fmt.Errorf("no target branch (detached HEAD or not a git repository) — pass 'to'"))
	}
	if err := s.svc.BranchSwitch(ctx, root, in.From, to); err != nil {
		return result(nil, err)
	}
	return result(map[string]string{"switched_to": to}, nil)
}

func (s *Server) handleCacheSave(ctx context.Context, _ *sdkmcp.CallToolRequest, in cacheInput) (*sdkmcp.CallToolResult, any, error) {
	stashID, treeHash, err := s.svc.CacheSave(ctx, cwdOf(in.Path))
	if err != nil {
		return result(nil, err)
	}
	return result(map[string]string{"action": "saved", "stash_id": stashID, "tree_hash": treeHash}, nil)
}

func (s *Server) handleCacheRestore(ctx context.Context, _ *sdkmcp.CallToolRequest, in cacheInput) (*sdkmcp.CallToolResult, any, error) {
	restored, stashID, err := s.svc.CacheRestore(ctx, cwdOf(in.Path))
	if err != nil {
		return result(nil, err)
	}
	action := "miss"
	if restored {
		action = "restored"
	}
	return result(map[string]string{"action": action, "stash_id": stashID}, nil)
}

func (s *Server) handleCacheList(ctx context.Context, _ *sdkmcp.CallToolRequest, in cacheListInput) (*sdkmcp.CallToolResult, any, error) {
	rep, err := s.svc.CacheList(ctx, cwdOf(in.Path), false)
	return result(rep, err)
}

func (s *Server) handleCacheDrop(ctx context.Context, _ *sdkmcp.CallToolRequest, in cacheDropInput) (*sdkmcp.CallToolResult, any, error) {
	dropped, err := s.svc.CacheDrop(ctx, cwdOf(in.Path), in.StashID, in.All)
	return result(map[string]any{"dropped": dropped}, err)
}

func (s *Server) handleSemantic(ctx context.Context, _ *sdkmcp.CallToolRequest, in semanticInput) (*sdkmcp.CallToolResult, any, error) {
	if r, v, stop := s.notIndexed(in.Path); stop {
		return r, v, nil
	}
	rep, err := s.svc.Semantic(ctx, cwdOf(in.Path), in.Query, in.TopK)
	return result(rep, err)
}

func (s *Server) handleCallers(ctx context.Context, _ *sdkmcp.CallToolRequest, in symbolQueryInput) (*sdkmcp.CallToolResult, any, error) {
	if r, v, stop := s.notIndexed(in.Path); stop {
		return r, v, nil
	}
	if in.Precise {
		rep, err := s.svc.PreciseCallers(ctx, cwdOf(in.Path), in.Symbol)
		return result(rep, err)
	}
	rep, err := s.svc.Callers(cwdOf(in.Path), in.Symbol)
	return result(rep, err)
}

func (s *Server) handleCallees(ctx context.Context, _ *sdkmcp.CallToolRequest, in symbolQueryInput) (*sdkmcp.CallToolResult, any, error) {
	if r, v, stop := s.notIndexed(in.Path); stop {
		return r, v, nil
	}
	if in.Precise {
		rep, err := s.svc.PreciseCallees(ctx, cwdOf(in.Path), in.Symbol)
		return result(rep, err)
	}
	rep, err := s.svc.Callees(cwdOf(in.Path), in.Symbol)
	return result(rep, err)
}

func (s *Server) handleImpact(_ context.Context, _ *sdkmcp.CallToolRequest, in impactInput) (*sdkmcp.CallToolResult, any, error) {
	if r, v, stop := s.notIndexed(in.Path); stop {
		return r, v, nil
	}
	rep, err := s.svc.Impact(cwdOf(in.Path), in.Symbol, in.Depth)
	return result(rep, err)
}

func (s *Server) handleRelatedFiles(_ context.Context, _ *sdkmcp.CallToolRequest, in relatedFilesInput) (*sdkmcp.CallToolResult, any, error) {
	rep, err := s.svc.RelatedFiles(cwdOf(in.Path), in.File)
	return result(rep, err)
}

func (s *Server) handleSymbolAt(_ context.Context, _ *sdkmcp.CallToolRequest, in symbolAtInput) (*sdkmcp.CallToolResult, any, error) {
	rep, err := s.svc.SymbolAt(cwdOf(in.Path), in.File, in.Line)
	return result(rep, err)
}

func (s *Server) handleSecretImpact(_ context.Context, _ *sdkmcp.CallToolRequest, in secretImpactInput) (*sdkmcp.CallToolResult, any, error) {
	rep, err := s.svc.SecretImpact(cwdOf(in.Path), in.Keys, in.Depth)
	return result(rep, err)
}

func (s *Server) handleHotspots(_ context.Context, _ *sdkmcp.CallToolRequest, in limitInput) (*sdkmcp.CallToolResult, any, error) {
	if r, v, stop := s.notIndexed(in.Path); stop {
		return r, v, nil
	}
	rep, err := s.svc.Hotspots(cwdOf(in.Path), in.Top)
	return result(rep, err)
}

func (s *Server) handleOrphans(_ context.Context, _ *sdkmcp.CallToolRequest, in limitInput) (*sdkmcp.CallToolResult, any, error) {
	if r, v, stop := s.notIndexed(in.Path); stop {
		return r, v, nil
	}
	rep, err := s.svc.Orphans(cwdOf(in.Path), in.Top)
	return result(rep, err)
}

func (s *Server) handlePath(_ context.Context, _ *sdkmcp.CallToolRequest, in pathQueryInput) (*sdkmcp.CallToolResult, any, error) {
	if r, v, stop := s.notIndexed(in.Path); stop {
		return r, v, nil
	}
	rep, err := s.svc.Path(cwdOf(in.Path), in.From, in.To)
	return result(rep, err)
}

func (s *Server) handleSymbols(_ context.Context, _ *sdkmcp.CallToolRequest, in symbolsInput) (*sdkmcp.CallToolResult, any, error) {
	if r, v, stop := s.notIndexed(in.Path); stop {
		return r, v, nil
	}
	rep, err := s.svc.Symbols(cwdOf(in.Path), in.File)
	return result(rep, err)
}

func (s *Server) handleFind(_ context.Context, _ *sdkmcp.CallToolRequest, in findInput) (*sdkmcp.CallToolResult, any, error) {
	if r, v, stop := s.notIndexed(in.Path); stop {
		return r, v, nil
	}
	rep, err := s.svc.FindSymbols(cwdOf(in.Path), in.Query, in.Top)
	return result(rep, err)
}

func (s *Server) handleSource(_ context.Context, _ *sdkmcp.CallToolRequest, in sourceInput) (*sdkmcp.CallToolResult, any, error) {
	if r, v, stop := s.notIndexed(in.Path); stop {
		return r, v, nil
	}
	rep, err := s.svc.Source(cwdOf(in.Path), in.Symbol)
	return result(rep, err)
}

func (s *Server) handleContext(_ context.Context, _ *sdkmcp.CallToolRequest, in contextInput) (*sdkmcp.CallToolResult, any, error) {
	if r, v, stop := s.notIndexed(in.Path); stop {
		return r, v, nil
	}
	rep, err := s.svc.Context(cwdOf(in.Path), in.Symbol, in.Depth)
	return result(rep, err)
}

func (s *Server) handleProjects(_ context.Context, _ *sdkmcp.CallToolRequest, _ emptyInput) (*sdkmcp.CallToolResult, any, error) {
	rep, err := s.svc.Projects()
	return result(rep, err)
}

func (s *Server) handleDocs(_ context.Context, _ *sdkmcp.CallToolRequest, in docsInput) (*sdkmcp.CallToolResult, any, error) {
	return &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: app.Docs(in.Topic)}},
	}, nil, nil
}

func (s *Server) handleAnnotate(_ context.Context, _ *sdkmcp.CallToolRequest, in annotateInput) (*sdkmcp.CallToolResult, any, error) {
	if in.From != "" && in.To != "" {
		id, target, matched, err := s.svc.AnnotatePath(cwdOf(in.Path), in.From, in.To, in.Source, in.Note, in.Data)
		out := map[string]any{"id": id, "kind": "path", "target": target, "matched": matched}
		if err == nil && !matched {
			out["note"] = "one or both path endpoints aren't indexed symbols — saved, but it won't surface until they are"
		}
		return result(out, err)
	}
	if in.Symbol != "" {
		id, matched, err := s.svc.AnnotateNode(cwdOf(in.Path), in.Symbol, in.Source, in.Note, in.Data)
		out := map[string]any{"id": id, "kind": "node", "target": in.Symbol, "matched": matched}
		if err == nil && !matched {
			out["note"] = "no indexed symbol matches this target — saved, but it won't surface in queries until one does (typo, or not indexed yet?)"
		}
		return result(out, err)
	}
	return errResult("annotate: provide 'symbol', or both 'from' and 'to'"), nil, nil
}

func (s *Server) handleAnnotations(_ context.Context, _ *sdkmcp.CallToolRequest, in annotationsInput) (*sdkmcp.CallToolResult, any, error) {
	cwd := cwdOf(in.Path)
	switch {
	case in.From != "" && in.To != "":
		return result(s.svc.PathAnnotations(cwd, in.From, in.To))
	case in.Symbol != "":
		return result(s.svc.NodeAnnotations(cwd, in.Symbol))
	default:
		return result(s.svc.AllAnnotations(cwd))
	}
}

func (s *Server) handleUnannotate(_ context.Context, _ *sdkmcp.CallToolRequest, in unannotateInput) (*sdkmcp.CallToolResult, any, error) {
	removed, err := s.svc.RemoveAnnotation(cwdOf(in.Path), in.ID)
	out := map[string]any{"id": in.ID, "removed": removed}
	if err == nil && !removed {
		out["note"] = "no annotation with that id in this project"
	}
	return result(out, err)
}

func (s *Server) handleDoctor(ctx context.Context, _ *sdkmcp.CallToolRequest, _ doctorInput) (*sdkmcp.CallToolResult, any, error) {
	return result(s.svc.Doctor(ctx), nil)
}

// ---- helpers ----

// notIndexed short-circuits a query handler when the project at path hasn't been
// indexed yet: it returns a structured {indexed:false,…} result so an agent gets a
// clear "call codemap_index first" signal instead of empty results that read as
// real "no callers" / "no matches" answers. stop is false when the project is
// indexed and the handler should proceed normally.
func (s *Server) notIndexed(path string) (res *sdkmcp.CallToolResult, payload any, stop bool) {
	indexed, name, err := s.svc.Indexed(cwdOf(path))
	if err != nil {
		r, v, _ := result(nil, err)
		return r, v, true
	}
	if !indexed {
		r, v, _ := result(map[string]any{
			"project": name,
			"indexed": false,
			"note":    "project not indexed — call codemap_index first",
		}, nil)
		return r, v, true
	}
	return nil, nil, false
}

func cwdOf(path string) string {
	if path != "" {
		return config.ExpandPath(path)
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

func result(v any, err error) (*sdkmcp.CallToolResult, any, error) {
	if err != nil {
		return errResult(err.Error()), nil, nil
	}
	// Indented JSON without HTML escaping — the inner result has no HTML context,
	// so <, >, & stay literal (e.g. a path target "A -> B", generics Array<T>);
	// the go-sdk re-escapes this string correctly in the JSON-RPC envelope.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if mErr := enc.Encode(v); mErr != nil {
		return errResult(mErr.Error()), nil, nil
	}
	return &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: string(bytes.TrimRight(buf.Bytes(), "\n"))}},
	}, v, nil
}

func errResult(msg string) *sdkmcp.CallToolResult {
	return &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "Error: " + msg}},
		IsError: true,
	}
}
