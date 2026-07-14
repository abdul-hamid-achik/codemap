# codemap GitHub Action

A composite GitHub Action (+ a thin GitLab CI mirror) that runs
[`codemap review`](https://github.com/abdul-hamid-achik/codemap) against a pull request's diff
and posts the result — changed symbols, blast radius, untested symbols, hotspots, and an
aggregate risk band — as a single sticky PR comment **and** a job summary. Optional inputs can
fail the check on untested changes or on a risk threshold, and every run exposes machine-readable
outputs for downstream steps (label a PR by risk, gate a deploy, post to Slack, ...).

This is an **adoption/distribution play**, not a new analysis engine: all the intelligence comes
from `codemap review --json` (see `schemas/codemap.review.v1.schema.json` in the codemap repo);
this repo only installs the binary, runs it, renders its JSON to Markdown, posts/updates the
comment, writes the job summary, and optionally gates the check on the JSON body.

## What it does, in order

1. **Validate** — checks configuration before downloading or running anything;
   `fail-on-risk` must be empty, `low`, `medium`, or `high`, and every boolean input
   (`precise`, language-server installs, `fail-on-untested`, and `skip-comment`) must be
   exactly `true` or `false`. Omitted booleans receive their documented defaults; explicitly
   empty booleans are invalid rather than being mistaken for an omission.
2. **Install** — resolves the `version` input (`latest` pins to an exact tag via the GitHub
   Releases API, once, at job start — it never floats mid-job), downloads the matching GoReleaser
   archive, verifies it against the release's `checksums.txt`, and puts `codemap` on `PATH`.
   Cached by `actions/cache` keyed on `tag-os-arch` so a repeat run on the same commit/runner
   doesn't re-download.
3. **Index** — `codemap init` then `codemap index --no-embed [--precise]` (`--no-embed`: no
   Ollama in CI; `--precise` needs the go toolchain for Go and/or
   `typescript-language-server`/`pyright-langserver` for TS/JS/Python — opt into installing those
   with `install-ts-language-server`/`install-pyright`; without them, those LSP-backed languages
   report an unresolved call graph). A nonzero index exit is an operational failure and stops the
   Action rather than reviewing a potentially partial or stale index. Want semantic search too? See
   [`examples/semantic-index-with-ollama.yml`](examples/semantic-index-with-ollama.yml).
4. **Review** — `codemap review --since <base-sha> --depth <depth> --json`. Because no native
   gate flags are passed here, any nonzero process exit is an operational failure even if stdout
   happens to resemble a valid report.
5. **Render** — a bash+jq script (`scripts/render-comment.sh`) turns the JSON into Markdown,
   capped under GitHub's 65 536-character comment limit (default soft budget 60 000; sections are
   dropped to counts-only, in order — blast radius, then changed symbols, then changed files — if
   needed; the analysis-completeness warning, risk band, and untested-symbols headline are never
   truncated). An incomplete report scopes empty test findings to the successfully analyzed subset
   and never claims that every changed symbol is covered.
6. **Post** — creates or updates **one** sticky comment, keyed by a hidden HTML marker
   (`<!-- codemap-review-action:marker -->`), via `actions/github-script`.
7. **Summarize** — appends the *same* rendered Markdown to `$GITHUB_STEP_SUMMARY`
   (`scripts/write-summary.sh`), unconditionally (`if: always()`). This is the surface that still
   works when step 6 doesn't apply: push events (no PR to comment on), forks without PR-write
   permission, or `skip-comment: true`. It reuses step 5's rendered file rather than re-deriving
   Markdown from the JSON, so there is exactly one rendering code path for both surfaces.
