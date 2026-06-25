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

Every config-file setting is reachable all three ways — config file, env var, and flag — with the
flag winning when explicitly set.

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
| `CODEMAP_EMBEDDING_DIMENSIONS` | `embedding.dimensions` |
| `CODEMAP_EMBEDDING_DISTANCE` | `embedding.distance` (e.g. `cosine`) |
| `CODEMAP_EXCLUDE_EXTRA` | `index.exclude_extra` (comma-separated; appended) |
| `CODEMAP_EMBED_BATCH_SIZE` | `index.embed_batch_size` |
| `CODEMAP_EMBED_CONCURRENCY` | `index.embed_concurrency` |
| `CODEMAP_EMBED_MAX_CHARS` | `index.embed_max_chars` |
| `CODEMAP_DAEMON_DEBOUNCE_MS` | `daemon.debounce_ms` |
| `CODEMAP_DAEMON_IDLE_TIMEOUT_MIN` | `daemon.idle_timeout_min` |
| `CODEMAP_DAEMON_EMBED_RPS` | `daemon.embed_rps` |
| `CODEMAP_DAEMON_EMBED_MAX_IN_FLIGHT` | `daemon.embed_max_in_flight` |

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
  embed_max_chars: 0      # cap per-node embed text (0 = no cap); lower = faster, less body recall
daemon:                   # background indexer (codemap daemon)
  debounce_ms: 500        # coalesce a burst of edits into one reindex
  idle_timeout_min: 0     # shut down after N minutes idle (0 = never)
  embed_rps: 0            # background embed rate to Ollama (0 = unlimited)
  embed_max_in_flight: 2  # max concurrent embed calls
  embed_cache_size: 4096  # embedding dedup cache (entries)
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

## Embedding profile guard

The embedding provider/model/dimension is stored with the vector collection. If it changes,
codemap fails the next index with a clear "reindex" message rather than silently corrupting
the vector space — run `codemap index --reindex` to rebuild.
