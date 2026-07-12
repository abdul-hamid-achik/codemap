package suite

import (
	"math"
	"testing"
)

func TestMeanStd(t *testing.T) {
	s := MeanStd([]float64{2, 4, 6})
	if s.Mean != 4 {
		t.Errorf("mean = %v, want 4", s.Mean)
	}
	// population std of {2,4,6} = sqrt(8/3) ≈ 1.633
	if math.Abs(s.Std-1.632993) > 1e-4 {
		t.Errorf("std = %v, want ~1.633", s.Std)
	}
	if s.N != 3 {
		t.Errorf("n = %d, want 3", s.N)
	}
	if empty := MeanStd(nil); empty.N != 0 {
		t.Errorf("empty should have n=0, got %+v", empty)
	}
}

func TestAggregate_TasksCorrectMajority(t *testing.T) {
	sessions := []Session{
		// task A: 2/3 reps pass in codemap -> counts as correct
		{Task: "A", Arm: "codemap", ToolCalls: 3, Pass: true},
		{Task: "A", Arm: "codemap", ToolCalls: 4, Pass: true},
		{Task: "A", Arm: "codemap", ToolCalls: 5, Pass: false},
		// task B: 1/3 -> not correct
		{Task: "B", Arm: "codemap", ToolCalls: 3, Pass: true},
		{Task: "B", Arm: "codemap", ToolCalls: 3, Pass: false},
		{Task: "B", Arm: "codemap", ToolCalls: 3, Pass: false},
		{Task: "A", Arm: "baseline", ToolCalls: 20, Pass: false},
	}
	arms := Aggregate(sessions, []string{"baseline", "codemap"})
	if len(arms) != 2 {
		t.Fatalf("want 2 arms, got %d", len(arms))
	}
	if arms[0].Arm != "baseline" || arms[1].Arm != "codemap" {
		t.Errorf("arm order not preserved: %v", arms)
	}
	cm := arms[1]
	if cm.TasksTotal != 2 || cm.TasksCorrect != 1 {
		t.Errorf("codemap tasks correct = %d/%d, want 1/2", cm.TasksCorrect, cm.TasksTotal)
	}
	if cm.Sessions != 6 {
		t.Errorf("codemap sessions = %d, want 6", cm.Sessions)
	}
}
