// Package mcp exposes codemap over the Model Context Protocol (stdio). It is a
// THIN layer: every handler resolves arguments and delegates to internal/app,
// then returns JSON for the agent. It uses the official go-sdk, whose
// StdioTransport emits newline-delimited JSON-RPC (required by Claude Code /
// Codex / OpenCode — Content-Length framing makes them report "failed to
// connect"). LSP's Content-Length framing must never leak in here.
package mcp

import (
	"context"
	"encoding/json"
	"os"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/abdul-hamid-achik/codemap/internal/app"
	"github.com/abdul-hamid-achik/codemap/internal/config"
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
named Foo. Pass precise:true to codemap_callers/codemap_callees for exact gopls resolution (Go);
treat codemap_hotspots/codemap_orphans as name-based (they can over- or under-count same-named
methods). Precise resolution degrades to name-based (with a "note" in the result) when gopls is
unavailable, so precise:true is never a hard failure. Likewise codemap_semantic returns a "note"
(not an error) when the project has no embeddings — fall back to codemap_find.

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
	Precise bool   `json:"precise,omitempty" jsonschema:"resolve call edges exactly with go/types (Go; needs the go toolchain) — eliminates same-named over-matching"`
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

// emptyInput is for tools that take no arguments (e.g. codemap_projects).
type emptyInput struct{}

type docsInput struct {
	Topic string `json:"topic,omitempty" jsonschema:"optional section: overview, workflow, commands, accuracy, ecosystem; empty returns the full guide"`
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
		Description: "Show index statistics for a project (nodes, edges, languages, kinds).",
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
		Name:        "codemap_hotspots",
		Description: "List the most-referenced symbols (hubs) in a project.",
	}, s.handleHotspots)
	sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
		Name:        "codemap_orphans",
		Description: "List functions/methods with no callers (dead-code candidates).",
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
		Name:        "codemap_projects",
		Description: "List every project registered with codemap and its index size (nodes, edges, files) — discover what's indexed. Queries target one project at a time (via path/cwd).",
	}, s.handleProjects)
	sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
		Name:        "codemap_docs",
		Description: "Learn how to use codemap effectively: an agent guide covering the index-first workflow, which tool to use for what, the accuracy model (when to pass precise:true), and how codemap fits the local toolchain. Optional 'topic' (overview/workflow/commands/accuracy/ecosystem).",
	}, s.handleDocs)
	sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
		Name:        "codemap_annotate",
		Description: "Pin a note and/or external data (e.g. DB rows from mongosh/postgres, or a finding) to a symbol (pass 'symbol') or a call path (pass 'from'+'to'). 'data' is stored opaquely. Annotations persist across reindex — use them as the harness's knowledge layer over the graph.",
	}, s.handleAnnotate)
	sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
		Name:        "codemap_annotations",
		Description: "List annotations: all in the project (no args), on a 'symbol', or on a 'from'→'to' call path.",
	}, s.handleAnnotations)
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
	return result(rep, err)
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
	b, mErr := json.MarshalIndent(v, "", "  ")
	if mErr != nil {
		return errResult(mErr.Error()), nil, nil
	}
	return &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: string(b)}},
	}, v, nil
}

func errResult(msg string) *sdkmcp.CallToolResult {
	return &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "Error: " + msg}},
		IsError: true,
	}
}
