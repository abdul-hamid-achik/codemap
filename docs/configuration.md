---
description: Configure codemap paths, indexing, embeddings, semantic backends, MCP profiles, and daemon behavior.
---

# Configuration

codemap uses XDG-style paths with `CODEMAP_*` environment overrides and an ecosystem
fallback.

```
$XDG_CONFIG_HOME/codemap/config.yaml     # config        (~/.config/codemap/…)
$XDG_DATA_HOME/codemap/                   # graph DB, veclite store, project registry
$XDG_CACHE_HOME/codemap/                  # caches
```

If `~/.codemap/` already exists it is used (back-compat with the vecgrep/noted ecosystem).
`codemap init --local` drops a `.codemap` marker so a repo-local `codemap.yaml` is picked up from
any subdirectory (the project-file step below); the index itself stays central — set `CODEMAP_DATA`
to a path inside the repo if you want a repo-local index too.

## Precedence (highest → lowest)

1. CLI flags (per-setting override flags — see [Command-line flags](#command-line-flags) below)
2. Environment variables (`CODEMAP_*` — see [Environment variables](#environment-variables) below)
3. Project-root `codemap.yaml` / `codemap.yml`
4. Project `.config/codemap.yaml`
5. Global `$XDG_CONFIG_HOME/codemap/config.yaml`
6. `~/.codemap/config.yaml` (legacy, if present)
7. Built-in defaults

Most config-file settings are reachable all three ways — config file, env var, and flag —
with the flag winning when explicitly set. Four knobs are exceptions: `daemon.embed_cache_size`
is file + flag only (no env var), `index.extract_concurrency` is file + env only (no flag),
`semantic.fusion_weights.*` (the per-profile weight floats) is file-only (no env var, no flag) —
`semantic.backend` and `semantic.fusion` are reachable all three ways — and
`embedding.api_key` is file + env only (**no flag, deliberately**): flag values show up in
`ps`/shell history, which a secret should never do. Use the config file or
`CODEMAP_OLLAMA_API_KEY` instead (see [Authenticated Ollama endpoints / Ollama Cloud](#authenticated-ollama-endpoints-ollama-cloud)).

## Environment variables

Each overrides the corresponding config-file value (and takes precedence over it):

| Variable | Overrides |
|---|---|
| `CODEMAP_CONFIG` | path to a specific config file |
| `CODEMAP_CONFIG_DIR` | the config directory |
| `CODEMAP_DATA` | the data directory (graph DB, veclite store, project registry) |
| `CODEMAP_CACHE` | the cache directory |
| `CODEMAP_EMBEDDING_PROVIDER` | `embedding.provider` (e.g. `ollama`) |
| `CODEMAP_EMBEDDING_MODEL` | `embedding.model` (e.g. `nomic-embed-text`) |
| `CODEMAP_OLLAMA_URL` | `embedding.ollama_url` |
| `CODEMAP_OLLAMA_API_KEY` | `embedding.api_key` (bearer token for Ollama Cloud or an authenticated Ollama-compatible endpoint) |
| `CODEMAP_EMBEDDING_DIMENSIONS` | `embedding.dimensions` |
| `CODEMAP_EMBEDDING_DISTANCE` | `embedding.distance` (e.g. `cosine`) |
| `CODEMAP_EXCLUDE_EXTRA` | `index.exclude_extra` (comma-separated; appended) |
| `CODEMAP_EMBED_BATCH_SIZE` | `index.embed_batch_size` |
| `CODEMAP_EMBED_CONCURRENCY` | `index.embed_concurrency` |
| `CODEMAP_EXTRACT_CONCURRENCY` | `index.extract_concurrency` (parallel Go extraction workers; no flag) |
| `CODEMAP_EMBED_MAX_CHARS` | `index.embed_max_chars` |
| `CODEMAP_VECGREP_ENABLED` | `vecgrep.enabled` |
| `CODEMAP_VECGREP_BIN` | `vecgrep.bin` |
| `CODEMAP_DAEMON_DEBOUNCE_MS` | `daemon.debounce_ms` |
| `CODEMAP_DAEMON_IDLE_TIMEOUT_MIN` | `daemon.idle_timeout_min` |
| `CODEMAP_DAEMON_PRECISE` | `daemon.precise` (keep exact call edges current after watched edits) |
| `CODEMAP_DAEMON_EMBED_RPS` | `daemon.embed_rps` |
| `CODEMAP_DAEMON_EMBED_MAX_IN_FLIGHT` | `daemon.embed_max_in_flight` |
| `CODEMAP_SEMANTIC_BACKEND` | `semantic.backend` (`fallback`, `local`, or `vecgrep`) |
| `CODEMAP_SEMANTIC_FUSION` | `semantic.fusion` (`auto` or `balanced`) |
| `CODEMAP_MCP_PROFILE` | `mcp.profile` (`agent`, `core`, or `full` — see [MCP tool profiles](/mcp#tool-profiles)) |

Typed environment values are validated when configuration loads. Invalid integers, numbers, or
booleans stop the command with an error that names the variable; codemap does not silently fall back
to a lower-precedence value. The rejected value is not echoed in the error message.

## Command-line flags

Each config knob also has a flag, which wins over the file and env when set:

| Flag | Setting | Command(s) |
|---|---|---|
| `--embed-provider` / `--embed-model` / `--ollama-url` / `--embed-dimensions` / `--embed-distance` | `embedding.*` | all (persistent) |
| `--exclude` | `index.exclude` (replaces defaults) | `index` |
| `--exclude-extra` | `index.exclude_extra` (appended) | `index`, `daemon start` |
| `--max-file-bytes` | `index.max_file_bytes` | `index` |
| `--embed-batch-size` / `--embed-concurrency` / `--embed-max-chars` | `index.embed_*` | `index` |
| `--debounce` / `--idle-timeout` / `--precise` | `daemon.debounce_ms` / `daemon.idle_timeout_min` / `daemon.precise` | `daemon start` |
| `--embed-rps` / `--embed-max-in-flight` / `--embed-cache-size` | `daemon.embed_*` | `daemon start` |
| `--backend` | `semantic.backend` | `semantic`, `search` |
| `--fusion` | `semantic.fusion` | `semantic`, `search` |
| `--profile` | `mcp.profile` (`agent`, `core`, or `full`) | `serve` |

```bash
codemap index --exclude-extra migrations,db/migrations,**/testdata
codemap daemon start --debounce 800ms --embed-rps 2
```

## config.yaml

Config files are decoded strictly. A misspelled or unknown key is an error instead of an ignored
setting, and filesystem/read/parse errors are surfaced. Missing global and project config files remain
optional; a path supplied through `--config` or `CODEMAP_CONFIG` must exist and be readable.

Numeric settings must be non-negative, and floating-point settings must also be finite. Zero keeps its
documented special meaning: no limit/cap for file size, embed text, or rate; no idle shutdown; the
built-in default for batch, concurrency, debounce, in-flight, cache, and individual fusion weights.

```yaml
embedding:
  provider: ollama
  model: nomic-embed-text
  ollama_url: http://localhost:11434
  api_key: ""       # bearer token; empty = today's unauthenticated local Ollama (default)
  dimensions: 768
  distance: cosine
index:
  max_file_bytes: 1048576 # 0 = no size limit
  exclude:                # REPLACES the built-in defaults — set only to override wholesale
    - .git
    - node_modules     # JS/TS deps (any depth)
    - venv/            # Python virtualenvs — root-anchored (also env/, dist/, build/, target/, coverage/)
    - __pycache__      # any depth
    - vendor           # Go deps (any depth)
    - dist/            # root-anchored
    - "*.min.js"
  exclude_extra:          # APPENDED to the defaults — add your own without restating them
    - migrations
    - db/migrations
    - "**/testdata"
  embed_batch_size: 64    # node texts per request (0 = default 64)
  embed_concurrency: 4    # concurrent requests (0 = default 4)
  extract_concurrency: 4 # parallel Go extraction workers (default 4; 1 = sequential)
  embed_max_chars: 0      # cap per-node embed text (0 = no cap); lower = faster, less body recall
daemon:                   # background indexer (codemap daemon)
  debounce_ms: 500        # coalesce edit bursts (0 = default 500)
  idle_timeout_min: 0     # shut down after N minutes idle (0 = never)
  precise: false          # opt in to exact Go/LSP edges on every watched edit
  embed_rps: 0            # background embed rate to Ollama (0 = unlimited)
  embed_max_in_flight: 2  # max concurrent embed calls (0 = default 2)
  embed_cache_size: 4096  # dedup cache entries (0 = default 4096)
vecgrep:                  # sibling-tool integration (see Ecosystem)
  enabled: true           # allow the one-hop vecgrep search/memory adapter
  bin: ""                 # path to the vecgrep binary (resolved via $PATH if empty)
semantic:
  backend: fallback       # fallback (local, then vecgrep if absent) | local | vecgrep
  fusion: auto            # auto (classify query shape) | balanced (equal weights, pre-F7 behavior)
  fusion_weights:          # file-only (no env/flag) — advanced tuning
    identifier:
      vector: 0.5
      text: 1.5
    natural_language:
      vector: 1.5
      text: 0.5
mcp:
  profile: full            # full (default, 43) | agent (25 taught + docs = 26) | core (compatible 26)
```

### Semantic owner

`semantic.backend` makes retrieval ownership explicit while keeping migration
back-compatible:

- `fallback` (default) reads codemap's local veclite collection and asks vecgrep
  only when this project has no local embeddings.
- `local` never invokes vecgrep and preserves codemap's original embedded-index
  behavior.
- `vecgrep` delegates every semantic query to the sibling CLI. In this mode
  `codemap index` skips and removes unused local vectors while continuing to
  index the structural graph; a missing vecgrep binary or invalid response is a
  visible error, not a silent owner switch.

The adapter is one process hop (`vecgrep search ... --format json`), not shared
packages, shared databases, or MCP-to-MCP recursion. `find` and `grep` remain
offline structural/text fallbacks in every mode.

The full built-in default list is:

- **Any-depth** (bare, matches at every path level): `.git`, `node_modules`, `vendor`, `__pycache__`,
  `site-packages`, `dist-*`, `build-*` (build-output variants like `dist-chrome`/`build-web`), `.next`,
  `.nuxt`, `*.min.js`, `*.gen.go`, `*_gen.go`, `*.pb.go`, `*_pb.go`, `*.lock`. None of these are
  plausible source-directory names in Go/TS/JS/Python, so matching them anywhere is safe — and it's
  required to also catch nested cases like a workspace's per-package `node_modules` or a virtualenv's
  deeply-nested `site-packages`.
- **Root-anchored** (trailing slash, matches only at the project root): `dist/`, `build/`, `target/`,
  `coverage/`, `venv/`, `env/`. These names collide with real source packages often enough to be a
  footgun at any depth — Go's standard library ships `go/build`, a Go project commonly has
  `internal/env` or `internal/coverage`, and `dist`/`target` are plausible package names too. Root
  anchoring keeps the common build-output/venv case (at the project root) excluded while leaving a
  same-named source subpackage alone. The trade-off: a monorepo with a nested per-package build
  output (e.g. `packages/foo/dist/`) needs its own `exclude_extra: ["dist"]` (bare, any-depth) if it
  wants that excluded too — that's opt-in bloat-avoidance, not a silently-dropped-code footgun.

Any dot-prefixed directory (`.git`, `.venv`, `.tox`, …) is also skipped automatically by the walker,
independent of the exclude list.

### exclude vs exclude_extra

`exclude` **replaces** the defaults (include the ones you still want); `exclude_extra` is **appended**
to whatever `exclude` resolves to — use it to skip your own folders (migrations, fixtures, generated code)
without losing `node_modules`/`vendor`/`.git`.

Both use the same **path-aware** glob semantics:

- **No slash anywhere** (`migrations`, `*.min.js`) — matches that file/dir name at **any depth**.
- **A slash anywhere — leading, trailing, or embedded** (`db/migrations`, `env/`, `/dist`) —
  **anchored at the project root**. `db/migrations` matches `db/migrations` and everything under it,
  but not `app/db/migrations`. A *lone trailing slash* like `env/` anchors that single segment the
  same way — it matches a root-level `env/` (and everything under it) but leaves a nested
  `internal/env/` alone. This is the important gotcha the trailing slash exists to signal: writing
  `env` (no slash) would match `internal/env` too, silently dropping real code — always use a
  trailing slash to root-anchor a single directory name.
- **`**/` prefix** (`**/testdata`, `**/gen/protobuf`) — un-anchors a slash pattern so it matches at
  **any depth**, including multi-segment patterns.
- A leading `./` is stripped and treated as an explicit root marker, equivalent to a trailing slash
  (`./env` behaves like `env/`). A pattern that normalizes to nothing (`/`, `./`, `**/`) is a no-op.

## Indexing performance

Indexing structure (the graph) is fast — the time in a full index is almost entirely **embedding**
(turning each symbol into a vector). If indexing feels slow, in order of impact:

1. **Don't `--reindex` for routine updates.** Plain `codemap index` is incremental: it content-hashes
   every file and **skips unchanged ones**, re-embedding only what changed. On a typical repo a no-op
   `codemap index` is well under a second, while `--reindex` re-embeds *everything*. Reserve `--reindex`
   for changing the embedding model or recovering a corrupt index.
2. **`--no-embed`** indexes structure only (no Ollama) — near-instant, and `callers`/`impact`/`hotspots`
   still work; you only lose semantic search until a later embed.
3. **Embedder throughput.** With a local Ollama, embedding is GPU-bound, so:
   - `--embed-max-chars N` (e.g. `512`) caps the text per symbol — embedding cost is ~linear in tokens,
     so this is a near-linear speedup, trading some long-function-body recall (the docstring + signature
     are always kept first).
   - Raise Ollama's own parallelism: `OLLAMA_NUM_PARALLEL=8 ollama serve`, then `--embed-concurrency`
     can overlap requests. A smaller model (e.g. `all-minilm`) embeds several times faster at some
     quality cost.
   - With a **remote Ollama endpoint** (for example, an authenticated team GPU box), per-request latency
     can dominate, so `--embed-batch-size` and `--embed-concurrency` matter more. Codemap batches and
     parallelizes Ollama requests by default; other embedding-provider adapters are not implemented yet.

If the embedder is unreachable mid-index, the **structural index still succeeds** — codemap reports
`embeddings skipped: …` and you can re-run later to add the vectors.

## Authenticated Ollama endpoints / Ollama Cloud

`embedding.ollama_url` doesn't have to be `localhost`. Point it at any Ollama-compatible HTTP
endpoint — a teammate's shared server behind a reverse proxy, or Ollama Cloud — and set
`embedding.api_key` (or `CODEMAP_OLLAMA_API_KEY`) so codemap sends
`Authorization: Bearer <key>` on every embed request. The wire format (`POST /api/embed`,
`{model, input}` in, `{embeddings: [...]}` out) is unchanged — same code path as local Ollama,
just with one extra header.

```yaml
embedding:
  ollama_url: https://ollama.com
  api_key: ""   # set via CODEMAP_OLLAMA_API_KEY instead — see below
  model: nomic-embed-text
```

```bash
export CODEMAP_OLLAMA_API_KEY="$(cat ~/.config/secrets/ollama-key)"  # never paste it inline
codemap index
```

Notes, verified against Ollama's docs as of this writing:

- **Host and auth**: Ollama Cloud is served at `https://ollama.com` (not a subdomain), using
  the same `/api/*` surface as a local server. Create a key at `ollama.com/settings/keys` and
  send it as `Authorization: Bearer <key>` — this is exactly what `embedding.api_key` does.
- **No cloud embedding model today**: as of this writing, Ollama Cloud's catalog has **no**
  embedding model (`ollama.com/search?c=cloud&c=embedding` returns none) — cloud models there
  are chat/generation models (`gpt-oss:120b-cloud` and similar). `nomic-embed-text` remains a
  **local** pull. This feature is therefore chiefly useful today for an **authenticated
  self-hosted/team Ollama** (e.g. a shared GPU box behind a reverse proxy that requires a
  bearer token) — and it costs nothing to support Ollama Cloud too, so a future embedding
  model on their cloud tier will work without a codemap change.
- **Never put the key on the command line.** There is intentionally no `--ollama-api-key`
  flag — command-line arguments are visible to other processes on the same machine (`ps`).
  Use the config file or `CODEMAP_OLLAMA_API_KEY`.
- **Secrets hygiene**: `codemap config show` and `codemap doctor` never print the key —
  `config show` masks it (`****`+last 4 characters, or `(set)` for short values; empty stays
  empty), and `doctor` reports only whether `embedding auth` is `configured` or not.

## Embedding profile guard

The embedding provider, model, dimensions, and distance metric are stored with the vector collection.
If any of them changes,
codemap fails the next index with a clear "reindex" message rather than silently corrupting
the vector space — run `codemap index --reindex` to rebuild.

## Privacy and network access

The codemap binary sends no product-usage telemetry. Its SQLite graph and local veclite collection
stay on disk. Language-server traffic stays between codemap and local subprocesses over stdio.
Embedding requests send symbol source text to `embedding.ollama_url`; that is localhost by default,
but the text leaves your machine if you explicitly configure a remote endpoint. An explicit vecgrep
semantic backend follows vecgrep's own configuration and process boundary.

This documentation website is separate from the CLI and uses
[cookie-free Vercel Web Analytics](https://vercel.com/docs/analytics/privacy-policy). It does not
change the binary's telemetry behavior. Maintainers must also enable Web Analytics for the Vercel
project in the Vercel dashboard; installing the client package alone does not activate collection.
