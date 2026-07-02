package config

import (
	"os"
	"path/filepath"
	"strings"
)

const appName = "codemap"

// Environment variables that override path resolution.
const (
	EnvConfig    = "CODEMAP_CONFIG"     // explicit config file path
	EnvConfigDir = "CODEMAP_CONFIG_DIR" // override the config directory
	EnvData      = "CODEMAP_DATA"       // override the data directory
	EnvCache     = "CODEMAP_CACHE"      // override the cache directory
)

// legacyBase returns ~/.codemap if it already exists as a directory. This keeps
// codemap compatible with the single-directory layout used elsewhere in the
// ecosystem (vecgrep's ~/.vecgrep, etc.) when a user already has one.
func legacyBase() (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	base := filepath.Join(home, "."+appName)
	if fi, err := os.Stat(base); err == nil && fi.IsDir() {
		return base, true
	}
	return "", false
}

// xdgDir returns the value of an XDG_* env var, or $HOME/<fallbackRel> when it
// is unset. We resolve XDG explicitly (rather than os.UserConfigDir) so the
// behavior is identical on Linux and macOS, honoring the user's XDG request.
func xdgDir(envVar, fallbackRel string) string {
	if v := os.Getenv(envVar); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, fallbackRel)
}

// ConfigDir is the directory holding config.yaml.
func ConfigDir() string {
	if v := os.Getenv(EnvConfigDir); v != "" {
		return v
	}
	if base, ok := legacyBase(); ok {
		return base
	}
	return filepath.Join(xdgDir("XDG_CONFIG_HOME", ".config"), appName)
}

// DataDir holds the graph DB, the vector store, and the project registry.
func DataDir() string {
	if v := os.Getenv(EnvData); v != "" {
		return v
	}
	if base, ok := legacyBase(); ok {
		return base
	}
	return filepath.Join(xdgDir("XDG_DATA_HOME", filepath.Join(".local", "share")), appName)
}

// CacheDir holds derived caches that are safe to delete.
func CacheDir() string {
	if v := os.Getenv(EnvCache); v != "" {
		return v
	}
	if base, ok := legacyBase(); ok {
		return filepath.Join(base, "cache")
	}
	return filepath.Join(xdgDir("XDG_CACHE_HOME", ".cache"), appName)
}

// ConfigFile is the path to the global config file. P3-01 (O74): the
// pre-fix version didn't honor the explicit `CODEMAP_CONFIG` override
// (or the project's repo-local one) — a `codemap config show/path`
// command today can point the user at the wrong file.
func ConfigFile() string {
	if v := os.Getenv(EnvConfig); v != "" {
		return v
	}
	if v := os.Getenv(EnvConfigDir); v != "" {
		return filepath.Join(v, "config.yaml")
	}
	return filepath.Join(ConfigDir(), "config.yaml")
}

// DBPath is the SQLite graph database path.
func DBPath() string { return filepath.Join(DataDir(), "graph.db") }

// VeclitePath is the veclite vector store path.
func VeclitePath() string { return filepath.Join(DataDir(), appName+".veclite") }

// RegistryDir holds per-project registry entries and local index state.
func RegistryDir() string { return filepath.Join(DataDir(), "projects") }

// SiblingProjectIndexed reports whether a sibling ecosystem tool (e.g. "vecgrep")
// has the named project indexed, by stat-ing its conventional global registry at
// ~/.<tool>/projects/<name>. It is NAME-BASED: it matches when both tools derived
// the same project name — the directory basename, which is the common case — so a
// collision-renamed project may be missed. Best-effort discovery for a status
// hint, never authoritative.
func SiblingProjectIndexed(tool, name string) bool {
	home, err := os.UserHomeDir()
	if err != nil || name == "" {
		return false
	}
	fi, err := os.Stat(filepath.Join(home, "."+tool, "projects", name))
	return err == nil && fi.IsDir()
}

// DaemonSocketPath is the unix socket the background daemon listens on.
func DaemonSocketPath() string { return filepath.Join(DataDir(), "daemon.sock") }

// DaemonStatePath is the daemon's status file (pid, project, last reindex).
func DaemonStatePath() string { return filepath.Join(DataDir(), "daemon.json") }

// ExpandPath expands a leading "~" or "~/" to the user's home directory.
func ExpandPath(p string) string {
	switch {
	case p == "~":
		home, _ := os.UserHomeDir()
		return home
	case strings.HasPrefix(p, "~/"):
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[2:])
	default:
		return p
	}
}

// EnsureDirs creates the config, data, cache, and registry directories.
func EnsureDirs() error {
	for _, d := range []string{ConfigDir(), DataDir(), CacheDir(), RegistryDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}
