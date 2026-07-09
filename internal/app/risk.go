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
	Tests   int          `json:"covering_tests_count"`
	Factors []RiskFactor `json:"factors"`
	Note    string       `json:"note,omitempty"`
	Next    []NextAction `json:"next,omitempty"`
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
	rep.Factors = riskFactorsFromImpact(imp)
	if imp.Resolution != "" {
		rep.Note = imp.Resolution
	}
	rep.Score = round3(combineRisk(rep.Factors))
	rep.Level = riskLevel(rep.Score)
	if imp.Resolution != "" {
		rep.Next = append(rep.Next, nextAction("codemap_index",
			"risk is uncertain because the call graph is unresolved",
			map[string]any{"path": cwd, "precise": true}))
	}
	if rep.Level == "high" {
		rep.Next = append(rep.Next, nextAction("codemap_impact",
			"high-risk symbols need callers, blast radius, and covering tests reviewed before change",
			map[string]any{"path": cwd, "symbol": symbol, "depth": depth}))
	}
	return rep, nil
}

// riskFactorsFromImpact computes the per-symbol change-risk factors from one
// ImpactReport — the inner logic of Risk, extracted so Review can aggregate the
// same signals across every changed symbol without re-running Impact. It does
// NOT set Score/Level (those combine at the symbol OR the diff level).
func riskFactorsFromImpact(imp *ImpactReport) []RiskFactor {
	if imp == nil {
		return []RiskFactor{}
	}
	factors := []RiskFactor{}
	add := func(factor string, sev float64, detail string) {
		factors = append(factors, RiskFactor{Factor: factor, Severity: sev, Detail: detail})
	}
	if imp.Resolution != "" {
		// No call graph (TS/JS/Python without --precise): the other signals are
		// unresolved, so risk is uncertain rather than computable. Surface that.
		add("unresolved", 0.3, "call graph unavailable without --precise — fan-in and coverage are unknown")
	} else {
		if imp.Untested {
			add("untested", 0.9, "no tests reach this symbol — a change here is unverified")
		}
		switch {
		case len(imp.DirectCallers) >= 10:
			add("high_fan_in", 0.5, fmt.Sprintf("%d direct callers — broadly depended on", len(imp.DirectCallers)))
		case len(imp.DirectCallers) >= 5:
			add("fan_in", 0.3, fmt.Sprintf("%d direct callers", len(imp.DirectCallers)))
		}
		if pkgs := callerPackages(imp.DirectCallers); pkgs >= 3 {
			add("cross_package", 0.3, fmt.Sprintf("called from %d packages — a change ripples across the codebase", pkgs))
		}
	}
	if strings.Contains(imp.Note, "matches") && strings.Contains(imp.Note, "definitions") {
		add("ambiguous_name", 0.2, "the name resolves to multiple definitions — the analysis merges them")
	}
	return factors
}

// ReviewRisk is the aggregate change-risk band for a whole diff, folded from
// the per-symbol risk signals over every changed symbol. It lets a harness
// gate verification on ONE band (instead of fanning `risk` out per symbol).
// The factor names are the review-level categories a consumer reads:
//
//   - untested_changes — some changed symbol has no covering test
//   - hotspot_fanin    — some changed symbol has many direct callers (a hub)
//   - cross_package    — some changed symbol's callers span ≥3 packages
//   - ambiguity        — some changed symbol's name resolves to multiple defs
//   - unresolved       — some changed symbol's call graph is unavailable
//
// Each factor's severity is the MAX across changed symbols (the strongest
// signal wins), and the factors combine with probabilistic OR into one score.
type ReviewRisk struct {
	Level   string       `json:"level"`   // low | medium | high
	Score   float64      `json:"score"`   // 0..1, probabilistic-OR of the factor severities
	Factors []RiskFactor `json:"factors"` // review-level categories (max severity across changed symbols)
}

// aggregateReviewRisk folds the per-symbol change-risk signals across a set of
// changed symbols' impact reports into one diff-scoped band. A review-level
// factor fires when ANY changed symbol triggers its per-symbol equivalent
// (max severity carried, with a detail noting how many symbols contributed),
// then the factors combine with probabilistic OR into one score + level.
// Returns nil for an empty set (nothing to risk-assess → absent/low).
func aggregateReviewRisk(imps []*ImpactReport) *ReviewRisk {
	if len(imps) == 0 {
		return nil
	}
	// category → (max severity, contributing symbol count)
	type agg struct {
		sev   float64
		count int
	}
	cats := map[string]*agg{
		"untested_changes": {},
		"hotspot_fanin":    {},
		"cross_package":    {},
		"ambiguity":        {},
		"unresolved":       {},
	}
	// Per-symbol factor → review category. Fan-in's two tiers (0.5/0.3)
	// collapse to the one hotspot_fanin category, carrying the max severity.
	mapFactor := func(f RiskFactor) (cat string, sev float64) {
		switch f.Factor {
		case "untested":
			return "untested_changes", f.Severity
		case "high_fan_in", "fan_in":
			return "hotspot_fanin", f.Severity
		case "cross_package":
			return "cross_package", f.Severity
		case "ambiguous_name":
			return "ambiguity", f.Severity
		case "unresolved":
			return "unresolved", f.Severity
		}
		return "", 0
	}
	for _, imp := range imps {
		for _, f := range riskFactorsFromImpact(imp) {
			cat, sev := mapFactor(f)
			if cat == "" {
				continue
			}
			a := cats[cat]
			if sev > a.sev {
				a.sev = sev
			}
			a.count++
		}
	}
	factors := []RiskFactor{}
	for _, cat := range []string{"untested_changes", "hotspot_fanin", "cross_package", "ambiguity", "unresolved"} {
		a := cats[cat]
		if a.count == 0 {
			continue
		}
		factors = append(factors, RiskFactor{Factor: cat, Severity: round3(a.sev), Detail: reviewRiskDetail(cat, a.count)})
	}
	score := round3(combineRisk(factors))
	return &ReviewRisk{
		Level:   riskLevel(score),
		Score:   score,
		Factors: factors,
	}
}

// reviewRiskDetail renders a short, human-readable reason for a review-level
// risk factor, noting how many changed symbols contributed.
func reviewRiskDetail(cat string, count int) string {
	switch cat {
	case "untested_changes":
		return fmt.Sprintf("%d changed symbol(s) have no covering test — changes are unverified", count)
	case "hotspot_fanin":
		return fmt.Sprintf("%d changed symbol(s) are hubs with many direct callers", count)
	case "cross_package":
		return fmt.Sprintf("%d changed symbol(s) are called from ≥3 packages — a change ripples widely", count)
	case "ambiguity":
		return fmt.Sprintf("%d changed symbol(s) resolve to multiple definitions — the analysis merges them", count)
	case "unresolved":
		return fmt.Sprintf("%d changed symbol(s) have an unresolved call graph — fan-in/coverage unknown; run 'codemap index --precise'", count)
	}
	return cat
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
