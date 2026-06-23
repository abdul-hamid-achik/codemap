# CLI

Every query command accepts `--json` for machine-readable output.

## Project management

| Command | Description |
|---|---|
| `codemap init [--local]` | Register the current directory as a project |
| `codemap index [--reindex] [--no-embed]` | Index (incremental); `--reindex` rebuilds, `--no-embed` skips embeddings |
| `codemap status` | Show index statistics (nodes, edges, languages, kinds) |
| `codemap projects` | List all registered projects and their index sizes |
| `codemap docs [topic]` | Print the agent guide (overview, workflow, commands, accuracy, ecosystem) |
| `codemap annotate <sym> \| <from> <to>` | Pin a `--note` and/or `--data` (e.g. DB rows) to a symbol or call path (`--source`) |
| `codemap annotations [<sym> \| <from> <to>]` | List annotations (all/node/path); `--rm <id>` to remove |

## Navigation

| Command | Description |
|---|---|
| `codemap callers <symbol>` | Functions/methods that call a symbol |
| `codemap callers <symbol> --lsp` | **Precise** callers via gopls (Go) — exact, not inflated by same-named symbols |
| `codemap callees <symbol>` | Functions/methods a symbol calls |
| `codemap callees <symbol> --lsp` | **Precise** callees via gopls (Go) |
| `codemap path <from> <to>` | Shortest call path between two symbols |
| `codemap symbols <file>` | Outline a file's symbols with their signatures (a structured alternative to reading it) |
| `codemap source <symbol>` | Print a symbol's source code (the body behind its signature) |

The fast default uses the indexed graph (name-based resolution; same-named methods can
over-match). `--lsp` asks the language server (gopls) for *exact* callers — e.g. `callers Close`
might list every caller of any `Close`, while `callers Close --lsp` lists only the callers of
the specific resolved method.

The same name-based model affects the analysis commands: `hotspots` can rank ubiquitous method
names (`String`, `Error`) high with inflated counts, and `orphans` can't see callers reached via
interface dispatch or reflection — treat its output as dead-code *candidates*. See
[Accuracy](https://github.com/abdul-hamid-achik/codemap#accuracy-name-based-graph-vs-precise-lsp).

## Analysis

| Command | Description |
|---|---|
| `codemap impact <symbol> [--depth N]` | Definition sites, direct callers, blast radius, and covering tests |
| `codemap hotspots [--top N]` | Most-referenced symbols (hubs) |
| `codemap orphans [--top N]` | Functions/methods with no callers (dead-code candidates) |

## Semantic

| Command | Description |
|---|---|
| `codemap semantic <query> [--top N]` | Meaning-based search across the indexed graph |
| `codemap find <query> [--top N]` | Find symbols by name, with signatures (offline; no embeddings needed) |

On a structure-only project (indexed with `--no-embed`, or before Ollama was available), `codemap semantic`
returns no hits with a short note explaining there are no embeddings — and the JSON carries `"mode": "none"`
plus that `"note"` — so you know to embed the index or fall back to `codemap find`. It never calls the
embedder or creates an empty vector store in that case.

## Surfaces

| Command | Description |
|---|---|
| `codemap serve` | Run the [MCP server](/mcp) over stdio |
| `codemap studio` | Open the interactive [TUI](/studio) |
| `codemap version` | Print version information |

## Example

```bash
$ codemap impact AddNode --depth 3
Impact of AddNode (codemap)
  defined:        internal/graph/store.go:185
  direct callers: 8
  blast radius:   14 (depth ≤ 3)
  tests covering: 11
   ✓ [1] TestAddGetNode    internal/graph/graph_test.go:70
     [1] indexFile         internal/index/indexer.go:182
     [2] IndexProject      internal/index/indexer.go:85
```
