/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/app"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
	"github.com/spf13/cobra"
)

var (
	callersCmd = &cobra.Command{
		Use:   "callers <symbol>",
		Short: "List functions/methods that call a symbol",
		Args:  cobra.ExactArgs(1),
		RunE:  runCallers,
	}
	calleesCmd = &cobra.Command{
		Use:   "callees <symbol>",
		Short: "List functions/methods that a symbol calls",
		Args:  cobra.ExactArgs(1),
		RunE:  runCallees,
	}
	impactCmd = &cobra.Command{
		Use:   "impact <symbol>",
		Short: "Impact analysis: blast radius (transitive callers) + test coverage",
		Args:  cobra.MaximumNArgs(1), // 0 args when --at <file>:<line> resolves the symbol
		RunE:  runImpact,
	}
	relatedFilesCmd = &cobra.Command{
		Use:   "related-files <file>",
		Short: "Files related to a file via the call/test graph (callers, callees, covering tests)",
		Args:  cobra.ExactArgs(1),
		RunE:  runRelatedFiles,
	}
	symbolAtCmd = &cobra.Command{
		Use:   "symbol-at <file>:<line>",
		Short: "Resolve a file:line position to its enclosing symbol (FQN, kind, range)",
		Args:  cobra.ExactArgs(1),
		RunE:  runSymbolAt,
	}
	secretImpactCmd = &cobra.Command{
		Use:   "secret-impact [<KEY>...]",
		Short: "Code blast radius of rotating secret keys: which symbols read each key, + covering tests (value-free — only key NAMES)",
		Args:  cobra.ArbitraryArgs, // 0 args is valid with --via-vault
		RunE:  runSecretImpact,
	}
	requiredKeysCmd = &cobra.Command{
		Use:   "required-keys <entrypoint>",
		Short: "Least-privilege key set: which candidate secret keys an entrypoint's call tree actually reads (for tvault seal/export)",
		Args:  cobra.ExactArgs(1),
		RunE:  runRequiredKeys,
	}
	semanticCmd = &cobra.Command{
		Use:     "semantic <query>",
		Aliases: []string{"search"}, // matches the studio "Search" tab and the common mental model
		Short:   "Semantic search across the code graph by meaning",
		Args:    cobra.MinimumNArgs(1),
		RunE:    runSemantic,
	}
	hotspotsCmd = &cobra.Command{
		Use:   "hotspots",
		Short: "List the most-referenced symbols (hubs)",
		RunE:  runHotspots,
	}
	orphansCmd = &cobra.Command{
		Use:   "orphans",
		Short: "List functions/methods with no callers (dead-code candidates)",
		RunE:  runOrphans,
	}
	pathCmd = &cobra.Command{
		Use:   "path <from> <to>",
		Short: "Shortest call path between two symbols",
		Args:  cobra.ExactArgs(2),
		RunE:  runPath,
	}
	symbolsCmd = &cobra.Command{
		Use:   "symbols <file>",
		Short: "List the symbols defined in a file (functions, types, methods, tests)",
		Args:  cobra.ExactArgs(1),
		RunE:  runSymbols,
	}
	findCmd = &cobra.Command{
		Use:   "find <query>",
		Short: "Find symbols by name (fast, offline — no embeddings needed)",
		Args:  cobra.MinimumNArgs(1),
		RunE:  runFind,
	}
	sourceCmd = &cobra.Command{
		Use:   "source <symbol>",
		Short: "Print a symbol's source code (the body behind its signature)",
		Args:  cobra.ExactArgs(1),
		RunE:  runSource,
	}
	contextCmd = &cobra.Command{
		Use:   "context <symbol>",
		Short: "Everything about a symbol in one call: definition, callers, callees, tests, notes",
		Args:  cobra.ExactArgs(1),
		RunE:  runContext,
	}
	projectsCmd = &cobra.Command{
		Use:   "projects",
		Short: "List all projects registered with codemap and their index sizes",
		Args:  cobra.NoArgs,
		RunE:  runProjects,
	}
	docsCmd = &cobra.Command{
		Use:   "docs [topic]",
		Short: "Print the agent guide to codemap (topics: overview, workflow, commands, annotations, accuracy, ecosystem)",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runDocs,
	}
)

