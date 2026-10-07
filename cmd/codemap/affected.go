/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/app"
	"github.com/spf13/cobra"
)

var affectedCmd = &cobra.Command{
	Use:   "affected [files...]",
	Short: "Changed files → the test files to run (covering tests + test files that import them)",
	Long: `Map changed files to the test files that should run, without parsing a review
report. A test file is selected when it covers a changed symbol through the call
graph, imports a changed file (directly or transitively, up to --depth hops), or
is itself a changed test file.

Changed files come from the arguments, from --stdin (newline-separated, e.g.
git diff --name-only), or from git: --staged for the index, --since <ref> for
everything since a ref. With no files and no source flag it uses the working
tree (staged + unstaged + untracked). Paths are project-relative; absolute paths
under the project root are accepted.

Human output is one test path per line and nothing else on stdout, so it pipes
straight into a test runner; notes about unresolved or name-based coverage go to
stderr. Use --json for reasons, unmapped files, call_graph and analysis_complete.`,
	Example: `  codemap affected internal/app/review.go
  git diff --name-only main | codemap affected --stdin
  codemap affected --staged --filter '*_test.go'
  codemap affected --since main --json
  codemap affected --stdin < changed.txt | xargs go test`,
	Args: cobra.ArbitraryArgs,
	RunE: runAffected,
}

func runAffected(cmd *cobra.Command, args []string) error {
	stdin, _ := cmd.Flags().GetBool("stdin")
	since, _ := cmd.Flags().GetString("since")
	staged, _ := cmd.Flags().GetBool("staged")
	filter, _ := cmd.Flags().GetString("filter")
	depth, _ := cmd.Flags().GetInt("depth")
	if err := app.ValidateAffectedOpts(app.AffectedOpts{Since: since, Staged: staged, Depth: depth}); err != nil {
		return err
	}

	files := append([]string(nil), args...)
	if stdin {
		more, err := readPathLines(cmd.InOrStdin())
		if err != nil {
			return fmt.Errorf("read --stdin: %w", err)
		}
		files = append(files, more...)
	}
	source := ""
	if stdin && !staged && since == "" {
		// An empty stdin list means "nothing changed", never "the working tree".
		source = app.AffectedSourceFiles
	}

	sess, err := openSession(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()
	cwd := targetDir(cmd)
	svc := app.NewService(sess)
	if ok, err := requireIndexed(cmd, svc); err != nil || !ok {
		return err
	}
	rep, err := svc.Affected(cwd, app.AffectedOpts{
		Files: files, Source: source, Since: since, Staged: staged, Filter: filter, Depth: depth,
	})
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(rep)
	}
	for _, f := range rep.TestFiles() {
		fmt.Println(f)
	}
	if rep.Note != "" {
		fmt.Fprintln(os.Stderr, "codemap affected: "+rep.Note)
	}
	return nil
}

// readPathLines reads newline-separated paths, ignoring blank lines and
// trimming CR/whitespace so Windows git output works too.
func readPathLines(r io.Reader) ([]string, error) {
	var out []string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			out = append(out, line)
		}
	}
	return out, sc.Err()
}
