# codemap — Backlog

> Source of truth for the autonomous build loop. Each iteration: read this file, pick the
> next unstarted task, do it, update status here. Convert relative dates to absolute.
> Started 2026-06-23. Cron `ffee7a2b` (every 5 min). See AGENTS.md / SPEC.md for design.

## Status legend
`[ ]` todo · `[~]` in progress · `[x]` done · `[!]` blocked/needs decision

## Iteration log
- 2026-06-23 #1 — Read SPEC.md. Set up 5-min cron. Launched research workflow
  `wf_2a7fe834-890`. Resolved D1/D2/D3 with user.
- 2026-06-23 #2 (cron) — Research still running. Added LICENSE (MIT) + .gitignore.
- 2026-06-23 #3 (cron→research done) — Research completed (6 agents, vault + ecosystem +
  live versions). Wrote AGENTS.md, CLAUDE.md, README.md; rewrote this BACKLOG with verified
  versions, tech decisions (TD1–TD8), resolved vault open-questions, risks. E0.1–E0.4 done.
  Then scaffolded the buildable foundation: `go mod init` + cobra v1.10.2, internal/version,
  cmd/codemap/main.go (full CLI surface, handlers stubbed → "not implemented"), Taskfile.yml.
  Verified: `CGO_ENABLED=0 go build` OK; `codemap version`/`--help`/stubs run. E1.1, E1.2 done.
  **NEXT:** docs/ VitePress scaffold (E0.5), then config package (E1.9), then graph store (E1.3).
- 2026-06-23 #4 (cron) — Built internal/config (XDG paths + legacy fallback + YAML resolution
  chain + CODEMAP_* env + project-root finding; 14 tests, all pass) → E1.9 done. Then built
  internal/graph (modernc sqlite v1.53.0, schema+migrations via user_version, WAL/FK/busy via
  DSN pragmas, projects/nodes/edges/index_state CRUD, FK cascade, stats; 11 tests, all pass)
  → E1.3 done. `go test ./...` green; `CGO_ENABLED=0 go build ./...` + `go vet` clean.
  **NEXT:** go/parser extractor (E1.4) → embed provider + veclite (E1.5/E1.6) → real
  init/index/status handlers (E1.7) → MCP serve (E1.8). docs/ scaffold (E0.5) can interleave.
- 2026-06-23 #5 (cron) — Built internal/extract (Extractor interface + Symbol/Reference/
  FileResult types + LanguageForPath) and internal/extract/gosrc (pure-Go go/parser backend:
  funcs/methods/types/tests with FQN, signature via go/printer, docstrings, source slice +
  line ranges; call-reference edges from func bodies, builtins filtered; 4 tests cover
  funcs/methods/types/test-files/pointer-receivers/syntax-errors). All green, gofmt clean,
  CGO_ENABLED=0 build + vet OK → E1.4 done.
  **NEXT:** embed provider (Ollama /api/embed) E1.5 → veclite integration E1.6 → indexer
  (walk→extract→embed→store, resolve refs to edges) → real handlers E1.7 → MCP serve E1.8.
- 2026-06-23 #6 (cron) — Built internal/embed: Provider interface + EmbeddingProfile
  (Compatible/CheckCompatible/IncompatibleError guard) + OllamaProvider (POST /api/embed,
  batched, dims inference, Available() via /api/tags model check). 6 httptest-based tests
  (embed/empty/error/count-mismatch/availability/profile), all pass; build+vet+fmt clean
  → E1.5 done.
  **NEXT (E1.6 veclite):** FIRST read the real veclite API from ~/projects/veclite
  (options.go for WithDistanceType/distance constants — research flagged floats.DistanceType
  may be internal; confirm the exported way to set distance; collection.go for Insert/
  InsertDocument/Search/HybridSearch; metadata for the profile guard). Then write
  internal/vector wrapper (Open/EnsureCollection w/ profile metadata guard/Insert/Search/
  Hybrid) + tests using an in-memory veclite (":memory:"). May need go get veclite@latest
  (or replace => ~/projects/veclite if proxy lacks it).
