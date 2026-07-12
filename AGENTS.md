# AGENTS.md

Instructions for AI agents (and humans) working on the **codemap** codebase. This is the
canonical source-of-truth doc; `CLAUDE.md` defers to it. `README.md` is the public-facing
intro. The design rationale (architecture, why-it-is-what-it-is) lives in the Obsidian
vault at `~/notes/projects/codemap/design-rationale.md`. `BACKLOG.md` tracks live work.

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
  Go callback/handler uses are persisted as `references`; imports are package-scoped evidence;
  `implements`/`overrides` remain reserved for planned backends).
- **Semantic search** — node source text embedded via Ollama (`nomic-embed-text`, 768-dim)
  into veclite; vector + BM25 hybrid (RRF) search.
- **Hybrid queries** — `codemap_context` (one-call source + wiring + impact), `codemap_impact`
  (blast radius + test coverage), `codemap_review` (diff → impact + tests), and confidence-aware
  file dependency analysis.
- **Cross-project** — the graph spans all registered projects.
- **Incremental** — hash-based reindex; embedding-profile guard forces rebuild on
  provider/model/dim change.
- **Offline** — once indexed, queryable without LSP servers running.

## Directory Structure

```
codemap/
├── cmd/codemap/              # cobra CLI, split per-command (vecgrep style): main.go +
│                            #   annotate/branch/cache/config/daemon/index/init_status/query.
│                            #   Each RunE handler is THIN → opens a session → calls internal/app.
│                            #   Files carry the header `/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */`.
├── internal/
│   ├── app/                 # shared service layer; CLI + MCP + TUI all call this
│   │   ├── service_core.go        #   Service + Session wiring, common helpers
│   │   ├── service_init.go        #   init / index / status / doctor
│   │   ├── service_query.go       #   callers / callees / path / symbols / find / source
│   │   ├── service_relations.go   #   precise callers/callees (callHierarchy / go/types)
│   │   ├── references.go          #   inbound callback/handler value-reference query
│   │   ├── service_impact.go      #   impact / blast radius / hotspots / orphans / read-order
│   │   ├── service_context.go     #   context (the one-call bundle) + context_batch
│   │   ├── service_semantic.go    #   semantic search (veclite hybrid) + find fallback
│   │   ├── service_annotations.go #   annotate / annotations / unannotate (knowledge layer)
│   │   ├── session.go             #   open/close store + veclite + provider (lazy)
│   │   ├── review.go              #   diff-scoped impact (git diff → symbols → blast + tests)
│   │   ├── dependencies.go        #   grouped file dependency evidence + per-domain completeness
│   │   ├── file_impact.go         #   dependency evidence + conservative delete verdict / breaking_change
│   │   ├── risk.go                #   0..1 change-risk score
│   │   ├── readorder.go           #   entrypoint + hub ranking
│   │   ├── secret_impact.go       #   secret-key rotation blast radius (names only)
│   │   ├── branchswitch.go        #   branch-aware index switching
│   │   ├── cache.go               #   fcheap content-addressed index cache
│   │   ├── doctor.go              #   environment + daemon health checks
│   │   ├── docs.go                #   the in-band agent guide (codemap docs / codemap_docs)
│   │   ├── errors.go              #   CodedError (stable machine codes) for --json failures
│   │   └── vecgrep_client.go      #   vecgrep semantic-fallback + memory recall
│   ├── graph/                # SQLite graph store (pure Go, modernc.org/sqlite)
│   │   ├── store.go          #   Open/Close, CRUD for nodes/edges/projects, stats
│   │   ├── schema.go         #   SQL schema + migrations (PRAGMA user_version); edges.provenance
│   │   ├── queries.go        #   callers/callees, blast radius, hotspots/orphans/path
│   │   ├── dependencies.go   #   deduped inbound call/reference/import evidence
│   │   └── references.go     #   project-scoped inbound references with ambiguity metadata
│   ├── extract/              # code structure extraction (pluggable backends)
│   │   ├── extractor.go      #   Extractor interface + Symbol/Reference/FileResult
│   │   ├── gosrc/            #   stdlib go/parser backend (pure Go, default for Go)
│   │   ├── typesrc/          #   in-process go/types pass (Go --precise; exact call edges)
│   │   ├── lspsrc/           #   LSP-backed extractor (documentSymbol → symbols; callHierarchy)
│   │   └── vuesrc/           #   Vue SFC (.vue) → routes <script> blocks to the TS server
│   ├── lsp/                  # headless LSP client (no deps; Content-Length JSON-RPC)
│   │   ├── jsonrpc.go        #   framed conn: read loop, Call/Notify, 30s default timeout
│   │   └── client.go         #   Spawn/Initialize/DidOpen/DocumentSymbols/References/callHierarchy
│   ├── embed/                # embedding providers
│   │   ├── provider.go       #   Provider interface + EmbeddingProfile guard
│   │   └── ollama.go         #   POST /api/embed (net/http + json, no SDK)
│   ├── vector/store.go       # veclite wrapper: collection + profile guard + hybrid
│   ├── index/                # walk → extract → embed → store; incremental + resolve edges
│   │   ├── indexer.go        #   main pipeline; resolveLSPCallEdges / resolveGoCallEdges
│   │   ├── staleness.go      #   hash-based drift detection (status/agent trust)
│   │   ├── watcher.go        #   fsnotify watcher (daemon hook)
│   │   └── import_index.go   #   fcheap cache restore (skip extract+embed)
│   ├── mcp/server.go         # stdio MCP server — THIN pass-through to internal/app (39 tools)
│   ├── tui/                   # studio TUI (Charm v2): model/view/theme/run + anim + highlight
│   │   ├── model.go          #   state, msgs, commands, key handling (Graph/Metrics/Impact/Search)
│   │   ├── view.go           #   full-screen layout, call-graph explorer, map, bar charts
│   │   ├── theme.go          #   lipgloss v2 styles
│   │   ├── anim.go           #   harmonica spring frame loop (bars + map reveal + spinner)
│   │   ├── highlight.go      #   chroma v2 syntax highlighting for the source overlay
│   │   └── run.go            #   tea.NewProgram entry
│   ├── daemon/               # background watcher: fsnotify, throttle, control socket, delegation
│   ├── git/                  # branch / ref / diff helpers (review + branch-switch)
│   ├── snapshot/             # fcheap-backed index snapshot/restore
│   ├── branchstate/          # per-branch snapshot pointer store
│   ├── cachestate/           # content-addressed cache pointer store
│   ├── config/               # XDG-style hierarchical config (see "Config")
│   │   ├── config.go          #   types + DefaultConfig + Load + env overrides
│   │   ├── paths.go           #   XDG dirs + ~/.codemap fallback + ExpandPath
│   │   └── project.go         #   FindProjectRoot + DeriveProjectName
│   └── version/version.go     # Version/Commit/Date (ldflags-injected)
│
│   # planned: extract/treesitter (CGO, build-tagged), extract/scip
├── docs/                      # VitePress site (product docs ONLY) → deployed to Vercel
├── specs/                     # glyphrun E2E specs (*.yml, 39): version/help/index_status/query/context/
│                              #   annotations/staleness/incremental/config/index_progress/mcp_serve/
│                              #   studio(+_ts)/semantic/precise/typescript/javascript/python/jsx/
│                              #   polyglot/review/read_order/risk/file_impact/daemon/cache_cli/
│                              #   exclude_extra/index_watch/timing/progress_eta/onboarding/
│                              #   ts_impact_note/studio_visuals/index_via_daemon/selectors/dependencies/
│                              #   review_deletion/references/studio_annotations
├── Taskfile.yml .golangci.yml .goreleaser.yaml glyphrun.config.yml
├── .github/workflows/         # ci.yml + release.yml
└── README.md AGENTS.md CLAUDE.md BACKLOG.md LICENSE
```

