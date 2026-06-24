# CLI

Every query command accepts `--json` for machine-readable output.

## Project management

| Command | Description |
|---|---|
| `codemap init [--local]` | Register the current directory as a project |
| `codemap index [--reindex] [--no-embed] [--precise]` | Index (incremental); `--reindex` rebuilds, `--no-embed` skips embeddings, `--precise` resolves call edges exactly (Go via go/types, TypeScript via callHierarchy) |
| `codemap status` | Show index statistics (nodes, edges, languages, kinds) |
| `codemap projects` | List all registered projects and their index sizes |
| `codemap docs [topic]` | Print the agent guide (overview, workflow, commands, annotations, accuracy, ecosystem) |
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

The fast default uses the indexed graph (name-based resolution; same-named methods can over-match,
e.g. `callers Close` lists callers of every `Close`). **The best fix is to reindex once with
`codemap index --precise`** — the unified exact-resolution pass (a pure-Go go/types pass for Go,
`typescript-language-server` callHierarchy for TypeScript), which makes *every* query — callers,
callees, impact, hotspots, path — exact, with no per-query flag. (TypeScript has no name-based call
edges, so `--precise` is what gives TS a call graph at all.) For a one-off exact Go answer without
reindexing, `callers`/`callees` also accept `--lsp` (gopls); both `--precise` and `--lsp` degrade to
name-based with a note when the toolchain/module isn't available — never a hard error.

On a name-based index the analysis commands flag their limits honestly: `callers`/`impact` note when
a name resolves to multiple definitions, `hotspots` marks name-collision inflation, and `orphans`
follows functions wired by value (handlers like cobra `RunE` / `mux.HandleFunc`) but can't see
callers reached via interface dispatch or reflection — treat its output as dead-code *candidates*.
`index --precise` removes the call-edge inflation outright. See
[Accuracy](https://github.com/abdul-hamid-achik/codemap#accuracy-name-based-vs-precise).

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
