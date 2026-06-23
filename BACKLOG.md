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
