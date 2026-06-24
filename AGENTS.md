# AGENTS.md

Instructions for AI agents (and humans) working on the **codemap** codebase. This is the
canonical source-of-truth doc; `CLAUDE.md` defers to it. `README.md` is the public-facing
intro. `SPEC.md` is the original design rationale. `BACKLOG.md` tracks live work.

## Project Overview

codemap is local-first code intelligence: it combines a **structural code graph**
(LSP + stdlib parsers, optional tree-sitter) with **semantic vector search** (veclite) and
exposes both as a unified query layer for agents and people. It answers questions a grep
or a single LSP call cannot — *"who calls this and what tests cover it"*, *"what's the blast
radius of changing this type across all my projects"*, *"find auth-like code, then show me
what calls it."*

Three surfaces over one store (the ecosystem pattern, see vecgrep/noted):
- **CLI** — human commands *and* `--json` machine output for agents (Cobra).
- **MCP server** — `codemap serve` (stdio), `codemap_*` tools for agents.
- **studio TUI** — `codemap studio` (Charm v2), interactive code map for humans.

Key features:
- **Structural graph** — nodes (files, functions, types, methods, tests) + `calls`/`defines` edges
  in pure-Go SQLite (Go call edges are name-based by default, exact via `go/types` with `--precise`;
  TypeScript/JavaScript/Python call edges come only from `--precise` via `callHierarchy`;
  `imports`/`implements`/`references`/`overrides` edge types are reserved for the planned LSP/tree-sitter
  backends).
- **Semantic search** — node source text embedded via Ollama (`nomic-embed-text`, 768-dim)
  into veclite; vector + BM25 hybrid (RRF) search.
- **Hybrid queries** — `codemap_impact` (blast radius + test coverage), `semantic_callers`
  (semantic match then graph expansion), `refactor_plan`.
- **Cross-project** — the graph spans all registered projects.
- **Incremental** — hash-based reindex; embedding-profile guard forces rebuild on
  provider/model/dim change.
- **Offline** — once indexed, queryable without LSP servers running.

## Directory Structure

```
codemap/
├── cmd/codemap/main.go        # single entrypoint, all cobra commands (vecgrep style)
├── internal/
│   ├── app/                   # shared service layer; CLI + MCP + TUI all call this
│   │   ├── service.go         #   business logic (index, query, impact …)
│   │   └── session.go         #   open/close store + veclite + provider (lazy)
│   ├── config/                # XDG-style hierarchical config (see "Config")
│   │   ├── config.go          #   types + DefaultConfig + Load + env overrides
│   │   ├── paths.go           #   XDG dirs + ~/.codemap fallback + ExpandPath
│   │   └── project.go         #   FindProjectRoot + DeriveProjectName
│   ├── graph/                 # SQLite graph store (pure Go, modernc.org/sqlite)
│   │   ├── store.go           #   Open/Close, CRUD for nodes/edges/projects, stats
│   │   ├── schema.go          #   SQL schema + migrations (PRAGMA user_version)
│   │   └── queries.go         #   callers/callees, blast radius, hotspots/orphans/path
│   ├── extract/               # code structure extraction (pluggable backends)
│   │   ├── extractor.go       #   Extractor interface + Symbol/Reference/FileResult
│   │   ├── gosrc/             #   stdlib go/parser backend (pure Go, default for Go)
│   │   └── lspsrc/            #   LSP-backed extractor (DocumentSymbols → symbols)
│   ├── lsp/                   # headless LSP client (no deps; Content-Length JSON-RPC)
│   │   ├── jsonrpc.go         #   framed conn: read loop, Call/Notify, handler
│   │   └── client.go          #   Spawn/Initialize/DidOpen/DocumentSymbols/References
│   ├── embed/                 # embedding providers
│   │   ├── provider.go        #   Provider interface + EmbeddingProfile guard
│   │   └── ollama.go          #   POST /api/embed (net/http + json, no SDK)
│   ├── vector/store.go        # veclite wrapper: collection + profile guard + hybrid
│   ├── index/indexer.go       # walk → extract → embed → store; incremental + resolve edges
│   ├── mcp/server.go          # stdio MCP server — THIN pass-through to internal/app
│   ├── tui/                   # studio TUI (Charm v2): model/update/view/run/theme
│   │   ├── model.go           #   state, msgs, commands, key handling (Graph/Metrics/Impact/Search)
│   │   ├── view.go            #   full-screen layout, call-graph explorer, bar charts
│   │   ├── theme.go           #   lipgloss v2 styles
│   │   └── run.go             #   tea.NewProgram entry
│   └── version/version.go     # Version/Commit/Date (ldflags-injected)
│
│   # planned: extract/treesitter (CGO, build-tagged), extract/scip, search/* fusion
├── docs/                      # VitePress site (product docs ONLY) → deployed to Vercel
├── specs/                     # glyphrun E2E specs (*.yml): version/help/index_status/query
│                              #   (graphs) · context (one-call bundle) · annotations (knowledge
│                              #   layer) · staleness (drift) · incremental (reindex updates) ·
│                              #   config (repo-local codemap.yaml) · index_progress (TTY bar) · mcp_serve
│                              #   (stdio JSON-RPC) · studio (gopls) · semantic (vectors+Ollama)
│                              #   · precise/typescript/javascript/python · jsx (<Component/> call edges)
│                              #   · polyglot (Go+TS+Py one repo)
├── Taskfile.yml .golangci.yml .goreleaser.yaml glyphrun.config.yml
├── .github/workflows/         # ci.yml + release.yml
└── README.md AGENTS.md CLAUDE.md BACKLOG.md SPEC.md LICENSE
```

