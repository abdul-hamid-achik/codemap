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

func TestAggregate_MCPToolCallsAndSessions(t *testing.T) {
	sessions := []Session{
		// codemap: one session calls MCP tools twice, one calls none, and one
		// errors out with 5 MCP calls recorded before the failure — the errored
		// session must be excluded from BOTH the MCPToolCalls stat pool and the
		// MCPSessions "used codemap tools" count (its transcript is incomplete).
		{Task: "A", Arm: "codemap", ToolCalls: 4, MCPToolCalls: 2, Pass: true},
		{Task: "B", Arm: "codemap", ToolCalls: 3, MCPToolCalls: 0, Pass: true},
		{Task: "C", Arm: "codemap", ToolCalls: 1, MCPToolCalls: 5, Error: "boom"},
		// baseline never has MCP tools available at all.
		{Task: "A", Arm: "baseline", ToolCalls: 12, MCPToolCalls: 0, Pass: false},
	}
	arms := Aggregate(sessions, []string{"baseline", "codemap"})
	base := arms[0]
	cm := arms[1]

	if cm.Sessions != 3 {
		t.Errorf("codemap sessions = %d, want 3 (incl. the failed one)", cm.Sessions)
	}
	if cm.Failed != 1 {
		t.Errorf("codemap failed = %d, want 1", cm.Failed)
	}
	// Stat pool excludes the failed session: mean of {2, 0} = 1, n=2.
	if cm.MCPToolCalls.N != 2 || cm.MCPToolCalls.Mean != 1 {
		t.Errorf("codemap MCPToolCalls = %+v, want mean=1 n=2", cm.MCPToolCalls)
	}
	// Only the one non-failed session with >=1 MCP call counts.
	if cm.MCPSessions != 1 {
		t.Errorf("codemap MCPSessions = %d, want 1", cm.MCPSessions)
	}

	if base.MCPToolCalls.Mean != 0 || base.MCPToolCalls.N != 1 {
		t.Errorf("baseline MCPToolCalls = %+v, want mean=0 n=1", base.MCPToolCalls)
	}
	if base.MCPSessions != 0 {
		t.Errorf("baseline MCPSessions = %d, want 0", base.MCPSessions)
	}
}
