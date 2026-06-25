package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the resolved codemap configuration.
type Config struct {
	Embedding EmbeddingConfig `yaml:"embedding"`
	Index     IndexConfig     `yaml:"index"`
	Daemon    DaemonConfig    `yaml:"daemon"`
	Vecgrep   VecgrepConfig   `yaml:"vecgrep"`
}

// VecgrepConfig controls the optional fallback to the sibling vecgrep tool for
// semantic search when codemap has no local embeddings (structure-only index).
type VecgrepConfig struct {
	Enabled bool   `yaml:"enabled"` // try vecgrep for semantic search when codemap has no vectors (default true)
	Bin     string `yaml:"bin"`     // path to the vecgrep binary (resolved via $PATH if empty)
}

// DaemonConfig tunes the background daemon (`codemap daemon`).
type DaemonConfig struct {
	DebounceMS       int     `yaml:"debounce_ms"`         // watcher debounce (default 500)
	IdleTimeoutMin   int     `yaml:"idle_timeout_min"`    // idle-shutdown after N minutes (0 = never)
	EmbedRPS         float64 `yaml:"embed_rps"`           // background embed rate to Ollama (0 = unlimited)
	EmbedMaxInFlight int     `yaml:"embed_max_in_flight"` // max concurrent embed calls (default 2)
	EmbedCacheSize   int     `yaml:"embed_cache_size"`    // dedup cache entries (default 4096)
}

// EmbeddingConfig controls how node source text is turned into vectors.
type EmbeddingConfig struct {
	Provider   string `yaml:"provider"`   // ollama (default), openai, cohere, voyage
	Model      string `yaml:"model"`      // e.g. nomic-embed-text
	OllamaURL  string `yaml:"ollama_url"` // base URL for the Ollama HTTP API
	Dimensions int    `yaml:"dimensions"` // vector dimension (nomic-embed-text = 768)
	Distance   string `yaml:"distance"`   // cosine (default), dot, euclidean
}

// IndexConfig controls what gets indexed.
type IndexConfig struct {
	MaxFileBytes int `yaml:"max_file_bytes"` // skip files larger than this
	// Exclude fully REPLACES the built-in default skip list (.git, node_modules,
	// vendor, …). Set it only to override those defaults wholesale. A pattern
	// without a slash matches any path segment ("migrations" skips a migrations
	// dir at any depth); a pattern with a slash anchors to the project root
	// ("db/migrations"), and a "**/" prefix matches at any depth ("**/testdata").
	Exclude []string `yaml:"exclude"`
	// ExcludeExtra is APPENDED to Exclude (whether default or overridden), so you
	// can skip your own folders (migrations, fixtures, generated/) without
	// restating the defaults. Same glob semantics as Exclude.
	ExcludeExtra []string `yaml:"exclude_extra"`
	// EmbedBatchSize is how many node texts are sent to the embedder per request
	// (default 64). EmbedConcurrency is how many such requests run at once (default
	// 4). Embedding dominates a reindex; batching + concurrency is the main speedup
	// (and a large one for network providers — openai/cohere/voyage).
	EmbedBatchSize   int `yaml:"embed_batch_size"`
	EmbedConcurrency int `yaml:"embed_concurrency"`
	// EmbedMaxChars caps the per-node text sent to the embedder (0 = no cap, the
	// default). Embedding cost is ~linear in tokens, so a cap (e.g. 512) trades some
	// long-body recall for a faster reindex; docstring+signature are kept first.
	EmbedMaxChars int `yaml:"embed_max_chars"`
}

// DefaultConfig returns the built-in defaults (lowest precedence).
func DefaultConfig() *Config {
	return &Config{
		Embedding: EmbeddingConfig{
			Provider:   "ollama",
			Model:      "nomic-embed-text",
			OllamaURL:  "http://localhost:11434",
			Dimensions: 768,
			Distance:   "cosine",
		},
		Index: IndexConfig{
			MaxFileBytes:     1 << 20, // 1 MiB
			EmbedBatchSize:   64,
			EmbedConcurrency: 4,
			Exclude: []string{
				".git", "node_modules", "vendor", "dist", "build",
				"dist-*", "build-*", "coverage", // build-output variants (dist-chrome, build-web) + test coverage — minified/generated code, not source
				".next", ".nuxt", "target", "__pycache__",
				"venv", "env", "site-packages", // Python virtualenvs / installed deps
				"*.min.js", "*.gen.go", "*_gen.go", "*.pb.go", "*_pb.go", "*.lock",
			},
		},
		Daemon: DaemonConfig{
			DebounceMS:       500,
			IdleTimeoutMin:   0,
			EmbedRPS:         0,
			EmbedMaxInFlight: 2,
			EmbedCacheSize:   4096,
		},
		Vecgrep: VecgrepConfig{Enabled: true},
	}
}

