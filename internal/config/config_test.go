package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// clearEnv blanks every env var that influences resolution so each test starts
// from a known baseline. HOME is set per-test.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		EnvConfig, EnvConfigDir, EnvData, EnvCache,
		"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME",
		"CODEMAP_EMBEDDING_PROVIDER", "CODEMAP_EMBEDDING_MODEL",
		"CODEMAP_OLLAMA_URL", "CODEMAP_OLLAMA_API_KEY", "CODEMAP_EMBEDDING_DIMENSIONS", "CODEMAP_EMBEDDING_DISTANCE",
		"CODEMAP_DAEMON_DEBOUNCE_MS", "CODEMAP_DAEMON_IDLE_TIMEOUT_MIN", "CODEMAP_DAEMON_PRECISE",
		"CODEMAP_DAEMON_EMBED_RPS", "CODEMAP_DAEMON_EMBED_MAX_IN_FLIGHT",
		"CODEMAP_EXCLUDE_EXTRA", "CODEMAP_EMBED_BATCH_SIZE", "CODEMAP_EMBED_CONCURRENCY",
		"CODEMAP_EXTRACT_CONCURRENCY", "CODEMAP_EMBED_MAX_CHARS",
		"CODEMAP_VECGREP_ENABLED", "CODEMAP_VECGREP_BIN",
		"CODEMAP_SEMANTIC_BACKEND", "CODEMAP_SEMANTIC_FUSION", "CODEMAP_MCP_PROFILE",
	} {
		t.Setenv(k, "")
	}
}

func TestDefaultConfig(t *testing.T) {
	c := DefaultConfig()
	if c.Embedding.Provider != "ollama" {
		t.Errorf("provider = %q, want ollama", c.Embedding.Provider)
	}
	if c.Embedding.Model != "nomic-embed-text" {
		t.Errorf("model = %q, want nomic-embed-text", c.Embedding.Model)
	}
	if c.Embedding.Dimensions != 768 {
		t.Errorf("dimensions = %d, want 768", c.Embedding.Dimensions)
	}
	if c.Embedding.Distance != "cosine" {
		t.Errorf("distance = %q, want cosine", c.Embedding.Distance)
	}
	if c.Index.MaxFileBytes != 1<<20 {
		t.Errorf("max_file_bytes = %d, want %d", c.Index.MaxFileBytes, 1<<20)
	}
	if len(c.Index.Exclude) == 0 {
		t.Error("expected non-empty default exclude list")
	}
	if c.Daemon.Precise {
		t.Error("daemon precise mode should remain opt-in by default")
	}
}

func TestDaemonPreciseFileEnvPrecedence(t *testing.T) {
	clearEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "codemap.yaml"), []byte("daemon:\n  precise: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	t.Setenv("CODEMAP_DAEMON_PRECISE", "true")

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Daemon.Precise {
		t.Fatal("CODEMAP_DAEMON_PRECISE=true should override daemon.precise=false from the project file")
	}
}

func TestXDGPaths(t *testing.T) {
	clearEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgHome := filepath.Join(home, "xc")
	dataHome := filepath.Join(home, "xd")
	cacheHome := filepath.Join(home, "xca")
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("XDG_CACHE_HOME", cacheHome)

	checks := map[string][2]string{
		"ConfigDir":   {ConfigDir(), filepath.Join(cfgHome, "codemap")},
		"DataDir":     {DataDir(), filepath.Join(dataHome, "codemap")},
		"CacheDir":    {CacheDir(), filepath.Join(cacheHome, "codemap")},
		"ConfigFile":  {ConfigFile(), filepath.Join(cfgHome, "codemap", "config.yaml")},
		"DBPath":      {DBPath(), filepath.Join(dataHome, "codemap", "graph.db")},
		"VeclitePath": {VeclitePath(), filepath.Join(dataHome, "codemap", "codemap.veclite")},
		"RegistryDir": {RegistryDir(), filepath.Join(dataHome, "codemap", "projects")},
	}
	for name, c := range checks {
		if c[0] != c[1] {
			t.Errorf("%s = %q, want %q", name, c[0], c[1])
		}
	}
}