**Package boundaries are part of the contract.** The dependency direction is one-way:
`cmd → app → {graph, search, index, extract, embed, config}`. The `tui`, `mcp`, and CLI
RunE handlers are all *thin* and call `internal/app`. Never put business logic in `mcp` or
`tui`. (Same rule glyphrun documents for its own MCP package.)

## Documentation Discipline (read this)

- `docs/` is a **deployed VitePress site** for product documentation **only**. Single
  hosting path: **Vercel** — no GitHub Pages.
- Repo root carries exactly these markdown files: `README.md`, `AGENTS.md`, `CLAUDE.md`,
  `BACKLOG.md`, `SPEC.md`. **Do not** create scratch / handoff / TODO / design `.md` files
  anywhere in the repo.
- Working notes, handoffs, investigations, and design exploration go to the **Obsidian
  vault** at `~/notes/projects/codemap/` (use the `obsidian-cli` skill), not the repo.
- `BACKLOG.md` is the one exception to "no working-notes in repo": it is the explicit,
  user-requested state file for the build loop. Keep it terse and current.

## Development Commands (Taskfile, version 3)

```
task                 # list tasks
task doctor          # check go, ollama (+ nomic-embed-text), task, glyph, golangci-lint
task setup           # deps + tools + docs deps
task build           # build → ./bin/codemap (ldflags inject version)
task test            # go test ./...
task race            # CGO_ENABLED=1 go test -race ./...
task lint            # golangci-lint (or go vet + gofmt -l)
task fmt             # gofmt -w .
task check           # fmt + lint + test  (aliases: ci, verify)
task flows           # glyph run specs/*.yml  (E2E; local only — not run in CI)
task site:dev        # VitePress dev server (Bun)
task site:build      # VitePress build
task ship            # check + site:build + race + build + flows
task install         # go install ./cmd/codemap
```

## Prerequisites

- **Go 1.25+** (toolchain pinned `1.25.x`, matching the ecosystem).
- **Ollama** running with `nomic-embed-text` pulled (`ollama pull nomic-embed-text`) for
  embeddings. Embedding tests skip if Ollama is unreachable.