8. **Gate** (optional) — `fail-on-untested`/`fail-on-risk` fail the *Action's final step* by reading
   the JSON body directly. The Action deliberately invokes `codemap review` without its native
   gate flags, so rendering, the sticky comment, the job summary, and outputs are all produced
   before the check fails. Action inputs are validated before setup begins. A gate also fails closed
   when `analysis_complete` is not `true`, since a partial result cannot safely enforce policy.
   The untested gate also fails when mapped symbols' call graph leaves test coverage unresolved. See
   [Gate sequencing](#gate-sequencing-vs-codemaps-native-exit-6) below.

## Adoption: reusable workflow vs. direct action

Two ways to wire this into a consumer repo — pick based on how much control you need.

**Reusable workflow (simplest — one line)**: the whole job — checkout, `fetch-depth: 0`, and the
action — is already wired up for you.

```yaml
jobs:
  review:
    uses: abdul-hamid-achik/codemap/.github/workflows/codemap-review-reusable.yml@main
    with:
      fail-on-untested: 'true'
      fail-on-risk: 'high'
```

**Direct action (more control)**: write your own job when you need custom triggers, extra steps
before/after the review, a matrix build, or a different checkout configuration.

```yaml
- uses: actions/checkout@v7
  with:
    fetch-depth: 0 # REQUIRED — see below

- uses: abdul-hamid-achik/codemap/integrations/github-action@main
  with:
    fail-on-untested: 'true'
    fail-on-risk: 'high'
```

`fetch-depth: 0` is **mandatory** either way. `codemap review --since <base-sha>` needs the
merge-base history; a composite action step cannot deepen a shallow checkout after the fact. This
action fails fast with a clear message (rather than a cryptic git error) if the checkout is
shallow. See [`examples/basic-review.yml`](examples/basic-review.yml) for a complete direct-action
workflow, and
[`.github/workflows/codemap-review-reusable.yml`](../../.github/workflows/codemap-review-reusable.yml)
(repo root) for the reusable workflow's source — it's a thin wrapper around the same action, so
both paths run identical logic and expose identical outputs.

## GitHub usage (direct action, in detail)

### Inputs

| Input | Default | Description |
|---|---|---|
| `version` | `latest` | codemap release to install (e.g. `v0.40.0`); `latest` is resolved to one exact tag at job start |
| `precise` | `true` | `true`/`false`; run `codemap index --precise` (exact call edges; degrades honestly without the right toolchain/LSP) |
| `depth` | `3` | blast-radius depth passed to `codemap review --depth` |
| `install-ts-language-server` | `false` | `true`/`false`; install `typescript-language-server` + TypeScript before indexing |
| `install-pyright` | `false` | `true`/`false`; install Pyright before indexing |
| `fail-on-untested` | `false` | `true`/`false`; fail if a changed symbol lacks a covering test or mapped symbols' test coverage is unresolved |
| `fail-on-risk` | `` (disabled) | fail the check if the aggregate `risk.level` is at or above this band: `low`\|`medium`\|`high`; any other value fails before setup |
| `base-sha` | `github.event.pull_request.base.sha \|\| github.event.before` | commit to diff against; set explicitly for events other than `pull_request`/`push` |
| `github-token` | `github.token` | token used to post/update the sticky comment |
| `skip-comment` | `false` | `true`/`false`; compute + gate, but don't post a PR comment (useful outside `pull_request` events) |

### Outputs

| Output | Description |
|---|---|
| `risk-level` | the aggregate `risk.level` (`unknown`\|`low`\|`medium`\|`high`), or `absent` when the diff touched no indexed symbols |
| `risk-score` | the aggregate `risk.score` (`0`..`1`), or `0` when risk was not computed |
| `untested-count` | count of `untested_symbols` |
| `changed-symbols-count` | total mapped symbol count from `total_symbols`; falls back to `changed_symbols` length for older reports |
| `analysis-complete` | `true` when the full diff was analyzed, `false` when codemap reports partial analysis, or `unknown` for an older/unsupported report shape |
| `comment-posted` | `"true"` if the sticky PR comment was created/updated this run, `"false"` if skipped (`skip-comment: true`, a non-`pull_request` event, or the render/post step failed before posting) |
| `review-json-path` | path to the raw `codemap review --json` output, for downstream steps |

`risk-level`/`risk-score`/`untested-count`/`changed-symbols-count`/`analysis-complete` are set
**even when the gate step fails** (`>> $GITHUB_OUTPUT` runs before `exit 1` in `gate.sh`), so a
downstream step in the same job can still read them regardless of
`fail-on-untested`/`fail-on-risk`.

Downstream-step example — label a PR by risk level instead of (or alongside) failing the check:

```yaml
- uses: abdul-hamid-achik/codemap/integrations/github-action@main
  id: codemap
  with:
    fail-on-risk: '' # don't fail the check — just label

- name: Label PR by risk level
  if: github.event_name == 'pull_request' && steps.codemap.outputs.risk-level != 'absent'
  env:
    GH_TOKEN: ${{ github.token }}
  run: |
    gh pr edit "${{ github.event.pull_request.number }}" \
      --add-label "codemap-risk:${{ steps.codemap.outputs.risk-level }}"
```

(See [`examples/basic-review.yml`](examples/basic-review.yml) for this wired into a full
workflow.)

### `--no-embed` is intentional, not an oversight

Semantic search needs a local Ollama; there isn't one in CI, and `review`'s JSON never carries
semantic fields regardless — omitting `--no-embed` would just make `index` hang/fail trying to
reach `http://localhost:11434`.

### Gate sequencing vs. codemap's native exit 6

`Review()` (`internal/app/review.go` in the codemap repo) still degrades gracefully and returns a
normal success report for high-risk, fully-untested, or diffs that map to no indexed symbols. The CLI now
adds native `--fail-on-risk <low|medium|high>` and `--fail-on-untested` gates: after printing that
same unchanged success report (including under `--json`), it exits **6** when a configured gate
trips. Exit 6 is deliberately not an `{"ok":false,...}` operational-error envelope.

This composite Action does not pass those native gate flags to `codemap review`. It needs the
successful JSON body first so it can render/post the report, write the summary, and expose outputs;
then its final `gate.sh` step applies the same risk/untested contract and fails the Action. Keeping
the gate last also means risk, count, and analysis-completeness outputs are available even when the
check fails. `run-review.sh` treats a structured `{"ok":false,...}` envelope as an operational
failure; it also rejects any nonzero `codemap index` or `codemap review` exit rather than accepting
a partial/stale index or valid-looking stdout from an unsuccessful process. Missing language
servers remain an accuracy limitation, not an operational failure: codemap
returns success and reports the affected call graph as unresolved. The Action requires a real,
non-shallow git checkout and fails before indexing when that precondition is missing. With native
gates intentionally absent from the review invocation, exit 6 is not expected there.

Gate configuration is validated before version resolution or installation. Invalid
`fail-on-risk` values do not disable the gate silently; they fail with the accepted values. An
unknown `schema_version` also fails whenever either gate is enabled, because applying policy to an
unrecognized payload would be unsafe. A recognized schema-v1 report likewise fails a configured
gate when `analysis_complete` is `false` or absent (older v1 producers), while preserving all gate
outputs for downstream diagnosis. `fail-on-untested` also fails closed when mapped symbols have
`call_graph: unresolved|none`, because an empty list then means coverage is unknown. When both
gates are disabled, the Action is explicitly a
reporting-only integration: it renders a prominent incomplete/compatibility notice and remains
nonblocking so partial analysis or an upgrade mismatch does not take down an observation-only
workflow.

### The risk-level ordinal is explicit, not lexical

`risk.level` is one of `unknown|low|medium|high`. `gate.sh` maps these through an explicit ordinal
table (`low=1, medium=2, high=3, anything else=-1`) rather than comparing strings — so
`risk.level: "unknown"` can never coincidentally satisfy `fail-on-risk: high` by string comparison
luck, and a diff with **no** `risk` object at all (nothing indexed was touched) never trips either
content gate. This rule is unchanged: an otherwise complete report with unknown risk passes a risk
threshold. Separately, `analysis_complete != true` fails any configured policy gate because the
report is partial, not because unknown risk was treated as high. `fail-on-risk` is inclusive of its
own threshold: `fail-on-risk: low` fails on `low`, `medium`, or `high`. The untested gate is
separate: mapped symbols with an unresolved/absent call graph cannot establish test coverage, so
an empty `untested_symbols` list does not pass `fail-on-untested`.

## GitLab CI usage

```yaml
include:
  - remote: 'https://raw.githubusercontent.com/abdul-hamid-achik/codemap/main/integrations/github-action/gitlab/codemap-review.yml'
```

Requires a CI/CD variable `CODEMAP_GITLAB_TOKEN` — a masked project or personal access token with
the `api` scope. **`$CI_JOB_TOKEN` cannot create or update merge request notes** (verified against
GitLab's CI/CD job token docs, 2026-07: the job token can only `GET` notes, not `POST`/`PUT`
them) — `scripts/post-comment-gitlab.sh` requires `CODEMAP_GITLAB_TOKEN` explicitly rather than
silently falling back to a token that would just 403.

The GitLab template reuses `resolve-version.sh`, `install-codemap.sh`, `run-review.sh`,
`render-comment.sh`, and `gate.sh` **completely unmodified** — that's the entire reason they're
bash+jq instead of `actions/github-script`-flavored JS. Only the comment-posting call differs
(`post-comment-gitlab.sh`, curl + GitLab's [Notes API](https://docs.gitlab.com/api/notes/), vs.
`post-comment.js` + Octokit on GitHub). See `gitlab/codemap-review.yml` for the full job
definition and variable list. `install-language-servers.sh` and `write-summary.sh` are GitHub-only
today — the GitLab template doesn't install language servers or write a job summary (GitLab has no
`$GITHUB_STEP_SUMMARY` equivalent; a merge-request note is its only report surface).
It maps `CODEMAP_FAIL_ON_UNTESTED`/`CODEMAP_FAIL_ON_RISK` to the shared gate variables and runs
`gate.sh --validate-inputs` immediately after downloading the scripts, before resolving or
installing codemap, indexing, rendering, or posting. GitLab therefore rejects malformed and
explicitly empty boolean inputs with the same early-failure contract as the GitHub Action.

