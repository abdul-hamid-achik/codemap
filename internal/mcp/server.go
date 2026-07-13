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
	"strings"
	"sync"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/abdul-hamid-achik/codemap/internal/app"
	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/daemon"
	"github.com/abdul-hamid-achik/codemap/internal/index"
	"github.com/abdul-hamid-achik/codemap/internal/version"
)

// Profile selects which subset of MCP tools NewServer registers. ProfileFull
// (the default, back-compat) registers every tool (39); ProfileCore
// registers only coreTools — the lean set the canonical playbook (docs.go +
// playbook.go, rendered via app.RenderPlaybook) actually teaches an agent to
// call, plus codemap_docs for self-discovery. This exists because a lean MCP
// server matters to schema-token budgets and to harnesses that cap total
// tool count (Cursor caps ~40 across ALL servers; see bench/README.md's
// "two runs disagree" measurement of +95% input tokens from 39 tool schemas
// riding in every session).
const (
	ProfileFull = "full"
	ProfileCore = "core"
)

// coreTools is the ProfileCore tool set. Every name here is either taught by
// the canonical playbook/docs (see TestCoreProfileCoversTaughtTools, which
// pins the invariant that a taught tool can never be missing here) or added
// because honesty requires it even though it isn't explicitly taught:
// codemap_docs (so a core-profile agent can still self-discover the full
// guide) and codemap_status (index staleness — already taught, kept for
// clarity). Everything else (init, doctor, projects, symbols, symbol_at,
// related_files, secret_impact, required_keys, annotate/annotations/
// unannotate, branch_status/branch_switch, cache_save/restore/list/drop) is
// admin/ecosystem surface, available only under ProfileFull.
var coreTools = map[string]bool{
	"codemap_callees":       true,
	"codemap_callers":       true,
	"codemap_context":       true,
	"codemap_context_batch": true,
	"codemap_coverage":      true,
	"codemap_dependencies":  true,
	"codemap_docs":          true,
	"codemap_file_impact":   true,
	"codemap_find":          true,
	"codemap_grep":          true,
	"codemap_hotspots":      true,
	"codemap_impact":        true,
	"codemap_index":         true,
	"codemap_orphans":       true,
	"codemap_path":          true,
	"codemap_read_order":    true,
	"codemap_references":    true,
	"codemap_review":        true,
	"codemap_risk":          true,
	"codemap_semantic":      true,
	"codemap_source":        true,
	"codemap_status":        true,
}

// resolveProfile normalizes cfg.MCP.Profile the same way config.Validate
// does (lowercase, trimmed, empty means full) so a Server built from a
// Config that skipped Validate (rare, e.g. a hand-built test Config) still
// behaves sanely instead of silently registering zero tools.
func resolveProfile(cfg *config.Config) string {
	if cfg == nil {
		return ProfileFull
	}
	p := strings.ToLower(strings.TrimSpace(cfg.MCP.Profile))
	if p == "" {
		return ProfileFull
	}
	return p
}

const instructions = `codemap is a local code knowledge graph: code structure (calls, types,
tests) fused with semantic vectors, queried offline. Index a project once with codemap_index,
then query it — until you do, query tools return {"indexed": false} with a hint to index first.
Every tool takes an optional "path" (project dir; defaults to cwd) and returns JSON.

Find code:
- codemap_semantic — by meaning ("jwt validation middleware"); needs an embedded index.
- codemap_find — by name; fast and offline (no embeddings).
- codemap_grep — by exact text content (a string literal, error message, route, env-var
  name); fast and offline, joins each hit onto its enclosing symbol.

Understand a symbol:
- codemap_impact (flagship) — definition sites, direct callers, the transitive blast radius
  (everything a change affects), and which tests cover it. One call replaces many file reads.
- codemap_callers / codemap_callees — who calls a symbol / what it calls.
- codemap_references — where a function/method is stored or passed as a callback value;
  enclosing scopes, not callers or exact expression lines. It has its own coverage/confidence.
- codemap_source — a symbol's source code; codemap_symbols — a file's outline.
- codemap_context — the one-call bundle for a symbol (def+callers+callees+value refs+tests+blast+notes);
  codemap_context_batch — the same for several symbols at once, plus the callers they share
  (coupling), so you model a whole component in one round-trip.
Results carry each symbol's signature and docstring, so you rarely need to open files.
When a result's short name is shared, project its existing file, start_line, fqn, and kind
fields into a selector object for codemap_source/context/callers/callees/references/impact/path. Selectors
choose one definition without exposing volatile database ids; file+fqn+kind survives line shifts.
When impact/context/callers/callees/risk/source ARE ambiguous, their response already includes
candidates:[{selector,signature,file,start_line}] — the exact merged set — so you don't need a
separate find/symbols round-trip to build that selector.

After you edit: codemap_review — diff-scoped impact + test selection. It reads your git diff
(working tree by default; staged:true, or since:<ref>), finds the changed symbols, and returns
their union blast radius, the covering_tests to RUN, and which changes are untested or hit a
hotspot. Run it after making changes to learn what you affected and what to test — one call
instead of diffing by hand and chaining codemap_impact per symbol. Deleted files are analyzed
from definitions retained in the last index when available; deletion_analysis states completeness,
and its selected tests come before the reindex action that will prune those old definitions.

Before touching a whole file (move/delete/split): codemap_dependencies returns bounded inbound
call/reference/import evidence plus explicit domain coverage. Samples and totals distinguish
confirmed evidence from name-fanout/package/stale candidates. codemap_file_impact adds blast radius,
covering tests, and conservative delete_verdict: only fresh confirmed file-scoped evidence can be
unsafe; candidate-only, package-only, stale, or missing evidence stays unknown.

How careful to be: codemap_risk — a symbol's change-risk as one score + level
(unknown/low/medium/high) from untested coverage, fan-in, cross-package spread, and name ambiguity.
An unavailable call graph is unknown, never low. Triage which edit is riskiest.

Survey a codebase: codemap_read_order (where to START — entrypoints + hubs ranked into a reading
guide; run this on first contact with an unfamiliar repo, then drill the top entries with
codemap_context), codemap_hotspots (hubs), codemap_orphans (dead-code candidates), codemap_status
(index size), codemap_projects (what's indexed).

Accuracy: the graph is name-based, so a cross-package method call (x.Foo()) matches every method
named Foo — codemap_callers/codemap_impact note when a name is ambiguous and codemap_hotspots flags
the inflation. The graph-wide fix is to re-run codemap_index with precise:true — the unified
exact-resolution pass: go/types for Go, language-server callHierarchy for the LSP languages
(TypeScript, JavaScript, Python). It makes graph EDGES exact; when multiple definitions share the
queried name, pass a source selector to keep the query on one definition. The LSP languages have NO
name-based call edges, so for a TS/JS/Python
project precise:true is what gives codemap_callers/impact/hotspots/path a call graph at all. For a
one-off exact answer on Go without reindexing, pass precise:true to codemap_callers/codemap_callees
(gopls; Go only — on the LSP languages, reindex with codemap_index precise:true instead). Precise
resolution degrades to name-based (with a "note" in the result) when the toolchain or module is
unavailable, so precise:true is never a hard failure. Likewise
codemap_semantic returns a "note" (not an error) when the project has no embeddings — fall back to
codemap_find.

Value references are independent of call precision: codemap_references follows stored callback/
registration 'references' edges, and never accepts precise:true. Its partial/unavailable coverage,
stale flag, and per-site confidence remain authoritative even after a precise call-graph index.

Call codemap_docs for the full guide (workflow, every tool, accuracy, and how codemap fits the
local toolchain) — useful when wiring codemap into a harness.`

