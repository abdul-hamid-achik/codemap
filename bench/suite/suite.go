// Package suite holds the shared, dependency-light data types for the codemap
// agent benchmark harness: task definitions, per-session results, aggregated
// summaries, and the small statistics helpers used to report mean ± σ.
//
// This package deliberately imports nothing from internal/* (and never codemap
// itself) so that neither the harness nor the ground-truth tooling can create a
// circular dependency on the thing being benchmarked. See bench/README.md.
package suite

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Task is one benchmark question, loaded from bench/tasks/NN_slug.json.
type Task struct {
	ID        string `json:"id"`
	Category  string `json:"category"` // orient | locate | understand | verify
	Prompt    string `json:"prompt"`
	Grader    string `json:"grader"`     // set_equal | exact | numeric | contains_path
	AnswerKey string `json:"answer_key"` // key in the agent's JSON answer to grade
	Truth     string `json:"truth"`      // path (relative to tasks dir) of the truth JSON
	// Tolerance is optional; used by the numeric grader (default 0).
	Tolerance float64 `json:"tolerance,omitempty"`
}

// LoadTasks reads every NN_*.json under dir (non-recursively), skipping any
// subdirectories (truth/, patches/), and returns them sorted by filename.
func LoadTasks(dir string) ([]Task, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		files = append(files, name)
	}
	sort.Strings(files)
	var tasks []Task
	for _, name := range files {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		var t Task
		if err := json.Unmarshal(raw, &t); err != nil {
			return nil, fmt.Errorf("parse task %s: %w", name, err)
		}
		if t.ID == "" {
			return nil, fmt.Errorf("task %s has no id", name)
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}

// LoadTruth reads and parses a truth JSON file into a generic map.
func LoadTruth(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parse truth %s: %w", path, err)
	}
	return m, nil
}

// Session is the record of a single agent run (one task × arm × repetition).
type Session struct {
	Task            string  `json:"task"`
	Arm             string  `json:"arm"`
	Rep             int     `json:"rep"`
	ToolCalls       int     `json:"tool_calls"`
	InputTokens     int     `json:"input_tokens"`
	OutputTokens    int     `json:"output_tokens"`
	CacheReadTokens int     `json:"cache_read_tokens"`
	WallClockMs     int64   `json:"wall_clock_ms"`
	CostUSD         float64 `json:"cost_usd"`
	Pass            bool    `json:"pass"`
	Score           float64 `json:"score"`
	GradeDetail     string  `json:"grade_detail,omitempty"`
	OK              bool    `json:"ok"` // driver reported a successful session
	Error           string  `json:"error,omitempty"`
	Transcript      string  `json:"transcript,omitempty"` // path to the raw stream-json artifact
}

// Stat is mean ± σ for one metric.
type Stat struct {
	Mean float64 `json:"mean"`
	Std  float64 `json:"std"`
	N    int     `json:"n"`
}

// ArmSummary aggregates every session for one arm.
type ArmSummary struct {
	Arm          string `json:"arm"`
	ToolCalls    Stat   `json:"tool_calls"`
	InputTokens  Stat   `json:"input_tokens"`
	OutputTokens Stat   `json:"output_tokens"`
	WallClockS   Stat   `json:"wall_clock_s"`
	CostUSD      Stat   `json:"cost_usd"`
	TasksCorrect int    `json:"tasks_correct"` // distinct tasks passing in a majority of reps
	TasksTotal   int    `json:"tasks_total"`
	Sessions     int    `json:"sessions"`
}

// Summary is the committed artifact: metadata + per-arm aggregates + raw
// sessions. report.go turns the newest one into the README table.
type Summary struct {
	SchemaVersion int          `json:"schema_version"`
	Directional   bool         `json:"directional"`
	GeneratedAt   string       `json:"generated_at"`
	Driver        string       `json:"driver"`
	Model         string       `json:"model"`
	FixtureRepo   string       `json:"fixture_repo"`
	FixtureSHA    string       `json:"fixture_sha"`
	Reps          int          `json:"reps"`
	IndexSeconds  float64      `json:"index_seconds"` // one-time codemap index cost, reported separately
	TotalCostUSD  float64      `json:"total_cost_usd"`
	Arms          []ArmSummary `json:"arms"`
	Sessions      []Session    `json:"sessions"`
}

// Aggregate folds raw sessions into per-arm summaries. armOrder fixes the arm
// ordering in the output (e.g. baseline before codemap).
func Aggregate(sessions []Session, armOrder []string) []ArmSummary {
	byArm := map[string][]Session{}
	for _, s := range sessions {
		byArm[s.Arm] = append(byArm[s.Arm], s)
	}
	seen := map[string]bool{}
	for _, a := range armOrder {
		seen[a] = true
	}
	var extra []string
	for a := range byArm {
		if !seen[a] {
			extra = append(extra, a)
		}
	}
	sort.Strings(extra)
	order := append(append([]string{}, armOrder...), extra...)

	var out []ArmSummary
	for _, arm := range order {
		ss := byArm[arm]
		if len(ss) == 0 {
			continue
		}
		var tc, in, outp, wall, cost []float64
		type pc struct{ pass, total int }
		perTask := map[string]*pc{}
		for _, s := range ss {
			tc = append(tc, float64(s.ToolCalls))
			in = append(in, float64(s.InputTokens))
			outp = append(outp, float64(s.OutputTokens))
			wall = append(wall, float64(s.WallClockMs)/1000.0)
			cost = append(cost, s.CostUSD)
			p := perTask[s.Task]
			if p == nil {
				p = &pc{}
				perTask[s.Task] = p
			}
			p.total++
			if s.Pass {
				p.pass++
			}
		}
		correct := 0
		for _, p := range perTask {
			if p.pass*2 >= p.total { // majority of reps correct
				correct++
			}
		}
		out = append(out, ArmSummary{
			Arm:          arm,
			ToolCalls:    MeanStd(tc),
			InputTokens:  MeanStd(in),
			OutputTokens: MeanStd(outp),
			WallClockS:   MeanStd(wall),
			CostUSD:      MeanStd(cost),
			TasksCorrect: correct,
			TasksTotal:   len(perTask),
			Sessions:     len(ss),
		})
	}
	return out
}

// MeanStd returns the mean and population standard deviation of xs.
func MeanStd(xs []float64) Stat {
	n := len(xs)
	if n == 0 {
		return Stat{}
	}
	var sum float64
	for _, x := range xs {
		sum += x
	}
	mean := sum / float64(n)
	var ss float64
	for _, x := range xs {
		d := x - mean
		ss += d * d
	}
	return Stat{Mean: mean, Std: math.Sqrt(ss / float64(n)), N: n}
}