func runCallers(cmd *cobra.Command, args []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer sess.Close()
	cwd, _ := os.Getwd()
	svc := app.NewService(sess)
	if ok, err := requireIndexed(cmd, svc); err != nil || !ok {
		return err
	}
	useLSP, _ := cmd.Flags().GetBool("lsp")
	var rep *app.RelationReport
	if useLSP {
		rep, err = svc.PreciseCallers(cmd.Context(), cwd, args[0])
	} else {
		rep, err = svc.Callers(cwd, args[0])
	}
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	if !rep.Found {
		printNoSymbol(rep.Symbol, rep.Project)
		return nil
	}
	label := fmt.Sprintf("Callers of %s", rep.Symbol)
	if useLSP && rep.Note == "" { // Note set => precise fell back to name-based; don't mislabel
		label += " (precise, via gopls)"
	}
	renderRefsCapped(label, rep.Results, relationsDisplayCap)
	if rep.Note != "" {
		fmt.Println("⚠ " + rep.Note)
	}
	if rep.Resolution != "" {
		fmt.Println("⚠ " + rep.Resolution)
	}
	renderAnnotations(rep.Annotations)
	return nil
}

// printNoSymbol reports a symbol that isn't in the index, with a recovery hint:
// `find` does name/substring search — the right next step when a name was
// guessed, partial, or misspelled. Shared by the symbol-query commands so the
// "no such symbol" message (and its fix) reads the same everywhere.
func printNoSymbol(symbol, project string) {
	fmt.Printf("no symbol named %q in project %s\n", symbol, project)
	fmt.Printf("  try: codemap find %s   (search symbols by name/substring)\n", symbol)
}

func runCallees(cmd *cobra.Command, args []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer sess.Close()
	cwd, _ := os.Getwd()
	svc := app.NewService(sess)
	if ok, err := requireIndexed(cmd, svc); err != nil || !ok {
		return err
	}
	useLSP, _ := cmd.Flags().GetBool("lsp")
	var rep *app.RelationReport
	if useLSP {
		rep, err = svc.PreciseCallees(cmd.Context(), cwd, args[0])
	} else {
		rep, err = svc.Callees(cwd, args[0])
	}
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	if !rep.Found {
		printNoSymbol(rep.Symbol, rep.Project)
		return nil
	}
	label := fmt.Sprintf("Callees of %s", rep.Symbol)
	if useLSP && rep.Note == "" { // Note set => precise fell back to name-based; don't mislabel
		label += " (precise, via gopls)"
	}
	renderRefsCapped(label, rep.Results, relationsDisplayCap)
	if rep.Note != "" {
		fmt.Println("⚠ " + rep.Note)
	}
	if rep.Resolution != "" {
		fmt.Println("⚠ " + rep.Resolution)
	}
	renderAnnotations(rep.Annotations)
	return nil
}

func runImpact(cmd *cobra.Command, args []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer sess.Close()
	cwd, _ := os.Getwd()
	depth, _ := cmd.Flags().GetInt("depth")
	svc := app.NewService(sess)
	if ok, err := requireIndexed(cmd, svc); err != nil || !ok {
		return err
	}
	// Resolve the target symbol: either a positional name or --at <file>:<line>.
	symbol := ""
	if len(args) > 0 {
		symbol = args[0]
	}
	if at, _ := cmd.Flags().GetString("at"); at != "" {
		file, line, perr := parseFileLine(at)
		if perr != nil {
			return perr
		}
		sa, serr := svc.SymbolAt(cwd, file, line)
		if serr != nil {
			return serr
		}
		if sa.Resolution == "none" {
			return fmt.Errorf("no symbol found at %s", at)
		}
		symbol = sa.Symbol
	}
	if symbol == "" {
		return fmt.Errorf("impact needs a <symbol> argument or --at <file>:<line>")
	}
	rep, err := svc.Impact(cwd, symbol, depth)
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	if !rep.Found {
		printNoSymbol(rep.Symbol, rep.Project)
		return nil
	}
	fmt.Printf("Impact of %s (%s)\n", rep.Symbol, rep.Project)
	for _, l := range rep.Locations {
		fmt.Printf("  defined:        %s:%d\n", l.File, l.StartLine)
	}
	if rep.Note != "" {
		fmt.Println("  ⚠ " + rep.Note)
	}
	fmt.Printf("  direct callers: %d\n", len(rep.DirectCallers))
	fmt.Printf("  blast radius:   %d (depth ≤ %d)\n", len(rep.BlastRadius), depth)
	fmt.Printf("  tests covering: %d\n", len(rep.Tests))
	if rep.Resolution != "" {
		fmt.Println("  ⚠ " + rep.Resolution)
	} else if rep.Untested {
		fmt.Println("  ⚠ no tests reach this symbol")
	}
	renderAnnotations(rep.Annotations)
	// List the covering tests explicitly so you know what to run — they're a
	// subset of the blast radius, but spelling them out beats hunting for ✓.
	if len(rep.Tests) > 0 {
		fmt.Println("  covering tests (run these):")
		tests, more := capList(rep.Tests, impactTestsCap)
		for _, t := range tests {
			fmt.Printf("     %-36s %s:%d\n", disp(t.FQN, t.Symbol), t.File, t.StartLine)
		}
		if more > 0 {
			fmt.Printf("     … (%d more — use --json for all)\n", more)
		}
	}
	if len(rep.BlastRadius) > 0 {
		fmt.Println("  affected (blast radius):")
		nodes, more := capList(rep.BlastRadius, impactBlastCap)
		for _, n := range nodes {
			marker := " "
			if n.Kind == "test" {
				marker = "✓"
			}
			fmt.Printf("   %s [%d] %-36s %s:%d\n", marker, n.Depth, disp(n.FQN, n.Symbol), n.File, n.StartLine)
		}
		if more > 0 {
			fmt.Printf("   … (%d more — use --json for all, or lower --depth)\n", more)
		}
	}
	return nil
}