// instructionsFor appends one sentence under ProfileCore noting that admin/
// ecosystem tools are trimmed from this session, without forking the
// playbook prose above.
func instructionsFor(profile string) string {
	if profile != ProfileCore {
		return instructions
	}
	return instructions + "\n\nprofile: core — admin and ecosystem tools (init, doctor, projects, symbols, symbol_at, related_files, secret_impact, required_keys, annotate/annotations/unannotate, branch_status/branch_switch, cache_save/restore/list/drop) are available under CODEMAP_MCP_PROFILE=full."
}

// Server wraps the go-sdk MCP server over a codemap session.
type Server struct {
	svc     *app.Service
	srv     *sdkmcp.Server
	profile string // ProfileFull or ProfileCore; gates which tools register()

	// operationMu lets ordinary tool calls run concurrently, but gives index
	// exclusive ownership of Session's graph/vector handles for its full
	// lifetime. The veclite flock still enforces exclusion across processes.
	operationMu sync.RWMutex
}

// NewServer builds an MCP server backed by the given session. The tool set
// registered is gated by sess.Config.MCP.Profile (see Profile/coreTools).
func NewServer(sess *app.Session) *Server {
	profile := resolveProfile(sess.Config)
	s := &Server{svc: app.NewService(sess), profile: profile}
	s.srv = sdkmcp.NewServer(
		&sdkmcp.Implementation{Name: "codemap", Version: version.Version},
		&sdkmcp.ServerOptions{Instructions: instructionsFor(profile)},
	)
	s.srv.AddReceivingMiddleware(s.coordinateToolOperations)
	s.register()
	return s
}

// include reports whether tool name should be registered under the server's
// current profile: everything registers under ProfileFull; only coreTools
// register under ProfileCore. Registration-time only — a tool that IS
// registered behaves identically under either profile.
func (s *Server) include(name string) bool {
	return s.profile != ProfileCore || coreTools[name]
}

// coordinateToolOperations serializes codemap_index against every other tool
// call made through this server. In-process coordination is deliberately based
// on object ownership, not PID lock bypass: unrelated processes must still win
// or lose through veclite's filesystem lock.
func (s *Server) coordinateToolOperations(next sdkmcp.MethodHandler) sdkmcp.MethodHandler {
	return func(ctx context.Context, method string, req sdkmcp.Request) (sdkmcp.Result, error) {
		call, ok := req.(*sdkmcp.CallToolRequest)
		if method != "tools/call" || !ok {
			return next(ctx, method, req)
		}
		if call.Params.Name == "codemap_index" {
			s.operationMu.Lock()
			defer s.operationMu.Unlock()
		} else {
			s.operationMu.RLock()
			defer s.operationMu.RUnlock()
		}
		return next(ctx, method, req)
	}
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
	Symbol   string              `json:"symbol,omitempty" jsonschema:"symbol name to look up; omit when selector is provided"`
	Selector *app.SymbolSelector `json:"selector,omitempty" jsonschema:"exact definition selector projected from a result's file/start_line/fqn/kind fields; takes precedence over symbol"`
	Path     string              `json:"path,omitempty" jsonschema:"project directory; defaults to cwd"`
	Precise  bool                `json:"precise,omitempty" jsonschema:"use the language server (gopls) for exact results (Go) — slower, but not inflated by same-named symbols"`
}

type referencesInput struct {
	Symbol   string              `json:"symbol,omitempty" jsonschema:"function/method name to inspect for callback/value wiring; omit when selector is provided"`
	Selector *app.SymbolSelector `json:"selector,omitempty" jsonschema:"exact target definition projected from file/start_line/fqn/kind; takes precedence over symbol"`
	Path     string              `json:"path,omitempty" jsonschema:"project directory; defaults to cwd"`
}

type impactInput struct {
	Symbol   string              `json:"symbol,omitempty" jsonschema:"symbol to analyze; omit when selector is provided"`
	Selector *app.SymbolSelector `json:"selector,omitempty" jsonschema:"exact definition selector projected from file/start_line/fqn/kind; takes precedence over symbol"`
	Path     string              `json:"path,omitempty" jsonschema:"project directory; defaults to cwd"`
	Depth    int                 `json:"depth,omitempty" jsonschema:"max hops for the blast radius (default 3)"`
}

type reviewInput struct {
	Path   string `json:"path,omitempty" jsonschema:"project directory; defaults to cwd"`
	Since  string `json:"since,omitempty" jsonschema:"review everything changed since this git ref (committed + uncommitted); omit to review the whole working tree"`
	Staged bool   `json:"staged,omitempty" jsonschema:"review only staged changes (the git index) instead of the working tree"`
	Depth  int    `json:"depth,omitempty" jsonschema:"max hops for each changed symbol's blast radius (default 3)"`
}

type readOrderInput struct {
	Path  string `json:"path,omitempty" jsonschema:"project directory; defaults to cwd"`
	Query string `json:"query,omitempty" jsonschema:"optional case-insensitive name/path filter to narrow the ranking (e.g. 'http')"`
	Top   int    `json:"top,omitempty" jsonschema:"maximum entries to rank (default 20)"`
}

type relatedFilesInput struct {
	File string `json:"file" jsonschema:"project-relative file path to find related files for"`
	Path string `json:"path,omitempty" jsonschema:"project directory; defaults to cwd"`
}

type dependenciesInput struct {
	File string `json:"file" jsonschema:"project-relative file path to inspect for inbound dependencies"`
	Path string `json:"path,omitempty" jsonschema:"project directory; defaults to cwd"`
}

type fileImpactInput struct {
	File  string `json:"file" jsonschema:"project-relative file path to analyze"`
	Path  string `json:"path,omitempty" jsonschema:"project directory; defaults to cwd"`
	Depth int    `json:"depth,omitempty" jsonschema:"max hops for the file's blast radius (default 3)"`
}

type riskInput struct {
	Symbol   string              `json:"symbol,omitempty" jsonschema:"symbol to score; omit when selector is provided"`
	Selector *app.SymbolSelector `json:"selector,omitempty" jsonschema:"exact definition selector projected from file/start_line/fqn/kind; takes precedence over symbol"`
	Path     string              `json:"path,omitempty" jsonschema:"project directory; defaults to cwd"`
	Depth    int                 `json:"depth,omitempty" jsonschema:"max hops for the fan-in/blast analysis (default 3)"`
}

