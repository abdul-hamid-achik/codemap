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
	Semantic  SemanticConfig  `yaml:"semantic"`
	MCP       MCPConfig       `yaml:"mcp"`
}

// MCPConfig controls `codemap serve` (the MCP stdio server).
type MCPConfig struct {
	// Profile selects which set of MCP tools codemap_serve registers:
	// "core" (a lean set covering the taught agent workflow — see
	// internal/mcp's coreTools) or "full" (every tool; the default,
	// back-compatible behavior). Reachable file < env
	// (CODEMAP_MCP_PROFILE) < flag (--profile on `codemap serve`).
	Profile string `yaml:"profile"`
}

// SemanticConfig controls how `codemap semantic`/`search` fuses vector and
// BM25 (text) search. Fusion is reachable file < env (CODEMAP_SEMANTIC_FUSION)
// < flag (--fusion); FusionWeights is a file-only advanced-tuning knob (no
// env/flag), matching the exception pattern documented for
// daemon.embed_cache_size/index.extract_concurrency.
type SemanticConfig struct {
	Fusion        string              `yaml:"fusion"` // auto (default, classify query shape) | balanced (equal weights, pre-F7 behavior)
	FusionWeights FusionWeightsConfig `yaml:"fusion_weights"`
}

// FusionWeightsConfig holds the per-profile vector/text weight pairs used
// when semantic.fusion is "auto".
type FusionWeightsConfig struct {
	Identifier      FusionWeightPair `yaml:"identifier"`
	NaturalLanguage FusionWeightPair `yaml:"natural_language"`
}

// FusionWeightPair is one profile's veclite HybridSearch weights.
type FusionWeightPair struct {
	Vector float64 `yaml:"vector"`
	Text   float64 `yaml:"text"`
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
	// APIKey authenticates against Ollama Cloud (ollama_url: https://ollama.com)
	// or any authenticated Ollama-compatible endpoint (e.g. a team server behind
	// a reverse proxy). Optional — empty means today's unauthenticated local
	// behavior, unchanged. Reachable via config file (this field) or the
	// CODEMAP_OLLAMA_API_KEY env var ONLY — deliberately no CLI flag, since flag
	// values are visible in `ps` output; see docs/configuration.md. Never printed
	// by `config show`/`doctor` — those report only whether it is set.
	APIKey string `yaml:"api_key"`
}

