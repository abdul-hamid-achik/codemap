/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// exitGateFailed extends the exit-code taxonomy in errors.go with code 6: a
// --fail-on-risk/--fail-on-untested threshold tripped on `review` or `risk`.
// It is deliberately distinct from every other non-zero code because it is
// NOT a query failure — the command answered normally (the human or --json
// output already printed unchanged); the gate only changes the process exit
// code so a script (a pre-commit hook, a CI step) can block on it without
// re-parsing output it already has.
const exitGateFailed = 6

// riskLevelOrdinal maps a risk level to its position in the low < medium < high
// order, for --fail-on-risk threshold comparison. "unknown" (and anything
// unrecognized) is ordinal 0 and therefore never satisfies a risk threshold —
// the honesty rule from internal/app/risk.go: a symbol whose call graph is
// unavailable must never be treated as "at least as risky as low". Review has
// a separate fail-closed completeness check before this comparison whenever a
// review gate is enabled (see reviewGateResult in query.go).
func riskLevelOrdinal(level string) int {
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

// parseFailOnRiskFlag validates --fail-on-risk's value. An empty value means
// the gate is disabled (set=false). A non-empty value must be one of
// low/medium/high; anything else is an operational error (exit 1) raised
// before any query work happens, matching the CLI's usual fail-fast behavior
// for a bad flag.
func parseFailOnRiskFlag(cmd *cobra.Command) (threshold int, set bool, err error) {
	v, _ := cmd.Flags().GetString("fail-on-risk")
	if v == "" {
		return 0, false, nil
	}
	switch v {
	case "low", "medium", "high":
		return riskLevelOrdinal(v), true, nil
	default:
		return 0, false, fmt.Errorf("invalid --fail-on-risk %q: must be low, medium, or high", v)
	}
}

// riskGateTrips reports whether a computed risk level meets or exceeds a
// --fail-on-risk threshold. level "unknown" does not trip this risk comparison;
// reviewGateResult separately rejects incomplete indexed reviews when any
// review gate is enabled.
func riskGateTrips(level string, threshold int) bool {
	if level == "" || level == "unknown" {
		return false
	}
	return riskLevelOrdinal(level) >= threshold
}

// gateExit signals that a --fail-on-risk/--fail-on-untested threshold tripped
// AFTER the RunE already printed its normal, unchanged output (human text or
// the --json success envelope). It carries no message on purpose: unlike a
// real error, jsonHandler must not synthesize an {"ok":false,...} failure
// envelope for it — the gate is exit-code-only (see jsonHandler in errors.go,
// which special-cases this type before its usual error-envelope path).
type gateExit struct{}

func (e *gateExit) Error() string { return "" }

// errGate is the shared gateExit sentinel; RunE handlers return it (instead of
// nil) once they've printed a report whose gate condition tripped.
var errGate = &gateExit{}
