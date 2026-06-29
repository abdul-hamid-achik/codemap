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
| `codemap_callers` | Functions/methods that call a symbol (`precise: true` → exact gopls callers for Go) |
| `codemap_callees` | Functions/methods a symbol calls (`precise: true` → exact gopls callees for Go) |
| `codemap_impact` | Callers + blast radius + covering tests (`depth`) |
| `codemap_review` | **Diff-scoped impact + test selection** — the query to run *after* editing. Maps your git diff (working tree by default; `staged: true`; or `since` a ref) to the changed symbols, then returns their union `blast_radius`, the `covering_tests` to run, and the changed symbols that are `untested` or `hotspots` — plus `stale`/`resolution` signals. *"What did I just affect, and what should I run?"* in one call, instead of parsing diffs and chaining per-symbol `codemap_impact` |
| `codemap_file_impact` | **File-level impact** — "what happens if I change or DELETE this `file`?" Aggregates the file's symbols into `dependent_files`, `blast_radius`, `covering_tests`, and the verdicts `safe_to_delete` + `breaking_change`. The file-level peer of `codemap_impact`/`codemap_review` — run before a file move/delete/split |
| `codemap_hotspots` | Most-referenced symbols (`top`) |
| `codemap_risk` | **Change-risk score** for a `symbol` — untested coverage + fan-in + cross-package spread + name ambiguity combined into a 0..1 `score` + `level` (low/medium/high), with the `factors` behind it. "How careful should I be changing this?" / which edit is riskiest |
| `codemap_orphans` | Dead-code candidates (`top`) |
| `codemap_read_order` | **Where to start reading** — ranks entrypoints (`main()`, `cmd/`, module index files, exported API) + call-graph hubs into a reading guide, each with a reason and score. Optional `query` narrows it. Run on first contact with an unfamiliar repo, then drill the top entries with `codemap_context` |
| `codemap_path` | Shortest call path (`from`, `to`) |
| `codemap_symbols` | List the symbols defined in a `file` (structured alternative to reading it) |
| `codemap_find` | Find symbols by name (offline; no embeddings) |
| `codemap_source` | Return a `symbol`'s source code (its body, read from the indexed line range) |
| `codemap_context` | **Everything about a symbol in one call** — definition (with source), callers, callees, covering tests, blast-radius size, and annotations; lists capped with `*_total` counts so the bundle stays small. Replaces source+callers+callees+impact for orientation |
| `codemap_context_batch` | **Context for several symbols in one call** — each symbol's full bundle plus `combined_blast_radius` and `common_callers` (callers that reach two or more of them — a shared entrypoint/coupling). Build a component's mental model without N round-trips; deduped, capped at 25 |
| `codemap_projects` | List all registered projects and their index sizes |
| `codemap_docs` | Return the agent guide (`topic`: overview/workflow/commands/annotations/accuracy/ecosystem) so a harness can learn the tool |
| `codemap_annotate` | Pin a note / opaque `data` to a `symbol` or a `from`→`to` path (`source` label) |
| `codemap_annotations` | List annotations: all, for a `symbol`, or for a `from`→`to` path |
| `codemap_unannotate` | Remove an annotation by `id` — prune/correct the knowledge layer |

The two an agent reaches for first: **`codemap_context`** bundles everything about a symbol
(definition, callers, callees, covering tests, blast radius) in **one call** instead of four, and
**`codemap_status`** reports index *freshness* so the agent reindexes before trusting a stale
answer. **`codemap_impact`** remains the deep change-analysis query — definition sites, callers,
the transitive blast radius, and which tests cover those paths, replacing many file reads.

::: tip Transport
codemap's MCP server uses newline-delimited JSON-RPC over stdio (what Claude Code, Codex, and
OpenCode expect). codemap also speaks LSP to language servers, which uses Content-Length
framing — the two transports are kept strictly separate.
:::
