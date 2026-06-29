/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Command codemap is local-first code intelligence: a structural code graph
// (LSP + parsers) fused with semantic vector search (veclite), exposed via a
// CLI, an MCP server, and the studio TUI. See AGENTS.md for architecture.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/app"
	"github.com/abdul-hamid-achik/codemap/internal/version"
	"github.com/spf13/cobra"
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:     "codemap",
	Short:   "Local-first code intelligence: graph + vectors for agents and people",
	Version: version.Full(),
	// A runtime failure (bad config, not-indexed, …) shouldn't dump the full usage
	// block — that's noise for a human and pollutes stderr for an agent parsing the
	// error. cobra still prints the "Error: …" line; `--help` is there when wanted.
	SilenceUsage: true,
	Long: `codemap combines a structural code graph (LSP + parsers) with semantic
vector search (veclite) and exposes both as a unified query layer.

Three surfaces over one store: a CLI (with --json for agents), an MCP server
(codemap serve), and the interactive studio TUI (codemap studio).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !isInteractiveTerminal() {
			return cmd.Help()
		}
		return runStudio(cmd, args)
	},
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("codemap %s\n", version.Version)
		fmt.Printf("  commit: %s\n", version.Commit)
		fmt.Printf("  built:  %s\n", version.Date)
	},
}

// isInteractiveTerminal reports whether both stdin and stdout are TTYs.
func isInteractiveTerminal() bool {
	so, err1 := os.Stdout.Stat()
	si, err2 := os.Stdin.Stat()
	if err1 != nil || err2 != nil {
		return false
	}
	return so.Mode()&os.ModeCharDevice != 0 && si.Mode()&os.ModeCharDevice != 0
}

func init() {
	rootCmd.SetVersionTemplate("codemap version {{.Version}}\n")

	// Persistent flags shared by all commands.
	rootCmd.PersistentFlags().StringP("config", "c", "", "path to config file")
	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "verbose output")
	rootCmd.PersistentFlags().Bool("json", false, "emit machine-readable JSON (for agents)")

	indexCmd.Flags().Bool("reindex", false, "wipe and rebuild the whole project index")
	indexCmd.Flags().Bool("no-embed", false, "skip semantic embeddings (index structure only)")
	indexCmd.Flags().Bool("precise", false, "resolve call edges exactly (Go via go/types, needs the go toolchain; TypeScript/JavaScript/Python via callHierarchy) — eliminates same-named over-matching and gives the LSP languages a call graph")
	indexCmd.Flags().Bool("no-lsp", false, "skip language-server-backed extraction (e.g. TypeScript via typescript-language-server)")
	indexCmd.Flags().Bool("watch", false, "after indexing, start the daemon to keep the index fresh automatically (same as 'codemap daemon start')")
	indexCmd.Flags().String("via-vault", "", "re-run indexing inside `tvault run -p <project>` so registry creds (GOPRIVATE/NPM_TOKEN/…) reach the language servers")
	indexCmd.Flags().Bool("cache", true, "save/restore the index to/from the fcheap stash vault (best-effort; auto-restore before --reindex, auto-save after index)")
	indexCmd.Flags().Bool("no-tips", false, "suppress post-index advisory tips (useful in scripts/CI)")
	initCmd.Flags().Bool("local", false, "drop a .codemap marker (so a repo-local codemap.yaml is found; index stays central)")
	callersCmd.Flags().Bool("lsp", false, "use the language server (gopls) for precise callers (Go)")
	calleesCmd.Flags().Bool("lsp", false, "use the language server (gopls) for precise callees (Go)")
	impactCmd.Flags().Int("depth", 3, "max hops for the blast radius")
	impactCmd.Flags().String("at", "", "resolve the symbol from a position instead of a name: <file>:<line>")
	reviewCmd.Flags().Int("depth", 3, "max hops for each changed symbol's blast radius")
	reviewCmd.Flags().String("since", "", "review everything changed since this git ref (committed + uncommitted)")
	reviewCmd.Flags().Bool("staged", false, "review only staged changes (the git index) instead of the whole working tree")
	readOrderCmd.Flags().Int("top", 20, "maximum entries to rank")
	fileImpactCmd.Flags().Int("depth", 3, "max hops for the file's blast radius")
	riskCmd.Flags().Int("depth", 3, "max hops for the fan-in/blast analysis")
	secretImpactCmd.Flags().Int("depth", 3, "max hops for each key's blast radius")
	secretImpactCmd.Flags().String("via-vault", "", "fetch the key NAMES from `tvault -p <project> list` (value-free) instead of passing them")
	secretImpactCmd.Flags().String("prefix", "", "with --via-vault, only keys with this prefix (e.g. STRIPE_)")
	requiredKeysCmd.Flags().Int("depth", 5, "max hops of the entrypoint's callee closure")
	requiredKeysCmd.Flags().StringSlice("keys", nil, "candidate key names to test (e.g. STRIPE_KEY,DATABASE_URL)")
	requiredKeysCmd.Flags().String("via-vault", "", "use all of `tvault -p <project> list` as the candidate keys (value-free)")
	requiredKeysCmd.Flags().String("prefix", "", "with --via-vault, restrict candidates to this prefix")
	contextCmd.Flags().Int("depth", 3, "max hops for the blast-radius count")
	semanticCmd.Flags().Int("top", 10, "maximum results")
	hotspotsCmd.Flags().Int("top", 20, "maximum results")
	orphansCmd.Flags().Int("top", 50, "maximum results")
	findCmd.Flags().Int("top", 50, "maximum results")
	annotateCmd.Flags().String("source", "note", "annotation producer (ecosystem convention): note, vecgrep, tinyvault, fcheap, vidtrace, cairntrace, glyphrun, mongosh, postgres")
	annotateCmd.Flags().String("note", "", "free-form note text")
	annotateCmd.Flags().String("data", "", "opaque data payload (e.g. JSON from a DB query)")
	annotationsCmd.Flags().Int64("rm", 0, "remove the annotation with this id")
	branchSwitchCmd.Flags().String("from", "", "branch being left (default: the last active branch)")
	branchSwitchCmd.Flags().String("to", "", "branch to switch to (default: the current git branch)")
	branchSwitchCmd.Flags().String("root", "", "repository root (default: cwd)")
	branchSwitchCmd.Flags().Bool("install-hook", false, "install a git post-checkout hook that auto-switches the index on every branch checkout")
	branchSnapshotCmd.Flags().String("branch", "", "branch to snapshot (default: the current git branch)")
	branchSnapshotCmd.Flags().String("root", "", "repository root (default: cwd)")

	// Per-setting override flags (config file < env < flag) for every tunable.
	registerConfigFlags(rootCmd, indexCmd, daemonStartCmd)

	rootCmd.AddCommand(versionCmd, initCmd, indexCmd, statusCmd, doctorCmd, serveCmd, studioCmd,
		callersCmd, calleesCmd, impactCmd, reviewCmd, readOrderCmd, relatedFilesCmd, fileImpactCmd, riskCmd, symbolAtCmd, secretImpactCmd, requiredKeysCmd, semanticCmd, hotspotsCmd, orphansCmd, pathCmd, symbolsCmd, findCmd, sourceCmd, contextCmd, projectsCmd, docsCmd,
		annotateCmd, annotationsCmd, branchStatusCmd, branchSwitchCmd, branchSnapshotCmd, daemonCmd)
}

// --- shared helpers (used across command files) ---

// truncStr shortens s to at most n runes (for fixed-width columns).
func truncStr(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return "…"
	}
	return string(r[:n-1]) + "…"
}

// disp prefers the fully-qualified name so same-named symbols are distinguishable.
func disp(fqn, symbol string) string {
	if fqn != "" {
		return fqn
	}
	return symbol
}

// sigOrName shows the signature (which includes the name and parameters) when
// available, falling back to the qualified name — so list commands read like a
// file outline rather than a bare list of names.
func sigOrName(signature, fqn, symbol string) string {
	if s := strings.TrimSpace(signature); s != "" {
		return s
	}
	return disp(fqn, symbol)
}

// capList returns the first n items of xs (all, if fewer) and how many were
// elided, so callers can print a "… (N more)" line.
func capList[T any](xs []T, n int) (shown []T, more int) {
	if len(xs) <= n {
		return xs, 0
	}
	return xs[:n], len(xs) - n
}

func openSession(cmd *cobra.Command) (*app.Session, error) {
	cfgPath, _ := cmd.Flags().GetString("config")
	sess, err := app.Open(cfgPath)
	if err != nil {
		return nil, err
	}
	applyConfigFlags(cmd, sess.Config) // flags win over config file + env
	return sess, nil
}

func jsonOut(cmd *cobra.Command) bool {
	v, _ := cmd.Flags().GetBool("json")
	return v
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	// CLI/agent JSON has no HTML context, so keep <, >, & literal instead of
	// </>/& — cleaner to read and grep (e.g. a path target "A -> B",
	// or TypeScript generics like Array<string> in a signature).
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// preciseTips returns the "add --precise" hints shown after a non-precise index,
// tailored to the languages present: Go's name-based edges can be made exact,
// while the LSP languages (TypeScript/JavaScript/Python) have no call graph at
// all without --precise — so a new user isn't left with empty `callers`/`impact`
// and no idea why. A language only appears here when its files were indexed,
// which means its server was present, so the tip is always actionable.
func preciseTips(languages map[string]int, goAvailable bool) []string {
	var tips []string
	if languages["go"] > 0 && goAvailable {
		tips = append(tips, "Go call edges are name-based; add --precise to resolve them exactly (eliminates same-named over-matching)")
	}
	var lsp []string
	for _, l := range []string{"typescript", "javascript", "python"} {
		if languages[l] > 0 {
			lsp = append(lsp, l)
		}
	}
	if len(lsp) > 0 {
		tips = append(tips, "no call graph for "+strings.Join(lsp, "/")+" yet — add --precise for callers/impact/hotspots/path")
	}
	return tips
}

// preciseEdgeNote renders the parenthetical after the edge count in `status`,
// engine-aware so it never lies: precise edges come from go/types for Go but
// callHierarchy for TypeScript, and a TS project without --precise has *no* call
// edges at all (not "name-based" — TS has no name-based call resolution).
func preciseEdgeNote(preciseEdges int, languages map[string]int) string {
	hasGo := languages["go"] > 0
	hasTS := languages["typescript"] > 0
	if preciseEdges > 0 {
		switch {
		case hasGo && hasTS:
			return fmt.Sprintf(" (%d precise: go/types + callHierarchy)", preciseEdges)
		case hasTS:
			return fmt.Sprintf(" (%d precise via callHierarchy)", preciseEdges)
		default:
			return fmt.Sprintf(" (%d precise via go/types)", preciseEdges)
		}
	}
	if hasTS && !hasGo {
		// TypeScript has no name-based call edges — --precise is the only source.
		return " (no call graph yet; run 'codemap index --precise' to resolve TypeScript calls)"
	}
	return " (name-based; run 'codemap index --precise' for exact call edges)"
}

func formatCounts(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, m[k]))
	}
	return strings.Join(parts, " ")
}

// Caps for the human-facing `impact` lists. A hub can have hundreds of
// dependents; dumping them all floods the terminal. The blast radius is
// depth-ordered (BFS), so the cap keeps the nearest — most relevant — nodes.
// `--json` always carries the complete set for agents/scripts.
const (
	impactTestsCap = 10
	impactBlastCap = 20
	// relationsDisplayCap bounds the human-facing callers/callees lists. Most
	// symbols have a handful, but a same-named query (e.g. `callers Close`) merges
	// every definition and can run to 100+ rows; the ambiguity ⚠ already explains
	// it. --json stays complete.
	relationsDisplayCap = 40
)
