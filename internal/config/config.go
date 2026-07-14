package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"gopkg.in/yaml.v3"
)

// LoadError identifies the configuration source and operation that failed.
// Environment parse errors intentionally omit the rejected value: config may
// contain credentials, and startup diagnostics must never echo them.
type LoadError struct {
	Operation   string
	Path        string
	Environment string
	Err         error
}

func (e *LoadError) Error() string {
	if e.Environment != "" {
		return fmt.Sprintf("%s config environment variable %s: %v", e.Operation, e.Environment, e.Err)
	}
	return fmt.Sprintf("%s config file %s: %v", e.Operation, e.Path, e.Err)
}

// Unwrap lets callers distinguish filesystem and parsing failures with
// errors.Is/errors.As without depending on the human-readable message.
func (e *LoadError) Unwrap() error { return e.Err }

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
	// "agent" (exactly the taught workflow plus self-discovery), "core"
	// (the backwards-compatible lean set), or "full" (every tool; the
	// default, back-compatible behavior). Reachable file < env
	// (CODEMAP_MCP_PROFILE) < flag (--profile on `codemap serve`).
	Profile string `yaml:"profile"`
}

// SemanticConfig selects the semantic owner and, for the local owner, controls
// how `codemap semantic`/`search` fuses vector and BM25 (text) search. Backend
// and Fusion are reachable file < env < flag; FusionWeights is a file-only
// advanced-tuning knob (no env/flag), matching the exception pattern documented
// for daemon.embed_cache_size/index.extract_concurrency.
type SemanticConfig struct {
	Backend       string              `yaml:"backend"` // fallback (default: local vectors, vecgrep only when absent) | local | vecgrep
	Fusion        string              `yaml:"fusion"`  // auto (default, classify query shape) | balanced (equal weights, pre-F7 behavior)
	FusionWeights FusionWeightsConfig `yaml:"fusion_weights"`
}

// FusionWeightsConfig holds the per-profile vector/text weight pairs used
// when semantic.fusion is "auto".
type FusionWeightsConfig struct {
	Identifier      FusionWeightPair `yaml:"identifier"`
	NaturalLanguage FusionWeightPair `yaml:"natural_language"`
}

// FusionWeightPair is one profile's veclite HybridSearch weights. Zero uses
// the hybrid search implementation's default weight for that channel.
type FusionWeightPair struct {
	Vector float64 `yaml:"vector"`
	Text   float64 `yaml:"text"`
}

// VecgrepConfig controls the optional CLI adapter to the sibling vecgrep tool.
// It can be a fallback for structure-only indexes or the explicitly configured
// semantic owner; codemap never imports vecgrep packages or shares its store.
type VecgrepConfig struct {
	Enabled bool   `yaml:"enabled"` // try vecgrep for semantic search when codemap has no vectors (default true)
	Bin     string `yaml:"bin"`     // path to the vecgrep binary (resolved via $PATH if empty)
}

