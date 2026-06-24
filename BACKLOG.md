# codemap — Backlog

> Source of truth for the autonomous build loop. Each iteration: read this file, pick the
> next unstarted task, do it, update status here. Convert relative dates to absolute.
> Started 2026-06-23. Cron `ffee7a2b` (every 5 min). See AGENTS.md / SPEC.md for design.

## Iteration log (post-v0.7.0)
- 2026-06-24 #137 (verify + docs — real-project edge cases hold; set expectations about deps) —
  continued dogfooding real layouts; all **clean** (no bugs): a 4 MB file is skipped (>1 MiB
  `MaxFileBytes`); `status`/queries from a **subdirectory** find the registered project root; **multi-
  project isolation** holds (project A's `find` never returns project B's symbols, and vice-versa).
  Shipped a small docs win from it: the README quick-start now states codemap "indexes your code, not
  your dependencies" (node_modules/venv/vendor/dist/build/__pycache__/.git skipped by default,
  configurable) — the #1 evaluation worry for a new user — and fixed a stale `--precise` line
  (TypeScript-only → TS/JS/Python). Docs-only; no behavior change. COMMIT+PUSH. (Reliability + venv
  fixes since v0.7.0 make a clean **v0.7.1 patch** when you want it.)
- 2026-06-24 #136 (BUG — exclude Python virtualenvs so they don't flood the graph) — dogfooded
  real-project layouts after v0.7.0. **node_modules/vendor/dist/.git etc. are correctly skipped**
  (default `Exclude` + the dot-prefix rule — verified node_modules stays out with LSP on). But a gap
  for the just-shipped **Python** support: virtualenvs named `venv`/`env`, and `site-packages`, are
  non-hidden and weren't excluded — so a Python project with a venv indexed **all of site-packages**
  (verified: `requests_internal` from `venv/lib/site-packages/...` was a graph node, flooding
  find/hotspots/search with dependency code). Fix: added `venv`, `env`, `site-packages` to the default
  `Index.Exclude`. Verified: same project now indexes only `src/app.py`; the venv symbol is gone. Test
  `TestIndexExcludesDependencyDirs` (src indexed; node_modules/venv/vendor symbols absent — uses `.go`
  fixtures so it runs in CI). docs/configuration.md updated (representative example + the note that
  setting `exclude` replaces the defaults, which is accurate — slices overlay). Full suite + lint(0) +
  fmt green. Real usability fix for Python on actual repos. COMMIT+PUSH.

## 🏷️ Release — v0.7.0 (2026-06-24)
**Multi-language breadth + reliability.** Headline since v0.6.0:
- **More languages, riding the LSP backend.** **JavaScript** (one `typescript-language-server` serves
  both TS and JS, calls resolving across the `.ts`↔`.js` boundary), **Python** (`pyright-langserver`),
  and a real **JSX/TSX** call graph (`.tsx`/`.jsx` opened with the *react languageIds so `<Component/>`
  usages resolve). `--precise` is the unified exact-resolution pass for all of them (go/types for Go,
  callHierarchy for the LSP languages). `appendSymbols` drops parameter/local noise.
- **`codemap doctor`** (CLI **and** MCP, the 19th tool) — checks the go toolchain, gopls, each language
  server, and Ollama embeddings, with install hints; diagnoses "why isn't my X indexed?".
- **Hybrid semantic search** — `semantic` (alias `search`) now fuses vector similarity with BM25 over
  symbol/fqn (the store had it; it was never wired). Better keyword + meaning matches.
- **Reliability:** every LSP request is bounded by a 30s timeout (a hung server can't freeze `index`);
  skipped/timed-out files are counted and explained (CLI + studio); **incremental reindex now prunes
  files deleted on disk** (was leaving ghost symbols in find/callers/search — a real trust bug).
- **`orphans` you can trust** — follows functions wired by *value* (cobra `RunE`, `mux.HandleFunc`), so
  framework handlers aren't false dead-code; `hotspots` stays a clean call-only metric.
- **studio:** `ctrl+g` opens any symbol in the Graph walker; `ctrl+r` preserves the project's precision
  (doesn't drop the call graph on refresh); honest empty-states and skip reporting.
- **Parity & polish:** CLI↔MCP parity (`unannotate`, `doctor`); language-aware `--precise` onboarding
  tips; markup (`.vue`/`.html`/`.css`) recognized as "planned"; every engine/precise surface names all
  four languages; a scannable languages table in the README.

Pre-release: CGO_ENABLED=0 full suite + `-race` green · golangci-lint v2 0 issues · gofmt/vet clean ·
`goreleaser check` + snapshot cross-compiled all 5 pure-Go targets · tree clean. Tagging `v0.7.0`
triggers `.github/workflows/release.yml` → goreleaser → binaries + `abdul-hamid-achik/homebrew-tap`.
Ships: CLI · **MCP (19 tools)** · studio TUI · graph+vectors+LSP (Go · TypeScript · JavaScript · Python).

## 🏷️ Release — v0.6.0 (2026-06-24)
**TypeScript becomes a first-class language**, plus trustworthy dead-code analysis and richer studio
navigation. Headline since v0.5.0:
- **Multi-language / TypeScript (LSP backend).** `internal/lsp` drives `typescript-language-server`;
  `index` extracts TS classes/methods/functions (structure + semantic search always), and
  **`index --precise` gives TypeScript a real call graph** via `callHierarchy` — so
  `callers`/`callees`/`impact`/`hotspots`/`path` work for TS through the same backend-blind queries Go
  uses. `--precise` is now the unified exact-resolution pass: go/types for Go, callHierarchy for TS.
  Pure-Go / CGO_ENABLED=0 (the server is a spawned subprocess). Present-aware: a Go-only repo never
  spawns a server.
- **`orphans` you can trust.** Now follows functions wired by *value* (cobra `RunE: handler`,
  `mux.HandleFunc("/", s.handle)`) via a new `references` edge type that never enters the call graph —
  so framework handlers are no longer false dead-code. `hotspots` stays a clean call-only hub metric.
- **studio `ctrl+g`** opens any Search/Impact/Metrics selection in the Graph walker; honest empty-state
  for a TS project indexed without `--precise`.
- **`codemap_unannotate`** MCP tool — agents can prune the knowledge layer, not just append (CLI/MCP
  parity). Every precise/engine surface (CLI/docs/studio/status/MCP) made honest about which engine
  resolved the graph.
- New E2E flows: `typescript.yml`, `studio_ts.yml` (studio driving a TS call graph). Ships: CLI ·
  **MCP (18 tools)** · studio TUI · graph+vectors+LSP (Go + TypeScript).

Pre-release: CGO_ENABLED=0 full suite green · golangci-lint v2 0 issues · gofmt/vet clean ·
`goreleaser check` + snapshot cross-compiled all 5 pure-Go targets · tree clean. Tagging `v0.6.0`
triggers `.github/workflows/release.yml` → goreleaser → binaries + `abdul-hamid-achik/homebrew-tap`.

## 🏷️ Release — v0.1.0 (2026-06-23)
First public release, tagged on user go-ahead. Pre-release audit (7-agent workflow) returned
**GO, zero blockers**: CGO_ENABLED=0 build + full test suite + race clean; golangci-lint v2 0
issues / gofmt / vet clean; all 7 glyphrun E2E specs pass (graphs · vectors via Ollama · LSP via
gopls); `goreleaser check` + snapshot cross-compiled all 5 pure-Go targets with correct
version-injection ldflags + homebrew-tap (`HOMEBREW_TAP_TOKEN`); tree clean on main. Tagging `v0.1.0`
triggers `.github/workflows/release.yml` → goreleaser → binaries + `abdul-hamid-achik/homebrew-tap`.
Ships: CLI (20 cmds) · MCP (17 tools) · studio TUI (Graph walker+source, Metrics dashboard, Impact,
Search; `?` help) · graph+vectors+LSP · signatures/docstrings/source everywhere · annotations layer.

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
- 2026-06-23 #17 (cron) — LSP client core (E2.1). Hand-rolled internal/lsp (no go.lsp.dev,
  no new deps): jsonrpc.go = Content-Length framed JSON-RPC 2.0 conn (bg read-loop, id→pending
  response routing, server-request handler so the server doesn't stall, cycle/close handling);
  client.go = Client (Spawn subprocess, Initialize/Initialized/DidOpen/DocumentSymbols/
  References/Shutdown/Exit/Close) + LSP types (Position/Range/Location/DocumentSymbol) + URI.
  Tests: fake-server full round-trip (initialize+documentSymbol+references over io.Pipe) + REAL
  gopls v0.21.0 integration (Foo/Bar symbols, 1.17s; skips when gopls absent → CI-safe) +
  `-race` clean. CRITICAL: LSP uses Content-Length framing, kept strictly separate from MCP's
  newline framing (documented in package doc). All green. COMMIT+PUSH.
  **NEXT (E2.2/E2.3):** wire the LSP client into extraction — an lsp Extractor (DocumentSymbols
  for TS/Python; References/callHierarchy → precise edges weight 1.0), unified merge w/ go/parser
  (LSP precedence, dedupe by FQN), graceful skip when no server. Then E4.2 charts/E4.3 graph/
  E0.5 docs.
- 2026-06-23 #18 (cron) — LSP→symbol mapping (E2.2a). internal/extract/lspsrc: stateful
  Extractor owning an lsp.Client session (New spawns+initializes a server at a root; ExtractFile
  = DidOpen+DocumentSymbols→[]extract.Symbol; Close shuts down). appendSymbols recurses children
  building dotted FQNs (ClassName.method), maps LSP SymbolKind→codemap kind (func/method/type;
  skips vars/fields), 0-based→1-based lines, source via line slice. Tests: mapKind, nested-FQN
  mapping, lineSlice, + REAL gopls (Foo=function, Bar=type, 1.14s; skips in CI). All green,
  build/vet/fmt clean. COMMIT+PUSH.
  **NEXT (E2.2b/E2.3):** indexer integration — per-language LSP session registry + lifecycle in
  IndexProject, use lspsrc for langs w/o go/parser (TS/Python) or as precise override; merge w/
  go/parser (LSP precedence, dedupe by FQN); References→precise call edges (weight 1.0). Config
  flag/server map. Then E4.2 ntcharts, E4.3 graph view, E0.5 docs.
- 2026-06-23 #19 (USER FEEDBACK) — User: "graph not working, struggling to see usefulness,
  polish, take better usage of the screen." Addressed: overhauled the studio TUI.
  • FULL-SCREEN LAYOUT: body fills width×height via lipgloss Width/Height, footer pinned to
    bottom, Metrics bars scaled to terminal width (was fixed 24-wide), digit 1-4 tab switching.
  • GRAPH TAB NOW WORKS: two-column call-graph explorer — left = Hubs (most-referenced symbols),
    right = selected hub's "Called by" + "Calls", ↑/↓ navigate, full-height divider, async load.
  • Default tab = Graph. Verified visually via the studio glyph snapshot (graph.txt/metrics.txt
    show full 120×40 usage). 8 TUI tests incl TestRenderFillsScreen (height==40, width-bounded).
  • Added specs/studio.yml E2E (TUI under PTY) → 4 specs pass. All units green, fmt/vet clean.
  ★ NEW DIRECTIVE (loop re-prioritized): favor POLISH + real USEFULNESS over new backend —
  make the TUI/CLI/MCP genuinely demonstrate value, keep docs accurate, keep everything green.
  COMMIT+PUSH. **NEXT (polish-first):** TUI Impact/Search visual parity + result navigation;
  README/AGENTS accuracy pass (commands now incl callers/callees/impact/hotspots/orphans/path/
  studio); then LSP edge wiring (E2.2b) for precise hubs (fixes same-name inflation), E0.5 docs.
- 2026-06-23 #20 (cron, polish-first) — DOCS ACCURACY PASS. README + AGENTS were stale (listed
  MCP tools that don't exist: codemap_symbols/_search/_blast_radius/_test_coverage/_similar/
  _dependencies; commands that don't exist: blast-radius, search --kind). Fixed both to the
  REAL surface: 10 MCP tools (init/index/status/semantic/callers/callees/impact/hotspots/
  orphans/path), 13 CLI commands (+ a Commands table + --json note in README), accurate studio
  description (call-graph explorer, not node-link/ntcharts), real directory tree (internal/lsp,
  extract/lspsrc, vector, tui files; dropped planned-but-absent search/treesitter/scip dirs),
  and the hand-rolled LSP client note (superseding go.lsp.dev/TD6). Verified: no stale refs
  remain; build/tests green. COMMIT+PUSH. **NEXT (polish):** TUI Impact/Search result scrolling
  + selection; then LSP edge wiring (E2.2b). CLAUDE.md is still accurate (spot-checked).
- 2026-06-23 #21 (cron, polish-first) — TUI navigation polish. (1) Cross-tab DRILL-DOWN:
  enter on a Graph hub jumps to the Impact tab and runs impact for that symbol (explore→drill
  workflow). (2) SCROLLABLE results: ↑/↓ scroll Search hits and Impact blast-radius lists
  (single-line inputs ignore ↑/↓, so no focus-mode needed), with ▲/▼ "N more" indicators.
  (3) Per-tab footer hints. 2 new TUI tests (drill-to-impact, search scroll). Extended
  specs/studio.yml to drill Graph→Impact and snapshot the Impact tab (verified: Helper → 2
  callers/3 blast/0 tests/⚠untested, full-screen). All units + 4 E2E green, fmt/vet clean.
  COMMIT+PUSH. **NEXT (polish):** Search hit → enter to drill into impact (per-row select);
  then LSP edge wiring (E2.2b) for precise hubs; E0.5 VitePress docs scaffold.
- 2026-06-23 #22 (cron, polish-first) — FQN display polish (fixes the visible "3× identical
  Close" confusion). TUI now shows fully-qualified names everywhere (hub list/detail, search,
  impact locations + blast radius, metrics top hubs) via displayName(fqn,symbol); CLI too
  (hotspots/callers/callees/impact/semantic via disp()). Now hubs read graph.Store.Close vs
  app.Session.Close vs lsp.Client.Close — distinguishable. Added FQN render assertion. All
  units + 4 E2E green, fmt clean. COMMIT+PUSH. NOTE: indegree is still by-name-inflated
  (46/45) — disambiguation is a display fix; precise DE-inflation needs LSP edges (E2.2b).
  **NEXT:** LSP edge wiring (E2.2b) to fix indegree precision; or Search per-row drill
  (focus-mode); or E0.5 VitePress docs scaffold.
- 2026-06-23 #23 (cron, polish-first) — E0.5 VitePress docs site (a long-deferred v0.1
  deliverable; mirrored vecgrep's setup). Root package.json (codemap-docs, vitepress 1.6.4,
  site/site:build/site:preview scripts, overrides) + vercel.json (framework vitepress, bun) +
  docs/.vitepress/config.mts (nav/sidebar/search/editLink) + 6 content pages derived from the
  accurate README (index hero, quick-start, cli, studio, mcp, configuration). Verified:
  `bun install` (131 pkgs) + `bun run site:build` → dist with all 6 pages. Taskfile site:*
  retargeted to root scripts. node_modules + dist/cache gitignored; bun.lock committed. Go CI
  unaffected (no Go changes). COMMIT+PUSH. **NEXT:** LSP edge wiring (E2.2b) for precise
  indegree; or Search per-row drill (focus-mode); or studio Impact "direct callers/tests"
  sections.
- 2026-06-23 #24 (cron, polish-first) — studio in-app REINDEX. ctrl+r (global, any tab)
  runs a structure-only Service.Index then refreshes status + hubs — no quit/relaunch cycle;
  studio is now a live workspace. Empty states + footer + docs/studio.md mention it. 2 new
  TUI tests (ctrl+r fires reindex + resets graphLoaded; indexedMsg refreshes). All units + 4
  E2E green, fmt clean. COMMIT+PUSH. **NEXT:** LSP edge wiring (E2.2b) for precise indegree;
  or Search per-row drill; or studio Impact callers/tests sections.
- 2026-06-23 #25 (cron, polish-first) — Search per-row drill (completes explore→drill
  symmetry with Graph). ↑/↓ select a hit (highlighted, keep-visible window via windowStart);
  enter drills the selected hit into Impact when the query is unchanged, or runs a new search
  when the text was edited (no focus-mode needed — the value!=query check disambiguates).
  Selected row rendered as clean plain-text highlight (no ANSI nesting). 3 TUI tests (select,
  drill, edited-query-runs). docs/studio.md keys updated. All units + 4 E2E green, fmt clean.
  COMMIT+PUSH. **NEXT:** Impact blast-radius row select+drill (same pattern); or LSP edge
  wiring (E2.2b); studio looks feature-complete for v0.1.
- 2026-06-23 #26 (cron, polish-first) — Impact recursive drill. ↑/↓ select a blast-radius node
  (highlighted, windowed); enter drills the selected node into its own impact when the input
  is unchanged, or analyzes a newly-typed symbol (same value!=symbol disambiguation as Search).
  Drill workflow now complete across all tabs: Graph→Impact, Search→Impact, Impact→Impact.
  1 new TUI test; footer + docs updated. All units + 4 E2E green, fmt clean. COMMIT+PUSH.
  ★ studio TUI is feature-complete for v0.1 (full-screen, live reindex, drillable everywhere,
  FQN-clear). **NEXT:** the remaining material item is LSP edge precision (E2.2b) — currently
  the LSP client/lspsrc aren't in the index path, so "LSP value" isn't user-visible yet.
  Consider wiring lspsrc as an opt-in indexer backend (per-language servers) OR a precise
  `callers --lsp` path via gopls callHierarchy. Otherwise: docs polish, more glyphrun specs.
- 2026-06-23 #27 (cron, polish-first) — Docs accuracy + E2E coverage. CLAUDE.md reconciled:
  fixed key-files map (removed nonexistent internal/search; added internal/lsp, vector, index,
  embed, config; gosrc/lspsrc backends), marked ntcharts gotcha forward-looking (Metrics uses
  ASCII bars, no chart dep), softened the extraction line (go/parser today, LSP wiring in
  progress). Added specs/query.yml E2E (index→callers→impact→hotspots; FQN-clear output) → 5
  glyphrun specs all PASS. All docs (README/AGENTS/CLAUDE + VitePress) now match reality.
  COMMIT+PUSH. **NEXT:** the one remaining material lever is LSP edge precision (E2.2b) — a
  user-visible `callers --lsp`/callHierarchy path or lspsrc-in-indexer. Else: keep polishing.
- 2026-06-23 #28 (cron) — ★ LSP value is now USER-VISIBLE: precise callers via gopls.
  lsp.Client: PrepareCallHierarchy/IncomingCalls + WaitReady (waits for $/progress "end";
  declared window.workDoneProgress so gopls actually emits it) + isNull helper. app.Service
  PreciseCallers: graph resolves symbol→file, gopls documentSymbol→prepareCallHierarchy→
  incomingCalls; findSymbolPos matches gopls method names "(*Store).AddNode" by base name
  (the bug that made methods return none — root-caused via diagnostic dump). CLI `callers --lsp`.
  Tests: gopls callHierarchy + PreciseCallers on a METHOD (regression guard), both skip in CI.
  DEMO on codemap: callers Close --lsp = 7 exact vs by-name = 50 inflated; NewService/AddNode
  precise. docs/cli.md updated. All green, fmt/vet clean. COMMIT+PUSH. **NEXT:** callees --lsp,
  MCP precise option, or wire precise edges into the indexer; else polish.