// parseFileLine splits a "path/to/file.go:42" position. The file part may itself
// contain no colon issues since we split on the LAST colon (Windows-style drive
// letters aren't a concern for project-relative paths).
func parseFileLine(s string) (string, int, error) {
	i := strings.LastIndex(s, ":")
	if i < 0 {
		return "", 0, fmt.Errorf("expected <file>:<line>, got %q", s)
	}
	line, err := strconv.Atoi(s[i+1:])
	if err != nil || line < 1 {
		return "", 0, fmt.Errorf("invalid line number in %q", s)
	}
	return s[:i], line, nil
}

func runRelatedFiles(cmd *cobra.Command, args []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer sess.Close()
	cwd, _ := os.Getwd()
	svc := app.NewService(sess)
	rep, err := svc.RelatedFiles(cwd, args[0])
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	if !rep.Indexed {
		fmt.Printf("Project %q is not indexed yet. Run 'codemap index'.\n", rep.Project)
		return nil
	}
	if len(rep.Related) == 0 {
		fmt.Printf("No files related to %s in the graph.\n", rep.File)
		return nil
	}
	fmt.Printf("Files related to %s (%s)\n", rep.File, rep.Project)
	for _, r := range rep.Related {
		fmt.Printf("  %-7s %s\n", r.Reason, r.RelativePath)
	}
	return nil
}

func runSymbolAt(cmd *cobra.Command, args []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer sess.Close()
	cwd, _ := os.Getwd()
	file, line, err := parseFileLine(args[0])
	if err != nil {
		return err
	}
	rep, err := app.NewService(sess).SymbolAt(cwd, file, line)
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	if rep.Resolution == "none" {
		fmt.Printf("No symbol at %s:%d\n", file, line)
		return nil
	}
	fmt.Printf("%s  %s:%d-%d  (%s, %s)\n", disp(rep.FQN, rep.Symbol), rep.File, rep.StartLine, rep.EndLine, rep.Kind, rep.Resolution)
	return nil
}

// fetchVaultKeys shells `tvault [-p project] list [--prefix p] --json` to get the
// project's secret key NAMES (a JSON array of strings — value-free). The ONLY tvault
// verb it runs is `list`; `tvault get` is never reachable, so secret values can't
// enter codemap.
func fetchVaultKeys(ctx context.Context, project, prefix string) ([]string, error) {
	tvault, err := exec.LookPath("tvault")
	if err != nil {
		return nil, fmt.Errorf("--via-vault needs tinyvault: 'tvault' not found on PATH")
	}
	args := []string{"-p", project, "list", "--json"}
	if prefix != "" {
		args = append(args, "--prefix", prefix)
	}
	out, err := exec.CommandContext(ctx, tvault, args...).Output()
	if err != nil {
		return nil, fmt.Errorf("tvault list failed for project %q: %w", project, err)
	}
	var keys []string
	if err := json.Unmarshal(out, &keys); err != nil {
		return nil, fmt.Errorf("parse tvault list output: %w", err)
	}
	return keys, nil
}

