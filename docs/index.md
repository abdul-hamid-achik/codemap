---
layout: home
hero:
  name: codemap
  text: Code intelligence for agents and people
  tagline: A local-first code graph + semantic search — exposed through a CLI, an MCP server, and a terminal UI.
  actions:
    - theme: brand
      text: Quick Start
      link: /quick-start
    - theme: alt
      text: View on GitHub
      link: https://github.com/abdul-hamid-achik/codemap
features:
  - title: Structural code graph
    details: Files, functions, types, methods, and tests as nodes; calls, imports, and test coverage as edges. Pure-Go SQLite, queryable offline.
  - title: Semantic search
    details: Every node's source is embedded (Ollama nomic-embed-text) into veclite, so you can find code by meaning.
  - title: Impact analysis
    details: One query returns a symbol's callers, the transitive blast radius, and which tests cover those paths.
  - title: Three surfaces, one store
    details: A JSON-capable CLI, a stdio MCP server for agents, and the full-screen studio TUI for humans.
---

## What is codemap?

codemap answers questions that grep and a single LSP call can't: *who calls this
function and which tests cover it*, *what's the blast radius of changing this type*,
*find auth-like code by meaning*. It precomputes the structure once, then serves narrow,
structured answers — so an agent spends a few tool calls instead of dozens of file reads.

It's a single pure-Go binary, runs entirely on your machine, and installs via Homebrew:

```bash
brew install abdul-hamid-achik/tap/codemap
```