func TestXDGDefaultDirs(t *testing.T) {
	clearEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	if got, want := ConfigDir(), filepath.Join(home, ".config", "codemap"); got != want {
		t.Errorf("ConfigDir = %q, want %q", got, want)
	}
	if got, want := DataDir(), filepath.Join(home, ".local", "share", "codemap"); got != want {
		t.Errorf("DataDir = %q, want %q", got, want)
	}
	if got, want := CacheDir(), filepath.Join(home, ".cache", "codemap"); got != want {
		t.Errorf("CacheDir = %q, want %q", got, want)
	}
}

func TestLegacyFallback(t *testing.T) {
	clearEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	base := filepath.Join(home, ".codemap")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := ConfigDir(); got != base {
		t.Errorf("ConfigDir = %q, want %q", got, base)
	}
	if got := DataDir(); got != base {
		t.Errorf("DataDir = %q, want %q", got, base)
	}
	if got, want := CacheDir(), filepath.Join(base, "cache"); got != want {
		t.Errorf("CacheDir = %q, want %q", got, want)
	}
}

func TestEnvOverridePaths(t *testing.T) {
	clearEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	custom := t.TempDir()
	t.Setenv(EnvData, custom)
	if got := DataDir(); got != custom {
		t.Errorf("DataDir = %q, want %q (CODEMAP_DATA override)", got, custom)
	}
}

func TestConfigFileExpandsTilde(t *testing.T) {
	clearEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvConfig, "~/custom/codemap.yaml")
	if got := ConfigFile(); got != filepath.Join(home, "custom", "codemap.yaml") {
		t.Errorf("ConfigFile = %q, want expanded path", got)
	}
}

func TestExpandPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cases := map[string]string{
		"~":     home,
		"~/x/y": filepath.Join(home, "x", "y"),
		"/abs":  "/abs",
		"rel/p": "rel/p",
	}
	for in, want := range cases {
		if got := ExpandPath(in); got != want {
			t.Errorf("ExpandPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLoadDefaults(t *testing.T) {
	clearEnv(t)
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Embedding.Model != "nomic-embed-text" {
		t.Errorf("model = %q, want default", c.Embedding.Model)
	}
}

// TestDefaultConfigNoAPIKey pins additive-only behavior: an absent api_key
// (no file, no env) leaves EmbeddingConfig.APIKey empty, so a config resolved
// with no Ollama Cloud/auth setup behaves exactly like it did before this
// field existed.
func TestDefaultConfigNoAPIKey(t *testing.T) {
	c := DefaultConfig()
	if c.Embedding.APIKey != "" {
		t.Errorf("default APIKey = %q, want empty", c.Embedding.APIKey)
	}
}

// TestLoadOllamaAPIKeyEnv verifies the CODEMAP_OLLAMA_API_KEY env override
// (file < env, same precedence tier as CODEMAP_OLLAMA_URL) using an obviously
// fake key value, per the "never write a real key" rule for this feature.
func TestLoadOllamaAPIKeyEnv(t *testing.T) {
	clearEnv(t)
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())
	t.Setenv("CODEMAP_OLLAMA_API_KEY", "test-key-1234")

	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Embedding.APIKey != "test-key-1234" {
		t.Errorf("api key = %q, want test-key-1234", c.Embedding.APIKey)
	}
}

// TestLoadOllamaAPIKeyFile verifies the config-file `embedding.api_key` key is
// honored (lowest of the three reachable tiers: file < env; there is no flag
// for this field — secrets on argv leak via `ps`).
func TestLoadOllamaAPIKeyFile(t *testing.T) {
	clearEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Chdir(t.TempDir())

	dir := ConfigDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := "embedding:\n  api_key: test-key-1234\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}

	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Embedding.APIKey != "test-key-1234" {
		t.Errorf("api key = %q, want test-key-1234", c.Embedding.APIKey)
	}
}

func TestLoadGlobalFile(t *testing.T) {
	clearEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Chdir(t.TempDir())

	dir := ConfigDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := "embedding:\n  model: custom-model\n  dimensions: 1024\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}

	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Embedding.Model != "custom-model" {
		t.Errorf("model = %q, want custom-model", c.Embedding.Model)
	}
	if c.Embedding.Dimensions != 1024 {
		t.Errorf("dimensions = %d, want 1024", c.Embedding.Dimensions)
	}
	if c.Embedding.Provider != "ollama" {
		t.Errorf("provider = %q, want ollama (untouched default)", c.Embedding.Provider)
	}
}

func TestLoadEnvOverridesFile(t *testing.T) {
	clearEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Chdir(t.TempDir())

	dir := ConfigDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("embedding:\n  model: file-model\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEMAP_EMBEDDING_MODEL", "env-model")

	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Embedding.Model != "env-model" {
		t.Errorf("model = %q, want env-model (env beats file)", c.Embedding.Model)
	}
}

func TestLoadRejectsUnknownYAMLFields(t *testing.T) {
	tests := []struct {
		name     string
		contents string
		unknown  string
	}{
		{name: "top level", contents: "embeddings: {}\n", unknown: "embeddings"},
		{name: "nested", contents: "embedding:\n  modell: custom\n", unknown: "modell"},
		{name: "deeply nested", contents: "semantic:\n  fusion_weights:\n    identifier:\n      vectors: 1\n", unknown: "vectors"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearEnv(t)
			t.Setenv("HOME", t.TempDir())
			t.Chdir(t.TempDir())
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(tt.contents), 0o644); err != nil {
				t.Fatal(err)
			}

			_, err := Load(path)
			if err == nil {
				t.Fatal("Load should reject an unknown YAML field")
			}
			var loadErr *LoadError
			if !errors.As(err, &loadErr) {
				t.Fatalf("error type = %T, want *LoadError: %v", err, err)
			}
			if loadErr.Operation != "parse" || loadErr.Path != path {
				t.Fatalf("LoadError = %#v, want parse failure for %q", loadErr, path)
			}
			if !strings.Contains(err.Error(), tt.unknown) {
				t.Fatalf("error = %q, want unknown field %q", err, tt.unknown)
			}
		})
	}
}