- **Task** (`go install github.com/go-task/task/v3/cmd/task@latest`).
- **glyph** (glyphrun) for E2E specs; **Bun** for docs; **golangci-lint** for lint.
- LSP servers for languages you index: `gopls`, `typescript-language-server`, etc.

## Architecture Notes

### Extraction (Go + TypeScript + JavaScript + Python)
- **`gosrc` (`go/parser`) is always registered** (Go: full call graph; `--precise` adds exact edges
  via `go/types`). **`lspsrc` (the LSP backend) is now wired in**: `IndexProject` runs a present-aware
  `registerLSP` that, for each `lspsrc.DefaultServers` spec, spawns its server **once** if any of the
  server's languages are present and the binary is on PATH, then registers an extractor per present
  language sharing that one connection (via `Extractor.Bind`) — so **one `typescript-language-server`
  serves both TypeScript and JavaScript** (`.ts/.tsx` + `.js/.jsx/.mjs/.cjs`), each routed with its own
  LSP `languageId`. Calls resolve **across** the `.ts`↔`.js` boundary. **Python** uses
  `pyright-langserver` (its own spec/process). Symbols + `defines` edges always; call edges via
  `callHierarchy` under `--precise`, resolved in `Indexer.resolveLSPCallEdges`. `appendSymbols` drops
  Variable symbols nested inside a callable (pyright reports params/locals as variables). A Go-only repo
  never spawns a server; `defer ix.Close()` reaps any that were.
- Languages present but missing their server are recorded in `Result.MissingServers` and surfaced
  as an actionable "install X" message; genuinely-unsupported languages are still `Result.Unsupported`
  ("skipped, planned"). `--no-lsp` disables the LSP backend.
- **Every LSP request is bounded.** `conn.Call` applies a 30s default per-request timeout when the
  caller sets no deadline (`internal/lsp/jsonrpc.go`), so a hung/misbehaving server can't freeze the
  index — a stalled request returns a deadline error and the file is skipped (recorded in
  `Result.Errors`), not hung. `index` runs on `context.Background()`, so this bound is what protects it.
- **Default backends are pure-Go** so release binaries stay `CGO_ENABLED=0` and cross-compile
  cleanly (the language server is a spawned subprocess, like gopls — never linked in). LSP edges
  carry weight `1.0`; heuristic/parser edges `0.7`.
- **tree-sitter is OPTIONAL**, gated behind the `treesitter` build tag (it needs CGO via
  `github.com/tree-sitter/go-tree-sitter`). Release builds do not include it; it rounds out
  long-tail language coverage in 0.2 (built with a `zig cc` matrix or the purego path).
- LSP client is **hand-rolled in `internal/lsp`** (no third-party deps): a Content-Length
  framed JSON-RPC 2.0 conn + `Initialize`/`DidOpen`/`DocumentSymbols`/`References`. The
  `internal/extract/lspsrc` backend maps `documentSymbol` → codemap symbols. Planned: wire it
  into the indexer (per-language sessions) + `references`/callHierarchy for precise edges
  (weight `1.0`; parser edges `0.7`). **LSP uses Content-Length framing — correct for LSP, and
  it must NOT leak into the MCP transport (which is newline-delimited).**

### Storage
- Graph: `modernc.org/sqlite` (pure Go), WAL mode, batch inserts. Tables: `nodes`, `edges`,
  `projects`, `index_state` (see SPEC.md "Storage Schema").
- Vectors: `github.com/abdul-hamid-achik/veclite` (≥ v0.17.0). One collection (`codemap`),
  one vector space in v0.1. Put **filterable** fields (`project`, `path`, `lang`, `kind`,
  `node_id`) in the veclite **Payload**; put the **searchable** source text in **Content**
  (or a `WithTextIndex` field) so BM25/`HybridSearch` works. `Result{Record, Score}`.
