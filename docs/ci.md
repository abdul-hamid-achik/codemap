---
description: Add codemap impact analysis, risk bands, and test-coverage gates to GitHub or GitLab CI.
---

# codemap in CI

The same review your agent runs after an edit can gate every pull request:
changed symbols → blast radius → covering tests → a risk band, posted as one
sticky PR comment and exposed as step outputs.

## GitHub — one line

```yaml
jobs:
  review:
    uses: abdul-hamid-achik/codemap/.github/workflows/codemap-review-reusable.yml@main
    with:
      fail-on-untested: 'true'   # fail when coverage is absent or unresolved
      fail-on-risk: 'high'       # fail at or above this risk level ('' to disable)
```

Or use the action directly for more control:

```yaml
- uses: actions/checkout@v7
  with:
    fetch-depth: 0   # review --since needs merge-base history
- uses: abdul-hamid-achik/codemap/integrations/github-action@main
  id: codemap
  with:
    fail-on-untested: 'true'
- run: echo "risk=${{ steps.codemap.outputs.risk-level }}"
```

**Outputs**: `risk-level`, `risk-score`, `untested-count`, `changed-symbols-count`,
`analysis-complete`, `comment-posted`, `review-json-path` — set even when a gate fails, so downstream
steps (labeling, notifications) always have the data. The review also lands in the
job summary, which works on push events and forked PRs where commenting is blocked.
`changed-symbols-count` uses the pre-cap `total_symbols` value, while
`analysis-complete` is `true`, `false`, or `unknown` for an older/unsupported report.

**What it needs**: nothing beyond the automatic `GITHUB_TOKEN`. Indexing runs
`--precise --no-embed` — the review is purely structural, so CI needs no Ollama
and no embedding keys. For TypeScript/JavaScript/Python repos, opt into language-server
installs via the action's inputs; without them the comment says honestly that the
call graph is unresolved. Plain function calls in these languages have no name-based
fallback (the base TS/JS JSX/import/framework edges are candidates, not resolved coverage),
and `fail-on-untested: true` fails closed because their test coverage cannot be established.

The Action fails closed on infrastructure and policy ambiguity: a nonzero index or
review exit stops the run, and invalid enum/boolean inputs are rejected before setup.
Omitted booleans receive defaults, but explicitly empty boolean values are rejected.
If a configured gate receives an unknown review `schema_version`, or a v1 report whose
`analysis_complete` value is false/absent, the gate fails because it cannot safely
enforce policy from that payload. `fail-on-untested` also fails when mapped symbols'
call graph is unresolved: an empty `untested_symbols` list then means unknown, not covered.
All outputs are still published before that final
gate failure. With both gates disabled, the run is explicitly reporting-only and may
remain nonblocking while rendering a prominent schema/incomplete-analysis notice.
An otherwise complete report with `risk.level:"unknown"` still does not trip a risk
threshold; incomplete analysis is a separate fail-closed signal.