func runSecretImpact(cmd *cobra.Command, args []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer sess.Close()
	cwd, _ := os.Getwd()
	depth, _ := cmd.Flags().GetInt("depth")
	keys := append([]string{}, args...)
	if vault, _ := cmd.Flags().GetString("via-vault"); vault != "" {
		prefix, _ := cmd.Flags().GetString("prefix")
		vk, ferr := fetchVaultKeys(cmd.Context(), vault, prefix)
		if ferr != nil {
			return ferr
		}
		keys = append(keys, vk...)
	}
	if len(keys) == 0 {
		return fmt.Errorf("supply one or more secret key names, or --via-vault <project> to fetch them")
	}
	rep, err := app.NewService(sess).SecretImpact(cwd, keys, depth)
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	if !rep.Indexed {
		fmt.Printf("Project %q is not indexed yet. Run 'codemap index'.\n", rep.Project)
		return nil
	}
	for _, k := range rep.Keys {
		warn := ""
		if k.Untested {
			warn = "  ⚠ untested"
		}
		fmt.Printf("%s — %d reader(s), blast radius %d, %d covering test(s)%s\n",
			k.Key, len(k.UsedBy), k.BlastRadius, k.CoveringTests, warn)
		for _, u := range k.UsedBy {
			fmt.Printf("    %s  %s:%d\n", disp(u.FQN, u.Symbol), u.File, u.Line)
		}
	}
	if len(rep.OrphanKeys) > 0 {
		fmt.Printf("no code usages found (verify before treating as dead): %s\n", strings.Join(rep.OrphanKeys, ", "))
	}
	if !rep.Precise {
		fmt.Println("⚠ " + rep.Note)
	}
	if rep.Stale {
		fmt.Println("⚠ index is stale — reindex before trusting a rotation")
	}
	return nil
}

func runRequiredKeys(cmd *cobra.Command, args []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer sess.Close()
	cwd, _ := os.Getwd()
	depth, _ := cmd.Flags().GetInt("depth")
	keys, _ := cmd.Flags().GetStringSlice("keys")
	if vault, _ := cmd.Flags().GetString("via-vault"); vault != "" {
		prefix, _ := cmd.Flags().GetString("prefix")
		vk, ferr := fetchVaultKeys(cmd.Context(), vault, prefix)
		if ferr != nil {
			return ferr
		}
		keys = append(keys, vk...)
	}
	if len(keys) == 0 {
		return fmt.Errorf("supply candidate keys with --keys K1,K2 or --via-vault <project>")
	}
	rep, err := app.NewService(sess).RequiredKeys(cwd, args[0], keys, depth)
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	if !rep.Found {
		printNoSymbol(rep.Entrypoint, rep.Project)
		return nil
	}
	// One key per line — pipe-friendly: `… | xargs -I{} tvault seal --key {}`.
	for _, k := range rep.RequiredKeys {
		fmt.Println(k)
	}
	return nil
}

func runSemantic(cmd *cobra.Command, args []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer sess.Close()
	cwd, _ := os.Getwd()
	top, _ := cmd.Flags().GetInt("top")
	svc := app.NewService(sess)
	if ok, err := requireIndexed(cmd, svc); err != nil || !ok {
		return err
	}
	rep, err := svc.Semantic(cmd.Context(), cwd, strings.Join(args, " "), top)
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	if len(rep.Hits) == 0 {
		if rep.Note != "" {
			fmt.Println(rep.Note)
		} else {
			fmt.Println("no matches")
		}
		return nil
	}
	for _, h := range rep.Hits {
		// Mirror `find`'s outline (file:line + signature) but lead with the relevance
		// score — so a meaning-based hit shows WHAT it is, not just a bare name.
		fmt.Printf("%s%.3f  %-26s %s\n", annMark(h.Annotations), h.Score,
			fmt.Sprintf("%s:%d", h.File, h.StartLine), sigOrName(h.Signature, h.FQN, h.Symbol))
	}
	return nil
}

