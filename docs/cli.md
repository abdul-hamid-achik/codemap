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
| `codemap context <symbol> [<symbol>...] [--depth N]` | **One call, everything about a symbol** — definition (signature + doc + source), callers, callees, covering tests, blast-radius size, and pinned annotations. Replaces separate `source`/`callers`/`callees`/`impact` calls; the `codemap_context` MCP tool returns the same JSON. **Pass several symbols** for a batch with `combined_blast_radius` and `common_callers` (shared entrypoints/coupling) — the `codemap_context_batch` MCP tool, for building a component's mental model in one round-trip |

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
| `codemap file-impact <file> [--depth N]` | **File-level impact** — "what happens if I change or delete this file?" Aggregates every symbol the file defines into the other files that depend on it, its blast radius, the covering tests, and two verdicts: `safe_to_delete` (nothing outside the file references it) and `breaking_change` (an externally-called symbol here is untested). The file-level peer of `impact` (a symbol) and `review` (a diff) — run it before a move/delete/split. |
| `codemap review [--since <ref>] [--staged] [--depth N]` | **Diff-scoped impact + test selection** — the command to run *after* editing. Maps your git diff (whole working tree by default; `--staged` for the index; `--since <ref>` for everything since a branch point) to the symbols it touches, then reports their union blast radius, the **tests to run** (regression test selection), and the changed symbols that are *untested* or are *hotspots* (many callers). Carries the same `stale`/`resolution` honesty signals as `impact`. Answers *"what did I just affect, and what should I run?"* in one call. |
| `codemap secret-impact [<KEY>...] [--via-vault <project>]` | **Rotation blast radius** for secret keys: which symbols read each key (`os.Getenv`/`process.env`/`os.environ`), the transitive callers affected, and covering tests (`untested:true` warns you're rotating a key no test reaches). Operates on key *names* only — never reads or returns values. `--via-vault` fetches the names from [tinyvault](/ecosystem). |
| `codemap required-keys <entrypoint> [--via-vault <project>]` | **Least-privilege key set**: which candidate keys an entrypoint's transitive call tree actually reads — pipe to `tvault seal`/`export` to grant only what a code path needs. One key per line. |
| `codemap risk <symbol> [--depth N]` | **Change-risk score** — "how careful should I be changing this?" in one number (0..1) + level (low/medium/high). Combines untested coverage, fan-in (direct callers), cross-package spread, and name ambiguity into a saturating score, with the factors behind it. Triage which of several edits is riskiest, or gate a change on test/review effort. |
| `codemap hotspots [--top N]` | Most-referenced symbols (hubs) |
| `codemap orphans [--top N]` | Functions/methods with no callers (dead-code candidates) |
| `codemap read-order [query] [--top N]` | **Where to start reading** — ranks entrypoints (`main()`, `cmd/` packages, module index files, exported public API) and load-bearing hubs (call-graph in-degree) into a newcomer's reading guide, each with the reason it ranked. Optional `query` narrows by name/path. The agent-facing answer to "I just landed in this repo — what do I read first?" |

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

## Branches & caching

The index is a snapshot of one working tree. Two features keep it aligned with your
git branch and make reindexing cheap when you return to a tree you've indexed before —
both best-effort over the sibling [fcheap](https://github.com/abdul-hamid-achik/fcheap)
stash vault, and both degrade to a normal index when `fcheap` isn't on `$PATH`. See
[Branches & caching](/branches) for the concepts.

| Command | Description |
|---|---|
| `codemap branch-status [path]` | Read-only git state (repo hash, branch, HEAD sha) used to key per-branch index snapshots |
| `codemap branch-switch [--to <branch>] [--from <branch>] [--root <dir>]` | Switch the code index to a git branch: snapshot the old branch into fcheap, restore/reindex the new one. `--to` defaults to the current git branch; a non-git dir or detached HEAD is a no-op |
| `codemap branch-switch --install-hook` | Install a git `post-checkout` hook that auto-switches the index on every `git checkout` (idempotent, preserves an existing hook; pins the running binary so it works off-PATH) |
| `codemap branch-snapshot [--branch <branch>] [--root <dir>]` | Stash the current branch's index into fcheap without switching (defaults to the current git branch) |
| `codemap cache save` | Stash the current index into the fcheap cache, keyed by a tree hash of all indexed `(path, content_hash)` pairs — two identical working trees share one entry |
| `codemap cache restore` | Restore the cached index matching the current working tree (skips extraction + embedding entirely); a miss is a no-op |
| `codemap cache list [--rebuild]` | List cached indexes for this repo (stash IDs, tree hashes, node/vector counts, age). `--rebuild` reconstructs from fcheap if the local pointer file is lost |
| `codemap cache drop --tree <hash> \| --all` | Drop one cached index by tree hash, or every cached index for this repo |

`codemap index` wraps cache for you behind `--cache` (on by default): it **auto-restores**
from a matching cache entry before a `--reindex` (skipping the full wipe+extract+embed
cycle), and **auto-saves** the freshly-built index to fcheap after a successful index.
With `--precise`, a restore only lands when the cache entry already has precise edges —
otherwise it falls through to a real reindex so the go/types pass actually runs. Pass
`--cache=false` to disable both for a run.

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

While a daemon is running, `codemap index` **delegates** the reindex to it over
the control socket instead of opening a second write handle (which would
collide with the daemon's exclusive database lock). The output is the normal
`Indexed ...` summary, annotated with `via daemon (pid N)`, and forwards
`--reindex` / `--precise` / `--no-lsp` / `--no-embed`. `--watch` is a no-op in
this case (the daemon is already watching); `--exclude-extra` is not forwarded
(stop + restart the daemon to change excludes).

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

### `review` — the post-edit query

Where `impact` starts from a *symbol*, `review` starts from your *diff*. After editing
`Save`, you don't have to name the symbol — `review` reads the working tree, finds the
changed symbols itself, and unions their impact:

```bash
$ codemap review
Review (store · working)
  changed files:   1
  changed symbols: 1
  blast radius:    2 (depth ≤ 3)
  covering tests:  1
  changed symbols:
     store.Save                           store.go:4
  tests to run:
     store.TestSave                       store_test.go:5
```

`--since main` reviews everything you changed on the branch (committed + uncommitted);
`--staged` reviews just what's staged. The `codemap_review` MCP tool returns the same
JSON — `{changed_symbols, blast_radius, covering_tests, untested, hotspots, stale,
resolution}` — so an agent harness can ask "what did I just affect, and what should I
run?" in a single call instead of parsing diffs and chaining per-symbol `impact`
queries. It degrades gracefully (a plain changed-file list with a note) when the
project isn't indexed or isn't a git repo.