func TestLoadAllowsEmptyConfigFile(t *testing.T) {
	clearEnv(t)
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("# defaults only\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Embedding.Model != "nomic-embed-text" {
		t.Fatalf("model = %q, want default", cfg.Embedding.Model)
	}
}

func TestLoadRejectsMalformedTypedEnvironmentValues(t *testing.T) {
	tests := []struct {
		name        string
		environment string
		value       string
		want        string
	}{
		{name: "embedding dimensions", environment: "CODEMAP_EMBEDDING_DIMENSIONS", value: "seven hundred", want: "base-10 integer"},
		{name: "daemon debounce", environment: "CODEMAP_DAEMON_DEBOUNCE_MS", value: "fast", want: "base-10 integer"},
		{name: "daemon idle timeout", environment: "CODEMAP_DAEMON_IDLE_TIMEOUT_MIN", value: "later", want: "base-10 integer"},
		{name: "daemon precise", environment: "CODEMAP_DAEMON_PRECISE", value: "sometimes", want: "boolean"},
		{name: "daemon embed rate", environment: "CODEMAP_DAEMON_EMBED_RPS", value: "NaN", want: "finite number"},
		{name: "daemon max in flight", environment: "CODEMAP_DAEMON_EMBED_MAX_IN_FLIGHT", value: "many", want: "base-10 integer"},
		{name: "embed batch size", environment: "CODEMAP_EMBED_BATCH_SIZE", value: "large", want: "base-10 integer"},
		{name: "embed concurrency", environment: "CODEMAP_EMBED_CONCURRENCY", value: "parallel", want: "base-10 integer"},
		{name: "extract concurrency", environment: "CODEMAP_EXTRACT_CONCURRENCY", value: "parallel", want: "base-10 integer"},
		{name: "embed max chars", environment: "CODEMAP_EMBED_MAX_CHARS", value: "all", want: "base-10 integer"},
		{name: "vecgrep enabled", environment: "CODEMAP_VECGREP_ENABLED", value: "enabled", want: "boolean"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearEnv(t)
			t.Setenv("HOME", t.TempDir())
			t.Chdir(t.TempDir())
			t.Setenv(tt.environment, tt.value)

			_, err := Load("")
			if err == nil {
				t.Fatalf("Load should reject %s=%q", tt.environment, tt.value)
			}
			var loadErr *LoadError
			if !errors.As(err, &loadErr) {
				t.Fatalf("error type = %T, want *LoadError: %v", err, err)
			}
			if loadErr.Operation != "parse" || loadErr.Environment != tt.environment {
				t.Fatalf("LoadError = %#v, want parse failure for %s", loadErr, tt.environment)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want %q", err, tt.want)
			}
			if strings.Contains(err.Error(), tt.value) {
				t.Fatalf("error must not echo the rejected environment value: %q", err)
			}
		})
	}
}