// DaemonConfig tunes the background daemon (`codemap daemon`).
type DaemonConfig struct {
	DebounceMS       int     `yaml:"debounce_ms"`         // watcher debounce (default 500; 0 uses default)
	IdleTimeoutMin   int     `yaml:"idle_timeout_min"`    // idle-shutdown after N minutes (0 = never)
	Precise          bool    `yaml:"precise"`             // keep exact call edges current after watched edits
	EmbedRPS         float64 `yaml:"embed_rps"`           // background embed rate to Ollama (0 = unlimited)
	EmbedMaxInFlight int     `yaml:"embed_max_in_flight"` // max concurrent embed calls (default 2; 0 uses default)
	EmbedCacheSize   int     `yaml:"embed_cache_size"`    // dedup cache entries (default 4096; 0 uses default)
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
	MaxFileBytes int `yaml:"max_file_bytes"` // skip files larger than this (0 = no limit)
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
	// (default 64; 0 uses the default). EmbedConcurrency is how many such requests
	// run at once (default 4; 0 uses the default). Embedding dominates a reindex;
	// batching + concurrency is the main speedup for remote endpoints.
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
			Backend: "fallback",
			Fusion:  "auto",
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
			exists, statErr := configFileExists(path)
			if statErr != nil {
				return nil, statErr
			}
			if exists {
				if err := mergeFile(cfg, path); err != nil {
					return nil, err
				}
				break // first match wins
			}
		}
	} else if !errors.Is(err, ErrNoProject) {
		return nil, err
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
	if err := applyEnv(cfg); err != nil {
		return nil, err
	}

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
	for _, setting := range []struct {
		name        string
		value       int
		zeroMeaning string
	}{
		{name: "embedding.dimensions", value: c.Embedding.Dimensions, zeroMeaning: "0 = auto-detect from the model"},
		{name: "index.max_file_bytes", value: c.Index.MaxFileBytes, zeroMeaning: "0 = no size limit"},
		{name: "index.embed_batch_size", value: c.Index.EmbedBatchSize, zeroMeaning: "0 = use the default"},
		{name: "index.embed_concurrency", value: c.Index.EmbedConcurrency, zeroMeaning: "0 = use the default"},
		{name: "index.extract_concurrency", value: c.Index.ExtractConcurrency, zeroMeaning: "0 = use the default"},
		{name: "index.embed_max_chars", value: c.Index.EmbedMaxChars, zeroMeaning: "0 = no text cap"},
		{name: "daemon.debounce_ms", value: c.Daemon.DebounceMS, zeroMeaning: "0 = use the default"},
		{name: "daemon.idle_timeout_min", value: c.Daemon.IdleTimeoutMin, zeroMeaning: "0 = never shut down for idleness"},
		{name: "daemon.embed_max_in_flight", value: c.Daemon.EmbedMaxInFlight, zeroMeaning: "0 = use the default"},
		{name: "daemon.embed_cache_size", value: c.Daemon.EmbedCacheSize, zeroMeaning: "0 = use the default"},
	} {
		if err := validateNonNegativeInt(setting.name, setting.value, setting.zeroMeaning); err != nil {
			return err
		}
	}
	for _, setting := range []struct {
		name        string
		value       float64
		zeroMeaning string
	}{
		{name: "daemon.embed_rps", value: c.Daemon.EmbedRPS, zeroMeaning: "0 = unlimited"},
		{name: "semantic.fusion_weights.identifier.vector", value: c.Semantic.FusionWeights.Identifier.Vector, zeroMeaning: "0 = use the hybrid-search default"},
		{name: "semantic.fusion_weights.identifier.text", value: c.Semantic.FusionWeights.Identifier.Text, zeroMeaning: "0 = use the hybrid-search default"},
		{name: "semantic.fusion_weights.natural_language.vector", value: c.Semantic.FusionWeights.NaturalLanguage.Vector, zeroMeaning: "0 = use the hybrid-search default"},
		{name: "semantic.fusion_weights.natural_language.text", value: c.Semantic.FusionWeights.NaturalLanguage.Text, zeroMeaning: "0 = use the hybrid-search default"},
	} {
		if err := validateNonNegativeFinite(setting.name, setting.value, setting.zeroMeaning); err != nil {
			return err
		}
	}
	switch c.Semantic.Fusion {
	case "auto", "balanced", "":
		// supported (empty defaults to auto at read time)
	default:
		return fmt.Errorf("semantic fusion %q is not a valid value; use auto or balanced", c.Semantic.Fusion)
	}
	backend := strings.ToLower(strings.TrimSpace(c.Semantic.Backend))
	switch backend {
	case "fallback", "local", "vecgrep", "":
		// supported (empty defaults to fallback at read time)
	default:
		return fmt.Errorf("semantic backend %q is not a valid value; use fallback, local, or vecgrep", c.Semantic.Backend)
	}
	if backend == "vecgrep" && !c.Vecgrep.Enabled {
		return fmt.Errorf("semantic backend vecgrep requires vecgrep.enabled: true")
	}
	c.Semantic.Backend = backend
	profile := strings.ToLower(strings.TrimSpace(c.MCP.Profile))
	switch profile {
	case "agent", "core", "full", "":
		// supported (empty defaults to full at read time)
	default:
		return fmt.Errorf("mcp profile %q is not a valid value; use agent, core, or full", c.MCP.Profile)
	}
	c.MCP.Profile = profile
	return nil
}

func validateNonNegativeInt(name string, value int, zeroMeaning string) error {
	if value < 0 {
		return fmt.Errorf("%s must be >= 0 (%s)", name, zeroMeaning)
	}
	return nil
}

func validateNonNegativeFinite(name string, value float64, zeroMeaning string) error {
	if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return fmt.Errorf("%s must be a finite number >= 0 (%s)", name, zeroMeaning)
	}
	return nil
}

// mergeFile reads a YAML file and overlays its keys onto cfg. Keys absent from
// the file keep their current (default/lower-precedence) value.
func mergeFile(cfg *Config, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return &LoadError{Operation: "read", Path: path, Err: err}
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(cfg); errors.Is(err, io.EOF) {
		return nil // empty/comment-only files preserve lower-precedence values
	} else if err != nil {
		return &LoadError{Operation: "parse", Path: path, Err: err}
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple YAML documents are not supported")
		}
		return &LoadError{Operation: "parse", Path: path, Err: err}
	}
	return nil
}

