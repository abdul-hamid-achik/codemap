package main

import (
	"time"

	"github.com/spf13/cobra"

	"github.com/abdul-hamid-achik/codemap/internal/config"
)

// registerConfigFlags wires CLI flags for every config-file/env-backed setting,
// so each knob is reachable three ways (config file < env < flag). Embedding flags
// are persistent (they apply wherever codemap embeds: index, serve, semantic);
// index and daemon flags live on the commands that index. Defaults mirror the
// config defaults for readable --help, but behavior is gated on .Changed, so an
// unset flag never overrides a config-file or env value.
func registerConfigFlags(root, index, daemonStart *cobra.Command) {
	pf := root.PersistentFlags()
	pf.String("embed-provider", "ollama", "embedding provider: ollama, openai, cohere, voyage")
	pf.String("embed-model", "nomic-embed-text", "embedding model")
	pf.String("ollama-url", "http://localhost:11434", "Ollama base URL")
	pf.Int("embed-dimensions", 768, "embedding vector dimension")
	pf.String("embed-distance", "cosine", "vector distance: cosine, dot, euclidean")

	for _, c := range []*cobra.Command{index, daemonStart} {
		c.Flags().StringSlice("exclude-extra", nil, "extra path globs to skip, appended to the configured excludes (e.g. migrations,db/migrations,**/testdata)")
	}
	index.Flags().StringSlice("exclude", nil, "path globs to skip, REPLACING the built-in defaults (.git, node_modules, …)")
	index.Flags().Int("max-file-bytes", 1<<20, "skip files larger than this many bytes")
	index.Flags().Int("embed-batch-size", 64, "node texts sent to the embedder per request")
	index.Flags().Int("embed-concurrency", 4, "concurrent embedding requests (big win for network providers)")
	index.Flags().Int("embed-max-chars", 0, "cap per-node embed text (0 = no cap); lower = faster reindex, less body recall")

	df := daemonStart.Flags()
	df.Duration("debounce", 500*time.Millisecond, "coalesce a burst of file edits within this window into one reindex")
	df.Duration("idle-timeout", 0, "shut the daemon down after this much inactivity (0 = never)")
	df.Float64("embed-rps", 0, "background embedding rate limit to Ollama, requests/sec (0 = unlimited)")
	df.Int("embed-max-in-flight", 2, "max concurrent background embedding calls")
	df.Int("embed-cache-size", 4096, "embedding dedup cache size (entries)")
}

// applyConfigFlags overlays explicitly-set CLI flags onto cfg, giving flags the
// highest precedence (above config files and env). Only flags present on cmd and
// changed by the user are applied, so a command lacking a given flag is unaffected.
func applyConfigFlags(cmd *cobra.Command, cfg *config.Config) {
	fs := cmd.Flags()
	changed := func(name string) bool {
		f := fs.Lookup(name)
		return f != nil && f.Changed
	}
	if changed("embed-provider") {
		cfg.Embedding.Provider, _ = fs.GetString("embed-provider")
	}
	if changed("embed-model") {
		cfg.Embedding.Model, _ = fs.GetString("embed-model")
	}
	if changed("ollama-url") {
		cfg.Embedding.OllamaURL, _ = fs.GetString("ollama-url")
	}
	if changed("embed-dimensions") {
		cfg.Embedding.Dimensions, _ = fs.GetInt("embed-dimensions")
	}
	if changed("embed-distance") {
		cfg.Embedding.Distance, _ = fs.GetString("embed-distance")
	}
	if changed("exclude") {
		cfg.Index.Exclude, _ = fs.GetStringSlice("exclude")
	}
	if changed("exclude-extra") {
		v, _ := fs.GetStringSlice("exclude-extra")
		cfg.Index.ExcludeExtra = append(cfg.Index.ExcludeExtra, v...)
	}
	if changed("max-file-bytes") {
		cfg.Index.MaxFileBytes, _ = fs.GetInt("max-file-bytes")
	}
	if changed("embed-batch-size") {
		cfg.Index.EmbedBatchSize, _ = fs.GetInt("embed-batch-size")
	}
	if changed("embed-concurrency") {
		cfg.Index.EmbedConcurrency, _ = fs.GetInt("embed-concurrency")
	}
	if changed("embed-max-chars") {
		cfg.Index.EmbedMaxChars, _ = fs.GetInt("embed-max-chars")
	}
	if changed("debounce") {
		d, _ := fs.GetDuration("debounce")
		cfg.Daemon.DebounceMS = int(d / time.Millisecond)
	}
	if changed("idle-timeout") {
		d, _ := fs.GetDuration("idle-timeout")
		cfg.Daemon.IdleTimeoutMin = int(d / time.Minute)
	}
	if changed("embed-rps") {
		cfg.Daemon.EmbedRPS, _ = fs.GetFloat64("embed-rps")
	}
	if changed("embed-max-in-flight") {
		cfg.Daemon.EmbedMaxInFlight, _ = fs.GetInt("embed-max-in-flight")
	}
	if changed("embed-cache-size") {
		cfg.Daemon.EmbedCacheSize, _ = fs.GetInt("embed-cache-size")
	}
}