type symbolAtInput struct {
	File      string             `json:"file,omitempty" jsonschema:"project-relative file path; omit when positions is set"`
	Line      int                `json:"line,omitempty" jsonschema:"1-based line number to resolve to its enclosing symbol; omit when positions is set"`
	Positions []app.FilePosition `json:"positions,omitempty" jsonschema:"batch form: several {file,line} positions resolved in one call (e.g. a pasted stack trace), up to 25; when set, file/line are ignored and the response is a batch report with one result per position"`
	Path      string             `json:"path,omitempty" jsonschema:"project directory; defaults to cwd"`
}

type secretImpactInput struct {
	Keys     []string `json:"keys,omitempty" jsonschema:"secret key NAMES to analyze (e.g. STRIPE_KEY) — names only, never values; optional when via_vault is set; maximum 256 unique names, 256 bytes each"`
	Path     string   `json:"path,omitempty" jsonschema:"project directory; defaults to cwd"`
	Depth    int      `json:"depth,omitempty" jsonschema:"max hops for each key's blast radius (default 3)"`
	ViaVault string   `json:"via_vault,omitempty" jsonschema:"optional: tinyvault project name to also list keys from its inventory"`
	Prefix   string   `json:"prefix,omitempty" jsonschema:"with via_vault, only inventory keys with this prefix (e.g. STRIPE_)"`
}

// P2-01: codemap_required_keys — the MCP twin of the CLI 'required-keys'
// command. Returns the minimal set of secret key NAMES an entrypoint's
// transitive call tree actually reads, for tinyvault least-privilege sealing.
type requiredKeysInput struct {
	Entrypoint string   `json:"entrypoint" jsonschema:"the entrypoint symbol (function/method) to scope from"`
	Keys       []string `json:"keys,omitempty" jsonschema:"candidate key NAMES to check; optional when via_vault is set; maximum 256 unique names, 256 bytes each"`
	Depth      int      `json:"depth,omitempty" jsonschema:"max hops for the forward call-graph closure (default 5)"`
	Path       string   `json:"path,omitempty" jsonschema:"project directory; defaults to cwd"`
	ViaVault   string   `json:"via_vault,omitempty" jsonschema:"optional: tinyvault project whose value-free key inventory supplies candidates"`
	Prefix     string   `json:"prefix,omitempty" jsonschema:"optional: restrict tinyvault inventory candidates to this prefix"`
}

type limitInput struct {
	Path string `json:"path,omitempty" jsonschema:"project directory; defaults to cwd"`
	Top  int    `json:"top,omitempty" jsonschema:"maximum results"`
}

// coverageInput is codemap_coverage's input. Rollups (by_language/by_directory)
// are always included; the bounded per-file list is included only when Files is
// true or any filter is set (a filter is itself a signal the caller wants rows,
// not just the rollup).
type coverageInput struct {
	Path      string `json:"path,omitempty" jsonschema:"project directory; defaults to cwd"`
	Prefix    string `json:"prefix,omitempty" jsonschema:"only include files whose project-relative path starts with this prefix"`
	Language  string `json:"language,omitempty" jsonschema:"only include files of this language (e.g. go, typescript, python)"`
	Uncovered bool   `json:"uncovered,omitempty" jsonschema:"only include files without precise call-graph coverage"`
	Files     bool   `json:"files,omitempty" jsonschema:"include the bounded per-file list even without a filter (rollups are always included)"`
	Top       int    `json:"top,omitempty" jsonschema:"cap on by_directory rows and per-file detail rows (default 200, max 2000)"`
}

