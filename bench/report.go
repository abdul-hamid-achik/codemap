package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/abdul-hamid-achik/codemap/bench/suite"
)

const (
	markerStart = "<!-- BENCH:START -->"
	markerEnd   = "<!-- BENCH:END -->"
)

// reportOnly loads the newest summary under outDir and splices its table into
// the README between the BENCH markers. It is idempotent: re-running re-splices
// the same numbers.
func reportOnly(outDir, readmePath string) error {
	sum, path, err := newestSummary(outDir)
	if err != nil {
		return err
	}
	readme, err := os.ReadFile(readmePath)
	if err != nil {
		return err
	}
	spliced, err := SpliceTable(string(readme), sum)
	if err != nil {
		return err
	}
	if err := os.WriteFile(readmePath, []byte(spliced), 0o644); err != nil {
		return err
	}
	fmt.Printf("spliced %s into %s\n", filepath.Base(path), readmePath)
	return nil
}

// newestSummary returns the most recent *.summary.json under dir.
func newestSummary(dir string) (suite.Summary, string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return suite.Summary{}, "", fmt.Errorf("no results dir %q (run the benchmark first): %w", dir, err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".summary.json") {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return suite.Summary{}, "", fmt.Errorf("no *.summary.json in %q", dir)
	}
	sort.Strings(names)
	path := filepath.Join(dir, names[len(names)-1])
	raw, err := os.ReadFile(path)
	if err != nil {
		return suite.Summary{}, "", err
	}
	var sum suite.Summary
	if err := json.Unmarshal(raw, &sum); err != nil {
		return suite.Summary{}, "", err
	}
	return sum, path, nil
}

// SpliceTable replaces the content between the BENCH markers in readme with a
// freshly rendered table. Pure and unit-tested (report_test.go).
func SpliceTable(readme string, sum suite.Summary) (string, error) {
	i := strings.Index(readme, markerStart)
	j := strings.Index(readme, markerEnd)
	if i < 0 || j < 0 || j < i {
		return "", fmt.Errorf("README markers %q / %q not found (or out of order)", markerStart, markerEnd)
	}
	table := RenderTable(sum)
	var b strings.Builder
	b.WriteString(readme[:i+len(markerStart)])
	b.WriteString("\n")
	b.WriteString(table)
	b.WriteString(readme[j:])
	return b.String(), nil
}

// RenderTable renders the DIRECTIONAL banner + metrics table from a summary.
func RenderTable(sum suite.Summary) string {
	var b strings.Builder
	auth := ""
	if sum.AuthMode != "" {
		auth = ", " + sum.AuthMode + " auth"
	}
	fmt.Fprintf(&b, "> **DIRECTIONAL** — %s @ `%s`, %s, N=%d (mean ± σ)%s, %s. Not a controlled study; see [bench/README.md](bench/README.md).\n>\n",
		sum.FixtureRepo, shortSHA(sum.FixtureSHA), sum.Model, sum.Reps, auth, dateOf(sum.GeneratedAt))

	base := findArm(sum.Arms, "baseline")
	cm := findArm(sum.Arms, "codemap")

	fmt.Fprintf(&b, "> | metric (mean ± σ, per session) | baseline (Read/Grep/Glob) | with codemap | Δ |\n")
	fmt.Fprintf(&b, "> |---|---|---|---|\n")
	rowStat(&b, "tool calls", base, cm, func(a suite.ArmSummary) suite.Stat { return a.ToolCalls }, fmtNum, true)
	rowStat(&b, "input tokens (incl. cache reads)", base, cm, func(a suite.ArmSummary) suite.Stat { return a.InputTokens }, fmtK, true)
	rowStat(&b, "output tokens", base, cm, func(a suite.ArmSummary) suite.Stat { return a.OutputTokens }, fmtK, true)
	rowStat(&b, "wall-clock (s)", base, cm, func(a suite.ArmSummary) suite.Stat { return a.WallClockS }, fmtNum, true)
	rowStat(&b, "cost (USD)", base, cm, func(a suite.ArmSummary) suite.Stat { return a.CostUSD }, fmtUSD, true)
	rowCorrect(&b, base, cm)

	if sum.IndexSeconds > 0 {
		fmt.Fprintf(&b, ">\n> One-time codemap index cost (disclosed, not per-session): **%.0fs**. codemap is modelled as pre-indexed (the daemon keeps it fresh in real use), so the codemap arm pays query time, not index time.\n", sum.IndexSeconds)
	}
	return b.String()
}

