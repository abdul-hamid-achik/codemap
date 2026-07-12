# codemap agent benchmark (DIRECTIONAL)

A re-runnable A/B eval that quantifies codemap's core pitch — *"an agent spends a few
tool calls instead of dozens of file reads"* — by running a headless coding agent over a
fixed fixture repo **with** vs **without** the codemap MCP server, and grading both against
ground truth derived **independently of codemap**.

**This is a proof artifact, not a product feature.** It adds no CLI/MCP/TUI surface. Every
published number is **DIRECTIONAL**: real LLM sessions are nondeterministic, the suite is
small, and this is not a controlled study. Numbers are stamped DIRECTIONAL wherever they
appear.

## What it measures

Two arms, identical except for the codemap MCP config:

| arm | tools | MCP |
|---|---|---|
| `baseline` | `Read,Grep,Glob` | none |
| `codemap` | `Read,Grep,Glob,mcp__codemap` | codemap `serve` over an isolated index |

Per session (one task × arm × repetition) the harness records: **tool calls** (the headline
metric — a count of `tool_use` content blocks across every `assistant` event, *not*
`num_turns`, which undercounts parallel calls), input/output/cache tokens, wall-clock,
cost (`total_cost_usd`), and whether the final answer **grades correct** against ground
truth. Results aggregate to **mean ± σ** over N repetitions.

## Running it

Local only (like `task flows`) — never in CI. Needs `claude` (Claude Code CLI) and
`gopls` (only to *regenerate* truth). Two auth modes:

- **`ANTHROPIC_API_KEY` set** (preferred): sessions run `--bare` — fully hermetic, no
  ambient hooks/skills/MCP/CLAUDE.md. Keys are never committed.
- **No key**: sessions use the logged-in claude CLI (subscription auth, bills your
  plan). `--bare` is replaced by `--strict-mcp-config`, which still pins the MCP
  surface exactly per arm — the baseline arm cannot see codemap through ambient
  config, preserving the A/B's core fairness property. Residual caveat: user-level
  CLAUDE.md/hooks may load, so treat subscription-mode numbers as even more
  directional than usual.

```sh
task bench                              # full matrix: build → fetch fixture → index → run → report
task bench CLI_ARGS="--tasks 01,02 --reps 1"   # cheap subset while iterating
CODEMAP_BENCH_MODEL=claude-opus-5 task bench    # override the model (default claude-sonnet-5)
task bench:report                       # regenerate the README table from the newest run (no API)
task bench:smoke                        # offline plumbing check (fabricated metrics, no API)
go run ./bench --dry-run                # print the full plan + exact claude invocations, no API
```

**Cost.** A full run is 10 tasks × 2 arms × 3 reps = ~60 sessions. Ballpark **$5–15** on
`claude-sonnet-5` (baseline sessions read many files; codemap sessions are cheaper). ~5× on
Opus. The harness prints the summed `total_cost_usd` at the end so spend is never a surprise.
Subset with `--tasks` / `--reps` to iterate cheaply.

## Methodology (read before trusting a number)

- **Fixture** — `github.com/go-git/go-git` pinned by SHA in `fixtures/fixture.lock`
  (v5.16.5). Pure-Go, ~40k LOC, a deep internal call graph. Fetched by SHA into
  `fixtures/repo/` (gitignored) — not vendored, not a submodule. Go-only for v1 by design; a
  Go+TS polyglot fixture is a documented v1.1 follow-up (codemap's TS/JS call graph needs a
  language server, which is slower and less deterministic to reproduce).
- **Ground truth is derived INDEPENDENTLY OF CODEMAP** — the load-bearing decision. If truth
  came from codemap's own graph, the codemap arm would win by construction. The oracle is the
  Go type checker (`go/types` via `golang.org/x/tools/go/packages` — the same independent type
  info gopls exposes), driven by the standalone generator in `tasks/truth/gen/`, which imports
  nothing from codemap. Which symbol each task targets is committed and auditable in
  `tasks/truth/targets.json`. Truth is **frozen at the fixture SHA**; regenerate only on a SHA
  bump (`tasks/truth/gen.sh`) and re-review the transitive sets. See `tasks/truth/README`.
- **Grading is deterministic and offline** (`grade/`). Each task prompt requires the agent to
  end with a single fenced ` ```json ` block of a stated shape; the grader extracts the last
  such block and applies `set_equal` (reports precision/recall on mismatch), `exact`,
  `numeric` (with tolerance), or `contains_path` (a call path validated against the
  ground-truth edge set). The grader and the stream-json parser are unit-tested in CI with no
  API key.
- **Pre-indexing is disclosed, not hidden.** codemap is modelled as pre-indexed (matching real
  use, where the daemon keeps the index fresh), so the codemap arm pays *query* time, not index
  time. The one-time index cost is measured and reported **separately** in the summary and the
  README banner — never folded into per-session metrics.
- **Nondeterminism is shown, not smoothed.** N=3 minimum, mean ± σ reported, DIRECTIONAL
  stamped everywhere. Sonnet rejects a temperature/seed knob, so variance is inherent.
- **No cross-run leakage.** Every session is a fresh `claude -p --bare` process (no
  `--continue`/`--resume`, no shared session). `--bare` blocks ambient hooks/skills/MCP/CLAUDE.md
  so the *only* difference between arms is the codemap MCP config. The codemap arm's index lives
  in an isolated `CODEMAP_DATA` under `bench/results/index` so it can't collide with your real
  index. The harness asserts (via the `system/init` event) that the codemap MCP server actually
  loaded for the codemap arm — a broken config fails the session loudly instead of silently
  degrading to plain file tools.

## Layout

```
bench/
├── main.go              orchestration: flags, task×arm×rep matrix, results + summary writer
├── report.go            --report-only: splice the DIRECTIONAL table into README between markers
├── drivers/             Driver interface + Claude headless driver (stream-json parser) + stubs
│   ├── driver.go        Arm / Metrics / Driver; ErrNotImplemented
│   ├── claude.go        ClaudeDriver: shell out to `claude`, fold stream-json into Metrics
│   ├── smoke.go         offline driver (fabricated metrics) for --smoke plumbing checks
│   ├── codex.go/gemini.go   v1 stubs (documented shape; ErrNotImplemented)
│   └── testdata/        recorded stream-json transcripts for the parser unit tests
├── grade/               deterministic graders + JSON-block extractor (unit-tested, no API)
├── suite/               shared task/session/summary types + mean±σ aggregation
├── tasks/               NN_*.json task specs (8–10), patches/, truth/ (frozen ground truth + gen)
├── mcp/                 codemap arm MCP config template (${CODEMAP_REPO} resolved at runtime)
├── fixtures/            fixture.lock (SHA pin) + fetch.sh (repo/ is gitignored)
└── results/             run artifacts (gitignored); only *.summary.json is committed by hand
```

## Extending

- **More drivers.** `drivers.Driver` is "run a prompt in an arm, return transcript metrics."
  Codex/Gemini CLIs slot in by implementing it (see the stubs); select with `--driver`.
- **More tasks.** Add `tasks/NN_slug.json` + its target in `tasks/truth/targets.json`, then
  `go run ./bench/tasks/truth/gen` and review. The task-integrity test guards the wiring.