type pathQueryInput struct {
	From         string              `json:"from,omitempty" jsonschema:"starting symbol; omit when from_selector is provided"`
	To           string              `json:"to,omitempty" jsonschema:"destination symbol; omit when to_selector is provided"`
	FromSelector *app.SymbolSelector `json:"from_selector,omitempty" jsonschema:"exact starting definition projected from file/start_line/fqn/kind"`
	ToSelector   *app.SymbolSelector `json:"to_selector,omitempty" jsonschema:"exact destination definition projected from file/start_line/fqn/kind"`
	Path         string              `json:"path,omitempty" jsonschema:"project directory; defaults to cwd"`
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

type grepInput struct {
	Pattern    string `json:"pattern" jsonschema:"literal substring or (with regex:true) RE2 regular expression to search indexed file content for"`
	Path       string `json:"path,omitempty" jsonschema:"project directory; defaults to cwd"`
	Regex      bool   `json:"regex,omitempty" jsonschema:"interpret pattern as a Go RE2 regex instead of a literal substring"`
	IgnoreCase bool   `json:"ignore_case,omitempty" jsonschema:"case-insensitive match"`
	Top        int    `json:"top,omitempty" jsonschema:"maximum results (default 100, capped at 1000)"`
}

type sourceInput struct {
	Symbol   string              `json:"symbol,omitempty" jsonschema:"symbol whose source code to return; omit when selector is provided"`
	Selector *app.SymbolSelector `json:"selector,omitempty" jsonschema:"exact definition selector projected from file/start_line/fqn/kind; takes precedence over symbol"`
	Path     string              `json:"path,omitempty" jsonschema:"project directory; defaults to cwd"`
	Brief    bool                `json:"brief,omitempty" jsonschema:"drop each match's source body, keeping signature/doc/location; sets source_omitted:true so you know to re-call without brief for the one definition you actually need"`
}

type contextBatchInput struct {
	Symbols   []string             `json:"symbols" jsonschema:"the symbols to fetch context for in one call (deduped; up to 25)"`
	Selectors []app.SymbolSelector `json:"selectors,omitempty" jsonschema:"exact definitions to include, unioned with symbols and deduped; union with symbols is capped at 25 total"`
	Path      string               `json:"path,omitempty" jsonschema:"project directory; defaults to cwd"`
	Depth     int                  `json:"depth,omitempty" jsonschema:"max hops for each symbol's blast-radius count (default 3)"`
	Brief     bool                 `json:"brief,omitempty" jsonschema:"drop every definition's source body across the batch, keeping signature/doc/location; sets source_omitted:true per definition instead of spending the source_budget"`
}

type contextInput struct {
	Symbol   string              `json:"symbol,omitempty" jsonschema:"symbol to gather full context for; omit when selector is provided"`
	Selector *app.SymbolSelector `json:"selector,omitempty" jsonschema:"exact definition selector projected from file/start_line/fqn/kind; takes precedence over symbol"`
	Path     string              `json:"path,omitempty" jsonschema:"project directory; defaults to cwd"`
	Depth    int                 `json:"depth,omitempty" jsonschema:"max hops for the blast-radius count (default 3)"`
	Brief    bool                `json:"brief,omitempty" jsonschema:"drop each definition's source body, keeping signature/doc/location; sets source_omitted:true so you know to call codemap_source for the one definition you actually need — everything else in the bundle is unchanged"`
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
	Path    string `json:"path,omitempty" jsonschema:"project directory; defaults to cwd"`
	Rebuild bool   `json:"rebuild,omitempty" jsonschema:"reconstruct the list from fcheap (use if the local pointer file is lost)"`
}

type cacheDropInput struct {
	// StashID is the stash id or tree hash to drop (both are accepted so agents
	// can pass what they got from codemap_cache_list). Required unless All=true.
	StashID string `json:"stash_id,omitempty" jsonschema:"stash id OR tree hash from codemap_cache_list; required unless all=true"`
	Path    string `json:"path,omitempty" jsonschema:"project directory; defaults to cwd"`
	All     bool   `json:"all,omitempty" jsonschema:"drop ALL cached indexes for this project (not just one)"`
}

func (s *Server) register() {
	if s.include("codemap_init") {
		sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
			Name:        "codemap_init",
			Description: "Register a project directory with codemap.",
		}, s.handleInit)
	}
	if s.include("codemap_index") {
		sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
			Name:        "codemap_index",
			Description: "Index (or reindex) a project: extract its code graph and embed nodes. Incremental by default.",
		}, s.handleIndex)
	}
	if s.include("codemap_status") {
		sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
			Name:        "codemap_status",
			Description: "Show index statistics for a project (nodes, edges, languages, kinds) AND index freshness: a 'stale' field counts files changed/new/deleted since the last index. Check it first — if stale is non-zero, call codemap_index before trusting query results, which are computed from the indexed snapshot, not live files. A 'daemon' object is present when a background daemon is auto-reindexing the project (so stale is unlikely to drift).",
		}, s.handleStatus)
	}
	if s.include("codemap_semantic") {
		sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
			Name:        "codemap_semantic",
			Description: "Semantic search across the code graph: find code by meaning, ranked by similarity. The result carries a \"fusion\" field naming the vector/BM25 weighting profile used (\"identifier\", \"natural_language\", or \"balanced\").",
		}, s.handleSemantic)
	}
	if s.include("codemap_callers") {
		sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
			Name:        "codemap_callers",
			Description: "List functions/methods that call a symbol. Name-only queries remain backward-compatible and may merge same-named definitions — when ambiguous, the result's candidates:[{selector,signature,file,start_line}] gives the exact merged set so you can re-query one definition without a separate find/symbols lookup. For one exact definition, pass selector:{file,start_line,fqn,kind} projected from any codemap symbol result; the source selector survives reindex and line shifts when file+fqn+kind still match. precise=true optionally resolves it on demand through the language server.",
		}, s.handleCallers)
	}
	if s.include("codemap_callees") {
		sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
			Name:        "codemap_callees",
			Description: "List functions/methods called by a symbol. Pass selector:{file,start_line,fqn,kind} projected from a prior result to select one exact definition instead of merging same-named definitions. An ambiguous name-only query's candidates:[{selector,signature,file,start_line}] gives the exact merged set to re-query with. Pass precise=true for an on-demand language-server resolution.",
		}, s.handleCallees)
	}
	if s.include("codemap_references") {
		sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
			Name:        "codemap_references",
			Description: "List bounded enclosing function/method/file scopes that store or pass a function/method as a callback value. This follows references edges, never calls; source.start_line is the enclosing declaration line, not the exact expression line. Returns definitions, references_total/truncation, stale, reference-specific confirmed|candidate confidence and partial|unavailable coverage, plus independent call_graph honesty. Pass selector:{file,start_line,fqn,kind} for one exact target. Missing sites do not prove no wiring; precise call indexing does not upgrade this evidence.",
		}, s.handleReferences)
	}
	if s.include("codemap_impact") {
		sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
			Name:        "codemap_impact",
			Description: "Impact analysis: definition sites, direct callers, transitive blast radius, and covering tests. Pass selector:{file,start_line,fqn,kind} projected from a find/symbols/context result to analyze one exact definition; name-only input remains supported and honestly reports when same-named definitions are merged, surfacing candidates:[{selector,signature,file,start_line}] — the exact merged set — so you can re-query one definition.",
		}, s.handleImpact)
	}
	if s.include("codemap_review") {
		sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
			Name:        "codemap_review",
			Description: "Diff-scoped impact + regression test selection — the query to run AFTER editing. Maps your git diff (whole working tree by default; staged=true for the index; since=<ref> for everything since a branch point) to the symbols it touches, then returns their union blast_radius, the covering_tests to run (regression test selection), the changed symbols that are untested or are hotspots (many callers), plus stale/resolution honesty signals. Answers 'what did I just affect, and what should I run?' in one call instead of chaining diff parsing + per-symbol codemap_impact. Degrades to a plain changed-file list with a note when the project isn't indexed or isn't a git repo.",
		}, s.handleReview)
	}
	if s.include("codemap_read_order") {
		sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
			Name:        "codemap_read_order",
			Description: "Where to START reading an unfamiliar codebase — a ranked reading guide. Blends call-graph importance (in-degree hubs) with entrypoint heuristics (main(), cmd/ packages, module index files, exported public API) so the symbols that orient you fastest come first, each with a reason and score. Optional 'query' narrows it (name/path substring). Use this on first contact with a repo instead of guessing where to look; pair with codemap_context to then drill the top entries. Resolution is set when there's no call graph (ranking falls back to entrypoint heuristics).",
		}, s.handleReadOrder)
	}
	if s.include("codemap_related_files") {
		sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
			Name:        "codemap_related_files",
			Description: "Files structurally related to a file via the call/test graph: the files of its callers, its callees, and the tests covering its symbols, each with a reason (caller|callee|test) and a confidence. Graph-accurate alternative to import-text heuristics; returns {indexed:false} when the project isn't indexed.",
		}, s.handleRelatedFiles)
	}
	if s.include("codemap_dependencies") {
		sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
			Name:        "codemap_dependencies",
			Description: "Direct inbound dependency evidence for one file, grouped and capped by dependent file and edge kind (calls, value references, imports). Returns stable source→target locations, file-vs-package scope, totals/truncation, stale/call_graph, and complete|partial|unavailable coverage for calls, references, imports, runtime wiring, and external consumers. Use it before a move/delete/split when you need the evidence without the blast/test work of codemap_file_impact. Missing evidence is never proof of safety.",
		}, s.handleDependencies)
	}
	if s.include("codemap_file_impact") {
		sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
			Name:        "codemap_file_impact",
			Description: "File-level impact — 'what happens if I change or DELETE this file?' Returns dependency_evidence grouped by dependent file and edge kind (calls/references/imports), bounded source→target samples with totals/truncation, per-domain coverage, blast radius, tests, and a conservative delete_verdict. File-scoped calls/references prove unsafe; Go imports are package-scoped hints and remain unknown for the exact file. Missing evidence never proves safety while type/value uses, runtime wiring, and external consumers are incomplete; legacy safe_to_delete remains false.",
		}, s.handleFileImpact)
	}
	if s.include("codemap_risk") {
		sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
			Name:        "codemap_risk",
			Description: "Change-risk score in one number (0..1) + level (unknown/low/medium/high), combining coverage, fan-in, cross-package spread, and ambiguity. Pass selector:{file,start_line,fqn,kind} to score one exact definition. An ambiguous name-only query's candidates:[{selector,signature,file,start_line}] gives the exact merged set to re-query with. An unresolved call graph returns unknown, never a reassuring low.",
		}, s.handleRisk)
	}
	if s.include("codemap_symbol_at") {
		sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
			Name:        "codemap_symbol_at",
			Description: "Resolve a file:line position to its enclosing symbol (FQN, kind, line range). The entry point for joining external file:line results (search hits, stack traces, diffs) onto the code graph. resolution is exact|enclosing|none. Pass positions:[{file,line}] instead of file/line to resolve several positions (e.g. a pasted stack trace) in one call — up to 25, each self-reporting resolution:none on a miss.",
		}, s.handleSymbolAt)
	}
	if s.include("codemap_secret_impact") {
		sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
			Name:        "codemap_secret_impact",
			Description: fmt.Sprintf("Code blast radius of rotating secret keys: for each key NAME, the symbols that read it (os.Getenv/os.environ/process.env), the transitive callers affected, and the covering tests (untested=true is a loud warning). Operates on key NAMES only — never reads, requests, or returns secret values. Inputs, including tinyvault inventory, are bounded to %d unique names of at most %d bytes each. blast radius is name-based unless the index is precise (precise:false note); reindex --precise for exact figures.", app.MaxSecretKeyNames, app.MaxSecretKeyNameBytes),
		}, s.handleSecretImpact)
	}
	if s.include("codemap_required_keys") {
		sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
			Name:        "codemap_required_keys",
			Description: fmt.Sprintf("Return the minimal set of secret key NAMES an entrypoint's transitive call tree actually reads — for tinyvault least-privilege sealing (seal/inject only these). Supply keys directly or use via_vault plus optional prefix to read tinyvault's value-free inventory. Inputs are bounded to %d unique names of at most %d bytes each. Only key names and positions are handled, never secret values.", app.MaxSecretKeyNames, app.MaxSecretKeyNameBytes),
		}, s.handleRequiredKeys)
	}
	if s.include("codemap_hotspots") {
		sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
			Name:        "codemap_hotspots",
			Description: "List the most-referenced symbols (hubs) in a project.",
		}, s.handleHotspots)
	}
	if s.include("codemap_orphans") {
		sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
			Name:        "codemap_orphans",
			Description: "List functions/methods with no callers (dead-code candidates). Follows functions wired by value (handlers like cobra RunE / mux.HandleFunc) and excludes methods that implement well-known stdlib interfaces (error/Stringer/Unwrap/json.Marshaler), so those aren't falsely flagged; still blind to custom-interface-dispatch/reflection callers, so treat results as candidates.",
		}, s.handleOrphans)
	}
	if s.include("codemap_coverage") {
		sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
			Name:        "codemap_coverage",
			Description: "Per-file precise call-graph coverage: which files have exact resolution (go/types or language-server callHierarchy) recorded, when, and whether that coverage is stale (this file's on-disk content changed since the last index — independent of codemap_status's project-wide drift count). Rollups by language and by directory (worst-covered first) are always included; pass prefix/language/uncovered filters or files:true for the bounded per-file list (default/max 200/2000 rows, files_total/files_truncated disclose the real count). Complements, does not replace, the per-query call_graph enum: use this to calibrate trust per package BEFORE asking a symbol question, instead of assuming the project's single worst-file confidence everywhere.",
		}, s.handleCoverage)
	}
	if s.include("codemap_path") {
		sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
			Name:        "codemap_path",
			Description: "Find the shortest call path between two symbols. For exact same-name-safe endpoints, pass both from_selector and to_selector as file/start_line/fqn/kind projections from prior results.",
		}, s.handlePath)
	}
	if s.include("codemap_symbols") {
		sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
			Name:        "codemap_symbols",
			Description: "List the symbols defined in a file (functions, types, methods, tests) with line ranges — a structured alternative to reading the file.",
		}, s.handleSymbols)
	}
	if s.include("codemap_find") {
		sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
			Name:        "codemap_find",
			Description: "Find symbols by name (case-insensitive substring over names/FQNs). Fast and offline — no embeddings needed, unlike codemap_semantic.",
		}, s.handleFind)
	}
	if s.include("codemap_grep") {
		sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
			Name:        "codemap_grep",
			Description: "Search the content of every indexed file for a literal substring (or, with regex:true, a Go RE2 pattern) and resolve each hit to its enclosing symbol — file, line, matched_line, symbol, fqn, kind, and a chainable selector. Distinct from codemap_semantic (meaning search) and codemap_find (name search): this is exact text content search. Only covers the indexed file set (same scope as the index, not every file in the repo); reads are live from disk. Capped results carry total/truncated.",
		}, s.handleGrep)
	}
	if s.include("codemap_source") {
		sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
			Name:        "codemap_source",
			Description: "Return source code from the indexed line range. Pass selector:{file,start_line,fqn,kind} projected from a result to fetch exactly one definition; a name-only query still returns every same-named definition, plus candidates:[{selector,signature,file,start_line}] (redundant with matches, present for uniformity with impact/context/callers/callees/risk). brief:true drops each match's source body (keeping signature/doc/location) and sets source_omitted:true — use it to confirm a definition's shape before paying for the full body.",
		}, s.handleSource)
	}
	if s.include("codemap_context") {
		sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
			Name:        "codemap_context",
			Description: "Everything about a symbol in ONE call: source, callers, callees, callback/value references, tests, blast radius, and pinned notes. Pass selector:{file,start_line,fqn,kind} projected from a prior result to keep the entire bundle scoped to one exact definition. Name-only input remains supported; when ambiguous, candidates:[{selector,signature,file,start_line}] gives the exact merged set to re-query with. Uses the indexed graph only; capped lists carry true *_total counts, reference coverage/staleness stays independent of call_graph, and optional failures appear in partial_errors. brief:true drops each definition's source body (keeping signature/doc/location) and sets source_omitted:true on it — everything else in the bundle is unchanged; follow up with codemap_source for the one definition you actually need the body of.",
		}, s.handleContext)
	}
	if s.include("codemap_context_batch") {
		sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
			Name:        "codemap_context_batch",
			Description: "Fetch the codemap_context bundle for SEVERAL symbols in one call — for building a mental model of a component without N round-trips. Returns each symbol's context plus cross-symbol analysis: combined_blast_radius and common_callers (a likely shared entrypoint/coupling point). Graph-only, deduped, and capped at 25; missing symbols land in not_found. Also accepts selectors:[{file,start_line,fqn,kind}] (e.g. from a prior ambiguous call's candidates) unioned with symbols, same 25-item cap — MCP-only, no CLI batch form for selectors; a malformed selector lands in not_found/partial_errors rather than failing the whole call. Aggregate source bodies share a 64 KiB budget reported by source_budget and per-definition source_truncations, while signatures/docs/locations remain complete. partial_errors preserves optional-component failures. brief:true drops every definition's source body across the whole batch (keeping signature/doc/location) and sets source_omitted:true on each instead of spending the source_budget — the cheapest way to orient across many symbols at once.",
		}, s.handleContextBatch)
	}
	if s.include("codemap_projects") {
		sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
			Name:        "codemap_projects",
			Description: "List every project registered with codemap and its index size (nodes, edges, files) — discover what's indexed. Queries target one project at a time (via path/cwd).",
		}, s.handleProjects)
	}
	if s.include("codemap_docs") {
		sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
			Name:        "codemap_docs",
			Description: "Learn how to use codemap effectively: an agent guide covering the index-first workflow, which tool to use for what, the accuracy model (when to pass precise:true), and how codemap fits the local toolchain. Optional 'topic' (overview/workflow/commands/annotations/accuracy/ecosystem).",
		}, s.handleDocs)
	}
	if s.include("codemap_annotate") {
		sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
			Name:        "codemap_annotate",
			Description: "Pin a note and/or external data (e.g. DB rows from mongosh/postgres, or a finding) to a symbol (pass 'symbol') or a call path (pass 'from'+'to'). 'data' is stored opaquely. Annotations persist across reindex — use them as the harness's knowledge layer over the graph.",
		}, s.handleAnnotate)
	}
	if s.include("codemap_annotations") {
		sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
			Name:        "codemap_annotations",
			Description: "List annotations: all in the project (no args), on a 'symbol', or on a 'from'→'to' call path. A 'dangling' list flags annotation ids whose target no longer matches an indexed symbol (renamed/removed) — prune them with codemap_unannotate or re-add.",
		}, s.handleAnnotations)
	}
	if s.include("codemap_unannotate") {
		sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
			Name:        "codemap_unannotate",
			Description: "Remove an annotation by 'id' (from codemap_annotate's result or codemap_annotations) — so the knowledge layer can be corrected and pruned, not only appended to.",
		}, s.handleUnannotate)
	}
	if s.include("codemap_doctor") {
		sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
			Name:        "codemap_doctor",
			Description: "Check the environment codemap runs in — go toolchain, gopls, the language servers (TypeScript/JavaScript, Python), and Ollama embeddings — each with a present/missing flag and an install hint. Use it to diagnose why a language isn't being indexed or why semantic search is unavailable.",
		}, s.handleDoctor)
	}
	if s.include("codemap_branch_status") {
		sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
			Name:        "codemap_branch_status",
			Description: "Show the git branch/commit state used to key per-branch index snapshots (read-only): current branch, HEAD sha, detached, and the stable repo/branch keys.",
		}, s.handleBranchStatus)
	}
	if s.include("codemap_branch_switch") {
		sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
			Name:        "codemap_branch_switch",
			Description: "Switch the code index to a git branch: snapshots the current branch's index into fcheap and restores the target branch's snapshot (or reindexes when stale/absent), so the graph follows the working tree with no full reindex. Defaults 'to' to the current git branch; a non-git dir or detached HEAD is a no-op.",
		}, s.handleBranchSwitch)
	}
	if s.include("codemap_cache_save") {
		sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
			Name:        "codemap_cache_save",
			Description: "Save the current project index (graph + vectors) to the fcheap stash vault as a content-addressed cache entry. The cache key is a tree hash of all indexed file paths+content hashes, so two branches with identical code share one entry. Best-effort — returns a note if fcheap isn't available.",
		}, s.handleCacheSave)
	}
	if s.include("codemap_cache_restore") {
		sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
			Name:        "codemap_cache_restore",
			Description: "Restore a project index from a matching fcheap cache entry (same tree hash + embedding profile), skipping extraction and embedding entirely. Returns the restored stash ID and tree hash, or a 'no matching cache' note.",
		}, s.handleCacheRestore)
	}
	if s.include("codemap_cache_list") {
		sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
			Name:        "codemap_cache_list",
			Description: "List cached indexes for a project in the fcheap vault, with stash IDs, tree hashes, and dates.",
		}, s.handleCacheList)
	}
	if s.include("codemap_cache_drop") {
		sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
			Name:        "codemap_cache_drop",
			Description: "Remove a cached index from the fcheap vault by stash_id, or all cached indexes for the project when all=true. Frees disk space when indexes are no longer needed.",
		}, s.handleCacheDrop)
	}
}