// IndexConfig controls what gets indexed.
type IndexConfig struct {
	MaxFileBytes int `yaml:"max_file_bytes"` // skip files larger than this
	// Exclude fully REPLACES the built-in default skip list (.git, node_modules,
	// vendor, …). Set it only to override those defaults wholesale. A pattern
	// without a slash matches any path segment ("migrations" skips a migrations
	// dir at any depth); a pattern with a slash ANYWHERE — including a lone
	// trailing slash like "env/" — anchors to the project root ("db/migrations",
	// "env/" skip only a root-level db/migrations or env/, not a nested
	// internal/env/), and a "**/" prefix un-anchors, matching at any depth
	// ("**/testdata"). See matchExclude in internal/index for the exact rules.
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
	// ExtractConcurrency is how many Go files are extracted in parallel (default
	// 4). The gosrc extractor is stateless (pure go/parser), so parsing is
	// CPU-bound and benefits from goroutines; graph writes serialize on the
	// single-connection pool, so the parallelism overlaps parsing with I/O. LSP
	// files stay sequential (stateful server connection). 0 defaults to 4.
	ExtractConcurrency int `yaml:"extract_concurrency"`
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
			MaxFileBytes:       1 << 20, // 1 MiB
			EmbedBatchSize:     64,
			EmbedConcurrency:   4,
			ExtractConcurrency: 4,
			// P1-11 (B66): bare names like "env" matched any segment at any depth,
			// silently excluding real Go subpackages like go/build or internal/env
			// (the stdlib itself ships a "go/build" package, and "internal/env" is
			// a common config-package name). A first fix attempt anchored these with
			// a trailing slash ("env/") but matchExclude trimmed that slash BEFORE
			// deciding whether the pattern was anchored, so it silently collapsed
			// back to the bare/any-depth form — matchExclude is now fixed (see its
			// doc comment) so the trailing slash below is real root-anchoring.
			//
			// Split by ambiguity, not by "is it a build artifact":
			//   - root-anchored (trailing slash): dist/, build/, target/, coverage/,
			//     venv/, env/ — these collide with plausible source package/dir names
			//     (go/build, internal/env, internal/coverage, a user "target" or
			//     "dist" package) often enough that any-depth was a footgun. Trade-off:
			//     a monorepo with a nested per-package build output (e.g.
			//     packages/foo/dist/) will need its own exclude_extra entry — this is
			//     bloat, not a correctness bug, so it's the acceptable side to err on.
			//   - any-depth (bare, no slash): node_modules, vendor, .git, __pycache__,
			//     site-packages — these are never legitimate source directory names in
			//     Go/TS/JS/Python, so any-depth stays safe and also catches nested
			//     dependency trees (e.g. a venv's own site-packages nested arbitrarily
			//     deep, or a workspace's per-package node_modules).
			Exclude: []string{
				".git", "node_modules", "vendor",
				"dist/", "build/", "target/", "coverage/",
				"dist-*", "build-*",
				".next", ".nuxt", "__pycache__",
				"venv/", "env/", "site-packages",
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
		Semantic: SemanticConfig{
			Fusion: "auto",
			FusionWeights: FusionWeightsConfig{
				Identifier:      FusionWeightPair{Vector: 0.5, Text: 1.5},
				NaturalLanguage: FusionWeightPair{Vector: 1.5, Text: 0.5},
			},
		},
		MCP: MCPConfig{Profile: "full"},
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

	// 2. project config file (walk up from cwd). P1-12 (B67): pre-fix
	// merged ALL matching markers (codemap.yaml + codemap.yml +
	// .config/codemap.yaml), with the LAST one winning — inverted
	// from the documented precedence (codemap.yaml at root is highest).
	// Now we stop at the first match so the documented order holds.
	if root, err := FindProjectRoot(""); err == nil {
		for _, name := range []string{"codemap.yaml", "codemap.yml", filepath.Join(".config", "codemap.yaml")} {
			path := filepath.Join(root, name)
			if _, statErr := os.Stat(path); statErr == nil {
				if err := mergeFile(cfg, path); err != nil {
					return nil, err
				}
				break // first match wins
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

	// P1-12 (B68/O72): validate the resolved config so silent bad
	// values (unsupported provider, invalid distance, zero dims)
	// surface as a clear error instead of silently degrading.
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Validate checks the resolved config for internal consistency. P1-12
// (B68/O72): the embedding provider field was advertised as
// openai/cohere/voyage but never implemented — now an unsupported
// provider is a hard error so the user knows to fix it, not wonder
// why embeddings silently fell back to ollama.
func (c *Config) Validate() error {
	switch c.Embedding.Provider {
	case "ollama", "":
		// supported (default)
	default:
		return fmt.Errorf("embedding provider %q is not implemented; only \"ollama\" is supported; set it to \"ollama\" or remove the provider field", c.Embedding.Provider)
	}
	switch c.Embedding.Distance {
	case "cosine", "dot", "euclidean", "":
		// supported
	default:
		return fmt.Errorf("embedding distance %q is not a valid value; use cosine, dot, or euclidean", c.Embedding.Distance)
	}
	if c.Embedding.Dimensions < 0 {
		return fmt.Errorf("embedding dimensions must be >= 0 (0 = auto-detect from the model)")
	}
	switch c.Semantic.Fusion {
	case "auto", "balanced", "":
		// supported (empty defaults to auto at read time)
	default:
		return fmt.Errorf("semantic fusion %q is not a valid value; use auto or balanced", c.Semantic.Fusion)
	}
	profile := strings.ToLower(strings.TrimSpace(c.MCP.Profile))
	switch profile {
	case "core", "full", "":
		// supported (empty defaults to full at read time)
	default:
		return fmt.Errorf("mcp profile %q is not a valid value; use core or full", c.MCP.Profile)
	}
	c.MCP.Profile = profile
	return nil
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
	if v := os.Getenv("CODEMAP_OLLAMA_API_KEY"); v != "" {
		cfg.Embedding.APIKey = v
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
	if v := os.Getenv("CODEMAP_EXTRACT_CONCURRENCY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Index.ExtractConcurrency = n
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
	if v := os.Getenv("CODEMAP_SEMANTIC_FUSION"); v != "" {
		cfg.Semantic.Fusion = v
	}
	if v := os.Getenv("CODEMAP_MCP_PROFILE"); v != "" {
		cfg.MCP.Profile = v
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