Full input/output reference: [integrations/github-action](https://github.com/abdul-hamid-achik/codemap/tree/main/integrations/github-action).

## GitLab

A thin mirror reuses the same render/gate scripts and posts via the Notes API
(needs a `CODEMAP_GITLAB_TOKEN` PAT — `CI_JOB_TOKEN` is read-only on Notes):

```yaml
include:
  - remote: 'https://raw.githubusercontent.com/abdul-hamid-achik/codemap/main/integrations/github-action/gitlab/codemap-review.yml'
```

The mirror maps its `CODEMAP_FAIL_ON_*` variables to the shared validator immediately
after downloading the scripts, before resolving/installing codemap, indexing, or posting.
Its enum/boolean validation therefore matches the GitHub Action, including rejection of
explicitly empty booleans.

## Cache the index between CI runs

A full (`--precise`) index is the expensive part — re-extracting and re-resolving call edges on
every run is wasted work when the tree hasn't changed. `codemap cache export`/`import` package
the finished index (graph + vectors) into a portable tarball with no shared store required, so
`actions/cache` can carry it between runs:

```yaml
- uses: actions/checkout@v7
- uses: actions/cache@v4
  id: codemap-index
  with:
    path: codemap-index.tar.gz
    key: codemap-index-${{ hashFiles('**/*.go', '**/*.ts', '**/*.py') }}
- run: |
    if [ -f codemap-index.tar.gz ]; then
      codemap cache import codemap-index.tar.gz || codemap index --precise --no-embed
    else
      codemap index --precise --no-embed
    fi
    codemap cache export codemap-index.tar.gz
```

`cache import` refuses (non-zero exit) on a schema/embedding-profile mismatch or a working-tree
hash that doesn't match the tarball — the `||` fallback above reindexes from scratch on any of
those, so a cache miss degrades to a normal (slower) run rather than failing the job. See
[Branches & caching](/branches#portable-tarballs-team-ci-shareable-no-fcheap-required) for the
full validation/refusal policy.

## pre-commit

For a local, pre-push gate (rather than a PR check), codemap ships a
[pre-commit](https://pre-commit.com) hook manifest at the repo root
([`.pre-commit-hooks.yaml`](https://github.com/abdul-hamid-achik/codemap/blob/main/.pre-commit-hooks.yaml)).
Add it to your project's `.pre-commit-config.yaml`:

```yaml
repos:
  - repo: https://github.com/abdul-hamid-achik/codemap
    rev: vX.Y.Z # pin a release tag
    hooks:
      - id: codemap-review
        # args: [--fail-on-untested, --fail-on-risk, high]  # override to add the risk gate
```

The hook runs `codemap review --staged --json --fail-on-untested` and fails
the commit (exit code 6 — see [CLI: Gating a commit or script](/cli#gating-a-commit-or-script))
when a staged change touches an untested symbol or its test coverage is unresolved.

**Trade-offs, by design:**

- **Name-based, not `--precise`.** The hook does not pass `--precise` — reindexing
  with the language server/`go/types` on every commit would blow past the "fast
  enough for a hook" bar. Name-based resolution is what's already indexed, so
  the hook stays well under 2s on a small diff; the cost is the usual
  same-named-method over-match on cross-package Go calls, and plain TypeScript/
  JavaScript/Python function calls get **no** call graph without `--precise`
  (TS/JS JSX/import edges are name-based candidates only). Their risk
  factor becomes `unresolved` → `level:"unknown"`, which `--fail-on-risk` never
  trips on an otherwise complete report. The default `--fail-on-untested` does fail
  closed in that state because test coverage is unknown; see the honesty rule in the CLI guide.
  Run `--precise` in CI (the
  GitHub Action above already does) where the extra seconds don't block a commit.
- **Missing setup degrades; incomplete evidence fails closed.** If the project
  has no indexed nodes yet (`codemap init` alone is not an index) or the directory isn't a git
  repository, `review` already returns a normal (not failed) report with a
  `note` explaining why — no changed symbols, no risk band, no untested list —
  so the hook exits `0`. Once an index exists, either enabled review gate requires
  `analysis_complete:true`; stale, capped, or partially mapped analysis exits `6`
  so missing evidence cannot silently pass policy. Refresh with `codemap index`
  and retry the commit.
- `entry` intentionally omits `--depth`/other tuning flags; override `args` in
  your own `.pre-commit-config.yaml` if you want a narrower or wider blast-radius
  bound.

## Embeddings in CI (optional)

PR review never needs embeddings. If a CI job should build a *semantic* index
(for `codemap semantic`), either run Ollama as a service container — see the
commented [example workflow](https://github.com/abdul-hamid-achik/codemap/blob/main/integrations/github-action/examples/semantic-index-with-ollama.yml)
with model caching — or point `CODEMAP_OLLAMA_URL` + `CODEMAP_OLLAMA_API_KEY` at an
authenticated Ollama endpoint (see [Configuration](/configuration)).
