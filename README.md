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

- **Structural code graph** — files, functions, types, methods, and tests as nodes; calls,
  imports, implements, references, overrides, and test-coverage as edges. Stored in pure-Go
  SQLite, queryable offline. **v0.1 indexes Go** (stdlib `go/parser`); more languages (via the
  built-in LSP backend and tree-sitter) are planned. Semantic search is language-agnostic.
- **Semantic search** — every node's source is embedded (Ollama `nomic-embed-text`, 768-dim)
  into [veclite](https://github.com/abdul-hamid-achik/veclite); vector + BM25 hybrid search.
- **Impact analysis** — `impact` returns a symbol's definition sites, direct callers, the
  transitive blast radius (everything affected by a change), and which tests cover those
  paths (flagging untested code).
- **Multi-project registry** — one shared store indexes all your repos; `projects` lists what's
  indexed, and any query targets one project (resolved from cwd, or `--path`).
- **Precise navigation (LSP)** — the fast name-based graph for everyday queries, plus
  `callers --lsp` / `callees --lsp` that use gopls `callHierarchy` for **exact** results — the
  specific resolved method, not every same-named one (e.g. `callers Close --lsp` returns the 7
  real callers instead of 50 inflated by name). Available on the CLI and MCP (`precise: true`).
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

> **Languages:** v0.1 indexes **Go**. Pointing `codemap index` at a non-Go project reports what it
> skipped; broader language support is planned.

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
| `init` / `index` / `status` | register, index (incremental; `--reindex`, `--no-embed`), show stats |
| `projects` | list all registered projects and their index sizes |
| `callers` / `callees` / `path` | call-graph navigation (`--lsp` on callers/callees for exact gopls results) |
| `symbols` | list a file's symbols (structured alternative to reading it) |
| `find` | find symbols by name (offline) |
| `source` | print a symbol's source code (the body behind its signature) |
| `impact` | blast radius + test coverage for a symbol (`--depth`) |
| `hotspots` / `orphans` | hubs / dead-code candidates (`--top`) |
| `semantic` | meaning-based search (`--top`) |
| `serve` | run the MCP server (stdio) |
| `studio` | open the interactive TUI |

All query commands accept `--json`.

## Accuracy: name-based graph vs precise (LSP)

codemap's graph is **name-based** by default — fast, offline, and language-agnostic. It resolves
calls *within* a package precisely (Go), but a cross-package method call like `x.Close()` links to
*every* method named `Close`, because resolving the receiver's type needs a type-checker. Concretely:

- `callers` / `callees` over-match same-named methods — pass `--lsp` (gopls) for **exact** results.
- `hotspots` can rank ubiquitous method names (`String`, `Error`) high with identical, inflated
  in-degrees (one per same-named definition).
- `orphans` finds call-graph dead ends; it can't see callers reached via interface dispatch or
  reflection, so treat its output as *candidates*, not proof.

This is the usual trade-off for an instant, dependency-free index. When you need exactness on Go,
reach for `--lsp`; precise, graph-wide resolution (pure-Go `go/types`) is planned.

## Use it from an agent (MCP)

codemap is a stdio MCP server. Register it with any MCP client:

**Claude Code**

```bash
claude mcp add codemap -- codemap serve
```

**Generic MCP config**

```json
{
  "mcpServers": {
    "codemap": { "command": "codemap", "args": ["serve"] }
  }
}
```

Tools (14): `codemap_init`, `codemap_index`, `codemap_status`, `codemap_semantic`,
`codemap_callers`, `codemap_callees`, `codemap_impact`, `codemap_hotspots`,
`codemap_orphans`, `codemap_path`, `codemap_symbols`, `codemap_find`, `codemap_source`,
`codemap_projects`. Each takes an optional `path` (the project directory) and
returns JSON. `codemap_callers` / `codemap_callees` accept `precise: true` for exact,
gopls-resolved results (Go); `codemap_source` returns a symbol's body so an agent can read a
definition without opening the file; `codemap_projects` lists what's indexed.

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