**Package boundaries are part of the contract.** The dependency direction is one-way:
`cmd → app → {graph, vector, index, extract, embed, lsp, config, daemon, git, snapshot,
branchstate, cachestate}`. The `tui`, `mcp`, and CLI RunE handlers are all *thin* and call
`internal/app`. Never put business logic in `mcp` or `tui`. (Same rule glyphrun documents
for its own MCP package.)

## Documentation Discipline (read this)

- `docs/` is a **deployed VitePress site** for product documentation **only**. Single
  hosting path: **Vercel** — no GitHub Pages.
- Repo root carries exactly these markdown files: `README.md`, `AGENTS.md`, `CLAUDE.md`,
  `BACKLOG.md`. **Do not** create scratch / handoff / TODO / design `.md` files
  anywhere in the repo. (Design rationale lives in the vault: `design-rationale.md`.)
- Working notes, handoffs, investigations, and design exploration go to the **Obsidian
  vault** at `~/notes/projects/codemap/` (use the `obsidian-cli` skill), not the repo.
- `BACKLOG.md` is the one exception to "no working-notes in repo": it is the explicit,
  user-requested state file for the build loop. Keep it terse and current.

## Development Commands (Taskfile, version 3)

```
task                 # list tasks
task doctor          # check go, ollama (+ nomic-embed-text), task, glyph, golangci-lint
task setup           # deps + tools (docs deps: task site:deps)
task build           # build → ./bin/codemap (ldflags inject version)
task test            # go test ./...
task race            # CGO_ENABLED=1 go test -race ./...
task lint            # golangci-lint (or go vet + gofmt -l)
task fmt             # gofmt -w .
task check           # fmt + lint + test  (aliases: ci, verify)
task flows           # glyph run specs/*.yml  (E2E; local only — not run in CI)
task site:dev        # VitePress dev server (Bun)
task site:build      # VitePress build
task ship            # check + race + build + flows
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

### Extraction (Go + TypeScript + JavaScript + Python + Vue SFC)
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
- **Vue SFC (`.vue`)** is indexed by `internal/extract/vuesrc`: a `.vue` file's
  `<script>`/`<script setup>` block (with `lang="ts"` routing to TypeScript,
  unmarked/`lang="js"` to JavaScript) is delegated to the **same**
  `typescript-language-server` connection that indexes plain `.ts`/`.js`, with
  symbol lines mapped back onto the original `.vue` file. Template/style blocks
  are not indexed. A project with only `.vue` files (no plain `.ts`/`.js`) spawns
  the server itself to serve the script blocks. Symbols + `defines` edges only —
  no `--precise` call graph for Vue yet.
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
- Graph: `modernc.org/sqlite` (pure Go), WAL mode, transaction-batched writes,
  `synchronous=NORMAL`, `SetMaxOpenConns(1)`. Tables: `nodes`, `edges`,
  `projects`, `index_state`, `call_graph_coverage` (schema v5). The
  `edges.provenance` column records whether an edge is name-based or precise;
  `call_graph_coverage` records successful precise resolution per file, including
  leaf files with zero call edges (see `design-rationale.md` "Storage" in the vault).
- Vectors: `github.com/abdul-hamid-achik/veclite` (≥ v0.22.0). One collection (`codemap`),
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
- Tool names are `codemap_`-prefixed. Current set (39): `init`, `index`, `status`, `doctor`, `semantic`, `callers`, `callees`, `references`, `impact`, `file_impact`, `dependencies`, `review`, `secret_impact`, `required_keys`, `risk`, `hotspots`, `orphans`, `coverage`, `read_order`, `path`, `related_files`, `symbols`, `symbol_at`, `find`, `grep`, `source`, `context`, `context_batch`, `projects`, `docs`, `annotate`, `annotations`, `unannotate`, `branch_status`, `branch_switch`, `cache_save`, `cache_restore`, `cache_list`, `cache_drop`. Each takes an optional `path` (project dir, defaults to cwd) and returns JSON; callers/callees take `precise`; `references` returns bounded callback/handler value-use sites with partial-coverage confidence; `dependencies` returns bounded inbound call/reference/import evidence plus domain coverage; `source` returns a symbol's body; `context` bundles a symbol's definition+callers+callees+value references+covering tests+blast radius; `docs` returns the agent guide; `annotate`/`annotations` pin/list notes on a symbol or `from→to` path; `coverage` returns per-file precise call-graph coverage rolled up by language/directory — the project-wide, per-file signal behind the per-query `call_graph` enum.
- Exact-definition inputs use the durable source-selector projection
  `{file,start_line,fqn,kind}`, never a SQLite node id. `source`, `context`,
  `callers`, `callees`, `impact`, and `risk` accept `selector`; `path` accepts
  paired `from_selector`/`to_selector`. Resolution prefers file+FQN+kind so
  ordinary line shifts survive reindex, with start_line as tie-break/fallback.
- CLI mirrors these 1:1: `init`, `index` (`--reindex`/`--no-embed`/`--precise`/`--watch`/`--no-lsp`), `status`, `config path/show`, `doctor`, `projects`, `callers`/`callees` (`--precise`), `path`, `impact` (`--depth`), `file-impact`, `dependencies`, `review` (`--staged`/`--since`), `secret-impact`, `required-keys`, `risk`, `hotspots`/`orphans` (`--top`), `semantic` (`--top`), `read-order`, `symbols`, `symbol-at`, `find`, `grep` (`--regex`/`-i`), `source`, `context` (multi-arg → batch), `related-files`, `annotate`/`annotations`, `branch-status`/`branch-switch`/`branch-snapshot`, `cache save`/`restore`/`list`/`drop`, `daemon start`/`status`/`stop`, `docs`, `serve`, `studio` — all query commands accept `--json`.
- **Accuracy model** (be honest with users): the graph is name-based by default — intra-package
  calls resolve precisely (Go), but cross-package method calls (`x.Foo()`) link to every same-named
  method (no type info). codemap flags this (`callers`/`impact` note ambiguous names; `hotspots`
  marks inflation; `orphans` follows functions wired by value — handlers like cobra `RunE` /
  `mux.HandleFunc(s.h)`, via `references` edges that never enter the call graph — but stays
  interface/reflection-blind, so its results are *candidates*). **The graph-wide
  fix is shipped: `codemap index --precise`** (CLI) / `codemap_index precise:true` (MCP) is the unified
  exact-resolution pass. For Go it runs an in-process pure-Go `go/types` pass (`internal/extract/typesrc`);
  for the LSP languages (TypeScript, JavaScript, Python) it drives the language server's `callHierarchy`
  (`Indexer.resolveLSPCallEdges`). It resolves each call to the one it invokes, writes precise call
  edges via `edges.provenance`, and records successful coverage per file. Query confidence is classified
  from the matching definition files: all covered → `resolved`; uncovered TS/JS/Python/Vue wins →
  `unresolved`; otherwise Go/parser → `name`. One precise edge never upgrades an unrelated file, and
  changed files lose coverage until their precise pass succeeds again. The LSP languages have **no**
  name-based call edges, so `--precise` is
  what gives them a call graph at all (for Go it *replaces* the name-based edges; name-based stays the Go
  default). The Go pass degrades
  per-package on type errors and wholesale (with a note) when the `go` toolchain/module is unavailable.
  `callers`/`callees --precise` (`precise:true` in MCP) remains the per-query language-server path for a one-off without
  reindexing.
- **Stable machine contract**: every impact/callers/callees/references/review/context/hotspots/orphans/path report carries a
  `call_graph` enum (`resolved`/`name`/`unresolved`/`none`) so a consumer can switch on
  confidence (resolved→high, name→medium, unresolved/none→low) without parsing the free-form
  `resolution` sentence. `codemap review` additionally folds one aggregate `risk` band
  (`level`/`score`/`factors`) from every changed symbol so a harness can gate verification on a
  single call; `level` is `unknown` when any changed symbol lacks a usable call graph, otherwise
  `low`/`medium`/`high`. The `blast_radius`/`covering_tests` elements are `ImpactNode` objects
  (`symbol`/`fqn`/`kind`/`file`/`start_line`/`depth`; no `end_line`) — the stable element shape.
  Successful review JSON always emits `schema_version: 1` and is governed by
  `schemas/codemap.review.v1.schema.json`. Canonical keys are snake_case; additive optional fields
  include `deletion_analysis` (`files`/`analyzed`/`missing`/`source:last_index`/`complete`) when a
  diff deletes files, so consumers know whether retained pre-delete definitions were available, and
  are compatible within v1. Renames, removals, required-field additions, enum narrowing, or
  nested shape changes require a new schema major and a consumer dual-read window. Keep the hard
  CLI error envelope outside the success schema.
- **Dependency confidence contract**: `dependencies` and embedded `file_impact.dependency_evidence`
  preserve legacy totals and add `confirmed_total`/`candidate_total` at report, dependent-file,
  and evidence-kind levels. Samples carry `confidence` (`confirmed|candidate`) and a stable reason.
  Only fresh confirmed file-scoped evidence may produce `delete_verdict:"unsafe"`; name fan-out,
  package-scoped imports, stale snapshots, and missing domains remain `unknown`.
- MCP text results use compact JSON (the structured payload is unchanged) so agents do not spend
  context tokens on indentation. HTML escaping remains disabled for readable code/FQNs.
- **CLI exit-code taxonomy** (extends P2-06): `0`=answered, `1`=operational error,
  `2`=not found/not indexed, `3`=`index_missing`, `4`=`index_corrupt`, `5`=`not_a_repo`.
  Under `--json`, ANY failure prints a structured envelope to **stdout**:
  `{"ok":false,"error":"…","code":"not_found|not_indexed|index_missing|index_corrupt|not_a_repo|operational","hint":"run: codemap index"}`
  (the `code` matches the exit-code suffix). The `cmd/codemap/jsonHandler` wraps every RunE;
  hard failures are wrapped as `app.CodedError` at the `Session.Graph()` seam (`internal/app/errors.go`).

### Config precedence (highest → lowest)
1. CLI flags (per-setting override flags — win when explicitly set; see `docs/configuration.md`).
2. Environment variables `CODEMAP_*` (e.g. `CODEMAP_CONFIG`, `CODEMAP_DATA`, `CODEMAP_EMBEDDING_MODEL`,
   `CODEMAP_OLLAMA_URL`, `CODEMAP_EXCLUDE_EXTRA`, `CODEMAP_DAEMON_*`).
3. Project-root `codemap.yaml` / `codemap.yml`.
4. Project `.config/codemap.yaml` (XDG-style, in-repo).
5. Global `$XDG_CONFIG_HOME/codemap/config.yaml` (fallback `~/.config/codemap/config.yaml`).
6. `~/.codemap/config.yaml` (legacy/ecosystem fallback, only if it exists).
7. Built-in `DefaultConfig()`.

Most config-file settings are reachable all three ways — file, env, flag — with the flag
winning when set (two exceptions: `daemon.embed_cache_size` is file+flag only, `index.extract_concurrency`
is file+env only; see `docs/configuration.md`).

Data (graph DB, veclite, project registry) lives in `$XDG_DATA_HOME/codemap/`
(fallback `~/.local/share/codemap/`); cache in `$XDG_CACHE_HOME/codemap/`. If `~/.codemap/`
already exists it is honored for back-compat with the vecgrep/noted ecosystem.
`codemap init --local` keeps repo-local state. Config format is **YAML** (matches the
ecosystem; `gopkg.in/yaml.v3`).

## Common Tasks for Agents

**Add a CLI command:** the CLI is split per-command under `cmd/codemap/` (main.go plus
annotate/branch/cache/config/daemon/index/init_status/query). Add the cobra command var +
`init()` registration + a `runX` handler in the matching file (or a new one); the handler
opens a session and calls `internal/app`. Support `--json` for machine output.

**Add an MCP tool:** define a typed input struct (json + jsonschema tags) in
`internal/mcp/server.go`, register with `mcp.AddTool`, and have the handler delegate to
`internal/app`. Keep it thin. Update the tool list in README/docs.

**Add a graph query:** implement traversal in `internal/graph/queries.go` (BFS with a
visited set — **always detect cycles**), expose it through `internal/app` (a `service_*.go`
method), then surface in CLI + MCP + TUI.

**Data-model change:** edit `internal/graph/schema.go` with a migration; bump the schema
version; never break an existing index without a rebuild path.

**Add a studio tab/widget:** add the view code in `internal/tui/view.go` and the
state/keys in `model.go` (there is no `update.go` — model.go holds the update logic; no
`tab_*.go` convention). Use Charm **v2** (`charm.land/...`). For charts use the hand-rolled
ASCII bars + the harmonica spring frame loop in `anim.go` — **not** ntcharts (its go.mod
`replace`s bubbletea to a fork that fights stock `charm.land/bubbletea/v2`; see CLAUDE.md).

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