func TestLoadPropagatesConfigFilesystemErrors(t *testing.T) {
	t.Run("explicit path is unreadable as a file", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("HOME", t.TempDir())
		t.Chdir(t.TempDir())
		path := t.TempDir()

		_, err := Load(path)
		if err == nil {
			t.Fatal("Load should return an explicit config read error")
		}
		var loadErr *LoadError
		if !errors.As(err, &loadErr) {
			t.Fatalf("error type = %T, want *LoadError: %v", err, err)
		}
		if loadErr.Operation != "read" || loadErr.Path != path {
			t.Fatalf("LoadError = %#v, want read failure for %q", loadErr, path)
		}
	})

	t.Run("explicit path enotdir is still fatal", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("HOME", t.TempDir())
		t.Chdir(t.TempDir())
		parent := t.TempDir()
		blockingPath := filepath.Join(parent, "not-a-directory")
		if err := os.WriteFile(blockingPath, []byte("file"), 0o644); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(blockingPath, "config.yaml")

		_, err := Load(path)
		if err == nil {
			t.Fatal("Load should return ENOTDIR for an explicit config path")
		}
		var loadErr *LoadError
		if !errors.As(err, &loadErr) {
			t.Fatalf("error type = %T, want *LoadError: %v", err, err)
		}
		if loadErr.Operation != "read" || loadErr.Path != path {
			t.Fatalf("LoadError = %#v, want read failure for %q", loadErr, path)
		}
	})

	t.Run("global config stat failure is not treated as missing", func(t *testing.T) {
		clearEnv(t)
		home := t.TempDir()
		t.Setenv("HOME", home)
		configHome := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", configHome)
		t.Chdir(t.TempDir())
		configDir := filepath.Join(configHome, "codemap")
		if err := os.MkdirAll(configDir, 0o755); err != nil {
			t.Fatal(err)
		}
		configPath := filepath.Join(configDir, "config.yaml")
		if err := os.Symlink("config.yaml", configPath); err != nil {
			t.Skipf("cannot create symlink loop: %v", err)
		}

		_, err := Load("")
		if err == nil {
			t.Fatal("Load should propagate a config stat failure")
		}
		var loadErr *LoadError
		if !errors.As(err, &loadErr) {
			t.Fatalf("error type = %T, want *LoadError: %v", err, err)
		}
		if loadErr.Operation != "inspect" {
			t.Fatalf("LoadError = %#v, want inspect failure", loadErr)
		}
	})
}

