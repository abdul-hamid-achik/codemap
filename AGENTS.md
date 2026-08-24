# AGENTS.md

Instructions for AI agents (and humans) working on the **codemap** codebase. This is the
canonical source-of-truth doc; `CLAUDE.md` defers to it. `README.md` is the public-facing
intro; product usage lives in `docs/`, including `docs/configuration.md` and the public
agent guide at `docs/agents.md`. The design rationale (architecture, why-it-is-what-it-is)
lives in the Obsidian vault at `~/notes/projects/codemap/design-rationale.md`. Live work is
tracked in the vault's `BACKLOG.md` (`~/notes/projects/codemap/BACKLOG.md`) — not in the repo.

## Project Overview

codemap is local-first code intelligence: it combines a **structural code graph**
(LSP + stdlib parsers; tree-sitter remains planned and is not shipped) with **semantic
retrieval** (local veclite plus an optional one-hop vecgrep fallback, or vecgrep as the
explicit owner) and
exposes both as a unified query layer for agents and people. It answers questions a grep
or a single LSP call cannot — *"who calls this and what tests cover it"*, *"what's the blast
radius of changing this type across all my projects"*, *"find auth-like code, then show me
what calls it."*

Two surfaces over one structural store (semantic retrieval may be delegated to vecgrep):
- **CLI** — human commands *and* `--json` machine output for agents (Cobra).
- **MCP server** — `codemap serve` (stdio), `codemap_*` tools for agents.

Product docs for humans start at `docs/quick-start.md`; agent workflows at `docs/agents.md` and `docs/mcp.md`. The former Studio TUI is not shipped — use CLI + MCP instead (see `docs/studio.md`).

Key features:
- **Structural graph** — nodes (files, functions, types, methods, tests) + `calls`/`defines` edges
  in pure-Go SQLite (Go, Ruby, and Lua call edges are name-based by default, Go exact via `go/types`
  with `--precise`; base TS/JS gets name-based JSX component-usage call edges, import edges, and
  Next.js framework-wiring references via `tsscan`, while plain TS/JS/Python function-call edges
  come only from `--precise` via `callHierarchy`;
  Vue SFC script blocks currently produce symbols + `defines` + import edges but no call graph;
  Go callback/handler uses and TS/JS framework wiring are persisted as `references`; Go imports are
  package-scoped evidence, TS/JS/Vue/Ruby/Lua imports resolve file→file;
  `implements`/`overrides` remain reserved for planned backends).
- **Local semantic search** — under the `local`/`fallback` backend with embeddings enabled,
  node source text is embedded through the configured Ollama-compatible endpoint
  (`http://localhost:11434` and `nomic-embed-text`, 768-dim, by default) into veclite; vector +
  BM25 hybrid (RRF) search. A remote endpoint is explicit opt-in and receives the source text
  sent for embedding.
- **Hybrid queries** — `codemap_context` (one-call source + wiring + impact), `codemap_impact`
  (blast radius + test coverage), `codemap_review` (diff → impact + tests), and confidence-aware
  file dependency analysis.
- **Cross-project** — the graph spans all registered projects.
- **Incremental** — hash-based reindex; embedding-profile guard forces rebuild on
  provider/model/dimensions/distance change.
- **Offline structural queries** — the stored graph remains queryable without LSP servers
  running; one-off precise callers/callees may launch the appropriate server, and semantic
  retrieval depends on whichever local or delegated backend is configured.

## Directory Structure

