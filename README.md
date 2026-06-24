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
  queryable offline. Indexes **Go** (stdlib `go/parser`, full call graph) and **TypeScript** (via
  `typescript-language-server` when it's on `PATH` — symbols + structure always, plus a **precise
  call graph** under `--precise`, so `callers`/`impact`/`hotspots`/`path` work for TS too);
  JavaScript/Python and structure-only markup (Vue/HTML/CSS/Docker) are next. Semantic search is
  language-agnostic.
- **Semantic search** — every node's source is embedded (Ollama `nomic-embed-text`, 768-dim)
  into [veclite](https://github.com/abdul-hamid-achik/veclite); vector + BM25 hybrid search.
- **Impact analysis** — `impact` returns a symbol's definition sites, direct callers, the
  transitive blast radius (everything affected by a change), and which tests cover those
  paths (flagging untested code).
- **Multi-project registry** — one shared store indexes all your repos; `projects` lists what's
  indexed, and any query targets one project (resolved from cwd, or `--path`).
- **Annotations** — pin notes and external data (DB rows from mongosh/postgres, vidtrace/vecgrep
  findings, …) to a symbol or a call path; they persist across reindex. A knowledge layer over the
  graph for agent harnesses (`annotate` / `annotations`, also on MCP).
- **Precise call resolution** — the fast name-based graph over-matches same-named methods
  (`x.Close()` links to *every* `Close`). One `codemap index --precise` resolves each call to the one
  it actually invokes and makes **every** query — callers, callees, impact, hotspots, path — exact at
  once, no per-query flag (e.g. a `Close` method that name-matching credited with 71 callers shows only
  its real ones). It's the unified exact-resolution pass across languages: an in-process, pure-Go
  `go/types` pass for **Go**, and `typescript-language-server` `callHierarchy` for **TypeScript** (which
  has no name-based call edges, so `--precise` is what gives TS a call graph at all). Opt-in and
  additive (name-based stays the default for Go); the Go pass degrades to name-based **with a note**
  when the `go` toolchain or module isn't available. For a one-off exact answer without reindexing,
  `callers`/`callees` also take `--lsp` (gopls `callHierarchy`). CLI + MCP.
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
Tea / Lip Gloss / Bubbles). Switch tabs with `1`–`4` or `tab`; navigate with `↑`/`↓`.

```
 codemap studio                       codemap · 509 nodes · 1849 edges · 35 files
  1 Graph   2 Metrics   3 Impact   4 Search
 Hubs                                 │ lspsrc.Extractor.Close
    57  lspsrc.Extractor.Close        │  Called by (57)
    56  app.Session.Close             │   ▸ main.runInit    cmd/codemap/main.go:186
    56  graph.Store.Close             │     main.runIndex   cmd/codemap/main.go:209
    26  app.NewService                │  Calls (9)
    19  app.Open                      │     app.Session.Close  internal/app/session.go:80
                                      │  ⟩ func runInit(cmd *cobra.Command, ...) error
 ↑/↓ hub · → walk callers/calls · enter → impact · p precise · ctrl+c quit
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

## Installation

### Homebrew (recommended)

```bash
brew install abdul-hamid-achik/tap/codemap
```

### Prerequisites

- **Go 1.25+** (only to build from source)
- **[Ollama](https://ollama.com)** with the embedding model:
  `ollama pull nomic-embed-text` (optional — without it, indexing is structure-only)
- **[gopls](https://pkg.go.dev/golang.org/x/tools/gopls)** — optional, for `--lsp` precise Go results
- Optional: **[Task](https://taskfile.dev)** for the dev workflow

> **Languages:** **Go** (full graph) and **TypeScript** (structure + semantic search always, plus a
> precise call graph under `--precise`, via `typescript-language-server` — auto-enabled when it's
> installed; install it to index `.ts`/`.tsx`, or use `--no-lsp` to skip). Other recognized languages
> (JS/Python/…) are reported as skipped; broader support is in progress.

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
codemap index --precise            # exact call edges (Go via go/types; TypeScript via callHierarchy)

# 2. Navigate the call graph
codemap callers authenticateUser   # who calls it (fast, name-based)
codemap callers authenticateUser --lsp   # exact callers via gopls (Go)
codemap callees authenticateUser   # what it calls
codemap path     Handler Login     # shortest call path between two symbols

# 3. Analyze impact and structure
codemap impact   authenticateUser --depth 3   # callers + blast radius + tests
codemap hotspots --top 20          # most-referenced symbols (hubs)
codemap orphans                    # functions with no callers (dead-code candidates)

# 4. Search by meaning (needs an embedded index)
codemap semantic "jwt validation middleware" --top 10

# 5. Explore visually
codemap studio
```

Add `--json` to any query command for machine-readable output (for agents/scripts).

The flagship `impact` answers *what breaks if I change this, and what do I run to check?* in one call
(real output, from codemap on itself):

