# codemap for agents

codemap is built for AI coding agents as much as for people. It precomputes a
codebase's structure once, then answers narrow questions in **one call** — with
provenance and honesty signals attached — instead of making an agent chain dozens of
`grep`/`read` calls to reconstruct what the graph already knows.

Every CLI command takes `--json`; every capability has a thin **`codemap_<name>` MCP
tool backed by the same service reports. Point your harness at `codemap serve` (a
stdio MCP server) or shell the CLI — successful report data is identical, while
transport-level misses follow each surface's convention (CLI exit codes/error
envelopes; MCP structured tool results). The built-in guide
`codemap docs workflow` (and the `codemap_docs` tool) is the in-band version of this
page.

## The agent loop

A typical "understand or change this code" loop, and the one-call tool for each step:

| When | Tool | Answers |
|---|---|---|
| **Index once** | `codemap_index` | build the graph (+ embeddings if Ollama is up) |
| **Where do I start?** | `codemap_read_order` | entrypoints (`main`, `cmd/`, exported API) + load-bearing hubs, ranked with a reason — a reading guide for an unfamiliar repo |
| **Find the entry point** | `codemap_semantic` / `codemap_find` | by meaning, or by name (offline) |
| **Orient on a symbol** | `codemap_context` | def + callers + callees + covering tests + blast size + notes, in ONE call |
| **Model a component** | `codemap_context_batch` | the bundle for several symbols at once, plus the callers they share (coupling) |
| **Go deeper** | `codemap_impact` · `codemap_source` | full blast radius · the implementation body |
| **How careful?** | `codemap_risk` | a 0..1 change-risk score (untested + fan-in + cross-package spread + ambiguity) + the factors |
| **Need file dependencies?** | `codemap_dependencies` | bounded inbound call/reference/import evidence with source→target samples and explicit domain coverage |
| **Touch a whole file?** | `codemap_file_impact` | the same dependency evidence plus blast radius, tests, and conservative `delete_verdict` |
| **Trace flow** | `codemap_path` | the shortest call chain between two symbols |
| **AFTER you edit** | `codemap_review` | your git diff → the changed symbols, their union blast radius, and the **tests to run** (regression test selection) |
| **Survey** | `codemap_hotspots` · `codemap_orphans` | hubs · dead-code candidates |

The two queries built specifically for the edit loop are **`codemap_review`** ("what
did I just affect, and what should I run?") and **`codemap_risk`** ("how careful should
I be?"). Run `read_order` on first contact with a repo; run `review` after every change.

## Honesty signals — why an agent can trust the answers

codemap never silently guesses. Every report carries the signals an agent needs to
calibrate its confidence:

- **`stale`** — files changed/new/deleted since the last index. If non-zero, reindex
  before trusting results (queries read the snapshot, not live files). `codemap_status`
  surfaces it too.
- **`resolution`** — set when a call graph is *unavailable* (TypeScript/JavaScript/
  Python without `--precise`): callers/blast/tests are **unresolved, not absent**.
  `codemap_review`/`codemap_risk` will not assert "no tests" in that state;
  `codemap_file_impact` reports deletion as `unsafe` only from positive inbound
  evidence and `unknown` otherwise — never "safe" from an empty call result.
- **`dependency_evidence.coverage`** — `complete`/`partial`/`unavailable` by
  calls, references, imports, runtime wiring, and external consumers. Evidence is
  grouped by dependent file/kind with totals and bounded source→target samples.
  Go imports are package-scoped hints, not proof that the representative file is required.
- **`note` / `shared_name`** — the name resolves to several definitions, so a count
  merges them. Precise indexing fixes the edges; pass an exact source selector to
  choose one definition.
- **`untested` / `heuristic`** — a symbol has no covering tests, or a test was matched
  by name-scan rather than the call graph (flag it, don't trust it blindly).
- **`*_total`** — true counts behind a capped list, so you know when to drill with
  `codemap_callers`/`impact` for the full set.

## Precision when you need it

The Go graph starts name-based from `go/parser`. For TypeScript/JavaScript/Python — and for
exact Go method resolution — run `codemap index --precise` (go/types + language-server
`callHierarchy`). Precise coverage is tracked per file: a query is `resolved` only when every
matched definition file completed the pass; partial failures remain `name`/`unresolved`. For a one-off exact Go
answer without reindexing, pass `precise: true` to `codemap_callers`/`codemap_callees`.

## Exact source selectors

Every symbol result already has `file`, `start_line`, `fqn`, and `kind`. Project those
same fields into `selector` for `codemap_source`, `codemap_context`, `codemap_callers`,
`codemap_callees`, `codemap_impact`, or `codemap_risk`; path accepts `from_selector`
and `to_selector`. This scopes the whole query to one definition even when a short
name is shared. File+FQN+kind is preferred, so a declaration can shift lines across
a reindex. A move or rename can invalidate the selector and returns a miss; codemap
never exposes the SQLite node id as a durable API. People can use the equivalent
`--at <file>:<line>` CLI flag.

## The knowledge layer

Pin findings to the graph so they survive reindex and surface on every query:
`codemap_annotate` attaches a note + opaque `data` (a DB row, a test result, a
security finding) to a symbol or a `from→to` call path; `codemap_annotations` reads
them back; `codemap_unannotate` prunes. This is how sibling tools
([cairntrace, glyphrun](/ecosystem)) write browser/terminal test outcomes back onto
the code graph, turning "what happened" into a durable, queryable fact about "which
code is responsible."
