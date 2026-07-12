# codemap GitHub Action

A composite GitHub Action (+ a thin GitLab CI mirror) that runs
[`codemap review`](https://github.com/abdul-hamid-achik/codemap) against a pull request's diff
and posts the result — changed symbols, blast radius, untested symbols, hotspots, and an
aggregate risk band — as a single sticky PR comment. Optional inputs can fail the check on
untested changes or on a risk threshold.

This is an **adoption/distribution play**, not a new analysis engine: all the intelligence comes
from `codemap review --json` (see `schemas/codemap.review.v1.schema.json` in the codemap repo);
this repo only installs the binary, runs it, renders its JSON to Markdown, posts/updates the
comment, and optionally gates the check on the JSON body.

## What it does, in order

1. **Install** — resolves the `version` input (`latest` pins to an exact tag via the GitHub
   Releases API, once, at job start — it never floats mid-job), downloads the matching GoReleaser
   archive, verifies it against the release's `checksums.txt`, and puts `codemap` on `PATH`.
   Cached by `actions/cache` keyed on `tag-os-arch` so a repeat run on the same commit/runner
   doesn't re-download.
2. **Index** — `codemap init` then `codemap index --no-embed [--precise]` (`--no-embed`: no
   Ollama in CI; `--precise` needs the go toolchain for Go and/or
   `typescript-language-server`/`pyright-langserver` for TS/JS/Python — opt into installing those
   with `install-ts-language-server`/`install-pyright`, or let codemap degrade to a name-based
   call graph and say so in the comment).
3. **Review** — `codemap review --since <base-sha> --depth <depth> --json`.
4. **Render** — a bash+jq script (`scripts/render-comment.sh`) turns the JSON into Markdown,
   capped under GitHub's 65 536-character comment limit (default soft budget 60 000; sections are
   dropped to counts-only, in order — blast radius, then changed symbols, then changed files — if
   needed; the risk band and untested-symbols headline are never truncated).
5. **Post** — creates or updates **one** sticky comment, keyed by a hidden HTML marker
   (`<!-- codemap-review-action:marker -->`), via `actions/github-script`.