func TestLoadValidPrecedence(t *testing.T) {
	tests := []struct {
		name             string
		projectValue     int
		explicitValue    int
		environmentValue string
		want             int
	}{
		{name: "global only", want: 100},
		{name: "project over global", projectValue: 200, want: 200},
		{name: "explicit over project", projectValue: 200, explicitValue: 300, want: 300},
		{name: "environment over explicit", projectValue: 200, explicitValue: 300, environmentValue: "400", want: 400},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearEnv(t)
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

			globalDir := ConfigDir()
			if err := os.MkdirAll(globalDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(globalDir, "config.yaml"), []byte("embedding:\n  dimensions: 100\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			project := t.TempDir()
			projectContents := "{}\n"
			if tt.projectValue != 0 {
				projectContents = fmt.Sprintf("embedding:\n  dimensions: %d\n", tt.projectValue)
			}
			if err := os.WriteFile(filepath.Join(project, "codemap.yaml"), []byte(projectContents), 0o644); err != nil {
				t.Fatal(err)
			}
			t.Chdir(project)

			explicitPath := ""
			if tt.explicitValue != 0 {
				explicitPath = filepath.Join(t.TempDir(), "explicit.yaml")
				contents := fmt.Sprintf("embedding:\n  dimensions: %d\n", tt.explicitValue)
				if err := os.WriteFile(explicitPath, []byte(contents), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if tt.environmentValue != "" {
				t.Setenv("CODEMAP_EMBEDDING_DIMENSIONS", tt.environmentValue)
			}

			cfg, err := Load(explicitPath)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Embedding.Dimensions != tt.want {
				t.Fatalf("dimensions = %d, want %d", cfg.Embedding.Dimensions, tt.want)
			}
		})
	}
}

func TestLoadProjectFile(t *testing.T) {
	clearEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "codemap.yaml"), []byte("embedding:\n  model: proj-model\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(proj, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)

	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Embedding.Model != "proj-model" {
		t.Errorf("model = %q, want proj-model (project file)", c.Embedding.Model)
	}
}

func TestFindProjectRoot(t *testing.T) {
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "codemap.yaml"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(proj, "x", "y")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := FindProjectRoot(sub)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.EvalSymlinks(proj)
	gotResolved, _ := filepath.EvalSymlinks(got)
	if gotResolved != want {
		t.Errorf("FindProjectRoot = %q, want %q", gotResolved, want)
	}
}

func TestFindProjectRootNone(t *testing.T) {
	_, err := FindProjectRoot(t.TempDir())
	if !errors.Is(err, ErrNoProject) {
		t.Errorf("err = %v, want ErrNoProject", err)
	}
}

func TestOptionalNestedConfigENOTDIRDoesNotHideDotCodemapMarker(t *testing.T) {
	clearEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".config"), []byte("regular file"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := FindProjectRoot(root)
	if err != nil {
		t.Fatalf("FindProjectRoot should continue past nested-marker ENOTDIR: %v", err)
	}
	if got != root {
		t.Fatalf("FindProjectRoot = %q, want %q", got, root)
	}

	t.Chdir(root)
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load should treat optional .config/codemap.yaml ENOTDIR as absent: %v", err)
	}
	if cfg.Embedding.Model != "nomic-embed-text" {
		t.Fatalf("model = %q, want default", cfg.Embedding.Model)
	}
}

func TestFindProjectRootPropagatesMarkerIOErrors(t *testing.T) {
	root := t.TempDir()
	if err := os.Symlink(".config", filepath.Join(root, ".config")); err != nil {
		t.Skipf("cannot create symlink loop: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := FindProjectRoot(root)
	if err == nil {
		t.Fatal("FindProjectRoot should propagate non-absence marker errors")
	}
	if !strings.Contains(err.Error(), "inspect project marker") {
		t.Fatalf("error = %q, want project marker context", err)
	}
}

func TestDeriveProjectName(t *testing.T) {
	cases := map[string]string{
		"/Users/x/projects/codemap":  "codemap",
		"/Users/x/projects/codemap/": "codemap",
		"/":                          "root",
		"":                           "root",
	}
	for in, want := range cases {
		if got := DeriveProjectName(in); got != want {
			t.Errorf("DeriveProjectName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSaveLoadRoundtrip(t *testing.T) {
	clearEnv(t)
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())

	c := DefaultConfig()
	c.Embedding.Model = "roundtrip"
	path := filepath.Join(t.TempDir(), "c.yaml")
	if err := Save(c, path); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Embedding.Model != "roundtrip" {
		t.Errorf("model = %q, want roundtrip", loaded.Embedding.Model)
	}
}

func TestSiblingProjectIndexed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// A vecgrep-style registry entry for "myproj".
	if err := os.MkdirAll(filepath.Join(home, ".vecgrep", "projects", "myproj"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !SiblingProjectIndexed("vecgrep", "myproj") {
		t.Error("should detect a vecgrep registry entry for myproj")
	}
	if SiblingProjectIndexed("vecgrep", "absent") {
		t.Error("should not detect a project with no registry entry")
	}
	if SiblingProjectIndexed("vecgrep", "") {
		t.Error("empty name must not match")
	}
}

// TestConfigValidateProvider pins P1-12 (B68): the embedding provider
// field was advertised as openai/cohere/voyage but never implemented —
// now an unsupported provider is a hard error.
func TestConfigValidateProvider(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Embedding.Provider = "openai"
	if err := cfg.Validate(); err == nil {
		t.Error("Validate should reject unsupported provider 'openai'")
	}
	cfg.Embedding.Provider = "ollama"
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate should accept 'ollama': %v", err)
	}
}

// TestConfigValidateDistance pins P1-12 (O72): invalid distance values
// are rejected.
func TestConfigValidateDistance(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Embedding.Distance = "manhattan"
	if err := cfg.Validate(); err == nil {
		t.Error("Validate should reject unsupported distance 'manhattan'")
	}
	cfg.Embedding.Distance = "cosine"
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate should accept 'cosine': %v", err)
	}
}

func TestLoadRejectsInvalidNumericYAMLValues(t *testing.T) {
	tests := []struct {
		name     string
		contents string
		want     string
		value    string
	}{
		{name: "embedding dimensions", contents: "embedding:\n  dimensions: -1\n", want: "embedding.dimensions", value: "-1"},
		{name: "max file bytes", contents: "index:\n  max_file_bytes: -2\n", want: "index.max_file_bytes", value: "-2"},
		{name: "embed batch size", contents: "index:\n  embed_batch_size: -3\n", want: "index.embed_batch_size", value: "-3"},
		{name: "embed concurrency", contents: "index:\n  embed_concurrency: -4\n", want: "index.embed_concurrency", value: "-4"},
		{name: "extract concurrency", contents: "index:\n  extract_concurrency: -5\n", want: "index.extract_concurrency", value: "-5"},
		{name: "embed max chars", contents: "index:\n  embed_max_chars: -6\n", want: "index.embed_max_chars", value: "-6"},
		{name: "daemon debounce", contents: "daemon:\n  debounce_ms: -7\n", want: "daemon.debounce_ms", value: "-7"},
		{name: "daemon idle timeout", contents: "daemon:\n  idle_timeout_min: -8\n", want: "daemon.idle_timeout_min", value: "-8"},
		{name: "daemon embed rate negative", contents: "daemon:\n  embed_rps: -0.5\n", want: "daemon.embed_rps", value: "-0.5"},
		{name: "daemon embed rate non-finite", contents: "daemon:\n  embed_rps: .nan\n", want: "daemon.embed_rps", value: ".nan"},
		{name: "daemon max in flight", contents: "daemon:\n  embed_max_in_flight: -9\n", want: "daemon.embed_max_in_flight", value: "-9"},
		{name: "daemon cache size", contents: "daemon:\n  embed_cache_size: -10\n", want: "daemon.embed_cache_size", value: "-10"},
		{name: "identifier vector weight negative", contents: "semantic:\n  fusion_weights:\n    identifier:\n      vector: -0.1\n", want: "semantic.fusion_weights.identifier.vector", value: "-0.1"},
		{name: "identifier text weight nan", contents: "semantic:\n  fusion_weights:\n    identifier:\n      text: .nan\n", want: "semantic.fusion_weights.identifier.text", value: ".nan"},
		{name: "natural language vector weight infinity", contents: "semantic:\n  fusion_weights:\n    natural_language:\n      vector: .inf\n", want: "semantic.fusion_weights.natural_language.vector", value: ".inf"},
		{name: "natural language text weight negative infinity", contents: "semantic:\n  fusion_weights:\n    natural_language:\n      text: -.inf\n", want: "semantic.fusion_weights.natural_language.text", value: "-.inf"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearEnv(t)
			t.Setenv("HOME", t.TempDir())
			t.Chdir(t.TempDir())
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(tt.contents), 0o644); err != nil {
				t.Fatal(err)
			}

			_, err := Load(path)
			if err == nil {
				t.Fatal("Load should reject the invalid numeric setting")
			}
			message := err.Error()
			if !strings.Contains(message, tt.want) {
				t.Fatalf("error = %q, want setting %q", message, tt.want)
			}
			if message != strings.ToLower(message) || strings.HasSuffix(message, ".") {
				t.Fatalf("validation error must be lowercase with no trailing punctuation: %q", message)
			}
			if strings.Contains(message, tt.value) {
				t.Fatalf("validation error must not echo the rejected value: %q", message)
			}
		})
	}
}

func TestLoadRejectsNegativeNumericEnvironmentValues(t *testing.T) {
	tests := []struct {
		name        string
		environment string
		want        string
	}{
		{name: "embedding dimensions", environment: "CODEMAP_EMBEDDING_DIMENSIONS", want: "embedding.dimensions"},
		{name: "daemon debounce", environment: "CODEMAP_DAEMON_DEBOUNCE_MS", want: "daemon.debounce_ms"},
		{name: "daemon idle timeout", environment: "CODEMAP_DAEMON_IDLE_TIMEOUT_MIN", want: "daemon.idle_timeout_min"},
		{name: "daemon embed rate", environment: "CODEMAP_DAEMON_EMBED_RPS", want: "daemon.embed_rps"},
		{name: "daemon max in flight", environment: "CODEMAP_DAEMON_EMBED_MAX_IN_FLIGHT", want: "daemon.embed_max_in_flight"},
		{name: "embed batch size", environment: "CODEMAP_EMBED_BATCH_SIZE", want: "index.embed_batch_size"},
		{name: "embed concurrency", environment: "CODEMAP_EMBED_CONCURRENCY", want: "index.embed_concurrency"},
		{name: "extract concurrency", environment: "CODEMAP_EXTRACT_CONCURRENCY", want: "index.extract_concurrency"},
		{name: "embed max chars", environment: "CODEMAP_EMBED_MAX_CHARS", want: "index.embed_max_chars"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearEnv(t)
			t.Setenv("HOME", t.TempDir())
			t.Chdir(t.TempDir())
			t.Setenv(tt.environment, "-1")

			_, err := Load("")
			if err == nil {
				t.Fatalf("Load should reject negative %s", tt.environment)
			}
			message := err.Error()
			if !strings.Contains(message, tt.want) {
				t.Fatalf("error = %q, want setting %q", message, tt.want)
			}
			if message != strings.ToLower(message) || strings.HasSuffix(message, ".") {
				t.Fatalf("validation error must be lowercase with no trailing punctuation: %q", message)
			}
			if strings.Contains(message, "-1") {
				t.Fatalf("validation error must not echo the rejected value: %q", message)
			}
		})
	}
}

func TestConfigValidateAllowsDocumentedZeroValues(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Embedding.Dimensions = 0
	cfg.Index.MaxFileBytes = 0
	cfg.Index.EmbedBatchSize = 0
	cfg.Index.EmbedConcurrency = 0
	cfg.Index.ExtractConcurrency = 0
	cfg.Index.EmbedMaxChars = 0
	cfg.Daemon.DebounceMS = 0
	cfg.Daemon.IdleTimeoutMin = 0
	cfg.Daemon.EmbedRPS = 0
	cfg.Daemon.EmbedMaxInFlight = 0
	cfg.Daemon.EmbedCacheSize = 0
	cfg.Semantic.FusionWeights = FusionWeightsConfig{}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate should accept documented zero-value semantics: %v", err)
	}
}