```
codemap/
├── cmd/codemap/              # cobra CLI, split by domain (vecgrep style): main.go +
│                            #   agent/annotate/branch/cache/config/context/coverage/daemon/
│                            #   dependencies/explore_traverse/gate/index/init_status/map/query/
│                            #   structural_export/structural_manifest (plus tests/helpers).
│                            #   Each RunE handler is THIN → opens a session → calls internal/app.
│                            #   Files carry the header `/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */`.
├── internal/
│   ├── app/                 # shared service layer; CLI + MCP + TUI all call this
│   │   ├── service_core.go        #   Service + Session wiring, common helpers
│   │   ├── service_init.go        #   init / index / status
│   │   ├── service_query.go       #   callers / callees / path / symbols / find / source
│   │   ├── service_relations.go   #   precise callers/callees (callHierarchy / go/types)
│   │   ├── references.go          #   inbound callback/handler value-reference query
│   │   ├── service_impact.go      #   impact / blast radius / hotspots / orphans / read-order
│   │   ├── service_context.go     #   context (the one-call bundle) + context_batch
│   │   ├── service_semantic.go    #   semantic search (veclite hybrid) + find fallback
│   │   ├── service_annotations.go #   annotate / annotations / unannotate (knowledge layer)
│   │   ├── service_grep.go        #   indexed exact-text grep + enclosing-symbol selectors
│   │   ├── session.go             #   open/close store + veclite + provider (lazy)
│   │   ├── review.go              #   diff-scoped impact (git diff → symbols → blast + tests)
│   │   ├── context_batch.go       #   bounded multi-symbol context planner
│   │   ├── coverage.go            #   precise call-graph coverage rollups
│   │   ├── dependencies.go        #   grouped file dependency evidence + per-domain completeness
│   │   ├── file_impact.go         #   dependency evidence + conservative delete verdict / breaking_change
│   │   ├── risk.go                #   0..1 change-risk score
│   │   ├── readorder.go           #   entrypoint + hub ranking
│   │   ├── service_map.go         #   bounded architecture overview (subsystems/bridges/hubs/entrypoints)
│   │   ├── service_explore.go     #   intent search → bounded exact structural neighborhoods
│   │   ├── service_traverse.go    #   durable-selector heterogeneous graph traversal
│   │   ├── secret_impact.go       #   secret-key rotation blast radius (names only)
│   │   ├── secret_scan.go         #   candidate secret-key name discovery (never values)
│   │   ├── branchswitch.go        #   branch-aware index switching
│   │   ├── cache.go               #   fcheap content-addressed index cache
│   │   ├── agentsetup.go/playbook.go # harness registration + generated in-band workflow
│   │   ├── structural_export.go   #   deterministic vecgrep structural feed
│   │   ├── structural_manifest.go #   lightweight identity/freshness preflight
│   │   ├── symbol_selector.go     #   durable selector validation/resolution helpers
│   │   ├── doctor.go              #   environment + daemon health checks
│   │   ├── docs.go                #   the in-band agent guide (codemap docs / codemap_docs)
│   │   ├── errors.go              #   CodedError (stable machine codes) for --json failures
│   │   └── vecgrep_client.go      #   vecgrep semantic owner/fallback + memory recall
│   ├── graph/                # SQLite graph store (pure Go, modernc.org/sqlite)
│   │   ├── store.go          #   Open/Close, CRUD for nodes/edges/projects, stats
│   │   ├── schema.go         #   SQL schema + migrations (PRAGMA user_version); edges.provenance
│   │   ├── queries.go        #   callers/callees, blast radius, hotspots/orphans/path
│   │   ├── topology.go       #   deterministic source-path subsystems + directed bridges
│   │   ├── traversal.go      #   bounded cycle-safe typed-edge walks
│   │   ├── dependencies.go   #   deduped inbound call/reference/import evidence
│   │   └── references.go     #   project-scoped inbound references with ambiguity metadata
│   ├── extract/              # code structure extraction (pluggable backends)
│   │   ├── extractor.go      #   Extractor interface + Symbol/Reference/FileResult
│   │   ├── gosrc/            #   stdlib go/parser backend (pure Go, default for Go)
│   │   ├── typesrc/          #   in-process go/types pass (Go --precise; exact call edges)
│   │   ├── lspsrc/           #   LSP-backed extractor (documentSymbol → symbols; callHierarchy)
│   │   ├── tsscan/           #   name-based TS/JS enrichment (imports, JSX usage, Next.js wiring)
│   │   ├── rubysrc/          #   pure-Go Ruby line scanner (symbols, name-based calls, requires)
│   │   ├── luasrc/           #   pure-Go Lua line scanner (symbols, name-based calls, requires)
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
│   │   └── import_index.go   #   resolve import specifiers to file→file import edges
│   ├── mcp/server.go         # stdio MCP server — THIN pass-through to internal/app
│   │                        #   (44 full; 26 agent/core — CODEMAP_MCP_PROFILE)
│   ├── tui/                   # studio TUI (Charm v2): model/view/theme/run + anim + highlight
│   │   ├── model.go          #   state, msgs, commands, key handling (Graph/Metrics/Impact/Search/Path)
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
│   # planned: extract/treesitter (keep optional if added), extract/scip
├── docs/                      # VitePress site (product docs ONLY) → deployed to Vercel
├── integrations/              # harness distribution: claude-code/ Claude Code plugin
│                              #   (.claude-plugin/plugin.json + .mcp.json + skills/using-codemap/SKILL.md
│                              #   [generated by RenderPlaybook, pinned by TestPlaybookSyncClaudeSkill] + commands/)
│                              # + github-action/ composite CI action (review → sticky PR comment +
│                              #   fail-on-untested/-risk gates; bash+jq, reused by gitlab/ mirror;
│                              #   `task action:test` runs its regression suite; dogfooded by
│                              #   .github/workflows/codemap-review.yml via `uses: ./integrations/github-action`;
│                              #   consumers: `uses: abdul-hamid-achik/codemap/integrations/github-action@main`)
├── .claude-plugin/            # marketplace.json (repo-root plugin marketplace entry)
├── specs/                     # glyphrun E2E specs (*.yml, 47): version/help/index_status/query/context/
│                              #   annotations/staleness/incremental/config/index_progress/mcp_serve/
│                              #   studio(+_ts)/semantic/precise/typescript/javascript/python/jsx/
│                              #   polyglot/review/read_order/risk/file_impact/daemon/cache_cli/semantic_degraded/cache_export/grep/
│                              #   exclude_extra/index_watch/timing/progress_eta/onboarding/coverage/
│                              #   ts_impact_note/studio_visuals/index_via_daemon/selectors/dependencies/
│                              #   review_deletion/references/studio_annotations/agent_setup/review_gate
├── Taskfile.yml .golangci.yml .goreleaser.yaml glyphrun.config.yml .pre-commit-hooks.yaml
├── .github/workflows/         # ci.yml + release.yml
└── README.md AGENTS.md CLAUDE.md LICENSE
```

