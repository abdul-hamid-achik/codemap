# Changelog

codemap's release notes are generated per tag by GoReleaser
(`.github/workflows/release.yml`) and published on the
[GitHub releases page](https://github.com/abdul-hamid-achik/codemap/releases).
This file exists so changelog-seeking tooling has somewhere to land; the
releases page is the authoritative history.

## [Unreleased]

### Added

- **Declared inheritance edges** — `extends` / `implements` edges for TypeScript, JavaScript, and
  Python classes and interfaces (resolved through same-file and import bindings), derived
  `overrides` edges from each method to the same-named method of a direct base, and — under Go
  `index --precise` — exact `implements` (module types × module interfaces, via `go/types`) with
  method-level `overrides`. `context` gains an additive `hierarchy` block, `traverse` accepts the
  new `extends` edge type, and `orphans` no longer lists methods that override a base method.
- **Python call candidates and import edges** — without `--precise`, Python now gets same-file,
  `self`/`cls`-method, `C.m()`, and imported-binding call candidates plus file→file import edges
  (relative, absolute, and `src/`-layout), from the tree-sitter binder emulation.
- **Honest budgets for impact buckets and explore processes** — `blast_radius_total` (additive) on
  `impact` reports the true blast-radius size even when `--max-tokens` or `task-context` trims the
  list; `buckets` counts stay true totals while their node lists follow the kept `blast_radius`,
  and `max_tokens` now also trims `buckets` and explore `processes`. `processes` gains
  `entrypoints_total`, `evaluated`, and per-process `partial_errors` (additive), lists every
  definition once per process, and reports `truncated: true` when a `--query` could not inspect
  every entry point.
- **Impact depth buckets and confidence filtering** — `codemap impact` / `codemap_impact` add an
  additive `buckets` view (`direct` depth-1 vs `transitive` depth-2+, with counts) next to the
  unchanged flat `blast_radius`, and a per-node `confidence` (`confirmed` when every edge on a
  shortest path is precise or same-file, else `candidate`). New `--min-confidence confirmed|candidate`
  (MCP `min_confidence`) drops candidate nodes from `blast_radius`/`buckets`/`tests`/`direct_callers`
  and reports `filtered: {"candidate": N}`; the default is unchanged. Review `blast_radius` and
  `covering_tests` nodes carry the same `confidence`.
- **Review coverage verdict separate from risk** — `codemap review` / `codemap_review` add an
  optional `coverage` block (`verdict` covered|partial|uncovered|unknown, `covered_symbols`,
  `uncovered_symbols`, `unknown_symbols`); `unknown` means no test link and no usable call graph and
  is never evidence of missing tests. The risk band is unchanged. A test file changed or added in the
  same diff that references a changed symbol by name now counts as covering it (before reindexing).
  New `--fail-on-uncovered` exits 6 only on `uncovered`/`partial` with known-uncovered symbols, never
  on `unknown`; `--fail-on-untested` is unchanged. `gate.would_fail_on.uncovered` mirrors it in the
  report. `schemas/codemap.review.v1.schema.json` gains the optional `coverage`, node `confidence`,
  and `uncovered` properties (additive within v1).
- **`codemap processes` / `codemap_processes`** — execution flows from every entry point
  (full MCP profile; desktop Studio panel). For each `features` entry with a resolved handler
  (CLI command, HTTP route, MCP/RPC tool, page, program) it runs the `flow` builder (same-name
  ambiguity collapse included) and returns `{id, kind, name, entry selector, steps[{symbol, fqn,
  kind, file, start_line, depth}], files, truncated, call_graph}`, so "how does signup work" is
  answered with the route, handler, and service chain in one call. Computed on demand — no
  schema or stored data — and bounded by `--top` (50), `--depth` (4), and `--max-steps` (40).
  `--query` keeps processes whose name or steps match the query's content words, using the
  lexical search floor's tokenization. The full MCP profile grows to 49 tools.
- **`explore` / `task-context` group seeds by process** — the explore report gains an additive
  `processes` field (up to 3): entry flows whose steps contain a joined seed, each with
  `matched_seeds` and the call-order path from the entry to them (capped at 8 steps). Only
  entrypoints whose handler can reach a seed within the process depth are built; the list is
  empty when the project has no detected entry points.
- **Lexical search floor for `explore` / `task-context` without embeddings** — a question such
  as `how does signup work` used to return `not_found` because name search requires every word
  in one symbol. When name search finds nothing or too little, a BM25 floor now drops
  question words/stopwords and ranks definitions over symbol names, FQNs, file paths,
  docstrings, and signatures (case-insensitive substring via an FTS5 trigram index; broader
  word coverage first, production code before tests). `search_mode` reports `lexical` or
  `name+lexical`; `matched_in` may be `path` or `signature`. Graph schema v10 adds the
  contentless `nodes_fts` index, reconciled in one bulk pass after each index run (no per-row
  triggers on the write path; ~+0.1 s per 8k nodes on a full build). Upgraded databases build
  it on first use.

- **Response token budgets** — `codemap_context`, `codemap_explore`, `codemap_impact`, and
  `codemap_task_context` accept an optional `max_tokens` (CLI: `--max-tokens N` on `context`,
  `explore`, `impact`, `task-context`). The estimate is `ceil(compact JSON bytes / 4)`. Over
  budget, reports shrink deterministically, least important first: source bodies, memories and
  advisory `next`, then list tails (references, callees, callers, tests; blast radius for
  impact), then trailing explore contexts and seeds. Identity fields (`schema_version`,
  query/symbol, selectors, `call_graph`/confidence enums, freshness, `partial_errors`, `*_total`)
  are never removed, and `references_truncated` stays consistent. A budgeted result gains an
  additive `budget` object `{max_tokens, estimated_tokens, truncated, dropped}`; `schema_version`
  values are unchanged. Implementation: `internal/app/budget.go`.
- **`codemap affected` / `codemap_affected`** — changed files → the test files to run, for CI
  and pre-commit hooks that do not want to parse a review report. A test file is selected when
  it covers a changed symbol through the call graph (`covers:<symbol>`), imports a changed file
  directly or transitively up to `--depth` hops (`imports:<file>`, file-scoped imports only —
  Go imports are package-scoped), or is itself a changed test (`changed`). Changed files come
  from arguments, `--stdin` (e.g. `git diff --name-only`), `--staged`, or `--since <ref>`
  (default: the working tree); `--filter <glob>` restricts the reported tests. Human output is
  one test path per line on stdout (notes on stderr) so it pipes into a runner; `--json` emits
  `schema_version: 1` with `files`, `tests` (+`reasons`), `unmapped`, `call_graph` (weakest
  confidence among contributing symbols), `analysis_complete`, `stale` and a `note` when
  coverage is name-based or unresolved. The MCP tool is full-profile only (49 tools; agent/core
  stay at 28, so the `full` schema cost is now 54,859 characters), and Codemap Studio gets an
  "Affected tests" panel.
- **`codemap task-context` / `codemap_task_context`** — mode-scoped task orientation in one
  call (CLI alias `brief`; full-profile MCP tool). The task text is used verbatim as the
  retrieval query (intent never interpreted); `--mode understand|change|debug` selects the
  deterministic composition — understand: freshness + explore neighborhoods; change: exact
  selectors or explore-joined targets + brief context bundles + per-target impact drill-downs
  (totals before the 25-cap, one shared impact state) + related files; debug: explore with
  caller/callee-emphasized contexts. `review` is not a mode (diff-scoped analysis stays
  `codemap review`); selectors require change/debug. Freshness is always assembled-and-flagged
  (`freshness.checked` guards against a failed staleness walk reading as fresh), `call_graph`
  is the weakest across sections, `partial_errors` is capped at 20 with a truncation count, and
  next actions are advisory only. Contract: `schemas/codemap.task-context.v1.schema.json`
  (envelope-pinned, additive within v1). E2E: `specs/task_context.yml`.
- **`codemap index --force-extra <glob>`** — re-extracts matching files even when their
  content hash is unchanged, without paying a project-wide `--reindex`. Recovers a file that
  was left with an empty symbol set by the parse-wait breaker above: its hash was recorded as
  processed at the time, so a later plain incremental run skips it forever even after the
  language server would have recovered on a fresh connection. Same glob semantics as
  `--exclude-extra`; a no-op once `--reindex` is already set.
- **`codemap atlas` / `codemap_atlas`** — the repository as a described directory/file tree for
  learning an unfamiliar codebase (full MCP profile). Per node: files, symbols, lines, tests,
  roles (`source`/`tests`/`docs`/`config`/`entrypoint`/`examples`/`bench`/`generated`/`vendor`),
  a `summary` with its `summary_source` (README first paragraph, Go package doc, Python module
  docstring, leading file comment, or Markdown; extracted from the project, never generated), key
  symbols (de-noised in-degree, with selectors), inbound/outbound/internal coupling, and top
  neighbours. Flags: `--prefix`, `--depth` (default 2), `--files`, `--max-nodes`, `--key-symbols`.
  A directory without its own doc falls back to its manifest description (`package.json`,
  `pyproject.toml`, `Cargo.toml`), and a wrapper directory holding a single sub-directory (`cmd/`)
  borrows that sub-directory's summary (`summary_source: "subdirectory <name>/"`).
  JSON `schema_version: 1` with `call_graph`, `stale`, `truncated`, and `partial_errors`.
- **`codemap features` / `codemap_features`** — capability inventory (agent, core, and full
  profiles): kinds `program`, `cli_command`, `rpc_tool`, `http_route`, `api_route`, and `page`.
  Go registrations are read from the AST (cobra, urfave/cli, net/http, chi/gin/echo/gorilla and
  Go 1.22 route patterns, MCP go-sdk and mark3labs, gRPC `Register*Server`, `func main`) and
  reported `confidence: confirmed`; TS/JS (Express-style routes, commander/yargs, Next.js App and
  Pages routers, package.json `bin`) and Python (FastAPI/Flask, Django `path()`, click/typer,
  `__main__`) are pattern-detected and reported `candidate`. Each feature carries its invocation,
  description from the registration or docstring, handler selector, parent for nested commands,
  and a bounded call footprint (symbols, files, subsystems, feature-specific tests,
  `ambiguous_edges`). Flags: `--kind`, `--query`, `--top`, `--depth`, `--no-footprint`. Ruby, Lua,
  and GDScript detection is not implemented; a note says so.
- **`codemap flow <symbol>` / `codemap_flow`** — a call tree from one entry in the order the code
  calls things (agent, core, and full profiles), each step with `file:line`, subsystem, signature,
  one-line doc, and confidence. Same-name fan-out on name-based graphs is collapsed to the most
  plausible definition (`alternatives`) or left as an unexpanded `ambiguous` step with candidates;
  repeats, cycles, and depth/`max-nodes` cuts are explicit. Flags: `--at`, `--depth` (default 4),
  `--max-nodes` (default 120), `--include-tests`.
- **TS/JS call candidates in the base index** — non-`--precise` indexing now emits
  high-precision name-based call edges for same-file calls (`f()`, `new C()`, `await f()`,
  `this.m()`) and for calls through imported bindings (named, default, and namespace imports,
  `require`; relative paths, `@/`/`~/` aliases, workspace packages). Arbitrary `obj.method()`
  calls are not linked, Python still has no base-level call edges, and `call_graph` stays
  `unresolved` for uncovered TS/JS (a partial graph). `--precise` supersedes the candidates per
  file.
- **`--include-tests` on `hotspots` and `read-order`** (MCP `include_tests`) — count calls from
  test code and rank test-defined symbols; see Changed for the new default.
- **Codemap Studio** — an Electron desktop app in `desktop/` that wires codemap features into one
  interface: panels run `codemap … --json`, with a graph explorer, review desk, architecture map,
  and MCP inspector. Its **Learn** section (the landing view for an indexed project) draws the
  repository from `atlas`, `features`, and `flow`: an Overview with a persisted six-step learning
  path, a zoomable Atlas treemap with a detail panel, a Features catalog grouped by surface, and a
  Flow view (outline, left-to-right diagram, source preview, "Copy as brief"). Docs:
  `docs/desktop.md`.
- **Codemap Studio installers on every release** — the Release workflow now packages the desktop
  app for macOS (arm64 and x64 `.dmg`/`.zip`), Linux (`.AppImage`/`.tar.gz`) and Windows (NSIS
  `.exe`), each with the `codemap` CLI of the same tag bundled under `Resources/bin`, and attaches
  them to the GitHub release. The app prefers an installed `codemap` unless it is older than the
  bundled one. macOS builds are ad-hoc signed by default and become Developer ID signed and
  notarized once `CSC_LINK`/`CSC_KEY_PASSWORD` and the `APPLE_*` secrets exist. Local builds:
  `npm run dist:mac|dist:linux|dist:win` in `desktop/`.

### Changed

- **TypeScript, JavaScript, and Python index without language servers** — a new built-in
  backend (`internal/extract/sittersrc`) parses them with a pure-Go tree-sitter runtime
  (gotreesitter, no CGO) and reproduces the symbol trees `typescript-language-server` and
  `pyright-langserver` return, quirks included, through the same normalization the LSP path
  uses. The graph is unchanged (100% symbol parity against the live servers on several real
  repositories), indexing is several times faster on TypeScript-heavy repos (a 1,100-file one: 19.5 s →
  2.5 s), and Vue script blocks and `--no-lsp` now index too. Language servers are spawned only
  for `--precise`, attached to the tree-sitter backend so a precise run produces the same symbols
  and the same precise edges as before; a missing server is reported with
  `capability: "precise"` instead of skipped files. `index.structural_backend: lsp`
  (`CODEMAP_STRUCTURAL_BACKEND=lsp`) restores the previous behavior. Release binaries embed only
  the four grammars via `grammar_subset` build tags (+~6 MB).

- **Ranking ignores test code by default** — `read-order`, `hotspots`, and the hubs in `map`
  no longer count calls from tests or rank symbols defined in test files and directories, and
  rank by `effective_in_degree` (precise callers plus name-based callers divided by the number
  of same-named definitions) so a common method name stops outranking real hubs. Pass
  `--include-tests` on `hotspots` or `read-order` for the previous behavior. `map` bridges and
  subsystem edge counts also exclude test code (`tests_excluded: true`).
- **Entrypoint semantics** — the `cmd`/`main` ranking boost now applies only to `main()` and to
  functions wired by value (for example a cobra `RunE` that nothing calls directly). Methods
  never get it, and a `main()` under bench, examples, scripts, tools, or hack ranks as an
  "auxiliary program entrypoint".
- **MCP profiles** — `codemap_features` and `codemap_flow` join the taught workflow, so the
  `agent` and `core` profiles grow from 26 to 28 tools and `full` from 45 to 48 (with
  `codemap_atlas`). `BenchmarkProfileSchemaTax` now measures 35,616 schema characters
  (≈8,904 approximate tokens) for `agent`/`core` versus 53,197 (≈13,300) for `full`.
  `codemap agent setup cursor` still defaults to `core`, now 28 tools.

### Removed

- **The retired terminal Studio TUI** — `internal/tui` (unwired since the `codemap studio`
  command was dropped; nothing imported it) and its four glyphrun specs (`specs/studio*.yml`)
  are deleted; `go mod tidy` drops `chroma` and leaves `harmonica` only as an indirect
  dependency. Codemap Studio is the desktop app in `desktop/`.

### Fixed

- **Opt-in parallel `--precise`** — `index.precise_servers` / `CODEMAP_PRECISE_SERVERS` (default 1)
  forks extra language-server processes that take whole projects (nearest `tsconfig.json`/
  `jsconfig.json`/`package.json`/`pyproject.toml`) from a shared largest-first queue; an oversized
  single project is split into fair-share chunks, and a failed fork just means fewer processes. On a
  1,100-file single project, 2 processes cut the pass from 10.2 s to 7.5 s with an identical graph; on
  a many-project monorepo each process reloads shared dependencies and there is no gain, hence opt-in.

- **Fewer `--precise` coverage gaps on TS/JS** — the server is handed tree-sitter's emulation of its
  own documentSymbol tree (one request less per file, no empty-answer race); a declaration whose
  selection starts at a modifier (`private constructor(`, `export default function`) retries
  callHierarchy at its name; a call into a nested local closure lands on the indexed callable that
  contains it instead of failing the whole file; and nodes sharing a start line resolve to the
  outermost declaration. The tsserver restart budget now resets every run, so a long-lived daemon
  keeps recovering.

- **`--precise` works on large polyglot monorepos** — on a 4,500-file repo it previously covered 6 of
  3,163 TS/JS files and loaded no Go at all:
  - Go: a repo whose modules live in subdirectories (no root `go.mod`) now loads every module, and
    precise edges join their caller by declaration position — every `main.main` keeps its own calls.
  - TS/JS: tsserver ran out of memory loading a root `tsconfig.json` with no `include` and
    `typescript-language-server` then answered empty for every remaining file. tsserver's heap
    ceiling is raised to 8 GB, its death is detected, and the server is restarted (bounded) with the
    file retried; the restart count is reported. callHierarchy targets whose range starts at a long
    JSDoc block now join on the callee's name line.
  - The CLI groups floods of identical per-file errors by cause instead of printing thousands of lines.
- **Lexical seeds rank code and match inflections** — `explore`'s keyword floor stems one inflection
  ("validated" finds `validate`/`validation`), ranks functions/methods/classes before constants on
  ties, and prints a real score (the fraction of query words matched) instead of `0.000`.
- **`context` prints the hierarchy** — extends/implements/subtypes/overrides were only in `--json`;
  the JSON adds `subtypes_total`/`overridden_by_total` since those lists are capped.

- **Go call edges no longer collapse same-named symbols across packages** — the edge resolver
  looked up a call's source by FQN alone, so every `package main` program's `main.main` (or two
  packages both named `data`) shared one node: one program received every program's calls, the
  others read as calling nothing, and the same-package filter then used the wrong directory. The
  source now resolves inside the file the reference came from.
- **Name-based calls stay inside their language family** — a Go `x.String()` could link to a
  TypeScript `String` method (one real monorepo carried ~133k such cross-language edges, 43% of
  its graph). Candidates are now limited to the caller's family (Go, TS/JS/Vue, Python, Ruby, Lua).
- **A failing vecgrep owner no longer kills `explore`** — with `semantic.backend: vecgrep` and no
  vecgrep index, `explore`/`task-context` failed with a bare "exit status 1". vecgrep's own stderr
  message is now part of the error, and orientation commands fall back to name+keyword seeds with
  the failure in `owner_error` and the note; `semantic` itself still returns the error.

- **Codemap Studio Run/Stop buttons and packaged binary detection** — the generic feature panel's
  Run and Stop buttons had no click handlers (only Enter in a field ran a command); Run now runs
  and Stop cancels any run by its feature id. The packaged app probed `codemap version` from
  inside `app.asar` (a file), so spawn failed with ENOTDIR and a found binary read as "codemap:
  not found"; the probe now runs from the home directory.
- **Multi-hour LSP stall on large monorepos** — `typescript-language-server` stops returning
  symbols part-way through a big index (empty `documentSymbol`, instantly, with no error). The
  per-file parse-wait retry then burned its whole ~10s backoff ladder on every remaining file
  at ~0% CPU, so a ~3.8k-file TS/Vue repo spent 11+ hours doing nothing and looked exactly like
  a hang. A parse-wait breaker now gives up after 3 consecutive files exhaust the budget
  without recovering a symbol — shared across every language on one connection, so TS/JS/Vue
  trip together — capping the waste at ~30s. The run is reported `degraded` with a new
  `lsp_stopped_responding` tooling issue instead of claiming a complete graph.
- **No progress on a non-interactive `codemap index`** — under `--json`, a pipe, or CI nothing
  was printed until the run finished, so "slow" and "hung" were indistinguishable. A throttled
  heartbeat (phase, file N of M) now goes to **stderr** when stderr is a terminal; stdout stays
  byte-identical, so agents parsing `--json` are unaffected.
- **Stale profile claim for `codemap_explore`** — docs/README said it was full-profile-only;
  it is part of the taught workflow and registered in every profile (`codemap_task_context` joins `codemap_map`/`codemap_traverse`/`codemap_refactor_plan`
  as full-profile surfaces).
- **`codemap docs --help` lists every topic** — including `formats`.
- **`codemap_context` not-found** now carries `code:"not_found"` and a `hint`, matching the other
  MCP tools.
- **File imports resolved to exactly one file are `confirmed`** — `dependencies` marks them with
  reason `resolved_import`. As a result, `file-impact` can report `delete_verdict:"unsafe"` for
  imported TS/JS files; that is intended.
- **Go package imports point at a deterministic non-test file** — and `traverse` hops carry
  `target_scope:"package"`.

## [0.63.1] — 2026-08-22

### Fixed

- **LSP precise joins treat `node_modules` as external** — callHierarchy callees under
  `node_modules`/`vendor` are skipped during precise edge joins instead of failing
  whole-file coverage when those paths are not indexed.
- **Precise position join tolerance** — when callHierarchy lands on a doc-comment
  line, join indexed symbols within ±2 lines before marking coverage incomplete.

## [0.63.0] — 2026-08-22

### Fixed

- **LSP indexing on large monorepos** — close each document after `DidOpen`+
  `documentSymbol` and run LSP extraction plus precise `callHierarchy` with one
  in-flight request per server. Concurrent stdio requests against
  `typescript-language-server` / `pyright` could deadlock (idle server, frozen
  progress). Go files still index in parallel via `extract_concurrency`.
- **Precise pass progress** — report `precise call hierarchy…` and
  `writing precise edges…` phases so long `--precise` runs do not sit on a blank
  label.

## [0.62.0] — 2026-08-21

### Added

- **`codemap status --skip-stale`** — skips the working-tree drift walk for cheap
  readiness probes (Cortex setup). Default status still reports stale.

## [0.61.0] — 2026-08-20

### Fixed

- **Release formatting** — `gofmt` on `cmd/codemap/index_progress.go` so
  `task verify:source` passes in CI.

## [0.60.0] — 2026-08-20

### Added

- **`codemap status` is lightweight by default** — skips opening the local vector
  store so readiness probes stay bounded-memory. Pass `--full` when the exact
  local vector count is required; JSON exposes `vectors_known` to distinguish a
  skipped count from zero. `stale` is reported as
  `{changed,new,deleted}` (plus legacy int compatibility for older consumers).
- **Indexer phase progress (`OnPhase`)** — LSP spawn, wipe, precise resolution,
  and store phases report free-form labels for honest CLI/TUI progress.

### Changed

- **Safer source materialization** — incremental/staleness hashing streams
  files without retaining bodies; oversized files are rejected under a hard
  64 MiB safety ceiling even when the configured limit is “unlimited.”

## [0.59.0] — 2026-08-17

### Added

- **GDScript support (T1 symbols).** Pure-Go scanner extracts `class_name`,
  inner classes, functions, methods, signals, enums, variables, and constants
  from Godot Engine `.gd` files. Name-based call graph plus `preload`/`load`
  imports. No external dependencies — works offline like Ruby/Lua. Test-path
  detection for `*_test.gd` and `test_*.gd`.
- **Partial-success batch impact.** `codemap impact --at f1:l1 --at f2:l2 ...
  --json` resolves up to 25 raw source positions in one process and preserves
  input order. Unresolved frames return item-level `symbol_not_found` data
  without discarding successful siblings. `--batch` forces the same stable
  `ImpactBatchReport` envelope for one position; `requested`, `processed`, and
  `truncated` make the cap explicit.
- **Idempotent annotations.** CLI `annotate --external-id <id>` and MCP
  `codemap_annotate.external_id` upsert within `(project, source, external_id)`.
  Responses report `created`, `updated`, or `unchanged`; annotation reads and
  portable snapshots preserve the external ID.
- **Annotate-for-incidents pattern** documented in `docs/agents.md`: sibling
  tools (Monitor) can pin retry-safe incidents onto the call graph.

### Fixed

- **Honest per-language precise status.** `status.precise` is now derived from
  per-file `call_graph_coverage`, not the existence of any precise edge in the
  project. Mixed-language indexes no longer upgrade uncovered languages, leaf
  files with zero calls count correctly, and uncovered call-graph languages
  appear explicitly as `false`.
- Fixed Cobra validation for repeatable `impact --at`; the original StringArray
  flag could be rejected before its handler ran because it was validated as a
  scalar flag.
