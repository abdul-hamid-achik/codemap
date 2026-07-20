package app

// This file computes the CI-gate signal carried on review/risk reports so a
// harness (the GitHub Action, a pre-commit hook, an MCP-driven pipeline) can
// reproduce the CLI --fail-on-risk/--fail-on-untested exit-6 logic from the
// report alone — without re-deriving the risk-level ordinal or the honesty
// rules. The CLI keeps its own gate evaluation for its exit code; these
// helpers are the canonical, report-attached form (D9).

// RiskLevelOrdinal maps a risk level to its position in the low < medium <
// high order, for --fail-on-risk threshold comparison. "unknown" (and empty)
// is ordinal 0 and therefore never satisfies a risk threshold — the honesty
// rule: a symbol whose call graph is unavailable must never be treated as
// "at least as risky as low".
func RiskLevelOrdinal(level string) int {
	switch level {
	case "low":
		return 1
	case "medium":
		return 2
	case "high":
		return 3
	default: // "unknown" or empty
		return 0
	}
}

// RiskGateTrips reports whether a risk level meets or exceeds a --fail-on-risk
// threshold. "unknown"/empty never trips.
func RiskGateTrips(level string, threshold int) bool {
	if level == "" || level == "unknown" {
		return false
	}
	return RiskLevelOrdinal(level) >= threshold
}

// RiskThresholds encodes, for each --fail-on-risk threshold, whether a risk
// level would trip it.
type RiskThresholds struct {
	Low    bool `json:"low"`
	Medium bool `json:"medium"`
	High   bool `json:"high"`
}

func riskThresholds(level string) RiskThresholds {
	return RiskThresholds{
		Low:    RiskGateTrips(level, RiskLevelOrdinal("low")),
		Medium: RiskGateTrips(level, RiskLevelOrdinal("medium")),
		High:   RiskGateTrips(level, RiskLevelOrdinal("high")),
	}
}

// ReviewGate is the computed CI-gate signal on a review report.
type ReviewGate struct {
	RiskLevel        string              `json:"risk_level"`        // aggregate risk level (unknown|low|medium|high; "" when no risk band)
	AnalysisComplete bool                `json:"analysis_complete"` // whether the indexed review is complete
	WouldFailOn      ReviewGateWouldFail `json:"would_fail_on"`
}

// ReviewGateWouldFail breaks down the conditions that trip a review gate.
type ReviewGateWouldFail struct {
	// IncompleteAnalysis: an indexed repo review that isn't complete fails
	// closed when ANY review gate is enabled.
	IncompleteAnalysis bool `json:"incomplete_analysis"`
	// Untested: --fail-on-untested would trip (untested symbols present, or
	// test coverage unresolved on a non-empty diff).
	Untested bool `json:"untested"`
	// RiskAtOrAbove: --fail-on-risk would trip at each threshold.
	RiskAtOrAbove RiskThresholds `json:"risk_at_or_above"`
}

// ComputeGate derives the CI-gate signal from a finalized review report.
func (rep *ReviewReport) ComputeGate() *ReviewGate {
	if rep == nil {
		return nil
	}
	level := ""
	if rep.Risk != nil {
		level = rep.Risk.Level
	}
	return &ReviewGate{
		RiskLevel:        level,
		AnalysisComplete: rep.AnalysisComplete,
		WouldFailOn: ReviewGateWouldFail{
			IncompleteAnalysis: rep.IsRepo && rep.Indexed && !rep.AnalysisComplete,
			Untested:           len(rep.UntestedSymbols) > 0 || rep.testCoverageUnresolved(),
			RiskAtOrAbove:      riskThresholds(level),
		},
	}
}

// testCoverageUnresolved reports whether a non-empty diff has unresolved test
// coverage (call graph not resolved/name) — the second --fail-on-untested
// condition.
func (rep *ReviewReport) testCoverageUnresolved() bool {
	if rep.TotalSymbols == 0 {
		return false
	}
	switch rep.CallGraph {
	case CallGraphResolved, CallGraphName:
		return false
	default: // unresolved, none, or absent compatibility metadata
		return true
	}
}

// RiskGate is the computed --fail-on-risk signal on a risk report.
type RiskGate struct {
	Level       string         `json:"level"`
	WouldFailOn RiskThresholds `json:"would_fail_on"`
}

// ComputeGate derives the --fail-on-risk signal from a risk report.
func (r *RiskReport) ComputeGate() *RiskGate {
	if r == nil {
		return nil
	}
	return &RiskGate{Level: r.Level, WouldFailOn: riskThresholds(r.Level)}
}