func rowStat(b *strings.Builder, label string, base, cm *suite.ArmSummary, pick func(suite.ArmSummary) suite.Stat, f func(float64) string, lowerBetter bool) {
	bc := cell(base, pick, f)
	cc := cell(cm, pick, f)
	delta := "—"
	if base != nil && cm != nil {
		bv := pick(*base).Mean
		cv := pick(*cm).Mean
		if bv != 0 {
			pct := (cv - bv) / bv * 100
			delta = fmt.Sprintf("%+.0f%%", pct)
		}
	}
	fmt.Fprintf(b, "> | %s | %s | %s | %s |\n", label, bc, cc, delta)
}

func rowCorrect(b *strings.Builder, base, cm *suite.ArmSummary) {
	bc, cc, delta := "—", "—", "—"
	if base != nil {
		bc = fmt.Sprintf("%d/%d", base.TasksCorrect, base.TasksTotal)
	}
	if cm != nil {
		cc = fmt.Sprintf("%d/%d", cm.TasksCorrect, cm.TasksTotal)
	}
	if base != nil && cm != nil {
		delta = fmt.Sprintf("%+d", cm.TasksCorrect-base.TasksCorrect)
	}
	fmt.Fprintf(b, "> | tasks correct | %s | %s | %s |\n", bc, cc, delta)
	warnFailed(b, base)
	warnFailed(b, cm)
}

// warnFailed appends a visible caveat when an arm had errored sessions — they
// are excluded from the metric stats, and a table that hides that would lie.
func warnFailed(b *strings.Builder, a *suite.ArmSummary) {
	if a != nil && a.Failed > 0 {
		fmt.Fprintf(b, ">\n> ⚠ %s: %d of %d sessions failed and are excluded from the stats above.\n", a.Arm, a.Failed, a.Sessions)
	}
}

func cell(a *suite.ArmSummary, pick func(suite.ArmSummary) suite.Stat, f func(float64) string) string {
	if a == nil {
		return "—"
	}
	s := pick(*a)
	return fmt.Sprintf("%s ± %s", f(s.Mean), f(s.Std))
}

func findArm(arms []suite.ArmSummary, name string) *suite.ArmSummary {
	for i := range arms {
		if arms[i].Arm == name {
			return &arms[i]
		}
	}
	return nil
}

func fmtNum(x float64) string { return fmt.Sprintf("%.1f", x) }

func fmtK(x float64) string {
	if x >= 1000 {
		return fmt.Sprintf("%.0fk", x/1000)
	}
	return fmt.Sprintf("%.0f", x)
}

func fmtUSD(x float64) string { return fmt.Sprintf("$%.3f", x) }

func shortSHA(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

func dateOf(rfc string) string {
	if len(rfc) >= 10 {
		return rfc[:10]
	}
	return rfc
}

// printSummaryTable dumps the rendered table to stdout after a run.
func printSummaryTable(sum suite.Summary) {
	fmt.Println()
	fmt.Println(strings.ReplaceAll(RenderTable(sum), "> ", ""))
}

// fixtureRepo / fixtureSHA read bench/fixtures/fixture.lock (repo= / sha=)
// living next to the fixture checkout.
func fixtureRepo(fixture string) string { return lockField(fixture, "repo") }
func fixtureSHA(fixture string) string  { return lockField(fixture, "sha") }

func lockField(fixture, key string) string {
	lock := filepath.Join(filepath.Dir(fixture), "fixture.lock")
	raw, err := os.ReadFile(lock)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, key+"=") {
			v := strings.TrimPrefix(line, key+"=")
			// Normalise a repo URL to a short org/name for the banner.
			if key == "repo" {
				v = strings.TrimSuffix(v, ".git")
				v = strings.TrimPrefix(v, "https://github.com/")
			}
			return v
		}
	}
	return ""
}
