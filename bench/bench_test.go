package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/codemap/bench/drivers"
	"github.com/abdul-hamid-achik/codemap/bench/grade"
	"github.com/abdul-hamid-achik/codemap/bench/suite"
)

const tasksDir = "tasks"

// TestTasksIntegrity guards against a half-authored task: every task file must
// parse, name a known grader + answer key, and reference a truth file that
// exists, parses, and carries the graded key.
func TestTasksIntegrity(t *testing.T) {
	tasks, err := suite.LoadTasks(tasksDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) < 8 {
		t.Fatalf("expected >= 8 tasks, got %d", len(tasks))
	}
	seen := map[string]bool{}
	for _, tk := range tasks {
		if seen[tk.ID] {
			t.Errorf("duplicate task id %s", tk.ID)
		}
		seen[tk.ID] = true
		if !grade.KnownGrader(tk.Grader) {
			t.Errorf("%s: unknown grader %q", tk.ID, tk.Grader)
		}
		if tk.AnswerKey == "" {
			t.Errorf("%s: empty answer_key", tk.ID)
		}
		if !strings.Contains(tk.Prompt, "json") {
			t.Errorf("%s: prompt should instruct a json answer", tk.ID)
		}
		truth, err := suite.LoadTruth(filepath.Join(tasksDir, tk.Truth))
		if err != nil {
			t.Errorf("%s: truth %s: %v", tk.ID, tk.Truth, err)
			continue
		}
		switch tk.Grader {
		case "set_equal", "numeric":
			if _, ok := truth[tk.AnswerKey]; !ok {
				t.Errorf("%s: truth missing answer_key %q", tk.ID, tk.AnswerKey)
			}
		case "contains_path":
			if _, ok := truth["edges"]; !ok {
				t.Errorf("%s: contains_path truth missing edges", tk.ID)
			}
		case "exact":
			if len(truth) == 0 {
				t.Errorf("%s: exact truth is empty", tk.ID)
			}
		}
	}
}

// TestSmokePipeline exercises drivers → grade → aggregate offline (no API): the
// smoke driver echoes the truth, so every task must grade PASS and aggregate.
func TestSmokePipeline(t *testing.T) {
	tasks, err := suite.LoadTasks(tasksDir)
	if err != nil {
		t.Fatal(err)
	}
	d, err := drivers.NewSmokeDriver(tasks, func(tk suite.Task) string {
		return filepath.Join(tasksDir, tk.Truth)
	})
	if err != nil {
		t.Fatal(err)
	}
	var sessions []suite.Session
	for _, tk := range tasks {
		truth, err := suite.LoadTruth(filepath.Join(tasksDir, tk.Truth))
		if err != nil {
			t.Fatal(err)
		}
		for _, armName := range []string{"baseline", "codemap"} {
			arm := drivers.Arm{Name: armName, Model: "smoke"}
			m, err := d.Run(context.Background(), tk.Prompt, arm, "")
			if err != nil {
				t.Fatalf("%s/%s: %v", tk.ID, armName, err)
			}
			ans, err := grade.ExtractJSONBlock(m.FinalAnswer)
			if err != nil {
				t.Fatalf("%s/%s: extract: %v", tk.ID, armName, err)
			}
			res, err := grade.Grade(tk.Grader, tk.AnswerKey, ans, truth, tk.Tolerance)
			if err != nil {
				t.Fatalf("%s/%s: grade: %v", tk.ID, armName, err)
			}
			if !res.Pass {
				t.Errorf("%s/%s: smoke answer should pass, got %s", tk.ID, armName, res.Detail)
			}
			sessions = append(sessions, suite.Session{Task: tk.ID, Arm: armName, ToolCalls: m.ToolCalls, InputTokens: m.InputTokens, WallClockMs: m.WallClockMs, CostUSD: m.CostUSD, Pass: res.Pass})
		}
	}
	arms := suite.Aggregate(sessions, []string{"baseline", "codemap"})
	base := findArm(arms, "baseline")
	cm := findArm(arms, "codemap")
	if base == nil || cm == nil {
		t.Fatal("expected both arms in aggregate")
	}
	if cm.ToolCalls.Mean >= base.ToolCalls.Mean {
		t.Errorf("smoke codemap arm should use fewer tool calls: base=%.1f codemap=%.1f", base.ToolCalls.Mean, cm.ToolCalls.Mean)
	}
	if cm.TasksCorrect != cm.TasksTotal {
		t.Errorf("all smoke tasks should be correct: %d/%d", cm.TasksCorrect, cm.TasksTotal)
	}
}

func TestSpliceTable_IdempotentAndDirectional(t *testing.T) {
	sum := suite.Summary{
		SchemaVersion: 1, Directional: true, GeneratedAt: "2026-07-11T00:00:00Z",
		Model: "claude-sonnet-5", FixtureRepo: "go-git/go-git", FixtureSHA: "48a1ae05eec4fff", Reps: 3,
		Arms: []suite.ArmSummary{
			{Arm: "baseline", ToolCalls: suite.Stat{Mean: 14.2, Std: 3.1}, InputTokens: suite.Stat{Mean: 38000, Std: 9000}, WallClockS: suite.Stat{Mean: 41, Std: 11}, CostUSD: suite.Stat{Mean: 0.12, Std: 0.03}, TasksCorrect: 7, TasksTotal: 10},
			{Arm: "codemap", ToolCalls: suite.Stat{Mean: 3.4, Std: 0.8}, InputTokens: suite.Stat{Mean: 11000, Std: 2000}, WallClockS: suite.Stat{Mean: 12, Std: 3}, CostUSD: suite.Stat{Mean: 0.05, Std: 0.01}, TasksCorrect: 9, TasksTotal: 10},
		},
	}
	readme := "intro\n" + markerStart + "\nOLD\n" + markerEnd + "\ntail\n"
	once, err := SpliceTable(readme, sum)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(once, "DIRECTIONAL") {
		t.Error("table must carry the DIRECTIONAL banner")
	}
	if !strings.Contains(once, "tool calls") || !strings.Contains(once, "14.2 ± 3.1") {
		t.Errorf("table missing expected rows:\n%s", once)
	}
	if strings.Contains(once, "OLD") {
		t.Error("old content between markers should be replaced")
	}
	if !strings.Contains(once, "intro") || !strings.Contains(once, "tail") {
		t.Error("content outside markers must be preserved")
	}
	twice, err := SpliceTable(once, sum)
	if err != nil {
		t.Fatal(err)
	}
	if once != twice {
		t.Error("SpliceTable should be idempotent")
	}
}

func TestSpliceTable_MissingMarkers(t *testing.T) {
	if _, err := SpliceTable("no markers here", suite.Summary{}); err == nil {
		t.Fatal("expected error when markers are absent")
	}
}