// ---- handlers (thin: resolve path, call Service, return JSON) ----

func (s *Server) handleInit(_ context.Context, _ *sdkmcp.CallToolRequest, in pathInput) (*sdkmcp.CallToolResult, any, error) {
	rep, err := s.svc.Init(cwdOf(in.Path), false)
	return result(rep, err)
}

func (s *Server) handleIndex(ctx context.Context, req *sdkmcp.CallToolRequest, in indexInput) (*sdkmcp.CallToolResult, any, error) {
	root := cwdOf(in.Path)
	// Pin P0-08: same daemon-delegation guard as the CLI. If a daemon is
	// serving THIS project, delegate to it (avoids the veclite lock collision).
	// If a daemon is serving a DIFFERENT project, refuse with an actionable
	// message — better than opening a colliding writer or silently indexing the
	// wrong tree.
	if info := daemon.QueryStatus(); info != nil {
		if ok, reason := daemon.DelegationAllowed(root, info); ok {
			dopts := daemon.ReindexOpts{Reindex: in.Reindex, Precise: in.Precise, NoLSP: false}
			embed := !in.NoEmbed
			dopts.Embed = &embed
			rep, err := daemon.Reindex(dopts)
			if err != nil {
				return result(nil, err)
			}
			return result(rep, nil)
		} else {
			return &sdkmcp.CallToolResult{
				Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: reason}},
				IsError: true,
			}, nil, nil
		}
	}
	// P2-02 (O42): wire MCP progress notifications when the client
	// supplied a progress token. The index.Options.OnFile/OnEmbed hooks
	// are throttled (every ~50ms or every 64 files / 64 embeds) so a
	// multi-minute reindex doesn't look hung or trip client timeouts.
	opts := index.Options{Reindex: in.Reindex, Precise: in.Precise}
	if token := req.Params.GetProgressToken(); token != nil && req.Session != nil {
		notifier := &mcpProgress{ctx: ctx, session: req.Session, token: token}
		opts.OnFile = notifier.onFile
		opts.OnEmbed = notifier.onEmbed
	}
	rep, err := s.svc.Index(ctx, root, opts, !in.NoEmbed)
	return result(rep, err)
}

