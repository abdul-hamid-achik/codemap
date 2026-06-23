package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"gopkg.in/yaml.v3"
)

// Config is the resolved codemap configuration.
type Config struct {
	Embedding EmbeddingConfig `yaml:"embedding"`
	Index     IndexConfig     `yaml:"index"`
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
	MaxFileBytes int      `yaml:"max_file_bytes"` // skip files larger than this
	Exclude      []string `yaml:"exclude"`        // path globs to skip
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
			MaxFileBytes: 1 << 20, // 1 MiB
			Exclude: []string{
				".git", "node_modules", "vendor", "dist", "build",
				".next", ".nuxt", "target", "__pycache__",
				"*.min.js", "*.gen.go", "*.pb.go", "*_pb.go", "*.lock",
			},
		},
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

// WriteDefault writes the default config to the global config file if absent.
func WriteDefault() (string, error) {
	path := ConfigFile()
	if _, err := os.Stat(path); err == nil {
		return path, nil // already exists
	}
	return path, Save(DefaultConfig(), path)
}