## Development

```
task action:test   # from the repo root (or ./test/test.sh from this dir)
task action:lint  # shellcheck + yamllint, best-effort locally; both run in CI regardless
```

`test/test.sh` is a plain-bash harness (no `bats-core` dependency assumed) that exercises
`render-comment.sh` and `gate.sh` against every fixture in `testdata/`, validates gate inputs,
and mocks `run-review.sh` to pin index/review process failures and checkout-precondition behavior — including a real
`codemap review --json` output generated by building codemap from source and running it against
a scratch git repo (`testdata/real-since-untested-high-risk.json`) and the project's own golden
contract fixture (`testdata/golden-contract.json`, copied from
`internal/app/testdata/contracts/codemap.review.v1.json` in the codemap repo) — plus the `gate.sh`
ordinal/completeness behavior (including `risk-score`, total `changed-symbols-count`, and
`analysis-complete` outputs),
GitLab's validate-before-work ordering and unset-versus-empty input defaults,
`write-summary.sh`'s `$GITHUB_STEP_SUMMARY` fallback path (present file, missing file, and unset
`$GITHUB_STEP_SUMMARY`), `resolve-version.sh`'s archive-name construction for `linux/amd64`,
`darwin/arm64`, and `windows/amd64` (+ the `windows/arm64` rejection), and
`install-codemap.sh`'s checksum verification (a forced mismatch, a cache-hit skip, and — network
permitting — a real end-to-end download against the live `v0.40.0` release).

