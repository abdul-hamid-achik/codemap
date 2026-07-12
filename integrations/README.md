# integrations/

Distribution surfaces for codemap that live outside the core Go module — each is a
self-contained package with its own tests and its own docs; this file is just the index.

| Directory | What it is | Documented in | Tested by |
|---|---|---|---|
| `claude-code/` | An agent-harness distribution: a Claude Code plugin (MCP server registration + `/codemap-setup` command + the `using-codemap` skill) that teaches an agent when to reach for codemap's tools. | `claude-code/.claude-plugin/plugin.json`, `claude-code/skills/using-codemap/SKILL.md` | manual/agent-driven verification (no automated suite today — see `claude-code/commands/codemap-setup.md` for the setup flow this exercises) |
| `github-action/` | A composite GitHub Action (+ a thin GitLab CI mirror) that runs `codemap review` against a PR's diff and posts the result as a sticky comment, a job summary, and a set of machine-readable outputs. | `github-action/README.md` | `task action:test` (bash+jq harness, `github-action/test/test.sh`) + `task action:lint` (shellcheck/yamllint); wired into CI as the `action` job in `.github/workflows/ci.yml` |

## Adding a new integration

Give it its own directory here, its own README (or doc-comments if it's config-only), and its own
test entry point — wire that entry point into the root `Taskfile.yml`'s `includes:` (see
`github-action/Taskfile.yml`, included as the `action:` namespace) and into `.github/workflows/ci.yml`
so it runs on every PR. Don't scatter integration-specific scripts or docs outside this directory —
`docs/` (VitePress, product docs) and the Obsidian vault (working notes) are for the core product,
not for integration internals.