// mcpProgress throttles per-file and per-embed progress events and
// forwards them to the MCP client via ServerSession.NotifyProgress.
// It's safe to call from any goroutine — NotifyProgress is documented
// as concurrent-safe in go-sdk v1.6.x — and the throttling keeps
// the notification stream at well under 20Hz even on a 10k-file
// project, which is the level most MCP clients (Claude Code,
// Continue, Cursor) handle gracefully.
type mcpProgress struct {
	ctx      context.Context
	session  *sdkmcp.ServerSession
	token    any
	lastFile int64
	lastEmb  int64
}

func (p *mcpProgress) send(progress, total int64, msg string) {
	if p == nil || p.session == nil {
		return
	}
	_ = p.session.NotifyProgress(p.ctx, &sdkmcp.ProgressNotificationParams{
		ProgressToken: p.token,
		Progress:      float64(progress),
		Total:         float64(total),
		Message:       msg,
	})
}

func (p *mcpProgress) onFile(done, total int, rel string) {
	// Throttle: emit at most ~20/sec. done monotonically increases
	// (atomic counter) so this also drops the per-file noise on a
	// 10k-file repo.
	d := int64(done)
	if d-p.lastFile < 32 && d != int64(total) {
		return
	}
	p.lastFile = d
	p.send(d, int64(total), "scanning "+rel)
}

