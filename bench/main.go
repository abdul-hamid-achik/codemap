// Command bench is the codemap agent benchmark harness. It runs a fixed suite
// of code-navigation tasks against a pinned OSS fixture (go-git) with two arms —
// baseline (Read/Grep/Glob) and codemap (same tools + the codemap MCP server) —
// over N repetitions, grades each answer against ground truth derived
// INDEPENDENTLY OF CODEMAP (gopls / go/parser / an independent BFS), and writes
// a committed summary JSON. `bench --report-only` regenerates the DIRECTIONAL
// results table in README.md from the newest summary.
//
// Every published number is DIRECTIONAL: this is not a controlled study. See
// bench/README.md for methodology and the circularity/independence rules.
//
// This is a proof artifact, not a product surface. It is local-only (like
// `task flows`): it needs `claude`, `gopls`, and ANTHROPIC_API_KEY, so it is
// never run in CI. The grader/parser unit tests DO run in CI via `go test ./...`.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/abdul-hamid-achik/codemap/bench/drivers"
	"github.com/abdul-hamid-achik/codemap/bench/grade"
	"github.com/abdul-hamid-achik/codemap/bench/suite"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "bench:", err)
		os.Exit(1)
	}
}

type config struct {
	driver       string
	model        string
	tasksFilter  string
	reps         int
	arms         string
	dryRun       bool
	smoke        bool
	reportOnly   bool
	out          string
	tasksDir     string
	fixture      string
	mcpConfig    string
	readme       string
	indexSeconds float64
}

func defaultModel() string {
	if m := os.Getenv("CODEMAP_BENCH_MODEL"); m != "" {
		return m
	}
	return "claude-sonnet-5"
}

func run() error {
	var c config
	flag.StringVar(&c.driver, "driver", "claude", "agent driver: claude|smoke|codex|gemini")
	flag.StringVar(&c.model, "model", defaultModel(), "model id (env CODEMAP_BENCH_MODEL)")
	flag.StringVar(&c.tasksFilter, "tasks", "", "comma-separated task id/slug subset (default: all)")
	flag.IntVar(&c.reps, "reps", 3, "repetitions per task×arm")
	flag.StringVar(&c.arms, "arms", "baseline,codemap", "comma-separated arms")
	flag.BoolVar(&c.dryRun, "dry-run", false, "print the plan and exact invocations; no API calls")
	flag.BoolVar(&c.smoke, "smoke", false, "offline smoke run (fabricated metrics; tests plumbing, no API)")
	flag.BoolVar(&c.reportOnly, "report-only", false, "regenerate README table from the newest summary; no run")
	flag.StringVar(&c.out, "out", "bench/results", "results directory")
	flag.StringVar(&c.tasksDir, "tasks-dir", "bench/tasks", "tasks directory")
	flag.StringVar(&c.fixture, "fixture", "bench/fixtures/repo", "fixture repo root (agent cwd)")
	flag.StringVar(&c.mcpConfig, "mcp-config", "bench/mcp/codemap.mcp.json", "codemap arm MCP config")
	flag.StringVar(&c.readme, "readme", "README.md", "README to splice the results table into")
	flag.Float64Var(&c.indexSeconds, "index-seconds", 0, "one-time codemap index cost (reported separately)")
	flag.Parse()

	if c.reportOnly {
		return reportOnly(c.out, c.readme)
	}
	return orchestrate(c)
}