**Package boundaries are part of the contract.** The dependency direction is one-way:
`cmd → app → {graph, vector, index, extract, embed, lsp, config, daemon, git, snapshot,
branchstate, cachestate}`. The `tui`, `mcp`, and CLI RunE handlers are all *thin* and call
`internal/app`. Never put business logic in `mcp` or `tui`. (Same rule glyphrun documents
for its own MCP package.)

## Documentation Discipline (read this)

- `docs/` is a **deployed VitePress site** for product documentation **only**. Single
  hosting path: **Vercel** — no GitHub Pages. Git auto-builds **`main` only**
  (`vercel.json`). Feature branches do not create Preview deployments.
  `ignoreCommand` skips non-docs commits. Do not `vercel promote`; `main` is the
  docs release. CLI binaries ship from tags. Keep public usage/configuration in the relevant
  product pages (`docs/quick-start.md`, `docs/configuration.md`, `docs/agents.md`,
  `docs/mcp.md`, `docs/languages.md`) instead of making users depend on this contributor guide.
- Repo root carries exactly these markdown files: `README.md`, `AGENTS.md`, `CLAUDE.md`.
  **Do not** create scratch / handoff / TODO / design `.md` files anywhere in the repo.
  (Design rationale lives in the vault: `design-rationale.md`.)
  The discipline restricts only root `.md` files: `.claude-plugin/marketplace.json` at the
  repo root is the documented Claude Code plugin-marketplace mechanism (a JSON directory, not
  a `.md`), and the `integrations/claude-code/` plugin's `SKILL.md`/`commands/*.md` are
  checked-in generated product artifacts (pinned by `TestPlaybookSyncClaudeSkill`), not notes.
- Working notes, handoffs, investigations, design exploration, AND the build-loop
  backlog go to the **Obsidian vault** at `~/notes/projects/codemap/` (use the
  `obsidian-cli` skill), not the repo. The backlog lives at
  `~/notes/projects/codemap/BACKLOG.md` (moved out of the repo 2026-07-13); keep it
  terse and current there.

## Development Commands (Taskfile, version 3)