// TestSemanticFusionDefault pins F7: fusion defaults to "auto".
func TestSemanticFusionDefault(t *testing.T) {
	c := DefaultConfig()
	if c.Semantic.Fusion != "auto" {
		t.Errorf("Semantic.Fusion = %q, want auto", c.Semantic.Fusion)
	}
}

func TestSemanticBackendDefaultEnvAndValidation(t *testing.T) {
	clearEnv(t)
	if got := DefaultConfig().Semantic.Backend; got != "fallback" {
		t.Fatalf("default semantic backend = %q, want fallback", got)
	}
	t.Setenv("CODEMAP_SEMANTIC_BACKEND", "vecgrep")
	t.Setenv("CODEMAP_VECGREP_ENABLED", "true")
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Semantic.Backend != "vecgrep" {
		t.Fatalf("semantic backend env = %q, want vecgrep", cfg.Semantic.Backend)
	}
	cfg.Semantic.Backend = "mystery"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "semantic backend") {
		t.Fatalf("invalid semantic backend error = %v", err)
	}
	cfg.Semantic.Backend = "vecgrep"
	cfg.Vecgrep.Enabled = false
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "vecgrep.enabled") {
		t.Fatalf("disabled vecgrep backend error = %v", err)
	}
}

// TestSemanticFusionEnvOverride pins F7: CODEMAP_SEMANTIC_FUSION overrides
// the config-file/default value via applyEnv.
func TestSemanticFusionEnvOverride(t *testing.T) {
	clearEnv(t)
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())
	t.Setenv("CODEMAP_SEMANTIC_FUSION", "balanced")
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Semantic.Fusion != "balanced" {
		t.Errorf("Semantic.Fusion = %q, want balanced (env override)", c.Semantic.Fusion)
	}
}