- **Lazy open**: do NOT open the DB/veclite at process startup. Open on first query. This is
  the v1 answer to multi-process contention (multiple MCP clients spawning servers).

### Embeddings
- `embed.Provider` interface; default `ollama.go` POSTs `{model, input}` to
  `http://localhost:11434/api/embed` and decodes `[]float32` (no third-party SDK). Endpoint,
  model, and dimensions are configurable. Optional cloud providers (OpenAI/Cohere/Voyage)
  can follow vecgrep's pattern later.
- **EmbeddingProfile guard**: store provider/model/dimension/distance/chunker in collection
  metadata; a mismatch fails the index with explicit rebuild guidance (never silently mix
  embedding spaces).

### MCP server (`internal/mcp/server.go`)
- SDK: `github.com/modelcontextprotocol/go-sdk/mcp` (v1.6.1). Build with
  `mcp.NewServer(&mcp.Implementation{Name:"codemap", Version: version.Version}, opts)`;
  register each tool via `mcp.AddTool(server, &mcp.Tool{Name:"codemap_x", …}, handler)` with
  a typed input struct using `json:"…,omitempty"` + `jsonschema:"description"` tags (a field
  **without** `omitempty` is required). Transport: `&mcp.StdioTransport{}`.
- **CRITICAL: stdio MCP output must be newline-delimited JSON-RPC, not Content-Length.** The
  official SDK's `StdioTransport` already does this; do not wrap or reframe it. (A sibling
  tool, `glyph`, reported "Failed to connect" in Claude Code purely because it used
  Content-Length framing. vecgrep/noted/vidtrace use newline-delimited and connect fine.)
- `ServerOptions.Instructions` should give agents a one-paragraph usage hint.
- Tool names are `codemap_`-prefixed. Current set (20): `init, index, status, doctor, semantic,
  callers, callees, impact, hotspots, orphans, path, symbols, find, source, context, projects, docs,
  annotate, annotations, unannotate`. Each takes an optional `path` (project dir, defaults to cwd) and returns
  JSON; callers/callees take `precise` (gopls); `source` returns a symbol's body; `context` bundles a
  symbol's definition+callers+callees+covering tests+blast radius (with `blast_depth`) in one call;
  `projects` lists the registry; `docs` returns the agent guide (`internal/app/docs.go`);
  `annotate`/`annotations` pin/list notes + opaque data on a symbol or `from→to` path (graph
  `annotations` table, schema v2, survives reindex). (Planned: `references`, `dependencies`, `semantic_callers`.)
- CLI mirrors these: `init`, `index` (`--reindex`/`--no-embed`/`--precise`), `status`, `callers`,
  `callees`, `path`, `impact` (`--depth`), `hotspots`/`orphans` (`--top`), `semantic`
  (`--top`), `serve`, `studio` — all query commands accept `--json`.
- **Accuracy model** (be honest with users): the graph is name-based by default — intra-package
  calls resolve precisely (Go), but cross-package method calls (`x.Foo()`) link to every same-named
  method (no type info). codemap flags this (`callers`/`impact` note ambiguous names; `hotspots`
  marks inflation; `orphans` follows functions wired by value — handlers like cobra `RunE` /
  `mux.HandleFunc(s.h)`, via `references` edges that never enter the call graph — but stays
  interface/reflection-blind, so its results are *candidates*). **The graph-wide
  fix is shipped: `codemap index --precise`** (CLI) / `codemap_index precise:true` (MCP) is the unified
  exact-resolution pass. For Go it runs an in-process pure-Go `go/types` pass (`internal/extract/typesrc`);
  for the LSP languages (TypeScript, JavaScript, Python) it drives the language server's `callHierarchy`
  (`Indexer.resolveLSPCallEdges`). It resolves each call to the one it invokes and writes precise call
  edges via the `edges.provenance` column — so every query (callers/callees/impact/hotspots/path) becomes
  exact at once, no query change. The LSP languages have **no** name-based call edges, so `--precise` is
  what gives them a call graph at all (for Go it *replaces* the name-based edges; name-based stays the Go
  default). The Go pass degrades
  per-package on type errors and wholesale (with a note) when the `go` toolchain/module is unavailable.
  `callers`/`callees --lsp` (`precise:true`) remains the per-query gopls path for a one-off without
  reindexing.

