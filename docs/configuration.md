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
`semantic.fusion` itself (the `auto`/`balanced` switch) is reachable all three ways — and
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
| `CODEMAP_DAEMON_EMBED_RPS` | `daemon.embed_rps` |
| `CODEMAP_DAEMON_EMBED_MAX_IN_FLIGHT` | `daemon.embed_max_in_flight` |
| `CODEMAP_SEMANTIC_FUSION` | `semantic.fusion` (`auto` or `balanced`) |

## Command-line flags

Each config knob also has a flag, which wins over the file and env when set:

| Flag | Setting | Command(s) |
|---|---|---|
| `--embed-provider` / `--embed-model` / `--ollama-url` / `--embed-dimensions` / `--embed-distance` | `embedding.*` | all (persistent) |
| `--exclude` | `index.exclude` (replaces defaults) | `index` |
| `--exclude-extra` | `index.exclude_extra` (appended) | `index`, `daemon start` |
| `--max-file-bytes` | `index.max_file_bytes` | `index` |
| `--embed-batch-size` / `--embed-concurrency` / `--embed-max-chars` | `index.embed_*` | `index` |
| `--debounce` / `--idle-timeout` | `daemon.debounce_ms` / `daemon.idle_timeout_min` | `daemon start` |
| `--embed-rps` / `--embed-max-in-flight` / `--embed-cache-size` | `daemon.embed_*` | `daemon start` |
| `--fusion` | `semantic.fusion` | `semantic`, `search` |

```bash
codemap index --exclude-extra migrations,db/migrations,**/testdata
codemap daemon start --debounce 800ms --embed-rps 2
```

## config.yaml

```yaml
embedding:
  provider: ollama
  model: nomic-embed-text
  ollama_url: http://localhost:11434
  api_key: ""       # bearer token; empty = today's unauthenticated local Ollama (default)
  dimensions: 768
  distance: cosine
index:
  max_file_bytes: 1048576
  exclude:                # REPLACES the built-in defaults — set only to override wholesale
    - .git
    - node_modules     # JS/TS deps
    - venv             # Python virtualenvs (also env, site-packages)
    - __pycache__
    - vendor           # Go deps
    - dist
    - "*.min.js"
  exclude_extra:          # APPENDED to the defaults — add your own without restating them
    - migrations
    - db/migrations
    - "**/testdata"
  embed_batch_size: 64    # node texts per embedder request
  embed_concurrency: 4    # concurrent embedder requests (big win for network providers)
  extract_concurrency: 4 # parallel Go extraction workers (default 4; 1 = sequential)
  embed_max_chars: 0      # cap per-node embed text (0 = no cap); lower = faster, less body recall
daemon:                   # background indexer (codemap daemon)
  debounce_ms: 500        # coalesce a burst of edits into one reindex
  idle_timeout_min: 0     # shut down after N minutes idle (0 = never)
  embed_rps: 0            # background embed rate to Ollama (0 = unlimited)
  embed_max_in_flight: 2  # max concurrent embed calls
  embed_cache_size: 4096  # embedding dedup cache (entries)
vecgrep:                  # sibling-tool integration (see Ecosystem)
  enabled: true           # use vecgrep for semantic search when codemap has no embeddings, + memory recall
  bin: ""                 # path to the vecgrep binary (resolved via $PATH if empty)
semantic:
  fusion: auto            # auto (classify query shape) | balanced (equal weights, pre-F7 behavior)
  fusion_weights:          # file-only (no env/flag) — advanced tuning
    identifier:
      vector: 0.5
      text: 1.5
    natural_language:
      vector: 1.5
      text: 0.5
```

The default exclude list also covers `build`, build-output variants (`dist-*`, `build-*`, e.g.
`dist-chrome`/`build-web`), `coverage`, `.next`, `.nuxt`, `target`, `env`, `site-packages`, `*.gen.go`,
`*.pb.go`, `*_pb.go`, and `*.lock`; any dot-prefixed directory (`.git`, `.venv`, `.tox`, …) is skipped
automatically.

### exclude vs exclude_extra

`exclude` **replaces** the defaults (include the ones you still want); `exclude_extra` is **appended**
to whatever `exclude` resolves to — use it to skip your own folders (migrations, fixtures, generated code)
without losing `node_modules`/`vendor`/`.git`.

Both use the same **path-aware** glob semantics:

- **Bare name** (`migrations`, `*.min.js`) — matches that file/dir name at **any depth**.
- **Slash pattern** (`db/migrations`) — **anchored at the project root**; matches `db/migrations` and
  everything under it, but not `app/db/migrations`.
- **`**/` prefix** (`**/testdata`, `**/gen/protobuf`) — un-anchors a slash pattern so it matches at
  **any depth**.

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
   - With a **network** embedder (OpenAI/Cohere/Voyage), per-request latency dominates, so `--embed-batch-size`
     and `--embed-concurrency` are a large win (codemap batches and parallelizes requests by default).

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

The embedding provider/model/dimension is stored with the vector collection. If it changes,
codemap fails the next index with a clear "reindex" message rather than silently corrupting
the vector space — run `codemap index --reindex` to rebuild.