func (p *mcpProgress) onEmbed(done, total int) {
	d := int64(done)
	if d-p.lastEmb < 64 && d != int64(total) {
		return
	}
	p.lastEmb = d
	p.send(d, int64(total), "embedding")
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
		return result(map[string]any{"switched": false, "note": "no target branch (detached HEAD or not a git repository) — pass 'to' explicitly"}, nil)
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
	rep, err := s.svc.CacheList(ctx, cwdOf(in.Path), in.Rebuild)
	return result(rep, err)
}

func (s *Server) handleCacheDrop(ctx context.Context, _ *sdkmcp.CallToolRequest, in cacheDropInput) (*sdkmcp.CallToolResult, any, error) {
	if in.StashID == "" && !in.All {
		return errResult("specify a stash_id/tree_hash or all:true (tip: codemap_cache_list returns the identifier to drop)"), nil, nil
	}
	dropped, err := s.svc.CacheDrop(ctx, cwdOf(in.Path), in.StashID, in.All)
	if err != nil {
		return result(nil, err)
	}
	out := map[string]any{"dropped": dropped}
	if !in.All && dropped == 0 {
		out["matched"] = false
		out["note"] = "no cache entry matches the given identifier (use codemap_cache_list to find the right stash_id or tree_hash)"
	} else {
		out["matched"] = true
	}
	return result(out, nil)
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
	if in.Selector != nil && in.Precise {
		rep, err := s.svc.PreciseCallersBySelector(ctx, cwdOf(in.Path), *in.Selector)
		return result(rep, err)
	}
	if in.Selector != nil {
		rep, err := s.svc.CallersBySelector(cwdOf(in.Path), *in.Selector)
		return result(rep, err)
	}
	if in.Symbol == "" {
		return result(nil, fmt.Errorf("callers needs symbol or selector"))
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
	if in.Selector != nil && in.Precise {
		rep, err := s.svc.PreciseCalleesBySelector(ctx, cwdOf(in.Path), *in.Selector)
		return result(rep, err)
	}
	if in.Selector != nil {
		rep, err := s.svc.CalleesBySelector(cwdOf(in.Path), *in.Selector)
		return result(rep, err)
	}
	if in.Symbol == "" {
		return result(nil, fmt.Errorf("callees needs symbol or selector"))
	}
	if in.Precise {
		rep, err := s.svc.PreciseCallees(ctx, cwdOf(in.Path), in.Symbol)
		return result(rep, err)
	}
	rep, err := s.svc.Callees(cwdOf(in.Path), in.Symbol)
	return result(rep, err)
}

func (s *Server) handleReferences(_ context.Context, _ *sdkmcp.CallToolRequest, in referencesInput) (*sdkmcp.CallToolResult, any, error) {
	if r, v, stop := s.notIndexed(in.Path); stop {
		return r, v, nil
	}
	if in.Selector != nil {
		rep, err := s.svc.ReferencesBySelector(cwdOf(in.Path), *in.Selector)
		return result(rep, err)
	}
	if in.Symbol == "" {
		return result(nil, fmt.Errorf("references needs symbol or selector"))
	}
	rep, err := s.svc.References(cwdOf(in.Path), in.Symbol)
	return result(rep, err)
}

func (s *Server) handleImpact(_ context.Context, _ *sdkmcp.CallToolRequest, in impactInput) (*sdkmcp.CallToolResult, any, error) {
	if r, v, stop := s.notIndexed(in.Path); stop {
		return r, v, nil
	}
	if in.Selector != nil {
		rep, err := s.svc.ImpactBySelector(cwdOf(in.Path), *in.Selector, in.Depth)
		return result(rep, err)
	}
	if in.Symbol == "" {
		return result(nil, fmt.Errorf("impact needs symbol or selector"))
	}
	rep, err := s.svc.Impact(cwdOf(in.Path), in.Symbol, in.Depth)
	return result(rep, err)
}

func (s *Server) handleReview(_ context.Context, _ *sdkmcp.CallToolRequest, in reviewInput) (*sdkmcp.CallToolResult, any, error) {
	mode := "working"
	if in.Staged {
		mode = "staged"
	} else if in.Since != "" {
		mode = "since"
	}
	rep, err := s.svc.Review(cwdOf(in.Path), app.ReviewOpts{Mode: mode, Since: in.Since, Depth: in.Depth})
	return result(rep, err)
}

func (s *Server) handleReadOrder(_ context.Context, _ *sdkmcp.CallToolRequest, in readOrderInput) (*sdkmcp.CallToolResult, any, error) {
	if r, v, stop := s.notIndexed(in.Path); stop {
		return r, v, nil
	}
	rep, err := s.svc.ReadOrder(cwdOf(in.Path), app.ReadOrderOpts{Top: in.Top, Query: in.Query})
	return result(rep, err)
}

func (s *Server) handleRelatedFiles(_ context.Context, _ *sdkmcp.CallToolRequest, in relatedFilesInput) (*sdkmcp.CallToolResult, any, error) {
	rep, err := s.svc.RelatedFiles(cwdOf(in.Path), in.File)
	return result(rep, err)
}

func (s *Server) handleDependencies(_ context.Context, _ *sdkmcp.CallToolRequest, in dependenciesInput) (*sdkmcp.CallToolResult, any, error) {
	if r, v, stop := s.notIndexed(in.Path); stop {
		return r, v, nil
	}
	rep, err := s.svc.Dependencies(cwdOf(in.Path), in.File)
	return result(rep, err)
}

func (s *Server) handleFileImpact(_ context.Context, _ *sdkmcp.CallToolRequest, in fileImpactInput) (*sdkmcp.CallToolResult, any, error) {
	if r, v, stop := s.notIndexed(in.Path); stop {
		return r, v, nil
	}
	rep, err := s.svc.FileImpact(cwdOf(in.Path), in.File, in.Depth)
	return result(rep, err)
}

func (s *Server) handleRisk(_ context.Context, _ *sdkmcp.CallToolRequest, in riskInput) (*sdkmcp.CallToolResult, any, error) {
	if r, v, stop := s.notIndexed(in.Path); stop {
		return r, v, nil
	}
	if in.Selector != nil {
		rep, err := s.svc.RiskBySelector(cwdOf(in.Path), *in.Selector, in.Depth)
		return result(rep, err)
	}
	if in.Symbol == "" {
		return result(nil, fmt.Errorf("risk needs symbol or selector"))
	}
	rep, err := s.svc.Risk(cwdOf(in.Path), in.Symbol, in.Depth)
	return result(rep, err)
}

func (s *Server) handleSymbolAt(_ context.Context, _ *sdkmcp.CallToolRequest, in symbolAtInput) (*sdkmcp.CallToolResult, any, error) {
	if r, v, stop := s.notIndexed(in.Path); stop {
		return r, v, nil
	}
	if len(in.Positions) > 0 {
		rep, err := s.svc.SymbolAtBatch(cwdOf(in.Path), in.Positions)
		return result(rep, err)
	}
	rep, err := s.svc.SymbolAt(cwdOf(in.Path), in.File, in.Line)
	return result(rep, err)
}

func (s *Server) handleSecretImpact(ctx context.Context, _ *sdkmcp.CallToolRequest, in secretImpactInput) (*sdkmcp.CallToolResult, any, error) {
	rep, err := s.svc.SecretImpactWithInventory(ctx, cwdOf(in.Path), in.Keys, in.Depth, in.ViaVault, in.Prefix)
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

func (s *Server) handleCoverage(_ context.Context, _ *sdkmcp.CallToolRequest, in coverageInput) (*sdkmcp.CallToolResult, any, error) {
	if r, v, stop := s.notIndexed(in.Path); stop {
		return r, v, nil
	}
	rep, err := s.svc.Coverage(cwdOf(in.Path), app.CoverageOptions{
		PathPrefix: in.Prefix, Language: in.Language, OnlyUncovered: in.Uncovered,
		Detail: in.Files, Top: in.Top,
	})
	return result(rep, err)
}

func (s *Server) handlePath(_ context.Context, _ *sdkmcp.CallToolRequest, in pathQueryInput) (*sdkmcp.CallToolResult, any, error) {
	if r, v, stop := s.notIndexed(in.Path); stop {
		return r, v, nil
	}
	if in.FromSelector != nil || in.ToSelector != nil {
		if in.FromSelector == nil || in.ToSelector == nil {
			return result(nil, fmt.Errorf("path needs both from_selector and to_selector"))
		}
		rep, err := s.svc.PathBySelectors(cwdOf(in.Path), *in.FromSelector, *in.ToSelector)
		return result(rep, err)
	}
	if in.From == "" || in.To == "" {
		return result(nil, fmt.Errorf("path needs from and to symbols or two selectors"))
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

func (s *Server) handleGrep(ctx context.Context, _ *sdkmcp.CallToolRequest, in grepInput) (*sdkmcp.CallToolResult, any, error) {
	if r, v, stop := s.notIndexed(in.Path); stop {
		return r, v, nil
	}
	rep, err := s.svc.GrepWithContext(ctx, cwdOf(in.Path), in.Pattern, app.GrepOptions{Regex: in.Regex, IgnoreCase: in.IgnoreCase, Top: in.Top})
	return result(rep, err)
}

func (s *Server) handleSource(_ context.Context, _ *sdkmcp.CallToolRequest, in sourceInput) (*sdkmcp.CallToolResult, any, error) {
	if r, v, stop := s.notIndexed(in.Path); stop {
		return r, v, nil
	}
	if in.Selector != nil {
		rep, err := s.svc.SourceBySelector(cwdOf(in.Path), *in.Selector, in.Brief)
		return result(rep, err)
	}
	if in.Symbol == "" {
		return result(nil, fmt.Errorf("source needs symbol or selector"))
	}
	rep, err := s.svc.Source(cwdOf(in.Path), in.Symbol, in.Brief)
	return result(rep, err)
}

func (s *Server) handleContext(ctx context.Context, _ *sdkmcp.CallToolRequest, in contextInput) (*sdkmcp.CallToolResult, any, error) {
	if r, v, stop := s.notIndexed(in.Path); stop {
		return r, v, nil
	}
	if in.Selector != nil {
		rep, err := s.svc.ContextBySelectorWithContext(ctx, cwdOf(in.Path), *in.Selector, in.Depth, in.Brief)
		return result(rep, err)
	}
	if in.Symbol == "" {
		return result(nil, fmt.Errorf("context needs symbol or selector"))
	}
	rep, err := s.svc.ContextWithContext(ctx, cwdOf(in.Path), in.Symbol, in.Depth, in.Brief)
	return result(rep, err)
}

func (s *Server) handleContextBatch(ctx context.Context, _ *sdkmcp.CallToolRequest, in contextBatchInput) (*sdkmcp.CallToolResult, any, error) {
	if r, v, stop := s.notIndexed(in.Path); stop {
		return r, v, nil
	}
	rep, err := s.svc.ContextBatchWithContext(ctx, cwdOf(in.Path), in.Symbols, in.Selectors, in.Depth, in.Brief)
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

func (s *Server) handleRequiredKeys(ctx context.Context, _ *sdkmcp.CallToolRequest, in requiredKeysInput) (*sdkmcp.CallToolResult, any, error) {
	if in.Entrypoint == "" {
		return errResult("specify an entrypoint symbol (the function/method to scope from)"), nil, nil
	}
	depth := in.Depth
	if depth <= 0 {
		depth = 5
	}
	rep, err := s.svc.RequiredKeysWithInventory(ctx, cwdOf(in.Path), in.Entrypoint, in.Keys, depth, in.ViaVault, in.Prefix)
	return result(rep, err)
}

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
		return codedErrResult(err), nil, nil
	}
	// Compact JSON without HTML escaping keeps agent responses token-efficient.
	// The inner result has no HTML context, so <, >, & stay literal (e.g. a path
	// target "A -> B", generics Array<T>); the go-sdk re-escapes this string
	// correctly in the JSON-RPC envelope.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if mErr := enc.Encode(v); mErr != nil {
		return errResult(mErr.Error()), nil, nil
	}
	return &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: string(bytes.TrimRight(buf.Bytes(), "\n"))}},
	}, v, nil
}

