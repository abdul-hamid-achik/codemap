# codemap

Local-first code intelligence that gives AI agents and people **structural awareness** of
codebases — combining a code graph (LSP + parsers) with semantic vector search (veclite),
exposed through a CLI, an MCP server, and an interactive terminal UI.

> Working on the code? Read [AGENTS.md](./AGENTS.md) first — it is the source of truth for
> conventions, architecture, and gotchas.

codemap answers questions that grep and a single LSP call can't: *who calls this function and
which tests cover it*, *what's the blast radius of changing this type*, *find auth-like code and
then show me everything that calls into it*. It precomputes the
structure once, then serves narrow, structured answers — so an agent spends a few tool calls
instead of dozens of file reads.

## Features

- **Structural code graph** — files, functions, types, methods, and tests as nodes; **call** edges
  (name-based by default, exact via `go/types` with `--precise`) and **defines** edges (file → symbol).
  Test coverage is derived by walking the call graph to test nodes. Stored in pure-Go SQLite,
  queryable offline. Indexes **Go** (stdlib `go/parser`, full call graph), **TypeScript + JavaScript**
  (one `typescript-language-server`, calls resolving *across* the `.ts`↔`.js` boundary), **Python**
  (`pyright-langserver`), and **Vue SFCs** (`.vue` — `<script>`/`<script setup>` blocks routed to the
  same `typescript-language-server`, symbol lines mapped back onto the original `.vue` file) — the
  LSP-backed languages give symbols + structure always, plus a **precise call graph** under
  `--precise`, so `callers`/`impact`/`hotspots`/`path` work for them too. Semantic search is
  language-agnostic.
