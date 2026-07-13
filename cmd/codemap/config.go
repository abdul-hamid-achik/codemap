/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"fmt"

	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/spf13/cobra"
)

var (
	configCmd = &cobra.Command{
		Use:   "config",
		Short: "Inspect codemap configuration paths and resolved values",
	}
	configPathCmd = &cobra.Command{
		Use:   "path",
		Short: "Print the resolved config file path (global or CODEMAP_CONFIG override)",
		RunE:  runConfigPath,
	}
	configShowCmd = &cobra.Command{
		Use:   "show",
		Short: "Print the resolved configuration values",
		RunE:  runConfigShow,
	}
)

func init() {
	configCmd.AddCommand(configPathCmd, configShowCmd)
}

func runConfigPath(cmd *cobra.Command, _ []string) error {
	path := config.ConfigFile()
	if jsonOut(cmd) {
		return printJSON(map[string]string{"config_file": path})
	}
	fmt.Println(path)
	return nil
}

func runConfigShow(cmd *cobra.Command, _ []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()
	// Never print the raw API key: show() renders a masked COPY of the config
	// (real key value is never marshaled or formatted, in JSON or text).
	shown := *sess.Config
	shown.Embedding.APIKey = maskSecret(sess.Config.Embedding.APIKey)
	cfg := &shown
	if jsonOut(cmd) {
		return printJSON(cfg)
	}
	fmt.Printf("config_file: %s\n", config.ConfigFile())
	fmt.Printf("embedding:\n  provider: %s\n  model: %s\n  ollama_url: %s\n  api_key: %s\n  dimensions: %d\n  distance: %s\n",
		cfg.Embedding.Provider, cfg.Embedding.Model, cfg.Embedding.OllamaURL, cfg.Embedding.APIKey, cfg.Embedding.Dimensions, cfg.Embedding.Distance)
	fmt.Printf("semantic:\n  backend: %s\n  fusion: %s\n", cfg.Semantic.Backend, cfg.Semantic.Fusion)
	fmt.Printf("index:\n  max_file_bytes: %d\n  embed_batch_size: %d\n  embed_concurrency: %d\n  extract_concurrency: %d\n  embed_max_chars: %d\n",
		cfg.Index.MaxFileBytes, cfg.Index.EmbedBatchSize, cfg.Index.EmbedConcurrency, cfg.Index.ExtractConcurrency, cfg.Index.EmbedMaxChars)
	fmt.Printf("daemon:\n  debounce_ms: %d\n  idle_timeout_min: %d\n  precise: %t\n  embed_rps: %.0f\n  embed_max_in_flight: %d\n  embed_cache_size: %d\n",
		cfg.Daemon.DebounceMS, cfg.Daemon.IdleTimeoutMin, cfg.Daemon.Precise, cfg.Daemon.EmbedRPS, cfg.Daemon.EmbedMaxInFlight, cfg.Daemon.EmbedCacheSize)
	if len(cfg.Index.Exclude) > 0 {
		fmt.Printf("exclude: %v\n", cfg.Index.Exclude)
	}
	if len(cfg.Index.ExcludeExtra) > 0 {
		fmt.Printf("exclude_extra: %v\n", cfg.Index.ExcludeExtra)
	}
	return nil
}

// maskSecret redacts a secret for display: empty stays empty (no key
// configured — today's default), a short value that would be fully exposed by
// a 4-char suffix collapses to "(set)", and anything longer keeps only its
// last 4 characters behind "****" so a user can recognize which key is active
// without the full value ever reaching stdout, logs, or `--json`.
func maskSecret(key string) string {
	switch {
	case key == "":
		return ""
	case len(key) <= 8:
		return "(set)"
	default:
		return "****" + key[len(key)-4:]
	}
}
