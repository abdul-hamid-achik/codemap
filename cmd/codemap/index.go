/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/app"
	"github.com/abdul-hamid-achik/codemap/internal/daemon"
	"github.com/abdul-hamid-achik/codemap/internal/index"
	"github.com/spf13/cobra"
)

var indexCmd = &cobra.Command{
	Use:   "index [paths...]",
	Short: "Index a project: extract the graph and embed nodes (incremental)",
	RunE:  runIndex,
}

func runIndex(cmd *cobra.Command, _ []string) error {
	// --via-vault re-runs this index inside `tvault run` so the language servers
	// (gopls/pyright/tsserver) see the project's registry creds. Handle it before
	// opening a session — the real index happens in the re-exec'd child.
	if vault, _ := cmd.Flags().GetString("via-vault"); vault != "" {
		if done, err := indexViaVault(cmd, vault); done {
			return err
		}
	}
	// If a background daemon already owns the writable handle, delegate the
	// reindex to it over the control socket. Opening a second write session
	// here would collide with the daemon's exclusive veclite lock (the
	// "database file is locked by PID ..." error). Delegating keeps the same
	// output and forwards reindex/precise/no-lsp/no-embed flags.
	//
	// Pin P0-08: the daemon's socket is global per data dir, not per-project,
	// so without a project-identity check here we'd silently reindex the
	// daemon's own project instead of the user's cwd. Refuse to delegate
	// when cwd is not inside the daemon's project root.
	if info := daemon.QueryStatus(); info != nil {
		cwd, _ := os.Getwd()
		if ok, reason := daemon.DelegationAllowed(cwd, info); !ok {
			return fmt.Errorf("%s", reason)
		}
		return indexViaDaemon(cmd, info)
	}
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	reindex, _ := cmd.Flags().GetBool("reindex")
	noEmbed, _ := cmd.Flags().GetBool("no-embed")
	precise, _ := cmd.Flags().GetBool("precise")
	noLSP, _ := cmd.Flags().GetBool("no-lsp")
	opts := index.Options{Reindex: reindex, Precise: precise, NoLSP: noLSP}
	svc := app.NewService(sess)

	// Auto-restore from fcheap cache before a costly --reindex: if a matching
	// cache entry exists (same tree hash + embedding profile), restore it and
	// skip the full wipe+re-extract+re-embed cycle entirely. When --precise is
	// requested, only accept a cache entry that already has precise edges —
	// otherwise the restore would skip the go/types pass the user asked for.
	doCache, _ := cmd.Flags().GetBool("cache")
	if reindex && doCache && svc.CacheFcheapAvailable() {
		restored, crep := svc.MaybeRestoreBeforeReindex(context.Background(), cwd)
		if restored && precise && !svc.RestoredCacheHasPrecise(cwd) {
			restored = false // cache has no precise edges; let reindex run
		}
		if restored {
			if jsonOut(cmd) {
				return printJSON(crep)
			}
			fmt.Printf("index restored from fcheap cache: stash %s (tree %s)\n", crep.StashID, shortHash(crep.TreeHash, 12))
			return nil
		}
	}

	// Live progress bar only for an interactive `codemap index` (TTY, no --json).
	// Under --json, MCP, studio reindex, or a pipe, opts.OnFile stays nil and
	// indexing runs exactly as before — no bar, no stdout noise.
	var rep *app.IndexReport
	if !jsonOut(cmd) && isInteractiveTTY() {
		rep, err = runIndexWithBar(cmd.Context(), svc, cwd, opts, !noEmbed)
	} else {
		rep, err = svc.Index(cmd.Context(), cwd, opts, !noEmbed)
	}
	if err != nil {
		return err
	}

	// Pin P0-10: side effects (cache auto-save + --watch daemon handoff) run
	// BEFORE the JSON-or-human output branch, so --json --watch and --json
	// alone both still save the cache and start the daemon — the prior early
	// `return printJSON(rep)` skipped them silently.
	//
	// Session ownership also moves: the session's veclite writer is closed
	// BEFORE startDaemonAfterIndex runs, so the daemon's own writer can open
	// without colliding on the exclusive flock (the deferred sess.Close()
	// ran only after runIndex returned, breaking `--watch`).
	envelope := buildIndexEnvelope(rep)
	if doCache && svc.CacheFcheapAvailable() {
		if crep := svc.MaybeCacheAfterIndex(context.Background(), cwd); crep != nil {
			envelope.Cache = crep
		}
	}
	watch, _ := cmd.Flags().GetBool("watch")
	if watch {
		// Release the CLI's exclusive veclite handle so the daemon's writer
		// can open. Session.Close is idempotent (the deferred Close below
		// is a safe no-op once fields are nil).
		_ = sess.Close()
		if dErr := startDaemonAfterIndex(cmd); dErr != nil {
			return dErr
		}
		envelope.Daemon = &indexDaemonInfo{Started: true, PID: os.Getpid()}
	}

	if jsonOut(cmd) {
		return printJSON(envelope)
	}
	printIndexReport(cmd, rep, precise)
	if watch {
		// Watch already handed off above; startDaemonAfterIndex prints its
		// own banner — nothing more to render here.
		return nil
	}
	if envelope.Cache != nil && envelope.Cache.Action == "saved" {
		fmt.Printf("  cache: saved to fcheap (stash %s, tree %s)\n", envelope.Cache.StashID, shortHash(envelope.Cache.TreeHash, 12))
	}

	if noTips, _ := cmd.Flags().GetBool("no-tips"); !noTips {
		fmt.Println("  tip: run 'codemap daemon start' (or 'codemap index --watch') to keep the index fresh automatically")
	}
	return nil
}