// Load resolves configuration with the precedence (low → high):
//
//	DefaultConfig < global file < project file < explicit file < environment.
//
// explicitPath, when non-empty, is the --config flag value; CODEMAP_CONFIG is
// consulted when it is empty.
func Load(explicitPath string) (*Config, error) {
	cfg := DefaultConfig()

	// 1. global config file (XDG/legacy location)
	if err := mergeFileIfExists(cfg, filepath.Join(ConfigDir(), "config.yaml")); err != nil {
		return nil, err
	}

	// 2. project config file (walk up from cwd)
	if root, err := FindProjectRoot(""); err == nil {
		for _, name := range []string{"codemap.yaml", "codemap.yml", filepath.Join(".config", "codemap.yaml")} {
			if err := mergeFileIfExists(cfg, filepath.Join(root, name)); err != nil {
				return nil, err
			}
		}
	}

	// 3. explicit file (--config flag or CODEMAP_CONFIG)
	if explicitPath == "" {
		explicitPath = os.Getenv(EnvConfig)
	}
	if explicitPath != "" {
		if err := mergeFile(cfg, ExpandPath(explicitPath)); err != nil {
			return nil, err
		}
	}

	// 4. environment overrides (highest precedence)
	applyEnv(cfg)

	return cfg, nil
}

// mergeFile reads a YAML file and overlays its keys onto cfg. Keys absent from
// the file keep their current (default/lower-precedence) value.
func mergeFile(cfg *Config, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return fmt.Errorf("parse config %s: %w", path, err)
	}
	return nil
}

func mergeFileIfExists(cfg *Config, path string) error {
	if _, err := os.Stat(path); err != nil {
		return nil //nolint:nilerr // missing file is not an error
	}
	return mergeFile(cfg, path)
}

func applyEnv(cfg *Config) {
	if v := os.Getenv("CODEMAP_EMBEDDING_PROVIDER"); v != "" {
		cfg.Embedding.Provider = v
	}
	if v := os.Getenv("CODEMAP_EMBEDDING_MODEL"); v != "" {
		cfg.Embedding.Model = v
	}
	if v := os.Getenv("CODEMAP_OLLAMA_URL"); v != "" {
		cfg.Embedding.OllamaURL = v
	}
	if v := os.Getenv("CODEMAP_EMBEDDING_DIMENSIONS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Embedding.Dimensions = n
		}
	}
	if v := os.Getenv("CODEMAP_EMBEDDING_DISTANCE"); v != "" {
		cfg.Embedding.Distance = v
	}
	if v := os.Getenv("CODEMAP_DAEMON_DEBOUNCE_MS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Daemon.DebounceMS = n
		}
	}
	if v := os.Getenv("CODEMAP_DAEMON_IDLE_TIMEOUT_MIN"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Daemon.IdleTimeoutMin = n
		}
	}
	if v := os.Getenv("CODEMAP_DAEMON_EMBED_RPS"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Daemon.EmbedRPS = f
		}
	}
	if v := os.Getenv("CODEMAP_DAEMON_EMBED_MAX_IN_FLIGHT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Daemon.EmbedMaxInFlight = n
		}
	}
	if v := os.Getenv("CODEMAP_EXCLUDE_EXTRA"); v != "" {
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				cfg.Index.ExcludeExtra = append(cfg.Index.ExcludeExtra, p)
			}
		}
	}
	if v := os.Getenv("CODEMAP_EMBED_BATCH_SIZE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Index.EmbedBatchSize = n
		}
	}
	if v := os.Getenv("CODEMAP_EMBED_CONCURRENCY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Index.EmbedConcurrency = n
		}
	}
	if v := os.Getenv("CODEMAP_EMBED_MAX_CHARS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Index.EmbedMaxChars = n
		}
	}
	if v := os.Getenv("CODEMAP_VECGREP_ENABLED"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.Vecgrep.Enabled = b
		}
	}
	if v := os.Getenv("CODEMAP_VECGREP_BIN"); v != "" {
		cfg.Vecgrep.Bin = v
	}
}

// Save writes cfg to path as YAML, creating parent directories as needed.
func Save(cfg *Config, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
