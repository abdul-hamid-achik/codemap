/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"testing"

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
