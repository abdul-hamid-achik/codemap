# Changelog

codemap's release notes are generated per tag by GoReleaser
(`.github/workflows/release.yml`) and published on the
[GitHub releases page](https://github.com/abdul-hamid-achik/codemap/releases).
This file exists so changelog-seeking tooling has somewhere to land; the
releases page is the authoritative history.

## [Unreleased]

### Added

- **Batch impact API.** `codemap impact --at f1:l1 --at f2:l2 ... --json`
  resolves impact for multiple source positions in one call (≤25), reducing
  N subprocess round-trips to 1. The `--at` flag is now a repeatable
  StringArray (same pattern as `context`). A new `ImpactBatchReport` JSON
  shape carries per-position results.
- **Per-language precise status.** `codemap status --json` now includes a
  `precise` map (`{"go": true, "typescript": false}`) so consumers like
  Monitor know whether blast-radius/impact results are trustworthy for a
  given language or structural-only.
- **Annotate-for-incidents pattern** documented in `docs/agents.md`: sibling
  tools (Monitor) can `codemap annotate <fqn> --source monitor --note
  "<stash-id>: <diagnosis>"` to pin incidents onto the call graph.
