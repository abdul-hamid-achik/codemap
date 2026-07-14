/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/app"
	"github.com/spf13/cobra"
)

// TestRiskGateTripsThresholdTable pins the ordinal comparison behind
// --fail-on-risk: a level trips a threshold when its ordinal is >= the
// threshold's ordinal (low < medium < high), and "unknown" NEVER trips
// regardless of threshold — the honesty rule (an unresolved call graph must
// never be treated as "at least as risky as low").
func TestRiskGateTripsThresholdTable(t *testing.T) {
	cases := []struct {
		level     string
		threshold string
		want      bool
	}{
		// unknown never trips, at any threshold.
		{"unknown", "low", false},
		{"unknown", "medium", false},
		{"unknown", "high", false},
		{"", "low", false},

		// --fail-on-risk=low trips low/medium/high.
		{"low", "low", true},
		{"medium", "low", true},
		{"high", "low", true},

		// --fail-on-risk=medium trips medium/high, not low.
		{"low", "medium", false},
		{"medium", "medium", true},
		{"high", "medium", true},

		// --fail-on-risk=high trips only high.
		{"low", "high", false},
		{"medium", "high", false},
		{"high", "high", true},
	}
	for _, tc := range cases {
		threshold := riskLevelOrdinal(tc.threshold)
		got := riskGateTrips(tc.level, threshold)
		if got != tc.want {
			t.Errorf("riskGateTrips(%q, ordinal(%q)=%d) = %v, want %v", tc.level, tc.threshold, threshold, got, tc.want)
		}
	}
}

func TestParseFailOnRiskFlag(t *testing.T) {
	newCmd := func() *cobra.Command {
		c := &cobra.Command{}
		c.Flags().String("fail-on-risk", "", "")
		return c
	}

	t.Run("unset means no gate", func(t *testing.T) {
		threshold, set, err := parseFailOnRiskFlag(newCmd())
		if err != nil || set || threshold != 0 {
			t.Fatalf("parseFailOnRiskFlag(unset) = (%d, %v, %v), want (0, false, nil)", threshold, set, err)
		}
	})

	for _, level := range []string{"low", "medium", "high"} {
		t.Run(level, func(t *testing.T) {
			c := newCmd()
			if err := c.Flags().Set("fail-on-risk", level); err != nil {
				t.Fatal(err)
			}
			threshold, set, err := parseFailOnRiskFlag(c)
			if err != nil || !set || threshold != riskLevelOrdinal(level) {
				t.Fatalf("parseFailOnRiskFlag(%q) = (%d, %v, %v)", level, threshold, set, err)
			}
		})
	}

	t.Run("invalid value is an operational error", func(t *testing.T) {
		c := newCmd()
		if err := c.Flags().Set("fail-on-risk", "critical"); err != nil {
			t.Fatal(err)
		}
		_, set, err := parseFailOnRiskFlag(c)
		if err == nil || set {
			t.Fatalf("parseFailOnRiskFlag(critical) = (set=%v, err=%v), want an error", set, err)
		}
	})
}

// TestGateExitBypassesJSONFailureEnvelope pins the jsonHandler special case:
// a gateExit must become exitGateFailed WITHOUT going through jsonFailure (no
// {"ok":false,...} envelope — the gate is exit-code-only).
func TestGateExitBypassesJSONFailureEnvelope(t *testing.T) {
	c := &cobra.Command{}
	c.Flags().Bool("json", true, "")
	handler := jsonHandler(func(cmd *cobra.Command, args []string) error {
		return errGate
	})
	err := handler(c, nil)
	code, ok := asExitCoded(err)
	if !ok || code != exitGateFailed {
		t.Fatalf("gate exit = (code=%d, ok=%v), want (%d, true)", code, ok, exitGateFailed)
	}
	if err.Error() != "" {
		t.Fatalf("gate exit error text = %q, want empty (no envelope, no cobra echo)", err.Error())
	}
}

func TestReviewGateFailsClosedOnlyForFinalizedIndexedReports(t *testing.T) {
	incomplete := &app.ReviewReport{
		IsRepo: true, Indexed: true, AnalysisComplete: false,
		Risk: &app.ReviewRisk{Level: "unknown", Factors: []app.RiskFactor{}},
	}
	if got := reviewGateResult(incomplete, true, riskLevelOrdinal("high"), false); got != errGate {
		t.Fatalf("incomplete indexed risk gate = %v, want gate failure", got)
	}
	if got := reviewGateResult(incomplete, false, 0, true); got != errGate {
		t.Fatalf("incomplete indexed untested gate = %v, want gate failure", got)
	}
	if got := reviewGateResult(incomplete, false, 0, false); got != nil {
		t.Fatalf("reporting-only incomplete review = %v, want nil", got)
	}

	for name, early := range map[string]*app.ReviewReport{
		"not indexed": {IsRepo: true, Indexed: false, AnalysisComplete: false},
		"not a repo":  {IsRepo: false, Indexed: false, AnalysisComplete: false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := reviewGateResult(early, true, riskLevelOrdinal("low"), true); got != nil {
				t.Fatalf("early graceful review gate = %v, want nil", got)
			}
		})
	}

	completeUnknown := &app.ReviewReport{
		IsRepo: true, Indexed: true, AnalysisComplete: true, TotalSymbols: 1,
		CallGraph: app.CallGraphUnresolved,
		Risk:      &app.ReviewRisk{Level: "unknown", Factors: []app.RiskFactor{}},
	}
	if got := reviewGateResult(completeUnknown, true, riskLevelOrdinal("low"), false); got != nil {
		t.Fatalf("complete unresolved review risk gate = %v, want nil", got)
	}
	if got := reviewGateResult(completeUnknown, false, 0, true); got != errGate {
		t.Fatalf("complete unresolved review untested gate = %v, want gate failure", got)
	}

	completeEmpty := &app.ReviewReport{
		IsRepo: true, Indexed: true, AnalysisComplete: true, TotalSymbols: 0,
	}
	if got := reviewGateResult(completeEmpty, false, 0, true); got != nil {
		t.Fatalf("complete zero-symbol review untested gate = %v, want nil", got)
	}
}
