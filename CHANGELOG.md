# Changelog

codemap's release notes are generated per tag by GoReleaser
(`.github/workflows/release.yml`) and published on the
[GitHub releases page](https://github.com/abdul-hamid-achik/codemap/releases).
This file exists so changelog-seeking tooling has somewhere to land; the
releases page is the authoritative history.

## [Unreleased]

### Added

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

### Changed

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

### Fixed

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
