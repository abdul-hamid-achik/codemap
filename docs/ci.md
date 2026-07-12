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

## Embeddings in CI (optional)

PR review never needs embeddings. If a CI job should build a *semantic* index
(for `codemap semantic`), either run Ollama as a service container — see the
commented [example workflow](https://github.com/abdul-hamid-achik/codemap/blob/main/integrations/github-action/examples/semantic-index-with-ollama.yml)
with model caching — or point `CODEMAP_OLLAMA_URL` + `CODEMAP_OLLAMA_API_KEY` at an
authenticated Ollama endpoint (see [Configuration](/configuration)).