- 2026-06-23 #29 (cron) — Precise callers now available to AGENTS via MCP. codemap_callers
  gained a `precise` param (callersInput) routing to Service.PreciseCallers (gopls). Tool
  description + docs/mcp.md updated. New gopls-gated MCP test connects a real in-memory client,
  calls codemap_callers precise=true on a method, asserts exact caller (skips in CI; isolates
  only CODEMAP_DATA so gopls uses the real cache). All green, fmt/vet clean. COMMIT+PUSH.
  **NEXT:** callees --lsp (needs callHierarchy/outgoingCalls on the client); or wire precise
  edges into the indexer; else polish. The three pillars (graphs/vectors/LSP) all deliver
  user- AND agent-visible value now.
- 2026-06-23 #30 (cron) — Precise CALLEES (symmetry with callers). lsp.Client.OutgoingCalls
  (callHierarchy/outgoingCalls) + CallHierarchyOutgoingCall. Refactored PreciseCallers into a
  shared preciseCallHierarchy(incoming bool) + added PreciseCallees + itemToRef helper. CLI
  `callees --lsp`; MCP codemap_callees `precise` (unified callers/callees on symbolQueryInput,
  dropped symbolInput). gopls-gated PreciseCallees test. DEMO: `callees IndexProject --lsp` →
  WipeProject/Stats/walk/indexFile/resolveEdges/DeleteByProject (precise, cross-package +
  stdlib). docs updated. All green, fmt/vet clean. COMMIT+PUSH. Precise call navigation pair
  complete on CLI + MCP. **NEXT:** wire precise edges into the indexer, or studio precise
  toggle, or polish; v0.1 is feature-complete.
- 2026-06-23 #31 (cron, polish-first) — README now showcases the precise LSP differentiator
  (the front door undersold it): replaced the "Precise + broad" bullet with "Precise
  navigation (LSP)" (callers/callees --lsp; the 7-vs-50 Close example), added a `--lsp` line to
  Quick start, noted it in the Commands table and the MCP tools paragraph (precise: true).
  Docs-only; go build + VitePress build both green. COMMIT+PUSH. **NEXT:** studio precise
  toggle (needs node-specific resolution by file+line, not just name, to disambiguate); or
  wire precise edges into the indexer; else polish.
- 2026-06-23 #32 (cron, polish-first) — ★ LSP now in the studio TUI. Refactored precise queries
  into preciseRelations(hintFile,hintLine) (one gopls session, both directions, node-specific
  resolution by file:line to disambiguate same-named hubs) + PreciseRelationsAt + nonNil helper;
  PreciseCallers/Callees delegate (existing gopls tests still pass). studio Graph: `p` recomputes
  the selected hub's callers/callees precisely via gopls; header/sections show "precise · gopls";
  nav reverts to fast by-name. 1 TUI test (toggle + apply + revert). studio.yml extended (go.mod
  added so gopls resolves; press p; wait "via gopls"; snapshot graph_precise shows the badge).
  All units + 5 E2E green, fmt/vet clean. COMMIT+PUSH. studio now demonstrates all three pillars
  (graphs/vectors/LSP). **NEXT:** wire precise edges into the indexer (make default graph
  accurate), or polish; v0.1 feature-complete.
- 2026-06-23 #33 (cron, polish-first) — Added `symbols` (a SPEC tool listed as planned):
  Service.Symbols (NodesInFile, project-relative path resolution, excludes the file node) + CLI
  `symbols <file>` + MCP codemap_symbols → **11 MCP tools, 14 CLI commands**. Lists a file's
  functions/types/methods with FQN + line ranges — the "narrow fact instead of a file read"
  value. Test + demo (symbols internal/embed/provider.go → Provider/EmbeddingProfile/…). Docs
  (cli/mcp/README/AGENTS) updated. All green, fmt/vet clean. COMMIT+PUSH. **NEXT:** references/
  dependencies tools, precise edges in the indexer, or polish.
- 2026-06-23 #34 (cron, polish-first) — studio Search now works WITHOUT Ollama (was dead on a
  structure-only index). graph.SearchSymbols (case-insensitive symbol/fqn LIKE) +
  Service.FindSymbols (Mode "name") + Service.Search (semantic, falls back to name on
  error OR zero hits) + SemanticReport.Mode. studio Search uses Search, shows "<mode> mode"
  badge. Tests (SearchSymbols, Search name fallback) + studio.yml exercises it (snapshot shows
  "name mode" + app.Run). All units + 5 E2E green, fmt/vet clean. COMMIT+PUSH. NOTE: studio.yml
  now needs gopls for `task flows` (precise step) — local-only (CI doesn't run flows).
  **NEXT:** CLI/MCP `find` (name search for agents w/o Ollama), references/dependencies, or
  precise edges in indexer.
- 2026-06-23 #35 (cron, polish-first) — Exposed offline name search to CLI + agents: `find
  <query>` command + `codemap_find` MCP tool (both → Service.FindSymbols). Agents/people
  without Ollama can now search (codemap_semantic hard-requires it). → **12 MCP tools, 15 CLI
  commands.** Demo: `find Precise` → PreciseCallers/Callees/RelationsAt etc. (offline). Docs
  (README/AGENTS/cli/mcp) updated. All units + 5 E2E green, fmt/vet clean. COMMIT+PUSH.
  **NEXT:** references/dependencies tools, precise edges in the indexer, or polish.
- 2026-06-23 #36 (cron, polish-first) — **Default-graph accuracy (no type-checker, no gopls):**
  an unqualified call `Foo()` in Go ALWAYS resolves within the caller's package, so the indexer
  now resolves those precisely (same-directory target only) instead of linking to every
  same-named symbol across packages. Selector calls `x.Foo()`/`pkg.Foo()` stay by-name (can't
  resolve without types). Added `extract.Reference.Qualified` (set by gosrc via `isQualifiedCall`,
  unwrapping generic IndexExpr); indexer `resolveEdges` filters unqualified refs to `samePackage`
  and weights them `WeightLSP` (precise), falling back to all matches only if same-package is
  empty. New test `TestIndexUnqualifiedCallSamePackage` proves two same-named `Helper`s in
  different packages no longer cross-link (Run→pkga.Helper only). Dogfood on codemap itself:
  new `samePackage` helper resolves precisely; callers/callees consistent. This shrinks
  by-name inflation for the common intra-package case without the cost/dependency of indexing
  with gopls. All units + fmt/vet clean. COMMIT+PUSH.
  **NEXT:** references/dependencies tools, full precise (gopls) edges in the indexer, or polish.
- 2026-06-23 #37 (cron, polish-first) — **studio Graph tab is now a real graph WALKER, not a
  static detail view** (directly addresses the original "graph isn't useful" complaint). The
  left pane stays a hub jump-list; `→`/`l` focuses the right pane and `enter` **re-centers** the
  explorer on the selected caller/callee — so you can traverse "who calls X → who calls that →
  what does it call". `backspace` pops back along a breadcrumb (header shows depth), `←`/`h`
  returns focus to hubs. Decoupled the detail header/precise target from the hub selection via a
  `graphCenter{sym,fqn,file,line}` + `graphStack` (walk path); `p` precise now works on the
  centered node at any depth. New `refBlock` renderer highlights the focused ref with windowing;
  hub selection dims when the refs pane is focused (`dimSelectedStyle`). Existing `enter`→Impact
  (hub focus) preserved → E2E + TestGraphEnterDrillsToImpact intact. 3 new unit tests
  (recenter+back, caller/callee boundary index, hub-focus enter still drills); `go test -race`
  clean. Extended studio.yml E2E to walk the graph (`l`→`enter`→depth-1→`h`) + new `graph_walk`
  snapshot — PTY-driven `esc`/`backspace` proved flaky (lone ESC ambiguous over a PTY) so the
  flow uses vim `l`/`h` focus keys. Docs (README + docs/studio.md keys table) updated. COMMIT+PUSH.
  **NEXT:** references/dependencies tools, full precise (gopls) edges in the indexer, or polish.
- 2026-06-23 #38 (cron, polish-first) — **Query results now carry the symbol's `signature`** —
  free (already loaded by `scanNode`), high-value for BOTH audiences. Added `Signature` to
  `SymbolRef`, `ImpactNode`, `SemanticHit` (json `signature,omitempty`); populated everywhere the
  source is a graph node (callers/callees/symbols/orphans/path/impact locations+direct_callers+
  blast_radius+tests, and offline `find`/name search). Agents calling `callers`/`impact`/`find`
  now understand each result without a follow-up file read, and same-named symbols disambiguate by
  signature (dogfood: `find Hotspots --json` → service vs store `Hotspots` distinct). studio
  previews the selected symbol's signature at the bottom of the Graph (refs pane), Impact, and
  Search panes (`⟩ func …`) so panes are self-contained — verified in the studio E2E snapshots
  (`graph_walk` → `⟩ func Top()`, `impact` → `⟩ func Run()`). Semantic-mode hits (veclite payload)
  stay blank for now (no signature in payload) — name-mode + all graph-sourced results covered.
  3 new tests (results-carry-signature, Impact preview, Search preview); race-clean; E2E green.
  Docs (README agent section + docs/studio.md) updated. COMMIT+PUSH.
  **NEXT:** signature in semantic-mode hits (payload or graph lookup), references/dependencies
  tools, full precise (gopls) edges in the indexer.
- 2026-06-23 #39 (cron, polish-first) — **Completed #38: semantic-mode hits now carry signatures
  too** (they come from the veclite payload, which has no signature). Resolved from the graph via
  a new `graph.SignatureIndex(projectID) map[FQN]signature` (one query, keyed by FQN, excludes
  blank sigs) — works on EXISTING indexes, no reindex. `Semantic()` now opens the graph, builds the
  index once when there are hits, and fills `hit.Signature` by FQN. Live dogfood (Ollama up):
  `semantic "shortest call path" --json` → `app.Service.Path` / `graph.Store.Path` distinguished by
  full signature (previously `(none)`). studio Search preview is now consistent across semantic AND
  name mode. 2 new tests (graph SignatureIndex incl. blank-exclusion; app semantic-hit signature via
  fakeEmbedder); full suite + studio E2E green; fmt/vet clean. (Caught a stale-`bin/` dogfood: rebuilt
  the binary before testing.) COMMIT+PUSH.
  **NEXT:** references/dependencies MCP tools, full precise (gopls) edges in the indexer, or polish.
- 2026-06-23 #40 (cron, polish-first) — **CLI text output now shows signatures** (data already in
  results since #38; the bare-name text was the last surface omitting it). `symbols <file>` is now a
  real file outline — `kind · line · full signature` (e.g. `method  141  func (s *Store) Callers(
  projectID int64, symbol string) ([]Node, error)`) — fulfilling its "structured alternative to
  reading the file" promise; `find` shows each match's signature inline so same-named symbols
  disambiguate (dogfood: two `Hotspots` = service vs store). New `sigOrName()` helper; callers/
  callees keep their relationship-focused name+location format. Extended the `query` E2E to run
  `symbols a.go` + `find Run` with a new `signatures_in_outline` outcome (asserts `func Run()`
  renders) — re-stamped; all 4 outcomes pass. Docs/cli.md rows updated. Full suite + all 5 E2E
  green; fmt/vet clean. COMMIT+PUSH.
  **NEXT:** references/dependencies MCP tools, full precise (gopls) edges in the indexer, or polish.
- 2026-06-23 #41 (cron, polish-first) — **studio Metrics is now a full-width overview dashboard**
  (directly addresses the user's repeated "take better usage of the screen" — it was a single
  left-aligned column with the right half empty). Header (counts) spans the top; below it two
  columns split by a divider: LEFT = By-kind + By-language bar charts; RIGHT = the call graph's two
  extremes — **Top hubs** (most-referenced/load-bearing) and **Dead-code candidates** (no callers,
  via `Service.Orphans`). Loaded orphans into the model (`orphansCmd`/`orphansMsg`, refreshed on
  `ctrl+r`). Fixed a column-wrap bug (bar line = indent+label+bar+count overflowed the left column,
  spilling the count to its own line) by budgeting `barW = leftW-22`. New test
  (`TestMetricsDashboardShowsHubsAndDeadCode`, width-bounded); race-clean; studio E2E green (metrics
  snapshot shows the two-column layout). Docs (README + docs/studio.md) updated. COMMIT+PUSH.
  **NEXT:** references/dependencies MCP tools, full precise (gopls) edges in the indexer, or polish.
- 2026-06-23 #42 (cron, polish-first) — **Surfaced docstrings** (the companion to signatures:
  shape + purpose = understand a symbol without reading it; 46% of codemap's own symbols carry one).
  Added `Doc` to `SymbolRef`/`ImpactNode`/`SemanticHit` (json `doc,omitempty`), populated from
  `graph.Node.Docstring` everywhere it's free (callers/callees/symbols/orphans/path/impact/find).
  Generalized `graph.SignatureIndex`→`SymbolInfoIndex` returning `{Signature, Doc}` per FQN (one
  query) so semantic hits get both. studio previews now show signature + the docstring's first line
  (`detailPreview`/`docFirstLine`) in the Graph refs, Impact, and Search panes; reserved one more
  line per pane. Tests: graph `TestSymbolInfoIndex`; app asserts Doc through callers/impact/find +
  semantic; tui asserts the doc first-line renders (and only the first line). Docs (README +
  docs/studio.md) updated. Dogfood: `find Hotspots --json` → each result's `doc` populated
  ("Hotspots returns the most-referenced nodes (hubs)."). Edits were authored during a Bash-tool
  outage and validated/shipped on a later firing: full suite + race + both E2E green, fmt/vet
  clean, CI success (`d043ecd`). COMMIT+PUSH done.
  **NEXT:** references/dependencies MCP tools, full precise (gopls) edges in the indexer, or polish.
- 2026-06-23 #43 (cron, polish-first) — **Impact tab now self-contained + refreshed stale README/docs
  studio example.** (1) The Impact "defined" header showed only a bare name+location; it now renders
  the analyzed symbol's **signature** on its own line plus the **docstring** first line (free from
  `rep.Locations`, which are SymbolRefs carrying both since #38/#42) — so you see what you're
  analyzing, matching the Graph/Search previews. New `firstDoc` helper; `TestImpactHeaderShowsAnalyzed
  Symbol`. (2) The README + docs/studio.md ASCII art was stale (bare `Close`, fabricated counts, no
  sig preview). Captured a REAL studio view of codemap-on-itself via a throwaway glyph spec and used
  the faithful output: FQN hub list (six distinct `Close` methods → shows disambiguation),
  `Called by (57)`/`Calls (9)`, the `▸` selection, and the `⟩ func runInit(...)` signature preview +
  focus-aware footer. Full suite + studio E2E green (impact snapshot shows `func Helper()` under the
  defined symbol); fmt/vet clean. COMMIT+PUSH.
  **NEXT:** references/dependencies MCP tools, full precise (gopls) edges in the indexer, or polish.
- 2026-06-23 #44 (cron, polish-first) — **`source` command + `codemap_source` MCP tool** — the last
  piece of the symbol-understanding story (where→what→why→**the actual code**), serving the "and
  refactors" half of the north star. `Service.Source(cwd, symbol)` resolves the symbol's node(s) and
  reads the body from the indexed file at its `[StartLine,EndLine]` range (new `readLineRange`; minimal
  backend — no new extraction/storage, just a file-slice read), returning `{signature, doc, source}`
  per match. So an agent reads a definition in one call without opening the file; same-named symbols
  return all matches. CLI `source <symbol>` (text prints `// fqn file:start-end` + body, or `--json`)
  and `codemap_source`. → **13 MCP tools, 17 CLI commands.** Dogfood: `source readLineRange` returned
  the function itself; `source samePackage --json` → source field populated. Tests: app
  `TestServiceSource`; mcp presence check extended (+find, +source). Docs (README tools list+count+
  Commands table, AGENTS set(13), docs/cli.md, docs/mcp.md) updated. Full suite + help/query/studio
  E2E green; fmt/vet clean. COMMIT+PUSH.
  **NEXT:** references/dependencies MCP tools, full precise (gopls) edges in the indexer, or polish.
- 2026-06-23 #45 (cron, polish-first) — **studio source viewer** (`s`) — makes studio a complete
  code-reading tool: navigate the graph AND read implementations without leaving the TUI, leveraging
  #44's `Service.Source`. From the Graph tab, `s` opens a full-screen scrollable overlay of the
  selected node's body (centered hub when focusHubs, selected ref when focusRefs): numbered lines,
  `↑/↓`·`pgup/pgdn`·`g/G` scroll, `esc`/`q` to close. Modal — captures keys until dismissed (handled
  before tab dispatch; ctrl+c still quits). New model state (`srcView`/`srcTitle`/`srcLines`/
  `srcScroll`) + `sourceViewCmd`/`sourceMsg`/`handleSourceKey` + `renderSource`; focus-aware footer.
  Tests: `TestSourceViewerScrollAndClose` (open via sourceMsg, scroll, number-key captured, q-close).
  Extended studio E2E with `s`→source-overlay→`q` + a `source` snapshot (real PTY path through
  Service.Source; shows `app.Top  a.go:1-1` + numbered line). Also fixed stale **CLAUDE.md** "10
  tools"→"13 tools". Docs (README + docs/studio.md keys/tab) updated. Full suite + race + all E2E
  green; fmt/vet clean. COMMIT+PUSH.
  **NEXT:** source view from Impact/Search (input-key conflict to solve), references/dependencies
  tools, full precise (gopls) edges in the indexer.
- 2026-06-23 #46 (cron, polish-first) — **Source viewer now works on every tab** (resolved #45's
  input-key conflict). Added a universal `ctrl+s` trigger — a modifier so it works on Impact/Search
  where the focused text input captures a plain `s`; bubbletea's raw mode clears IXON so ctrl+s
  arrives as a key, not flow-control. Unified the selection logic in `sourceTarget()` (Graph: selected
  ref/centered hub · Impact: selected blast node, else analyzed symbol · Search: selected hit) →
  `viewSource()`, shared by Graph `s` and global `ctrl+s`. Footer hints updated (Impact/Search show
  `ctrl+s source`). Tests: `TestSourceTargetAcrossTabs` (Search→selected hit, Impact→selected blast
  node). Extended studio E2E: `ctrl+s` from the Search tab → `source_search` snapshot (`app.Run
  a.go:1-1`) → `q`. Full suite + race + all 5 E2E green; fmt/vet clean. Docs/studio.md updated.
  COMMIT+PUSH.
  **NEXT:** references/dependencies MCP tools, full precise (gopls) edges in the indexer, or polish.