func orchestrate(c config) error {
	tasks, err := suite.LoadTasks(c.tasksDir)
	if err != nil {
		return fmt.Errorf("load tasks: %w", err)
	}
	tasks = filterTasks(tasks, c.tasksFilter)
	if len(tasks) == 0 {
		return fmt.Errorf("no tasks selected")
	}
	armNames := splitCSV(c.arms)
	arms, err := buildArms(armNames, c)
	if err != nil {
		return err
	}

	if c.dryRun {
		return printPlan(tasks, arms, c)
	}

	driver, err := pickDriver(c, tasks)
	if err != nil {
		return err
	}

	ts := time.Now().UTC().Format("20060102-150405")
	runDir := filepath.Join(c.out, ts)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return err
	}

	var sessions []suite.Session
	total := len(tasks) * len(arms) * c.reps
	n := 0
	for _, t := range tasks {
		truth, err := suite.LoadTruth(filepath.Join(c.tasksDir, t.Truth))
		if err != nil {
			return fmt.Errorf("load truth for %s: %w", t.ID, err)
		}
		for _, arm := range arms {
			for rep := 1; rep <= c.reps; rep++ {
				n++
				transcript := filepath.Join(runDir, fmt.Sprintf("%s_%s_%d.jsonl", t.ID, arm.Name, rep))
				fmt.Printf("[%d/%d] %s / %s / rep %d ... ", n, total, t.ID, arm.Name, rep)
				s := runOne(driver, t, arm, rep, transcript, truth)
				sessions = append(sessions, s)
				status := "PASS"
				if !s.Pass {
					status = "FAIL"
				}
				if s.Error != "" {
					status = "ERR:" + s.Error
				}
				fmt.Printf("%s tools=%d in=%d cost=$%.3f (%s)\n", status, s.ToolCalls, s.InputTokens, s.CostUSD, s.GradeDetail)
			}
		}
	}

	sum := suite.Summary{
		SchemaVersion: 1,
		Directional:   true,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		Driver:        driver.Name(),
		Model:         c.model,
		AuthMode:      drivers.AuthMode(),
		FixtureRepo:   fixtureRepo(c.fixture),
		FixtureSHA:    fixtureSHA(c.fixture),
		Reps:          c.reps,
		IndexSeconds:  c.indexSeconds,
		Arms:          suite.Aggregate(sessions, armNames),
		Sessions:      sessions,
	}
	for _, s := range sessions {
		sum.TotalCostUSD += s.CostUSD
	}

	summaryPath := filepath.Join(c.out, ts+".summary.json")
	if err := writeJSON(summaryPath, sum); err != nil {
		return err
	}
	fmt.Printf("\nsummary: %s\n", summaryPath)
	fmt.Printf("total cost this run: $%.2f (%d sessions)\n", sum.TotalCostUSD, len(sessions))
	printSummaryTable(sum)
	fmt.Println("\nregenerate the README table with: go run ./bench --report-only")
	return nil
}

func runOne(d drivers.Driver, t suite.Task, arm drivers.Arm, rep int, transcript string, truth map[string]any) suite.Session {
	s := suite.Session{Task: t.ID, Arm: arm.Name, Rep: rep, Transcript: transcript}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	m, err := d.Run(ctx, t.Prompt, arm, transcript)
	s.ToolCalls = m.ToolCalls
	s.InputTokens = m.InputTokens
	s.OutputTokens = m.OutputTokens
	s.CacheReadTokens = m.CacheReadTokens
	s.WallClockMs = m.WallClockMs
	s.CostUSD = m.CostUSD
	s.OK = m.OK
	if err != nil {
		s.Error = err.Error()
		return s
	}
	answer, err := grade.ExtractJSONBlock(m.FinalAnswer)
	if err != nil {
		s.GradeDetail = "extract: " + err.Error()
		return s
	}
	res, err := grade.Grade(t.Grader, t.AnswerKey, answer, truth, t.Tolerance)
	if err != nil {
		s.GradeDetail = "grade: " + err.Error()
		return s
	}
	s.Pass = res.Pass
	s.Score = res.Score
	s.GradeDetail = res.Detail
	return s
}

