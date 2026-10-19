# AGENTS.md

Contributor/agent guide for **codemap**. Product usage lives in `docs/` (start at
`docs/quick-start.md`; agents: `docs/agents.md`, `docs/mcp.md`; learning layer:
`docs/learn.md`; config: `docs/configuration.md`). Design rationale lives in the
Obsidian vault at `~/notes/projects/codemap/design-rationale.md`; live work in the
vault's `BACKLOG.md` — never in the repo. The in-band agent guide is generated from
`internal/app/docs.go` (`codemap docs [topic]`); this file is contributor rules and
invariants, not a second copy of the product contracts — when a contract detail
matters, follow the pointer and read the code/schema.

## Project Overview

codemap is local-first code intelligence: a **structural code graph** (stdlib parsers +
pure-Go tree-sitter for TS/JS/Python; LSP for `--precise`) plus **semantic retrieval**
(local veclite, or delegated one-hop to vecgrep) behind one query layer. It answers
questions grep/LSP-alone can't: *"who calls this and what tests cover it"*, *"blast
radius of changing this type"*, *"find auth-like code, then show what calls it"*.

Three surfaces over one store:
- **CLI** — human commands + `--json` machine output (Cobra, `cmd/codemap/`).
- **MCP server** — `codemap serve` (stdio), `codemap_*` tools (`internal/mcp/`).
- **Codemap Studio** — Electron app in `desktop/`; never reimplements anything, every
  panel spawns `codemap … --json` (see `docs/desktop.md`). The terminal Studio TUI was
  retired.

Highlights: learning layer (`atlas`/`features`/`flow`/`processes` — summaries are read
from READMEs/package docs/leading comments, never generated), hybrid queries
(`context`, `impact`, `review`, `affected`), cross-project graph, hash-incremental
reindex with an embedding-profile guard, offline structural queries (one-off precise
relations may launch a language server on demand).

## Directory Structure

```
cmd/codemap/        # Cobra CLI split by domain; every RunE is THIN → session → internal/app.
                    #   Files carry /* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */.
internal/
  app/              # shared service layer (CLI + MCP call this; desktop via CLI)
  graph/            # SQLite graph store: store/schema/queries/topology/ranking/traversal
  extract/          # backends: gosrc, typesrc (go/types), sittersrc (tree-sitter),
                    #   lspsrc, tsscan, rubysrc, luasrc, vuesrc, featuresrc, declarative
  lsp/              # hand-rolled LSP client (Content-Length JSON-RPC)
  embed/            # embed.Provider + Ollama provider + EmbeddingProfile guard
  vector/           # veclite wrapper (one `codemap` collection)
  index/            # walk → extract → embed → store; incremental, watcher, import edges
  mcp/              # stdio MCP server — thin pass-through to app
  daemon/           # fsnotify watcher + control socket
  git/ snapshot/ branchstate/ cachestate/ config/ version/
docs/               # VitePress product site ONLY (deployed via Vercel from main)
desktop/            # Codemap Studio (Electron); see "Desktop" under Common Tasks
integrations/       # claude-code plugin, portable agent skill, github-action (generated
                    #   playbook artifacts, pinned by sync tests — not notes)
specs/              # glyphrun E2E specs (*.yml, local only)
schemas/            # versioned JSON schemas for machine contracts (e.g. review v1)
```

**Package boundaries are part of the contract.** Dependency direction is one-way:
`cmd → app → {graph, vector, index, extract, embed, lsp, config, daemon, git, snapshot,
branchstate, cachestate}`. MCP handlers and CLI RunE handlers are thin; the desktop app
only spawns the CLI. Never put business logic in `internal/mcp` or `desktop/`.

## Documentation Discipline

- `docs/` is the public VitePress site **only** (Vercel, `main` only, no GitHub Pages,
  no preview deploys, never `vercel promote`). Public usage/config goes in product
  pages, never only here.
- Repo root carries exactly `README.md` + `AGENTS.md`. No scratch/handoff/TODO/design
  `.md` anywhere in the repo — notes, investigations, backlog → `~/notes/projects/codemap/`
  (checked-in generated artifacts under `integrations/` are the documented exception).
- **Keep this file small** (harness rules budgets truncate around 64 KiB). New contract
  detail goes to code + `docs/` + schemas, with a one-line pointer here.

## Development Commands

```
task build         # → ./bin/codemap        task test        # go test ./...
task race          # CGO race tests         task lint:strict # pinned golangci-lint
task check:verify  # CI/release gate (non-mutating)   task flows # glyphrun specs (local)
task ship          # check:verify + docs + race + build + flows
```
Full list: `task`. Prerequisites: Go 1.25+; Ollama + `nomic-embed-text` for local
embeddings (tests skip if unreachable); Task, glyph, Bun (docs), golangci-lint.
`typescript-language-server`/`pyright-langserver` are optional — only `--precise`
TS/JS/Python (and the parity harness) needs them; `gopls` powers one-off precise Go
relations, graph-wide Go `--precise` is in-process `go/types`. Release builds pin
tree-sitter grammar `-tags` (Taskfile, mirrored in `.goreleaser.yaml` and
`desktop/scripts/stage-binary.mjs`).

