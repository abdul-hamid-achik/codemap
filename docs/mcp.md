---
description: Configure the codemap MCP server and explore its agent, core, and full tool profiles.
---

# MCP server

codemap is a stdio [Model Context Protocol](https://modelcontextprotocol.io) server, so AI
agents can query your code graph directly instead of reading dozens of files. This page is
the full tool reference — for registering codemap in one command with a playbook included,
see [codemap for agents](/agents#one-command-setup); for the order to call these tools in,
see [the agent loop](/agents#the-agent-loop).
Tool text payloads use compact JSON to avoid spending context tokens on indentation; the
structured result fields are identical to CLI `--json` reports.

## Register it

Install codemap (`brew install abdul-hamid-achik/tap/codemap`, or `go install
github.com/abdul-hamid-achik/codemap/cmd/codemap@latest`), then register `codemap serve` with your
agent. Most CLIs have a one-liner:

**Claude Code**

```bash
claude mcp add codemap -- codemap serve        # add --scope user to share across all projects
```

**OpenAI Codex**

```bash
codex mcp add codemap -- codemap serve
```

**GitHub Copilot CLI**

```bash
copilot mcp add codemap -- codemap serve
```

**Any other MCP client** — add a stdio server to its config (the key may be `mcpServers`, `mcp`, or
`context_servers` depending on the client):

```json
{
  "mcpServers": {
    "codemap": { "command": "codemap", "args": ["serve"] }
  }
}
```

Once connected, an agent can call **`codemap_docs`** to learn the tools and the
index → understand → read workflow on its own.

## Tool profiles

By default `codemap serve` registers all 44 tools. That's a real cost: a
[hermetic benchmark](https://github.com/abdul-hamid-achik/codemap/blob/main/bench/README.md)
measured **+95% input tokens** on the codemap arm, driven by every tool's schema
riding in every session's context — and some clients (Cursor) cap total MCP tools
at ~40 *across all servers combined*, so a full codemap registration can crowd out
every other server.

Set `CODEMAP_MCP_PROFILE=agent` (env), `mcp.profile: agent` (`codemap.yaml`), or pass
`--profile agent` to `codemap serve` for the exact **26-tool** surface derived from
the canonical taught workflow: the 25 tools it names plus `codemap_docs` for
self-discovery. The shipped `core` profile remains compatible and currently has
the same 26-tool inventory; its contract stays stable while `agent` is pinned to
what the playbook actually teaches. `full` remains the default for backwards
compatibility and is the explicit expert/admin surface.

`codemap_index`, `codemap_status`, `codemap_docs`, `codemap_read_order`,
`codemap_semantic`, `codemap_find`, `codemap_grep`, `codemap_context`,
`codemap_context_batch`, `codemap_impact`, `codemap_source`, `codemap_callers`,
`codemap_callees`, `codemap_references`, `codemap_risk`, `codemap_dependencies`,
`codemap_file_impact`, `codemap_file_context`, `codemap_review`, `codemap_path`,
`codemap_hotspots`, `codemap_orphans`, `codemap_coverage`, `codemap_explore`,
`codemap_symbol_at`, `codemap_related_files`.

The current offline microbenchmark drives real `tools/list` calls through the Go
MCP SDK's in-memory transport, with no model, network, embeddings, or language
server. On an Apple M5 over 100 iterations, `agent`/`core` each serialize **31,151
schema characters (≈7,788 tokens using the declared chars/4 planning estimate)**;
`full` serializes **44,487 characters (≈11,122 estimated tokens)**. That is about
30% less schema context for the taught surface. Reproduce it with:

```bash
go test ./internal/mcp -run '^$' -bench '^BenchmarkProfileSchemaTax$' -benchtime=100x -benchmem
```

Everything else — `codemap_init`, `codemap_doctor`, `codemap_projects`,
`codemap_symbols`, `codemap_refactor_plan`, `codemap_secret_impact`,
`codemap_required_keys`, `codemap_annotate` / `codemap_annotations` /
`codemap_unannotate`, `codemap_branch_status` / `codemap_branch_switch`, and
`codemap_cache_save` / `codemap_cache_restore` / `codemap_cache_list` /
`codemap_cache_drop`, plus the full-profile orientation surfaces `codemap_map`
and `codemap_traverse` — is admin/ecosystem/extended surface, available
under the default `full` profile and excluded from both current lean profiles. Precedence is the same three-way order as every
other codemap setting: config file < environment < CLI flag. An unrecognized value
is a startup error, not a silent fallback. [`codemap agent setup
cursor`](/agents#one-command-setup) defaults its generated `.cursor/mcp.json` entry
to `CODEMAP_MCP_PROFILE=core` for exactly this reason; every other harness stays on
`full` since only Cursor has the tool-count ceiling.

## Tools

Project-scoped tools take an optional `path` (the project directory; defaults to the
server's working directory) and return JSON. Global helpers such as `codemap_projects`,
`codemap_docs`, and `codemap_doctor` do not need a project path.

| Tool | Description |
|---|---|
| `codemap_init` | Register a project directory |
| `codemap_index` | Index/reindex a project (`reindex`, `no_embed`, `precise` → exact call edges via `go/types` for Go and LSP `callHierarchy` for TypeScript/JavaScript/Python; Vue remains symbols + imports only). Base TS/JS indexing already carries name-based import, JSX component-usage, and Next.js framework-wiring edges; Ruby and Lua index name-based with no server |
| `codemap_status` | Index statistics **plus freshness** — a `stale` count of files changed/added/removed since indexing, so an agent reindexes before trusting results. The health call skips the local vector-store count (`vectors_known:false`) to stay bounded; use the CLI's explicit `codemap status --full` for that diagnostic |
| `codemap_doctor` | Check the environment (go toolchain, gopls, TS/JS + Python language servers, Ollama) with install hints — diagnose why a language isn't indexed or semantic search is off |
| `codemap_semantic` | Semantic search by meaning (`query`, `top_k`). Adaptively balances the vector/BM25 hybrid-search fusion by query shape — an identifier-looking query leans BM25, a natural-language question leans vector — and reports the chosen profile as `fusion` (`identifier`/`natural_language`/`balanced`). Set `semantic.fusion: balanced` (or `CODEMAP_SEMANTIC_FUSION=balanced`) on the server for exact equal weighting |
| `codemap_callers` | Functions/methods that call a symbol (`precise: true` → language-server resolution; `selector` → one exact definition). Carries a stable `call_graph` enum (`resolved`/`name`/`unresolved`/`none`) |
| `codemap_callees` | Functions/methods a symbol calls; accepts the same `precise` and exact `selector` inputs. Same `call_graph` enum |
| `codemap_references` | Places a function/method is used as a value rather than called (callbacks, handlers, registrations — and, for TS/JS, Next.js framework wiring). Accepts an exact `selector`; returns capped source sites with totals plus independent `coverage` and confirmed/candidate confidence. Coverage is partial and name fan-out remains candidate, so an empty result is not proof of no runtime wiring. |
| `codemap_impact` | Callers + blast radius + covering tests + `test_commands` (copy/paste-ready runner invocations derived from those tests, same derivation as `codemap_review`) (`depth`). `selector` scopes all traversal to one definition. Carries `call_graph` alongside the human `resolution` note — `unresolved` means callers/blast/tests are unknown, not absent (for example, uncovered TS/JS/Python definitions or Vue, whose call graph is not supported yet) |
| `codemap_review` | **Diff-scoped impact + test selection** — maps a working/staged/`since` diff to changed symbols, `blast_radius`, `covering_tests`, `test_commands`, aggregate `risk`, confidence, and bounded `next` actions. `analysis_complete` plus total/analyzed/truncated counts and bounded `partial_errors` prevent stale, capped, or partially failed analysis from looking authoritative; structural-source mapping errors include failed symbol lookup, deletion-only hunks, recognized callable/type declaration lines removed in mixed or equal-count hunks, and exact source renames with no mapped symbols. Documentation/assets remain visible in `changed_files` without structural mapping failures. Fresh indexed untracked source files and exact source renames map as whole files. Incomplete analysis forces `risk.level:"unknown"`. Deleted source files are analyzed from retained last-index definitions when available; `deletion_analysis` reports completeness and test actions precede reindexing. |
| `codemap_dependencies` | Direct inbound dependency evidence for a `file`, grouped and capped by dependent file and calls/references/imports. Every sample carries `confidence`/`confidence_reason`; confirmed/candidate totals, file-vs-package scope, truncation, freshness/`call_graph`, and domain coverage stay explicit. |
| `codemap_file_impact` | **File-level impact** — returns confidence-aware `dependency_evidence`, blast/tests, and a conservative `delete_verdict`. Only fresh confirmed file-scoped evidence proves `unsafe`; name-fanout candidates, stale snapshots, Go package imports, and missing evidence remain `unknown`. Legacy `safe_to_delete` stays false. |
| `codemap_required_keys` | **Least-privilege key set** — for an `entrypoint`, the candidate secret key NAMES its transitive call tree actually reads. Supply `keys` directly or use `via_vault` plus optional `prefix`; operates on names only, never values. Candidate input is capped at 256 unique names, 256 bytes per name |
| `codemap_secret_impact` | **Secret-key rotation blast radius** — for each key NAME, the symbols that read it (`os.Getenv`/`os.environ`/`process.env`), the transitive callers affected, and covering tests (`untested:true` warns a key no test reaches). Operates on key NAMES only — never reads/returns values. Pairs with [tinyvault](/ecosystem); `via_vault` fetches names from it. Each request is capped at 256 unique names, 256 bytes per name. Name-based unless the index is `--precise` |
| `codemap_hotspots` | Most-referenced symbols (`top`), with project-wide `call_graph`/`resolution` so incomplete rankings are explicit |
| `codemap_risk` | **Change-risk score** for a `symbol` or exact `selector` — untested coverage + fan-in + cross-package spread + name ambiguity combined into a 0..1 `score` + `level` (unknown/low/medium/high), with the `factors` behind it. An unavailable call graph is `unknown`, never a reassuring `low` |
| `codemap_orphans` | Dead-code candidates (`top`), with project-wide `call_graph`/`resolution` so an unresolved graph never reads as proven dead code |
| `codemap_coverage` | **Per-file precise call-graph coverage** — rollups by language/directory (worst-covered first) always included; `prefix`/`language`/`uncovered` filters or `files:true` add the bounded per-file list (`top`, default/max 200/2000; `files_total`/`files_truncated` disclose the real count). Each file reports `resolver`/`resolved_at`/`stale`. Complements the per-query `call_graph` enum — use it to calibrate trust per package before asking a symbol question. |
| `codemap_read_order` | **Where to start reading** — ranks entrypoints (`main()`, `cmd/`, module index files, exported API) + call-graph hubs into a reading guide, each with a reason and score. Optional `query` narrows it. Run on first contact with an unfamiliar repo, then drill the top entries with `codemap_context` |
| `codemap_map` | **Architecture overview** (full profile) — bounded source-path subsystems, directed cross-subsystem bridges with edge type/provenance, likely entrypoints, and hubs. Independent `top_subsystems`/`top_bridges`/`top_hubs`/`top_entrypoints` caps; response carries totals/truncation plus freshness and call-graph honesty. |
| `codemap_explore` | **Intent to exact neighborhoods** — accepts `query` plus bounded `seeds`, `edges`, and `depth`; searches semantically when embeddings exist (name fallback otherwise), joins usable hits to durable selectors, and returns compact context neighborhoods without source bodies. Limits: seeds 1–10, edges per context 1–20, depth 1–10. Unjoined hits and optional failures remain explicit. |
| `codemap_traverse` | **Typed heterogeneous graph walk** (full profile) — requires `selector:{file,start_line,fqn,kind}` and never accepts an ambiguous name union. `direction` is `outgoing`, `incoming`, or `both`; `edge_types` is a list drawn from `calls`, `references`, `imports`, `implements`, `overrides`, `depends_on`, `tests`, and `defines`; `depth` is 1–10 and `limit` is 1–500 nodes. Each hop returns durable child/parent selectors, edge provenance, and confirmed/candidate confidence; the report is cycle-safe, bounded, and exposes truncation/domain totals. |
| `codemap_path` | Shortest call path (`from`, `to`, or paired `from_selector`/`to_selector`), with endpoint-scoped `call_graph`/`resolution` distinguishing disconnected from unresolved. Unique FQNs are exact endpoints too |
| `codemap_related_files` | Files structurally related to a `file` via the call/test graph — its callers', callees', and covering-test files, each with a reason (`caller`/`callee`/`test`) and confidence. Graph-accurate alternative to import-text heuristics |
| `codemap_symbols` | List the symbols defined in a `file` (structured alternative to reading it) |
| `codemap_symbol_at` | Resolve a `file:line` position to its enclosing symbol (FQN, kind, range) — join external `file:line` results (search hits, stack traces, diffs) onto the graph. `resolution` is `exact`/`enclosing`/`none`. The `indexed` field is `false` when the project hasn't been indexed yet, so an agent knows to call `codemap_index` before concluding "no symbol". Pass `positions:[{file,line}]` instead of `file`/`line` to resolve several positions — e.g. every frame of a pasted stack trace — in one call (up to 25; each self-reports its own `resolution`) |
| `codemap_find` | Find symbols by name (offline; no embeddings). In no-Ollama/structure-only degraded mode it tokenizes the query on whitespace/camelCase and matches symbol/FQN or docstring; each hit carries `matched_in` (`"symbol"`, `"fqn"`, or `"docstring"`) explaining the match |
| `codemap_grep` | Exact text search (`pattern`, `regex`, `ignore_case`, `top`) over indexed file content — each hit resolved to its enclosing symbol (`symbol`/`fqn`/`kind`/`selector`). Offline, no embeddings. Distinct from `codemap_semantic` (meaning) and `codemap_find` (name) |
| `codemap_source` | Return source code by `symbol`, or exactly one body by `selector`. `brief:true` drops each match's `source` (keeping `signature`/`doc`/location) and sets `source_omitted:true` |
| `codemap_context` | **Everything about a symbol in one call** — definition (with source), callers, callees, value-reference wiring, covering tests + `test_commands`, blast-radius size, and annotations. `selector` keeps the full bundle on one definition; lists are capped with `*_total` counts (`test_commands` is derived from the full, uncapped test list). Uses the indexed graph only; optional component failures are explicit in `partial_errors`. `brief:true` drops each definition's `source` (keeping `signature`/`doc`/location) and sets `source_omitted:true` — everything else in the bundle is unchanged; follow up with `codemap_source` for the one body you actually need |
| `codemap_context_batch` | **Context for several symbols in one call** — each symbol's bundle (including its own `test_commands`) plus `combined_blast_radius` and `common_callers` (callers that reach two or more of them — a shared entrypoint/coupling). Build a component's mental model without N round-trips; deduped and capped at 25. Aggregate source bodies are capped at 64 KiB with `source_budget` and per-definition `source_truncations` metadata — or pass `brief:true` to drop every body up front (`source_omitted:true` per definition) instead of spending that budget |
| `codemap_projects` | List all registered projects and their index sizes |
| `codemap_docs` | Return the agent guide (`topic`: overview/workflow/commands/annotations/accuracy/ecosystem) so a harness can learn the tool |
| `codemap_annotate` | Pin a note / opaque `data` to a `symbol` or a `from`→`to` path (`source` label). Automated writers should pass a stable `external_id`; retries upsert within project + source and return the same annotation `id` with `action:"created\|updated\|unchanged"`. |
| `codemap_annotations` | List annotations: all, for a `symbol`, or for a `from`→`to` path |
| `codemap_unannotate` | Remove an annotation by `id` — prune/correct the knowledge layer |
| `codemap_branch_status` | Read-only git branch/commit state + the stable repo/branch keys used to key per-branch index snapshots |
| `codemap_branch_switch` | Switch the code index to a git branch — snapshot the old branch into fcheap, restore/reindex the new one. Defaults `to` to the current git branch; a non-git dir or detached HEAD is a no-op |
| `codemap_cache_save` | Save the current index (graph + vectors) to the fcheap stash vault, keyed by a tree hash — two identical working trees share one entry |
| `codemap_cache_restore` | Restore a matching fcheap cache entry (same tree hash + embedding profile), skipping extraction + embedding entirely; a miss is a no-op |
| `codemap_cache_list` | List cached indexes for a project (stash IDs, tree hashes, dates) |
| `codemap_cache_drop` | Drop a cached index by `stash_id` or `tree_hash` (from `codemap_cache_list`), or all cached indexes for the project |

The two an agent reaches for first: **`codemap_context`** bundles everything about a symbol
(definition, callers, callees, covering tests, blast radius) in **one call** instead of four, and
**`codemap_status`** reports index *freshness* so the agent reindexes before trusting a stale
answer. **`codemap_impact`** remains the deep change-analysis query — definition sites, callers,
the transitive blast radius, and which tests cover those paths, replacing many file reads.

## Exact source selectors

A name-only query stays backward-compatible: if six methods are named `Close`, it
returns their union and says so. Precise indexing makes the stored edges exact; to
choose one definition, project the fields already present on any symbol result:

```json
{
  "selector": {
    "file": "internal/graph/store.go",
    "start_line": 91,
    "fqn": "graph.Store.Close",
    "kind": "method"
  }
}
```

The same shape works on `source`, `context`, `callers`, `callees`, `references`, `impact`, and
`risk`; `path` accepts `from_selector` and `to_selector`. File+FQN+kind is the
preferred identity and `start_line` disambiguates/falls back, so inserting lines
above the declaration does not break the selector after reindex. A move or rename
can invalidate it and returns `found:false` rather than selecting an arbitrary
node. Database node IDs are deliberately absent from the public contract.

## Honesty signals (stable machine contract)

The analysis tools carry three kinds of signal so a consumer can act on confidence instead of guessing:

- **`candidates`** — on `codemap_callers`/`codemap_callees`/`codemap_impact`/`codemap_risk`/`codemap_source`/`codemap_context`, an ambiguous name-only query returns its merged result *and* `candidates:[{selector,signature,file,start_line}]` — the exact same merged set, already shaped as selectors. Re-query with `candidates[i].selector` to pin one definition without a separate `codemap_find`/`codemap_symbols` round-trip.
- **`matched_in`** — on `codemap_find` in no-embeddings/degraded mode, each hit reports whether it matched the query on `"symbol"`, `"fqn"`, or `"docstring"`, so a docstring-only hit (ranked below a name match) is distinguishable from an exact name hit.
- **`fusion`** — on `codemap_semantic`, the hybrid vector/BM25 weighting profile actually used (`"identifier"`, `"natural_language"`, or `"balanced"`), chosen adaptively from the query's shape unless the server is configured with `semantic.fusion: balanced` (or `CODEMAP_SEMANTIC_FUSION=balanced`).
- **`next`** — at most two executable `{tool,args,why}` follow-ups on `context`, `context_batch`, `impact`, `risk`, `file_impact`, and `review`. These are conditional recommendations, not a generic tool list: reindex when resolution is weak, run selected tests after a diff, or inspect risk for an untested hub.
- **`partial_errors`** — non-fatal optional-component failures on `context`/`context_batch` (`callers`, `callees`, `references`, `impact`, or `memory_recall`) and on the composed `map`/`explore` orientation reports. Hard prerequisites still fail the tool; otherwise usable sections are returned alongside bounded error entries.
- **`source_budget` / `source_truncations`** — explicit context-batch body budgeting. The aggregate source limit is 64 KiB; signatures, docs, and locations remain complete when bodies are shortened.
- **`source_omitted`** — on `codemap_source`/`codemap_context`/`codemap_context_batch`, set per definition when `brief:true` dropped its `source` body. Signature/doc/location stay; call `codemap_source` (without `brief`) for the one definition you actually need the body of. Pass `brief:true` any time a hub symbol's response feels heavy — it's the response-side counterpart to `mcp.profile` (which trims the *tool list*, not individual response bodies).
- **Structured errors** — MCP failures preserve stable `{code,message,hint}` metadata when the service returns a `CodedError`, while the visible text includes the remediation hint for clients that only render text.

- **`call_graph`** — a stable enum on `codemap_impact`/`codemap_callers`/`codemap_callees`/`codemap_references`/
  `codemap_review`/`codemap_context`/`codemap_hotspots`/`codemap_orphans`/`codemap_path`/
  `codemap_map`/`codemap_traverse`
  that a consumer switches on (no prose parsing):
  - `resolved` — every matched definition file has precise coverage (go/types for Go, language-server callHierarchy for TS/JS/Python/Vue)
  - `name` — name-based call graph (the Go/Ruby/Lua default; same-named symbols may over-match)
  - `unresolved` — plain calls in the language have no name-based edges and the index isn't precise (TS/JS/Python/Vue) — callers/blast/tests are **incomplete, not absent** (TS/JS may still carry name-based JSX component-usage candidates); reindex with `codemap_index precise:true`
  - `none` — no matching symbol / nothing to classify

  The free-form `resolution` sentence stays for humans. Map resolved→high, name→medium, unresolved/none→low confidence.
- **`codemap_coverage`** — the project-wide, per-file view behind `call_graph`: which files
  have a persisted precise-resolution row, when it was recorded, and whether that file's
  on-disk content has since drifted (independent of `codemap_status`'s aggregate
  `stale`/`staleness` counts, which describe the whole index, not one file). Use it to find
  out WHICH packages to trust before a `call_graph:"name"` on a broad query forces a
  worst-file assumption.
- **Reference honesty** — `codemap_references` and the embedded `context.references` list carry
  separate `coverage`, `confidence`, and stale signals. These describe stored callback/value wiring;
  `call_graph:"resolved"` never upgrades them, and empty partial/unavailable coverage is not proof of
  no registration.
- **`risk` on `codemap_review`** — one band for the whole diff (`level` unknown/low/medium/high, `score` 0..1, `factors`), folded from every changed symbol so a harness can gate verification on a single call instead of fanning `codemap_risk` out per symbol. `unknown` means at least one changed symbol lacks a usable call graph or `analysis_complete` is false, including mapping failures with zero safely identified symbols. It is absent for a complete zero-symbol diff and early no-repository/no-index degradation.
- **`stale` / `staleness`** on `codemap_review` (and `codemap_status`) — index drift since the last index. Normally refresh before trusting snapshot-based impact. A deleted file is the intentional exception: when its old nodes remain, `deletion_analysis` identifies `source:"last_index"` and selected tests come before the reindex action that will prune those nodes.
- **`confidence` on dependency samples** — `confirmed` means fresh precise or exact same-package evidence; `candidate` covers qualified name fan-out, package-scoped imports, and stale snapshots. Additive `confirmed_total`/`candidate_total` fields remain available when samples are capped.
- **`blast_radius` / `covering_tests` element shape** — both are `ImpactNode` objects (`symbol`, `fqn`, `kind`, `file`, `start_line`, `depth`, …; no `end_line`). `depth` is the blast-radius hop distance. This is the stable element contract.
- **`test_commands`** — `codemap_review`, `codemap_impact`, `codemap_context`, and each `codemap_context_batch` result carry a `test_commands` array: the same covering-test list turned into copy/paste-ready `go test -run`/`bun test`/`pytest` invocations by one shared helper, deduped and capped at 10. The same symbol yields identical commands on every surface, so a pre-edit `codemap_impact`/`codemap_context` call is just as directly runnable as post-edit `codemap_review`. Omitted (not an empty array) when there are no covering tests.
- **`schema_version` on `codemap_review`** — every successful review emits version `1` and
  conforms to `schemas/codemap.review.v1.schema.json` (Draft 2020-12,
  `urn:codemap:review:v1`). Consumers may accept an absent version as legacy v1, but must reject
  unknown future versions rather than treating contract drift as an authoritative empty radius.

## Branches & caching

Six tools keep the index aligned with the working tree and make reindexing cheap — both
best-effort over the sibling [fcheap](https://github.com/abdul-hamid-achik/fcheap) stash
vault, degrading to a normal index when `fcheap` isn't on `$PATH` (see
[Branches & caching](/branches) for the concepts):

- **`codemap_branch_status` / `codemap_branch_switch`** — a `git checkout` switches the
  code index too: snapshot the branch you're leaving into fcheap, restore the target
  branch's snapshot (or reindex when stale/absent). Keyed on the branch tip sha + embedding
  profile, so a restore only lands when it's still valid.
- **`codemap_cache_save` / `codemap_cache_restore` / `codemap_cache_list` /
  `codemap_cache_drop`** — content-addressed index caching: a tree hash of all indexed
  `(path, content_hash)` pairs keys each entry, so two identical working trees share one.
  A restore skips extraction + embedding entirely. `codemap_index` does this automatically
  around a `--reindex` (auto-restore before, auto-save after) unless `cache: false` is set.

All six are no-ops on a non-git directory or detached HEAD and never fail the index.

::: tip Transport
codemap's MCP server uses newline-delimited JSON-RPC over stdio (what Claude Code, Codex, and
OpenCode expect). codemap also speaks LSP to language servers, which uses Content-Length
framing — the two transports are kept strictly separate.
:::