func buildArms(names []string, c config) ([]drivers.Arm, error) {
	fixtureAbs, err := filepath.Abs(c.fixture)
	if err != nil {
		return nil, err
	}
	var resolvedMCP string
	for _, name := range names {
		if name == "codemap" {
			resolvedMCP, err = resolveMCPConfig(c.mcpConfig, c.out)
			if err != nil {
				return nil, err
			}
		}
	}
	var arms []drivers.Arm
	for _, name := range names {
		a := drivers.Arm{Name: name, WorkDir: fixtureAbs, Model: c.model}
		switch name {
		case "baseline":
			a.AllowedTools = "Read,Grep,Glob"
		case "codemap":
			a.AllowedTools = "Read,Grep,Glob,mcp__codemap"
			a.MCPConfig = resolvedMCP
			a.MCPServer = "codemap"
		default:
			return nil, fmt.Errorf("unknown arm %q (want baseline|codemap)", name)
		}
		arms = append(arms, a)
	}
	return arms, nil
}

// resolveMCPConfig expands ${CODEMAP_REPO} in the committed MCP template to the
// repo root (cwd) and writes a clean resolved copy under outDir. claude runs
// with cwd=fixture, so the codemap binary + data paths must be absolute.
func resolveMCPConfig(templatePath, outDir string) (string, error) {
	repoRoot, err := os.Getwd()
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(templatePath)
	if err != nil {
		return "", fmt.Errorf("read mcp template: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", fmt.Errorf("parse mcp template: %w", err)
	}
	delete(m, "_comment")
	clean, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", err
	}
	expanded := strings.ReplaceAll(string(clean), "${CODEMAP_REPO}", repoRoot)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", err
	}
	out := filepath.Join(outDir, "codemap.mcp.resolved.json")
	if err := os.WriteFile(out, []byte(expanded+"\n"), 0o644); err != nil {
		return "", err
	}
	// claude runs with cwd=fixture, so the --mcp-config path itself must be
	// absolute too — a relative path would resolve inside the fixture repo.
	return filepath.Abs(out)
}

func pickDriver(c config, tasks []suite.Task) (drivers.Driver, error) {
	if c.smoke {
		return drivers.NewSmokeDriver(tasks, func(t suite.Task) string {
			return filepath.Join(c.tasksDir, t.Truth)
		})
	}
	switch c.driver {
	case "claude":
		return drivers.ClaudeDriver{}, nil
	case "smoke":
		return drivers.NewSmokeDriver(tasks, func(t suite.Task) string {
			return filepath.Join(c.tasksDir, t.Truth)
		})
	case "codex":
		return drivers.CodexDriver{}, nil
	case "gemini":
		return drivers.GeminiDriver{}, nil
	default:
		return nil, fmt.Errorf("unknown driver %q", c.driver)
	}
}

func printPlan(tasks []suite.Task, arms []drivers.Arm, c config) error {
	fmt.Printf("DRY RUN — %d tasks × %d arms × %d reps = %d sessions\n", len(tasks), len(arms), c.reps, len(tasks)*len(arms)*c.reps)
	fmt.Printf("driver=%s model=%s fixture=%s\n\n", c.driver, c.model, c.fixture)
	cd := drivers.ClaudeDriver{}
	for _, t := range tasks {
		fmt.Printf("== %s [%s / grader=%s] ==\n", t.ID, t.Category, t.Grader)
		fmt.Printf("   prompt: %s\n", oneLine(t.Prompt))
		for _, arm := range arms {
			fmt.Printf("   arm %s: claude %s\n", arm.Name, strings.Join(cd.Args(t.Prompt, arm), " "))
		}
		fmt.Println()
	}
	return nil
}

func filterTasks(tasks []suite.Task, filter string) []suite.Task {
	if strings.TrimSpace(filter) == "" {
		return tasks
	}
	want := map[string]bool{}
	for _, f := range splitCSV(filter) {
		want[f] = true
	}
	var out []suite.Task
	for _, t := range tasks {
		if want[t.ID] || want[strings.SplitN(t.ID, "_", 2)[0]] {
			out = append(out, t)
		}
	}
	return out
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 120 {
		return s[:117] + "..."
	}
	return s
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}
