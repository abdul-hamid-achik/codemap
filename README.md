# codemap

Local-first code intelligence that gives AI agents and people **structural awareness** of
codebases — combining a code graph (LSP + parsers) with semantic vector search (veclite),
exposed through a CLI, an MCP server, and an interactive terminal UI.

> Working on the code? Read [AGENTS.md](./AGENTS.md) first — it is the source of truth for
> conventions, architecture, and gotchas.

codemap answers questions that grep and a single LSP call can't: *who calls this function and
which tests cover it*, *what's the blast radius of changing this type across all my projects*,
*find auth-like code and then show me everything that calls into it*. It precomputes the
structure once, then serves narrow, structured answers — so an agent spends a few tool calls
instead of dozens of file reads.

## Features

- **Structural code graph** — files, functions, types, methods, and tests as nodes; calls,
  imports, implements, references, overrides, and test-coverage as edges. Stored in pure-Go
  SQLite, queryable offline.
- **Semantic search** — every node's source is embedded (Ollama `nomic-embed-text`, 768-dim)
  into [veclite](https://github.com/abdul-hamid-achik/veclite); vector + BM25 hybrid search.
- **Hybrid queries** — `impact` (blast radius + test coverage + untested paths),
  `semantic_callers` (semantic match, then expand up the call graph), `refactor_plan`.
- **Cross-project** — the graph spans every registered project, not just one repo.
- **Precise + broad** — headless **LSP** (gopls, ts_ls, …) for precision; stdlib parsers and
  (optional) tree-sitter for coverage. LSP edges outrank heuristic edges.
- **Incremental** — hash-based reindex; an embedding-profile guard forces a rebuild when the
  provider/model/dimension changes instead of corrupting the vector space.
- **Three surfaces, one store** — a Cobra **CLI** (with `--json` for agents), a stdio **MCP
  server**, and the **studio** TUI for humans.
- **Graph analytics** — hotspots (hubs), orphans (dead-code candidates), shortest call path,
  circular-dependency detection.
- **Local-first & private** — everything runs on your machine; no cloud, no uploads.
- **Single binary** — pure-Go, `CGO_ENABLED=0`, cross-compiled and shipped via Homebrew.

## studio (TUI)

`codemap studio` opens an interactive map of your code (Charm v2 — Bubble Tea / Lip Gloss /
Bubbles, charts via ntcharts):

```
 codemap studio ── myproject
 [1] Graph   [2] Metrics   [3] Impact   [4] Search
 ────────────────────────────────────────────────────
   authSvc ──▶ login ──▶ handler        Hotspots
       │  ╲                                auth  ███████ 42
       ▼   ╲──▶ guard                      db    █████   31
   authTest                               http  ████    24
```

- **Graph** — node-link call/dependency map.
- **Metrics** — hotspots, blast-radius sizes, test-coverage gaps, language breakdown.
- **Impact** — pick a symbol, see callers, tests, blast radius, and similar code.
- **Search** — semantic + structural search with filters.

## Installation

### Homebrew (recommended)

```bash
brew install abdul-hamid-achik/tap/codemap
```

### Prerequisites

- **Go 1.25+** (only to build from source)
- **[Ollama](https://ollama.com)** with the embedding model:
  `ollama pull nomic-embed-text`
- LSP servers for the languages you index (`gopls`, `typescript-language-server`, …)
- Optional: **[Task](https://taskfile.dev)** for the dev workflow

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
codemap init                     # registers the current directory
codemap index                    # extract graph + embed nodes (incremental)

# 2. Ask structural questions
codemap callers authenticateUser
codemap impact  authenticateUser --depth 3
codemap blast-radius UserService --depth 3

# 3. Ask semantic questions
codemap semantic "jwt validation middleware"
codemap search   "error handling without checking" --kind function --json

# 4. Explore visually
codemap studio
```

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

Tools (initial set): `codemap_init`, `codemap_index`, `codemap_status`, `codemap_symbols`,
`codemap_callers`, `codemap_callees`, `codemap_references`, `codemap_blast_radius`,
`codemap_test_coverage`, `codemap_path`, `codemap_semantic`, `codemap_similar`,
`codemap_impact`, `codemap_search`, `codemap_dependencies`.

The flagship is `codemap_impact` — one call returns a symbol's callers, the tests that cover
them, the untested paths, and semantically similar code, replacing 10–15 file reads.

## Configuration

XDG-style, with `CODEMAP_*` environment overrides and an ecosystem fallback:

```
$XDG_CONFIG_HOME/codemap/config.yaml     # config        (~/.config/codemap/…)
$XDG_DATA_HOME/codemap/                   # graph DB, veclite, project registry
$XDG_CACHE_HOME/codemap/                  # caches
```

If `~/.codemap/` already exists it is used (back-compat with vecgrep/noted). Use
`codemap init --local` to keep repo-local state. Precedence and all keys are documented in
[AGENTS.md](./AGENTS.md) and via `codemap config show`.

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
