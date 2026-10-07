package app

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/git"
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
//     same diff reaches the symbol
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
	case len(imp.Tests) > 0:
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

// diffTestFile is a test file touched by the diff under review, with its content
// loaded once so every changed symbol can be matched against it.
type diffTestFile struct {
	path    string
	content []byte
}

// loadDiffTestFiles reads the changed or untracked test files of the diff (not
// deleted ones) from the working tree. These may be missing from the index —
// the common post-edit flow reviews before reindexing — so the indexed
// heuristic scan alone cannot see a brand-new test.
func loadDiffTestFiles(root string, changed []git.ChangedFile) []diffTestFile {
	var out []diffTestFile
	for _, cf := range changed {
		if cf.Status == "D" || !isTestFilePath(cf.Path) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, cf.Path))
		if err != nil {
			continue
		}
		out = append(out, diffTestFile{path: cf.Path, content: b})
	}
	return out
}

// diffTestCoverage links a changed symbol to the diff's own test files: a
// changed/new test file that references the symbol by bare name (word
// boundary) counts as covering it. Same-name false positives are bounded by
// requiring, for a Go symbol, that the test file lives in the symbol's
// directory (same package, including the _test external package).
func diffTestCoverage(files []diffTestFile, sym SymbolRef) []ImpactNode {
	name := sym.Symbol
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}
	if name == "" || len(files) == 0 {
		return nil
	}
	re, err := regexp.Compile(`\b` + regexp.QuoteMeta(name) + `\b`)
	if err != nil {
		return nil
	}
	goSym := strings.EqualFold(filepath.Ext(sym.File), ".go")
	var out []ImpactNode
	for _, f := range files {
		if goSym && (!strings.EqualFold(filepath.Ext(f.path), ".go") || filepath.Dir(f.path) != filepath.Dir(sym.File)) {
			continue
		}
		if !re.Match(f.content) {
			continue
		}
		out = append(out, ImpactNode{
			Symbol: filepath.Base(f.path), FQN: f.path, Kind: graph.KindTest,
			File: f.path, StartLine: 1, Heuristic: true, Confidence: ConfidenceCandidate,
		})
	}
	return out
}

// withDiffTests wraps a per-symbol impact analyzer so the diff's own test files
// count as covering tests for the changed symbols they reference. The union is
// additive: tests already found by the call graph or the indexed heuristic are
// kept, and a test file already present is not duplicated.
func withDiffTests(analyze func(SymbolRef) (*ImpactReport, error), files []diffTestFile) func(SymbolRef) (*ImpactReport, error) {
	if len(files) == 0 {
		return analyze
	}
	return func(s SymbolRef) (*ImpactReport, error) {
		imp, err := analyze(s)
		if err != nil || imp == nil || !imp.Found {
			return imp, err
		}
		have := map[string]bool{}
		for _, t := range imp.Tests {
			have[t.File] = true
		}
		var added int
		for _, t := range diffTestCoverage(files, s) {
			if have[t.File] {
				continue
			}
			have[t.File] = true
			imp.Tests = append(imp.Tests, t)
			added++
		}
		if added > 0 {
			imp.Untested = false
			imp.TestCommands = testCommands(imp.Tests)
			imp.Note = joinNote(imp.Note, fmt.Sprintf("%d covering test file(s) found in the same diff referencing %q — not confirmed via the call graph", added, s.Symbol))
		}
		return imp, nil
	}
}
