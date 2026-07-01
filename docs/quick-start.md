# Quick Start

## Install

```bash
brew install abdul-hamid-achik/tap/codemap
# or build from source:
go install github.com/abdul-hamid-achik/codemap/cmd/codemap@latest
```

## Prerequisites

- **[Ollama](https://ollama.com)** with the embedding model (for semantic search):
  `ollama pull nomic-embed-text`. Structure-only indexing works without it.
- Optionally, **`gopls`** for `--lsp` precise Go results.

> Indexes **Go** (full graph), **TypeScript + JavaScript** (one `typescript-language-server`), and
> **Python** (`pyright-langserver`) — structure + semantic search always, plus a precise call graph
> under `--precise` when the relevant server is installed (`--no-lsp` to skip). **Vue SFCs** (`.vue`)
> are indexed too: each `<script>`/`<script setup>` block is routed to the same
> `typescript-language-server` (so `codemap_symbols`/`codemap_find`/`codemap_source` see the real
> declarations, with lines mapped back onto the `.vue` file). Other languages are recognized and
> reported as skipped (HTML/CSS support is planned).

## Index a project

```bash
cd ~/projects/myapp
codemap init                 # register the project
codemap index                # extract the graph + embed nodes (incremental)
codemap index --no-embed     # structure only (no Ollama needed)
codemap index --precise      # exact call edges (Go via go/types; TS/JS/Python via callHierarchy)
```

Re-running `index` is incremental — unchanged files are skipped. Use `--reindex` to rebuild.
For **TypeScript/JavaScript/Python**, `--precise` is what builds the call graph at all (there are no
name-based call edges for them), so `callers`/`callees`/`impact` need it — run it once after indexing.

## Ask questions

```bash
codemap context  authenticateUser     # everything about it in ONE call (def, callers, callees, tests)
codemap callers  authenticateUser     # who calls it
codemap impact   authenticateUser     # callers + blast radius + covering tests
codemap path     Handler Login        # shortest call path
codemap hotspots                      # the most-referenced symbols
codemap semantic "jwt validation"     # find code by meaning
codemap status                        # stats + warns if the index is stale vs your files
```

Add `--json` to any query for machine-readable output (handy for agents and scripts).

## Explore visually

```bash
codemap studio
```

See [studio](/studio) for the interactive TUI, [CLI](/cli) for the full command list, and
[MCP](/mcp) to wire codemap into an AI agent.
