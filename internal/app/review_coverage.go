package app

import (
	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

// Coverage verdicts carried by ReviewCoverage.Verdict.
const (
	CoverageCovered   = "covered"   // every changed symbol has a covering test
	CoveragePartial   = "partial"   // some covered, some uncovered or unknown
	CoverageUncovered = "uncovered" // no covered symbol and at least one known-uncovered symbol
	CoverageUnknown   = "unknown"   // coverage cannot be determined for any changed symbol
)

// ReviewCoverage is the test-coverage verdict for a diff, kept separate from
// the structural risk band. Risk folds fan-in, cross-package spread and call-graph
// resolution into one score and goes "unknown" whenever any symbol lacks a call
// graph; coverage answers only "did we find a test for each changed symbol?" and
// keeps three honest states per symbol:
//
//   - covered:   a call-graph path, a heuristic name match, or a test file in the
//     same diff reaches the symbol (the same-diff link counts toward this verdict
//     ONLY — never toward untested_symbols, risk, or --fail-on-untested)
//   - uncovered: the call graph is usable (resolved or name-based) and found no
//     test, heuristic or otherwise
//   - unknown:   no test link was found AND the call graph cannot say (TS/JS/Python
//     without --precise, declarative formats), or the symbol was never analyzed
//
// Only uncovered symbols are evidence of missing tests; unknown never is — the
// same honesty rule the risk band follows.
type ReviewCoverage struct {
	Verdict          string `json:"verdict"` // covered | partial | uncovered | unknown
	CoveredSymbols   int    `json:"covered_symbols"`
	UncoveredSymbols int    `json:"uncovered_symbols"`
	UnknownSymbols   int    `json:"unknown_symbols"`
}

// classifyImpactCoverage reports one analyzed symbol as covered, uncovered or
// unknown from its impact report (after any same-diff test linkage).
func classifyImpactCoverage(imp *ImpactReport) string {
	switch {
	case len(imp.Tests) > 0 || len(imp.diffTests) > 0:
		return CoverageCovered
	case impactRiskUnknown(imp) || imp.CallGraph == CallGraphNone || imp.CallGraph == CallGraphUnresolved:
		return CoverageUnknown
	default:
		return CoverageUncovered
	}
}

// impactIsTestSubject reports whether every definition behind an impact report
// is a test or lives in a test file.
func impactIsTestSubject(imp *ImpactReport) bool {
	if len(imp.Locations) == 0 {
		return false
	}
	for _, l := range imp.Locations {
		if l.Kind != graph.KindTest && !isTestFilePath(l.File) {
			return false
		}
	}
	return true
}

// computeReviewCoverage folds per-symbol coverage into the diff-level verdict.
// Mapped symbols that were never analyzed (truncated past the cap, or whose
// impact failed) count as unknown so a successful subset never reads as covered.
// Test symbols (and anything defined in a test file) are the coverage, not its
// subject, so they are skipped. Returns nil when nothing remains to assess —
// no mapped symbols, or a diff that only touches tests.
func computeReviewCoverage(rep *ReviewReport, imps []*ImpactReport) *ReviewCoverage {
	if rep.TotalSymbols == 0 {
		return nil
	}
	c := &ReviewCoverage{}
	skipped := 0
	for _, imp := range imps {
		if impactIsTestSubject(imp) {
			skipped++
			continue
		}
		switch classifyImpactCoverage(imp) {
		case CoverageCovered:
			c.CoveredSymbols++
		case CoverageUncovered:
			c.UncoveredSymbols++
		default:
			c.UnknownSymbols++
		}
	}
	if unanalyzed := rep.TotalSymbols - len(imps); unanalyzed > 0 {
		c.UnknownSymbols += unanalyzed
	}
	if c.CoveredSymbols+c.UncoveredSymbols+c.UnknownSymbols == 0 {
		return nil
	}
	switch {
	case c.CoveredSymbols == 0 && c.UncoveredSymbols == 0:
		c.Verdict = CoverageUnknown
	case c.CoveredSymbols == 0:
		c.Verdict = CoverageUncovered
	case c.UncoveredSymbols == 0 && c.UnknownSymbols == 0:
		c.Verdict = CoverageCovered
	default:
		c.Verdict = CoveragePartial
	}
	return c
}

// UncoveredGateTrips reports whether --fail-on-uncovered trips: only a verdict
// of uncovered or partial that actually contains known-uncovered symbols. An
// unknown verdict, or a partial one made of covered + unknown symbols, never
// trips — unresolved coverage is not evidence of missing tests.
func (c *ReviewCoverage) UncoveredGateTrips() bool {
	if c == nil || c.UncoveredSymbols == 0 {
		return false
	}
	return c.Verdict == CoverageUncovered || c.Verdict == CoveragePartial
}
