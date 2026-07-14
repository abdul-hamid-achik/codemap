---
description: Connect codemap to coding agents and use its MCP tools in a confidence-aware development loop.
---

# codemap for agents

codemap is built for AI coding agents as much as for people. It precomputes a
codebase's structure once, then answers narrow questions in **one call** — with
provenance and honesty signals attached — instead of making an agent chain dozens of
`grep`/`read` calls to reconstruct what the graph already knows.

Query and report commands take `--json`, and the core query surface has thin
**`codemap_<name>` MCP tools backed by the same service reports. Point your harness at
`codemap serve` (a stdio MCP server) or shell the CLI — shared successful report data is
identical, while transport-level misses follow each surface's convention (CLI exit
codes/error envelopes; MCP structured tool results). Administration and export workflows
such as `structural-manifest`, `export-symbols`, cache export/import, daemon management,
branch snapshots, agent setup, `serve`, and `studio` remain CLI-only. The built-in guide
`codemap docs workflow` (and the `codemap_docs` tool) is the in-band version of this
page.

## One-command setup

codemap registers itself with your harness — no hand-editing MCP config files, no forgetting
the guidance. `codemap agent setup <harness>` merges the codemap MCP server into the harness's
native config and writes the canonical playbook (this page, in-band) to the harness's guidance
surface, so the agent knows *when* to reach for the tools before it is even connected.

```bash
codemap agent setup claude-code   # installs the plugin (MCP server + using-codemap skill)
codemap agent setup cursor        # .cursor/mcp.json + .cursor/rules/codemap.mdc
codemap agent setup vscode        # .vscode/mcp.json (key: servers) + copilot-instructions.md
codemap agent list                # what's detected here, and whether codemap is registered
```