// TestSemanticFusionValidate pins F7: Validate rejects an unrecognized
// semantic.fusion value.
func TestSemanticFusionValidate(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Semantic.Fusion = "nonsense"
	if err := cfg.Validate(); err == nil {
		t.Error("Validate should reject semantic.fusion 'nonsense'")
	}
	cfg.Semantic.Fusion = "balanced"
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate should accept 'balanced': %v", err)
	}
	cfg.Semantic.Fusion = ""
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate should accept empty (defaults to auto): %v", err)
	}
}

// TestConfigProjectFirstMatchWins pins P1-12 (B67): pre-fix Load merged
// ALL matching project config markers (codemap.yaml + codemap.yml +
// .config/codemap.yaml) with last-wins, inverting the documented
// precedence. Now the first match wins.
func TestConfigProjectFirstMatchWins(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("CODEMAP_DATA", filepath.Join(home, "data"))
	t.Setenv("CODEMAP_CONFIG", "")
	// Create a project with two config files; codemap.yaml should win.
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "codemap.yaml"), []byte("embedding:\n  model: from-yaml\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(root, ".config"), 0o755)
	_ = os.WriteFile(filepath.Join(root, ".config", "codemap.yaml"), []byte("embedding:\n  model: from-dotconfig\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(root, ".codemap"), 0o755) // marker so FindProjectRoot finds root
	t.Chdir(root)
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Embedding.Model != "from-yaml" {
		t.Errorf("first-match-wins: model = %q, want from-yaml (codemap.yaml at root)", cfg.Embedding.Model)
	}
}

// TestMCPProfileDefault pins I01: mcp.profile defaults to "full" — every
// existing MCP registration keeps back-compat behavior (42 tools) unless a
// project/user opts into "agent" or "core".
func TestMCPProfileDefault(t *testing.T) {
	c := DefaultConfig()
	if c.MCP.Profile != "full" {
		t.Errorf("MCP.Profile = %q, want full", c.MCP.Profile)
	}
}

// TestMCPProfileFileEnvPrecedence pins I01's file < env precedence for
// mcp.profile (flag precedence, on top of env, is covered in cmd/codemap
// since the --profile flag lives on `codemap serve`).
func TestMCPProfileFileEnvPrecedence(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("CODEMAP_DATA", filepath.Join(home, "data"))
	t.Setenv("CODEMAP_CONFIG", "")
	t.Setenv("CODEMAP_MCP_PROFILE", "")

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "codemap.yaml"), []byte("mcp:\n  profile: core\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)

	// File alone: codemap.yaml wins over the DefaultConfig() "full".
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MCP.Profile != "core" {
		t.Errorf("file-only: MCP.Profile = %q, want core", cfg.MCP.Profile)
	}

	// Env overrides the file (env is higher precedence than a project file),
	// including the exact taught-workflow profile.
	t.Setenv("CODEMAP_MCP_PROFILE", "agent")
	cfg, err = Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MCP.Profile != "agent" {
		t.Errorf("env-over-file: MCP.Profile = %q, want agent", cfg.MCP.Profile)
	}
}

// TestMCPProfileValidate pins I01's clean-startup-error contract: an
// unrecognized mcp.profile value is a hard, lowercase, no-trailing-
// punctuation error (not a silent fallback), while "agent"/"core"/"full"/"" are
// all accepted (empty normalizes to "full" at read time) and Validate
// lowercases/trims a valid value in place.
func TestMCPProfileValidate(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MCP.Profile = "bogus"
	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate should reject mcp.profile 'bogus'")
	}
	msg := err.Error()
	if msg != strings.ToLower(msg) {
		t.Errorf("error message should be lowercase: %q", msg)
	}
	if strings.HasSuffix(msg, ".") {
		t.Errorf("error message should have no trailing punctuation: %q", msg)
	}
	for _, v := range []string{"agent", "core", "full", "", "  AGENT  ", "  CORE  "} {
		cfg.MCP.Profile = v
		if err := cfg.Validate(); err != nil {
			t.Errorf("Validate should accept %q: %v", v, err)
		}
	}
	cfg.MCP.Profile = "  CORE  "
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.MCP.Profile != "core" {
		t.Errorf("Validate should normalize profile to lowercase/trimmed, got %q", cfg.MCP.Profile)
	}
}
