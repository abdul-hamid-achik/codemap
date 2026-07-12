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
      fail-on-untested: 'true'   # fail when a load-bearing change has no covering tests
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
`comment-posted`, `review-json-path` — set even when a gate fails, so downstream
steps (labeling, notifications) always have the data. The review also lands in the
job summary, which works on push events and forked PRs where commenting is blocked.

**What it needs**: nothing beyond the automatic `GITHUB_TOKEN`. Indexing runs
`--precise --no-embed` — the review is purely structural, so CI needs no Ollama
and no embedding keys. For TypeScript/Python repos, opt into language-server
installs via the action's inputs; without them the comment says honestly that the
call graph is name-based.

Full input/output reference: [integrations/github-action](https://github.com/abdul-hamid-achik/codemap/tree/main/integrations/github-action).

## GitLab

A thin mirror reuses the same render/gate scripts and posts via the Notes API
(needs a `CODEMAP_GITLAB_TOKEN` PAT — `CI_JOB_TOKEN` is read-only on Notes):

```yaml
include:
  - remote: 'https://raw.githubusercontent.com/abdul-hamid-achik/codemap/main/integrations/github-action/gitlab/codemap-review.yml'
```

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
when a staged change touches an untested symbol.

**Trade-offs, by design:**

- **Name-based, not `--precise`.** The hook does not pass `--precise` — reindexing
  with the language server/`go/types` on every commit would blow past the "fast
  enough for a hook" bar. Name-based resolution is what's already indexed, so
  the hook stays well under 2s on a small diff; the cost is the usual
  same-named-method over-match on cross-package Go calls, and TypeScript/
  JavaScript/Python get **no** call graph at all without `--precise` (their risk
  factor becomes `unresolved` → `level:"unknown"`, which `--fail-on-risk` never
  trips — see the honesty rule in the CLI guide). Run `--precise` in CI (the
  GitHub Action above already does) where the extra seconds don't block a commit.
- **Degrades to exit 0, never blocks on missing infrastructure.** If the project
  hasn't been indexed yet (`codemap index`) or the directory isn't a git
  repository, `review` already returns a normal (not failed) report with a
  `note` explaining why — no changed symbols, no risk band, no untested list —
  so the gate has nothing to trip on and exits 0. A missing/stale index is a
  reason to run `codemap index`, not a reason to block every commit.
- `entry` intentionally omits `--depth`/other tuning flags; override `args` in
  your own `.pre-commit-config.yaml` if you want a narrower or wider blast-radius
  bound.

## Embeddings in CI (optional)

PR review never needs embeddings. If a CI job should build a *semantic* index
(for `codemap semantic`), either run Ollama as a service container — see the
commented [example workflow](https://github.com/abdul-hamid-achik/codemap/blob/main/integrations/github-action/examples/semantic-index-with-ollama.yml)
with model caching — or point `CODEMAP_OLLAMA_URL` + `CODEMAP_OLLAMA_API_KEY` at an
authenticated Ollama endpoint (see [Configuration](/configuration)).
