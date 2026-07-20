/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Command codemap is local-first code intelligence: a structural code graph
// (LSP + parsers) fused with semantic vector search (veclite), exposed via a
// CLI, an MCP server, and the studio TUI. See AGENTS.md for architecture.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/app"
	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/version"
	"github.com/spf13/cobra"
)

// P2-06 exit-code contract:
//
//	0 = answered (results, possibly empty-but-resolved like "no callers")
//	1 = operational error (bad flag, git failure, DB error)
//	2 = not found / not indexed (a valid query with no answer)
//
// Scripts/agents can distinguish "answered" from "dead end" without parsing output.
var (
	errNotFound   = errors.New("not found")
	errNotIndexed = errors.New("not indexed")
)

// targetDir returns the project directory the command should operate on:
// the --path / -C flag value when set, otherwise os.Getwd(). P2-05:
// every command uses this instead of a bare os.Getwd() so the CLI can
// target a project the way MCP tools do (uniform 'path' param).
func targetDir(cmd *cobra.Command) string {
	p, _ := cmd.Root().PersistentFlags().GetString("path")
	if p != "" {
		p = config.ExpandPath(p)
		if abs, err := filepath.Abs(p); err == nil {
			return abs
		}
		return p
	}
	d, _ := os.Getwd()
	return d
}