```
$ codemap impact Stats --depth 2
Impact of Stats (codemap)
  defined:        internal/graph/store.go:291
  direct callers: 4
  blast radius:   18 (depth ≤ 2)
  tests covering: 9
  covering tests (run these):
     graph.TestStats            internal/graph/graph_test.go:387
     app.TestServiceLifecycle   internal/app/app_test.go:29
     index.TestIndexProject     internal/index/indexer_test.go:87
     … (6 more)
  affected (blast radius):
     [1] app.Service.Status          internal/app/service.go:165
     [1] index.Indexer.IndexProject  internal/index/indexer.go:89
   ✓ [1] graph.TestStats             internal/graph/graph_test.go:387
     [2] main.runStatus              cmd/codemap/main.go:252
     … (14 more)
```

## Commands

| Command | What it does |
|---|---|
| `init` / `index` / `status` | register, index (incremental; `--reindex`, `--no-embed`, `--precise`), show stats |
| `projects` | list all registered projects and their index sizes |
| `callers` / `callees` / `path` | call-graph navigation (`--lsp` on callers/callees for exact gopls results) |
| `symbols` | list a file's symbols (structured alternative to reading it) |
| `find` | find symbols by name (offline) |
| `source` | print a symbol's source code (the body behind its signature) |
| `docs` | print the agent guide to codemap (`docs [topic]`) |
| `annotate` / `annotations` | pin / list notes + external data on a symbol or call path |
| `impact` | blast radius + test coverage for a symbol (`--depth`) |
| `hotspots` / `orphans` | hubs / dead-code candidates (`--top`) |
| `semantic` | meaning-based search (`--top`) |
| `serve` | run the MCP server (stdio) |
| `studio` | open the interactive TUI |

All query commands accept `--json`.

## Accuracy: name-based vs precise

codemap's graph is **name-based by default** — instant, offline, and tolerant of broken code. It
resolves calls *within* a package precisely (Go), but a cross-package method call like `x.Close()`
links to *every* method named `Close`, because resolving the receiver's type needs a type-checker.
codemap is honest about this rather than hiding it: `callers`/`callees`/`impact` flag when a name
resolves to multiple definitions, and `hotspots` marks name-collision inflation.

**For an exact graph, reindex with `codemap index --precise`** (Go). It runs an in-process, pure-Go
`go/types` pass that resolves every call to the one method it actually invokes and **replaces** the
name-based call edges — so *every* query (`callers`, `callees`, `impact`, `hotspots`, `path`) becomes
precise at once, with no per-query flag. On the codemap repo itself this collapses the `Close`/`Error`
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
`index --precise` drives `typescript-language-server` `callHierarchy` to resolve them exactly, and the
same `callers`/`callees`/`impact`/`hotspots`/`path` queries then work for TS with no flag of their own.
Needs `typescript-language-server` on `PATH`.

For a one-off exact answer *without* reindexing, `callers`/`callees` also accept `--lsp` (gopls
`callHierarchy`), which likewise degrades to name-based with a note when gopls can't resolve.

`orphans` finds call-graph dead ends; it can't see callers reached via interface dispatch or
reflection, so treat its output as *candidates*, not proof.

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

Tools (17): `codemap_init`, `codemap_index`, `codemap_status`, `codemap_semantic`,
`codemap_callers`, `codemap_callees`, `codemap_impact`, `codemap_hotspots`,
`codemap_orphans`, `codemap_path`, `codemap_symbols`, `codemap_find`, `codemap_source`,
`codemap_projects`, `codemap_docs`, `codemap_annotate`, `codemap_annotations`. Each takes an
optional `path` (the project directory) and returns JSON. `codemap_callers` / `codemap_callees`
accept `precise: true` for exact, gopls-resolved results (Go); `codemap_source` returns a symbol's
body; `codemap_projects` lists what's indexed; **`codemap_docs`** returns an agent guide so a
harness can learn the tool; **`codemap_annotate` / `codemap_annotations`** pin notes and external
data (DB rows, findings) to symbols and call paths — a knowledge layer over the graph (see below).

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

If `~/.codemap/` already exists it is used (back-compat with vecgrep/noted). Use
`codemap init --local` to keep repo-local state. Precedence and all keys are documented in
[AGENTS.md](./AGENTS.md). Override paths with `CODEMAP_CONFIG` / `CODEMAP_DATA`.

## How it fits the ecosystem

codemap is built on [veclite](https://github.com/abdul-hamid-achik/veclite) and shares
conventions with [vecgrep](https://github.com/abdul-hamid-achik/vecgrep) (semantic code
search) and [noted](https://github.com/abdul-hamid-achik/noted) (code notes). An agent can
combine them: vecgrep/codemap to *find* code by meaning, codemap to learn *what calls it* and
*what breaks* if it changes.

## Documentation

Full docs: **[docs/](./docs)** (VitePress). Design rationale: **[SPEC.md](./SPEC.md)**.

## License

[MIT](./LICENSE) © 2026 Abdul Hamid Achik