func runHotspots(cmd *cobra.Command, _ []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer sess.Close()
	cwd, _ := os.Getwd()
	top, _ := cmd.Flags().GetInt("top")
	svc := app.NewService(sess)
	if ok, err := requireIndexed(cmd, svc); err != nil || !ok {
		return err
	}
	rep, err := svc.Hotspots(cwd, top)
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	if len(rep.Hotspots) == 0 {
		// Indexed (requireIndexed passed) but no call edges → almost always a
		// TS/JS/Python project indexed name-based, which has no call graph.
		fmt.Printf("no hotspots in %s (no call edges — TypeScript/JavaScript/Python need 'codemap index --precise')\n", rep.Project)
		return nil
	}
	fmt.Printf("Hotspots in %s:\n", rep.Project)
	anyInflated := false
	for _, h := range rep.Hotspots {
		line := fmt.Sprintf("  %4d  %-36s %s:%d", h.InDegree, disp(h.FQN, h.Symbol), h.File, h.StartLine)
		if h.SharedName > 1 { // count fanned across same-named defs (name-based)
			line += fmt.Sprintf("  ⚠ name shared by %d (inflated)", h.SharedName)
			anyInflated = true
		}
		fmt.Println(line)
	}
	// The ⚠ rows say WHAT is wrong; point at the fix. Inflation only happens on a
	// name-based index (precise edges are exact), so the hint is always actionable here.
	if anyInflated {
		fmt.Println("  → ⚠ counts are inflated by same-named symbols; 'codemap index --precise' resolves them exactly")
	}
	return nil
}

func runOrphans(cmd *cobra.Command, _ []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer sess.Close()
	cwd, _ := os.Getwd()
	top, _ := cmd.Flags().GetInt("top")
	svc := app.NewService(sess)
	if ok, err := requireIndexed(cmd, svc); err != nil || !ok {
		return err
	}
	rep, err := svc.Orphans(cwd, top)
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	renderRefs(fmt.Sprintf("Orphans in %s (no callers — dead-code candidates)", rep.Project), rep.Orphans)
	return nil
}

func runPath(cmd *cobra.Command, args []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer sess.Close()
	cwd, _ := os.Getwd()
	svc := app.NewService(sess)
	if ok, err := requireIndexed(cmd, svc); err != nil || !ok {
		return err
	}
	rep, err := svc.Path(cwd, args[0], args[1])
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	if !rep.Found {
		if rep.Note != "" {
			fmt.Println(rep.Note)
		} else {
			fmt.Printf("no call path from %s to %s\n", rep.From, rep.To)
		}
		renderAnnotations(rep.Annotations) // a pinned path note is worth showing even with no current path
		return nil
	}
	names := make([]string, 0, len(rep.Path))
	for _, p := range rep.Path {
		names = append(names, p.Symbol)
	}
	fmt.Println(strings.Join(names, " → "))
	for _, p := range rep.Path {
		fmt.Printf("  %-30s %s:%d\n", p.Symbol, p.File, p.StartLine)
	}
	renderAnnotations(rep.Annotations)
	return nil
}

func runFind(cmd *cobra.Command, args []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer sess.Close()
	cwd, _ := os.Getwd()
	top, _ := cmd.Flags().GetInt("top")
	svc := app.NewService(sess)
	if ok, err := requireIndexed(cmd, svc); err != nil || !ok {
		return err
	}
	rep, err := svc.FindSymbols(cwd, strings.Join(args, " "), top)
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	if len(rep.Hits) == 0 {
		// requireIndexed passed, so the project IS indexed — the name just isn't here.
		fmt.Printf("no symbols matching %q\n", rep.Query)
		return nil
	}
	for _, h := range rep.Hits {
		fmt.Printf("%s%-9s %-26s %s\n", annMark(h.Annotations), h.Kind, fmt.Sprintf("%s:%d", h.File, h.StartLine),
			sigOrName(h.Signature, h.FQN, h.Symbol))
	}
	return nil
}

func runSymbols(cmd *cobra.Command, args []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer sess.Close()
	cwd, _ := os.Getwd()
	svc := app.NewService(sess)
	if ok, err := requireIndexed(cmd, svc); err != nil || !ok {
		return err
	}
	rep, err := svc.Symbols(cwd, args[0])
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	if len(rep.Symbols) == 0 {
		// requireIndexed passed, so the project IS indexed — the file path isn't.
		fmt.Printf("no symbols in %s (file not in the index — check the path, relative to the project root)\n", rep.File)
		return nil
	}
	fmt.Printf("%s (%d symbols):\n", rep.File, len(rep.Symbols))
	for _, s := range rep.Symbols {
		fmt.Printf("  %-9s %5d  %s\n", s.Kind, s.StartLine, sigOrName(s.Signature, s.FQN, s.Symbol))
	}
	return nil
}