func mergeFileIfExists(cfg *Config, path string) error {
	exists, err := configFileExists(path)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	return mergeFile(cfg, path)
}

func configFileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	switch {
	case err == nil:
		return true, nil
	case isOptionalPathAbsent(err):
		return false, nil
	default:
		return false, &LoadError{Operation: "inspect", Path: path, Err: err}
	}
}

// isOptionalPathAbsent treats ENOTDIR like ENOENT for optional layered config
// probes. This matters for the nested .config/codemap.yaml marker: a project is
// allowed to have a regular file named .config and a later valid .codemap
// marker. Explicit config paths bypass this helper and still fail on ENOTDIR.
func isOptionalPathAbsent(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}

func applyEnv(cfg *Config) error {
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
	if n, set, err := envInt("CODEMAP_EMBEDDING_DIMENSIONS"); err != nil {
		return err
	} else if set {
		cfg.Embedding.Dimensions = n
	}
	if v := os.Getenv("CODEMAP_EMBEDDING_DISTANCE"); v != "" {
		cfg.Embedding.Distance = v
	}
	if n, set, err := envInt("CODEMAP_DAEMON_DEBOUNCE_MS"); err != nil {
		return err
	} else if set {
		cfg.Daemon.DebounceMS = n
	}
	if n, set, err := envInt("CODEMAP_DAEMON_IDLE_TIMEOUT_MIN"); err != nil {
		return err
	} else if set {
		cfg.Daemon.IdleTimeoutMin = n
	}
	if b, set, err := envBool("CODEMAP_DAEMON_PRECISE"); err != nil {
		return err
	} else if set {
		cfg.Daemon.Precise = b
	}
	if f, set, err := envFloat("CODEMAP_DAEMON_EMBED_RPS"); err != nil {
		return err
	} else if set {
		cfg.Daemon.EmbedRPS = f
	}
	if n, set, err := envInt("CODEMAP_DAEMON_EMBED_MAX_IN_FLIGHT"); err != nil {
		return err
	} else if set {
		cfg.Daemon.EmbedMaxInFlight = n
	}
	if v := os.Getenv("CODEMAP_EXCLUDE_EXTRA"); v != "" {
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				cfg.Index.ExcludeExtra = append(cfg.Index.ExcludeExtra, p)
			}
		}
	}
	if n, set, err := envInt("CODEMAP_EMBED_BATCH_SIZE"); err != nil {
		return err
	} else if set {
		cfg.Index.EmbedBatchSize = n
	}
	if n, set, err := envInt("CODEMAP_EMBED_CONCURRENCY"); err != nil {
		return err
	} else if set {
		cfg.Index.EmbedConcurrency = n
	}
	if n, set, err := envInt("CODEMAP_EXTRACT_CONCURRENCY"); err != nil {
		return err
	} else if set {
		cfg.Index.ExtractConcurrency = n
	}
	if n, set, err := envInt("CODEMAP_EMBED_MAX_CHARS"); err != nil {
		return err
	} else if set {
		cfg.Index.EmbedMaxChars = n
	}
	if b, set, err := envBool("CODEMAP_VECGREP_ENABLED"); err != nil {
		return err
	} else if set {
		cfg.Vecgrep.Enabled = b
	}
	if v := os.Getenv("CODEMAP_VECGREP_BIN"); v != "" {
		cfg.Vecgrep.Bin = v
	}
	if v := os.Getenv("CODEMAP_SEMANTIC_FUSION"); v != "" {
		cfg.Semantic.Fusion = v
	}
	if v := os.Getenv("CODEMAP_SEMANTIC_BACKEND"); v != "" {
		cfg.Semantic.Backend = v
	}
	if v := os.Getenv("CODEMAP_MCP_PROFILE"); v != "" {
		cfg.MCP.Profile = v
	}
	return nil
}

func envInt(name string) (int, bool, error) {
	v := os.Getenv(name)
	if v == "" {
		return 0, false, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, true, invalidEnv(name, "must be a base-10 integer")
	}
	return n, true, nil
}

func envBool(name string) (bool, bool, error) {
	v := os.Getenv(name)
	if v == "" {
		return false, false, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, true, invalidEnv(name, "must be a boolean (true/false, t/f, or 1/0)")
	}
	return b, true, nil
}

func envFloat(name string) (float64, bool, error) {
	v := os.Getenv(name)
	if v == "" {
		return 0, false, nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, true, invalidEnv(name, "must be a finite number")
	}
	return f, true, nil
}

func invalidEnv(name, requirement string) error {
	return &LoadError{
		Operation:   "parse",
		Environment: name,
		Err:         errors.New(requirement),
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
