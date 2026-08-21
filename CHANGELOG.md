# Changelog

codemap's release notes are generated per tag by GoReleaser
(`.github/workflows/release.yml`) and published on the
[GitHub releases page](https://github.com/abdul-hamid-achik/codemap/releases).
This file exists so changelog-seeking tooling has somewhere to land; the
releases page is the authoritative history.

## [Unreleased]

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
