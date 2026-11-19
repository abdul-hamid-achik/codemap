---
description: How codemap compares to other code-graph tools for AI agents — measured, not asserted.
---

# Comparison

Code-graph tools for AI agents make similar promises. This page shows where
codemap is measurably different, where competitors are ahead, and how every
claim here was produced — the harness is in the repo, the raw runs are
committed, and the numbers are **DIRECTIONAL** (same fixture, same driver,
same neutral system prompt; N is small and stated).

The head-to-head below was run with the same agent loop (deepseek-v4-pro,
no CLI in the middle) over the same fixture repository, with each tool's MCP
server armed identically.

## Residual context, measured

When a code-graph tool answers, its payload stays in the agent's context for
the rest of the session. Dense payloads are the failure mode CodeGraph's
own benchmarks disclose about themselves (~80% more resident context than
grep-and-read). We measured it directly — summed `tool_result` text bytes per
session, with the MCP-originated subset attributed by tool id:

| per session (mean ± σ) | grep/read baseline | **codemap** | **CodeGraph 1.6.x** |
|---|---|---|---|
| residual context (tool result bytes) | 33 KiB ± 53 | **26 KiB ± 26 (−21%)** | 46 KiB ± 56 (+40%) |
| — of which from the code-graph tool | 0 | **4.3 KiB** | **23 KiB (5.3×)** |
| sessions that used the code-graph tool | — | 8/10 | 7/10 |
| tasks answered correctly | 9/10 | 8/10 | 7/10 |

And when a question **needs** structure, bounded answers are decisive: on the
multi-step CI-selection task (every test that transitively covers either of
two changed functions, any call depth), the grep/read baseline burned 77 tool
calls and never produced an answer; the codemap arm answered with perfect
precision and recall in 47 calls, at half the cost.

codemap's bounded, per-question answers leave the session *below* the
grep/read baseline; CodeGraph's dense explore payloads sit 40% above it.
That is the entire design bet of codemap in one table.

## Where codemap is ahead

- **Opt-in exactness** — name-based graph by default; `--precise` resolves
  calls via in-process `go/types` (Go) and language-server `callHierarchy`
  (TypeScript/JavaScript/Python/Rust), with per-file coverage recorded and
  reported. `call_graph: resolved|name|unresolved|none` is on every answer.
- **Honesty contracts** — stable exit codes (0–6), versioned JSON schemas,
  provenance on every edge (name vs precise vs imported-SCIP). No competitor
  documents a comparable machine contract.
- **Local-first, no telemetry** — one static Go binary, MIT. CodeGraph phones
  home by default; GitNexus is PolyForm Noncommercial (commercial use is paid).
- **Semantic search included** — local hybrid (vector + BM25) via
  Ollama+veclite, or delegated to [vecgrep](/ecosystem). CodeGraph has none.
- **A knowledge layer** — `atlas`/`features`/`flow`/`processes` summaries are
  *extracted* (READMEs, docstrings — never generated), and annotations pin
  notes and external data to symbols across reindexes.

## Where competitors are ahead

Honesty cuts both ways:

- **Language breadth**: CodeGraph parses 34 languages, GitNexus 18. codemap
  has six first-class backends (Go, TypeScript/JavaScript, Python, Ruby, Lua,
  Rust) plus declarative formats — and the
  [`--scip` importer](/languages) extends it to any language with a SCIP
  indexer (validated on a real repository: junit4 → 8,206 nodes, 21,521
  resolved references). Still fewer out of the box.
- **Traction**: they have 48k–77k stars. We have better numbers and fewer
  people who know.
- **Adoption is unsolved everywhere**: in the claude-driver runs codemap was
  used in ~50% of sessions even with the playbook injected — a negative
  result we publish. Models that do adopt it (deepseek: 8–9/10) keep
  exploring with file tools too.

## Reproduce it

```bash
git clone https://github.com/abdul-hamid-achik/codemap
cd codemap/bench
DEEPSEEK_API_KEY=… go run . -driver deepseek -model deepseek-v4-pro \
  -arms baseline,codemap,codegraph -reps 1
```

The driver runs the agent loop itself (no claude involved), jails the file
tools to the fixture, and speaks each tool's MCP protocol directly — both
arms see exactly their allowed tools, like-for-like.