## Architecture Invariants

### Extraction
- Backends (`internal/extract/`): `gosrc` (go/parser), `rubysrc`, `luasrc` always on;
  `sittersrc` is the **default TS/JS/Python backend** — pure-Go tree-sitter
  (odvcencio/gotreesitter, pinned) emulating the servers' documentSymbol trees and
  feeding the same `lspsrc.FromDocumentSymbols` normalization. Quirks are replicated
  on purpose (an arrow `const` is a variable); changing graph semantics is a separate
  decision. `index.structural_backend: lsp` selects the `lspsrc` backend instead.
  Language servers are spawned **once per server spec**, only when a served language
  is present (one tsserver serves TS+JS via `Bind`); in tree-sitter mode only for
  `--precise`, attached as `sittersrc.Hybrid`. Vue SFC script blocks delegate to the
  TS/JS extractor; symbols+`defines`+imports, no precise call graph yet.
- Every LSP request is bounded (30s default in `internal/lsp/jsonrpc.go`); a stalled
  server skips the file, never hangs the index. Missing-server languages surface as
  actionable `MissingServers`/`ServerIssues`; unsupported = `Result.Unsupported`.
  `--no-lsp` never spawns a server.
- Default builds are pure-Go (`CGO_ENABLED=0`): never add CGO tree-sitter bindings.
  New tree-sitter languages must emulate an authoritative symbol source and ship a
  parity harness + goldens (see Gotchas). Edge weights: LSP 1.0, parser/heuristic 0.7.
- Precise `--precise` supersedes name-based candidates only on a complete, non-empty
  precise answer; an empty precise answer never erases name-based candidates.
- Declarative formats (SQL/YAML/Markdown/HTML/CSS): `call_graph: none`; consumers use
  `dependencies`/`traverse`. Details: `docs/data-and-docs.md`.

### Storage, vectors, embeddings
- Graph: `modernc.org/sqlite`, WAL, transaction-batched writes, `SetMaxOpenConns(1)`.
  `edges.provenance` records name vs precise; `call_graph_coverage` records per-file
  precise success (changed files lose coverage until re-resolved). The contentless
  FTS5 `nodes_fts` is reconciled by `Store.SyncLexical` in one pass after each index
  run — never on the per-row write path.
- Vectors: veclite, one shared `codemap` collection. Filterable fields go in
  **Payload**, searchable source text in **Content**. **Lazy open**: never open the
  graph DB or veclite at process startup — open on first use.
- Embeddings: `embed.Provider`; only Ollama (`/api/embed`) is implemented — never
  advertise OpenAI/Cohere/Voyage. A remote `ollama_url` is explicit opt-in and
  receives source text. **EmbeddingProfile guard**: provider/model/dimensions/distance
  mismatch fails the index with rebuild guidance; never mix embedding spaces.
- **Opening veclite loads every project's records into RAM** (it is an in-memory store;
  `Sync` re-serializes the whole collection). Structure-only paths must therefore not
  open it "just to check": gate on graph-side evidence (`graph.ProjectHasVecIDs` /
  `ProjectVecIDCount`) and skip `Sync` when nothing was deleted/inserted. A cleared
  `vec_id` is `''`, never NULL. `semantic.backend: vecgrep` ⇒ no local vector writes/opens.

### MCP server and machine contracts
- go-sdk MCP (`internal/mcp/server.go`), typed input structs (`json:",omitempty"` +
  `jsonschema` tags; no `omitempty` = required). **stdio transport is newline-delimited
  JSON-RPC — never Content-Length** (that's LSP framing; wrapping the SDK transport
  breaks clients). Handlers stay thin over `internal/app`.
- Tool inventory, profiles (`agent`/`core` 28, `full` 50), per-tool semantics, CLI
  flags, schemas: `docs/mcp.md`, `codemap --help`, `schemas/`. Profile gating is
  registration-time only (`Server.include`); `TestAgentProfileExactlyMatchesTaughtWorkflow`
  pins the `agent` set to what `RenderPlaybook` teaches.
- Exact-definition inputs use durable selectors `{file,start_line,fqn,kind}`, never
  SQLite node ids; resolution prefers file+FQN+kind (line shifts survive reindex).