6. **Gate** (optional) — `fail-on-untested`/`fail-on-risk` fail the *Action's own step* by reading
   the JSON body directly. **This does not use `codemap review`'s process exit code** — see
   [Gotcha](#gotcha-codemap-reviews-exit-code-is-not-a-gate-signal) below.

## GitHub usage

```yaml
- uses: actions/checkout@v7
  with:
    fetch-depth: 0 # REQUIRED — see below

- uses: abdul-hamid-achik/codemap/integrations/github-action@main
  with:
    fail-on-untested: 'true'
    fail-on-risk: 'high'
```

`fetch-depth: 0` is **mandatory**. `codemap review --since <base-sha>` needs the merge-base
history; a composite action step cannot deepen a shallow checkout after the fact. This action
fails fast with a clear message (rather than a cryptic git error) if the checkout is shallow.

### Inputs

| Input | Default | Description |
|---|---|---|
| `version` | `latest` | codemap release to install (e.g. `v0.40.0`); `latest` is resolved to one exact tag at job start |
| `precise` | `true` | run `codemap index --precise` (exact call edges; degrades honestly without the right toolchain/LSP) |
| `depth` | `3` | blast-radius depth passed to `codemap review --depth` |
| `install-ts-language-server` | `false` | `npm install -g typescript-language-server typescript` before indexing |
| `install-pyright` | `false` | `npm install -g pyright` before indexing |
| `fail-on-untested` | `false` | fail the check if any changed symbol has no covering test |
| `fail-on-risk` | `` (disabled) | fail the check if the aggregate `risk.level` is at or above this band: `low`\|`medium`\|`high` |
| `base-sha` | `github.event.pull_request.base.sha \|\| github.event.before` | commit to diff against; set explicitly for events other than `pull_request`/`push` |
| `github-token` | `github.token` | token used to post/update the sticky comment |
| `skip-comment` | `false` | compute + gate, but don't post a PR comment (useful outside `pull_request` events, e.g. a push-triggered smoke check) |

### Outputs

| Output | Description |
|---|---|
| `risk-level` | the aggregate `risk.level` (`unknown`\|`low`\|`medium`\|`high`), or `absent` when the diff touched no indexed symbols |
| `untested-count` | count of `untested_symbols` |
| `review-json-path` | path to the raw `codemap review --json` output, for downstream steps |

Gate outputs are set **even when the gate step fails** (`>> $GITHUB_OUTPUT` runs before `exit 1`),
so a downstream step in the same job can still read them.

### `--no-embed` is intentional, not an oversight

Semantic search needs a local Ollama; there isn't one in CI, and `review`'s JSON never carries
semantic fields regardless — omitting `--no-embed` would just make `index` hang/fail trying to
reach `http://localhost:11434`.

### Gotcha: `codemap review`'s exit code is not a gate signal

`Review()` (`internal/app/review.go` in the codemap repo) is explicitly documented to degrade
gracefully and return `ok:true` even for a high-risk, fully-untested, non-indexed, or non-repo
diff — "*Never errors on a non-repo or unindexed project — it degrades to a plain changed-file
list with a Note so an agent always gets an actionable answer.*" A fully-untested, high-risk diff
is still a **successful** `codemap review` call. Confirmed against `cmd/codemap/main.go`: there is
no `--fail-on-risk`/`--fail-on-untested` flag on `codemap review` itself today. So `gate.sh` reads
`.risk.level` and `.untested_symbols | length` straight out of the JSON body — never the process
exit code. `run-review.sh` only treats a `codemap review` invocation as an *operational* failure
when it prints a structured `{"ok":false,...}` envelope (a real crash/bad-flag/git-failure case,
per `cmd/codemap/errors.go`'s exit taxonomy), which is a completely separate concept from the
risk/untested gate.

> **TODO(I10)**: if/when codemap ships native `--fail-on-risk`/`--fail-on-untested` exit codes on
> `codemap review` itself, `gate.sh`'s jq-based threshold check becomes a one-line swap to reading
> codemap's own exit code, and this whole section goes away.

### The risk-level ordinal is explicit, not lexical

`risk.level` is one of `unknown|low|medium|high`. `gate.sh` maps these through an explicit ordinal
table (`low=1, medium=2, high=3, anything else=-1`) rather than comparing strings — so
`risk.level: "unknown"` can never coincidentally satisfy `fail-on-risk: high` by string comparison
luck, and a diff with **no** `risk` object at all (nothing indexed was touched) never trips either
gate. `fail-on-risk` is inclusive of its own threshold: `fail-on-risk: low` fails on `low`,
`medium`, or `high`.

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

The GitLab template reuses `install-codemap.sh`, `run-review.sh`, `render-comment.sh`, and
`gate.sh` **completely unmodified** — that's the entire reason they're bash+jq instead of
`actions/github-script`-flavored JS. Only the comment-posting call differs
(`post-comment-gitlab.sh`, curl + GitLab's [Notes API](https://docs.gitlab.com/api/notes/), vs.
`post-comment.js` + Octokit on GitHub). See `gitlab/codemap-review.yml` for the full job
definition and variable list.

## Development

```
task action:test   # from the repo root (or ./test/test.sh from this dir)
task action:lint  # shellcheck + yamllint, best-effort locally; both run in CI regardless
```

`test/test.sh` is a plain-bash harness (no `bats-core` dependency assumed) that exercises
`render-comment.sh` and `gate.sh` against every fixture in `testdata/` — including a real
`codemap review --json` output generated by building codemap from source and running it against
a scratch git repo (`testdata/real-since-untested-high-risk.json`) and the project's own golden
contract fixture (`testdata/golden-contract.json`, copied from
`internal/app/testdata/contracts/codemap.review.v1.json` in the codemap repo) — plus the `gate.sh`
ordinal table, `resolve-version.sh`'s archive-name construction for `linux/amd64`,
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