```
task                 # list tasks
task doctor          # check go, ollama (+ nomic-embed-text), task, glyph, golangci-lint
task setup           # deps + tools (docs deps: task site:deps)
task build           # build → ./bin/codemap (ldflags inject version)
task test            # go test ./...
task race            # CGO_ENABLED=1 go test -race ./...
task lint            # golangci-lint (or go vet + gofmt -l)
task lint:strict     # exact pinned linter; fails if unavailable (release/CI)
task fmt             # gofmt -w .
task check           # fmt + lint + test  (aliases: ci, verify)
task verify:source   # non-mutating gofmt + go mod tidy checks
task check:verify    # non-mutating source checks + strict lint + test (release/CI gate)
task flows           # glyph run specs/*.yml  (E2E; local only — not run in CI)
task site:deps       # frozen Bun install for the docs site
task site:dev        # VitePress dev server (Bun)
task site:build      # VitePress build
task ship            # check:verify + docs + race + build + flows
task install         # go install ./cmd/codemap
```

## Prerequisites

- **Go 1.25+** (toolchain pinned `1.25.x`, matching the ecosystem).
- **Ollama** running with `nomic-embed-text` pulled (`ollama pull nomic-embed-text`) for
  embeddings. Embedding tests skip if Ollama is unreachable.
- **Task** (`go install github.com/go-task/task/v3/cmd/task@latest`).
- **glyph** (glyphrun) for E2E specs; **Bun** for docs; **golangci-lint** for lint.
- LSP servers for LSP-backed indexing: `typescript-language-server` (TS/JS/Vue) and
  `pyright-langserver` (Python). `gopls` is used for one-off precise Go relations and Studio's
  precise toggle; graph-wide Go `--precise` uses in-process `go/types`.

## Architecture Notes

### Extraction (Go + TypeScript + JavaScript + Python + Ruby + Lua + Vue SFC)
- **`gosrc` (`go/parser`), `rubysrc`, and `luasrc` are always registered** (Go: full call graph,
  `--precise` adds exact edges via `go/types`; Ruby and Lua are pure-Go line scanners producing
  symbols, name-based call references, and `require` imports — heredoc/`=begin`/string-safe for
  Ruby, long-string/comment-safe for Lua; no precise pass for either yet).
  **`lspsrc` (the LSP backend) is now wired in**: `IndexProject` runs a present-aware
  `registerLSP` that, for each `lspsrc.DefaultServers` spec, spawns its server **once** if any of the
  server's languages are present and the binary is on PATH, then registers an extractor per present
  language sharing that one connection (via `Extractor.Bind`) — so **one `typescript-language-server`
  serves both TypeScript and JavaScript** (`.ts/.tsx/.mts/.cts` + `.js/.jsx/.mjs/.cjs`), each
  routed with its own LSP `languageId`. Calls resolve **across** the `.ts`↔`.js` boundary.
  **Python** uses `pyright-langserver` (its own spec/process). With the server available, normal
  indexing writes symbols + `defines` edges — and for TS/JS, `lspsrc.ExtractFile` calls
  `tsscan.Enrich`, adding name-based import specifiers, JSX component-usage call references
  (`.tsx`/`.jsx` only; member expressions resolve to the root binding, lowercase intrinsics never
  match, comments/strings are sanitized), and Next.js framework-wiring references (App Router
  special files, `route.ts` HTTP verbs, middleware, Pages Router) so React components and
  framework entrypoints stop reading as orphans. These edges carry candidate weight (0.7);
  `--precise` additionally resolves exact call edges via
  `callHierarchy` in `Indexer.resolveLSPCallEdges` and supersedes the candidates per file
  (an EMPTY on-demand precise answer never erases non-empty name-based candidates —
  see `autoUpgradeRelation`). The import resolver understands `@/`/`~/` aliases and
  monorepo workspace packages via `package.json` names (`exports`/`module`/`main`), resolved
  deterministically on duplicate names. `appendSymbols` drops
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
- **tree-sitter is not implemented today.** `internal/extract/treesitter` is a planned backend;
  there is no current tree-sitter dependency or `treesitter` build-tagged implementation. If it
  is added, keep the CGO-dependent backend optional and out of default `CGO_ENABLED=0` releases
  until its build matrix and accuracy fixtures are established.
- LSP client is **hand-rolled in `internal/lsp`** (no third-party deps): a Content-Length
  framed JSON-RPC 2.0 conn + `Initialize`/`DidOpen`/`DocumentSymbols`/`References` and call
  hierarchy methods. The wired `internal/extract/lspsrc` backend maps `documentSymbol` to
  codemap symbols and implements `CallResolver`; the indexer drives its `callHierarchy` under
  `--precise` and records per-file coverage. **LSP uses Content-Length framing — correct for
  LSP, and it must NOT leak into the MCP transport (which is newline-delimited).**