- **Accuracy model** (details: `codemap docs accuracy`): name-based by default —
  same-name fan-out, reflection-blind; `references` edges (value wiring) never enter
  the call graph. `--precise` is the graph-wide fix (Go `go/types`; TS/JS/Python
  `callHierarchy`), with per-file coverage. Reports carry the `call_graph` enum
  (`resolved|name|unresolved|none`) — consumers switch on it; keep it honest
  (unresolved/none is never evidence of risk; gates fail closed only on incomplete
  analysis). Optional fields are additive within a schema major; renames/removals/
  enum narrowing require a new major + dual-read window. Regenerate the review golden
  with `go test ./internal/app -run TestReviewContractV1 -update-review-contract`.
- CLI exit codes: 0 answered · 1 operational · 2 not found · 3 `index_missing` ·
  4 `index_corrupt` · 5 `not_a_repo` · 6 `gate_failed` (normal output first, never an
  error envelope). `--json` failures print `{ok:false,error,code,hint}` to **stdout**
  (`internal/app/errors.go`, `cmd/codemap/gate.go`).

### Config
Precedence: CLI flags > `CODEMAP_*` env > project `codemap.yaml` > project
`.config/codemap.yaml` > `$XDG_CONFIG_HOME/codemap/config.yaml` > `~/.codemap/config.yaml`
> `DefaultConfig()`. Full table (file/env/flag reachability per setting):
`docs/configuration.md`. Data lives under `$XDG_DATA_HOME/codemap` (legacy `~/.codemap`
honored); `codemap init --local` only adds a discovery marker — data stays central
unless `CODEMAP_DATA` says otherwise. YAML via `gopkg.in/yaml.v3`.

## Common Tasks

**Add a CLI command:** cobra var + `init()` registration + thin `runX` in the matching
`cmd/codemap/<domain>.go`; opens a session → `internal/app`; support `--json`; add a
desktop registry row.

**Add an MCP tool:** typed input struct + `mcp.AddTool` in `internal/mcp/server.go`,
delegate to `internal/app`, update README/`docs/mcp.md`, decide profile membership
(the sync test above will tell you).

**Change the agent playbook / add a harness:** ONE source — `internal/app/docs.go`
(`docTopics`) + `playbookPreamble` in `internal/app/playbook.go`, via `RenderPlaybook`;
never hand-type guidance per harness. Regenerate pinned skills after preamble/topic
changes: `go run ./cmd/codemap agent playbook --format claude-skill > integrations/claude-code/skills/using-codemap/SKILL.md`
and `--format skill > integrations/agent-skills/using-codemap/SKILL.md`. Harness
registries: `internal/app/agentsetup.go`, `skillHarnesses` in `agentskill.go`.

**Add a graph query:** traversal in `internal/graph/queries.go` (BFS + visited set —
**always detect cycles**) → `internal/app` service method → CLI + MCP + desktop panel.

**Data-model change:** migration in `internal/graph/schema.go`, bump schema version,
never break an existing index without a rebuild path.

**Desktop panel/view:** registry row in `desktop/renderer/js/features.mjs` (audited
against `codemap --help`), renderer in `report.mjs`, trimmed real fixture in
`desktop/test/fixtures/`; dependency-free ES modules, theme tokens from
`styles/theme.css`. Verify: `npm test`, `npm run test:live`, `npm run smoke`.

## Code Style, Testing, Before Committing

- `gofmt` + golangci-lint (errcheck + staticcheck). Error messages lowercase, no
  trailing punctuation; `os.Exit(1)` in `main` only; small testable functions.
- Tests: unit coverage on graph/extract/config; Ollama tests skip when unreachable;
  `task race` for indexer/daemon concurrency. glyphrun specs declare intent+outcomes
  (contract) and steps (hints); stamp `contractHash` via
  `glyph spec verify specs/x.yml --stamp` after intentional contract changes.
- Before committing: `task check:verify` (+ `task flows` if specs changed). Commit/push
  only when the user asks.

## Gotchas

- **Lazy-open + veclite cost**: see Storage above. A "cheap" existence check that opens
  the shared veclite store once cost every structure-only index multiple GB of RSS.
- **Graph traversal cycles**: every BFS/DFS needs a visited set.
- **gotreesitter is pinned and young**: bump deliberately; re-run goldens + parity
  (`CODEMAP_PARITY_ROOT=… go test ./internal/extract/sittersrc -run Parity`) on real
  repos before merging. Known misparses are normalized in sittersrc — add a golden
  for any new one.
- **Flows are local-only** (CI skips them) and toolchain-gated (Ollama, go, language
  servers per spec). They isolate `CODEMAP_DATA`, not `HOME`, so asdf shims resolve.
- **Node text is never updated in place** (deletions are anti-joins) — `SyncLexical`
  and incremental logic rely on it.

## Related

Siblings under `~/projects`: veclite (vector store), vecgrep (semantic owner; codemap
mirrors its CLI/config/MCP conventions), noted, glyphrun. Design rationale:
`~/notes/projects/codemap/design-rationale.md`.

