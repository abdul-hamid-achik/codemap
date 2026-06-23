# MCP server

codemap is a stdio [Model Context Protocol](https://modelcontextprotocol.io) server, so AI
agents can query your code graph directly instead of reading dozens of files.

## Register it

**Claude Code**

```bash
claude mcp add codemap -- codemap serve
```

**Generic MCP config**

```json
{
  "mcpServers": {
    "codemap": { "command": "codemap", "args": ["serve"] }
  }
}
```

## Tools

All tools take an optional `path` (the project directory; defaults to the server's working
directory) and return JSON.

| Tool | Description |
|---|---|
| `codemap_init` | Register a project directory |
| `codemap_index` | Index/reindex a project (`reindex`, `no_embed`) |
| `codemap_status` | Index statistics |
| `codemap_semantic` | Semantic search by meaning (`query`, `top_k`) |
| `codemap_callers` | Functions/methods that call a symbol (`precise: true` → exact gopls callers for Go) |
| `codemap_callees` | Functions/methods a symbol calls (`precise: true` → exact gopls callees for Go) |
| `codemap_impact` | Callers + blast radius + covering tests (`depth`) |
| `codemap_hotspots` | Most-referenced symbols (`top`) |
| `codemap_orphans` | Dead-code candidates (`top`) |
| `codemap_path` | Shortest call path (`from`, `to`) |
| `codemap_symbols` | List the symbols defined in a `file` (structured alternative to reading it) |
| `codemap_find` | Find symbols by name (offline; no embeddings) |
| `codemap_source` | Return a `symbol`'s source code (its body, read from the indexed line range) |
| `codemap_projects` | List all registered projects and their index sizes |
| `codemap_docs` | Return the agent guide (`topic`: overview/workflow/commands/accuracy/ecosystem) so a harness can learn the tool |
| `codemap_annotate` | Pin a note / opaque `data` to a `symbol` or a `from`→`to` path (`source` label) |
| `codemap_annotations` | List annotations: all, for a `symbol`, or for a `from`→`to` path |

The flagship is **`codemap_impact`** — one call returns a symbol's definition sites, callers,
the transitive blast radius, and which tests cover those paths, replacing many file reads.

::: tip Transport
codemap's MCP server uses newline-delimited JSON-RPC over stdio (what Claude Code, Codex, and
OpenCode expect). codemap also speaks LSP to language servers, which uses Content-Length
framing — the two transports are kept strictly separate.
:::