| Harness | MCP registration | Playbook surface |
|---|---|---|
| Claude Code | the [plugin](#claude-code-plugin) (`.mcp.json`) | `using-codemap` skill |
| Cursor | `.cursor/mcp.json` (`mcpServers`) | `.cursor/rules/codemap.mdc` |
| OpenAI Codex | `codex mcp add` / `~/.codex/config.toml` (`[mcp_servers.codemap]`) | `AGENTS.md` |
| Gemini CLI | `.gemini/settings.json` (`mcpServers`) | `GEMINI.md` |
| Cline | VS Code globalStorage `cline_mcp_settings.json` | `.clinerules` |
| Roo Code | `.roo/mcp.json` (`mcpServers`) | `.roo/rules/codemap.md` |
| Zed | `~/.config/zed/settings.json` (`context_servers`) | `.rules` |
| VS Code Copilot | `.vscode/mcp.json` (**`servers`**, not `mcpServers`) | `.github/copilot-instructions.md` |
| OpenCode | `opencode.json` (`mcp`, command is an array) | `AGENTS.md` |
| aider | none (no MCP) — CLI playbook | `CONVENTIONS.md` |
| Any AGENTS.md-reading harness (`agents-md`) | none (playbook-only) | `AGENTS.md` |

`setup` defaults to **project-scoped** files and never clobbers other servers or your prose (it
merges JSON and replaces only a marked `<!-- codemap:begin … end -->` block). Global-only configs
(Codex, Zed, Cline) print the exact snippet unless you pass `--global`; `--dry-run` shows every
planned write. For an AGENTS.md-aware harness not otherwise listed, run
`codemap agent setup agents-md`; for any other harness, `codemap agent playbook` prints the guidance
to paste.

Cursor's generated `mcpServers.codemap` entry also sets `CODEMAP_MCP_PROFILE=core` — see
[MCP tool profiles](/mcp#tool-profiles) — because Cursor caps total MCP tools at ~40 across
*all* servers combined; every other harness above stays on the full 42-tool default.
For a manually configured harness, choose `CODEMAP_MCP_PROFILE=agent` to bind its
surface exactly to this page's taught loop. `agent` and the backwards-compatible
`core` profile both contain 22 tools today; `full` is the explicit expert/admin
surface and remains the default for compatibility.

### Claude Code plugin

In Claude Code, add the marketplace and install the plugin (it references `codemap` on your PATH —
install via Homebrew/`go install`, it is not bundled):

```
/plugin marketplace add abdul-hamid-achik/codemap
/plugin install codemap@codemap
```

You get the `codemap` MCP tools, the `using-codemap` skill (this playbook), and a
`/codemap:codemap-setup` command that indexes the current repo.

## The agent loop

Call `codemap_index` once per repo (it builds the graph, and embeddings if Ollama is up)
before any of this works. Then it's six moves, from landing in an unfamiliar repo to
shipping a change. Each row names the one tool to call and the one signal that tells you
whether to trust what it returned — full tool descriptions live in the
[MCP reference](/mcp#tools), this is the order to call them in.

| Stage | Tool | Check |
|---|---|---|
| **Orient** — where do I start? | `codemap_read_order` | ranked entrypoints + hubs, each with a reason — read these first, once per repo |
| **Locate** — find the symbol | `codemap_find` (by name) / `codemap_semantic` (by meaning) / `codemap_grep` (exact text) | `matched_in` on `find`, `fusion` on `semantic` — why the hit surfaced |
| **Understand** — read it in full | `codemap_context` (one symbol) / `codemap_context_batch` (several) | `call_graph` — trust level; `candidates` if the name is ambiguous, re-query with `candidates[i].selector` |
| **Gate** — how careful, and is this even current? | `codemap_risk` (change-risk score) alongside `codemap_impact` / `codemap_file_impact` for the blast surface | `stale` — an index that's drifted since last run makes every other signal provisional |
| **Edit** — make the change | informed by the tools above; codemap has no write path | — |
| **Verify** — did it land, what do I run | `codemap_review` | `call_graph` + aggregate `risk` — the diff's changed symbols, blast radius, and the tests to run |

Deeper tools plug into the same stages on demand: `codemap_dependencies` and
`codemap_references` sharpen **Locate**/**Gate** with confirmed-vs-candidate file and
callback evidence; `codemap_hotspots`/`codemap_orphans` support a **Gate**-time survey
(hubs, dead-code candidates); `codemap_coverage` tells you which packages' `call_graph`
answers to trust before you lean on them; `codemap_path` traces the shortest call chain
between two symbols anywhere in the loop. Run `read_order` once per repo and `review`
after every change — those two bookend the loop.

The default `full` profile also exposes three bounded orientation tools outside the lean taught
loop: `codemap_map` surveys subsystems, `codemap_explore` turns an intent query into exact context
neighborhoods (`seeds`/`edges`/`depth`), and `codemap_traverse` walks selected relation types from
a required durable selector (`direction`/`edge_types`/`depth`/`limit`). They are intentionally not
registered in the current 22-tool `agent` or `core` profiles.

## Honesty signals — why an agent can trust the answers

codemap never silently guesses. Every report carries the signals an agent needs to
calibrate its confidence:

- **`stale`** — files changed/new/deleted since the last index. Normally reindex
  before trusting results (queries read the snapshot, not live files). For a deletion,
  `codemap_review` intentionally uses retained definitions from the last index and emits
  `deletion_analysis`; run its selected tests **before** reindexing prunes that evidence.
  `codemap_status` surfaces freshness too.
- **`analysis_complete` on `codemap_review`** — staleness, total/analyzed/truncated symbol counts,
  and bounded structured `partial_errors` distinguish a complete diff analysis from a successful
  subset. Structural-source mapping errors explicitly cover failed symbol lookup, deletion-only hunks with no
  post-image line, recognized callable/type declaration lines removed in mixed or equal-count hunks, and an exact
  source rename with no mapped symbols at its new path. Fresh indexed untracked source files and exact source renames map as
  whole files; documentation/assets remain ordinary zero-symbol changes. A stale, partial, or capped review always reports aggregate `risk.level:"unknown"`.
- **`resolution`** — set when a call graph is *unavailable* (TypeScript/JavaScript/
  Python without successful precise coverage, or Vue SFCs whose call edges are not yet
  supported even by precise indexing): callers/blast/tests are **unresolved, not absent**.
  `codemap_review`/`codemap_risk` will not assert "no tests" in that state, and
  `--fail-on-untested` fails closed because coverage cannot be established;
  `codemap_file_impact` reports deletion as `unsafe` only from fresh confirmed
  file-scoped evidence and `unknown` otherwise — never "safe" from a candidate or empty result.
- **`codemap_coverage`** — the per-file map behind every `call_graph` enum: which files have
  recorded precise coverage, when, and whether that specific file has drifted on disk since
  (distinct from `codemap_status`'s project-wide drift count). Check it once per area of the
  codebase to calibrate trust per package instead of assuming the whole project's
  worst-file confidence.
- **`dependency_evidence.coverage`** — `complete`/`partial`/`unavailable` by
  calls, references, imports, runtime wiring, and external consumers. Evidence is
  grouped by dependent file/kind with totals and bounded source→target samples. Every
  sample is `confirmed` or `candidate` with a reason; additive confirmed/candidate totals
  survive list caps. Go imports are package-scoped candidates, not proof that the
  representative file is required.
- **`references.coverage` / `references.confidence`** — Go callback/handler patterns are
  indexed, but general type/value use and runtime wiring are not. Empty partial coverage is
  never presented as proof that no registration exists; name fan-out and stale snapshots remain
  candidates.
- **`note` / `shared_name`** — the name resolves to several definitions, so a count
  merges them. Precise indexing fixes the edges; pass an exact source selector to
  choose one definition. The response's own `candidates:[{selector,signature,file,start_line}]`
  is that exact merged set already shaped as selectors — re-query with `candidates[i].selector`
  instead of a separate `codemap_find`/`codemap_symbols` lookup.
- **`matched_in`** — `codemap_find` in degraded (no-embeddings) mode reports whether each hit
  matched on `"symbol"`, `"fqn"`, or `"docstring"`, so you know why it surfaced.
- **`fusion`** — `codemap_semantic` reports which hybrid vector/BM25 weighting it used
  (`"identifier"`, `"natural_language"`, `"balanced"`), adaptively chosen from the query's shape
  unless the server is configured with `semantic.fusion: balanced` (or
  `CODEMAP_SEMANTIC_FUSION=balanced`).
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
`codemap_callees`, `codemap_references`, `codemap_impact`, `codemap_risk`, or full-profile
`codemap_traverse`; path accepts `from_selector`
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