### Storage
- Graph: `modernc.org/sqlite` (pure Go), WAL mode, transaction-batched writes,
  `synchronous=NORMAL`, `SetMaxOpenConns(1)`. Tables: `nodes`, `edges`,
  `projects`, `index_state`, `call_graph_coverage`, `annotations` (schema v6). The
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
  model, dimensions, and optional bearer token are configurable. Ollama is the only implemented
  provider; pointing `ollama_url` at a remote Ollama-compatible service explicitly sends source
  text there. OpenAI/Cohere/Voyage providers are not implemented and must not be advertised as
  current options.
- **EmbeddingProfile guard**: store provider/model/dimensions/distance in collection metadata;
  a mismatch on any field fails the index with explicit rebuild guidance (never silently mix
  embedding spaces). There is no chunker field in the current profile.

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
- Tool names are `codemap_`-prefixed. Current set (44, full profile; agent/core = 26): `init`, `index`, `status`, `doctor`, `semantic`, `callers`, `callees`, `references`, `impact`, `file_impact`, `file_context`, `refactor_plan`, `dependencies`, `review`, `secret_impact`, `required_keys`, `risk`, `hotspots`, `orphans`, `coverage`, `read_order`, `map`, `explore`, `traverse`, `path`, `related_files`, `symbols`, `symbol_at`, `find`, `grep`, `source`, `context`, `context_batch`, `projects`, `docs`, `annotate`, `annotations`, `unannotate`, `branch_status`, `branch_switch`, `cache_save`, `cache_restore`, `cache_list`, `cache_drop`. Project-scoped tools generally take an optional `path` (project dir, defaults to cwd) and return JSON; `projects`, `docs`, and `doctor` take no project path. Callers/callees take `precise`; `references` returns bounded callback/handler value-use sites with partial-coverage confidence; `map` returns bounded source-path subsystems, directed cross-subsystem bridges, entrypoints, and hubs; `explore` turns an intent query into bounded semantic/name seeds plus exact context neighborhoods; `traverse` walks selected relationship domains from a required durable selector with per-edge confidence; `dependencies` returns bounded inbound call/reference/import evidence plus domain coverage; `source` returns a symbol's body; `context` bundles a symbol's definition+callers+callees+value references+covering tests+blast radius; `refactor_plan` plans a rename/move (call sites, value references, dependent files, covering tests, blast radius); `docs` returns the agent guide; `annotate`/`annotations` pin/list notes on a symbol or `from→to` path (`external_id` makes automated writes idempotent within project + source); `coverage` returns per-file precise call-graph coverage rolled up by language/directory — the project-wide, per-file signal behind the per-query `call_graph` enum. `codemap_map`, `codemap_traverse`, and `codemap_refactor_plan` are registered only in the `full` MCP profile.
- **Tool profiles**: `CODEMAP_MCP_PROFILE=agent|core|full` (env) / `mcp.profile` (config file) /
  `--profile` on `codemap serve` (flag) gates the registered set at the go-sdk `mcp.AddTool` call
  site (`Server.include`, `internal/mcp/server.go`) — registration-time only, zero behavior change
  for any tool that IS registered. Default `full` is the back-compatible 44-tool expert surface.
  `agent` is exactly the 25 tools named by `RenderPlaybook`/the workflow topic plus `codemap_docs`;
  `TestAgentProfileExactlyMatchesTaughtWorkflow` pins both inclusion and exclusion. `core` preserves
  its shipped 26-tool inventory and currently matches `agent`, but is a separate compatibility
  contract. The hermetic `BenchmarkProfileSchemaTax` measures 31,151 schema characters (≈7,788
  schema-approx-tokens) for agent/core versus 44,487 (≈11,122) for full on the current 44-tool
  build. Lean profiles also help harnesses with a hard tool-count ceiling (Cursor caps
  ~40 across ALL MCP servers — `codemap agent setup cursor` defaults its generated entry to
  `core` for exactly this reason; every other harness stays `full`).
