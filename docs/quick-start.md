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
- Optionally, language servers for LSP-based extraction (`gopls`, …).

## Index a project

```bash
cd ~/projects/myapp
codemap init                 # register the project
codemap index                # extract the graph + embed nodes (incremental)
codemap index --no-embed     # structure only (no Ollama needed)
```

Re-running `index` is incremental — unchanged files are skipped. Use `--reindex` to rebuild.

## Ask questions

```bash
codemap callers  authenticateUser     # who calls it
codemap impact   authenticateUser     # callers + blast radius + covering tests
codemap path     Handler Login        # shortest call path
codemap hotspots                      # the most-referenced symbols
codemap semantic "jwt validation"     # find code by meaning
```

Add `--json` to any query for machine-readable output (handy for agents and scripts).

## Explore visually

```bash
codemap studio
```

See [studio](/studio) for the interactive TUI, [CLI](/cli) for the full command list, and
[MCP](/mcp) to wire codemap into an AI agent.
