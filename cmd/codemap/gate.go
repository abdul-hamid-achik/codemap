/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"fmt"

	"github.com/abdul-hamid-achik/codemap/internal/app"
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

// parseFailOnRiskFlag validates --fail-on-risk's value. An empty value means
// the gate is disabled (set=false). A non-empty value must be one of
// low/medium/high; anything else is an operational error (exit 1) raised
// before any query work happens, matching the CLI's usual fail-fast behavior
// for a bad flag. The ordinal comes from app.RiskLevelOrdinal — the single
// source of truth shared with the report-attached gate signal (D9), so the
// exit-code path and the JSON `gate` field can never drift apart.
func parseFailOnRiskFlag(cmd *cobra.Command) (threshold int, set bool, err error) {
	v, _ := cmd.Flags().GetString("fail-on-risk")
	if v == "" {
		return 0, false, nil
	}
	switch v {
	case "low", "medium", "high":
		return app.RiskLevelOrdinal(v), true, nil
	default:
		return 0, false, fmt.Errorf("invalid --fail-on-risk %q: must be low, medium, or high", v)
	}
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
