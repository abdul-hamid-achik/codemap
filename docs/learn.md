---
description: A practical walkthrough for learning an unfamiliar repository with codemap atlas, features, flow, read-order, and task-context, for people and agents.
---

# Learn a codebase

You landed in a repository you do not know. This page is the order to run codemap in,
from the big picture down to one handler's call tree, and the signals that tell you how
much to trust each answer. It works the same for a person at a terminal and for an agent
over [MCP](/mcp). The output below is real, taken from codemap indexing its own repository
(trimmed; yours will differ).

## 1. Index

```bash
cd ~/projects/unfamiliar-repo
codemap init
codemap index --no-embed
```

`--no-embed` keeps this structure-only, so no Ollama is needed. Everything below uses
the stored graph. For TypeScript, JavaScript, or Python also run `codemap index --precise`
when you can; see [step 7](#_7-know-how-much-to-trust-it).

## 2. See the shape: `atlas`

`atlas` prints the repository as a directory tree. Each node carries size metrics
(files, symbols, lines, tests), coarse roles (`source`, `tests`, `docs`, `config`,
`entrypoint`, `examples`, `bench`, `generated`, `vendor`), and a one-line summary.
Summaries are extracted from the project's own text, never generated: the README first
paragraph, a Go package doc, a Python module docstring, a leading file comment, or
Markdown. `summary_source` in `--json` says which.

```bash
codemap atlas                              # depth 2 from the repo root
codemap atlas --prefix internal --depth 1  # zoom into one directory
codemap atlas --prefix internal/app --files  # include file leaves
```

```text
$ codemap atlas --prefix internal --depth 1
internal                             3,569 sym   254 files [source]
├─ app/                              1,309 sym   100 files [source]  Package app is codemap's shared service layer. The…
├─ extract/                            618 sym    40 files [source]  Package extract turns source files into structural…
├─ graph/                              319 sym    22 files [source]  Package graph is codemap's structural code graph: …
├─ index/                              309 sym    28 files [source,tests]  Package index walks a project, extracts its …
```

Use `--max-nodes` (default 1500) to bound a very large tree and `--key-symbols` (default
5, max 20) to change how many top symbols each node lists. In `--json`, every node also
has its key symbols with durable `selector`s, inbound/outbound/internal coupling counts,
and its top neighbouring directories, so you can see what a directory depends on and
what depends on it before opening any file. Key symbols are ranked by a de-noised in-degree
(see [step 5](#_5-find-the-entrypoints-and-hubs-read-order-and-map)).

## 3. See what it does: `features`

`features` lists the user-facing entry points: what the software can do and where each
capability starts. Kinds are `program`, `cli_command`, `rpc_tool` (MCP and gRPC),
`http_route`, `api_route`, and `page`.

```bash
codemap features                        # everything, with call footprints
codemap features --kind cli_command     # one kind
codemap features --query review         # substring over label, description, handler, file
codemap features --no-footprint         # faster; skips the call walk
```

```text
$ codemap features --kind cli_command --query review
CLI (1):
  review          Diff-scoped impact + test selection: what your changes affe…  cmd/codemap/query.go:690
                    61 sym · 21 files · 6 subsystems · 34 tests
```

Each feature has an invocation, a description taken from the registration itself (a cobra
`Short`, an MCP tool `Description`) or a docstring, the handler symbol with its selector,
and a **footprint**: a bounded call walk that counts symbols, files, subsystems, and the
tests specific to that feature. The footprint tells you how big a feature is before you
read it.

Go registrations are read from the syntax tree (cobra, urfave/cli, `net/http`, chi, gin,
echo, gorilla, Go 1.22 route patterns, MCP SDKs, gRPC `Register*Server`, `func main`) and
reported as `confidence: confirmed`. TypeScript, JavaScript, and Python are detected by
pattern (Express-style routes, commander and yargs, Next.js App and Pages routers, FastAPI
and Flask decorators, Django `path()`, click and typer, `__main__`) and reported as
`candidate`. Ruby, Lua, and GDScript detection is not implemented, and the report says so
in `notes`.

## 4. See how it works: `flow`

Take a feature's handler and follow it. `flow` returns a call tree in the order the code
calls things (call sites are read from the source), with each step's `file:line`,
subsystem, one-line doc, and confidence.

```bash
codemap flow runReview --depth 2
codemap flow --at cmd/codemap/query.go:690   # exact definition
```

```text
$ codemap flow runReview --depth 2
Flow from runReview — cmd/codemap/query.go:690 (codemap) · depth ≤ 2 · 43 step(s)
  call graph: name

runReview                               cmd/codemap/query.go:690 — runReview renders diff-scoped intelligence…
├─ 1 parseFailOnRiskFlag                cmd/codemap/gate.go:28 — parseFailOnRiskFlag validates --fail-on-risk's value.
│  └─ 1 app.RiskLevelOrdinal            internal/app/gate.go:15 — RiskLevelOrdinal maps a risk level to its position…
├─ 2 openSession                        cmd/codemap/main.go:297
├─ 3 app.Session.Close                  internal/app/session.go:316 — Close closes any stores that were opened.  (+8 alt)
│  └─ 1 Close ×8                        (ambiguous, not expanded)
├─ 5 app.NewService                     internal/app/service_core.go:39 — NewService wraps a session.
├─ 6 app.Service.Review                 internal/app/review.go:123 — Review computes diff-scoped impact + test selection…
…
Subsystems: cmd/codemap (17) → internal/app (17) → internal/config (1) → internal/git (3) → internal/index (1)
6 step(s) involve same-named definitions; ×N marks an unexpanded ambiguous call, +N alt a pick among N others
```

Read the markers:

- `(+N alt)` means the call matched several same-named definitions on a name-based
  graph, and `flow` kept the most plausible one (using call-site syntax, receiver,
  directory, and subsystem). `alternatives` in `--json` counts the others.
- `Close ×8 (ambiguous, not expanded)` means no definition was plausible enough, so the
  step is a placeholder (`leaf_reason: "ambiguous"`) with its candidates and is not expanded.
- Repeats, cycles, and depth or node cuts are explicit (`leaf_reason` is `repeat`,
  `cycle`, `depth`, or `max_nodes`), never silently dropped.
- Precise edges are never collapsed.

Defaults are `--depth 4` and `--max-nodes 120`. Tests are skipped unless you pass
`--include-tests`. To go from a step to full detail, run
`codemap context --at <file>:<line>` on it.

### Every entry's flow at once: `processes`

`flow` explains one handler. `processes` runs the same builder from every entry point
that `features` found (a resolved handler is required) and returns each as an ordered
list of steps. A question like "how does signup work" is then answered with the route,
its handler, and the service chain behind it in one call.

```bash
codemap processes --top 10                      # one process per entry point
codemap processes --kind http_route,cli_command # only these entry kinds
codemap processes --query "signup"              # name or steps match the query's words
codemap processes --depth 3 --max-steps 20      # tighter bounds
```

```text
$ codemap processes --kind rpc_tool --query "task context" --top 1 --max-steps 7 --depth 3
rpc_tool:codemap_task_context  [rpc_tool · 5 steps · 3 files (truncated)]
  mcp.Server.handleTaskContext             internal/mcp/server.go:1297
    mcp.Server.notIndexed                    internal/mcp/server.go:1581
      app.Service.Indexed                      internal/app/service_core.go:306
      mcp.cwdOf                                internal/mcp/server.go:1598
    app.Service.TaskContext                  internal/app/service_task_context.go:189
```

Each process in `--json` has a stable `id` (`kind:name`, the feature id), the entry's
`selector`, `steps` (`symbol`, `fqn`, `kind`, `file`, `start_line`, `depth`) in call
order, the unique `files`, `truncated`, and its own `call_graph`. Ambiguous same-name
placeholders and repeats are not listed as steps. Nothing is stored: processes are
computed on demand from the graph and the registrations, bounded by `--top` (default 50,
max 200), `--depth` (default 4, max 8), and `--max-steps` per process (default 40,
max 200). `--query` uses the same content-word tokenization as the keyword search floor
(question words such as "how does" are ignored), keeps processes that match at least one
word anywhere in their name or steps, and ranks the best match first. Run `codemap flow`
on a process's `entry` for the full annotated tree.

## 5. Find the entrypoints and hubs: `read-order` and `map`

```bash
codemap read-order --top 8   # where to start reading, each with a reason
codemap map                  # subsystems, bridges between them, entrypoints, hubs
```

```text
$ codemap read-order --top 3
Read order (codemap) — start here
   1 ▶ main.main
        program entrypoint — main() — cmd/codemap/main.go:76
   2 ▶ app.Session.Graph
        central — 52 caller(s); also exported (public API surface) — internal/app/session.go:79
   3 ▶ app.NewService
        central — 47 caller(s); also exported (public API surface) — internal/app/service_core.go:39
```

`read-order`, `hotspots`, and the hubs in `map` ignore test code by default: calls from
tests do not count, and symbols defined in test files are not ranked. They rank by
`effective_in_degree`, which is precise callers plus name-based callers divided by the
number of same-named definitions, so a method called `Close` does not outrank real hubs.
Pass `--include-tests` on `hotspots` or `read-order` to count tests. `map` bridges and
subsystem edge counts also exclude tests (`tests_excluded: true` in `--json`).

## 6. Ask a concrete question: `task-context` and `explore`

Once you know the layout, ask about a specific thing.

```bash
codemap explore "how are review gates enforced" --seeds 5
codemap task-context "how are review gates enforced" --mode understand
```

`explore` searches by meaning when embeddings exist (and by name otherwise), joins each hit
to an exact definition, and returns a bounded neighbourhood of callers, callees,
references, and tests without source bodies. `task-context --mode understand` wraps that
with a freshness check in one call. When a seed sits inside an entry point's flow, the
report also carries `processes` (up to 3): the route or command, its handler, and the
chain down to the seed, so "how does signup work" shows a call chain and not just
scattered symbols. The list is empty when the project has no detected entry points or no
flow reaches a seed. Open one definition with
`codemap context --at <file>:<line>` or `codemap source --at <file>:<line>`. See the
[CLI reference](/cli#orientation) for every flag.

## 7. Know how much to trust it

Every orientation report carries the same honesty signals. Read them before acting.

| Signal | Meaning |
|---|---|
| `call_graph` | `resolved` (exact for every matched file), `name` (name-based; same-named symbols can over-match), `unresolved` (the language has no complete call graph without `--precise`), `none` (nothing matched) |
| `stale` | Files changed since the last index. Run `codemap index` before trusting line numbers or counts. |
| `confidence` | `confirmed` was read from the syntax tree or resolved exactly. `candidate` was pattern-detected or name-matched. |
| `truncated`, `partial_errors` | Output was cut by a bound, or an optional part failed. Raise the bound or follow the error. |

`call_graph: name` is normal for Go, Ruby, and Lua. For TypeScript, JavaScript, and Python,
base indexing gives only a partial graph (see [language support](/languages)); run
`codemap index --precise` to get exact call edges per file. Exact edges replace the
name-based candidates in the files they cover, and `flow`, `hotspots`, and `read-order`
become more reliable. `codemap coverage` shows which files have exact edges.

## 8. The same loop over MCP

An agent runs the same sequence with the [MCP tools](/mcp):

1. `codemap_features` (optionally with `kind` and `query`) to find the entry points, or
   `codemap_processes` (full profile) for every entry's flow in one call.
2. `codemap_flow` with the handler's `selector` from step 1 to get the call tree.
3. `codemap_context` with a step's `selector` to read one definition, its callers,
   tests, and blast radius.

`codemap_read_order` and `codemap_explore` cover the orientation questions, and
`codemap_atlas` (full profile only) returns the described tree. `codemap_features` and
`codemap_flow` are in the `agent`, `core`, and `full` profiles. See
[codemap for agents](/agents#the-agent-loop) for where these fit among the other stages.

## Prefer a GUI?

[Codemap Studio](/desktop) is a desktop app over the same CLI commands.
