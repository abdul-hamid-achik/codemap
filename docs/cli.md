---
description: Complete codemap CLI reference for indexing, navigation, impact analysis, search, caching, and automation.
---

# CLI

The full `codemap` command reference — for people running it by hand and for scripts/CI
piping `--json`. New here? Start with [Quick Start](/quick-start); wiring an agent instead
of a terminal? See [codemap for agents](/agents) and the [MCP server](/mcp). Every query
command below accepts `--json` for machine-readable output.

## Global options

These persistent options can be placed before or after a subcommand:

| Option | Description |
|---|---|
| `-C, --path <dir>` | Run against a project directory instead of the current working directory; this is the CLI counterpart of MCP's uniform `path` input. |
| `-c, --config <file>` | Load a specific config file instead of the normal precedence chain. |
| `--json` | Emit machine-readable report output and structured failure envelopes where the command has a JSON contract. |
| `--embed-provider ollama` | Override the embedding provider (currently only Ollama). |
| `--embed-model <name>` / `--ollama-url <url>` | Override the embedding model or Ollama endpoint. |
| `--embed-dimensions <n>` / `--embed-distance <cosine\|dot\|euclidean>` | Override the vector profile; changing either on an existing collection requires a reindex. |

## Project management

Register a directory, build its graph, and check on it — the commands you run before
anything else.

