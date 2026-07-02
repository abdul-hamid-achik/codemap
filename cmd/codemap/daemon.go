/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/daemon"
	"github.com/abdul-hamid-achik/codemap/internal/embed"
)

var (
	daemonCmd = &cobra.Command{
		Use:   "daemon",
		Short: "Background indexer: watch the working tree and keep the index fresh (start/stop/status)",
	}
	daemonStartCmd = &cobra.Command{
		Use:   "start [path]",
		Short: "Run the daemon in the foreground (watches the project and incrementally re-indexes)",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runDaemonStart,
	}
	daemonStopCmd = &cobra.Command{
		Use:   "stop",
		Short: "Stop the running daemon",
		RunE:  runDaemonStop,
	}
	daemonStatusCmd = &cobra.Command{
		Use:   "status",
		Short: "Show whether the daemon is running and what it's watching",
		RunE:  runDaemonStatus,
	}
)

func init() {
	daemonStartCmd.Flags().Bool("no-embed", false, "watch + index structure only (skip Ollama embeddings)")
	daemonCmd.AddCommand(daemonStartCmd, daemonStopCmd, daemonStatusCmd)
}

func runDaemonStart(cmd *cobra.Command, args []string) error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	if len(args) > 0 {
		root = args[0]
	}
	return startDaemonForeground(cmd, root)
}

// startDaemonForeground starts the daemon in the current process and blocks
// until it exits. Shared by `codemap daemon start` and `codemap index --watch`
// so the config flag application and foreground lifecycle can't drift.
func startDaemonForeground(cmd *cobra.Command, root string) error {
	cfgPath, _ := cmd.Flags().GetString("config")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	applyConfigFlags(cmd, cfg) // flags win over config file + env
	noEmbed, _ := cmd.Flags().GetBool("no-embed")
	dc := cfg.Daemon
	d, err := daemon.Start(cmd.Context(), root, daemon.Config{
		Debounce:    time.Duration(dc.DebounceMS) * time.Millisecond,
		IdleTimeout: time.Duration(dc.IdleTimeoutMin) * time.Minute,
		Throttle: embed.ThrottleConfig{
			RPS:         dc.EmbedRPS,
			MaxInFlight: dc.EmbedMaxInFlight,
			CacheSize:   dc.EmbedCacheSize,
		},
		NoEmbed:   noEmbed,
		Overrides: func(c *config.Config) { applyConfigFlags(cmd, c) },
	})
	if err != nil {
		return err
	}
	fmt.Printf("\ncodemap daemon started (pid %d), watching %s\n  socket: %s\n  stop with: codemap daemon stop  (or Ctrl-C)\n",
		os.Getpid(), root, config.DaemonSocketPath())

	// Clean shutdown on Ctrl-C / SIGTERM.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() { <-sig; d.Stop() }()

	d.Wait()
	fmt.Println("codemap daemon stopped")
	return nil
}

func runDaemonStatus(cmd *cobra.Command, _ []string) error {
	resp, err := daemonRequest("daemon.status")
	if err != nil {
		if jsonOut(cmd) {
			return printJSON(map[string]any{"running": false, "error": err.Error()})
		}
		fmt.Println("codemap daemon: not running")
		return nil
	}
	if jsonOut(cmd) {
		fmt.Println(resp)
		return nil
	}
	fmt.Printf("codemap daemon: running\n  %s\n", resp)
	return nil
}

func runDaemonStop(cmd *cobra.Command, _ []string) error {
	if _, err := daemonRequest("daemon.shutdown"); err != nil {
		if jsonOut(cmd) {
			return printJSON(map[string]any{"running": false, "stopped": false, "error": err.Error()})
		}
		fmt.Println("codemap daemon: not running")
		return nil
	}
	if jsonOut(cmd) {
		return printJSON(map[string]bool{"running": false, "stopped": true})
	}
	fmt.Println("codemap daemon: stopping")
	return nil
}

// daemonRequest sends one control method to the daemon's unix socket and returns
// its single-line JSON response.
func daemonRequest(method string) (string, error) {
	c, err := net.DialTimeout("unix", config.DaemonSocketPath(), time.Second)
	if err != nil {
		return "", err
	}
	defer c.Close()
	if _, err := fmt.Fprintf(c, "{\"method\":%q}\n", method); err != nil {
		return "", err
	}
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	sc := bufio.NewScanner(c)
	if !sc.Scan() {
		return "", fmt.Errorf("no response from daemon")
	}
	return sc.Text(), nil
}
