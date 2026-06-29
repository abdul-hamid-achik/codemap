package app

import (
	"fmt"
	"path/filepath"
	"strings"
)

// RiskFactor is one contributor to a symbol's change-risk, with how much it adds
// (severity 0..1) and a human reason. Factors are independent signals codemap
// already computes; Risk combines them into one score.
type RiskFactor struct {
	Factor   string  `json:"factor"`
	Severity float64 `json:"severity"`
	Detail   string  `json:"detail"`
}

// RiskReport answers "how risky is changing this symbol?" in one number, so an
// agent can decide how much care (tests, review) a change warrants. It synthesizes
// the honesty signals already on ImpactReport — untested coverage, fan-in, the
// spread of callers across packages, and name ambiguity — into a 0..1 score and a
// low/medium/high level, each backed by the factors that produced it.
type RiskReport struct {
	Symbol  string       `json:"symbol"`
	Project string       `json:"project"`
	Found   bool         `json:"found"`
	Score   float64      `json:"score"` // 0..1, probabilistic-OR of the factor severities
	Level   string       `json:"level"` // low | medium | high
	Callers int          `json:"callers"`
	Tests   int          `json:"covering_tests"`
	Factors []RiskFactor `json:"factors"`
	Note    string       `json:"note,omitempty"`
}

// Risk computes a change-risk score for a symbol from its impact analysis. Reuses
// Impact end to end; never errors on a missing symbol (Found=false).
func (svc *Service) Risk(cwd, symbol string, depth int) (*RiskReport, error) {
	imp, err := svc.Impact(cwd, symbol, depth)
	if err != nil {
		return nil, err
	}
	rep := &RiskReport{Symbol: imp.Symbol, Project: imp.Project, Found: imp.Found, Factors: []RiskFactor{}}
	if !imp.Found {
		return rep, nil
	}
	rep.Callers = len(imp.DirectCallers)
	rep.Tests = len(imp.Tests)

	add := func(factor string, sev float64, detail string) {
		rep.Factors = append(rep.Factors, RiskFactor{Factor: factor, Severity: sev, Detail: detail})
	}

	if imp.Resolution != "" {
		// No call graph (TS/JS/Python without --precise): the other signals are
		// unresolved, so risk is uncertain rather than computable. Surface that.
		add("unresolved", 0.3, "call graph unavailable without --precise — fan-in and coverage are unknown")
		rep.Note = imp.Resolution
	} else {
		if imp.Untested {
			add("untested", 0.9, "no tests reach this symbol — a change here is unverified")
		}
		switch {
		case rep.Callers >= 10:
			add("high_fan_in", 0.5, fmt.Sprintf("%d direct callers — broadly depended on", rep.Callers))
		case rep.Callers >= 5:
			add("fan_in", 0.3, fmt.Sprintf("%d direct callers", rep.Callers))
		}
		if pkgs := callerPackages(imp.DirectCallers); pkgs >= 3 {
			add("cross_package", 0.3, fmt.Sprintf("called from %d packages — a change ripples across the codebase", pkgs))
		}
	}
	if strings.Contains(imp.Note, "matches") && strings.Contains(imp.Note, "definitions") {
		add("ambiguous_name", 0.2, "the name resolves to multiple definitions — the analysis merges them")
	}

	rep.Score = round3(combineRisk(rep.Factors))
	rep.Level = riskLevel(rep.Score)
	return rep, nil
}

// combineRisk folds independent factor severities with a probabilistic OR
// (1 - ∏(1-sᵢ)), so factors compound but the score saturates at 1 instead of
// summing past it.
func combineRisk(factors []RiskFactor) float64 {
	survive := 1.0
	for _, f := range factors {
		s := f.Severity
		if s < 0 {
			s = 0
		} else if s > 1 {
			s = 1
		}
		survive *= 1 - s
	}
	return 1 - survive
}

func riskLevel(score float64) string {
	switch {
	case score >= 0.67:
		return "high"
	case score >= 0.34:
		return "medium"
	default:
		return "low"
	}
}

// callerPackages counts the distinct package directories among a symbol's callers —
// a proxy for how widely a change ripples (intra-file/intra-package vs system-wide).
func callerPackages(callers []SymbolRef) int {
	seen := map[string]bool{}
	for _, c := range callers {
		if c.File == "" {
			continue
		}
		seen[filepath.Dir(c.File)] = true
	}
	return len(seen)
}
