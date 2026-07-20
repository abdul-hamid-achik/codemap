package app

import "testing"

func TestRiskLevelOrdinal(t *testing.T) {
	for _, tc := range []struct {
		level string
		want  int
	}{
		{"low", 1}, {"medium", 2}, {"high", 3}, {"unknown", 0}, {"", 0}, {"bogus", 0},
	} {
		if got := RiskLevelOrdinal(tc.level); got != tc.want {
			t.Errorf("RiskLevelOrdinal(%q) = %d, want %d", tc.level, got, tc.want)
		}
	}
}

func TestRiskGateTrips(t *testing.T) {
	// unknown/empty never trips any threshold (the honesty rule).
	for _, threshold := range []int{1, 2, 3} {
		if RiskGateTrips("unknown", threshold) || RiskGateTrips("", threshold) {
			t.Errorf("unknown/empty must never trip threshold %d", threshold)
		}
	}
	// high meets every threshold; low meets only low; medium meets low+medium.
	if !RiskGateTrips("high", RiskLevelOrdinal("low")) || !RiskGateTrips("high", RiskLevelOrdinal("high")) {
		t.Error("high must trip low and high thresholds")
	}
	if !RiskGateTrips("low", RiskLevelOrdinal("low")) || RiskGateTrips("low", RiskLevelOrdinal("medium")) {
		t.Error("low must trip low but not medium")
	}
	if !RiskGateTrips("medium", RiskLevelOrdinal("medium")) || RiskGateTrips("medium", RiskLevelOrdinal("high")) {
		t.Error("medium must trip medium but not high")
	}
}

func TestReviewComputeGate(t *testing.T) {
	// An incomplete indexed review fails closed; untested symbols trip
	// --fail-on-untested; a high aggregate risk trips every --fail-on-risk level.
	rep := &ReviewReport{
		IsRepo: true, Indexed: true, AnalysisComplete: false,
		UntestedSymbols: []SymbolRef{{Symbol: "Run"}},
		Risk:            &ReviewRisk{Level: "high"},
	}
	g := rep.ComputeGate()
	if g == nil {
		t.Fatal("ComputeGate returned nil")
	}
	if !g.WouldFailOn.IncompleteAnalysis {
		t.Error("incomplete indexed review must fail closed")
	}
	if !g.WouldFailOn.Untested {
		t.Error("untested symbols must trip --fail-on-untested")
	}
	if !g.WouldFailOn.RiskAtOrAbove.Low || !g.WouldFailOn.RiskAtOrAbove.Medium || !g.WouldFailOn.RiskAtOrAbove.High {
		t.Errorf("high risk must trip every threshold, got %+v", g.WouldFailOn.RiskAtOrAbove)
	}
	if g.RiskLevel != "high" || g.AnalysisComplete {
		t.Errorf("gate fields = level %q complete %v", g.RiskLevel, g.AnalysisComplete)
	}
}

func TestReviewComputeGateUnknownRiskNeverTrips(t *testing.T) {
	rep := &ReviewReport{
		IsRepo: true, Indexed: true, AnalysisComplete: true,
		TotalSymbols: 1, CallGraph: CallGraphResolved,
		Risk: &ReviewRisk{Level: "unknown"},
	}
	g := rep.ComputeGate()
	if g.WouldFailOn.IncompleteAnalysis {
		t.Error("a complete review must not fail closed")
	}
	if g.WouldFailOn.Untested {
		t.Error("resolved coverage with no untested symbols must not trip --fail-on-untested")
	}
	if g.WouldFailOn.RiskAtOrAbove.Low || g.WouldFailOn.RiskAtOrAbove.Medium || g.WouldFailOn.RiskAtOrAbove.High {
		t.Errorf("unknown risk must trip no threshold, got %+v", g.WouldFailOn.RiskAtOrAbove)
	}
}

func TestReviewComputeGateUnresolvedCoverageTripsUntested(t *testing.T) {
	// A non-empty diff whose call graph is unresolved trips --fail-on-untested
	// even with no explicit untested symbols (coverage can't be confirmed).
	rep := &ReviewReport{
		IsRepo: true, Indexed: true, AnalysisComplete: true,
		TotalSymbols: 2, CallGraph: CallGraphUnresolved,
	}
	if !rep.ComputeGate().WouldFailOn.Untested {
		t.Error("unresolved coverage on a non-empty diff must trip --fail-on-untested")
	}
}

func TestRiskComputeGate(t *testing.T) {
	if g := (&RiskReport{Level: "high"}).ComputeGate(); !g.WouldFailOn.Low || !g.WouldFailOn.High {
		t.Errorf("high risk gate = %+v, want all thresholds tripped", g.WouldFailOn)
	}
	if g := (&RiskReport{Level: "unknown"}).ComputeGate(); g.WouldFailOn.Low || g.WouldFailOn.Medium || g.WouldFailOn.High {
		t.Errorf("unknown risk gate = %+v, want no threshold tripped", g.WouldFailOn)
	}
}