- 2026-06-23 #7 (cron) — Read real veclite API (v0.19.0; confirmed exported veclite.Distance*
  constants, WithTextIndex enables Content BM25, Result/Record/Filter/SearchOption exported).
  Built internal/vector: Store wrapping veclite — Open w/ profile-metadata guard (stores
  embed.EmbeddingProfile JSON in db metadata, returns *embed.IncompatibleError on mismatch),
  Insert(InsertDocument), Search/HybridSearch (TopK + project Equal filter + WithContent),
  DeleteByFile (Find+Delete for incremental), payload↔NodeMeta mapping. 5 tests
  (insert/search, project filter, delete-by-file, hybrid, persistent profile guard), all pass.
  `go get veclite@latest` → v0.19.0 from proxy (no replace needed). CGO_ENABLED=0 build still
  clean (onnx/tokenizers transitive but not required). → E1.6 done. **Storage layer complete.**
  **NEXT:** internal/index indexer — walk project (gitignore-aware), dispatch by language to
  extractor, batch-embed symbol sources via embed.Provider, store nodes in graph + vectors in
  veclite (link node_id↔vec), resolve extract.Reference targets to edges by FQN/symbol match
  (weight 0.7), incremental via graph.FileHash/SetFileHash. Then internal/app service +
  real init/index/status handlers (E1.7) + MCP serve (E1.8) = first end-to-end slice.
- 2026-06-23 #8 (cron) — Built internal/index (the indexer) + graph helpers (UpdateNodeVecID,
  ProjectNodes, WipeProject) + vector.DeleteByProject. Indexer: gitignore-style exclude walk
  (config globs + hidden dirs + maxsize + language filter), per-file sha256 incremental skip,
  per-changed-file delete+re-extract, file+symbol nodes with `defines` edges, batched embedding
  via embed.Provider→veclite with node_id↔vec_id linkage, two-pass reference resolution into
  call edges (cross-file, weight 0.7, self-edges skipped) against project-wide symbol map,
  authoritative totals from graph.Stats. 4 tests (full index, incremental skip+edit, reindex
  stability/no-dup, structure-only w/o embedder; cross-file Other→Run resolved), all pass.
  build+vet+fmt clean → indexer done (E1.10). **The walk→extract→embed→store pipeline works.**
  NOTE (documented limitation): incremental rebuilds OUTGOING edges of changed files; incoming
  edges from UNCHANGED files to changed symbols need `index --reindex` (fully correct). Future:
  persist refs in a table to make incremental edge-consistent. See indexer.go resolveEdges doc.
  **NEXT (E1.7/E1.8):** internal/app Session (lazy-open graph+vector+provider per config/
  project) + Service (Init/Index/Status) → wire real cmd handlers (init/index/status, +--json,
  --reindex, --local) → MCP serve (E1.8). Then `codemap index` works on a real dir (structure-
  only if Ollama down). First demoable end-to-end slice.
- 2026-06-23 #9 (cron) — Built internal/app (Session: lazy-open graph+vector, Embedder from
  config; Service: Init/Index/Status with project resolution via the registry — walk up cwd
  to a registered path, closest wins; detectLanguage from marker files; structure-only
  fallback when Ollama unreachable via optional Available() type-assertion). Wired real
  cmd handlers (runInit/runIndex/runStatus) with --json/--reindex/--no-embed/--local, JSON
  output, formatCounts. 2 app integration tests (lifecycle init→index→status, unregistered)
  + structure-only path, all pass. → E1.7 done.
  **DEMO (real end-to-end slice):** `codemap index --no-embed` on the codemap repo itself
  indexed 23 Go files → 231 nodes (23 file, 73 func, 58 method, 44 test, 33 type), 630 edges;
  `status`/`status --json` render correctly; incremental re-run skipped all 23. **It works.**
  **NEXT (E1.8):** MCP server `serve` — go-sdk mcp.NewServer (newline-delimited StdioTransport,
  TD7), thin handlers over internal/app delegating to Service, initial 5 tools (codemap_init,
  _index, _status, _semantic, _callers). _semantic needs a Service.Semantic (vector.Search via
  query embed); _callers needs graph traversal (start internal/graph BFS or simple edge query).
  After E1.8, Epic 1 MVP foundation complete; codemap usable via CLI + MCP.