// errResult renders a bare error message for call sites with no error value
// to inspect for a CodedError (e.g. hand-rolled failures). Prefer result(nil,
// err) or codedErrResult(err) wherever an error value exists, so the agent
// gets a stable code + hint instead of a free-form string.
func errResult(msg string) *sdkmcp.CallToolResult {
	return &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "Error: " + msg}},
		IsError: true,
	}
}

// mcpError is the structured error payload surfaced in CallToolResult.Meta,
// mirroring the {code, message, hint} shape other tools in the ecosystem
// (veclite, vecgrep) already emit — one machine-readable contract an agent
// can switch on instead of parsing free-form text.
type mcpError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

// codedErrResult renders err as a structured MCP error. When err carries a
// *app.CodedError (via errors.As), the stable code and remediation hint ride
// along in Meta["error"] and the hint is appended to the visible text so it
// reads naturally even for clients that only surface Content. Falls back to
// CodeOf/HintOf's "operational" default for untyped errors.
func codedErrResult(err error) *sdkmcp.CallToolResult {
	code := app.CodeOf(err)
	hint := app.HintOf(err)
	msg := err.Error()
	text := "Error: " + msg
	if hint != "" {
		text += " (hint: " + hint + ")"
	}
	errData, _ := json.Marshal(mcpError{Code: code, Message: msg, Hint: hint})
	return &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: text}},
		IsError: true,
		Meta:    sdkmcp.Meta{"error": json.RawMessage(errData)},
	}
}
