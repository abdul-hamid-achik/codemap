# Configuration

codemap uses XDG-style paths with `CODEMAP_*` environment overrides and an ecosystem
fallback.

```
$XDG_CONFIG_HOME/codemap/config.yaml     # config        (~/.config/codemap/…)
$XDG_DATA_HOME/codemap/                   # graph DB, veclite store, project registry
$XDG_CACHE_HOME/codemap/                  # caches
```

If `~/.codemap/` already exists it is used (back-compat with the vecgrep/noted ecosystem).
Use `codemap init --local` to keep repo-local state.

## Precedence (highest → lowest)

1. Environment variables (`CODEMAP_CONFIG`, `CODEMAP_DATA`, `CODEMAP_EMBEDDING_MODEL`,
   `CODEMAP_OLLAMA_URL`, `CODEMAP_EMBEDDING_DIMENSIONS`, …)
2. Project-root `codemap.yaml` / `codemap.yml`
3. Project `.config/codemap.yaml`
4. Global `$XDG_CONFIG_HOME/codemap/config.yaml`
5. `~/.codemap/config.yaml` (legacy, if present)
6. Built-in defaults

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
  exclude:
    - .git
    - node_modules     # JS/TS deps
    - venv             # Python virtualenvs (also env, site-packages)
    - __pycache__
    - vendor           # Go deps
    - dist
    - "*.min.js"
```

The default exclude list also covers `build`, `.next`, `.nuxt`, `target`, `env`, `site-packages`,
`*.gen.go`, `*.pb.go`, and `*.lock`; any dot-prefixed directory (`.git`, `.venv`, `.tox`, …) is skipped
automatically. Setting `exclude` replaces the defaults, so include the ones you still want.

## Embedding profile guard

The embedding provider/model/dimension is stored with the vector collection. If it changes,
codemap fails the next index with a clear "reindex" message rather than silently corrupting
the vector space — run `codemap index --reindex` to rebuild.