// targetDirArg gives an optional positional project path the same semantics as
// -C. An explicitly-set -C wins when both are supplied; otherwise the positional
// path wins, then cwd. Commands with a historical [path] argument (index,
// studio, daemon start, branch-status) use this helper consistently.
func targetDirArg(cmd *cobra.Command, args []string) string {
	if pathFlagChanged(cmd) || len(args) == 0 || args[0] == "" {
		return targetDir(cmd)
	}
	p := config.ExpandPath(args[0])
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

func pathFlagChanged(cmd *cobra.Command) bool {
	f := cmd.Root().PersistentFlags().Lookup("path")
	return f != nil && f.Changed
}

func init() {
	rootCmd.PersistentFlags().StringP("path", "C", "", "project directory (defaults to cwd; like MCP's uniform 'path' param)")
}

func main() {
	jsonRequested := jsonRequestedInArgs(os.Args[1:])
	if jsonRequested {
		// Unknown flags and Args validation fail before a RunE wrapper can set
		// SilenceErrors. Suppress Cobra's plain stderr so main can emit the same
		// stdout JSON envelope as handler failures.
		rootCmd.SilenceErrors = true
	}
	if err := rootCmd.Execute(); err != nil {
		// A --json failure already printed its structured envelope and carries
		// its exit code through exitCoded — honor it over the generic mapping.
		if code, ok := asExitCoded(err); ok {
			os.Exit(code)
		}
		if jsonRequested {
			os.Exit(jsonFailure(err).code)
		}
		// P2-06: map not-found / not-indexed to exit 2 so scripts
		// can distinguish a dead-end from an operational failure
		// (exit 1) or a successful answer (exit 0). Empty-but-
		// resolved (a real leaf with no callers) is still exit 0.
		if errors.Is(err, errNotFound) || errors.Is(err, errNotIndexed) {
			os.Exit(2)
		}
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
	// P2-05: global --path / -C flag so every command can target a project
	// directory the way MCP tools do (uniform 'path' param). Falls back to
	// os.Getwd() when absent, so backward compat is exact.
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
	// P1-09: --precise is the primary flag; --lsp is kept as a hidden alias
	// so existing agent scripts don't break. The capability drives
	// gopls (Go), typescript-language-server (TS/JS/Vue), or pyright
	// (Python) per the project's present languages.
	callersCmd.Flags().Bool("precise", false, "use the language server for precise callers (gopls / typescript-language-server / pyright, per the project's languages)")
	callersCmd.Flags().String("at", "", "select one definition by source position instead of merging a name: <file>:<line>")
	callersCmd.Flags().Bool("lsp", false, "(alias for --precise; kept for back-compat)")
	_ = callersCmd.Flags().MarkHidden("lsp")
	referencesCmd.Flags().String("at", "", "select one definition by source position instead of merging a name: <file>:<line>")
	calleesCmd.Flags().Bool("precise", false, "use the language server for precise callees (gopls / typescript-language-server / pyright, per the project's languages)")
	calleesCmd.Flags().String("at", "", "select one definition by source position instead of merging a name: <file>:<line>")
	calleesCmd.Flags().Bool("lsp", false, "(alias for --precise; kept for back-compat)")
	_ = calleesCmd.Flags().MarkHidden("lsp")
	impactCmd.Flags().Int("depth", 3, "max hops for the blast radius")
	impactCmd.Flags().String("at", "", "resolve the symbol from a position instead of a name: <file>:<line>")
	reviewCmd.Flags().Int("depth", 3, "max hops for each changed symbol's blast radius")
	reviewCmd.Flags().String("since", "", "review everything changed since this git ref (committed + uncommitted)")
	reviewCmd.Flags().Bool("staged", false, "review only staged changes (the git index) instead of the whole working tree")
	reviewCmd.Flags().String("fail-on-risk", "", "after printing the normal report, exit 6 if the aggregate risk level is at or above this threshold (low|medium|high); 'unknown' never trips it")
	reviewCmd.Flags().Bool("fail-on-untested", false, "after printing the normal report, exit 6 if any changed symbol has no covering test")
	readOrderCmd.Flags().Int("top", 20, "maximum entries to rank")
	fileImpactCmd.Flags().Int("depth", 3, "max hops for the file's blast radius")
	fileContextCmd.Flags().Int("depth", 3, "max hops for the file's blast radius")
	riskCmd.Flags().Int("depth", 3, "max hops for the fan-in/blast analysis")
	riskCmd.Flags().String("at", "", "select one definition by source position instead of merging a name: <file>:<line>")
	riskCmd.Flags().String("fail-on-risk", "", "after printing the normal report, exit 6 if the risk level is at or above this threshold (low|medium|high); 'unknown' never trips it")
	secretImpactCmd.Flags().Int("depth", 3, "max hops for each key's blast radius")
	secretImpactCmd.Flags().String("via-vault", "", "fetch the key NAMES from `tvault -p <project> list` (value-free) instead of passing them")
	secretImpactCmd.Flags().String("prefix", "", "with --via-vault, only keys with this prefix (e.g. STRIPE_)")
	requiredKeysCmd.Flags().Int("depth", 5, "max hops of the entrypoint's callee closure")
	requiredKeysCmd.Flags().StringSlice("keys", nil, "candidate key names to test (e.g. STRIPE_KEY,DATABASE_URL)")
	requiredKeysCmd.Flags().String("via-vault", "", "use all of `tvault -p <project> list` as the candidate keys (value-free)")
	requiredKeysCmd.Flags().String("prefix", "", "with --via-vault, restrict candidates to this prefix")
	contextCmd.Flags().Int("depth", 3, "max hops for the blast-radius count")
	contextCmd.Flags().StringArray("at", nil, "select definition(s) by source position (repeatable): <file>:<line> — pass several to batch exact definitions")
	contextCmd.Flags().Bool("brief", false, "drop each definition's source body, keeping signature/doc/location (source_omitted:true) — cheaper first look at a hub symbol; follow up with 'codemap source' for the body you actually need")
	semanticCmd.Flags().Int("top", 10, "maximum results")
	hotspotsCmd.Flags().Int("top", 20, "maximum results")
	orphansCmd.Flags().Int("top", 50, "maximum results")
	findCmd.Flags().Int("top", 50, "maximum results")
	grepCmd.Flags().Bool("regex", false, "interpret <pattern> as a Go RE2 regular expression instead of a literal substring")
	grepCmd.Flags().BoolP("ignore-case", "i", false, "case-insensitive match")
	grepCmd.Flags().Int("top", app.DefaultGrepTop, "maximum results")
	sourceCmd.Flags().String("at", "", "select one definition by source position instead of merging a name: <file>:<line>")
	sourceCmd.Flags().Bool("brief", false, "drop each match's source body, keeping signature/doc/location (source_omitted:true) — cheaper first look at a hub definition")
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
	coverageCmd.Flags().String("prefix", "", "only include files whose project-relative path starts with this prefix")
	coverageCmd.Flags().String("lang", "", "only include files of this language (e.g. go, typescript, python)")
	coverageCmd.Flags().Bool("uncovered", false, "only include files without precise call-graph coverage")
	coverageCmd.Flags().Bool("files", false, "include the bounded per-file list even without a filter")
	coverageCmd.Flags().Int("top", 200, "cap on by_directory rows and per-file detail rows (max 2000)")

	// Per-setting override flags (config file < env < flag) for every tunable.
	registerConfigFlags(rootCmd, indexCmd, daemonStartCmd, semanticCmd, serveCmd)

	rootCmd.AddCommand(versionCmd, initCmd, indexCmd, statusCmd, doctorCmd, serveCmd, studioCmd,
		callersCmd, calleesCmd, referencesCmd, impactCmd, reviewCmd, readOrderCmd, mapCmd, exploreCmd, traverseCmd, relatedFilesCmd, dependenciesCmd, fileImpactCmd, fileContextCmd, riskCmd, symbolAtCmd, secretImpactCmd, requiredKeysCmd, semanticCmd, hotspotsCmd, orphansCmd, coverageCmd, pathCmd, symbolsCmd, findCmd, grepCmd, sourceCmd, contextCmd, projectsCmd, docsCmd,
		annotateCmd, annotationsCmd, branchStatusCmd, branchSwitchCmd, branchSnapshotCmd, structuralManifestCmd, structuralExportCmd, configCmd, daemonCmd, agentCmd)

	// Wrap every descendant's RunE so a --json failure prints the structured
	// {ok,error,code,hint} envelope to stdout with a stable machine code, and
	// returns an exitCoded error main() maps to the documented exit taxonomy.
	// This must be recursive: config/cache/daemon subcommands previously bypassed
	// the envelope because only root's direct children were wrapped.
	wrapJSONHandlers(rootCmd)
}

func wrapJSONHandlers(parent *cobra.Command) {
	for _, c := range parent.Commands() {
		if c.RunE != nil {
			c.RunE = jsonHandler(c.RunE)
		}
		wrapJSONHandlers(c)
	}
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

// shortHash returns the first n runes of s, or the full string if shorter.
// Use it for any hash that may be empty or malformed and must never panic.
func shortHash(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
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
	return openSessionAt(cmd, targetDir(cmd))
}

// openSessionAt loads project-local configuration as though dir were the
// process working directory, then restores the caller's cwd. This makes -C and
// positional project paths affect both service calls and config discovery.
func openSessionAt(cmd *cobra.Command, dir string) (*app.Session, error) {
	cfgPath, _ := cmd.Flags().GetString("config")
	oldwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	if dir != "" {
		if err := os.Chdir(dir); err != nil {
			return nil, fmt.Errorf("change to project directory %q: %w", dir, err)
		}
		defer func() { _ = os.Chdir(oldwd) }()
	}
	sess, err := app.Open(cfgPath)
	if err != nil {
		return nil, err
	}
	applyConfigFlags(cmd, sess.Config) // flags win over config file + env
	if err := sess.Config.Validate(); err != nil {
		_ = sess.Close()
		return nil, err
	}
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
	hasJS := languages["javascript"] > 0
	hasPy := languages["python"] > 0
	hasLSP := hasTS || hasJS || hasPy || languages["vue"] > 0
	// P1-09 (B61): pre-fix, only "go" and "typescript" were considered, so a
	// Python- or JS-only project with --precise was reported as
	// "go/types" — a confident lie. Now any LSP-backed language flips
	// hasLSP and we attribute precise edges to callHierarchy.
	if preciseEdges > 0 {
		switch {
		case hasGo && hasLSP:
			return fmt.Sprintf(" (%d precise: go/types + callHierarchy)", preciseEdges)
		case hasLSP:
			return fmt.Sprintf(" (%d precise via callHierarchy)", preciseEdges)
		default:
			return fmt.Sprintf(" (%d precise via go/types)", preciseEdges)
		}
	}
	if hasLSP && !hasGo {
		// TS/JS/Python/Vue have no name-based call edges — --precise is
		// the only source.
		return " (no call graph yet; run 'codemap index --precise' to resolve LSP-backed calls)"
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