- Exact-definition inputs use the durable source-selector projection
  `{file,start_line,fqn,kind}`, never a SQLite node id. `source`, `context`, `traverse`,
  `callers`, `callees`, `impact`, and `risk` accept `selector`; `path` accepts
  paired `from_selector`/`to_selector`. Resolution prefers file+FQN+kind so
  ordinary line shifts survive reindex, with start_line as tie-break/fallback.
- The CLI exposes the same service reports for overlapping operations, but it is not a strict
  flag-for-field mirror: CLI `index` additionally has `--watch` and `--no-lsp` (MCP
  `codemap_index` currently has neither), and several operational surfaces are CLI-only. Current
  commands: `init`, `index` (`--reindex`/`--no-embed`/`--precise`/`--watch`/`--no-lsp`), `status`, `config path/show`, `doctor`, `projects`, `callers`/`callees` (`--precise`), `path`, `impact` (`--depth`; repeat `--at`, optionally `--batch`, for bounded partial-success frame batches), `file-impact`, `dependencies`, `review` (`--staged`/`--since`), `secret-impact`, `required-keys`, `risk`, `hotspots`/`orphans` (`--top`), `semantic` (`--top`), `read-order`, `map` (`--top-subsystems`/`--top-bridges`/`--top-hubs`/`--top-entrypoints`), `explore <query>` (`--seeds`/`--edges`/`--depth`), `traverse --at <file>:<line>` (`--direction outgoing|incoming|both`/`--edge-types` CSV/`--depth`/`--limit`), `symbols`, `symbol-at`, `find`, `grep` (`--regex`/`-i`), `source`, `context` (multi-arg → batch), `related-files`, `annotate` (`--external-id` for retry-safe writes)/`annotations`, `branch-status`/`branch-switch`/`branch-snapshot`, `cache save`/`restore`/`list`/`drop`, `cache export`/`import` (`--force`; portable team/CI-shareable
index tarballs — no fcheap/shared store, CLI-only, no MCP tool), `daemon start`/`status`/`stop`, `agent setup`/`list`/`playbook` (register codemap with an AI coding harness — CLI-only, no MCP tool), `docs`, `serve` — query commands accept `--json`.
  `structural-manifest` and `export-symbols` are also CLI-only: the former is a lightweight
  `codemap.structural-manifest.v1` identity/freshness preflight that streams indexed metadata
  without source bodies; the latter is the deterministic paginated `codemap.structural-export.v1`
  feed for vecgrep `structural_chunks` (`auto|off|required`). Neither shares a database/package.
- **Accuracy model** (be honest with users): the graph is name-based by default — intra-package
  calls resolve precisely (Go), but cross-package method calls (`x.Foo()`) link to every same-named
  method (no type info). codemap flags this (`callers`/`impact` note ambiguous names; `hotspots`
  marks inflation; `orphans` follows functions wired by value — handlers like cobra `RunE` /
  `mux.HandleFunc(s.h)`, and in TS/JS the tsscan JSX/framework-wiring references — via
  `references` edges that never enter the call graph — but stays
  interface/reflection-blind and cannot see components passed only as props
  (`Link={AuthLink}`) or wrapped default exports (`export default memo(Page)`), so its results
  are *candidates*). **The graph-wide
  fix is shipped: `codemap index --precise`** (CLI) / `codemap_index precise:true` (MCP) is the unified
  exact-resolution pass. For Go it runs an in-process pure-Go `go/types` pass (`internal/extract/typesrc`);
  for the LSP languages (TypeScript, JavaScript, Python) it drives the language server's `callHierarchy`
  (`Indexer.resolveLSPCallEdges`). It resolves each call to the one it invokes, writes precise call
  edges via `edges.provenance`, and records successful coverage per file. Query confidence is classified
  from the matching definition files: all covered → `resolved`; uncovered TS/JS/Python/Vue wins →
  `unresolved`; otherwise parser-backed (Go/Ruby/Lua) → `name`. One precise edge never upgrades an unrelated file, and
  changed files lose coverage until their precise pass succeeds again. TS/JS get name-based
  candidate edges for JSX component usage, imports, and Next.js framework wiring; plain TS/JS
  function calls and all Python calls have **no** name-based edges, so `--precise` is
  what gives those languages a complete call graph (for Go it *replaces* the name-based edges;
  name-based stays the Go/Ruby/Lua
  default). The Go pass degrades
  per-package on type errors and wholesale (with a note) when the `go` toolchain/module is unavailable.
  `callers`/`callees --precise` (`precise:true` in MCP) remains the per-query language-server path for a one-off without
  reindexing.
