# Changelog

codemap's release notes are generated per tag by GoReleaser
(`.github/workflows/release.yml`) and published on the
[GitHub releases page](https://github.com/abdul-hamid-achik/codemap/releases).
This file exists so changelog-seeking tooling has somewhere to land; the
releases page is the authoritative history.

## [Unreleased]

### Added

- **`codemap task-context` / `codemap_task_context`** — mode-scoped task orientation in one
  call (CLI alias `brief`; 45th full-profile MCP tool). The task text is used verbatim as the
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
  it is part of the taught workflow and registered in every profile (agent/core stay at 26
  tools; `codemap_task_context` joins `codemap_map`/`codemap_traverse`/`codemap_refactor_plan`
  as full-profile surfaces).

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
