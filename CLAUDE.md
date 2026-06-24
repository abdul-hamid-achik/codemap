# CLAUDE.md

**Source of truth: `AGENTS.md`. Read it first.** This file adds Claude-specific orientation
and the handful of things that are easy to get wrong.

## What codemap is

Local-first code intelligence: a structural code graph (Go via stdlib `go/parser`, plus
`--precise` go/types; TypeScript **+ JavaScript** via one `typescript-language-server` — calls
resolving across the `.ts`↔`.js` boundary — and **Python** via `pyright-langserver`;
tree-sitter still planned) fused with semantic vectors (veclite), exposed as a unified query
layer. Three surfaces over one store: CLI (`--json` for agents), MCP server (`codemap serve`),
and the `studio` TUI.

Surfaces / key files:
- CLI + all commands: `cmd/codemap/main.go`
- Service layer (everything routes here): `internal/app/` (service.go, session.go)
- MCP server (thin, 20 tools): `internal/mcp/server.go`
- studio TUI: `internal/tui/` (model.go/view.go; tabs graph/metrics/impact/search)
- Graph store + traversal: `internal/graph/`  ·  vectors (veclite wrapper): `internal/vector/`
- Extraction: `internal/extract/` (`gosrc` = go/parser, `lspsrc` = LSP-backed)
- LSP client: `internal/lsp/`  ·  indexer: `internal/index/`  ·  embeddings: `internal/embed/`
  ·  config (XDG): `internal/config/`

## Two documentation surfaces — do not mix them

- `docs/` → VitePress **product docs**, deployed to **Vercel** (no GitHub Pages).
- `~/notes/projects/codemap/` → Obsidian vault for **working notes / handoffs**, via the
  `obsidian-cli` skill. **Never** write scratch `.md` into the repo. Repo root `.md` is
  limited to: README, AGENTS, CLAUDE, BACKLOG, SPEC.

## Gotchas (learned the hard way)

- **MCP stdio framing must be newline-delimited JSON-RPC, NOT Content-Length.** Use the
  go-sdk `StdioTransport` as-is. Content-Length makes Claude Code's health check report
  "Failed to connect" (this exact bug hit `glyph`). codemap also speaks **LSP, which DOES
  use Content-Length** — keep the two transports strictly separate; never let LSP framing
  leak into the MCP server.
- **Charm v2 lives on vanity module paths**: import `charm.land/bubbletea/v2`,
  `charm.land/lipgloss/v2`, `charm.land/bubbles/v2`, `charm.land/glamour/v2` — **not**
  `github.com/charmbracelet/...`. The github `/v2` tags resolve on the proxy but fail the
  module-path check because `go.mod` declares `charm.land` as canonical.
- **(Future) ntcharts** — Metrics currently uses hand-rolled ASCII bars (no chart dep). If you
  add `github.com/NimbleMarkets/ntcharts/v2`, note its `go.mod` `replace`s bubbletea to a fork
  ("awaiting upstream merges"); that replace is ignored by our module, so it may not build
  against stock `charm.land/bubbletea/v2`. Mirror the replace or pin bubbletea to what ntcharts
  wants, and `go build ./...` early.
- **Keep `CGO_ENABLED=0` for releases.** Everything is pure-Go (`modernc.org/sqlite`,
  veclite, the go-sdk). tree-sitter is the *only* thing that needs CGO — it stays behind the
  `treesitter` build tag and out of release binaries until 0.2.
- **Lazy-open the DB.** Don't open SQLite/veclite at startup (multiple MCP clients spawn
  multiple servers and would fight over the lock). Open on first query.
- **Detect cycles in graph traversal.** Call graphs have cycles; every BFS/DFS needs a
  visited set or it loops forever (`graph_path`, `blast_radius`).
- **veclite payload vs content**: filterable fields (`path`, `lang`, `kind`, `node_id`) go in
  Payload; the embeddable/searchable source text goes in Content (or a `WithTextIndex`
  field). `HybridSearch` needs a text index enabled.
- **No tree-sitter exists in the ecosystem** — codemap introduces it. Use the official
  `github.com/tree-sitter/go-tree-sitter`, not the abandoned `smacker` fork.

## Validate your work

`task check` (fmt + lint + test) before every commit · `task race` for TUI/indexer ·
`task build` · `task flows` (glyphrun) when specs change. Flows are **local-only** (CI skips
them): `studio.yml` needs `gopls`, `semantic.yml` needs a local Ollama with `nomic-embed-text`,
`precise.yml` needs the `go` toolchain (it runs `index --precise`); `typescript.yml` +
`javascript.yml` (one server, mixed TS+JS, cross-language call graph) + `studio_ts.yml` (studio
driving a TS call graph in the Graph tab) need `typescript-language-server` + `node`; and `python.yml`
needs `pyright-langserver` + `node`. The toolchain-dependent flows only isolate `CODEMAP_DATA`, not
`HOME`, so an asdf shim still resolves — the rest are pure-Go.

## Related projects

[[../veclite/index|VecLite]] · [[../vecgrep/index|vecgrep]] · [[../noted/index|noted]] ·
[[../glyphrun/index|glyphrun]] (`~/projects/*`). When in doubt about a convention, copy
**vecgrep** (closest sibling: Go CLI + config + MCP + veclite).