// indexDaemonInfo is the small "daemon started" detail block that the CLI
// surfaces in --json output (P0-10). Absent when --watch is off or the
// handoff failed.
type indexDaemonInfo struct {
	Started bool `json:"started"`
	PID     int  `json:"pid,omitempty"`
}

// indexEnvelope wraps the Service.Index report in a CLI-only JSON envelope
// that carries the side-effect outcomes (cache + daemon). Empty fields stay
// nil so the --json shape stays forward-compatible with what an agent parses.
type indexEnvelope struct {
	*app.IndexReport
	Cache  *app.CacheReport `json:"cache,omitempty"`
	Daemon *indexDaemonInfo `json:"daemon,omitempty"`
}

func buildIndexEnvelope(rep *app.IndexReport) *indexEnvelope {
	return &indexEnvelope{IndexReport: rep}
}

// indexViaVault re-execs `codemap index` inside `tvault run -p <project>` so the
// language servers spawned by the precise pass inherit the project's secrets
// (private-registry creds: GOPRIVATE/NPM_TOKEN/PIP_INDEX_TOKEN). Returns done=true
// when it handled the index (the re-exec'd child did the work); done=false to fall
// through to a normal in-process index when tvault isn't installed. The ONLY tvault
// subcommand it ever runs is `run` — no value-reading verb (`tvault get`) is
// reachable from here, so secret VALUES can never enter codemap.
func indexViaVault(cmd *cobra.Command, project string) (done bool, err error) {
	tvault, lookErr := exec.LookPath("tvault")
	if lookErr != nil {
		fmt.Fprintln(os.Stderr, "warning: --via-vault set but 'tvault' is not on PATH; indexing without injected secrets")
		return false, nil // fall through to a normal index
	}
	self, exeErr := os.Executable()
	if exeErr != nil {
		self = "codemap"
	}
	// Reconstruct the inner `codemap index`, dropping --via-vault (no recursion).
	inner := []string{self, "index"}
	for _, f := range []string{"reindex", "no-embed", "no-lsp", "precise"} {
		if b, _ := cmd.Flags().GetBool(f); b {
			inner = append(inner, "--"+f)
		}
	}
	if cfg, _ := cmd.Flags().GetString("config"); cfg != "" {
		inner = append(inner, "--config", cfg)
	}
	if j, _ := cmd.Flags().GetBool("json"); j {
		inner = append(inner, "--json")
	}
	args := append([]string{"run", "-p", project, "--"}, inner...)
	c := exec.CommandContext(cmd.Context(), tvault, args...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return true, c.Run()
}

// printIndexReport renders the human-facing summary of an IndexReport (the
// "Indexed ..." block through errors/oversized). Shared by the local index
// path and the daemon-delegated path so output is identical regardless of
// which process did the work. JSON output is handled by the caller.
func printIndexReport(cmd *cobra.Command, rep *app.IndexReport, precise bool) {
	if rep.Warning != "" {
		fmt.Fprintf(os.Stderr, "warning: %s\n", rep.Warning)
	}
	fmt.Printf("Indexed %q (%s)\n", rep.Project, rep.Root)
	fmt.Println(indexFilesSummary(rep))
	fmt.Printf("  graph: %d nodes, %d edges (embeddings: %v)\n", rep.Nodes, rep.Edges, rep.Embedded)
	if rep.TotalMs > 0 {
		fmt.Printf("  time: %s", formatDuration(rep.TotalMs))
		parts := make([]string, 0, 3)
		if rep.ExtractMs > 0 {
			parts = append(parts, "extract "+formatDuration(rep.ExtractMs))
		}
		if rep.PreciseMs > 0 {
			parts = append(parts, "precise "+formatDuration(rep.PreciseMs))
		}
		if rep.EmbedMs > 0 {
			parts = append(parts, "embed "+formatDuration(rep.EmbedMs))
		}
		if len(parts) > 0 {
			fmt.Printf(" (%s)", strings.Join(parts, ", "))
		}
		fmt.Println()
	}
	if precise {
		if rep.PreciseNote != "" {
			fmt.Printf("  precise: %s\n", rep.PreciseNote)
		} else {
			// "skipped" are calls go/types DID resolve but whose target isn't an
			// indexed node (stdlib/external functions) — not precision failures, so
			// don't call them "unresolved" (that wrongly implies the pass fell short).
			fmt.Printf("  precise: %d call edges resolved exactly (%d to external/stdlib code)\n", rep.PreciseUpgraded, rep.PreciseSkipped)
		}
	} else {
		// Surface --precise at the moment a user would most benefit: it refines Go's
		// name-based edges, and it's the ONLY source of a call graph for the LSP
		// languages (a TS/JS/Python project has no callers/impact without it).
		noTips, _ := cmd.Flags().GetBool("no-tips")
		if !noTips {
			goAvailable := false
			if _, lookErr := exec.LookPath("go"); lookErr == nil {
				goAvailable = true
			}
			for _, tip := range preciseTips(rep.Languages, goAvailable) {
				fmt.Println("  tip: " + tip)
			}
		}
	}
	for _, e := range rep.Errors {
		fmt.Fprintf(os.Stderr, "  ! %s: %s\n", e.File, e.Err)
	}
	for _, f := range rep.Oversized {
		fmt.Fprintf(os.Stderr, "  ~ %s: skipped — exceeds index.max_file_bytes (raise it to include this file)\n", f)
	}
}

// indexViaDaemon delegates a reindex to an already-running daemon and renders
// the result exactly like a local `codemap index`. Used when `codemap index` is
// run while a daemon owns the writable handle, so the CLI never opens a second
// write session (which would collide with the daemon's exclusive veclite lock).
// Forwards --reindex/--precise/--no-lsp/--no-embed. --exclude-extra is NOT
// forwarded (the daemon's excludes are fixed at start); stop + restart the
// daemon to change them. --watch is a no-op (the daemon is already watching).
func indexViaDaemon(cmd *cobra.Command, info *daemon.Info) error {
	reindex, _ := cmd.Flags().GetBool("reindex")
	precise, _ := cmd.Flags().GetBool("precise")
	noLSP, _ := cmd.Flags().GetBool("no-lsp")
	noEmbed, _ := cmd.Flags().GetBool("no-embed")
	opts := daemon.ReindexOpts{Reindex: reindex, Precise: precise, NoLSP: noLSP}
	if cmd.Flags().Changed("no-embed") {
		v := !noEmbed
		opts.Embed = &v
	}
	rep, err := daemon.Reindex(opts)
	if err != nil {
		return fmt.Errorf("delegate to daemon (pid %d): %w\n  stop it with 'codemap daemon stop', then re-run", info.PID, err)
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	printIndexReport(cmd, rep, precise)
	fmt.Printf("  via daemon (pid %d)\n", info.PID)
	return nil
}

// indexFilesSummary renders the "files:" line of `codemap index`. Recognized
// files of a language with no available extractor (e.g. its language server isn't
// installed) were genuinely scanned and skipped, so they're folded into the
// scanned+skipped counts here — otherwise the summary would claim "0 skipped"
// while the warning above reports skipped files. (IndexReport keeps FilesScanned
// as extractable-only for the advisory; this reconciles the human view with it.)
func indexFilesSummary(rep *app.IndexReport) string {
	unsupported := 0
	for _, n := range rep.Unsupported {
		unsupported += n
	}
	// P2-07 (O108): render "N up-to-date" for unchanged files so a
	// clean incremental index doesn't read as failure ("112 skipped").
	// Reserve "skipped" for genuine skips (oversized/generated/errors).
	unchanged := rep.FilesUnchanged
	skipped := rep.FilesSkipped + unsupported
	line := fmt.Sprintf("  files: %d scanned, %d indexed", rep.FilesScanned+unsupported, rep.FilesIndexed)
	if unchanged > 0 {
		line += fmt.Sprintf(", %d up-to-date", unchanged)
	}
	if skipped > 0 {
		line += fmt.Sprintf(", %d skipped", skipped)
	}
	if rep.FilesDeleted > 0 {
		line += fmt.Sprintf(", %d removed", rep.FilesDeleted)
	}
	return line
}

// formatDuration renders milliseconds as a human-friendly duration string:
// "1.2s", "820ms", or "1m30s" for longer runs.
func formatDuration(ms int) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	sec := float64(ms) / 1000
	if sec < 60 {
		return fmt.Sprintf("%.1fs", sec)
	}
	m := ms / 60000
	s := (ms % 60000) / 1000
	return fmt.Sprintf("%dm%ds", m, s)
}

// startDaemonAfterIndex is the --watch path: after a successful one-shot
// index, hand off to the daemon so the index stays fresh. This is a thin
// alias — it reuses daemon.Start with the same config flags the index command
// already applied. The user gets a single "index then watch" flow.
func startDaemonAfterIndex(cmd *cobra.Command) error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	return startDaemonForeground(cmd, root)
}
