/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"fmt"
	"os"

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
	rootCmd.AddCommand(configCmd)
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
	cfgPath, _ := cmd.Flags().GetString("config")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(cfg)
	}
	fmt.Printf("config_file: %s\n", config.ConfigFile())
	fmt.Printf("embedding:\n  provider: %s\n  model: %s\n  ollama_url: %s\n  dimensions: %d\n  distance: %s\n",
		cfg.Embedding.Provider, cfg.Embedding.Model, cfg.Embedding.OllamaURL, cfg.Embedding.Dimensions, cfg.Embedding.Distance)
	fmt.Printf("index:\n  max_file_bytes: %d\n  embed_batch_size: %d\n  embed_concurrency: %d\n  extract_concurrency: %d\n  embed_max_chars: %d\n",
		cfg.Index.MaxFileBytes, cfg.Index.EmbedBatchSize, cfg.Index.EmbedConcurrency, cfg.Index.ExtractConcurrency, cfg.Index.EmbedMaxChars)
	fmt.Printf("daemon:\n  debounce_ms: %d\n  idle_timeout_min: %d\n  embed_rps: %.0f\n  embed_max_in_flight: %d\n  embed_cache_size: %d\n",
		cfg.Daemon.DebounceMS, cfg.Daemon.IdleTimeoutMin, cfg.Daemon.EmbedRPS, cfg.Daemon.EmbedMaxInFlight, cfg.Daemon.EmbedCacheSize)
	if len(cfg.Index.Exclude) > 0 {
		fmt.Printf("exclude: %v\n", cfg.Index.Exclude)
	}
	if len(cfg.Index.ExcludeExtra) > 0 {
		fmt.Printf("exclude_extra: %v\n", cfg.Index.ExcludeExtra)
	}
	return nil
}

// ensure configCmd is registered in main.go's rootCmd.AddCommand list.
// This is invoked by init() above; the command is auto-discovered by Cobra.
func _() { _ = os.Getenv("") }