- **Stable machine contract**: every impact/callers/callees/references/review/context/hotspots/orphans/path/map/traverse report carries a
  `call_graph` enum (`resolved`/`name`/`unresolved`/`none`) so a consumer can switch on
  confidence (resolved→high, name→medium, unresolved/none→low) without parsing the free-form
  `resolution` sentence. `codemap review` additionally folds one aggregate `risk` band
  (`level`/`score`/`factors`) from every changed symbol so a harness can gate verification on a
  single call; `level` is `unknown` when any changed symbol lacks a usable call graph or review
  analysis is incomplete, otherwise `low`/`medium`/`high`. `analysis_complete` and the additive
  `total_symbols`/`analyzed_symbols`/`truncated_symbols` counts expose staleness, the 200-symbol work cap, and
  any bounded structured `partial_errors` (including failed structural-source symbol mapping, deletion-only source hunks
  with no post-image range, recognized callable/type declaration lines removed in mixed or equal-count source hunks, and
  exact source renames with no mapped symbols), so a successful subset never looks authoritative for
  the whole diff. The `blast_radius`/`covering_tests` elements are `ImpactNode` objects
  (`symbol`/`fqn`/`kind`/`file`/`start_line`/`depth`; no `end_line`) — the stable element shape.
  Successful review JSON always emits `schema_version: 1` and is governed by
  `schemas/codemap.review.v1.schema.json`. Canonical keys are snake_case; additive optional fields
  include those review-completeness signals and `deletion_analysis`
  (`files`/`analyzed`/`missing`/`source:last_index`/`complete`) when a
  diff deletes files, so consumers know whether retained pre-delete definitions were available, and
  are compatible within v1. Renames, removals, required-field additions, enum narrowing, or
  nested shape changes require a new schema major and a consumer dual-read window. Keep the hard
  CLI error envelope outside the success schema.
  `explore` and `traverse` likewise emit `schema_version: 1`; `explore` caps intent seeds and each
  joined context neighborhood, while `traverse` caps depth/nodes and reports per-domain confidence.
- **Dependency confidence contract**: `dependencies` and embedded `file_impact.dependency_evidence`
  preserve legacy totals and add `confirmed_total`/`candidate_total` at report, dependent-file,
  and evidence-kind levels. Samples carry `confidence` (`confirmed|candidate`) and a stable reason.
  Only fresh confirmed file-scoped evidence may produce `delete_verdict:"unsafe"`; name fan-out,
  package-scoped imports, stale snapshots, and missing domains remain `unknown`.
- MCP text results use compact JSON (the structured payload is unchanged) so agents do not spend
  context tokens on indentation. HTML escaping remains disabled for readable code/FQNs.
- **CLI exit-code taxonomy** (extends P2-06): `0`=answered, `1`=operational error,
  `2`=not found/not indexed, `3`=`index_missing`, `4`=`index_corrupt`, `5`=`not_a_repo`,
  `6`=`gate_failed`. Under `--json`, ANY failure (codes 1-5) prints a structured envelope to **stdout**:
  `{"ok":false,"error":"…","code":"not_found|not_indexed|index_missing|index_corrupt|not_a_repo|operational","hint":"run: codemap index"}`
  (the `code` matches the exit-code suffix). The `cmd/codemap/jsonHandler` wraps every RunE;
  hard failures are wrapped as `app.CodedError` at the `Session.Graph()` seam (`internal/app/errors.go`).
  Exit **6** (`gate_failed`, `cmd/codemap/gate.go`) is different in kind: `codemap review`/`codemap risk`
  accept `--fail-on-risk <low|medium|high>` and (`review` only) `--fail-on-untested`; after printing
  the normal, unchanged output (human or `--json` success envelope — never an `{"ok":false,...}`
  failure envelope), the process exits 6 if the aggregate/symbol risk level's ordinal is at or above
  the threshold, or `--fail-on-untested` finds a non-empty `untested_symbols` or mapped symbols whose
  test coverage is unresolved. On an otherwise complete report, `level:"unknown"` never trips the
  risk comparison (the honesty rule — an unresolved call graph is not evidence of risk). Enabling either gate on `review` separately fails
  closed with exit 6 when an indexed Git repository reports `analysis_complete:false`; reporting-only
  reviews and early non-repository/no-index degradation remain exit 0. A
  ready-made `.pre-commit-hooks.yaml` at the repo root runs `codemap review --staged --json
  --fail-on-untested` (name-based, not `--precise`, for hook speed; an unindexed project or non-git
  directory degrades to exit 0, while a stale/partial indexed review fails closed).

