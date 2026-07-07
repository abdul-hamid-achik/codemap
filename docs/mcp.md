# MCP server

codemap is a stdio [Model Context Protocol](https://modelcontextprotocol.io) server, so AI
agents can query your code graph directly instead of reading dozens of files.

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

## Tools

All tools take an optional `path` (the project directory; defaults to the server's working
directory) and return JSON.

| Tool | Description |
|---|---|
| `codemap_init` | Register a project directory |
| `codemap_index` | Index/reindex a project (`reindex`, `no_embed`, `precise` → exact call edges via go/types for Go) |
| `codemap_status` | Index statistics **plus freshness** — a `stale` count of files changed/added/removed since indexing, so an agent reindexes before trusting results |
| `codemap_doctor` | Check the environment (go toolchain, gopls, TS/JS + Python language servers, Ollama) with install hints — diagnose why a language isn't indexed or semantic search is off |
| `codemap_semantic` | Semantic search by meaning (`query`, `top_k`) |
| `codemap_callers` | Functions/methods that call a symbol (`precise: true` → exact gopls callers for Go). Carries a stable `call_graph` enum (`resolved`/`name`/`unresolved`/`none`) so a consumer can down-weight confidence without parsing the `resolution` note |
| `codemap_callees` | Functions/methods a symbol calls (`precise: true` → exact gopls callees for Go). Same `call_graph` enum |
| `codemap_impact` | Callers + blast radius + covering tests (`depth`). Carries `call_graph` (`resolved`/`name`/`unresolved`/`none`) alongside the human `resolution` note — `unresolved` means the callers/blast/tests are unknown (not absent) on a TS/JS/Python/Vue symbol without `--precise` |
| `codemap_review` | **Diff-scoped impact + test selection** — the query to run *after* editing. Maps your git diff (working tree by default; `staged: true`; or `since` a ref) to the changed symbols, then returns their union `blast_radius`, the `covering_tests` to run, and the changed symbols that are `untested` or `hotspots` — plus an aggregate **`risk` band** (one `level`/`score`/`factors` for the whole diff, folded from every changed symbol so a harness can gate verification on one call), `stale`/`resolution`, and a stable `call_graph` enum. *"What did I just affect, and what should I run?"* in one call, instead of parsing diffs and chaining per-symbol `codemap_impact` |
| `codemap_file_impact` | **File-level impact** — "what happens if I change or DELETE this `file`?" Aggregates the file's symbols into `dependent_files`, `blast_radius`, `covering_tests`, and the verdicts `safe_to_delete` + `breaking_change`. The file-level peer of `codemap_impact`/`codemap_review` — run before a file move/delete/split |
| `codemap_required_keys` | **Least-privilege key set** — for an `entrypoint`, the candidate secret key NAMES its transitive call tree actually reads. Pipe to `tvault seal`/`export`; operates on names only, never values |
| `codemap_secret_impact` | **Secret-key rotation blast radius** — for each key NAME, the symbols that read it (`os.Getenv`/`os.environ`/`process.env`), the transitive callers affected, and covering tests (`untested:true` warns a key no test reaches). Operates on key NAMES only — never reads/returns values. Pairs with [tinyvault](/ecosystem); `via_vault` fetches names from it. Name-based unless the index is `--precise` |
| `codemap_hotspots` | Most-referenced symbols (`top`) |
| `codemap_risk` | **Change-risk score** for a `symbol` — untested coverage + fan-in + cross-package spread + name ambiguity combined into a 0..1 `score` + `level` (low/medium/high), with the `factors` behind it. "How careful should I be changing this?" / which edit is riskiest |
| `codemap_orphans` | Dead-code candidates (`top`) |
| `codemap_read_order` | **Where to start reading** — ranks entrypoints (`main()`, `cmd/`, module index files, exported API) + call-graph hubs into a reading guide, each with a reason and score. Optional `query` narrows it. Run on first contact with an unfamiliar repo, then drill the top entries with `codemap_context` |
| `codemap_path` | Shortest call path (`from`, `to`) |
| `codemap_related_files` | Files structurally related to a `file` via the call/test graph — its callers', callees', and covering-test files, each with a reason (`caller`/`callee`/`test`) and confidence. Graph-accurate alternative to import-text heuristics |
| `codemap_symbols` | List the symbols defined in a `file` (structured alternative to reading it) |
| `codemap_symbol_at` | Resolve a `file:line` position to its enclosing symbol (FQN, kind, range) — join external `file:line` results (search hits, stack traces, diffs) onto the graph. `resolution` is `exact`/`enclosing`/`none`. The `indexed` field is `false` when the project hasn't been indexed yet, so an agent knows to call `codemap_index` before concluding "no symbol" |
| `codemap_find` | Find symbols by name (offline; no embeddings) |
| `codemap_source` | Return a `symbol`'s source code (its body, read from the indexed line range) |
| `codemap_context` | **Everything about a symbol in one call** — definition (with source), callers, callees, covering tests, blast-radius size, and annotations; lists capped with `*_total` counts so the bundle stays small. Replaces source+callers+callees+impact for orientation |
| `codemap_context_batch` | **Context for several symbols in one call** — each symbol's full bundle plus `combined_blast_radius` and `common_callers` (callers that reach two or more of them — a shared entrypoint/coupling). Build a component's mental model without N round-trips; deduped, capped at 25 |
| `codemap_projects` | List all registered projects and their index sizes |
| `codemap_docs` | Return the agent guide (`topic`: overview/workflow/commands/annotations/accuracy/ecosystem) so a harness can learn the tool |
| `codemap_annotate` | Pin a note / opaque `data` to a `symbol` or a `from`→`to` path (`source` label) |
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

## Honesty signals (stable machine contract)

The analysis tools carry two kinds of signal so a consumer can act on confidence, not just results:

- **`call_graph`** — a stable enum on `codemap_impact`/`codemap_callers`/`codemap_callees`/
  `codemap_review`/`codemap_context` a consumer switches on (no prose parsing):
  - `resolved` — precise edges (go/types for Go, language-server callHierarchy for TS/JS/Python/Vue)
  - `name` — name-based call graph (Go default; same-named methods may over-match)
  - `unresolved` — the language has no name-based call edges and the index isn't precise (TS/JS/Python/Vue) — callers/blast/tests are **unknown, not absent**; reindex with `codemap_index precise:true`
  - `none` — no matching symbol / nothing to classify

  The free-form `resolution` sentence stays for humans. Map resolved→high, name→medium, unresolved/none→low confidence.
- **`risk` on `codemap_review`** — one band for the whole diff (`level` low/medium/high, `score` 0..1, `factors`), folded from every changed symbol so a harness can gate verification on a single call instead of fanning `codemap_risk` out per symbol. Absent when the diff maps to no indexed symbols.
- **`stale` / `staleness`** on `codemap_review` (and `codemap_status`) — index drift since the last index; surface a "reindex before trusting the blast radius" warning when set. The blast radius is computed from the snapshot, so a stale index can miss/misattribute — honest by design.
- **`blast_radius` / `covering_tests` element shape** — both are `ImpactNode` objects (`symbol`, `fqn`, `kind`, `file`, `start_line`, `depth`, …; no `end_line`). `depth` is the blast-radius hop distance. This is the stable element contract.

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