func runSource(cmd *cobra.Command, args []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer sess.Close()
	cwd, _ := os.Getwd()
	rep, err := app.NewService(sess).Source(cwd, args[0])
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	if len(rep.Matches) == 0 {
		fmt.Printf("no symbol named %q (is the project indexed?)\n", rep.Symbol)
		return nil
	}
	for i, mch := range rep.Matches {
		if i > 0 {
			fmt.Println()
		}
		fmt.Printf("// %s  %s:%d-%d\n", disp(mch.FQN, mch.Symbol), mch.File, mch.StartLine, mch.EndLine)
		fmt.Println(mch.Source)
	}
	if len(rep.Annotations) > 0 {
		fmt.Println()
		renderAnnotations(rep.Annotations)
	}
	return nil
}

func runProjects(cmd *cobra.Command, _ []string) error {
	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer sess.Close()
	rep, err := app.NewService(sess).Projects()
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	if len(rep.Projects) == 0 {
		fmt.Println("no projects registered yet — run 'codemap init' in a project")
		return nil
	}
	fmt.Printf("%-20s %8s %8s %7s  %s\n", "PROJECT", "NODES", "EDGES", "FILES", "PATH")
	for _, p := range rep.Projects {
		fmt.Printf("%-20s %8d %8d %7d  %s\n", truncStr(p.Name, 20), p.Nodes, p.Edges, p.Files, p.Path)
	}
	return nil
}

func runDocs(_ *cobra.Command, args []string) error {
	topic := ""
	if len(args) > 0 {
		topic = args[0]
	}
	fmt.Print(app.Docs(topic))
	return nil
}

// requireIndexed reports whether the current project has been indexed, printing a
// clear "run codemap index" message first if not — text, or a structured
// {indexed:false,…} object under --json so agents get the same signal. Query
// commands gate on it so a cold repo doesn't yield misleading empty results.
func requireIndexed(cmd *cobra.Command, svc *app.Service) (bool, error) {
	cwd, _ := os.Getwd()
	indexed, name, err := svc.Indexed(cwd)
	if err != nil {
		return false, err
	}
	if !indexed {
		if jsonOut(cmd) {
			_ = printJSON(map[string]any{
				"project": name,
				"indexed": false,
				"note":    "project not indexed — run 'codemap index' first",
			})
		} else {
			fmt.Printf("Project %q is not indexed yet. Run 'codemap index'.\n", name)
		}
		return false, nil
	}
	return true, nil
}

// renderRefs renders a relation list under a label (e.g. "Callers of X").
func renderRefs(label string, refs []app.SymbolRef) {
	if len(refs) == 0 {
		fmt.Printf("%s: none\n", label)
		return
	}
	fmt.Printf("%s:\n", label)
	for _, r := range refs {
		fmt.Printf("  %-36s %s:%d\n", disp(r.FQN, r.Symbol), r.File, r.StartLine)
	}
}

// renderRefsCapped renders a relation list bounded to cap rows — the complete
// set is always in --json — printing a "… (N more)" line when truncated. Used by
// callers/callees, which (unlike the ranked top-N commands) return the full set.
func renderRefsCapped(label string, refs []app.SymbolRef, limit int) {
	shown, more := capList(refs, limit)
	renderRefs(label, shown)
	if more > 0 {
		fmt.Printf("  … (%d more — use --json for all)\n", more)
	}
}

// annMark is a row prefix flagging that a result carries annotations.
func annMark(anns []graph.Annotation) string {
	if len(anns) > 0 {
		return "⟐ "
	}
	return "  "
}

// renderAnnotations prints pinned notes/data under a query result, if any.
func renderAnnotations(anns []graph.Annotation) {
	if len(anns) == 0 {
		return
	}
	fmt.Println("  annotations:")
	for _, a := range anns {
		line := a.Source
		if a.Note != "" {
			line += ": " + a.Note
		}
		if a.Data != "" {
			line += "  " + truncStr(a.Data, 80)
		}
		fmt.Printf("     #%d %s\n", a.ID, line)
	}
}