### Config precedence (highest → lowest)
1. CLI flags (per-setting override flags — win when explicitly set; see `docs/configuration.md`).
2. Environment variables `CODEMAP_*` (e.g. `CODEMAP_CONFIG`, `CODEMAP_DATA`, `CODEMAP_EMBEDDING_MODEL`,
   `CODEMAP_OLLAMA_URL`, `CODEMAP_SEMANTIC_BACKEND`, `CODEMAP_EXCLUDE_EXTRA`, `CODEMAP_DAEMON_*`).
3. Project-root `codemap.yaml` / `codemap.yml`.
4. Project `.config/codemap.yaml` (XDG-style, in-repo).
5. Global `$XDG_CONFIG_HOME/codemap/config.yaml` (fallback `~/.config/codemap/config.yaml`).
6. `~/.codemap/config.yaml` (legacy/ecosystem fallback, only if it exists).
7. Built-in `DefaultConfig()`.

Most config-file settings are reachable all three ways — file, env, flag — with the flag
winning when set. `semantic.backend` is `fallback|local|vecgrep`; explicit vecgrep mode skips
codemap vector writes and surfaces adapter failures. Exceptions include
`daemon.embed_cache_size` (file+flag), `index.extract_concurrency` (file+env), and
`semantic.fusion_weights.*` (file only); see `docs/configuration.md`.

Data (graph DB, veclite, project registry) lives in `$XDG_DATA_HOME/codemap/`
(fallback `~/.local/share/codemap/`); cache in `$XDG_CACHE_HOME/codemap/`. If `~/.codemap/`
already exists it is honored for back-compat with the vecgrep/noted ecosystem.
`codemap init --local` creates a repo-local `.codemap` marker so project config is found from
subdirectories; graph/vector data remains central unless `CODEMAP_DATA` points inside the repo.
Config format is **YAML** (matches the ecosystem; `gopkg.in/yaml.v3`).

## Common Tasks for Agents

**Add a CLI command:** the CLI is split by domain under `cmd/codemap/` (for example,
`agent.go`, `cache.go`, `context.go`, `dependencies.go`, `explore_traverse.go`, `index.go`,
`map.go`, `query.go`, and the structural export/manifest files). Add the cobra command var +
`init()` registration + a `runX` handler in the matching domain file (or a new one); the
handler opens a session and calls `internal/app`. Support `--json` for machine output.

**Add an MCP tool:** define a typed input struct (json + jsonschema tags) in
`internal/mcp/server.go`, register with `mcp.AddTool`, and have the handler delegate to
`internal/app`. Keep it thin. Update the tool list in README/docs.

**Change the agent playbook / add a harness:** the "when to use codemap" guidance has ONE
source — `docs.go` (`docTopics`) plus the short `playbookPreamble` in `internal/app/playbook.go`.
Never type tool guidance into a per-harness file; route it through `RenderPlaybook`. After any
change to the preamble or the `workflow`/`accuracy` topics, regenerate the pinned plugin skill:
`go run ./cmd/codemap agent playbook --format claude-skill > integrations/claude-code/skills/using-codemap/SKILL.md`
(`TestPlaybookSyncClaudeSkill` fails until you do). New harnesses are registry rows in
`internal/app/agentsetup.go`; keep `cmd/codemap/agent.go` thin and open no DB.

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

`task check` (fmt + lint + test) during development; use the non-mutating `task check:verify`
for CI/release verification. Then `task build` → `task flows` if specs changed. Keep docs
discipline (no stray `.md`; product docs in `docs/`, notes in `~/notes`). Commit/push only
when the user asks.

## Related projects (Obsidian wikilinks)

[[../veclite/index|VecLite]] · [[../vecgrep/index|vecgrep]] · [[../noted/index|noted]] ·
[[../glyphrun/index|glyphrun]] — siblings under `~/projects`. codemap mirrors vecgrep's CLI +
config + MCP conventions and noted's three-surface pattern.
