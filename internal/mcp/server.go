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

const instructions = `codemap is a local code knowledge graph (structure + semantics).
Workflow: codemap_index a project once, then query it.
- codemap_semantic finds code by meaning ("jwt validation middleware").
- codemap_callers lists what calls a symbol.
- codemap_status shows index size.
Each tool takes an optional "path" (the project directory); it defaults to the
server's working directory. Results are JSON.`

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
}

type semanticInput struct {
	Query string `json:"query" jsonschema:"natural-language description of the code to find"`
	Path  string `json:"path,omitempty" jsonschema:"project directory; defaults to cwd"`
	TopK  int    `json:"top_k,omitempty" jsonschema:"maximum results (default 10)"`
}

type symbolInput struct {
	Symbol string `json:"symbol" jsonschema:"the symbol name to look up"`
	Path   string `json:"path,omitempty" jsonschema:"project directory; defaults to cwd"`
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
		Description: "List the functions/methods that call a given symbol.",
	}, s.handleCallers)
	sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
		Name:        "codemap_callees",
		Description: "List the functions/methods that a given symbol calls.",
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
}

// ---- handlers (thin: resolve path, call Service, return JSON) ----

func (s *Server) handleInit(_ context.Context, _ *sdkmcp.CallToolRequest, in pathInput) (*sdkmcp.CallToolResult, any, error) {
	rep, err := s.svc.Init(cwdOf(in.Path), false)
	return result(rep, err)
}

func (s *Server) handleIndex(ctx context.Context, _ *sdkmcp.CallToolRequest, in indexInput) (*sdkmcp.CallToolResult, any, error) {
	rep, err := s.svc.Index(ctx, cwdOf(in.Path), index.Options{Reindex: in.Reindex}, !in.NoEmbed)
	return result(rep, err)
}

func (s *Server) handleStatus(_ context.Context, _ *sdkmcp.CallToolRequest, in pathInput) (*sdkmcp.CallToolResult, any, error) {
	rep, err := s.svc.Status(cwdOf(in.Path))
	return result(rep, err)
}

func (s *Server) handleSemantic(ctx context.Context, _ *sdkmcp.CallToolRequest, in semanticInput) (*sdkmcp.CallToolResult, any, error) {
	rep, err := s.svc.Semantic(ctx, cwdOf(in.Path), in.Query, in.TopK)
	return result(rep, err)
}

func (s *Server) handleCallers(_ context.Context, _ *sdkmcp.CallToolRequest, in symbolInput) (*sdkmcp.CallToolResult, any, error) {
	rep, err := s.svc.Callers(cwdOf(in.Path), in.Symbol)
	return result(rep, err)
}

func (s *Server) handleCallees(_ context.Context, _ *sdkmcp.CallToolRequest, in symbolInput) (*sdkmcp.CallToolResult, any, error) {
	rep, err := s.svc.Callees(cwdOf(in.Path), in.Symbol)
	return result(rep, err)
}

func (s *Server) handleImpact(_ context.Context, _ *sdkmcp.CallToolRequest, in impactInput) (*sdkmcp.CallToolResult, any, error) {
	rep, err := s.svc.Impact(cwdOf(in.Path), in.Symbol, in.Depth)
	return result(rep, err)
}

func (s *Server) handleHotspots(_ context.Context, _ *sdkmcp.CallToolRequest, in limitInput) (*sdkmcp.CallToolResult, any, error) {
	rep, err := s.svc.Hotspots(cwdOf(in.Path), in.Top)
	return result(rep, err)
}

func (s *Server) handleOrphans(_ context.Context, _ *sdkmcp.CallToolRequest, in limitInput) (*sdkmcp.CallToolResult, any, error) {
	rep, err := s.svc.Orphans(cwdOf(in.Path), in.Top)
	return result(rep, err)
}

func (s *Server) handlePath(_ context.Context, _ *sdkmcp.CallToolRequest, in pathQueryInput) (*sdkmcp.CallToolResult, any, error) {
	rep, err := s.svc.Path(cwdOf(in.Path), in.From, in.To)
	return result(rep, err)
}

// ---- helpers ----

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