| Command | Description |
|---|---|
| `codemap init [--local]` | Register the current directory as a project |
| `codemap index [--reindex] [--no-embed] [--no-lsp] [--precise] [--watch] [--via-vault <project>] [--cache=<bool>] [--no-tips]` | Index (incremental — re-indexes changed files and prunes deleted ones); `--reindex` rebuilds, `--no-embed` skips embeddings, `--no-lsp` skips the language-server backend (Go is still indexed via `go/parser`; TS/JS/Python/Vue SFC files are skipped — no `typescript-language-server`/`pyright` spawned), `--precise` resolves call edges exactly (Go via go/types, TypeScript/JavaScript/Python via callHierarchy; Vue SFCs get symbols + `defines` + import edges only, no call graph yet), `--watch` hands off to the [background daemon](#background-daemon) after indexing once, `--via-vault` re-execs inside `tvault run` so language servers get private-registry creds (see [secret-impact](#analysis) / [ecosystem](/ecosystem)), `--cache=false` disables best-effort fcheap restore/save, and `--no-tips` suppresses post-index advice for scripts/CI |
| `codemap status [--full] [--skip-stale]` | Show index statistics (nodes, edges, languages, kinds), plus **index freshness** — warns when files have changed/been added/removed since the last index (a `stale` field in `--json`), so you know to reindex before trusting queries. The default status deliberately skips opening the local vector store; use `--full` when the exact local vector count is needed (it can use substantial memory). `--skip-stale` skips the dirty-tree drift walk for cheap readiness probes (e.g. Cortex setup); default status still reports stale. JSON exposes `vectors_known` to distinguish a skipped count from zero. `precise` contains an explicit boolean for each indexed call-graph language: `true` only when every indexed file in that language completed precise resolution at the last index. Combine it with `stale` and each query's `call_graph`; `precise_edges` is diagnostic only. Also reports a running [background daemon](#background-daemon) (a `daemon` object in `--json`) |
| `codemap doctor` | Check the environment — go toolchain, gopls, language servers (TS/JS, Python, Vue SFC via the same `typescript-language-server`), Ollama embeddings, and the [background daemon](#background-daemon) — with install hints (`--json`) |
| `codemap projects` | List all registered projects and their index sizes |
| `codemap docs [topic]` | Print the agent guide (overview, workflow, commands, annotations, accuracy, ecosystem) |
| `codemap structural-manifest --json` | Emit the single-response `codemap.structural-manifest.v1` preflight for `export-symbols`: explicit export schema version, project identity, the exact export fingerprint, total records, completeness, and working-tree freshness counters. It streams indexed metadata without reading source bodies or loading the full export. |
| `codemap export-symbols [--offset N] [--limit N] [--max-content-bytes N] --json` | Export deterministic, paginated structural records under `codemap.structural-export.v1`: a contiguous global ordinal, durable selectors, hashes, signature/doc, and bounded current content. Stale/missing/unsafe content is omitted explicitly. This is the CLI-only boundary consumed by vecgrep `structural_chunks` modes `auto`, `off`, and `required`; it never shares codemap's DB or Go packages. |
| `codemap annotate <sym> \| <from> <to>` | Pin a `--note` and/or `--data` (e.g. DB rows) to a symbol or call path (`--source`). Automation should add `--external-id <id>`: retries upsert one row within project + source and report `action:"created\|updated\|unchanged"`; reads return the external ID. |
| `codemap annotations [<sym> \| <from> <to>]` | List annotations (all/node/path); `--rm <id>` to remove |

Index-specific configuration overrides are also available as flags: `--exclude`,
`--exclude-extra`, `--max-file-bytes`, `--embed-batch-size`, `--embed-concurrency`, and
`--embed-max-chars`. See [Configuration](/configuration) for precedence and semantics.

## Configuration introspection

Read-only commands for checking which config file codemap resolved and what it contains
— useful when precedence (flag > env > project file > global file) isn't giving the
value you expect.

| Command | Description |
|---|---|
| `codemap config path` | Show the resolved config file path (honours `CODEMAP_CONFIG`, `~`, and XDG defaults) |
| `codemap config show` | Print the resolved config as YAML (or `--json` for machine-readable) |
| `codemap config show --json` | Same values as `show`, as structured JSON for agents |

## Navigation

Follow the call graph from a symbol you already have a name for — who calls it, what it
calls, where it's registered, and how it connects to another symbol.

| Command | Description |
|---|---|
| `codemap callers <symbol>` | Functions/methods that call a symbol |
| `codemap callers <symbol> --precise` | **Precise one-off** callers via the language server — exact without reindexing |
| `codemap callers --at <file>:<line>` | Callers of exactly one definition (same selector is available on `callees`) |
| `codemap callees <symbol>` | Functions/methods a symbol calls |
| `codemap callees <symbol> --precise` | **Precise one-off** callees via the language server |
| `codemap references <symbol>` | Places a function/method is used as a value rather than called — callbacks, handlers, and registrations. `--at file:line` selects one definition; JSON reports bounded sites plus totals, partial/unavailable coverage, stale state, and confirmed/candidate confidence. The stored edge identifies the enclosing symbol or file, not the exact expression column. |
| `codemap path <from> <to>` | Shortest call path between two symbols. Unique FQNs such as `app.Controller.Run` and `app.Store.Save` select exact endpoints; human output always reports `call_graph` confidence and any resolution/staleness warning. |
| `codemap symbols <file>` | Outline a file's symbols with their signatures (a structured alternative to reading it) |
| `codemap symbol-at <file>:<line> [<file>:<line>...]` | Resolve one or more positions to their enclosing symbols (FQN, kind, range, reusable selector). Multiple positions are batched in one call; at most the first 25 are resolved and an over-limit response includes a note. The `indexed` field in `--json` distinguishes an unindexed project (`indexed:false`) from a real miss (`indexed:true`, `resolution:none`). `callers`, `callees`, `source`, `context`, `impact`, and `risk` also accept `--at` to select one exact definition. |
| `codemap related-files <file>` | Files related to a file via the call/test graph — its callers', callees', and covering-test files, each with a reason (`caller`/`callee`/`test`) and confidence |
| `codemap source <symbol> [--brief]` | Print source for matching definitions; use `--at <file>:<line>` for exactly one. `--brief` drops each match's body (keeping signature/doc/location) and sets `source_omitted:true` in `--json` — a cheaper first look at a hub definition |
| `codemap context <symbol> [<symbol>...] [--depth N] [--brief]` | **One call, everything about a symbol** — definition (signature + doc + source), callers, callees, value-reference wiring, covering tests + runnable `test_commands`, blast-radius size, and pinned annotations. Uses the indexed graph only (never launches a language server implicitly); unresolved relationships stay explicit. Replaces separate `source`/`callers`/`callees`/`references`/`impact` calls; the `codemap_context` MCP tool returns the same JSON. **Pass several symbols** for a batch with `combined_blast_radius` and `common_callers` (shared entrypoints/coupling) — each result carries its own `test_commands`. Batch source bodies share a 64 KiB budget disclosed by `source_budget`/`source_truncations`; optional component failures appear in `partial_errors` without discarding usable context. `--brief` drops every definition's source body (keeping signature/doc/location, `source_omitted:true`) — the token-diet follow-up for a hub symbol whose context feels heavy; everything else in the bundle is unchanged. |

The fast default uses the indexed graph (name-based resolution; same-named methods can over-match,
e.g. `callers Close` lists callers of every `Close`). **The best fix is to reindex once with
`codemap index --precise`** — the unified exact-resolution pass (a pure-Go go/types pass for Go,
`typescript-language-server` callHierarchy for TypeScript/JavaScript, and pyright callHierarchy for
Python). Successful coverage is recorded per file;
a query is `resolved` only when all matched definition files completed the pass, while partial
failures remain `name`/`unresolved`. (TypeScript and JavaScript get name-based candidate edges for
JSX component usage, imports, and Next.js framework wiring; plain function calls there — and all
Python calls — have no name-based edges, so `--precise` is what gives covered files a complete
call graph, superseding the candidates per file.) Vue SFCs currently provide script-block
symbols, `defines`, and import edges only; precise indexing does not add Vue call edges yet. For a one-off exact answer without
reindexing, `callers`/`callees` accept `--precise`; it degrades to the indexed graph with a note
when the language server isn't available — never a hard error. The old `--lsp` spelling remains a
hidden compatibility alias, but new scripts and people should use `--precise`.

Precise indexing fixes call **edges**; a bare name can still match several exact
definitions. Use `--at <file>:<line>` to keep callers/callees/references/source/context/impact/risk
on one definition. `--at` replaces the positional symbol and cannot be combined
with one. JSON/MCP consumers use the field-compatible
`selector:{file,start_line,fqn,kind}` described in the MCP guide. The selector prefers
file+FQN+kind, so ordinary line shifts survive reindex; moves/renames return a miss.

On a name-based index the analysis commands flag their limits honestly: `callers`/`impact` note when
a name resolves to multiple definitions, `hotspots` marks name-collision inflation, and `orphans`
follows functions wired by value (handlers like cobra `RunE` / `mux.HandleFunc`; JSX-rendered
components and Next.js framework-invoked exports in TS/JS) but can't see
callers reached via interface dispatch or reflection, components passed only as props
(`Link={AuthLink}`), or wrapped default exports (`export default memo(Page)`) — treat its output
as dead-code *candidates*.
`index --precise` removes the call-edge inflation outright. See
[Accuracy](https://github.com/abdul-hamid-achik/codemap#accuracy-name-based-vs-precise).

## Analysis

The commands built around a change: how far it reaches, what tests cover it, how risky
it is, and — for `review` — what your current diff already touched.

| Command | Description |
|---|---|
| `codemap impact <symbol> [--depth N]` | Definition sites, direct callers, blast radius, covering tests, and copy/paste-ready `test_commands`. `--at file:line` selects one definition; repeat `--at` to analyze up to 25 positions in one ordered, partial-success batch. A missed frame carries item-level `error.code:"symbol_not_found"`. Add `--batch` to force the stable batch envelope for one position. |
| `codemap dependencies <file>` | Direct inbound call/reference/import evidence grouped by dependent file and edge kind. Every relationship is classified as **confirmed** or **candidate** with a reason (`precise`, `same_package`, `name_fanout`, `package_scope`, or `stale_snapshot`); totals and bounded source→target samples preserve that confidence. Coverage remains explicit for calls, references, imports, runtime wiring, and external consumers. Missing evidence never means safe. |
| `codemap file-impact <file> [--depth N]` | **File-level impact** — "what happens if I change or delete this file?" Returns grouped dependency evidence, coverage, blast radius, tests, and `delete_verdict`. Only fresh, confirmed, file-scoped indexed evidence can prove `unsafe`; name-fanout candidates, stale evidence, and Go's package-scoped imports remain `unknown` for the exact file. Missing evidence never proves safety; legacy `safe_to_delete` stays false. |
| `codemap review [--since <ref>] [--staged] [--depth N] [--fail-on-risk <low\|medium\|high>] [--fail-on-untested]` | **Diff-scoped impact + test selection** — the command to run *after* editing. Maps your git diff (whole working tree by default; `--staged` for the index; `--since <ref>` for everything since a branch point) to the symbols it touches, then reports their union blast radius, the **tests to run** (regression test selection), and the changed symbols that are *untested* or are *hotspots* (many callers). Deleted files are analyzed from definitions retained in the last index; run the selected tests before reindexing removes that evidence. Carries aggregate `risk`, `stale`/`resolution`, and stable `call_graph` honesty signals. `--fail-on-risk`/`--fail-on-untested` gate on that data — see [Gating a commit or script](#gating-a-commit-or-script). |
| `codemap secret-impact [<KEY>...] [--via-vault <project>]` | **Rotation blast radius** for secret keys: which symbols read each key (`os.Getenv`/`process.env`/`os.environ`), the transitive callers affected, and covering tests (`untested:true` warns you're rotating a key no test reaches). Operates on key *names* only — never reads or returns values. `--via-vault` fetches the names from [tinyvault](/ecosystem). Each request accepts at most 256 unique names, 256 bytes per name. |
| `codemap required-keys <entrypoint> [--via-vault <project>]` | **Least-privilege key set**: which candidate keys an entrypoint's transitive call tree actually reads — pipe to `tvault seal`/`export` to grant only what a code path needs. One key per line. Candidate input is capped at 256 unique names, 256 bytes per name. |
| `codemap risk <symbol> [--depth N] [--fail-on-risk <low\|medium\|high>]` | **Change-risk score** — "how careful should I be changing this?" in one number (0..1) + level (unknown/low/medium/high). Combines untested coverage, fan-in (direct callers), cross-package spread, and name ambiguity into a saturating score, with the factors behind it. If the call graph is unavailable, the level is `unknown` rather than a misleading `low`. Use `--at file:line` for one definition. `--fail-on-risk` gates on the level — see [Gating a commit or script](#gating-a-commit-or-script). |
| `codemap hotspots [--top N]` | Most-referenced symbols (hubs) |
| `codemap orphans [--top N]` | Functions/methods with no callers (dead-code candidates) |
| `codemap coverage [--prefix P] [--lang L] [--uncovered] [--files] [--top N]` | Per-file precise call-graph coverage: rollups by language and by directory (worst-covered first), plus a bounded per-file list (`--files`, or any filter, includes it; capped at `--top`, default 200) showing each file's `resolver`, `resolved_at`, and whether it's gone `stale` since the last index. Complements, does not replace, the per-query `call_graph` enum. |
| `codemap read-order [query] [--top N]` | **Where to start reading** — ranks entrypoints (`main()`, `cmd/` packages, module index files, exported public API) and load-bearing hubs (call-graph in-degree) into a newcomer's reading guide, each with the reason it ranked. Optional `query` narrows by name/path. The agent-facing answer to "I just landed in this repo — what do I read first?" |
| `codemap map [--top-subsystems N] [--top-bridges N] [--top-hubs N] [--top-entrypoints N]` | **Architecture overview** — deterministic source-path subsystems, directed cross-subsystem bridges with relationship provenance, likely entrypoints, and hubs. JSON includes `schema_version`, totals/truncation, `call_graph`, `resolution`, `stale`, and `partial_errors`. |
| `codemap traverse --at <file>:<line> [--direction outgoing\|incoming\|both] [--edge-types calls,references,...] [--depth N] [--limit N]` | **Exact heterogeneous walk** — starts from the one indexed definition enclosing `--at` (no ambiguous positional name), resolves it to the durable `{file,start_line,fqn,kind}` identity, then walks selected relationship domains cycle-safely. `--edge-types` is CSV from `calls`, `references`, `imports`, `implements`, `overrides`, `depends_on`, `tests`, and `defines`; depth is 1–10 (default 2), node limit is 1–500 (default 100). JSON v1 includes every hop's parent selector, direction, edge type/provenance, and confirmed/candidate confidence. MCP counterpart `codemap_traverse` is full-profile only and requires the typed durable `selector`. |

## Semantic

Three ways to search when you don't already have an exact symbol name: by meaning, by
name fragment, or by literal text.

| Command | Description |
|---|---|
| `codemap semantic <query> [--top N] [--backend fallback\|local\|vecgrep] [--fusion auto\|balanced]` | Meaning-based search across the indexed graph (alias: `codemap search`); the backend flag explicitly selects the semantic owner |
| `codemap explore <query> [--seeds N] [--edges N] [--depth N]` | **Intent to structure** — finds semantic/name seeds, joins each usable hit to an exact durable selector, then returns bounded caller/callee/reference/test neighborhoods without source bodies. Seeds are 1–10 (default 5), edges per neighborhood are 1–20 (default 5), and depth is 1–10 (default 2). MCP counterpart `codemap_explore` is full-profile only. |
| `codemap find <query> [--top N]` | Find symbols by name, with signatures (offline; no embeddings needed) |
| `codemap grep <pattern> [--regex] [-i] [--top N]` | Exact text search over indexed file content, each hit resolved to its enclosing symbol (offline, no embeddings) |

The default backend is `fallback`: codemap reads its local vectors when present,
then asks vecgrep only for a structure-only project. Use `--backend local` to
forbid that adapter, or `--backend vecgrep` to make vecgrep the sole semantic
owner. Explicit vecgrep mode treats a missing binary, execution failure, or bad
JSON as an error and preserves a genuine zero-hit answer; it never silently
switches back to local vectors. Indexing with that backend keeps the graph but
skips/removes codemap's unused local vectors.

On a structure-only project where neither permitted owner has embeddings,
`codemap semantic` returns no hits with a short note and JSON `"mode": "none"`.
It never invokes the embedder or creates an empty vector store on that query
path; `codemap find` remains the offline name-search floor.

**`codemap find` in degraded (no-Ollama) mode**: it's the offline search floor, not a semantic
replacement — it tokenizes the query on whitespace and camelCase boundaries (query side only) and
matches each token against symbol/FQN or docstring, so a multi-word query like `parse selector` finds
`ParseSelector` and `parse_selector` alike, and a docstring-only mention still surfaces (ranked below a
name match). Each hit carries `matched_in` (`"symbol"`, `"fqn"`, or `"docstring"`) so you can see why it
matched. It's still substring/keyword matching, not meaning search: it won't find a conceptually related
symbol that shares no words with the query — for that, embed the index (`codemap index`, with Ollama
running) and use `codemap semantic`.

`codemap grep` searches only the **indexed file set** — the files codemap extracted structure from
(same excludes as `codemap index`) — not every byte in the repo; a config/YAML/README file with no
registered extractor is invisible to it, exactly as it already is to `codemap find`/`codemap symbols`.
Reads happen live from disk at query time, so a file edited since the last index is searched at its
current content; a file *added* since the last index is not yet in that set (the `stale` field on the
report discloses this — it describes file-set completeness, not staleness of a matched line). Matching
is a literal substring by default; pass `--regex` for Go RE2 syntax and `-i`/`--ignore-case` for
case-insensitive matching in either mode. This is deliberately not a ripgrep reimplementation: no
context lines, no glob/language filters — use `codemap semantic` for meaning search and `codemap find`
for name search instead.

`codemap semantic` adaptively balances the vector/BM25 hybrid-search fusion by the shape of your query:
an identifier-looking query (`ParseSelector`, `graph.Store.NodeAtLine`) leans toward the exact-name/BM25
channel, while a natural-language question (`where do we retry on rate limit`) leans toward the vector
channel — both channels always contribute, just unevenly. The chosen profile is surfaced as `fusion`
(`"identifier"`, `"natural_language"`, or `"balanced"`) in `--json` output and printed as a `fusion: …`
line above the hit list otherwise. Pass `--fusion balanced` (or set `semantic.fusion: balanced` in
config) for the exact pre-adaptive equal-weighted behavior.

## Surfaces

The three ways to run codemap beyond one-off CLI queries: as an MCP server, as an
interactive TUI, or just check what's installed.

| Command | Description |
|---|---|
| `codemap serve` | Run the [MCP server](/mcp) over stdio. `--profile agent\|core\|full` selects the [tool profile](/mcp#tool-profiles): `agent` is exactly 25 taught workflow tools plus `codemap_docs` (26 total), `core` preserves the compatible 26-tool surface, and default `full` exposes all 44. Same file < env (`CODEMAP_MCP_PROFILE`) < flag precedence as every other setting. |
| `codemap version` | Print version information |

## Agent harness setup

One command wires codemap into an AI coding harness — no hand-editing MCP config files. See [codemap for agents](/agents#one-command-setup) for the harness table and the Claude Code plugin.

| Command | Description |
|---|---|
| `codemap agent list` | List known harnesses (Claude Code, Cursor, Codex, Gemini, Cline/Roo, Zed, VS Code, OpenCode, aider, and the `agents-md` fallback), whether each is detected here, and if codemap is already registered (`--json`) |
| `codemap agent setup <harness>` | Merge the codemap MCP server into the harness's native config and drop the canonical playbook into its guidance file (`.cursor/rules/*.mdc`, `AGENTS.md`, `GEMINI.md`, …). Never clobbers other servers or your prose. The `agents-md` fallback is deliberately playbook-only: it updates the marked block in `AGENTS.md` for any AGENTS.md-reading harness without registering MCP. Flags: `--global` (user-level config where the harness has one), `--dry-run` (print planned writes, change nothing), `--no-playbook` (MCP registration only) |
| `codemap agent playbook [--format markdown\|markdown-cli\|claude-skill\|cursor-rule]` | Print the canonical "when to use codemap" playbook, for wiring an unlisted harness by hand |

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
| `codemap cache export <file.tar.gz>` | Package the current index into a self-contained, portable tarball — no fcheap/shared store needed, so a CI job can hand it to the next runner |
| `codemap cache import <file.tar.gz> [--force]` | Restore a tarball from `cache export` (registers the project first if unindexed). Refuses a schema/embedding-profile mismatch outright; refuses a working-tree hash mismatch unless `--force` |

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
| `codemap daemon start [path]` | Run the daemon in the foreground (watches the project; background it with `&`, stop with Ctrl-C). Flags: `--precise` (rerun exact Go/LSP resolution after watched edits), `--no-embed` (structure only, no Ollama), `--debounce`, `--idle-timeout`, `--embed-rps`, `--embed-max-in-flight`, `--embed-cache-size` |
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

Exactness is persistent daemon state, not a one-shot label. Start with
`--precise` (or `daemon.precise: true` / `CODEMAP_DAEMON_PRECISE=true`) to run
the exact Go `go/types` and LSP `callHierarchy` pass on the initial index and
every changed batch. If a package stops type-checking or an LSP request fails,
the affected files lose precise coverage instead of retaining ghost confidence.

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
`--staged` reviews just what's staged. JSON/MCP output also includes copy/paste-ready
`test_commands` grouped by package and at most two conditional `next` actions, so an
agent can execute the selected regressions without deriving runner syntax. The
`codemap_review` MCP tool returns the same JSON. Every successful review document emits
`schema_version: 1`; the authoritative Draft 2020-12 contract is
`schemas/codemap.review.v1.schema.json`. Canonical keys are snake_case:
`{schema_version, changed_symbols, analysis_complete, total_symbols, analyzed_symbols,
truncated_symbols, partial_errors, blast_radius, covering_tests, test_commands,
untested_symbols, hotspots, stale, resolution, call_graph, risk, next}`. Version 1 permits
additive optional properties but does not rename or repurpose existing fields. The command
degrades gracefully (a plain changed-file list with a note) when the project isn't indexed or
isn't a git repo; hard-failure error envelopes are separate from the success schema.

`analysis_complete` is false when the index is stale, a supporting stage fails, an individual
symbol cannot be analyzed, deleted definitions are unavailable, or the 200-symbol work cap omits part of the
diff. Pure-deletion hunks are classified before they can make a review incomplete: a deleted block that fell
inside a surviving symbol's span maps to that symbol (it is analyzed), deleted lines that belong to no symbol
at all (blank lines, comments, imports — in the gaps between definitions or at the top/end of the file) are
informational, and only a removed declaration or an unclassifiable file is an error. Fresh indexed untracked source files and exact source-file renames are mapped as whole files
because they have no post-image hunks; an empty renamed source file remains partial. Documentation,
assets, and configuration files stay visible in `changed_files` but do not create structural mapping errors. Compare `total_symbols`,
`analyzed_symbols`, and `truncated_symbols`; bounded
`partial_errors` entries carry stable `stage`/`code` fields plus an actionable message. An
incomplete review's aggregate `risk.level` is always `unknown`, even when the successfully
analyzed subset produced a numeric score. Mapping-stage codes distinguish a failed symbol lookup
(`symbol_mapping_failed`), a structural-source pure-deletion hunk whose file has no indexed symbols to
classify it against (`deletion_only_hunk` when the file has no mapped hunks, `deletion_hunk_unmapped`
otherwise), a recognized
callable or type declaration line removed from structural source by any hunk shape
(`removed_definition_unavailable`), and an exact structural-source rename whose new path has no
indexed symbols (`rename_unmapped`). These signals prevent a
successfully mapped post-image subset from hiding old definitions that review could not analyze.

**`call_graph`** (stable machine enum) on `impact`/`callers`/`callees`/`references`/`review`/`context`/
`hotspots`/`orphans`/`path`/`map`/`traverse`
tells a consumer how much to trust the call graph without parsing prose: `resolved`
(every matched definition file has precise coverage), `name` (Go/Ruby/Lua name-based — same-named symbols may over-match), `unresolved`
(TS/JS/Python without successful precise coverage, or Vue whose call graph is not supported yet — callers/blast/tests are incomplete, not absent; TS/JS may still return name-based JSX candidates),
`none` (no matching symbol). The free-form `resolution` sentence stays for humans.

**`risk`** on `review` is one band for the whole diff — `level` (unknown/low/medium/high),
`score` (0..1), and `factors` (`untested_changes`/`hotspot_fanin`/`cross_package`/
`ambiguity`/`unresolved`) folded from every changed symbol — so a harness can gate
verification on one call instead of fanning `risk` out per symbol. It is absent for a complete
zero-symbol diff and early no-repository/no-index degradation; a finalized incomplete indexed
review emits `unknown` even when no symbol could be mapped safely. `unknown` also covers a changed
symbol whose call graph is unavailable.

### Gating a commit or script

`codemap review` and `codemap risk` compute a risk level and an untested-symbols
list either way, but historically always exited `0` — a caller wanting to block
on "this diff touches untested high-risk code" had to hand-roll the check
against `--json` output (exactly what the [GitHub Action](/ci) did before it
grew `fail-on-untested`/`fail-on-risk` inputs). Two flags turn that into a
first-class exit code instead:

- `--fail-on-risk <low|medium|high>` — after printing the normal output
  (unchanged), exit **6** if the risk level's ordinal is at or above the
  threshold (`low` ≤ `medium` ≤ `high`). On a complete report,
  `level:"unknown"` does not trip this risk comparison: an unresolved call
  graph is not evidence of risk, so a repo indexed without `--precise` cannot
  spuriously fail a risk threshold. Available on both `review`
  (gates the aggregate diff-wide `risk` band) and `risk` (gates one symbol).
- `--fail-on-untested` — after printing the normal output (unchanged), exit
  **6** if `untested_symbols` is non-empty, or if mapped symbols have an
  `unresolved`/`none` call graph and test coverage therefore cannot be established.
  An empty list is not proof of coverage when relationships are unknown. `review`
  only (there is no untested-*symbols* list on `risk`, which reports one symbol at a time).

For `review`, enabling **either** gate also requires a complete analysis. An
indexed Git repository with `analysis_complete:false` exits **6** before policy
comparison, even though its aggregate risk is honestly `unknown`; otherwise a
stale or partially mapped diff could pass because the evidence needed to enforce
the gate is missing. With both flags disabled, the same incomplete report remains
reporting-only and exits `0`. Early graceful reports for a non-Git directory or a
project with no indexed nodes (including `codemap init` without `codemap index`)
also remain nonblocking and exit `0`.

The gate is **exit-code-only**: under `--json` the body printed is the exact
same success envelope you'd get without the flag (`ok`/`error`/`code` never
appear — that shape is reserved for real failures, see below). A tripped gate
is not a query failure; nothing was left unanswered. This means a script can
run the same command it already runs for output and just also check `$?`.

A ready-made [pre-commit](https://pre-commit.com) hook packages the common
case — see [`.pre-commit-hooks.yaml`](https://github.com/abdul-hamid-achik/codemap/blob/main/.pre-commit-hooks.yaml)
and the [pre-commit section of the CI guide](/ci#pre-commit) for the trade-offs
(name-based resolution for speed; a non-Git or never-indexed project degrades to
exit `0`, while an existing but stale/partial index fails closed until refreshed).

### Machine-readable errors + exit codes

Under `--json`, a failure prints a structured envelope to **stdout** (not stderr) so an
agent parsing JSON still gets it:

```json
{"ok": false, "error": "…", "code": "not_found|not_indexed|index_missing|index_corrupt|not_a_repo|operational", "hint": "run: codemap index"}
```

The non-zero exit codes follow a documented taxonomy:

| exit | meaning |
---|---|
| 0 | answered (results, possibly empty-but-resolved like "no callers") |
| 1 | operational error (bad flag, git failure, untyped runtime error) |
| 2 | `not_found` / `not_indexed` — a valid query with no answer |
| 3 | `index_missing` — no index for the project |
| 4 | `index_corrupt` — the graph DB exists but won't open |
| 5 | `not_a_repo` — a git operation was required but cwd isn't a repo |
| 6 | `gate_failed` — a `--fail-on-risk`/`--fail-on-untested` threshold tripped on `review`/`risk`. **Not** a query failure: there is no `--json` failure envelope for it (see [Gating a commit or script](#gating-a-commit-or-script)) |

So a consumer can map `code`→action deterministically either way (the JSON `code`
matches the exit-code suffix) for exit codes 0-5; exit 6 is exit-code-only by design.

### Durable impact batches

For hits returned by Vecgrep's structural chunks, pass each `selector` unchanged:

```sh
codemap impact --selector '{"file":"main.go","start_line":12,"fqn":"main.Run","kind":"function"}' --json
```

Repeat `--selector` for up to 25 definitions. It always returns the ordered batch
shape, including for one selector. It cannot be combined with positional names
or `--at`. Each JSON selector is bounded to 16 KiB and rejects unknown fields.
File, FQN and kind retain identity across ordinary line shifts after reindexing.

Impact batches (`--selector`, repeated `--at`, or `--at --batch`) include
`freshness.checked` and `freshness.stale`. A false `checked` is unknown freshness,
not a fresh index. This is a working-tree observation, not a transaction spanning
concurrent edits. Check each item's `found` and `call_graph` independently.
