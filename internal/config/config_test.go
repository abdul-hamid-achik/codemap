package config

import (
	"os"
	"path/filepath"
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
		"CODEMAP_OLLAMA_URL", "CODEMAP_EMBEDDING_DIMENSIONS", "CODEMAP_EMBEDDING_DISTANCE",
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
	if !IsNoProject(err) {
		t.Errorf("err = %v, want ErrNoProject", err)
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
