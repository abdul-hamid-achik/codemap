# Changelog

codemap's release notes are generated per tag by GoReleaser
(`.github/workflows/release.yml`) and published on the
[GitHub releases page](https://github.com/abdul-hamid-achik/codemap/releases).
This file exists so changelog-seeking tooling has somewhere to land; the
releases page is the authoritative history.

## [Unreleased]

### Added

- **GDScript support (T1 symbols + T2 navigation).** Pure-Go scanner extracts
  `class_name`, inner classes, functions, methods, signals, enums, variables,
  and constants from Godot Engine `.gd` files. Name-based call graph plus
  `preload`/`load` imports. No external dependencies — works offline like
  Ruby/Lua. Test-path detection for `*_test.gd` and `test_*.gd`.
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