- 2026-06-23 #10 (cron) — Built E1.8 MCP serve + the query layer it needs. graph: Callers/
  Callees (alias-qualified edge joins). app.Service: Callers/Callees/Semantic + SymbolRef/
  RelationReport/SemanticReport; Session.SetEmbedder override (testable semantic). internal/mcp:
  go-sdk v1.6.1 server, newline-delimited StdioTransport (TD7), 5 thin tools (codemap_init/
  _index/_status/_semantic/_callers) → JSON, optional `path` arg. Wired `serve` cmd w/ signal
  ctx. Tests: graph Callers/Callees; app Callers + Semantic (fake embedder); **mcp end-to-end
  via in-memory client+server (tools/list = all 5; codemap_status→registered+nodes; codemap_
  callers(Helper)→Run)**. All green, build+vet+fmt clean. → E1.8 done.
  **★ EPIC 1 (MVP FOUNDATION) COMPLETE — codemap works via CLI + MCP.** Storage (graph+vector+
  config+embed), indexer, query layer, both surfaces, ~52 tests.
  Note: static-file stdin smoke test hits an EOF race (responses unflushed) — that's a TEST
  artifact, not a server bug; real clients keep the pipe open (the in-memory test proves it).
  **NEXT (release-ready scaffolding, all well-specified from research):** .golangci.yml (E5.3)
  → CI ci.yml (E5.4) → .goreleaser.yaml v2 CGO_ENABLED=0 (E6.1) → release.yml + brews block
  (E6.2/E6.3) → glyphrun.config.yml + specs/*.yml now that the CLI works (E5.2). THEN the big
  user-facing features: studio TUI (E4, Charm v2) and LSP backend (E2). docs/ scaffold (E0.5)
  can interleave. Hold actual v0.1 tag until TUI+LSP land (D1=everything) OR cut an early
  v0.1.0-rc once CI/goreleaser are green (confirm Q-publish first).
- 2026-06-23 #11 (cron) — Release-ready scaffolding, all validated against installed tools
  (golangci-lint v1.64.8, goreleaser v2.13.3, glyph v0.1.2). .golangci.yml (v2 schema, mirrors
  vecgrep minus viper). .goreleaser.yaml (v2, CGO_ENABLED=0, 5 targets, brews→homebrew-tap).
  ci.yml (test/race/build/coverage + golangci-lint-action@v8 lint job). release.yml (tag v*,
  goreleaser-action@v7 ~>v2, GITHUB_TOKEN+HOMEBREW_TAP_TOKEN). Updated Taskfile lint to detect
  golangci-lint v2 (local v1 → graceful fallback to vet+gofmt; CI uses the action).
  ★ VALIDATED: `goreleaser check` OK; `goreleaser build --snapshot` built ALL 5 targets
  (darwin/linux amd64+arm64, windows amd64) pure-Go in 11s — CGO-free cross-compile PROVEN.
  `task check`/`task build` green. → E5.3, E5.4, E6.1, E6.2, E6.3 done.
  NOTES: goreleaser warns `brews` phased out for `homebrew_casks` (still works v2.x; matches
  vecgrep; future migration). Repo NOT git-initialized yet → version injects "dev"; actual
  release needs git init + public GitHub repo + HOMEBREW_TAP_TOKEN secret (Q-publish, user).
  Won't git init/push without the user's go-ahead (outward-facing).
  **NEXT:** glyphrun E2E specs (E5.2; glyph v0.1.2 installed, CLI works) → studio TUI (E4) →
  LSP backend (E2). docs/ scaffold (E0.5) interleave.
- 2026-06-23 #12 (cron) — glyphrun E2E (E5.2). glyphrun.config.yml (mirror vecgrep) + 3 specs
  in specs/ (version, help, index_status — the last spins up an isolated HOME+CODEMAP_DATA,
  writes a 1-line Go file, runs init→index--no-embed→status, asserts nodes:/function=/go=).
  Used `/bin/sh -lc` argv pattern + single-line semicolon Go source to dodge YAML newline
  escaping. Stamped contractHashes via `glyph spec verify --stamp`. Taskfile flows now
  `deps: [build]`. ★ `task flows`: all 3 PASS. Artifacts gitignored. → E5.2 done.
  Note: specs use screen.contains (not snapshot-verify), so no golden snapshots to commit;
  `snapshot:` steps are debug-only. **NEXT:** studio TUI (E4) — start E4.1 shell (Charm v2,
  charm.land paths!) + E4.4 Impact tab (easiest). Validate ntcharts v2 build EARLY (risk R1:
  replace-directive) when adding E4.2 Metrics. Then LSP (E2), hybrid queries (E3), docs (E0.5).
- 2026-06-23 #13 (USER REQUEST) — Published the repo. git init (was not a repo), fixed
  .gitignore to exclude /bin/ and .claude/ (caught bin/codemap staged), committed (43 files,
  no secrets/binaries). `gh repo create abdul-hamid-achik/codemap --public --push`. Set
  `HOMEBREW_TAP_TOKEN` Actions secret from ~/.config/secrets/env (piped via stdin, value never
  printed/logged). ★ CI ran on push and PASSED: test+race+build+coverage (2m35s) and
  golangci-lint v2 (48s, first real v2 run — clean). Repo PUBLIC, both workflows active.
  → Q-publish resolved; E6.4 unblocked (tag held for everything-scope, rc on request).
  **NEXT (resume feature build):** studio TUI (E4) — E4.1 shell + E4.4 Impact tab, charm.land
  v2 paths, validate ntcharts v2 (R1) when adding E4.2. Then LSP (E2), hybrid (E3), docs (E0.5).
- 2026-06-23 #14 (cron) — studio TUI (E4). Mirrored vecgrep's Bubble Tea v2 patterns
  (tea.NewView+AltScreen, tea.KeyPressMsg/msg.String(), tea.WindowSizeMsg, lipgloss v2 styles,
  bubbles v2 textinput). Built internal/tui: theme/model/view/run + 7 tests. Tabbed shell
  (Graph/Metrics/Impact/Search), tab+shift+tab+ctrl+c, async status/semantic/callers via
  tea.Cmd→Msg. Metrics = ASCII bar charts over Stats (no ntcharts dep yet). Impact = symbol→
  callers. Search = semantic. Graph = placeholder. Wired `studio` cmd + interactive-default
  launch (non-TTY→help, so scripts/CI safe). Deps: charm.land bubbletea v2.0.7/lipgloss
  v2.0.4/bubbles v2.1.0. All tests green, build+vet+fmt clean. → E4.1/E4.4/E4.5 done, E4.2
  partial (ASCII bars). COMMIT+PUSH this increment (repo is live).
  **NEXT:** E4.2 real ntcharts v2 charts (validate R1 first) + E4.3 graph view; then LSP (E2),
  hybrid impact (E3), docs scaffold (E0.5). TUI glyphrun spec once it's richer.
- 2026-06-23 #15 (cron) — Flagship impact query (E3.1). graph.BlastRadius (cycle-safe BFS up
  `calls` edges, depth-limited, min-depth per node; tests for chain/depth-limit/cycle) +
  Service.Impact (locations + direct callers + blast radius + tests-in-radius + untested flag;
  integration test). Exposed via 4 new CLI query commands (callers/callees/impact/semantic,
  with --depth/--top/--json) and 2 new MCP tools (codemap_callees, codemap_impact → 7 tools
  total). All tests green, build/vet/fmt clean. ★ DEMO on codemap itself: `impact AddNode` →
  8 direct callers, 14 blast radius, 11 covering tests with the indexFile→IndexProject→Index
  chain. COMMIT+PUSH. **NEXT:** E4.2 ntcharts (validate R1) / E4.3 graph view; or LSP (E2);
  or E3.4 hotspots/orphans/path (BFS helper exists); or docs (E0.5). Lean E2 LSP next for
  precise cross-file edges (current edges are by-name weight 0.7).
- 2026-06-23 #16 (cron) — Graph analytics (E3.4). graph.Hotspots (incoming calls/references
  rank, hub detection), Orphans (func/method nodes with no caller — dead-code candidates),
  Path (shortest call path via BFS over outgoing calls + parent-pointer reconstruction;
  cycle-safe). Service.Hotspots/Orphans/Path (+ a `project()` resolver helper) + CLI commands
  (hotspots --top, orphans --top, path <from> <to>) + MCP tools (codemap_hotspots/_orphans/
  _path). **MCP now 10 tools; CLI 13 commands.** graph test for all three. All green, fmt
  clean. DEMO: hotspots (Close/Error/NewService hubs), orphans (entrypoints, documented FP),
  path Index→…→AddNode. COMMIT+PUSH. Note: by-name resolution inflates same-named hubs
  (e.g. 3× Close@38) — LSP (E2) will disambiguate. **NEXT: LSP backend (E2)** — the remaining
  big differentiator (precise weight-1.0 cross-file edges). Or E4.2 ntcharts / E4.3 graph view
  / E0.5 docs / E3.2 semantic_callers. Leaning E2.

## Resolved product decisions (user, 2026-06-23)
- [x] **D1. v0.1 scope = EVERYTHING** — MVP + LSP + studio TUI all ship in 0.1 (Epics 1–6).
  Epic 7 (deep ecosystem) may trail. Caveat: publish only once it builds + specs pass.
- [x] **D2. studio TUI = tabbed** — `[1] Graph` · `[2] Metrics` · `[3] Impact` · `[4] Search`.
- [x] **D3. config = XDG + `~/.codemap` fallback** — `$XDG_CONFIG_HOME/codemap/config.yaml`,
  `$XDG_DATA_HOME/codemap/`, `$XDG_CACHE_HOME/codemap/`; `CODEMAP_*` overrides; honor
  `~/.codemap/` if present. **Config format = YAML** (ecosystem standard, not TOML).

## Tech decisions taken from research (reversible — flag if you disagree)
- [x] **TD1. v0.1 extraction = pure-Go (LSP + stdlib `go/parser`), `CGO_ENABLED=0`.**
  tree-sitter needs CGO (breaks clean cross-compile the whole ecosystem relies on), so it
  becomes an OPTIONAL backend behind the `treesitter` build tag, out of release binaries,
  targeted to round out language coverage in 0.2 (zig-cc matrix or purego path).
  ⚠️ **This is the biggest deviation from SPEC's dual-backend MVP — veto if you want
  tree-sitter in 0.1 release binaries.**
- [x] **TD2. One vector space in v0.1** (code text). Named spaces (docstring/signature) →
  0.2+ (veclite ≥0.17 already supports them).
- [x] **TD3. No domain-entity/LogicLens nodes in v0.1** (needs LLM enrichment) → Phase 5.
- [x] **TD4. Registry = `$XDG_DATA_HOME/codemap/projects/`** (+ `~/.codemap` fallback),
  `init --local` escape hatch. Separate from vecgrep's registry ("separate is cleaner").
- [x] **TD5. Lazy DB open** on first query (v1 multi-process answer).
- [x] **TD6. LSP client = `go.lsp.dev/protocol` + `go.lsp.dev/jsonrpc2` (v1.0.0)**, not
  sourcegraph/jsonrpc2 (typed LSP coverage, both just hit v1.0.0).
- [x] **TD7. MCP stdio = newline-delimited JSON-RPC** (go-sdk StdioTransport as-is). Never
  let LSP's Content-Length framing leak into MCP. (Hard-won lesson from `glyph`.)
- [x] **TD8. CLI emits `--json`** for agents (three-surface pattern from noted).

## Verified dependency set (web-checked 2026-06-23; pin in go.mod when scaffolding)
- go `1.25.x` · module `github.com/abdul-hamid-achik/codemap`
- `github.com/modelcontextprotocol/go-sdk` **v1.6.1** (official MCP SDK; `/mcp` subpkg)
- `github.com/abdul-hamid-achik/veclite` **≥ v0.17.0** (latest in-tree 0.19.0)
- `modernc.org/sqlite` **v1.53.0** (pure-Go)
- `github.com/spf13/cobra` **v1.10.2** (+ optional `viper` v1.21.0)
- `go.lsp.dev/protocol` **v1.0.0** + `go.lsp.dev/jsonrpc2` **v1.0.0**
- `gopkg.in/yaml.v3` v3.0.1 · `github.com/fsnotify/fsnotify` v1.10.x ·
  `github.com/sabhiram/go-gitignore` (gitignore-aware walk)
- **TUI (Charm v2, vanity paths!):** `charm.land/bubbletea/v2` **v2.0.7** ·
  `charm.land/lipgloss/v2` **v2.0.4** · `charm.land/bubbles/v2` **v2.1.0** ·
  `charm.land/glamour/v2` **v2.0.1**
- **Charts:** `github.com/NimbleMarkets/ntcharts/v2` **v2.2.0** (⚠️ replace-directive risk)
- **Optional/0.2:** `github.com/tree-sitter/go-tree-sitter` v0.24.0 + per-grammar modules
  (CGO); `github.com/scip-code/scip/bindings/go/scip` v0.8.1 (SCIP import; moved from
  sourcegraph). Embeddings: no lib — POST `http://localhost:11434/api/embed`.
- **Build/CI:** goreleaser **v2.16.0** (config `version: 2`); GitHub Actions
  checkout@v6 + setup-go@v6 (go 1.25/1.26) + goreleaser-action@v6 `~> v2`; codecov@v7;
  Task `v3.x`. Secrets: `GITHUB_TOKEN` (auto) + `HOMEBREW_TAP_TOKEN` (PAT → homebrew-tap).

## Active risks (from research — watch these)
- **R1.** ntcharts v2 `go.mod` `replace charm.land/bubbletea/v2 => neomantra fork`; ignored
  by consumers → may not build against bubbletea v2.0.7. Mitigation: build early; mirror the
  replace or pin bubbletea v2.0.6.
- **R2.** tree-sitter = CGO → breaks static cross-compile + complicates goreleaser. Mitigated
  by TD1 (keep it optional/0.2; everything else pure-Go).
- **R3.** `modernc.org/sqlite` very high release cadence → pin exact; run integration tests
  before bumps.
- **R4.** LSP servers implement varying LSP versions/extensions → client must tolerate
  unknown/optional fields; lock server versions in CI.
- **R5.** Ollama is a runtime dep → detect `nomic-embed-text` at startup (`GET /api/tags`),
  clear error if missing.

## Open questions for user (non-blocking; sensible defaults taken above)
- [!] **Q-CGO** — OK to ship v0.1 release binaries pure-Go (LSP + go/parser) with tree-sitter
  deferred to 0.2 (per TD1)? Or pull tree-sitter into 0.1 (accept CGO + zig-cc matrix)?
- [x] **Q-publish RESOLVED (2026-06-23)** — repo published PUBLIC at
  github.com/abdul-hamid-achik/codemap; `HOMEBREW_TAP_TOKEN` secret set (from the user's
  ~/.config/secrets/env); CI green (test/race/build/coverage + golangci-lint v2). Release
  pipeline ready. Actual `v0.1.0` tag still held pending TUI+LSP (D1=everything); an
  `v0.1.0-rc.1` tag can be cut on request to exercise goreleaser→release+homebrew-tap.

---

## Epic 0 — Docs & coordination
- [x] E0.1 BACKLOG.md  · [x] E0.2 README.md  · [x] E0.3 AGENTS.md  · [x] E0.4 CLAUDE.md
- [x] E0.5a LICENSE (MIT) + .gitignore
- [ ] E0.5 docs/ scaffold for VitePress (Bun, Vercel deploy; no GitHub Pages)

## Epic 1 — Foundation (pure-Go MVP)
- [x] E1.1 go.mod (`github.com/abdul-hamid-achik/codemap`, go 1.25.5) + cobra v1.10.2
- [x] E1.2 Taskfile.yml (vecgrep-style: build→./bin, check[ci,verify], doctor, flows, site:*)
- [~] E1.7a CLI skeleton — cmd/codemap/main.go: root + version + init/index/status/serve/studio
      (handlers stubbed `not implemented`; builds & runs). Real handlers land per-epic.
- [x] E1.9 config: XDG + fallback, `CODEMAP_*` env, YAML, resolution chain (14 tests pass)
- [x] E1.3 SQLite schema + graph store (nodes/edges/projects/index_state, WAL/FK, stats; 11 tests)
- [x] E1.4 extraction: `go/parser` backend (Go) — pure-Go default (extract + gosrc; tests pass)
- [x] E1.5 embed.Provider + ollama.go (/api/embed) + EmbeddingProfile guard (6 tests pass)
- [x] E1.6 veclite integration (internal/vector: collection, payload=filterable, content=
      searchable, hybrid, profile guard, delete-by-file; 5 tests pass) — veclite v0.19.0
- [x] E1.10 indexer (internal/index): walk→extract→embed→store→resolve-edges, incremental,
      reindex, structure-only; 4 tests pass (cross-file resolution verified)
- [x] E1.7 CLI: `init`, `index`, `status` (+ `--json`/`--reindex`/`--no-embed`/`--local`) +
      internal/app Session+Service (registry-based project resolution, structure-only fallback);
      2 integration tests; DEMO indexed codemap itself (231 nodes/630 edges)
- [x] E1.8 MCP server: `serve` + 5 tools (init, index, status, semantic, callers); newline
      framing; go-sdk v1.6.1; end-to-end test via in-memory client. **EPIC 1 COMPLETE.**

## Epic 2 — LSP integration
- [ ] E2.1 headless LSP client (go.lsp.dev) — start gopls/ts_ls subprocess, JSON-RPC, init
- [ ] E2.2 LSP extraction (documentSymbol/definition/references/callHierarchy/impl)
- [ ] E2.3 unified extractor (merge LSP precedence over parser, dedupe by FQN)
- [ ] E2.4 incremental hash-based reindex
- [ ] E2.5 MCP tools: callees, references, blast_radius, test_coverage, symbols, dependencies

## Epic 3 — Hybrid queries
- [x] E3.1 codemap_impact (blast radius + test coverage + untested) — graph.BlastRadius
      (cycle-safe depth-limited BFS up `calls` edges) + Service.Impact + CLI `impact` + MCP
      `codemap_impact`; tests + real demo (AddNode: 8 callers/14 blast/11 tests). TODO: add
      semantically-similar (needs vector.Similar).
- [ ] E3.2 codemap_semantic_callers (semantic → graph expansion)
- [ ] E3.3 codemap_refactor_plan
- [x] E3.4 hotspots / orphans / path — graph.Hotspots (incoming-usage rank), Orphans
      (no-caller funcs/methods), Path (shortest call path, BFS w/ parent reconstruction) +
      Service + CLI (hotspots/orphans/path) + MCP (codemap_hotspots/_orphans/_path → 10 tools).
      Tested + demoed on codemap (path Index→IndexProject→indexFile→AddNode).

## Epic 4 — studio TUI (Charm v2)
- [x] E4.1 TUI shell (bubbletea v2 + lipgloss v2 + bubbles v2 textinput; tab bar, header/
      footer, key handling, async status load; 7 tests). `studio` cmd + interactive-default; non-TTY→help
- [x] E4.4 Impact tab (symbol textinput → Service.Callers, async)
- [~] E4.2 Metrics tab — DONE as ASCII bar charts over Stats (kinds/languages/nodes/edges),
      no deps; ntcharts v2 real charts still TODO (validate R1 replace-directive then)
- [ ] E4.3 Graph tab (Sugiyama/layered DAG on canvas; fallback collapsible tree) — placeholder now
- [x] E4.5 Search tab (semantic via Service.Semantic, async, score-ranked results)

## Epic 5 — Tests & quality
- [ ] E5.1 unit tests (graph traversal/cycles, extract, search, config) — high coverage
- [x] E5.2 glyphrun.config.yml + specs/*.yml (version, help, index_status) — 3 specs PASS via
      `task flows`; contractHashes stamped
- [x] E5.3 .golangci.yml (v2 schema; errcheck + staticcheck) — local lint falls back to
      vet+gofmt when golangci-lint v2 absent
- [x] E5.4 CI .github/workflows/ci.yml (test/race/build/coverage + golangci-lint-action lint)

## Epic 6 — Distribution (v0.1)
- [x] E6.1 .goreleaser.yaml (v2, CGO_ENABLED=0, 5 targets) — VALIDATED, all cross-compile
- [x] E6.2 .github/workflows/release.yml (tag v*, goreleaser-action@v7 ~>v2, both tokens)
- [x] E6.3 brews: block → abdul-hamid-achik/homebrew-tap (note: brews deprecation, still works)
- [ ] E6.4 tag + publish v0.1 (ONLY after build + specs pass; confirm Q-publish first)

## Epic 7 — Ecosystem integration (Phase 5, may trail 0.1)
- [ ] E7.1 MCP registration snippets (Claude Code, Hermes, vecai, local-agent)
- [ ] E7.2 shared/aware project registry with vecgrep
- [ ] E7.3 hunk --agent-context sidecar from codemap_impact
- [ ] E7.4 noted integration (notes about graph findings: cycles, dead code)