### Config precedence (highest → lowest)
1. Env vars `CODEMAP_*` (e.g. `CODEMAP_CONFIG`, `CODEMAP_DATA`, `CODEMAP_EMBEDDING_MODEL`,
   `CODEMAP_OLLAMA_URL`).
2. Project-root `codemap.yaml` / `codemap.yml`.
3. Project `.config/codemap.yaml` (XDG-style, in-repo).
4. Global `$XDG_CONFIG_HOME/codemap/config.yaml` (fallback `~/.config/codemap/config.yaml`).
5. `~/.codemap/config.yaml` (legacy/ecosystem fallback, only if it exists).
6. Built-in `DefaultConfig()`.

Data (graph DB, veclite, project registry) lives in `$XDG_DATA_HOME/codemap/`
(fallback `~/.local/share/codemap/`); cache in `$XDG_CACHE_HOME/codemap/`. If `~/.codemap/`
already exists it is honored for back-compat with the vecgrep/noted ecosystem.
`codemap init --local` keeps repo-local state. Config format is **YAML** (matches the
ecosystem; `gopkg.in/yaml.v3`).

## Common Tasks for Agents

**Add a CLI command:** add the cobra command var + `init()` registration + `runX` handler in
`cmd/codemap/main.go`; the handler opens a session and calls `internal/app`. Support
`--json` for machine output.

**Add an MCP tool:** define a typed input struct (json + jsonschema tags) in
`internal/mcp/server.go`, register with `mcp.AddTool`, and have the handler delegate to
`internal/app`. Keep it thin. Update the tool list in README/docs.

**Add a graph query:** implement traversal in `internal/graph/queries.go` (BFS with a
visited set — **always detect cycles**), expose via `internal/search`, then surface in CLI +
MCP + TUI.

**Data-model change:** edit `internal/graph/schema.go` with a migration; bump the schema
version; never break an existing index without a rebuild path.

**Add a studio tab/widget:** add a `tab_*.go` in `internal/tui`, wire it into the tab bar in
`model.go`/`update.go`. Use Charm **v2** (`charm.land/...`) and ntcharts **v2** for charts.

## Code Style

- `go fmt` + `golangci-lint` (config version 2; errcheck + staticcheck enabled).
- Error messages **lowercase, no trailing punctuation**; return errors, `os.Exit(1)` in
  `main` only.
- Small, testable functions; explicit error handling over panics.
- `cmd/` files carry the header `/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */`.

## Testing

- High unit coverage on `graph` (traversal, cycles), `extract`, `search`, `config`.
- Ollama-dependent tests skip when Ollama isn't running.
- `task race` for the TUI and concurrent indexer.
- glyphrun specs in `specs/` are the E2E contract: declare **intent + outcomes** (the
  contract) and **steps** (repairable hints). Stamp the `contractHash` with
  `glyph spec verify specs/x.yml --stamp` after an intentional intent/outcome change. Run
  with `task flows`.

## Before Committing

`task check` (fmt + lint + test) → `task build` → `task flows` if specs changed. Keep docs
discipline (no stray `.md`; product docs in `docs/`, notes in `~/notes`). Commit/push only
when the user asks.

## Related projects (Obsidian wikilinks)

[[../veclite/index|VecLite]] · [[../vecgrep/index|vecgrep]] · [[../noted/index|noted]] ·
[[../glyphrun/index|glyphrun]] — siblings under `~/projects`. codemap mirrors vecgrep's CLI +
config + MCP conventions and noted's three-surface pattern.