- **Semantic search** — every node's source is embedded (Ollama `nomic-embed-text`, 768-dim)
  into [veclite](https://github.com/abdul-hamid-achik/veclite); vector + BM25 hybrid search.
- **Impact analysis** — `impact` returns a symbol's definition sites, direct callers, the
  transitive blast radius (everything affected by a change), and which tests cover those
  paths (flagging untested code).
- **Built for agent harnesses** — `context <symbol>` bundles definition + callers + callees +
  value-reference wiring + tests + blast radius in **one call** (no multi-round-trip stitching),
  and `status` reports index
  *freshness* (files changed since indexing) so an agent reindexes before trusting a stale answer.
- **Exact, reusable source selectors** — every symbol result already carries `file`, `start_line`,
  `fqn`, and `kind`. Agents project those fields into a `selector` for `source`/`context`/`callers`/
  `callees`/`references`/`impact`/`risk` (or paired path selectors), while people use
  `--at file:line`. This picks
  one same-named definition without persisting reindex-volatile database IDs.
- **Multi-project registry** — one shared store indexes all your repos; `projects` lists what's
  indexed, and any query targets one project (resolved from cwd, or `--path`).
- **Annotations** — pin notes and external data (DB rows from mongosh/postgres, vidtrace/vecgrep
  findings, …) to a symbol or a call path; they persist across reindex. A knowledge layer over the
  graph for agent harnesses (`annotate` / `annotations`, also on MCP) and people (`a` in Studio).
- **Precise call resolution** — the fast name-based graph over-matches same-named methods
  (`x.Close()` links to *every* `Close`). `codemap index --precise` attempts to resolve each call to
  the one it actually invokes and records successful precise coverage per file. A query is reported
  `resolved` only when every matched definition file completed that pass; partial failures remain
  honestly `name`/`unresolved` instead of upgrading the whole project. It's the unified exact-resolution pass across languages: an in-process, pure-Go
  `go/types` pass for **Go**, and the language server's `callHierarchy` for the **LSP languages**
  (TypeScript, JavaScript, Python — which have no name-based call edges, so `--precise` is what gives
  them a call graph at all). Opt-in and additive (name-based stays the default for Go); the Go pass
  degrades to name-based **with a note**
  when the `go` toolchain or module isn't available. For a one-off exact answer without reindexing,
  `callers`/`callees` also take `--precise` (language-server `callHierarchy`). CLI + MCP.
- **Incremental** — hash-based reindex; an embedding-profile guard forces a rebuild when the
  provider/model/dimension changes instead of corrupting the vector space.
- **Three surfaces, one store** — a Cobra **CLI** (with `--json` for agents), a stdio **MCP
  server**, and the **studio** TUI for humans.
- **Graph analytics** — `hotspots` (hubs), `orphans` (dead-code candidates), and `path`
  (shortest call path between two symbols).
- **Local-first & private** — everything runs on your machine; no cloud, no uploads.
- **Single binary** — pure-Go, `CGO_ENABLED=0`, cross-compiled and shipped via Homebrew.

## studio (TUI)

`codemap studio` opens an interactive, full-screen explorer of your code (Charm v2 — Bubble
Tea / Lip Gloss / Bubbles). Switch tabs with `1`–`5` or `tab`; navigate with `↑`/`↓`.

```
 codemap studio                       codemap · 509 nodes · 1849 edges · 35 files
  1 Graph   2 Metrics   3 Impact   4 Search   5 Path
 Hubs (164)                           │ lspsrc.Extractor.Close
    57  lspsrc.Extractor.Close        │  Called by (57)
    56  app.Session.Close             │   ▸ main.runInit   cmd/codemap/init_status.go:64
    56  graph.Store.Close             │     main.runIndex  cmd/codemap/index.go:24
    26  app.NewService                │  Calls (9)
    19  app.Open                      │     app.Session.Close  internal/app/session.go:126
     ▼ 159 more                       │  ⟩ func runInit(cmd *cobra.Command, ...) error
 ↑/↓ hub · → walk · enter → impact · s source · p precise · ctrl+c quit · ? help
```

Fully-qualified names disambiguate same-named symbols (six different `Close` methods above), and
the selected node's signature is previewed (`⟩ func runInit(...)`).

- **Graph** — a call-graph explorer: hubs (most-referenced symbols) on the left as jump
  points, the centered node's callers and callees on the right. Press `→` to focus the right
  pane and **walk the graph** — `enter` re-centers on a caller/callee so you can traverse the
  call chain; `backspace` steps back; `s` reads the selected symbol's **source** in a
  scrollable overlay, without leaving studio.
- **Metrics** — an overview dashboard: counts and bar charts (by kind/language) on the left;
  the call graph's two extremes on the right — top hubs (most-referenced) and dead-code
  candidates (no callers). Both lists are navigable — `enter` drills a row into Impact, `ctrl+s`
  reads its source.
- **Impact** — type a symbol, see its callers, blast radius, and which tests cover it.
- **Search** — semantic search by meaning, falling back to fast name search when there are no
  embeddings (so it works even without Ollama).
- **Path** — choose `FROM` and `TO`, then inspect the shortest directed call chain.
  The ordered nodes are keyboard-navigable and every answer shows its `call_graph` confidence,
  keeping “disconnected,” “unresolved,” and “missing endpoint” visibly distinct.
- **Knowledge capture** — press `a` on an exact selected symbol in Graph, Search, Impact, or Path
  to write an annotation without leaving Studio. The composer keeps typing isolated from global
  shortcuts and pins the note to the selected FQN.

## Installation

### Homebrew (recommended)

```bash
brew install abdul-hamid-achik/tap/codemap
```

### Prerequisites

- **Go 1.25+** (only to build from source)
- **[Ollama](https://ollama.com)** with the embedding model:
  `ollama pull nomic-embed-text` (optional — without it, indexing is structure-only)
- **[gopls](https://pkg.go.dev/golang.org/x/tools/gopls)** — optional, for one-off `callers`/`callees --precise` Go results
- Optional: **[Task](https://taskfile.dev)** for the dev workflow

**Languages** — structure + semantic search work for all of these; a precise call graph
(`callers`/`callees`/`impact`/`hotspots`/`path`) needs `--precise`:

| Language | How | Extensions | Call graph |
|---|---|---|---|
| **Go** | stdlib `go/parser` (pure Go, always) · `--precise` adds exact edges via in-process `go/types` | `.go` | name-based by default; exact via `--precise` |
| **TypeScript / JavaScript** | `typescript-language-server` (one server, JSX/TSX-aware, resolves across the `.ts`↔`.js` boundary) | `.ts` `.tsx` `.js` `.jsx` `.mjs` `.cjs` | `--precise` only |
| **Python** | `pyright-langserver` | `.py` | `--precise` only |
| **Vue SFC** | `typescript-language-server` via `vuesrc` — `<script>`/`<script setup>` block content is extracted and routed to the TS/JS delegate; symbol lines are mapped back onto the original `.vue` file | `.vue` | symbols + `defines` edges only (no `--precise` call graph yet) |

> Vue SFCs: a `.vue` file's `<script>`/`<script setup>` block (with `lang="ts"` routing to TypeScript, unmarked/`lang="js"` to JavaScript) is delegated to the same `typescript-language-server` connection that indexes plain `.ts`/`.js` files. Template/style blocks are not indexed. A project with only `.vue` files (no plain `.ts`/`.js`) spawns the server itself to serve the script blocks.

The language servers auto-enable when installed — run [`codemap doctor`](docs/cli.md) to see which are
detected, or `--no-lsp` to skip. HTML/CSS remain recognized-but-unindexed (planned); Vue SFCs
  are now indexed (see above). Other recognized languages are reported as skipped.
  Semantic search is language-agnostic.

### From source

```bash
git clone https://github.com/abdul-hamid-achik/codemap
cd codemap
task build        # → ./bin/codemap   (or: go build ./cmd/codemap)
```

### Go install

```bash
go install github.com/abdul-hamid-achik/codemap/cmd/codemap@latest
```

## Quick start

```bash
# 1. Register and index a project
codemap init                       # registers the current directory
codemap index                      # extract graph + embed nodes (incremental)
codemap index --no-embed           # structure only (no Ollama needed)
codemap index --precise            # exact call edges (Go via go/types; TS/JS/Python via callHierarchy)
codemap index --watch              # index once, then hand off to the background daemon
# Indexes your code, not your dependencies: node_modules, venv, vendor, dist, build,
# __pycache__, .git (and any dotdir) are skipped by default — configurable via `exclude`.

# 2. Check the environment and inspect config
codemap doctor
codemap config show

# 3. Orient on a symbol — everything in one call (definition, callers, callees, tests)
codemap context authenticateUser   # the one-call overview (agents: codemap_context)
codemap context --at auth.go:42    # exactly one same-named definition

# 4. Navigate the call graph
codemap callers authenticateUser   # who calls it (fast, name-based)
codemap callers authenticateUser --precise   # exact one-off callers via the language server
codemap callers --at auth.go:42    # exact definition from the indexed graph
codemap callees authenticateUser   # what it calls
codemap references authenticateUser # where it is stored/passed as a callback or handler
codemap path     Handler Login     # shortest call path between two symbols

# 5. Analyze impact and structure
codemap impact   authenticateUser --depth 3   # callers + blast radius + tests
codemap hotspots --top 20          # most-referenced symbols (hubs)
codemap orphans                    # functions with no callers (dead-code candidates)
codemap review                     # diff-scoped impact + tests to run
codemap status                     # stats + warns if the index is stale vs your files

# 6. Search by meaning (needs an embedded index)
codemap semantic "jwt validation middleware" --top 10

# 7. Explore visually
codemap studio
```

Add `--json` to any query command for machine-readable output (for agents/scripts).

The flagship `impact` answers *what breaks if I change this, and what do I run to check?* in one call
(real output, from codemap on itself):

```
$ codemap impact BlastRadius --depth 2
Impact of BlastRadius (codemap)
  defined:        internal/graph/queries.go:140
  direct callers: 4
  blast radius:   22 (depth ≤ 2)
  tests covering: 11
  covering tests (run these):
     graph.TestBlastRadius                internal/graph/graph_test.go:377
     graph.TestBlastRadiusCycleSafe       internal/graph/graph_test.go:419
     app.TestQueryResultsCarrySignature   internal/app/app_test.go:226
     app.TestImpactSurfacesAnnotations    internal/app/app_test.go:394
     app.TestImpactWarnsOnAmbiguousName   internal/app/app_test.go:1309
     app.TestServiceImpact                internal/app/app_test.go:1486
     app.TestImpactHeuristicTestCoverage  internal/app/app_test.go:1693
     app.TestImpactBlankSymbolRejected    internal/app/app_test.go:1871
     app.TestSecretImpact                 internal/app/secret_impact_test.go:79
     app.TestSecretImpactOrphanKey        internal/app/secret_impact_test.go:100
     … (1 more — use --json for all)
  affected (blast radius):
     [1] app.Service.SecretImpact             internal/app/secret_impact.go:50
     [1] app.Service.Impact                   internal/app/service_impact.go:49
   ✓ [1] graph.TestBlastRadius                internal/graph/graph_test.go:377
   ✓ [1] graph.TestBlastRadiusCycleSafe       internal/graph/graph_test.go:419
     [2] main.runImpact                       cmd/codemap/query.go:218
     [2] main.runSecretImpact                 cmd/codemap/query.go:667
   ✓ [2] app.TestQueryResultsCarrySignature   internal/app/app_test.go:226
   ✓ [2] app.TestImpactSurfacesAnnotations    internal/app/app_test.go:394
   ✓ [2] app.TestImpactWarnsOnAmbiguousName   internal/app/app_test.go:1309
   ✓ [2] app.TestServiceImpact                internal/app/app_test.go:1486
   ✓ [2] app.TestImpactHeuristicTestCoverage  internal/app/app_test.go:1693
   ✓ [2] app.TestImpactBlankSymbolRejected    internal/app/app_test.go:1871
     [2] app.Service.FileImpact               internal/app/file_impact.go:39
     [2] app.Service.Review                   internal/app/review.go:69
     [2] app.Service.Risk                     internal/app/risk.go:37
   ✓ [2] app.TestSecretImpact                 internal/app/secret_impact_test.go:79
   ✓ [2] app.TestSecretImpactOrphanKey        internal/app/secret_impact_test.go:100
   ✓ [2] app.TestSecretImpactNoValueLeak      internal/app/secret_impact_test.go:114
     [2] app.Service.Context                  internal/app/service_context.go:69
     [2] mcp.Server.handleImpact              internal/mcp/server.go:633
   … (2 more — use --json for all, or lower --depth)
Long lists are capped for readability (the nearest blast-radius nodes and the
first covering tests, with a `… (N more)` line); `--json` always carries the
complete set.

## Commands

| Group | Command | What it does |
|---|---|---|
| Project | `init` / `index` / `status` | register, index (`--reindex`, `--no-embed`, `--no-lsp`, `--precise`), show stats + freshness |
| Project | `doctor` | check the environment — toolchains, language servers, embeddings — with install hints |
| Project | `projects` | list all registered projects and their index sizes |
| Project | `config path` / `config show` | resolved config file path and values (`--json`) |
| Navigate | `callers` / `callees` | call-graph navigation (`--at file:line` selects one definition; `--precise` resolves on demand) |
| Navigate | `references` | where a function/method is used as a value (callbacks, handlers, registrations); partial-coverage confidence is explicit |
| Navigate | `path` | shortest call path; unique FQNs select exact endpoints and every result states call-graph confidence |
| Navigate | `symbols` / `symbol-at <file>:<line>` / `find` | outline a file, resolve a position, or find symbols by name |
| Navigate | `source` | print a symbol's source code |
| Navigate | `context` | **one call, everything about a symbol**: definition, callers, callees, value references, tests, blast radius |
| Navigate | `read-order` | ranked reading list for an unfamiliar repo |
| Analyze | `impact` / `dependencies` / `file-impact` / `review` | exact symbol impact, file dependency evidence, or diff-scoped tests |
| Analyze | `hotspots` / `orphans` | hubs / dead-code candidates |
| Analyze | `risk` | 0..1 change-risk score with factors |
| Analyze | `secret-impact` / `required-keys` | key-rotation blast radius and least-privilege key sets |
| Search | `semantic` (alias `search`) / `find` | meaning-based search, and offline name search |
| Cache | `cache save` / `restore` / `list` / `drop` | fcheap content-addressed index snapshots |
| Branch | `branch-status` / `branch-switch` / `branch-snapshot` | per-branch index snapshots via fcheap |
| Daemon | `daemon start` / `status` / `stop` | background watcher that keeps the index fresh |
| Knowledge | `annotate` / `annotations` | pin / list notes and external data on symbols/paths |
| Surfaces | `serve` / `studio` | MCP server (stdio) and interactive TUI |

All query commands accept `--json`.

## Accuracy: name-based vs precise

codemap's graph is **name-based by default** — instant, offline, and tolerant of broken code. It
resolves calls *within* a package precisely (Go), but a cross-package method call like `x.Close()`
links to *every* method named `Close`, because resolving the receiver's type needs a type-checker.
codemap is honest about this rather than hiding it: `callers`/`callees`/`impact` flag when a name
resolves to multiple definitions, and `hotspots` marks name-collision inflation.

**For precise graph coverage, reindex with `codemap index --precise`** (Go). It runs an in-process,
pure-Go `go/types` pass that replaces name-based edges in each package/file it resolves successfully.
Each query is `resolved` only when all definition files it matches have precise coverage; packages
that fail type-checking stay name-based, and an uncovered LSP-language definition stays `unresolved`.
On the codemap repo itself this collapses the `Close`/`Error`
fan-out (e.g. one `Close` method that name-matching credited with 71 callers drops to its real ~12)
and turns `hotspots` from name-collision noise into genuine hubs. Requirements and guarantees:

- Needs the `go` toolchain and a buildable module. A package that doesn't type-check keeps its
  name-based edges (per-package degrade); a project with no `go`/`go.mod` falls back wholesale **with
  a note** — never a hard error, and never worse than name-based.
- Purely additive and opt-in: without `--precise`, indexing is byte-for-byte the fast name-based path.
- Interface dispatch is statically undecidable, so a precise edge points at the interface method, not
  the concrete implementors.

**For TypeScript, `--precise` is the *only* source of call edges.** The name-based pass extracts TS
structure (classes, methods, functions) but not calls, so a plain `index` gives TS no call graph;
`index --precise` drives `typescript-language-server` `callHierarchy`; files it resolves gain exact
edges, while any uncovered definition remains explicitly `unresolved`. The same
`callers`/`callees`/`impact`/`hotspots`/`path` queries then use that indexed coverage with no flag of their own.
Needs `typescript-language-server` on `PATH`.

For a one-off exact answer *without* reindexing, `callers`/`callees` also accept `--precise`
(`callHierarchy` through the language server), which degrades to the indexed graph with a note
when the server can't resolve.

Precise indexing makes the **edges** exact; a bare query such as `impact Close` still deliberately
unions every definition named `Close`. To keep a follow-up on one definition, use CLI `--at
file:line`, or pass MCP `selector:{file,start_line,fqn,kind}` projected from any symbol result.
Selectors prefer file+FQN+kind, so ordinary line shifts survive reindex; moves/renames return a
miss instead of silently selecting another node. Raw SQLite node IDs are never a public contract.

File dependency evidence is confidence-aware. Exact same-package or precise relationships are
`confirmed`; qualified name fan-out, package-scoped imports, and stale snapshots are `candidate`.
`file-impact` reports `delete_verdict:"unsafe"` only for fresh confirmed file-scoped evidence;
candidate-only or incomplete evidence remains `unknown`. `review` analyzes deleted definitions from
the last indexed snapshot when available and tells an agent to run selected tests before reindexing
prunes that historical evidence.

`orphans` finds call-graph dead ends. It follows functions wired by *value* — handlers in a
table like cobra's `RunE: runInit`, callbacks passed to a registrar — and excludes methods that
implement well-known stdlib interfaces (`error`, `fmt.Stringer`, `Unwrap`, the JSON/text
marshalers), so those aren't flagged as dead (Go). It still can't see callers reached via
*custom* interface dispatch or reflection, so treat its output as *candidates*, not proof.

## Use it from an agent (MCP)

codemap is a stdio MCP server. Register `codemap serve` with your agent — most CLIs have a one-liner:

```bash
claude mcp add codemap -- codemap serve     # Claude Code (add --scope user for all projects)
codex mcp add  codemap -- codemap serve     # OpenAI Codex
copilot mcp add codemap -- codemap serve    # GitHub Copilot CLI
```

For any other MCP client, add a stdio server to its config (the key may be `mcpServers`, `mcp`, or
`context_servers`):

```json
{
  "mcpServers": {
    "codemap": { "command": "codemap", "args": ["serve"] }
  }
}
```

Once connected, an agent can call `codemap_docs` to learn the tools and workflow on its own.

Tools (37): `codemap_init`, `codemap_index`, `codemap_status`, `codemap_doctor`, `codemap_semantic`,
`codemap_callers`, `codemap_callees`, `codemap_references`, `codemap_impact`, `codemap_file_impact`,
`codemap_dependencies`, `codemap_review`, `codemap_secret_impact`, `codemap_required_keys`,
`codemap_risk`, `codemap_hotspots`, `codemap_orphans`, `codemap_read_order`, `codemap_path`,
`codemap_related_files`, `codemap_symbols`, `codemap_symbol_at`, `codemap_find`, `codemap_source`,
`codemap_context`, `codemap_context_batch`, `codemap_projects`, `codemap_docs`, `codemap_annotate`,
`codemap_annotations`, `codemap_unannotate`, `codemap_branch_status`, `codemap_branch_switch`,
`codemap_cache_save`, `codemap_cache_restore`, `codemap_cache_list`, `codemap_cache_drop`. Each takes an
optional `path` (the project directory) and returns JSON. The two an agent reaches for first:
**`codemap_context <symbol>`** bundles a symbol's definition, callers, callees, value references,
covering tests and blast radius in **one call**, and **`codemap_status`** reports index *freshness* —
a `stale` count of files changed/added/removed since indexing, so the agent knows to reindex before
trusting results. `codemap_callers` / `codemap_callees` accept `precise: true` for exact,
gopls-resolved results (Go); `codemap_source` returns a symbol's body; `codemap_projects` lists
what's indexed; **`codemap_docs`** returns an agent guide so a harness can learn the tool;
**`codemap_annotate` / `codemap_annotations`** pin notes and external data (DB rows, findings) to
symbols and call paths — a knowledge layer over the graph (see below).

For same-named definitions, pass `selector:{file,start_line,fqn,kind}` to `codemap_source`,
`codemap_context`, `codemap_callers`, `codemap_callees`, `codemap_references`, `codemap_impact`,
or `codemap_risk`.
`codemap_path` accepts `from_selector` + `to_selector`. The fields are a direct projection of the
symbol objects these tools already return, so chaining adds no database-ID contract.

`codemap_context` uses the indexed graph only: it never launches language servers implicitly, and
reports `call_graph:"unresolved"` plus a precise-index action when the graph is incomplete.
`codemap_context_batch` caps aggregate source bodies at 64 KiB while retaining signatures, docs,
and locations; `source_budget` / `source_truncations` disclose any shortening. Optional component
failures appear in `partial_errors` without discarding the usable parts of the bundle.

Results carry each symbol's **signature** (e.g. `func (s *Store) Hotspots(projectID int64, limit
int) ([]Hotspot, error)`) and **docstring**, so an agent understands what callers/callees/hits are
and what they do without a follow-up file read — and same-named symbols are easy to tell apart.

The flagship is `codemap_impact` — one call returns a symbol's definition sites, callers, the
transitive blast radius, and which tests cover those paths, replacing many file reads.

## Configuration

XDG-style, with `CODEMAP_*` environment overrides and an ecosystem fallback:

```
$XDG_CONFIG_HOME/codemap/config.yaml     # config        (~/.config/codemap/…)
$XDG_DATA_HOME/codemap/                   # graph DB, veclite, project registry
$XDG_CACHE_HOME/codemap/                  # caches
```

If `~/.codemap/` already exists it is used (back-compat with vecgrep/noted). `codemap init --local`
drops a `.codemap` marker so a repo-local `codemap.yaml` is picked up from any subdirectory; the
index stays central (set `CODEMAP_DATA` to a path inside the repo for a repo-local index). Precedence and all keys are documented in
[AGENTS.md](./AGENTS.md). Override paths with `CODEMAP_CONFIG` / `CODEMAP_DATA`.

## How it fits the ecosystem

codemap is built on [veclite](https://github.com/abdul-hamid-achik/veclite) and shares
conventions with [vecgrep](https://github.com/abdul-hamid-achik/vecgrep) (semantic code
search) and [noted](https://github.com/abdul-hamid-achik/noted) (code notes). An agent can
combine them: vecgrep/codemap to *find* code by meaning, codemap to learn *what calls it* and
*what breaks* if it changes.

## Documentation

Full docs: **[docs/](./docs)** (VitePress). Design rationale: `~/notes/projects/codemap/design-rationale.md` (Obsidian vault).

## License

[MIT](./LICENSE) © 2026 Abdul Hamid Achik
