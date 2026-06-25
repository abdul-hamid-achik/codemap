# CLI

Every query command accepts `--json` for machine-readable output.

## Project management

| Command | Description |
|---|---|
| `codemap init [--local]` | Register the current directory as a project |
| `codemap index [--reindex] [--no-embed] [--no-lsp] [--precise]` | Index (incremental — re-indexes changed files and prunes deleted ones); `--reindex` rebuilds, `--no-embed` skips embeddings, `--no-lsp` skips the language-server backend (Go is still indexed via `go/parser`; TS/JS/Python files are skipped — no `typescript-language-server`/`pyright` spawned), `--precise` resolves call edges exactly (Go via go/types, TypeScript/JavaScript/Python via callHierarchy) |
| `codemap status` | Show index statistics (nodes, edges, languages, kinds), plus **index freshness** — warns when files have changed/been added/removed since the last index (a `stale` field in `--json`), so you know to reindex before trusting queries. Also reports a running [background daemon](#background-daemon) (a `daemon` object in `--json`) |
| `codemap doctor` | Check the environment — go toolchain, gopls, language servers (TS/JS, Python), Ollama embeddings, and the [background daemon](#background-daemon) — with install hints (`--json`) |
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
| `codemap symbol-at <file>:<line>` | Resolve a file:line position to its enclosing symbol (FQN, kind, range) — the entry point for joining external `file:line` results (search hits, stack traces, diffs) onto the graph. Also `codemap impact --at <file>:<line>` |
| `codemap related-files <file>` | Files related to a file via the call/test graph — its callers', callees', and covering-test files, each with a reason (`caller`/`callee`/`test`) and confidence |
| `codemap source <symbol>` | Print a symbol's source code (the body behind its signature) |
| `codemap context <symbol> [--depth N]` | **One call, everything about a symbol** — definition (signature + doc + source), callers, callees, covering tests, blast-radius size, and pinned annotations. Replaces separate `source`/`callers`/`callees`/`impact` calls; the `codemap_context` MCP tool returns the same JSON for agents |

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
| `codemap secret-impact [<KEY>...] [--via-vault <project>]` | **Rotation blast radius** for secret keys: which symbols read each key (`os.Getenv`/`process.env`/`os.environ`), the transitive callers affected, and covering tests (`untested:true` warns you're rotating a key no test reaches). Operates on key *names* only — never reads or returns values. `--via-vault` fetches the names from [tinyvault](/ecosystem). |
| `codemap hotspots [--top N]` | Most-referenced symbols (hubs) |
| `codemap orphans [--top N]` | Functions/methods with no callers (dead-code candidates) |

## Semantic

| Command | Description |
|---|---|
| `codemap semantic <query> [--top N]` | Meaning-based search across the indexed graph (alias: `codemap search`) |
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

## Background daemon

The daemon watches the working tree and keeps the index fresh automatically —
incrementally re-indexing on save and throttling embeddings so it never hammers
Ollama. Tune it via the `daemon:` config block / `CODEMAP_DAEMON_*` env / the flags
below (see [Configuration](/configuration)).

| Command | Description |
|---|---|
| `codemap daemon start [path]` | Run the daemon in the foreground (watches the project; background it with `&`, stop with Ctrl-C). Flags: `--no-embed` (structure only, no Ollama), `--debounce`, `--idle-timeout`, `--embed-rps`, `--embed-max-in-flight`, `--embed-cache-size` |
| `codemap daemon status` | Show whether a daemon is running and what it's watching |
| `codemap daemon stop` | Stop the running daemon |

When a daemon is running, `codemap status` and the `codemap_status` MCP tool report
it (a `daemon` object in `--json`), `codemap doctor` lists it as a health check, and
the [studio](/studio) header shows a live `● daemon` indicator.

## Example

Given a small `store` package:

```go
// store.go
package store

func openDB() error       { return nil }
func Save(x int) error    { return openDB() }
func Delete(id int) error { return openDB() }

// store_test.go
func TestSave(t *testing.T) { _ = Save(1) }
```

`impact` answers *what breaks if I change this, and what do I run to check?* — here,
`openDB` is reached by `Save` and `Delete`, and `TestSave` (✓) transitively covers it:

```bash
$ codemap impact openDB --depth 2
Impact of openDB (store)
  defined:        store.go:3
  direct callers: 2
  blast radius:   3 (depth ≤ 2)
  tests covering: 1
  covering tests (run these):
     store.TestSave                       store_test.go:5
  affected (blast radius):
     [1] store.Save                           store.go:4
     [1] store.Delete                         store.go:5
   ✓ [2] store.TestSave                       store_test.go:5
```

For symbols with many dependents the human-facing lists are capped — the nearest
blast-radius nodes and the first covering tests, with a `… (N more)` line. `--json`
always carries the complete set. (The README shows the same command run on codemap
itself.)
