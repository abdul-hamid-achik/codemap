---
description: Install codemap, index a repository, and ask your first structural and semantic code questions.
---

# Quick Start

Three minutes, one repo, real answers. This page is for anyone trying codemap for the
first time — human or agent. Each step below shows the command and what it actually prints.

## 1. Install

```bash
brew install abdul-hamid-achik/tap/codemap
# or build from source:
go install github.com/abdul-hamid-achik/codemap/cmd/codemap@latest
```

**Optional but recommended:** [Ollama](https://ollama.com) with `ollama pull nomic-embed-text`
enables semantic search — structure-only indexing works fine without it.

> **Go** works with the built-in parser. To index **TypeScript, JavaScript, or Vue**,
> install `typescript-language-server`; **Python** needs `pyright-langserver`. `--precise`
> resolves calls for Go/TypeScript/JavaScript/Python; Vue currently provides symbols and
> `defines` edges only. See [Language support](/languages) for install commands and limits.
> Semantic search is available when embeddings are enabled.

## 2. Index a project

```bash
cd ~/projects/myapp
codemap init     # register the project
codemap index    # extract the graph + attempt embeddings (incremental — safe to rerun)
```

Here's that same command run on codemap's own repo, for scale — add `--precise` for
exact Go edges and for any TypeScript/JavaScript/Python call graph:

```
Indexed "codemap" (/Users/you/projects/codemap)
  files: 631 scanned, 613 indexed, 18 up-to-date
  graph: 8744 nodes, 164077 edges (embeddings: true)
  time: 1m47s (extract 6.6s, embed 1m32s)
  tip: Go call edges are name-based; add --precise to resolve them exactly
```

## 3. Ask it something

One call replaces the usual grep-and-read chase — definition, callers, callees, and
covering tests together:

```bash
$ codemap context DeriveProjectName
Context: DeriveProjectName (codemap)
  defined internal/config/project.go:50-56  (function)
      func DeriveProjectName(dir string) string
      DeriveProjectName returns a stable, human-readable project name for dir.
  callers (11): app.Service.resolveProject, app.Service.Init, … (+9)
  blast radius: 503 (depth ≤ 3)
```

Trace how two symbols connect:

```bash
$ codemap path runInit DeriveProjectName
runInit → Init → DeriveProjectName
  runInit                        cmd/codemap/init_status.go:64
  Init                           internal/app/service_init.go:92
  DeriveProjectName              internal/config/project.go:50
  call graph: name
```

Search by meaning instead of name:

```bash
$ codemap semantic "embedding profile guard"
fusion: natural_language
  0.033  internal/embed/provider.go:16 Profile() EmbeddingProfile
  0.029  internal/app/branchswitch.go:240 func profileCompatible(snap, current string) bool
  0.028  internal/embed/provider.go:12 type Provider interface {
  …
```

Add `--json` to any query for machine-readable output; symbol results carry `file`,
`start_line`, `fqn`, and `kind` — project those into `selector` to pin follow-up queries to
that exact definition (see [MCP exact source selectors](/mcp#exact-source-selectors)).
`codemap status` warns when files have changed since the last index, so you know to
reindex before trusting a query.

## Where to go next

- **Wiring up an AI coding agent?** Skip to `codemap agent setup <harness>` — it registers
  the MCP server and drops the playbook that teaches your agent when to reach for these
  tools, in one command. See [codemap for agents](/agents#one-command-setup).
- **Exploring by hand?** Run `codemap studio` for the interactive TUI — see [studio](/studio).
- **Wiring up CI?** The [GitHub Action](/ci) posts impact + risk on every PR and can fail
  the build on untested or high-risk changes.

Full command reference: [CLI](/cli) · [MCP](/mcp).
