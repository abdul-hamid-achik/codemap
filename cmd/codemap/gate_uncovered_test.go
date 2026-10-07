/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/app"
)

// TestUncoveredGate pins the --fail-on-uncovered contract: it trips only on an
// uncovered/partial verdict that has known-uncovered symbols, never on unknown
// coverage, and it never changes --fail-on-untested (which still fails closed on
// an unresolved call graph).
func TestUncoveredGate(t *testing.T) {
	mk := func(verdict string, covered, uncovered, unknown int) *app.ReviewReport {
		return &app.ReviewReport{
			IsRepo: true, Indexed: true, AnalysisComplete: true,
			TotalSymbols: covered + uncovered + unknown, CallGraph: app.CallGraphUnresolved,
			Risk:     &app.ReviewRisk{Level: "unknown", Factors: []app.RiskFactor{}},
			Coverage: &app.ReviewCoverage{Verdict: verdict, CoveredSymbols: covered, UncoveredSymbols: uncovered, UnknownSymbols: unknown},
		}
	}
	cases := []struct {
		name string
		rep  *app.ReviewReport
		trip bool
	}{
		{"uncovered trips", mk(app.CoverageUncovered, 0, 2, 0), true},
		{"partial with uncovered trips", mk(app.CoveragePartial, 1, 1, 0), true},
		{"partial of covered and unknown does not trip", mk(app.CoveragePartial, 1, 0, 1), false},
		{"unknown never trips", mk(app.CoverageUnknown, 0, 0, 3), false},
		{"covered does not trip", mk(app.CoverageCovered, 2, 0, 0), false},
		{"no coverage block does not trip", &app.ReviewReport{IsRepo: true, Indexed: true, AnalysisComplete: true}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := uncoveredGateResult(tc.rep, true)
			if tc.trip && got != errGate {
				t.Fatalf("gate = %v, want gate failure", got)
			}
			if !tc.trip && got != nil {
				t.Fatalf("gate = %v, want nil", got)
			}
			if got := uncoveredGateResult(tc.rep, false); got != nil {
				t.Fatalf("disabled gate = %v, want nil", got)
			}
		})
	}

	// Fail closed: an indexed git review that is not a complete analysis
	// (truncated at the 200-symbol cap, failed/partial, or stale) proves nothing
	// about coverage, so the enabled gate trips even though the verdict alone
	// (here: unknown, or covered-only) would not.
	incomplete := func(verdict string, covered, uncovered, unknown int) *app.ReviewReport {
		rep := mk(verdict, covered, uncovered, unknown)
		rep.AnalysisComplete = false
		return rep
	}
	for name, rep := range map[string]*app.ReviewReport{
		"truncated unknown":   incomplete(app.CoverageUnknown, 0, 0, 3),
		"truncated covered":   incomplete(app.CoverageCovered, 2, 0, 0),
		"incomplete partial":  incomplete(app.CoveragePartial, 1, 0, 1),
		"no coverage block":   {IsRepo: true, Indexed: true, AnalysisComplete: false},
		"stale-style failure": incomplete(app.CoverageUncovered, 0, 1, 0),
	} {
		if got := uncoveredGateResult(rep, true); got != errGate {
			t.Errorf("%s: gate = %v, want gate failure (fail closed on incomplete analysis)", name, got)
		}
		if got := uncoveredGateResult(rep, false); got != nil {
			t.Errorf("%s: disabled gate = %v, want nil", name, got)
		}
	}
	// Early non-repo / unindexed degradation stays non-blocking, and a complete
	// analysis with genuine unknown coverage still never trips.
	for name, rep := range map[string]*app.ReviewReport{
		"not a repo":  {IsRepo: false},
		"not indexed": {IsRepo: true, Indexed: false},
	} {
		if got := uncoveredGateResult(rep, true); got != nil {
			t.Errorf("%s: gate = %v, want nil", name, got)
		}
	}
	if got := uncoveredGateResult(mk(app.CoverageUnknown, 0, 0, 3), true); got != nil {
		t.Errorf("complete analysis with unknown coverage = %v, want nil", got)
	}

	// Back-compat: --fail-on-untested keeps failing on an unresolved call graph
	// even when the coverage verdict is unknown.
	unknown := mk(app.CoverageUnknown, 0, 0, 1)
	if got := reviewGateResult(unknown, false, 0, true); got != errGate {
		t.Fatalf("--fail-on-untested on unresolved coverage = %v, want gate failure (unchanged)", got)
	}
	if got := uncoveredGateResult(unknown, true); got != nil {
		t.Fatalf("--fail-on-uncovered on unknown coverage = %v, want nil", got)
	}
	if nil != uncoveredGateResult(nil, true) {
		t.Fatal("nil report must not trip")
	}
}