- 2026-06-23 #47 (cron, polish-first) — **`projects` command + `codemap_projects` tool, and fixed the
  cross-project overclaim.** Audit found the README touted cross-project blast radius ("across all my
  projects") + "the graph spans every registered project", but queries are single-project AND the
  registry wasn't even discoverable (no way to list indexed projects). Added `Service.Projects()`
  (ListProjects + per-project Stats → name/path/lang/nodes/edges/files; surfaces existing registry, no
  new backend), CLI `projects` (aligned text table or `--json`) and `codemap_projects` (no-arg, new
  `emptyInput`). → **14 MCP tools, 18 CLI commands.** Dogfood: `projects` → `codemap 534 nodes 1950
  edges 35 files`. Corrected docs to match reality: README intro drops "across all my projects";
  "Cross-project" bullet → "Multi-project registry — one shared store indexes all repos; `projects`
  lists them; queries target one project (cwd/`--path`)". Tests: app `TestServiceProjects`; mcp
  presence (+projects). Docs (README count+table, AGENTS set(14), cli.md, mcp.md) updated. Full suite +
  help/query E2E green; fmt/vet clean. COMMIT+PUSH.
  **NEXT:** references/dependencies MCP tools, full precise (gopls) edges in the indexer, or polish.
- 2026-06-23 #48 (cron, polish-first) — **`semantic.yml` E2E — demonstrates the vectors pillar
  end-to-end** (the one of graphs/vectors/LSP with no flow: query.yml=graphs, studio.yml `p`=LSP,
  now semantic.yml=vectors). Indexes a 2-func project WITH embeddings (Ollama nomic-embed-text),
  then `codemap semantic "verify a signed login credential"` — wording with NO lexical overlap with
  the symbol name, so a hit proves MEANING-based retrieval (the CLI `semantic` is pure-vector, no
  name fallback). Snapshot: `app.Authenticate 0.550` ranked above `app.RenderTemplate 0.315`. All 6
  flows pass. `task flows` globs specs/*.yml so it's auto-included → flows are now local-only and
  need gopls (studio) + Ollama (semantic); noted in AGENTS.md + CLAUDE.md. No Go change (CI runs
  unit tests, not flows; green). COMMIT+PUSH.
  **NEXT:** references/dependencies MCP tools, Metrics-tab interactivity, full precise edges in indexer.
- 2026-06-23 #49 (cron, polish-first) — **Metrics tab is now navigable** (was the last read-only tab;
  serves "easy to navigate"). The right column (top hubs + dead-code candidates) is one selectable
  list spanning both: `↑/↓` select (`metricsSel`), `enter` drills the row into Impact, `ctrl+s` reads
  its source — so the overview is also a launchpad ("here's a dead-code candidate → enter to confirm 0
  callers → ctrl+s to read it → delete"). New `metricsItem`/`metricsCount`/`handleMetricsKey`;
  `sourceTarget` handles tabMetrics; `metricBlock` renders plain selectable rows with windowing +
  highlight; `metricsSel` reset on reindex; footer hint updated. Caught a `go vet` unreachable-code
  warning (default case now returns unconditionally → removed dead trailing return). Tests:
  `TestMetricsNavigationDrills` (select into orphans → sourceTarget + enter→Impact). Extended studio
  E2E: `ctrl+s` from Metrics → `metrics_source` snapshot (`app.Helper a.go:1-1`). Full suite + race +
  all 6 E2E green; fmt/vet clean. Docs (README + docs/studio.md) updated. COMMIT+PUSH.
  Follow-up: CI lint (golangci-lint v2) caught `firstN` as unused (the Metrics rewrite dropped its
  last caller) — local `go vet` doesn't check unused, so it slipped through. Removed firstN (fix
  `a049444`, CI green). Then hardened `task lint` to mirror CI: use a v2 golangci-lint binary if
  present, else `go install` v2 into bin/ (gitignored, cached) and run it; vet+gofmt only when
  offline — so lint-only issues are caught locally before pushing.
  **NEXT:** references/dependencies MCP tools, full precise (gopls) edges in the indexer, or polish.
- 2026-06-23 #50 (cron, polish-first) — **Dogfood on a large real repo + honest accuracy docs.**
  Indexed `~/projects/blueprint` (83 own files, 1941 nodes, 7144 edges) in **0.5s** — fast,
  vendored deps correctly excluded, no errors. BUT it exposed the by-name inflation starkly: top
  `hotspots` were 5 different `.String` methods all tied at in-degree **128** (every `x.String()`
  call attributed to every `String` method); `orphans` flagged interface-dispatch methods
  (`ast.File.nodeType`) as dead code. Root cause: name-based resolution has no receiver type → the
  proper fix is graph-wide precise resolution (pure-Go `go/types`), a backend epic the directive
  steers away from rushing; risky heuristics (same-package for methods) mis-attribute. So the SAFE,
  directive-aligned move = **be honest**: added an "Accuracy: name-based graph vs precise (LSP)"
  section to README + accuracy notes to AGENTS (agent guidance) and docs/cli.md, covering
  hotspots/impact over-counting and orphans-as-candidates, pointing to `--lsp` and the planned
  go/types fix. Docs-only (CI green trivially). **TOP NEXT priority: pure-Go `go/types` call
  resolution** to make the default graph accurate (replaces by-name method edges with precise ones;
  falls back to by-name where a package doesn't type-check) — the clear highest-value backend item.
- 2026-06-23 #51 (cron, polish-first) — **studio list page-navigation** (validated by #50's dogfood:
  blueprint's Graph hub list had ~200 entries, navigable only one row at a time). Added `pgup`/`pgdn`
  to every selection list (Graph hubs + refs, Metrics, Search, Impact) jumping by a screenful
  (`pageStep` ≈ height), plus `home`/`end` on the non-input tabs (Graph/Metrics; skipped on
  Search/Impact where the text input owns those keys). Refactored hub up/down through a shared
  `selectHub`/`clampIdx`/`blastLen`; behavior preserved. Tests: `TestGraphHubPageNavigation` (page
  jump + clamp + home), `TestSearchPageNavigation` (confirms the "pgup"/"pgdown" key strings match).
  Ran the improved `task lint` (golangci-lint v2) → 0 issues; full suite + race + studio E2E green;
  fmt clean. Docs/studio.md keys updated. COMMIT+PUSH.
  **NEXT:** pure-Go `go/types` call resolution (top accuracy item), references/dependencies tools.
- 2026-06-23 #52 (cron, polish-first) — **Rewrote the MCP `instructions`** (the agent's first-contact
  playbook — half the value prop is "used via agents"). The old text mentioned only 3 of 14 tools,
  omitted the flagship `codemap_impact`, and gave agents no accuracy guidance. New version: index-once
  workflow; grouped tool guide (find: semantic/find · understand: impact/callers/callees/source/
  symbols · survey: hotspots/orphans/status/projects); notes results carry signature+docstring; and
  — crucially — the **accuracy model** so an agent knows to pass `precise:true` for exact Go callers
  and to treat hotspots/orphans as name-based. Guarded by `TestInstructionsCoverKeyCapabilities`
  (asserts impact/source/projects/`precise:true`/`name-based` stay mentioned). Full suite + lint v2
  (0 issues) + fmt clean. COMMIT+PUSH.
  **NEXT:** pure-Go `go/types` call resolution (top accuracy item), references/dependencies tools.
- 2026-06-23 #53 (cron, polish-first) — **Honest about Go-only v0.1 (docs + helpful warning).** Found a
  real gap: the default indexer registers ONLY `gosrc`, so non-Go files are silently skipped, yet the
  README/docs implied multi-language ("LSP servers for the languages you index"). A TS/Python user
  would `index` → 0 nodes, no explanation. Fixes: (1) indexer `walk` now counts recognized-but-
  unsupported languages (`Result.Unsupported`); `Service.Index` sets a warning when a project has no
  Go files but other recognized source — dogfood: TS+Py dir → "no Go files to index (codemap v0.1
  indexes Go); skipped 1 python, 1 typescript — support planned". (2) Corrected the overclaims:
  README (Structural-graph bullet "v0.1 indexes Go", prerequisites note + gopls-optional), AGENTS
  ("Extraction (v0.1 = Go only)" — lspsrc built but not wired), docs/quick-start.md. Tests:
  `TestIndexNonGoWarns`. Full suite + lint v2 (0) + index_status/query E2E green; fmt clean. COMMIT+PUSH.
  **NEXT:** wire the LSP backend into the indexer for TS/Python (unlocks multi-language), pure-Go
  `go/types` call resolution (accuracy), references/dependencies tools.
- 2026-06-23 #54 (cron, polish-first) — **studio surfaces the index warning on `ctrl+r`** (consistency
  follow-up to #53). The CLI warns when a project has no Go files (or embeddings are unavailable), but
  studio's in-place reindex showed only "reindexed: 0 files · 0 nodes · 0 edges" — leaving a non-Go
  (or no-Ollama) user puzzled. Now `indexedMsg` shows `⚠ <warning>` in the footer when `rep.Warning`
  is set, so the TUI matches the CLI. Test: `TestIndexedMsgShowsWarning`. Full suite + lint v2 (0) +
  studio E2E green; fmt clean. COMMIT+PUSH.
  **NEXT:** wire the LSP backend into the indexer for TS/Python, pure-Go `go/types` resolution.
- 2026-06-23 #55 (cron, polish-first) — **`impact` now lists the covering tests explicitly** (the
  flagship's headline answer — "what breaks + what do I run"). Previously tests were only inline in
  the blast radius (✓ markers); for a large radius you had to hunt for them. Added a "covering tests
  (run these):" section listing `rep.Tests` (name + file:line), plus an "affected (blast radius):"
  header so the two lists are distinct. Dogfood/E2E: `impact Helper` → "covering tests (run these):
  app.TestHelper a_test.go:1". Extended `query.yml` (added a test file to its project so coverage is
  actually exercised) with a `covering_tests_listed` outcome — all 5 pass; re-stamped. Full suite +
  lint v2 (0) + fmt clean. COMMIT+PUSH.
  **NEXT:** wire the LSP backend into the indexer for TS/Python, pure-Go `go/types` resolution.
- 2026-06-23 #56 (cron, polish-first) — **studio Impact names the covering tests** (consistency with
  #55's CLI change). The TUI showed tests only as ✓ markers in the blast radius; it now prints a
  compact "covered by TestX, TestY" line under the cover summary (the "what do I run" answer), with
  the blast budget reserving an extra line when tests exist. Test: `TestImpactNamesCoveringTests`.
  Full suite + lint v2 (0) + studio E2E green; fmt clean. COMMIT+PUSH.
  **NEXT:** wire the LSP backend into the indexer for TS/Python, pure-Go `go/types` resolution.
- 2026-06-23 #57 (cron, polish-first) — **`path` now has E2E coverage** (it was the one graph command
  with no end-to-end demonstration). The `query` spec's project already forms a Top→Run→Helper call
  chain, so added `codemap path Top Helper` + a `call_path_traced` outcome asserting the rendered
  chain `Top → Run → Helper`. Demonstrates the shortest-call-path graph capability end-to-end and
  guards its output. 6/6 query outcomes pass; re-stamped. Spec-only (no Go change; CI green). COMMIT+PUSH.
  **NEXT:** wire the LSP backend into the indexer for TS/Python, pure-Go `go/types` resolution.
- 2026-06-23 #58 (cron, polish-first) — **source viewer scroll-position indicator.** Reading a long
  function in the `s`/`ctrl+s` overlay gave no sense of position; the title now shows `(lines A–B of
  N)` when the source exceeds the viewport. Computed from `srcScroll`+viewport; hidden when it all
  fits. (Verified `version` already shows commit+date — no change needed.) Test extended
  (`TestSourceViewerScrollAndClose` asserts the indicator). Full suite + lint v2 (0) + studio E2E
  green; fmt clean. COMMIT+PUSH.
  **NEXT:** the safe-polish surface is essentially exhausted; the next real usefulness jump is the
  deferred backend — pure-Go `go/types` call resolution (accuracy) and LSP-backend wiring (multi-
  language). Both are deliberate epics warranting a focused go-ahead, not 5-min loop increments.
- 2026-06-23 #59 (cron, polish-first) — **studio `?` help overlay.** The TUI has many keys (tabs,
  walk, source, precise, page-nav, drill) but no discoverability surface beyond context footers. `?`
  now toggles a full-screen keybinding overlay (Global / Graph / Metrics·Impact·Search), reachable on
  any tab — `?` is captured globally (searching for "?" isn't meaningful, so it's safe even with a
  text input focused) and footers gained a "· ? help" hint. Tests: `TestHelpOverlay` (opens, documents
  keys, closes, and does NOT leak into the search input). Studio E2E extended (`?`→help snapshot→`?`).
  Full suite + lint v2 (0) + studio E2E (43 steps) green; fmt clean. COMMIT+PUSH.
  **NEXT:** deferred backend epics (go/types accuracy, LSP multi-language) — warrant a focused go-ahead.
- 2026-06-23 #60 (cron, polish-first) — **README: concrete `impact` example (show, don't tell).** The
  README had a studio ASCII but no CLI output, while the flagship is `impact`. Added a real, trimmed
  `codemap impact Stats --depth 2` block (captured from codemap on itself): defined site, 4 callers,
  18 blast, 9 covering tests (3 listed + "… 6 more"), and the affected list with the ✓ test marker —
  demonstrating "what breaks + what do I run" in one call. Docs-only (CI green trivially); fmt clean.
  **NEXT:** deferred backend epics (go/types accuracy, LSP multi-language) — warrant a focused go-ahead.
- 2026-06-23 #61 (USER ASK) — **`docs` command + `codemap_docs` MCP tool** — so agents/harnesses can
  learn the tool (user requested this directly). New `internal/app/docs.go` holds a concise,
  topic-addressable agent guide (overview · workflow · commands · accuracy · ecosystem), reflecting
  the real commands, the index→understand→read loop, the name-based-vs-precise accuracy model, and
  how codemap fits the local toolchain (vecgrep/vidtrace/fcheap/noted). `codemap docs [topic]` (CLI)
  and `codemap_docs` (MCP, optional topic); MCP `instructions` now point a harness at codemap_docs.
  → **15 MCP tools, 19 CLI commands.** Tests: app `TestDocs`, mcp presence (+docs). Docs (README
  count+table, AGENTS set(15), cli.md, mcp.md) updated. Full suite + lint v2 (0) + help E2E green. PUSH.

## User vision (2026-06-23) — agent harness on top of the toolchain
The user wants codemap (with vecgrep/vidtrace/fcheap/noted, all XDG-stored) to power an agent
harness that analyzes & fixes codebases. Concrete asks, in priority order:
1. [DONE #61] `docs` command + `codemap_docs` so a harness can learn the tool.
2. **Annotations/notes (the core new capability):** tag graph nodes AND call paths with (a) external
   data fetched from a DB (mongosh/postgres/etc.) and (b) free-form exploration notes; then query and
   analyze them. → NEW: an annotations store keyed by node/FQN and by path, CRUD + list + attached to
   query results, on CLI + MCP. Pure-Go, SQLite alongside the graph.
3. Ecosystem flow: vidtrace (repro) → vecgrep (semantic) → codemap (structure/impact) → fcheap
   (persist/search artifacts). codemap's role: structural understanding + the annotation layer.
**NEXT:** design + build the annotations/notes layer (#2) — the core of the harness vision.
- 2026-06-23 #62 (USER ASK — annotation layer) — **Annotations: pin notes + external data to nodes AND
  paths** (user chose "node+path together", opaque-JSON data). New graph `annotations` table (schema
  v2, IF NOT EXISTS + version bump → existing DBs migrate; FK to projects so it SURVIVES reindex,
  which only wipes nodes/edges). Store CRUD (`AddAnnotation`/`AnnotationsByTarget`/`AllAnnotations`/
  `DeleteAnnotation`); service `AnnotateNode`/`AnnotatePath` (auto-registers project so you can
  annotate pre-index) + `NodeAnnotations`/`PathAnnotations`/`AllAnnotations`/`RemoveAnnotation`;
  CLI `annotate <sym>|<from> <to> --source/--note/--data` + `annotations [..] [--rm id]`; MCP
  `codemap_annotate`/`codemap_annotations`. `data` stored opaquely (JSON or anything). New docs.go
  "annotations" topic. → **17 MCP tools, 21 CLI commands.** Tests: graph `TestAnnotations`, app
  `TestServiceAnnotations` (incl. reindex-survival + remove), mcp presence. Query E2E gains an
  annotate→annotations round-trip (7/7). Docs (README features+tools+table, AGENTS, cli.md, mcp.md,
  agent guide) updated. Full suite + lint v2 (0) + help/query E2E green; fmt clean. COMMIT+PUSH.
  **NEXT (annotation follow-ups):** surface annotations inline in impact/callers + studio (Graph/Impact
  panes); then the broader harness wiring (vidtrace/vecgrep/fcheap orchestration is harness-side).
- 2026-06-23 #63 (USER ASK follow-up) — **impact surfaces pinned annotations inline.** `ImpactReport`
  gains `Annotations` (json omitempty); `Impact()` gathers node-annotations matching the query name OR
  any resolved definition FQN/symbol (new `nodeAnnotationsFor` dedup helper) — so pinning by FQN and
  querying by short name still surfaces (dogfood: annotate `x.B`, `impact B` shows it). CLI prints an
  "annotations:" section; studio Impact pane shows `⟐ <source>: <note> <data>` lines (budget reserves
  per line). Tests: app `TestImpactSurfacesAnnotations` (FQN-match), tui `TestImpactPaneShowsAnnotations`.
  Full suite + lint v2 (0) + E2E green; fmt clean. COMMIT+PUSH.
  **NEXT:** same inline surfacing for `source`/`callers`, then studio Graph-pane annotation indicator.
- 2026-06-23 #64 (USER ASK follow-up) — **`source`, `callers`, `callees` also surface annotations
  inline.** `RelationReport` + `SourceReport` gain `Annotations`; populated via a shared
  `symbolAnnotations(g, pid, symbol)` (resolves the symbol's FQNs so FQN-pinned notes match a
  short-name query). CLI: shared `renderAnnotations` helper (refactored impact to use it too) prints
  an "annotations:" section after callers/callees/source; JSON carries it for agents. Dogfood:
  `callers B` and `source B` show the pinned note. Tests: app `TestSourceAndCallersSurfaceAnnotations`.
  Agent guide notes the inline surfacing. (Precise/`--lsp` callers don't surface yet — minor.) Full
  suite + lint v2 (0) + E2E green; fmt clean. COMMIT+PUSH.
  **NEXT:** studio Graph/Search panes annotation indicator; precise-path annotations; then harness wiring.
- 2026-06-23 RELEASE+OPS — **v0.1.0 tagged & published** (pre-release audit GO). `brew install
  abdul-hamid-achik/tap/codemap` → /opt/homebrew/bin/codemap 0.1.0 (release installs cleanly).
  Then **registered codemap's MCP across the user's agents** (per user ask): Claude Code (✔ Connected),
  Codex, Copilot CLI, Hermes, Forge via first-party CLIs; coder (~/.config/agents), opencode, crush,
  zed via validated JSON/JSONC merges (each backed up). Functional smoke test: `codemap serve` exposes
  all 17 tools over newline-delimited JSON-RPC.
- 2026-06-23 #65 (docs) — **Documented agent registration.** Expanded docs/mcp.md + README "Use it from
  an agent" with the validated one-liners (`claude/codex/copilot mcp add codemap -- codemap serve`) +
  the generic stdio config (noting the `mcpServers`/`mcp`/`context_servers` key varies), and a pointer
  that agents can call `codemap_docs` to self-onboard. Captures the MCP-registration work as reusable
  docs. Docs-only (CI green trivially). COMMIT+PUSH.
  **NEXT:** studio Graph-pane annotation indicator (the deferred product polish).
- 2026-06-23 #66 (annotation follow-up) — **studio Graph pane surfaces the centered node's
  annotations.** Completes "annotations show up wherever you look" in the TUI (Impact had it since
  #63; Graph is the default/most-used tab). The header shows an at-a-glance `· ⟐ N` count, and the
  detail pane lists up to 3 annotation lines (`⟐ source: note  data`, "+N more" if truncated) under
  the callers/calls — reserving budget so the ref lists still fit. Free of extra queries: `detailCmd`
  reuses `Callers().Annotations` (populated since #64). New `graphAnnotations` model field +
  `graphDetailMsg.annotations`; preciseDetailMsg leaves it intact (same center). Test
  `TestGraphPaneShowsAnnotations`. Full suite + lint v2 (0) + race + studio E2E green; fmt clean.
  COMMIT+PUSH.
  **NEXT:** precise/`--lsp`-path annotation surfacing; Search-pane indicator; then harness wiring.
- 2026-06-23 #67 (annotation follow-up) — **precise/`--lsp` path now surfaces annotations too.**
  `PreciseCallers`/`PreciseCallees` (CLI `--lsp` + MCP `precise:true`) populated their RelationReport
  without annotations — inconsistent with the by-name path. Added `symbolAnnotationsByName(name,
  symbol)` (resolves pid from the project name the precise path carries) and wired it into both
  precise reports; the CLI already renders `rep.Annotations`. Dogfood: `callers B --lsp` shows the
  exact gopls caller AND the pinned note. Extended `TestPreciseCallersGopls` to assert it. Full suite
  (gopls present) + lint v2 (0) green; fmt clean. COMMIT+PUSH.
  **Annotation layer is now complete on every surface** (CLI by-name + precise, MCP, studio
  Graph/Impact, callers/callees/source). **NEXT:** Search-pane per-hit indicator (lower value — needs
  per-hit lookup); broader harness wiring is harness-side.
- 2026-06-23 #68 (annotation follow-up) — **Search surfaces annotations too** (last surface). Added
  `Annotations` to `SemanticHit`, enriched in one bulk query (`enrichHitAnnotations`: AllAnnotations →
  target map → attach by FQN/symbol) for both `Semantic`/`Search` and `FindSymbols` — so agents get
  annotations inline in `codemap_semantic`/`codemap_find` JSON. studio Search marks annotated rows with
  `⟐`; CLI `find`/`semantic` text get the same marker (`annMark`). Tests: app `TestFindSurfaces
  Annotations`, tui `TestSearchPaneMarksAnnotated`. Full suite + lint v2 (0) + studio/query/semantic
  E2E green; fmt clean. COMMIT+PUSH. **Annotation layer now complete on ALL surfaces + all studio tabs.**

## 🎯 Epic — multi-language support (LSP backend) — GREENLIT 2026-06-23
User greenlit TypeScript/JavaScript, Python, Docker, HTML, CSS, Vue. Designed via a 16-agent workflow
(map → research → judge → synthesize → adversarial review). **Chosen approach (registry-plus-structure,
45/50):** extend the existing language-agnostic `internal/extract/lspsrc` into a generic LSP-driven
extractor fed by a tiny server registry (lang, langID, binary, args), LookPath-guarded, registered next
to gosrc inside IndexProject. The Go path (gosrc+typesrc+Pass 1/2/3) is byte-for-byte untouched; queries
are backend-blind (consume generic extract.Symbol/Reference) so callers/impact/hotspots/path/semantic
work on the new nodes with ZERO query changes. Pure-Go / CGO_ENABLED=0 (the server is a spawned
subprocess, like gopls; no tree-sitter, which would need CGO and stays gated). **Honest scoping:** TS/JS
+ Python are call-graph-capable (ride existing queries); Docker/HTML/CSS/Vue are STRUCTURE-ONLY
(reference/import edges, resolve by path not name — need a new Pass-2b, sequenced LAST). Slices: **A**
foundation (below) · **B** TS symbols-only via typescript-language-server (nodes+defines+embeddings ⇒
structure browsing + semantic search for TS) · **C** TS call edges (callHierarchy, written direct as
ProvPrecise bypassing Pass-2's name fan-out) · then JS/Python rows · then markup structure layer.
Adversarial reviews flagged two slice-1 fixes (both incorporated in A): the spawned server was never
`Wait()`'d (zombie) and the "install the server" message is gated on FilesScanned==0 (slice B fix).
- 2026-06-24 #135 (BUG — incremental reindex now prunes deleted files; no more ghost symbols) —
  dogfounded a real correctness bug: after deleting `b.go` and `codemap index` (incremental), `find
  Gamma` STILL returned `Gamma` from the deleted file, and the graph stayed at 5 nodes — **deleted
  files left ghost symbols in find/callers/impact/search forever** (only a full `--reindex` cleaned
  them). The walk only sees on-disk files, so deletions were never detected; nothing even enumerated
  the indexed-file set. Fix: new `graph.IndexedFiles`/`DeleteFileHash`; `Indexer.pruneDeleted` (run on
  every incremental index) lists previously-indexed files and, for each that **`os.Stat` reports gone**,
  deletes its nodes (edges cascade), vectors, and index_state row. Crucially it checks the *disk*, not
  the walk result — so a file still on disk but currently *unsupported* (server uninstalled, or
  `--no-lsp`) is **kept, never wiped**; only genuinely-deleted files are pruned. New `Result.FilesDeleted`
  → CLI "N removed" + JSON. Verified live: delete b.go → "1 removed", graph 5→3 nodes, `find Gamma`
  empty, `callers Beta` (a.go, on disk) still works. Test `TestIndexPrunesDeletedFiles` (gone → pruned;
  on-disk → kept). Full suite + lint(0) + fmt + `-race` + query/precise E2E green. This was a genuine
  trust bug (stale results after editing a repo) — exactly what dogfooding is for. COMMIT+PUSH.
- 2026-06-24 #134 (honesty — recognize markup as "planned"; fix stale advisory) — the Vue dead-end
  (#133) surfaced two real gaps. (1) **Silent skips**: `.lua`/`.rb` were recognized-but-unsupported (so
  they're reported as "planned"), but `.vue`/`.html`/`.css` were unmapped → *silently* ignored, so a Vue
  user got no signal at all. Mapped them in `LanguageForPath` (`.vue`→vue, `.html`/`.htm`→html,
  `.css`→css) — no extractor/ServerSpec, so zero Volar risk; they just flow into `Result.Unsupported`
  and get reported. (2) **Stale message**: the index advisory still said "codemap indexes Go (and
  TypeScript via typescript-language-server)" — written before JS/Python — fixed to "Go, TypeScript,
  JavaScript, and Python; more languages planned (run 'codemap doctor' …)". Verified live: a pure
  Vue/CSS project now prints "skipped 1 css, 1 vue — codemap indexes Go, TypeScript, JavaScript, and
  Python; more planned" instead of silence. Tests: new `TestLanguageForPath` (incl. markup + unknown),
  `TestIndexAdvisory` still green. Full suite + lint(0) + fmt. So while Vue's *call graph* isn't feasible
  (#133), a Vue user is now at least told codemap sees the files and support is planned. COMMIT+PUSH.
- 2026-06-24 #133 (investigation — Vue via Volar is a DEAD END for the generic LSP driver) —
  followed up the user's Vue request with two definitive raw probes (driving `vue-language-server`
  3.2.5 directly via `lsp.Client`/`conn.Call` with custom `initialize` params). Found a real tsdk:
  `…/nodejs/.../node_modules/typescript/lib` (has `tsserverlibrary.js`). Result: **even with
  `initializationOptions.typescript.tsdk` set — and again with `vue.hybridMode:false` — Volar's first
  `documentSymbol` never responds (20–25s timeout)**. So the blocker is NOT just missing init options
  (#123's hypothesis): Volar 3.x is architecturally incompatible with codemap's generic one-server,
  documentSymbol/callHierarchy driver. In hybrid mode it expects TS features to come from a *separate*
  `typescript-language-server` loaded with `@vue/typescript-plugin` (a different integration entirely);
  in full mode it still doesn't answer a plain documentSymbol here. **Conclusion: drop the
  "vue-language-server as a ServerSpec row" approach.** The realistic path to Vue is parsing the SFC
  ourselves — extract the `<script>` block and feed it to the existing TS pipeline, i.e. the deferred
  **tree-sitter / structure-only markup** layer (Pass-2b), which is genuine new backend. `.vue` stays
  unmapped (safely skipped) until then. No code shipped — `.vue` was never wired, so nothing to revert;
  the #124 timeout already guarantees no hang regardless. This closes the Volar avenue with evidence.
- 2026-06-24 #132 (docs — scannable languages table in the README) — the first question a visitor
  asks ("does it support my language?") was buried in a dense prose blockquote. Replaced it with a
  4-column table (Language · How · Extensions · Call graph) covering Go, TypeScript/JavaScript (one
  tsserver, JSX/TSX-aware, cross-`.ts↔.js`), and Python (pyright) — making it instantly clear what's
  supported, which server, and that a call graph needs `--precise` for the LSP languages. Cross-links
  `codemap doctor`; notes markup as planned. Docs-only; no behavior change. COMMIT+PUSH.
- 2026-06-24 #131 (studio — reindex reports skipped files, like the CLI) — closing the #125 honesty
  loop on the #1 surface: the studio `ctrl+r` reindex status said "reindexed: N files · N nodes · N
  edges" even when files were **skipped** (e.g. a language server timed out), silently hiding it — while
  the CLI shows the skipped count. The `indexedMsg` handler now appends "· N skipped" when
  `FilesSkipped > 0` (a missing-server Warning still takes precedence, as before). Test
  `TestIndexedMsgReportsSkipped`. Full suite + tui lint(0) + fmt green. COMMIT+PUSH.
- 2026-06-24 #130 (agent parity — `codemap_doctor` MCP tool) — `doctor` (#121) was CLI-only, but
  "why isn't my Python/TS being indexed?" / "why is semantic search empty?" is exactly what an agent
  needs to diagnose, and the structured `DoctorReport` is ideal for it. Exposed the existing
  `Service.Doctor(ctx)` as the **19th MCP tool** `codemap_doctor` (empty input — it inspects the
  environment, not a project): `handleDoctor` → the report's data dir + toolchain/language-server/
  embeddings checks, each with present/missing + install hint, as JSON. No new backend (parity, like
  #117's unannotate). Test `TestMCPDoctor` (in-memory transport: report lists data_dir, go toolchain,
  pyright-langserver, embeddings — fast, Ollama probe fails-closed in CI) + added to the tools/list
  presence check. Swept "18 tools" → "19" + listed `codemap_doctor` in README/AGENTS/CLAUDE. Full suite
  + mcp lint(0) + fmt green. An agent can now self-diagnose a broken setup over MCP. COMMIT+PUSH.
- 2026-06-24 #129 (accuracy — precise framing was TS-only; now names all LSP languages) — the
  precise-engine docs (#112) predated JS/Python/JSX, so several surfaces still said "callHierarchy for
  **TypeScript**" and "TypeScript has no name-based call edges" — leaving an **agent on a JS/Python
  project unaware that `precise:true` gives it a call graph** (the agent-facing twin of #128). Swept to
  "the LSP languages (TypeScript, JavaScript, Python)": MCP Instructions/accuracy preamble + the
  `codemap_index` precise jsonschema param (the two an agent reads), the in-CLI `codemap docs accuracy`
  (verified it renders), AGENTS.md accuracy model, and the README "Precise call resolution" bullet.
  Strengthened `TestInstructionsCoverKeyCapabilities` to require "JavaScript" + "Python" (not just
  "TypeScript") so the playbook can't regress to one language. No behavior change; full suite + lint(0)
  + fmt green. Every precise/engine surface now tells the truth for all four languages. COMMIT+PUSH.
- 2026-06-24 #128 (onboarding — `index` tips TS/JS/Python users toward `--precise`) — dogfooding the
  new-user path for the LSP languages exposed a real dead-end: a TS/JS/Python project indexed with a
  plain `codemap index` gets structure but **no call graph** (those need `--precise`), and the
  `--precise` tip was gated on `Languages["go"]>0` — so a TS user saw no hint, then `callers validate`
  → "none" even though `handle` calls it, and would reasonably think codemap is broken. Fix: the tip is
  now language-aware (`preciseTips(languages, goAvailable)`): Go → "name-based; add --precise to resolve
  exactly"; the LSP languages → "no call graph for typescript/javascript/python yet — add --precise for
  callers/impact/hotspots/path"; both shown for a mixed repo. A language only appears once its files
  were indexed (⇒ its server was present), so the tip is always actionable. Verified live (TS project
  now shows the tip; Go repo unchanged). Test `TestPreciseTips` (go-only, go-without-toolchain, ts-only,
  mixed, empty). `precise.yml` still green (its `contains: "add --precise"` holds — the Go wording still
  includes it). Full suite + lint(0) + fmt green. COMMIT+PUSH.
- 2026-06-24 #127 (discoverability — `codemap search` alias for `semantic`) — small CLI/studio
  consistency fix: the studio's 4th tab is "Search" (and the universal mental model is "search"), but
  the CLI command is `semantic`, so `codemap search "…"` returned "unknown command". Added `search` as
  a cobra alias on `semanticCmd`. Verified live (`codemap search "hotspots ranking"` works); docs
  (README + cli.md) note the alias; `TestSemanticSearchAlias` guards it. Full suite + fmt green.
  COMMIT+PUSH.
- 2026-06-24 #126 (studio — ctrl+r preserves precision instead of dropping the call graph) — a real
  in-session regression the multi-language work exposed: the studio reindex (`ctrl+r`) was hardcoded
  structure-only, so pressing it on a **`--precise` project** (any TS/JS/Python — they have *no* call
  graph without `--precise` — or a precise Go index) **discarded the exact call graph the user was
  exploring**, dropping the Graph tab to the "no call graph" hint with no way to recover from inside
  studio. Fix: `reindexCmd` now reindexes with `--precise` exactly when the project already has precise
  edges (`reindexPrecise()` = `status.PreciseEdges > 0`), so a refresh keeps the graph; name-based Go
  projects stay on the fast structure-only path. Embeddings are still skipped either way (no Ollama
  needed). Help/footer + `docs/studio.md` updated ("keeps the project's precision"). Test
  `TestReindexPreservesPrecision` (no status / name-based → structure-only; precise edges → precise).
  Full suite + tui lint(0) + fmt + studio E2E green. Polish-first, no backend change. COMMIT+PUSH.
- 2026-06-24 #125 (polish — index surfaces skipped/timed-out files clearly) — two follow-ups #124
  exposed (now that a file can be *skipped* on a server timeout): (1) **accounting bug** — an errored
  file was appended to `Result.Errors` but `FilesSkipped` was never incremented, so the summary
  "scanned X, indexed Y, skipped Z" silently didn't add up (the errored files vanished from the
  totals). Fixed: an errored file now counts as skipped, so `scanned = indexed + skipped` always holds.
  (2) **cryptic message** — a timed-out file printed `! file.ts: context deadline exceeded`. New
  `lspsrc.wrapExtractErr` turns a `context.DeadlineExceeded` into `"<lang> language server timed out on
  <file> — file skipped"` (other errors pass through). Tests: index `TestIndexErroredFileCountedAsSkipped`
  (erroring extractor injected via `ix.Register` over the "go" slot → asserts the scanned=indexed+skipped
  invariant), lspsrc `TestWrapExtractErr` (pure helper: timeout → actionable, other → unchanged). Full
  suite + lint(0) + fmt green. Closes the loop on #124 — a skipped file is now both counted and
  explained. COMMIT+PUSH.
- 2026-06-24 #124 (robustness — per-request LSP timeout; no more index hangs) — the Vue probe
  exposed a real gap independent of Vue: `codemap index` runs on `context.Background()` (no deadline),
  and lspsrc passed that unbounded context to LSP requests — so **a hung/misbehaving language server
  would freeze indexing indefinitely** (the user would have to Ctrl-C). Fix: `conn.Call`
  (`internal/lsp/jsonrpc.go`) now applies a **30s default per-request timeout when the caller sets no
  deadline** — central, so every request (documentSymbol, callHierarchy, even initialize) from any
  caller is bounded; callers with their own deadline keep it. A stalled request returns a deadline
  error → the indexer skips that file (records `Result.Errors`) and continues, instead of hanging.
  `conn.Call` already `select`ed on `ctx.Done()`, so this just supplies the missing bound. Test
  `TestRequestTimeout` (in-memory pipe + a server that never replies → bounded at a tiny test timeout in
  ~100ms, not forever). Full suite + `-race` (lsp) + lint(0) + fmt green; server-gated TS/Py tests still
  pass (30s doesn't trip on normal requests). This is robustness item (3) from #123 — done; it also
  de-risks adding any slow server (Vue, rust-analyzer) later. COMMIT+PUSH.
- 2026-06-24 #123 (investigation — Vue SFC deferred; Volar needs special init) — user asked for Vue
  SFC support. `vue-language-server` (Volar 3.2.5) IS installed, but a probe showed it **hangs** with
  codemap's generic LSP client: `New`/initialize returns, but the first `documentSymbol` never responds
  (40s context-deadline-exceeded). Cause: Volar 3.x requires `initializationOptions` carrying the
  TypeScript SDK path (`typescript.tsdk` → a dir with `tsserverlibrary.js`) and a `vue` block; without
  it Volar can't load TS and silently stalls. Our `lsp.Client.Initialize` sends no per-server
  `initializationOptions`. **Deliberately NOT shipped** — a naive row would make `index` hang on every
  `.vue` file (worst failure mode). `.vue` stays unmapped in `LanguageForPath`, so SFCs are safely
  skipped (reported unsupported), never hung. Proper Vue support is a scoped future increment: (1) add
  `initializationOptions` support to `lsp.Client.Initialize` (also unblocks rust-analyzer etc.); (2)
  discover a `tsdk` path (project `node_modules/typescript/lib`, else a bundled/global TS) — fragile and
  project-dependent; (3) a per-file request timeout so a stalling server degrades gracefully instead of
  eating the whole index budget. Recommend doing (1)+(3) first (general robustness) before (2).
- 2026-06-24 #122 (multi-lang — JSX/TSX call graph; per-file languageId) — user asked for JSX; a
  probe showed it was half-broken: a `.tsx` opened with `languageId:typescript` extracts symbols but
  **resolves no JSX call edges** (`<Button/>` usage invisible), whereas `typescriptreact` resolves them.
  Root cause: the extractor sent one fixed languageId per language, so `.tsx`/`.jsx` never got the
  *react ids tsserver needs to parse JSX. Fix: new `lspLanguageID(relPath, fallback)` refines the id by
  extension — `.tsx`→`typescriptreact`, `.jsx`→`javascriptreact`, everything else keeps the language
  default — used in `ExtractFile`'s `didOpen` (callHierarchy then rides the already-open doc). Verified
  live: `callers Button` on `App.tsx` (App renders `<Button/>`) → `App` (was empty before). Tests:
  lspsrc `TestLSPLanguageID`; index `TestIndexTSXCallEdges` (server-gated: JSX `<Button/>` → call edge).
  Full suite + lint(0) + fmt green. (React `.jsx`/`.tsx` were already indexed for structure; now their
  component-usage call graph works too.) COMMIT+PUSH. Next: Vue SFC (vue-language-server is installed —
  probe + add).
- 2026-06-24 #121 (polish — `codemap doctor` environment check) — now that codemap drives several
  optional language servers, users need a proactive answer to "is my TS/Python actually going to be
  indexed?" (the missing-server note only appeared mid-index). New `codemap doctor` (sibling tools all
  have one): `Service.Doctor(ctx)` → a `DoctorReport` of environment checks — data dir, go toolchain
  (`--precise` Go), gopls (`--lsp`), each `lspsrc.DefaultServers` server (typescript-language-server
  for TS/JS, pyright-langserver for Python, via `exec.LookPath`), and Ollama embeddings (type-asserted
  `Available` probe with a 3s timeout; structure works without it). Each check carries an actionable
  **install hint** when missing (⚠) and `--json` for agents. Verified live: all ✓ locally; with a
  restricted PATH, ⚠ + hints ("install typescript-language-server to index typescript/javascript",
  "go install …/gopls@latest"). Test `TestServiceDoctor` (fake embedder → no network; asserts every
  check present + the hint/OK invariant). Docs: README/cli.md/docs.go command tables. Full suite +
  lint(0) + fmt green. COMMIT+PUSH.
- 2026-06-24 #120 (multi-lang — Python via pyright) — **Python is now first-class**, riding the
  LSP plumbing as a second `ServerSpec` row (`pyright-langserver --stdio`, its own process). De-risked
  with a probe first: pyright extracts symbols AND `callHierarchy` resolves (`compute → add`). One real
  wrinkle — pyright reports a function's **parameters/locals as nested Variable symbols** (`add.a`,
  `add.b`), which would clutter the graph. Fixed generally in `appendSymbols`: a new `insideCallable`
  flag drops Variable symbols nested inside a function/method, while keeping module/class-level
  variables — so no param nodes for any LSP language. Verified live: `symbols calc.py` → just `add` +
  `compute` (no params), `callers add` → `compute` (precise Python call graph). `.py` was already
  mapped by `LanguageForPath`; missing-pyright is auto-surfaced via the existing MissingServers advisory.
  Tests: lspsrc `TestAppendSymbolsSkipsParams` (params dropped, module var kept) + updated
  `TestAppendSymbolsNesting` signature; index `TestIndexPython` (server-gated: indexed, no param node,
  cross-function precise edge). New `specs/python.yml` E2E (3 outcomes green, contractHash stamped).
  Docs swept to **Go + TypeScript + JavaScript + Python** across README/AGENTS/CLAUDE/docs.go/
  quick-start/cli.md + the `--precise` flag help (TS/JS/Python via callHierarchy). Full suite + lint(0)
  + fmt green; pure-Go (pyright is a spawned subprocess). COMMIT+PUSH. Next: structure-only markup
  (Vue/HTML/CSS/Docker) via a Pass-2b, or polish.
- 2026-06-24 #119 (multi-lang — JavaScript via server-sharing; cross-language call graph) — the
  natural completion of the LSP epic: **JavaScript is now first-class**, riding all existing machinery.
  `typescript-language-server` handles JS natively, so the blocker was only avoiding a double-spawn for
  a TS+JS repo. Solution — **one server serves both**: `ServerSpec` now carries `Langs []LangBinding`
  (a server → many languages); `registerLSP` spawns the server **once** per spec (first present language
  owns it) and `Extractor.Bind(lang, langID)` registers the rest sharing that one connection, each
  routed with its own LSP `languageId`; only the owner closes the server (bound extractors are
  `shared` → Close is a no-op). `LanguageForPath` already mapped `.js/.jsx/.mjs/.cjs`. De-risked with a
  probe first (tsserver extracts JS symbols via `languageId:javascript`). Verified live on a mixed
  project: `index --precise` indexes both, and **`callers add` (a JS function) returns `compute`
  (app.ts) AND `run` (main.js)** — calls resolve *across* the `.ts`↔`.js` boundary via the shared
  callHierarchy. Tests: lspsrc `TestBind` (binding shares root/client, marked shared, Close no-ops),
  index `TestIndexJavaScriptMixed` (server-gated: both langs indexed by one server, cross-language
  precise edge). New `specs/javascript.yml` E2E (mixed TS+JS, asserts js node + both languages +
  cross-language caller; contractHash stamped). Docs swept Go+TS → **Go + TypeScript + JavaScript**
  across README/AGENTS/CLAUDE/docs.go/quick-start. Full suite + lint(0) + fmt green; pure-Go /
  CGO_ENABLED=0 (server is a subprocess). COMMIT+PUSH. Next: Python (pyright — a second ServerSpec row).
- 2026-06-24 #118 (vectors — wire the hybrid (vector + BM25) search the store already had) —
  dogfooding semantic search (a "hotspots ranking" query returned only loosely-related hits) traced to
  a real gap: `Service.Semantic` called pure-vector `vstore.Search`, while `vstore.HybridSearch`
  (vector + BM25 over the symbol/fqn text index) was **built but never wired** — and the README/AGENTS
  already *claimed* "vector + BM25 hybrid (RRF) search," so the docs were aspirational. Switched
  `Semantic` to `HybridSearch(vec, query, topK, name)` with a defensive fallback to pure `Search` (so an
  older index lacking a text index never hard-fails). Now a query that names a symbol gets a keyword
  boost while conceptual queries still match by vector — verified live: "hotspots ranking by in-degree"
  → the four Hotspots symbols up top (was loosely-related before); the conceptual `semantic.yml` flow
  ("verify a signed login credential" → Authenticate) still passes with real Ollama. Query-side only —
  **no reindex** (the text index already exists from `WithTextIndex(symbol, fqn)`). Improves both the
  CLI `semantic` and the studio Search tab. Test: extended `TestServiceSemantic` (a name query ranks the
  named symbol first via hybrid BM25); store-level `TestHybridSearch` already covered fusion. Full suite
  + lint(0) + fmt + semantic E2E green. The docs are now accurate (hybrid is real). COMMIT+PUSH.
- 2026-06-24 #117 (agent parity — `codemap_unannotate` MCP tool) — dogfooding the annotation layer
  surfaced a real CLI/MCP asymmetry: the CLI can create, list **and remove** annotations
  (`annotations --rm <id>`), but the MCP exposed only `codemap_annotate` (create) + `codemap_annotations`
  (list) — **no delete**. The annotation layer is explicitly "the harness's knowledge layer for agents,"
  yet an agent could only *accumulate* notes, never prune a stale one. The backend already existed
  (`Service.RemoveAnnotation`, used by the CLI), so this just exposes it: new `codemap_unannotate` tool
  (`{id, path}`) → `{id, removed}` (+ a "no annotation with that id" note when absent, never an error).
  18th MCP tool. Test `TestMCPUnannotate` (in-memory transport round-trip: annotate → capture id →
  unannotate removed:true → gone from list → second remove a graceful removed:false), and added it to
  the tools/list presence check. Swept the live "17 tools" → "18" in README/AGENTS/CLAUDE + listed
  `codemap_unannotate` (BACKLOG release-history entries left as-is). Verified the rest of the layer is
  solid by dogfooding: `annotate`/`annotations` round-trip and surface in `impact` (⟐), `callers Close`
  honestly warns "matches 7 definitions … query a more specific name", nonexistent-symbol/no-path
  errors are clean. Full suite + lint(0) + fmt green. COMMIT+PUSH.
- 2026-06-24 #116 (docs accuracy — `ctrl+g` + `orphans` value-following, swept consistent) — paid
  down the doc debt from the last three increments. **studio `ctrl+g`** (#115): `docs/studio.md` Keys
  table gained a `ctrl+g` row and the Metrics/Impact/Search tab blurbs now mention "open in the Graph
  walker." **`orphans` value-following** (#113/#114): four surfaces still framed orphans as only
  "interface/reflection-blind candidates" — now consistent with the README that it *follows functions
  wired by value* (cobra `RunE` / `mux.HandleFunc`, via `references` edges that never enter the call
  graph) while staying interface/reflection-blind: updated the in-CLI `codemap docs accuracy`
  (`internal/app/docs.go`, verified it renders), `AGENTS.md`, `docs/cli.md`, and the MCP
  `codemap_orphans` tool description (so agents know value-wired handlers aren't falsely flagged). No
  behavior change; build + full suite + fmt clean. COMMIT+PUSH.
- 2026-06-24 #115 (studio TUI — cross-tab `ctrl+g` opens any symbol in the Graph walker) — the
  Graph walker (browse hubs → walk callers/callees → re-center → backspace) is the nicest exploration
  UI, but it was only reachable via its own hub list: a symbol found by **Search**, **Impact**, or
  **Metrics** could be drilled to Impact (blast radius) but not *walked*. New global **`ctrl+g`** (any
  tab, like ctrl+s/ctrl+r) re-centers the Graph on the active selection and switches to it, focused on
  the callers/calls pane — so a search hit / blast node / metrics row becomes a starting point for
  walking the call graph. Implementation: refactored the per-tab selection logic into one
  `selectedCenter()` (sym/fqn/file/line) that now backs **both** ctrl+s (source) and ctrl+g, so they
  always act on the same target; `openInGraph()` sets the center, clears the walk stack, focuses refs.
  Footer hints + help overlay updated (`ctrl+g  open the selection in the Graph walker`). Test
  `TestOpenInGraphFromSearch` (search hit → ctrl+g → Graph centered on it, focusRefs, fresh stack,
  detail load fired). Extended `specs/studio.yml`: after the name search, ctrl+g → walker centered on
  `app.Run` showing "Called by (2)" → Top/Other, snapshot `graph_from_search`, back to Search to finish
  (contractHash re-stamped). Full suite + tui lint(0) + fmt + studio E2E green. Polish-first, no backend
  change. COMMIT+PUSH.
- 2026-06-24 #114 (real usefulness — `orphans` follows method-value handlers; `hotspots` stays
  clean) — continued dogfooding: after #113, the dominant remaining false positives were the **17
  `mcp.Server.handleXxx`** methods, registered via `sdkmcp.AddTool(s.srv, tool, s.handleInit)` — a
  *selector* method value, which #113's bare-ident-only capture skipped. This is also the ubiquitous web
  pattern `mux.HandleFunc("/path", s.handleHome)`. Fix: `gosrc` value-ref extraction now also takes
  selectors (`s.handle`, `pkg.Fn` → the selected name) via a new `valueRefName`, so method-value handler
  registration produces a `references` edge. To keep this safe, **decoupled `Hotspots` (and the
  `HasNameInEdges` inflation flag) from `references` — they now count only `calls`**: a hub is what many
  *call* sites depend on, and counting value refs would let a commonly-named field/method shadow real
  hubs. References feed **only** `orphans` (where being conservative — keeping a node *out* of dead-code
  — is the correct direction; a false value-ref can never create a phantom call). Pre-#113 this is a
  no-op (no references existed). Verified live (reindex --precise): the 17 MCP handlers + `Extractor.
  Language` gone from `orphans` (remaining list is now overwhelmingly interface implementations — the
  documented interface-dispatch blind spot); `hotspots` top-6 unchanged (Session.Close 53, NewService
  52, …); `callers handleInit` → none (no call-graph leak). Tests: extended gosrc
  `TestValueRefsForFunctionValues` (method-value selector case), new graph
  `TestHotspotsCountsCallsNotReferences` (references don't inflate hotspots). Full suite + lint(0) + fmt
  + precise/query E2E green. COMMIT+PUSH. (Still blind: calls inside FuncLits in top-level vars, e.g.
  `isInteractiveTerminal` in rootCmd's RunE closure — a separate, smaller gap.)
- 2026-06-24 #113 (real usefulness — `orphans` no longer flags function values as dead code)
  — found by **dogfooding** `orphans` on codemap itself: the top ~20 results were all `main.runXxx`
  cobra handlers — obvious false positives, because a handler wired by value (`RunE: runInit`) is
  never *called*, and the call-graph extraction only saw `*ast.CallExpr`. On any cobra-based Go CLI
  (a huge fraction of Go tools) `orphans` was useless. Fix — follow function **values**, kept entirely
  separate from the call graph: (1) gosrc now extracts bare identifiers naming a function used as a
  value — composite-literal field values (`RunE: runInit`), call arguments (`register(handler)`),
  slice/map elements — as `RefReferences` (a `references` edge, NOT `calls`); body refs attributed to
  the enclosing func, **top-level** decls (the cobra command table is a package-level `var`) attributed
  to the file. (2) `resolveEdges` now keys file nodes by path (paths have slashes, FQNs have dots — no
  collision) so file-scope refs resolve. (3) `DeleteCallEdgesBySource` (the `--precise` supersede) now
  deletes only `calls`, preserving `references` — go/types resolves calls, not value uses, so nuking
  them lost data (latent bug: it deleted references too, harmless only because nothing created them).
  (4) `Orphans` excludes nodes with an incoming `calls` OR `references` edge. The call graph
  (callers/callees/impact/path) is **untouched** — references aren't calls. Verified live (reindex
  --precise): the 20 cobra handlers are gone from `orphans`; `callers runInit` → none, `impact runInit`
  → 0 blast (no leak). 3 tests: gosrc `TestValueRefsForFunctionValues`, graph
  `TestOrphansExcludesValueReferenced`, index `TestIndexValueReferencedHandlerNotOrphan` (end-to-end).
  Full suite + lint(0) + fmt + precise/query E2E green. README orphans caveat updated (follows value
  wiring; interface/reflection still blind). Bare-idents only (precise same-package resolution, no
  over-matching); method values via selector (`s.handle`) deferred. COMMIT+PUSH.
- 2026-06-24 #112 (accuracy — MCP agent playbook didn't know TS precise) — the agent-facing surface
  (`codemap serve`) still described `precise:true` as Go-only, so an agent indexing a **TypeScript**
  project wouldn't know `codemap_index precise:true` is what gives it a call graph at all. Fixed two
  spots in `internal/mcp/server.go`: (1) the Instructions/accuracy preamble — `precise:true` is now the
  "unified exact-resolution pass: go/types for Go, typescript-language-server callHierarchy for
  TypeScript", explicitly noting TS has NO name-based call edges so on a TS project precise:true is the
  only way to get callers/impact/hotspots/path, and that the per-query `precise:true` on
  codemap_callers/callees is gopls = **Go only** (on TS, reindex with codemap_index precise:true);
  (2) the `codemap_index` `precise` jsonschema param now matches the CLI flag ("Go via go/types …;
  TypeScript via callHierarchy … gives TypeScript a call graph"). MCP `codemap_status` was already
  clean — it returns the raw StatusReport (precise_edges/languages as fields), no human engine-label.
  Extended `TestInstructionsCoverKeyCapabilities` to require "TypeScript" + "callHierarchy". Full suite
  + mcp lint(0) + fmt clean. With this, every precise/engine surface — CLI index msg, docs, studio,
  `status`, AND the MCP agent guide — tells the truth about which engine resolved the graph. COMMIT+
  PUSH. Next: JS row (server-dedup first).
- 2026-06-24 #111 (accuracy — `status` precise-edge label was Go-centric, wrong for TS) — the
  human-readable `status` output hardcoded **"N precise via go/types"** for *every* project, so a
  TypeScript project showed "3 precise via go/types" — wrong engine (TS precise edges come from
  `callHierarchy`, not go/types). And the no-precise fallback said "name-based; run --precise for exact
  **Go** call edges", doubly wrong for TS (which has *no* name-based call edges, and they aren't Go).
  New `preciseEdgeNote(preciseEdges, languages)` helper, engine-aware from the language mix: Go →
  "via go/types", TS → "via callHierarchy", mixed → "go/types + callHierarchy"; and the empty case →
  "no call graph yet; run 'codemap index --precise' to resolve TypeScript calls" for TS-only vs the
  generic "name-based …" otherwise. Pure helper → unit-testable: new `cmd/codemap/main_test.go`
  `TestPreciseEdgeNote` (5 cases incl. the absent-substring checks that catch the regression). Verified
  live: Go repo still "1272 precise via go/types"; TS project now "3 precise via callHierarchy". Full
  suite + cmd lint(0) + fmt clean. This was the last surface still saying "go/types" unconditionally
  (index message + docs were fixed in C2/#108). COMMIT+PUSH. Next: JS row (server-dedup first).
- 2026-06-24 #110 (E2E — studio drives a TypeScript call graph) — new `specs/studio_ts.yml`: the
  third pillar (flows that demonstrate value via LSP) now covers the studio TUI on a **TypeScript**
  project, proving the multi-language work reaches the TUI, not just the CLI. Sets up auth.ts (two
  exported functions) + server.ts (handleRequest + middleware, both calling across files), runs
  `index --precise` (TS call graph via callHierarchy), launches `studio`. Asserts inline (glyphrun
  `wait: screen: contains`, like studio.yml): the Graph tab opens on the top TS hub **validateToken**
  with **"Called by (2)"** → handleRequest/middleware (the cross-file precise call graph, badged
  `precise · index`), walks into a caller (`depth 1`), and the Metrics tab shows **typescript** in the
  language distribution. 3 snapshots (graph_ts, graph_ts_walk, metrics_ts). Isolates only
  `CODEMAP_DATA` (not `HOME`) so the typescript-language-server asdf shim resolves; local-only (CI
  skips flows). Verified: `glyph run` passes (1/1, clean exit), snapshot shows validateToken ←
  handleRequest(server.ts:2)/middleware(server.ts:6) and the typescript language bar. contractHash
  stamped; CLAUDE.md flow inventory updated. No Go change. COMMIT+PUSH. Next: JS row (server-dedup
  first); then JS/Python E2E once those land.
- 2026-06-24 #109 (studio polish — honest Graph empty-state for TS) — the multi-language work
  exposed a misleading TUI state: `graph.Hotspots` ranks only call/reference edges, so a **TypeScript
  project indexed without `--precise`** (nodes + `defines` edges, but zero call edges — TS calls come
  only from the precise pass) produced an empty hub list, and the Graph tab then showed
  `notIndexedHint` = "no index yet — press ctrl+r to index." Flat-out wrong: it IS indexed. New
  `Model.emptyGraphHint` distinguishes three states — (1) genuinely unindexed → the index hint;
  (2) indexed-with-nodes-but-no-hubs **and** `Languages["typescript"]>0` → "indexed — N nodes — but no
  call graph yet … TypeScript call edges come only from the precise pass. Reindex with `codemap index
  --precise` (needs typescript-language-server)" — NOT ctrl+r, which is structure-only and wouldn't add
  TS call edges; (3) Go-only trivial project with no calls → the same but without the TS server line.
  Pre-wrapped into short lines so the body width doesn't soft-wrap `typescript-language-server` across a
  break. `TestGraphEmptyStateDistinguishesNoCallGraph` covers all three. Full suite + tui lint(0) + vet
  + fmt clean; studio E2E (PTY) still green. Polish-first, no backend change. COMMIT+PUSH. Next: JS row
  (needs server-dedup so a TS+JS repo doesn't spawn two tsservers); then a studio-on-TS E2E flow.
- 2026-06-23 #108 (multi-lang — docs: TS call graph is shipped, not "in progress") — swept every
  doc surface that still said TypeScript call edges were "in progress / the next slice" and made them
  accurate now that C2 (#107) shipped. **README**: features graph bullet ("symbols + structure always,
  plus a precise call graph under `--precise`"), the "Precise call resolution" bullet (now framed as the
  unified cross-language pass: go/types for Go + callHierarchy for TS, noting TS has no name-based call
  edges so `--precise` is its *only* call-graph source), the Languages note, the `index --precise`
  command comment, and a new paragraph in the Accuracy section spelling out the TS-specific story.
  Renamed the Accuracy heading `name-based vs precise (go/types)` → `name-based vs precise` (no longer
  Go-only) and fixed the now-stale `#accuracy-…-gotypes` anchor link in `docs/cli.md`. **`internal/app/
  docs.go`** (the in-CLI `codemap docs` the harness reads): overview + accuracy topics updated — verified
  they render (`docs accuracy` mentions callHierarchy, `docs overview` mentions the TS precise graph).
  **`docs/cli.md`** table row + analysis-commands prose. **`cmd/codemap/main.go`** `--precise` flag help.
  **AGENTS.md** three spots (high-level edge summary, Extraction section pointing at
  `Indexer.resolveLSPCallEdges`, Accuracy model). No behavior change — `go build ./...` clean, gofmt
  clean. Docs now match the actual commands (the directive's accuracy pillar). COMMIT+PUSH. Next: JS row
  (one `DefaultServers` entry, but needs server-dedup so a TS+JS repo doesn't spawn two tsservers — not
  literally one line); then Python (pyright); then markup structure-only layer.
- 2026-06-23 #107 (multi-lang — slice C2: TS call graph) — **`index --precise` now gives TypeScript a
  call graph** — callers/impact/hotspots/path work for TS. New `indexer.resolveLSPCallEdges` (Pass 3,
  under `--precise`): collects `extract.CallResolver` extractors, builds `fqnTo` (caller) + `posTo`
  ((file,line)→nodeID) from the LSP-language nodes, drives `CallEdges` per file, and writes each
  resolved call as a `ProvPrecise`/`WeightLSP` `calls` edge — so the SAME callers/impact/hotspots/path
  queries return TS results, no query change. The Go `go/types` pass is now gated on `Languages["go"]>0`
  (so a TS-only `--precise` doesn't emit a spurious "not a buildable Go module" note). **Caught a real
  bug while wiring:** the `callee.ts` *file node* shares line 1 with the first symbol, so the collision
  guard was deleting the callee's `posTo` entry → 0 edges; fix = exclude file nodes from the position
  map (never call targets). Index message made engine-neutral ("N call edges resolved exactly" — it now
  spans go/types + callHierarchy; precise.yml re-stamped). Verified live: `callers callee` → `caller`,
  `impact callee` → 1 caller. Test `TestIndexTypeScriptCallEdges` (server-gated: callers work with
  `--precise`, none without). Full suite + lint v2 (0) + precise/typescript/query/studio E2E green; fmt
  clean. CGO_ENABLED=0. COMMIT+PUSH. **`--precise` is now the unified exact-call-resolution pass: go/types
  for Go, callHierarchy for TypeScript.** Next: doc the TS call-graph (#104 said "in progress"); then
  JS/Python rows; perf for large TS repos (callHierarchy is 2 RPCs/symbol — fine for now, budget later).
- 2026-06-23 #106 (multi-lang — slice C1: TS call extraction) — **`lspsrc.CallEdges` resolves
  TypeScript calls via LSP callHierarchy.** First de-risked with a probe: confirmed `tsserver`
  `prepareCallHierarchy` + `outgoingCalls` resolve a cross-file call (`caller → callee`) correctly after
  `didOpen`, ~30ms, no warm-up needed for a small project — so the reviewer's "cold server" risk didn't
  bite (will revisit for large repos). New neutral `extract.CallEdge` {FromFQN, ToFile, ToLine,
  External} + `extract.CallResolver` interface. `lspsrc.Extractor` implements it: `CallEdges(relPath)`
  re-walks documentSymbols and, for each function/method/constructor, prepares call hierarchy at the
  symbol's SelectionRange and maps each outgoing call's callee to its declaration position (callee
  outside the project root ⇒ External, no node). Additive — not wired into indexing yet (slice C2), so
  zero behavior change. Test `TestCallEdgesTypeScript` (server-gated): `caller → callee.ts` edge
  resolves. Full suite + lint v2 (0) + CGO_ENABLED=0 build green; fmt clean. COMMIT+PUSH. Next: C2 wires
  it into the indexer under `--precise` (write ProvPrecise edges via a posTo position-join, like Go's
  resolvePreciseEdges) so callers/impact/hotspots work for TS.
- 2026-06-23 #105 (multi-lang — TS E2E flow) — **`specs/typescript.yml` demonstrates LSP-backed
  TypeScript indexing end-to-end** — hits the directive's "demonstrate value via … LSP" channel for a
  non-Go language and guards the new feature. Indexes a `.ts` file (class + method + function) and shows
  `index` → `symbols` → `find` working on TS: the snapshot proves `UserService` (class), `getUser`
  (method, class-nested FQN), `makeService` (function) all become real graph nodes, and `find getUser`
  resolves to `svc.ts:2`. 3 positive `contains` outcomes; contractHash stamped; `glyph run` passes.
  Server-gated/local-only like studio.yml (needs `typescript-language-server` + `node`; CI skips flows);
  isolates only `CODEMAP_DATA` so the asdf shim resolves. CLAUDE.md flows note updated. No Go change ⇒
  CI green. COMMIT+PUSH. **Flows now cover every value channel: graphs (query), vectors (semantic),
  Go-precision (precise), and multi-language/LSP (typescript).** Next: slice C (TS call edges via
  callHierarchy) — the hard slice; will probe tsserver callHierarchy reliability first.
- 2026-06-23 #104 (multi-lang — docs) — **documented TypeScript support** so the shipped feature is
  discoverable (the #91/#98 lesson: an undocumented capability is invisible). Updated every "v0.1
  indexes Go" / "Go only" claim to the accurate Go + TypeScript reality, honest that TS is structure +
  semantic search today with call edges in progress: README (graph bullet + Languages note), AGENTS.md
  (rewrote the "Extraction (v0.1 = Go only)" section → "Go + TypeScript", and corrected the stale
  "lspsrc built but NOT yet wired" claim — it IS wired via present-aware registerLSP), docs/quick-start,
  the in-tool `codemap docs overview`, and CLAUDE.md's one-liner. MCP instructions needed no change
  (they never claimed Go-only; codemap_docs now mentions TS). Docs-only ⇒ full suite + lint v2 (0) +
  MCP playbook test green; fmt clean. COMMIT+PUSH. Next: slice C (TS call edges via callHierarchy — the
  hard slice per the design reviews: needs warm-up + retry-on-empty), then a TS E2E flow.
- 2026-06-23 #103 (multi-lang — slice B2: index-output accuracy) — **actionable missing-server message
  + Go-gated `--precise` tip.** Two index-output fixes now that TS is real. (1) The "install the server"
  advisory: `Index` now calls `indexAdvisory(res)` which surfaces `N typescript file(s) skipped —
  install "typescript-language-server" to index them (or --no-lsp)` whenever a recognized language is
  present but its server is missing — **decoupled from the `FilesScanned==0` gate** (the reviewer's P1:
  a TS file dropped from a Go+TS repo is no longer silent), while genuinely-unsupported languages keep
  the "planned" note only when nothing indexed. (2) The `--precise` tip (#97) was over-firing on
  TS-only projects (it's a go/types pass — Go-only); now gated on `rep.Languages["go"] > 0` via a new
  `Result.Languages` (file-count-per-language, populated from the walk). Tests: `TestIndexAdvisory`
  (missing-server→actionable even with FilesScanned>0; unsupported→planned; clean→empty), updated
  `TestIndexNonGoWarns`. Verified live: a TS-only index shows no `--precise` tip; a Go index still does.
  Full suite + lint v2 (0) + query/studio/precise/index_status E2E green; fmt clean. COMMIT+PUSH. Next:
  slice C = TS call edges (callHierarchy) to light up callers/impact/hotspots for TS.
- 2026-06-23 #102 (multi-lang — slice B: TypeScript indexes) — **`codemap index` now indexes
  TypeScript** when `typescript-language-server` is on PATH. New `lspsrc/registry.go` (`ServerSpec` +
  `DefaultServers` = TS). `IndexProject` restructured: walk → **present-aware** `registerLSP` (spawns a
  server ONLY for a recognized language actually in the repo, so a Go-only project pays zero cost and
  never spawns) → re-walk to route those files → `defer ix.Close()` shuts the server down. `Options.NoLSP`
  + `--no-lsp` flag to opt out. `Result.MissingServers` records present-but-uninstalled servers (for the
  message slice). **Verified live: a `.ts` file with a class + method + function indexes into class/
  method/function nodes with dotted FQNs (UserService.getUser) + defines edges + works with
  `symbols`** — so structure browsing and (with embeddings) semantic search work on TS end-to-end; the
  Go path is byte-for-byte unchanged (queries are backend-blind). Arrow-fn consts fall to `variable`
  when tsserver's Detail is empty (known lossy heuristic). Tests: `TestIndexTypeScriptSymbols`
  (server-gated, asserts kinds/FQNs/defines), `TestIndexTypeScriptDisabledByNoLSP`; updated
  `TestIndexNonGoWarns` → `NoLSP:true` (deterministic now that a TS server would index .ts). Full suite
  + lint v2 (0) + query/studio/precise E2E green; CGO_ENABLED=0 clean. COMMIT+PUSH. **Follow-ups:** (B2)
  surface `MissingServers` as an actionable "install typescript-language-server" message decoupled from
  the FilesScanned==0 gate; the `--precise` tip (#97) now over-fires on TS-only projects (gate on Go
  presence); then slice C = TS call edges (callHierarchy).
- 2026-06-23 #101 (multi-lang — slice A: foundation) — **lspsrc satisfies the Extractor interface +
  Indexer lifecycle + broadened symbol mapping.** Additive, zero behavior change (lspsrc not yet
  registered; Go path untouched). (1) Fixed the latent interface blocker: `lspsrc.ExtractFile` was 3-arg
  (absPath, relPath, src) but `extract.Extractor` is 2-arg — now stores `root` and derives the file://
  URI internally; added a `var _ extract.Extractor` compile assertion. (2) `Indexer` gained `closers
  []io.Closer` + `Close()` (best-effort, idempotent) — the load-bearing lifecycle owner so a future
  spawned language server is shut down per run, not leaked. (3) **Reaped the subprocess**: the
  `lsp.Spawn` closer Killed but never `Wait()`'d (zombie — reviewer's catch); now Kill+Wait under a
  sync.Once (idempotent). (4) Broadened `mapKind` to take the whole `DocumentSymbol` (for the arrow-fn
  Detail heuristic) and the full LSP SymbolKind set (class→class, interface/enum/struct→type,
  namespace/module→module, constructor→method, arrow-fn const→function, plain var→variable); added the
  missing LSP enum consts + `extract.Kind{Class,Module,Variable}` (schema already accepts them). Tests:
  rewritten `TestMapKind` (12 cases incl. arrow-fn), `TestAppendSymbolsNesting` (class), new
  `TestIndexerCloseNoServers` (idempotent). Full suite + lint v2 (0) + CGO_ENABLED=0 build green; fmt
  clean. COMMIT+PUSH. Next: slice B wires TS in (registry + LookPath-guarded registration + --no-lsp +
  the decoupled missing-server message).

## Post-v0.5.0 polish
- 2026-06-23 #100 (studio precise-awareness) — **the Graph tab knows when the index is already precise.**
  The precise feature created a redundancy: pressing `p` in the Graph tab spawns gopls to recompute the
  centered node's callers/callees, but on a `--precise` index the stored edges are *already* exact — so
  the gopls round-trip is wasted (and can be slow or hit "no views"). Now, when `status.PreciseEdges > 0`:
  the hub-detail header shows a `precise · index` badge (the relations are exact, from go/types), and
  `p` short-circuits with "already precise — these relations are from the --precise index" instead of
  spawning gopls. On a name-based index `p` still does its gopls recompute (studio.yml's name-based
  fixture proves this). Test `TestGraphPreciseIndexAware` (badge shown + `p` fires no command + informs);
  `TestGraphPreciseToggle` (name-based path) stays green. Full suite + lint v2 (0) + studio E2E green;
  fmt clean. COMMIT+PUSH.

## 🏷️ Release — v0.5.0 (2026-06-23) — RELEASED
The precise-call-resolution epic (#87–#99, the 13 commits since v0.4.0). Tagged `v0.5.0`, release.yml
green: GitHub release with 5 pure-Go targets + checksums, homebrew-tap formula bumped to 0.5.0
(`brew upgrade codemap`). Headline: opt-in `codemap index --precise` (in-process go/types) makes every
query exact, eliminating same-named over-matching; name-based stays the fast default. See the epic
below for the full slice-by-slice record.

## 🎯 Epic — precise call resolution (go/types) — GREENLIT 2026-06-23 · SHIPPED in v0.5.0
The name-based over-matching that #79–#86 *flag* (callers/impact/hotspots inflation) gets *eliminated*
by resolving calls precisely with pure-Go `go/types`. Designed via a 16-agent workflow (map → research
→ judge panel → synthesize → adversarial review). **Chosen approach:** opt-in `--precise` Pass 3 in the
indexer using in-process `golang.org/x/tools/go/packages`+`go/types` (NOT gopls, NOT NeedDeps), in a new
`internal/extract/typesrc/`. Name-based Pass 2 stays the byte-for-byte fast default. "Precise supersedes
name" is made deterministic by an explicit `edges.provenance` column (verified: no query reads `weight`,
so weight can't be the supersede key, and in-package WeightLSP=1.0 name edges would collide with precise
1.0 — the winning design's missed double-counting bug). Supersede = physical delete-then-insert per clean
source node, same `calls`/`references` edge_type ⇒ **zero query changes**. Degrades per-package (any
type-check error keeps that package's name edges) and globally (no `go` toolchain ⇒ no-op). Shipping in 3
CI-green slices: **A** schema/store foundation (done below) · **B** typesrc resolver + unit test · **C**
indexer Pass 3 + `--precise` CLI/MCP flag + integration test. Adversarial reviews fixed two CI-RED traps
the plan missed: migrate() isn't transactional (use idempotent duplicate-column-tolerant ALTER, done),
and the headline fixture is wrong (need N callers→one concrete type, not 1 caller→N same-named methods).
- 2026-06-23 #99 (docs surface audit) — **systematic command/tool/topic accuracy audit.** Diffed the
  documented surface against the code: the **20 CLI commands** (docs/cli.md) and **17 MCP tools**
  (docs/mcp.md, matches CLAUDE.md's count) are fully and accurately listed — no drift. The one gap: the
  `annotations` docs topic *exists and is served* (the full `codemap docs` guide includes it), but was
  **advertised in only 5 of 6 topics** in five places — the CLI `docs` help, the MCP `docsInput`
  jsonschema, the MCP tool Description, docs/cli.md, and docs/mcp.md — so an agent reading the help
  wouldn't know to request `docs annotations`. Added it to all five. Full suite + lint v2 (0) green
  (the MCP playbook-sync test is unaffected — it checks the `instructions` const, not the topic
  schema); fmt clean. COMMIT+PUSH.
- 2026-06-23 #98 (front-page docs accuracy) — **README/AGENTS feature list now matches reality.** Two
  fixes after a front-page audit: (1) the headline `--precise` capability was invisible — the
  "Precise navigation (LSP)" bullet described only the per-query `--lsp` gopls path, never the
  in-process `go/types` `index --precise` pass that makes EVERY query exact at once. Rewrote it
  ("Precise call resolution (go/types)") to lead with `--precise` as the graph-wide fix, `--lsp` as the
  one-off, matching the #91 Accuracy rewrite. (2) The graph bullet claimed edges "calls, imports,
  implements, references, overrides, and test-coverage" — but the real index produces **only `calls`
  and `defines`** (verified: `SELECT edge_type … GROUP BY` → just those two; `imports`/`implements`/
  `overrides`/`references` are unused schema slots reserved for the planned LSP/tree-sitter backends,
  and test-coverage is *derived* by walking the call graph to test nodes, not a stored edge). Corrected
  both README and AGENTS.md (source of truth) to list `calls`/`defines` and reserve the rest. Docs-only
  ⇒ CI green. COMMIT+PUSH.
- 2026-06-23 #97 (precise epic — discoverability at index) — **plain `codemap index` nudges toward
  `--precise`.** Dogfooding showed a new user indexing a Go project never learns `--precise` exists at
  the moment they'd benefit (status hints, but only if run). Now a name-based index of a non-empty
  project prints `tip: add --precise to resolve Go call edges exactly (eliminates same-named
  over-matching)` — gated on `exec.LookPath("go")` so it's only shown when actionable (no go ⇒
  `--precise` would just degrade to this same graph), and suppressed when `--precise` was used or under
  `--json`. Tested in `specs/precise.yml` (new `tip_suggests_precise` outcome; the flow's name-based
  phase now surfaces the tip, re-stamped). Existing flows pipe index to /dev/null so are unaffected.
  Full suite + lint v2 (0) + index_status/query/studio/precise E2E green; fmt clean. COMMIT+PUSH.
  **Precise is now discoverable at index (#97), in status (#95), and in the docs (#91) — the feature
  surfaces everywhere a user or agent would look.**
- 2026-06-23 #96 (precise epic — note consistency) — **`impact`/`callers`/`callees` ambiguity notes are
  provenance-aware**, closing the last spot where the precise feature and the honest-messaging notes
  (#79/#80) disagreed: on a `--precise` index those notes still said "(name-based) … use --lsp", which
  is stale. New `Service.hasPreciseEdges(g, pid)` (reuses #95's `CountEdgesByProvenance`) switches the
  note: name-based index → "matches N definitions (name-based) … reindex with 'codemap index --precise'
  for exact edges, or --lsp for one method"; precise index → "matches N definitions — each resolved
  precisely, but the results still merge all of them; query a more specific name to separate them". The
  studio Impact pane and MCP JSON inherit it (they render `rep.Note`). Existing name-based tests still
  green (note keeps "definitions"); new go-gated `TestAmbiguityNoteIsProvenanceAware` (name→mentions
  name-based+--precise, precise→"resolved precisely", never "name-based"). Verified live: `impact Close`
  on the precise index now reads "each resolved precisely … query a more specific name". Full suite +
  lint v2 (0) + query/studio E2E green; fmt clean. COMMIT+PUSH. **Every honest-messaging surface
  (hotspots #92, status #95, ambiguity notes #96) now respects the precise/name-based distinction.**
- 2026-06-23 #95 (precise epic — discoverability) — **`status` reports whether the index is precise or
  name-based**, the status-enrichment pattern (cf. #70's vectors line) applied to edge accuracy. New
  `graph.Store.CountEdgesByProvenance(pid, prov)`; `StatusReport.PreciseEdges`; `Status()` fills it.
  CLI: `edges: 2295 (1272 precise via go/types)` on a precise index, or `edges: N (name-based; run
  'codemap index --precise' for exact Go call edges)` otherwise — a trust signal (can I rely on these
  counts?) plus an actionable hint, and `codemap_status` JSON carries `precise_edges` for agents. Studio
  Metrics header mirrors it (`… · N edges (M precise) · …`), consistent with #71's embedding state.
  Tests: `TestStatusReportsPreciseEdges` (name index→0, precise→>0, go-gated) + tui
  `TestMetricsShowsPreciseState`. Verified live: the real precise index shows `edges: 1890 (1272
  precise via go/types)`. Full suite + lint v2 (0) + index_status/query/studio E2E green; fmt clean.
  COMMIT+PUSH. **Precise indexing is now discoverable from where users/agents already look (status).**
- 2026-06-23 #94 (precise epic — robustness) — **precise callee join is collision-safe.** The #93
  follow-up: the position-keyed `(file,line)→nodeID` join in `resolvePreciseEdges` mis-resolved when
  multiple declarations share a line (last-writer-wins overwrote the map), so a call to one of two
  same-line methods could route to the wrong node. Now the build detects colliding `(file,line)` keys
  and deletes them, forcing the lookup to fall through to the unique FQN match (line-independent, and
  reliable since typesrc replicates gosrc's FQN scheme exactly). Test `TestPreciseHandlesSameLineDecls`:
  `func (Real) Handle() {}; func (Other) Handle() {}` on ONE line; a caller of `Real.Handle` resolves
  to Real.Handle (in-degree 2), Other.Handle stays 0 — without the fix the collision routed both to
  the same-line Other.Handle. gofmt'd code (one decl per line) was already fine; this hardens the
  un-gofmt'd case. Full suite + lint v2 (0) + precise.yml flow green; fmt clean. COMMIT+PUSH.
- 2026-06-23 #93 (precise epic — E2E flow) — **`specs/precise.yml` demonstrates the precise pass
  end-to-end** — the directive's "flows that demonstrate value via … LSP/types" pillar, now viable
  because `index --precise` is deterministic in-process go/types (unlike the flaky one-shot gopls
  path that killed the idea at #78). The flow indexes a fixture where `Other.Handle` is **never
  called** yet shares a name with `Real.Handle`: name-based ranks the phantom `Other.Handle` as a hub
  (in-degree 3, flagged `(inflated)`), then `index --reindex --no-embed --precise` resolves 3 edges
  exactly and `Other.Handle` **vanishes**, leaving `Real.Handle` accurate and un-flagged. 3 positive
  `contains` outcomes (phantom hub appears name-based / inflation flagged / "resolved exactly via
  go/types"); contractHash stamped; `glyph run` passes. Spec only isolates `CODEMAP_DATA` not `HOME`
  (asdf's `go` shim needs a real HOME — verified that isolating HOME makes `--precise` degrade
  gracefully with a note, which itself proves the safety contract). CLAUDE.md flows note updated. No
  Go change ⇒ CI green (flows are local-only). COMMIT+PUSH. **Found a latent edge case to follow up:**
  the precise (file,line) callee join collides when multiple decls share a line (a one-line fixture
  mis-resolved); gofmt'd code is unaffected, but the indexer's posTo should drop colliding (file,line)
  keys and fall back to the FQN match — small, worth a test.
- 2026-06-23 #92 (precise epic — slice E: provenance-aware flag) — **the `⚠ name shared by N
  (inflated)` hotspots flag (#85/#86) no longer over-warns on a `--precise` index.** New
  `graph.Store.HasNameInEdges(nodeIDs)` returns which nodes still have ≥1 name-provenance incoming
  call/reference edge (chunked, via scanIDs). `Hotspots` now sets `SharedName` only when the name is
  shared (>1 def) AND the node still has name-based in-edges — so a hotspot whose callers were resolved
  exactly by the go/types pass shows its (accurate) count with no inflation warning. The CLI and studio
  Metrics rendering are unchanged (both gate on `SharedName>1`), so the studio dashboard becomes
  provenance-aware for free. Tests: existing `TestHotspotsFlagsSharedNames` still flags on a name-based
  index; new `TestHotspotsInflationFlagIsProvenanceAware` (go-gated) proves T.Close is flagged
  name-based but NOT after `--precise` (count accurate). **Real-repo dogfood: the live precise index's
  hotspots are now clean** — Session.Close (50), NewService (49), tui.sized (36) shown as genuine hubs,
  zero false "(inflated)". (Left as-is: the `impact`/`callers` ambiguity notes from #79/#80 — those are
  about the *query* being name-keyed across N definitions, which is still true and useful even on a
  precise index, independent of edge provenance.) Full suite + lint v2 (0) + studio E2E green; fmt
  clean. COMMIT+PUSH. **Precise-resolution epic complete: built (#87–90) · documented (#91) · UX
  reconciled (#92).**
- 2026-06-23 #91 (precise epic — docs) — **documented `--precise` everywhere** so the major new
  capability is discoverable (an undocumented flag helps no one). The accuracy story is rewritten from
  "name-based with a per-query `--lsp` escape hatch; graph-wide go/types *planned*" to "**`codemap
  index --precise` is the graph-wide fix, shipped**" across all six doc surfaces: README Accuracy
  section (full rewrite + index examples + command table) and its anchor (now
  `#accuracy-name-based-vs-precise-gotypes`, link in docs/cli.md updated to match), docs/cli.md,
  docs/quick-start.md, docs/mcp.md (`codemap_index` `precise` arg), AGENTS.md (the source of truth),
  the MCP `instructions` playbook (Accuracy paragraph now leads with `codemap_index precise:true` as
  the graph-wide fix), and the in-tool `codemap docs` accuracy + commands topics. Framing throughout:
  `--precise` makes EVERY query exact at once (no per-call flag), opt-in/additive, degrades with a
  note, `--lsp` remains the one-off per-query path. Playbook-sync test
  `TestInstructionsCoverKeyCapabilities` still green (kept its asserted strings). Full suite + lint v2
  (0) green; fmt clean. COMMIT+PUSH. (Remaining follow-up: slice E — make the `⚠ …(inflated)` flags
  provenance-aware so they don't over-warn on a `--precise` index.)
- 2026-06-23 #90 (precise epic — slice D: test-file callers) — **precise resolution now covers
  `_test.go` callers too** (`typesrc` flipped to `packages.Config.Tests = true`). The #89 diagnosis
  showed the entire residual was test-file callers staying name-based (Session.Close = 18 precise + 46
  name). Tests:true loads multiple variants of each package (plain / test-augmented / external `_test`
  / synthesized `.test` main), so the SAME file appears several times — added a `seen[absFile]` dedup
  (and a skip for files outside root, e.g. the generated test main) so each real file is processed
  exactly once, preventing precise-edge double-counting. Test `TestPreciseResolvesTestCallers`: a
  `_test.go` caller's `t.Run()` deflates T2.Run from 2→0 just like a production caller; existing
  typesrc/integration tests stay green (dedup ⇒ no regression at Tests:false-equivalent shape).
  **Real-repo result — the full payoff:** `--precise` now resolves **1,272** call edges (was 683),
  total edges DROP 2295→1890 (spurious fan-out physically gone), and `Session.Close` is in-degree 50
  **100% provenance='precise'** (was 64 = 18 precise + 46 fanned). Hotspots are finally trustworthy —
  the top hubs (`Session.Close`, `NewService`, `tui.sized`, `Service.Index`, `Open`) are genuine, not
  name collisions. Full suite + lint v2 (0) + query/studio E2E green; fmt clean. Live index restored to
  embedded+precise (617 vectors). COMMIT+PUSH. **Precise call resolution is complete for all Go code
  (production + tests).** Follow-up (slice E): the `⚠ name shared by N (inflated)` marker (#85/#86) is
  now over-cautious on a `--precise` index — the count is accurate, not inflated — so make that flag
  provenance-aware (only warn when the in-edges are name-based).
- 2026-06-23 #89 (precise epic — slice C: wired + shipped) — **`codemap index --precise` eliminates
  same-named call over-matching end-to-end.** Pass 3 in the indexer (gated on `opts.Precise`, after the
  name-based Pass 2): `exec.LookPath("go")` → `typesrc.Resolve(root)` → build `fqnTo` (caller) + `posTo`
  ((file,line)→nodeID, callee) from ProjectNodes → `DeleteCallEdgesBySource(cleanSourceIDs, ProvName)`
  → re-insert one `AddEdgeProv(…, ProvPrecise)` per resolved in-module call. Same `calls` edge_type ⇒
  **zero query changes**; Callers/Impact/Hotspots/BlastRadius all deflate to truth automatically. Wired
  through every surface: `Options.Precise`, `Result.Precise{Upgraded,Skipped,Note}`, `IndexReport` +
  CLI `--precise` flag (prints "N call edges resolved exactly via go/types") + MCP `codemap_index`
  `precise` arg. Degrades safely: no `go`/non-module ⇒ note + keep name edges (never wipes what it
  can't replace). Tests (go-gated): `TestPreciseCollapsesNameFanout` — the corrected fixture (3 callers
  → one concrete T1.Run; name-based inflates T2/T3.Run to in-degree 3, precise drops them to 0, T1.Run
  stays 3, **total edges to T1.Run == 3 = the double-counting guard the design's winning approach
  missed**) + `TestPreciseDegradesGracefully`. **Real-repo dogfood proves it surgically:** on codemap
  `--precise` resolved 683 call edges exactly; `lspsrc.Extractor.Close` (was 71, almost all spurious)
  dropped out of the top hotspots, and the identical fan-out pairs (71,71,70,70…) differentiated. The
  provenance breakdown is the proof — `app.Session.Close` in-degree 64 = **18 non-test callers all
  `provenance='precise'` (resolved exactly) + 46 `_test.go` callers still `'name'`** (typesrc uses
  `Tests:false` to match slice-1 scope). So precise is 100% correct for non-test code; resolving
  test-file callers (flip to `Tests:true`) is **slice D**. Full suite + lint v2 (0) + query/studio E2E
  green; fmt clean. CGO_ENABLED=0 pure-Go throughout. COMMIT+PUSH. **The name-based over-matching that
  #79–#86 could only flag is now eliminated for real Go code — the deferred backend, delivered.**
- 2026-06-23 #88 (precise epic — slice B: typesrc resolver) — **pure-Go `go/types` precise call
  resolver** in new `internal/extract/typesrc/`, standalone + unit-tested (not yet wired into the
  indexer). `Resolve(ctx, root)` runs one whole-module `packages.Load(LoadMode)` where `LoadMode`
  deliberately EXCLUDES `NeedDeps` (NeedTypesInfo already resolves cross-package callees via export
  data — the 706MB footgun, guarded by `TestLoadModeExcludesNeedDeps`). For each cleanly type-checked
  package it walks every func/method body's CallExprs, resolves each to the exact `*types.Func` (via
  `Info.Selections`/`Info.Uses`, generics collapsed with `.Origin()`), and emits a `PreciseEdge`
  {CallerFQN, CalleeFQN, CalleeFile, CalleeLine, External, Interface}. FQNs use gosrc's EXACT scheme
  (`pkgClause.Func` / `pkgClause.Recv.Method`, pointers+generics stripped) so they join existing nodes;
  callee position is root-relative for the (file,line) join. Degrades cleanly: a package with type
  errors is skipped (its files absent from CleanFiles ⇒ caller keeps name edges); a non-module dir or
  missing `go` ⇒ Available=false, no edges, no panic. Tests (go-gated skip): `TestResolvePrecise` —
  proves the crux, `t.Close()` over three same-named Close methods resolves to **exactly ONE** edge to
  `fix.T1.Close` (name-based would fan to 3); interface dispatch → `fix.Closer.Close` (Interface=true);
  stdlib → `fmt.Println` (External=true); **and position parity verified** (precise CalleeLine ==
  gosrc StartLine, so slice C's join will land). `x/tools` v0.45.0 promoted indirect→direct via
  `go mod tidy` (no download); CGO_ENABLED=0 build stays clean (go/packages is pure-Go). Full suite +
  lint v2 (0) green; fmt clean. COMMIT+PUSH. Next: slice C wires this into the indexer behind
  `--precise` (CLI+MCP) with the corrected N-callers→one-type integration fixture + supersede.
- 2026-06-23 #87 (precise epic — slice A: foundation) — **`edges.provenance` column + race-safe v2→v3
  migration + store plumbing.** Additive, zero behavior change (every edge tagged `'name'`). schema.go:
  `schemaVersion` 2→3, `provenance TEXT NOT NULL DEFAULT 'name'` on edges, `ProvName`/`ProvPrecise`
  consts, `idx_edges_source_prov`. store.go: migrate() adds the column via an idempotent
  `addColumnIfMissing` that tolerates SQLite "duplicate column" (race-safe across the multi-MCP model —
  NOT the TOCTOU table_info check the reviewer flagged) and creates the prov index in migrate() (it
  can't live in schemaSQL — the column doesn't exist yet on an upgraded edges table; that ordering bug
  was caught and fixed mid-implementation). New `AddEdgeProv` (AddEdge delegates with ProvName, so the
  9 test + 2 prod callers need no change) and `DeleteCallEdgesBySource(ids, prov)` (chunked, calls/refs
  only, leaves defines). Tests: `TestEdgeProvenance`, `TestMigrateV2ToV3AddsProvenance` (simulated v2 DB
  → column added, existing rows read 'name', idempotent). **Verified the REAL live v2 index migrated to
  v3 cleanly: 30,859 edges, all now provenance='name', user_version=3, zero errors.** Full suite + lint
  v2 (0) + query E2E green; fmt clean. COMMIT+PUSH.

## Post-v0.2.0 polish
- 2026-06-23 #86 (studio metrics honesty) — **studio Metrics "Top hubs" flags inflated hubs too**, the
  #85 follow-up on the directive's named Metrics surface. The dashboard's hub ranking showed the same
  name-collision inflation (Close/Error topping it) silently. Now each hub row whose name is shared by
  >1 definition gets a compact `⚠×N` suffix (reusing `HotspotRef.SharedName` already loaded via
  hubsCmd — no new query), with a one-time `⚠=name-inflated` legend in the section title when any hub
  is flagged. Width-safe (name budget reserved for the marker; the existing ≤120-col assertion still
  holds). Test `TestMetricsFlagsInflatedHubs` (⚠×6 on the collision, legend present, width ok). Full
  suite + lint v2 (0) + studio E2E green; fmt clean. COMMIT+PUSH. **Name-collision inflation is now
  flagged everywhere it's ranked: CLI hotspots (#85) and the studio Metrics dashboard (#86).**
- 2026-06-23 #85 (hotspots honesty) — **`hotspots` flags name-collision inflation.** Dogfooding showed
  the survey was useless: the top 8 hubs on the codemap repo were ALL `Close`/`Error` methods (71, 71,
  70, …) — not real hubs, just name collisions (name-based edges fan a `Close()` call out to all 6
  Close defs, so each is credited with every Close call). Now `Hotspots` runs one grouped query
  (`graph.Store.SymbolDefCounts`) and sets `HotspotRef.SharedName` when a name has >1 definition; CLI
  appends `⚠ name shared by N (inflated)`, JSON carries `shared_name`. So a genuine unique-named hub
  stands out as trustworthy while the inflated collisions are clearly labeled. One query, not N. Test
  `TestHotspotsFlagsSharedNames` (Close→SharedName 2; unique Solo→0). Verified live: every top Close
  row now reads `⚠ name shared by 6 (inflated)`. Full suite + lint v2 (0) + query/studio E2E green; fmt
  clean. COMMIT+PUSH. (Scoped to CLI+MCP: the studio hub columns are width-constrained and the detail
  pane keys off graphCenter not a HotspotRef, so marking there is a disproportionately fiddly follow-up
  for a navigation view — `SharedName` is now on the studio's data, ready when wanted.)
- 2026-06-23 #84 (orphans accuracy) — **`orphans` no longer flags `main`/`init` as dead code.**
  Dogfooding `codemap orphans` on the codemap repo listed `main.main` and `main.init` at the top as
  "dead-code candidates" — but Go invokes `main` and every `init` automatically, so they're never dead;
  a guaranteed false positive that undermined the command. `graph.Store.Orphans` now excludes package-
  level functions named `main`/`init` (`AND NOT (kind = function AND symbol IN ('main','init'))` — the
  kind guard keeps a *method* named main/init eligible, since those aren't special). Tests already showed
  `orphans` excludes test functions (kind filter); this removes the other built-in false positive.
  (The `runX` cobra handlers still appear — genuine name-based limitation: they're invoked via RunE
  function-value assignment the graph can't see; docs already call orphans output *candidates*.) Test
  `TestOrphansExcludesMainAndInit` (Orphan present; main/init absent). Verified live: top of the orphans
  list no longer starts with main/init. Full suite + lint v2 (0) + query/studio E2E green; fmt clean.
  COMMIT+PUSH.
- 2026-06-23 #83 (annotate surface, finish) — **path-annotation validation + `annotate --json` parity**,
  the two #82 follow-ups. (1) `AnnotatePath` now returns `matched` (both endpoints indexed, via
  `NodeExistsByName`) — annotating a path with a ghost endpoint is saved but warns (CLI `⚠ path
  endpoints "A" and "Ghost" aren't both indexed symbols …`; MCP `"matched": false` + note), mirroring
  #82's node behavior. (2) `codemap annotate` now honors the global `--json` flag (it silently ignored
  it while `annotations` already emitted JSON) — emits `{id, kind, target, source, matched, note?}`,
  the same shape MCP returns, so an agent/script using the CLI gets machine-readable output incl. the
  matched signal. Refactored runAnnotate to one node/path-shared tail. Tests:
  `TestAnnotatePathReportsUnknownEndpoint` (both-real→matched; ghost endpoint→saved,not matched).
  Verified live: node real/ghost `--json`, path real/ghost text. Full suite + lint v2 (0) + query E2E
  green; fmt clean. COMMIT+PUSH. **Annotate surface now complete & honest on CLI + MCP (node & path).**
- 2026-06-23 #82 (annotation honesty) — **`annotate` warns when the target matches no indexed symbol.**
  Dogfooding the harness-centerpiece feature found a real footgun: `codemap annotate NoSuchSymbolZZZ
  --note ghost` (and annotating on an un-indexed project) silently succeeded, creating an annotation
  that can NEVER surface in queries — exactly the wrong outcome for an agent pinning DB data to symbols
  by name (a typo is saved forever with no feedback). Now `AnnotateNode` returns `matched` (via a new
  tiny `graph.Store.NodeExistsByName`, checking `symbol=? OR fqn=?` — both, since annotations surface
  by either). Annotations are still SAVED when unmatched (they're reindex-durable by design and may
  predate code), but: CLI prints `⚠ no indexed symbol named "X" — saved, but it won't surface until one
  is (typo? not indexed yet?)`; MCP `codemap_annotate` returns `"matched": false` + an explanatory
  `note`. A real target → matched=true, no warning (verified: `annotate Bar` still clean and surfaces
  in impact). `source` was already fine (lists all defs). Tests: `TestAnnotateReportsUnknownTarget`
  (app) + `TestMCPAnnotateUnknownTarget` (in-memory MCP, the agent surface). Full suite + lint v2 (0) +
  query E2E green; fmt clean. COMMIT+PUSH. (Path annotations + CLI `annotate --json` parity are small
  follow-ups; node annotation is the common case and the dogfooded wart.)
- 2026-06-23 #81 (path honesty) — **`path` distinguishes "not a symbol" from "no path".** Dogfooding:
  `codemap path NoSuchSymbolXYZ Status` printed "no call path from NoSuchSymbolXYZ to Status" — but the
  symbol doesn't exist, so a typo'd name read as a merely-unconnected pair. Now `Path` checks both
  endpoints with `FindNodesBySymbol` up front and sets `PathReport.Note` ("X is not a symbol in
  <project>" / "neither X nor Y …") when one is missing, returning before the (pointless) search. CLI
  prints the note instead of "no call path"; note rides JSON to MCP. When BOTH endpoints exist but
  aren't connected, no note is set, so the CLI still shows the plain "no call path from X to Y"
  (correct). (`source` was checked too — it already shows ALL definitions of an ambiguous symbol with
  clear headers, no wart.) Test `TestPathReportsMissingEndpoint` (missing→note; real path→found,no
  note; both-real-no-path→not found,no note). Verified live. Full suite + lint v2 (0) + query E2E
  green; fmt clean. COMMIT+PUSH.
- 2026-06-23 #80 (ambiguity honesty, cont.) — **`callers`/`callees` warn on ambiguous names too**, the
  grounded #79 follow-up. `codemap callers Close` lists 71 callers merged across 6 unrelated `Close`
  methods with no signal — same wart as impact, but here `--lsp` is the directly actionable fix. The
  shared `relation` helper now does one extra `FindNodesBySymbol` and, when >1 definition, sets
  `RelationReport.Note`: `"Close" matches 6 definitions (name-based) — these results merge all of them;
  add --lsp (gopls) for one exact method`. CLI prints it as a `⚠` line (also gave the #77 precise-
  fallback note a `⚠`, consistent); note rides JSON to MCP agents. The ambiguity check is about the
  *queried* symbol's definitions (verified: `callees runStatus` has no note even though its results
  include the ambiguous Close methods). Reuses RelationReport.Note from #77 with no collision — name-
  based path sets ambiguity, precise path sets fallback (and preciseFallback's name-based call has its
  ambiguity note overwritten by the more-actionable fallback message, which still passes its test).
  Tests: `TestCallersWarnsOnAmbiguousName` (ambiguous→note, unique→none). Verified live on the codemap
  repo. Full suite + lint v2 (0) + query/studio E2E green; fmt clean. COMMIT+PUSH. (Studio Graph tab is
  node-centric exploration — different model — so left as-is; CLI+MCP cover the bare-name query surface.)
- 2026-06-23 #79 (flagship honesty) — **`impact` warns when the name is ambiguous.** Dogfooding found
  a real flagship wart: `codemap impact Close` resolves to 6 different `Close` methods across packages
  and silently merges them into one blast radius reading "direct callers: 71" — a misleadingly huge,
  conflated number with no signal it's name-based over-matching (contrast `impact Status` → 1 def,
  correct). Now `Impact` sets `ImpactReport.Note` when `len(Locations) > 1`: `"Close" matches 6
  definitions (name-based) — direct callers, blast radius, and covering tests below merge all of them;
  for one exact method use callers/callees --lsp`. (FindNodesBySymbol matches the bare name exactly, so
  the guidance points at precise callers/callees, not a "more specific symbol" — which wouldn't work
  for impact.) Surfaced on all three surfaces: CLI prints a `⚠` line right after the defined-sites
  list; the note rides JSON to MCP agents; the studio Impact pane shows it under the counts. Tests:
  `TestImpactWarnsOnAmbiguousName` (ambiguous→note, unique→none) + tui `TestImpactPaneWarnsOnAmbiguousName`.
  Verified live: `impact Close` shows the 6 sites + warning; `impact Status` clean. Full suite + lint v2
  (0) + query/studio E2E green; fmt clean. COMMIT+PUSH.
- 2026-06-23 #78 (docs accuracy) — **documented the honest-behavior contracts from #73–#77** so the
  people and agents who hit them understand they're expected, not bugs. Updated all four doc surfaces:
  the MCP `instructions` playbook (query tools return `{"indexed": false}` until indexed; precise
  degrades to name-based with a `note` when gopls is unavailable; `codemap_semantic` returns a `note`
  not an error when unembedded), the README Accuracy section, `docs/cli.md`, and the in-tool
  `codemap docs accuracy` guide (`internal/app/docs.go`). Extended `TestInstructionsCoverKeyCapabilities`
  to assert the new playbook content (`"indexed": false`, "degrades to name-based") so the agent's
  first-contact guide can't silently drift from the behavior. Confirmed while investigating that
  `preciseRelations` already calls `WaitReady` (waits for gopls' `$/progress "end"`), so the
  isolated-HOME "no views" is a genuine environment limitation, not a fixable race — #77's fallback is
  the correct handling and no LSP backend change is warranted. (A standalone CLI `precise.yml` flow
  would be flaky for the same reason; studio.yml already demonstrates precise-via-gopls reliably via
  its persistent session.) No behavior change. Full suite + lint v2 (0) green; fmt clean. COMMIT+PUSH.
- 2026-06-23 #77 (lsp polish) — **precise (`--lsp` / `precise:true`) degrades gracefully instead of
  erroring.** Dogfooding the LSP path surfaced a real wart: `codemap callers Foo --lsp` works when
  gopls can form a workspace view (correctly resolving `T.Process` vs `U.Process` where name-based
  over-matches both), but in a restricted environment — isolated HOME, no module cache, or a
  non-buildable project — gopls returns `jsonrpc error 0: no views`, which the CLI surfaced as a raw
  error **plus a full usage dump**. Now `PreciseCallers`/`PreciseCallees` catch a language-server
  failure and fall back to name-based results via a shared `preciseFallback` helper, setting
  `RelationReport.Note` ("precise (gopls) resolution unavailable (…) — showing name-based results").
  The CLI prints the note and drops the "(precise, via gopls)" label when it fell back (so it never
  mislabels name-based output as precise); the note rides the JSON to MCP agents too. Verified E2E:
  isolated env → graceful degrade (name-based + note, exit 0, no usage spew); real env → precise still
  resolves exactly one caller, labeled precise, no note. Test `TestPreciseFallbackToNameBased` drives
  the helper deterministically (no gopls needed). New field `RelationReport.Note`. Full suite + lint v2
  (0) + query/studio/semantic E2E green; fmt clean. COMMIT+PUSH. (Confirmed the precise path itself
  works end-to-end — a dedicated LSP demonstration flow is now a clean future increment.)
- 2026-06-23 #76 (mcp/agent polish) — **MCP query tools signal "not indexed" instead of empty results.**
  Completes the cold-start theme on the surface the harness vision cares most about: agents. Before,
  `codemap_callers`/`codemap_impact`/`codemap_find`/etc. on a never-indexed project returned empty
  arrays an agent would read as a real "no callers" / "no matches" answer (the MCP counterpart of the
  CLI #74 and studio #75 fixes). Added an `s.notIndexed(path)` guard (reusing `Service.Indexed` from
  #74) that short-circuits with a structured `{project, indexed:false, note:"project not indexed —
  call codemap_index first"}`; wired into all 10 query handlers (semantic, callers, callees, impact,
  hotspots, orphans, path, symbols, find, source). Leaves index/init/status/projects/docs/annotate
  alone. Semantic still composes: unindexed → "call codemap_index"; indexed-but-unembedded → the #73
  "no embeddings" note. Test `TestMCPNotIndexedSignal` drives 5 tools through the in-memory MCP
  transport against an un-indexed project and asserts each returns `"indexed": false` + the
  codemap_index hint. Full suite + lint v2 (0) green; fmt clean. COMMIT+PUSH. **Honest cold-start
  messaging now complete on ALL THREE surfaces: CLI (#74), studio (#75), MCP (#76).**
- 2026-06-23 #75 (studio polish) — **studio Impact & Search tabs show the cold-start hint too.** Graph
  and Metrics already short-circuited to "no index yet — press ctrl+r to index, or run 'codemap index'"
  on an un-indexed project, but Impact and Search did not: they invited you to "type a symbol and press
  enter" / "type and press enter" — input that could only ever return "not found" / "no matches" with
  nothing indexed (the studio counterpart of #74's misleading CLI empties). Added the same
  `!status.Registered` guard to both, factored the shared message into a `notIndexedHint(tab)` helper
  (Graph/Metrics now reuse it — 4 literals → 1). Test `TestColdStartTabsHintToIndex` (Impact + Search
  both render the hint when Registered=false). Full suite + lint v2 (0) + studio E2E green; fmt clean.
  COMMIT+PUSH. **All four studio tabs now give a consistent, actionable cold-start instead of inviting
  doomed input.**
- 2026-06-23 #74 (correctness) — **query commands say "not indexed" instead of misleading empties on a
  cold repo.** A probe of an un-indexed project showed `callers`/`callees` → "none" (as if the symbol
  genuinely had no callers), `impact` → "symbol X not found" (as if that symbol specifically was
  missing), `path` → "no call path" (as if the symbols existed but weren't connected), `orphans` →
  "none", `semantic` → "no matches" — all misleading when the truth is "this project was never
  indexed." (`status`/`find`/`symbols`/`hotspots` already self-hinted.) Added a cheap
  `Service.Indexed(cwd) (bool, name, err)` (registration check, no stats) + a CLI `requireIndexed`
  guard that prints `Project %q is not indexed yet. Run 'codemap index'.` (or, under --json, a
  structured `{project, indexed:false, note}` so agents get the same signal) and returns early. Wired
  into the 6 misleading commands; after the guard their existing "no result" messages are now correct
  (they only fire when the project IS indexed). Verified: cold probe → all 6 give the canonical
  message + JSON object; post-`index` `callers` resolves normally. Test
  `TestIndexedReportsRegistration` (false cold → true after index). Full suite + lint v2 (0) +
  query/index_status/semantic/studio E2E green; fmt clean. COMMIT+PUSH. (find/symbols/hotspots already
  self-hint — left as-is; unifying their wording onto this guard is an easy future follow-up.)
- 2026-06-23 #73 (correctness) — **`semantic` answers honestly on structure-only projects.** Before,
  `codemap semantic` / `codemap_semantic` on an un-embedded project would (a) call the embedder anyway
  — erroring if Ollama was down even though the project legitimately has no vectors, (b) lazily create
  an empty veclite file, and (c) print a misleading `no matches` (as if the symbols didn't exist).
  Now `Semantic` checks the embedded count up front via a new shared `embeddedCount` helper (never
  creates the store — absent veclite file = known 0; the same guard `Status` uses, now DRY) and
  returns early with `Mode: "none"` + a `Note`: "no embeddings for this project — run 'codemap index'
  with Ollama … or use 'codemap find' …". CLI prints the note instead of `no matches`; JSON carries
  `mode`+`note` so agents know to embed or fall back to `find`. This is the CLI/MCP counterpart of the
  studio name-mode hint (#72) — and matters more, since agents hit the CLI/MCP not the TUI. New field
  `SemanticReport.Note`. Test `TestSemanticNoEmbeddings` (default Ollama embedder, never called; mode
  "none", note set, no error). Docs updated (agent guide `commands` topic + docs/cli.md). Verified with
  an isolated-XDG `index --no-embed` → `semantic` real-CLI run (text note + JSON mode/note). Full suite
  + lint v2 (0) + semantic/index_status/query E2E green; fmt clean. COMMIT+PUSH.
- 2026-06-23 #72 (status polish) — **studio Search badge says why it isn't semantic.** When the
  search runs in name mode *because the project has no embeddings*, the header badge now reads
  `name mode (no embeddings)` (conditioned on `m.status.Vectors == 0`, so an Ollama-down-but-embedded
  project still just shows `name mode`). Completes the embedding-readiness UX thread across CLI
  `status` (#70), Metrics (#71), and Search (#72) — every surface now tells you whether semantic
  search is available and, in name mode, why not. Test `TestSearchNameModeNoEmbeddingsHint`. Full
  suite + lint v2 (0) + studio E2E green; fmt clean. COMMIT+PUSH.
- 2026-06-23 #71 (status polish) — **studio Metrics shows embedding state too** (consistency with #70).
  The Metrics header now reads `… · N embedded · semantic search ready` or `… · no embeddings — name
  search only`, reusing `m.status.Vectors` already loaded by statusCmd (no new fetch). So a studio user
  knows whether the Search tab will do semantic vs name search. Test `TestMetricsShowsEmbeddingState`
  (both states). Full suite + lint v2 (0) + studio E2E green; fmt clean. COMMIT+PUSH.
- 2026-06-23 #70 (status polish) — **`status` now reports embedded-vector count** so you can tell at a
  glance whether semantic search is available. New `vector.Store.CountByProject` (filtered Find,
  mirrors DeleteByProject); `StatusReport.Vectors`; `Status()` fills it best-effort (only if the
  veclite file exists — never creates it for structure-only projects; per-project count handles the
  shared store). CLI prints `vectors: N (semantic search ready)` or `vectors: 0 (structure-only — run
  'codemap index' with Ollama …)`; `codemap_status` JSON carries it for agents. Test
  `TestStatusReportsVectors` (0 structure-only → >0 after embedded reindex). Full suite + lint v2 (0)
  + index_status/query E2E green; fmt clean. COMMIT+PUSH.
- 2026-06-23 #69 (annotation follow-up) — **studio Search now shows the SELECTED hit's annotations**
  (not just the `⟐` row marker), consistent with the Graph/Impact panes — using `hit.Annotations`
  already enriched in #68 (no extra query): ⟐ note/data lines under the sig/doc preview (capped 2).
  Also refreshed the `codemap_docs` "annotations" topic to state annotations surface in EVERY query
  result (impact, callers/callees incl. precise, source, semantic/find) and every studio tab — so the
  agent guide matches reality. Test `TestSearchPreviewShowsSelectedAnnotations`. Full suite + lint v2
  (0) + studio E2E green; fmt clean. COMMIT+PUSH. **Annotation surfacing is now fully consistent
  across all surfaces (marker + selected-detail in every list pane).**

## 🏷️ Release — v0.2.0 (2026-06-23)
Minor release (user-requested). Since v0.1.0: the **annotation layer surfaces everywhere** — added
inline surfacing on the studio Graph pane (#66), the precise/`--lsp`+MCP-`precise` path (#67), and
Search incl. JSON enrichment for agents (#68); plus **documented agent registration** (#65: per-CLI
`mcp add` one-liners). Same proven pure-Go pipeline as v0.1.0 (goreleaser → 5 targets → homebrew-tap).
RELEASED & verified: tag `v0.2.0` on `916059f` → GitHub release (5 platform artifacts + checksums) +
homebrew-tap formula `0.2.0`; `brew upgrade` → local /opt/homebrew/bin/codemap is 0.2.0, so the
registered agents serve the new annotation features on their next session.

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
- [x] E0.5 docs/ VitePress site (Bun, Vercel) — root package.json (vitepress 1.6.4) + vercel.json
      + docs/.vitepress/config.mts + 6 pages (index/quick-start/cli/studio/mcp/configuration);
      `bun run site:build` produces dist (6 pages render). Taskfile site:* updated. node_modules/
      dist gitignored.

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
- [x] E2.1 headless LSP client (internal/lsp) — HAND-ROLLED (not go.lsp.dev; no new deps):
      Content-Length JSON-RPC 2.0 conn w/ bg read-loop + response correlation + server-request
      handler; Client Spawn/Initialize/DidOpen/DocumentSymbols/References/Shutdown/Exit + LSP
      types. Tests: fake-server round-trip + REAL gopls v0.21.0 integration (skips in CI) +
      race-clean. (Supersedes TD6's go.lsp.dev plan.)
- [~] E2.2 LSP extraction backend — lspsrc DONE (DocumentSymbols→symbols). PRECISE CALLERS
      DONE & user-visible: `callers --lsp` uses gopls callHierarchy for exact callers (funcs +
      methods; gopls names methods "(*T).M" → match base name). lsp.Client gained
      Prepare/IncomingCalls + WaitReady ($/progress end; window.workDoneProgress capability).
      DEMO: `callers Close --lsp`=7 precise vs by-name=50 inflated. Tests (gopls-gated, skip CI).
      TODO: indexer integration (precise edges at index time); callees --lsp; MCP precise flag.
- [ ] E2.3 unified extractor (merge LSP precedence over go/parser, dedupe by FQN)
- [x] E2.4 incremental hash-based reindex — already done in the indexer (E1.10)
- [~] E2.5 MCP query tools — callees/blast_radius(via impact)/path/hotspots/orphans done;
      references/symbols/dependencies still TODO

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
- [x] E4.3 Graph tab — WORKING call-graph explorer: two-column (Hubs list │ selected hub's
      Called-by / Calls), ↑/↓ navigation, full-height divider, async detail load. (Future
      enhancement: node-link/Sugiyama canvas layout; the explorer is the useful v1.)
- [x] E4.6 Full-screen layout — body fills width×height, footer pinned to bottom, bars scaled
      to width, digit (1-4) tab switching. Verified by TestRenderFillsScreen + studio snapshot.
- [x] E4.5 Search tab (semantic via Service.Semantic, async, score-ranked results)

## Epic 5 — Tests & quality
- [ ] E5.1 unit tests (graph traversal/cycles, extract, search, config) — high coverage
- [x] E5.2 glyphrun.config.yml + specs/*.yml (version, help, index_status, studio, query) —
      5 specs PASS via `task flows`; contractHashes stamped. studio.yml drives the TUI under a
      120×40 PTY; query.yml exercises callers/impact/hotspots end-to-end (snapshot source).
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
