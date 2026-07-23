# CLAUDE.md

**Source of truth: `AGENTS.md`. Read it first.** This file adds Claude-specific orientation
and the handful of things that are easy to get wrong.

## What codemap is

Local-first code intelligence: a structural code graph (Go via stdlib `go/parser`, plus
`--precise` go/types; TypeScript **+ JavaScript** symbols via one
`typescript-language-server` — enriched by `tsscan` with name-based import, JSX component-usage,
and Next.js framework-wiring edges — with precise calls resolving across the `.ts`↔`.js` boundary;
**Python** via `pyright-langserver`; **Ruby** and **Lua** via built-in pure-Go line scanners
(symbols + name-based calls + `require` imports, no server); and **Vue SFC** `<script>` blocks
routed to the same TS server
for symbols + `defines` + import edges only). Tree-sitter remains planned and has no implementation today.
The graph is fused with semantic retrieval (local veclite plus an optional one-hop vecgrep
fallback, or vecgrep as the explicit owner) and exposed as a unified query layer. Three surfaces
share the structural store: CLI (`--json` for agents), MCP server (`codemap serve`: 44 tools in
`full`, 26 in `agent`/`core`), and the `studio` TUI.

Surfaces / key files:
- CLI: `cmd/codemap/` — cobra CLI split per-command (main.go plus agent/annotate/branch/cache/
  config/coverage/daemon/gate/index/init_status/query). Each `RunE` is thin → opens a session →
  calls `internal/app`.
- Service layer (everything routes here): `internal/app/` (service_core / _init / _query /
  _relations / _impact / _context / _semantic / _annotations; plus session, review, file_impact,
  risk, readorder, service_map, service_explore, service_traverse, secret_impact, branchswitch,
  cache, doctor, docs, agentsetup, playbook,
  vecgrep_client).
- MCP server (thin, 42 full; agent/core 22): `internal/mcp/server.go`. `agent` is
  pinned exactly to the taught workflow; `core` is the separate compatible lean contract.
- studio TUI: `internal/tui/` (model.go/view.go/theme.go/run.go + anim.go [harmonica springs]
  + highlight.go [chroma syntax]; tabs graph/metrics/impact/search/path)
- Graph store + traversal: `internal/graph/`  ·  vectors (veclite wrapper): `internal/vector/`
- Extraction: `internal/extract/` (`gosrc` = go/parser · `typesrc` = go/types [`--precise`] ·
  `lspsrc` = LSP-backed [TS/JS/Python] · `tsscan` = name-based TS/JS imports/JSX/Next.js wiring ·
  `rubysrc`/`luasrc` = pure-Go Ruby/Lua scanners · `vuesrc` = Vue SFC `.vue` → TS server)
- LSP client: `internal/lsp/`  ·  indexer: `internal/index/`  ·  embeddings: `internal/embed/`
- Branch/cache/daemon: `internal/branchstate` · `internal/cachestate` · `internal/snapshot`
  (fcheap) · `internal/daemon` (watcher) · `internal/git` (branch/ref/diff for review + branch-switch)
  ·  config (XDG): `internal/config/`
- Harness distribution: `integrations/claude-code/` (Claude Code plugin — MCP registration +
  `using-codemap` skill, generated from `docs.go`/`playbook.go`) and `integrations/github-action/`
  (composite CI action + reusable workflow; GitLab mirror)

## Two documentation surfaces — do not mix them

- `docs/` → VitePress **product docs**, deployed to **Vercel** (no GitHub Pages). Public
  configuration belongs in `docs/configuration.md`; agent usage belongs in `docs/agents.md`.
- `~/notes/projects/codemap/` → Obsidian vault for **working notes / handoffs**, via the
  `obsidian-cli` skill. **Never** write scratch `.md` into the repo. Repo root `.md` is
  limited to: README, AGENTS, CLAUDE — the backlog lives in the vault
  (`~/notes/projects/codemap/BACKLOG.md`), not the repo. (Design rationale lives in the vault:
  `~/notes/projects/codemap/design-rationale.md`.)

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
- **Keep `CGO_ENABLED=0` for releases.** The shipped code is pure-Go (`modernc.org/sqlite`,
  veclite, the go-sdk). No tree-sitter backend or `treesitter` build tag exists today; if that
  planned CGO-dependent backend is implemented, keep it optional until its release matrix is
  explicitly supported.
- **Lazy-open the DB.** Don't open SQLite/veclite at startup (multiple MCP clients spawn
  multiple servers and would fight over the lock). Open on first query.
- **Detect cycles in graph traversal.** Call graphs have cycles; every BFS/DFS needs a
  visited set or it loops forever (`graph_path`, `blast_radius`).
- **veclite payload vs content**: filterable fields (`path`, `lang`, `kind`, `node_id`) go in
  Payload; the embeddable/searchable source text goes in Content (or a `WithTextIndex`
  field). `HybridSearch` needs a text index enabled.
- **Tree-sitter is planned, not present.** Do not describe it as a current backend. If it is
  introduced, use the official `github.com/tree-sitter/go-tree-sitter`, not the abandoned
  `smacker` fork, and keep the default release pure-Go.

## Validate your work

`task check` (fmt + lint + test) during development; `task check:verify` is the non-mutating
CI/release gate · `task race` for TUI/indexer ·
`task build` · `task flows` (glyphrun) when specs change. Flows are **local-only** (CI skips
them): `studio.yml` needs `gopls`, `semantic.yml` needs a local Ollama with `nomic-embed-text`,
`precise.yml` needs the `go` toolchain (it runs `index --precise`); `typescript.yml` +
`javascript.yml` (one server, mixed TS+JS, cross-language call graph) + `jsx.yml` (`<Component/>`
usages resolve as call edges in `.tsx`/`.jsx`) + `studio_ts.yml` (studio driving a TS call graph in
the Graph tab) need `typescript-language-server` + `node`; `python.yml`
needs `pyright-langserver` + `node`; and `polyglot.yml` (Go+TS+Python in one repo, the unified precise
pass across go/types + callHierarchy) needs ALL of them. The toolchain-dependent flows only isolate
`CODEMAP_DATA`, not `HOME`, so an asdf shim still resolves — the rest are pure-Go.

## Related projects

[[../veclite/index|VecLite]] · [[../vecgrep/index|vecgrep]] · [[../noted/index|noted]] ·
[[../glyphrun/index|glyphrun]] (`~/projects/*`). When in doubt about a convention, copy
**vecgrep** (closest sibling: Go CLI + config + MCP + veclite).