Iterate on `action.yml` locally without pushing tags using
[`nektos/act`](https://github.com/nektos/act) (a dev-loop convenience, not a CI requirement):

```
act pull_request -W .github/workflows/ci.yml -j smoke
```

## Verified-not-assumed facts (2026-07)

Per the source plan's instruction to verify rather than trust memory:

- **GoReleaser archive naming** — confirmed against the real, published `v0.40.0` release
  (`gh release view v0.40.0 --repo abdul-hamid-achik/codemap`, and its `checksums.txt`):
  `codemap_<version-without-v>_<os>_<arch>.tar.gz` (`.zip` on Windows), lowercase `os`/`arch`,
  flat archive (`codemap`/`codemap.exe` at the root, no subdirectory).
- **Current major action versions** (checked via the GitHub API's tag/release lists, not
  training-data memory): `actions/checkout@v7`, `actions/github-script@v9`, `actions/cache@v6`.
- **`actions/github-script`'s `require(...)`-a-sibling-file pattern** for keeping script logic out
  of the inline YAML `script:` block is the documented approach for that action.
- **GitLab's `$CI_JOB_TOKEN` Notes API scope** — confirmed via GitLab's CI/CD job token docs that
  it's read-only on the Notes API; write access needs a project/personal access token.
- **`ollama/ollama`'s Docker image** (used by `examples/semantic-index-with-ollama.yml`) — the
  official image serves on port `11434` (`OLLAMA_HOST=0.0.0.0:11434`), and models default to
  `/root/.ollama` inside the container. Confirmed via [Ollama's Docker
  docs](https://docs.ollama.com/docker) and the [Docker Hub
  listing](https://hub.docker.com/r/ollama/ollama).
- **The official `ollama/ollama` image ships without `curl`** — a known gap
  ([ollama/ollama#9781](https://github.com/ollama/ollama/issues/9781)), which breaks Docker-native
  `HEALTHCHECK`/`--health-cmd` (those exec *inside* the container). The example workflow polls
  readiness from the **job** instead (`curl` runs fine on the `ubuntu-latest` runner, hitting the
  service's port-mapped `localhost:11434`), which sidesteps the missing binary entirely.
- **`POST /api/pull` accepts `{"model": "<name>", "stream": false}`** and returns one blocking JSON
  response instead of an NDJSON progress stream when `stream: false` — confirmed via [Ollama's API
  docs](https://docs.ollama.com/api/pull). This is what the example workflow uses to pull
  `nomic-embed-text` without parsing a stream.
- **`CODEMAP_OLLAMA_API_KEY`** is already implemented (`internal/config/config.go`'s `applyEnv`,
  alongside `CODEMAP_OLLAMA_URL`) — confirmed by reading the codemap source directly, not assumed.
  It authenticates against a hosted/authenticated Ollama endpoint (e.g. Ollama Cloud) as an
  alternative to running Ollama as a CI service container; see
  `examples/semantic-index-with-ollama.yml`'s closing comment block.
