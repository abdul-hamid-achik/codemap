# codemap for agents

codemap is built for AI coding agents as much as for people. It precomputes a
codebase's structure once, then answers narrow questions in **one call** — with
provenance and honesty signals attached — instead of making an agent chain dozens of
`grep`/`read` calls to reconstruct what the graph already knows.

Every CLI command takes `--json`; every capability has a thin **`codemap_<name>` MCP
tool** that returns the same shape. Point your harness at `codemap serve` (a stdio
MCP server) or shell the CLI — the data is identical. The built-in guide
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
| **Touch a whole file?** | `codemap_file_impact` | who depends on it, its blast radius, and a `safe_to_delete` / `breaking_change` verdict |
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
  `codemap_review`/`codemap_file_impact`/`codemap_risk` will not assert "no tests" or
  "safe to delete" in that state — they say the verdict is unavailable.
- **`note` / `shared_name`** — the name resolves to several definitions, so a count
  merges them; reindex `--precise` (or use `callers --lsp`) to separate.
- **`untested` / `heuristic`** — a symbol has no covering tests, or a test was matched
  by name-scan rather than the call graph (flag it, don't trust it blindly).
- **`*_total`** — true counts behind a capped list, so you know when to drill with
  `codemap_callers`/`impact` for the full set.

## Precision when you need it

The Go graph is exact from `go/parser`. For TypeScript/JavaScript/Python — and for
exact Go method resolution — run `codemap index --precise` (go/types + language-server
`callHierarchy`): every query becomes exact, no per-call flag. For a one-off exact Go
answer without reindexing, pass `precise: true` to `codemap_callers`/`codemap_callees`.

## The knowledge layer

Pin findings to the graph so they survive reindex and surface on every query:
`codemap_annotate` attaches a note + opaque `data` (a DB row, a test result, a
security finding) to a symbol or a `from→to` call path; `codemap_annotations` reads
them back; `codemap_unannotate` prunes. This is how sibling tools
([cairntrace, glyphrun](/ecosystem)) write browser/terminal test outcomes back onto
the code graph, turning "what happened" into a durable, queryable fact about "which
code is responsible."
