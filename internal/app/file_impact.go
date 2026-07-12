package app

import (
	"fmt"
	"strings"
)

// reviewMaxSymbols is reused as the per-file analysis cap.
const fileImpactMaxSymbols = 200

// FileImpactReport answers "what happens if I change or delete THIS file?" — the
// file-level peer of impact (symbol) and review (changeset). It aggregates every
// symbol the file defines into grouped inbound call/reference/import evidence,
// the transitive blast radius, the tests that cover it, conservative deletion
// coverage, and whether changing it is a breaking change (an externally-called
// symbol is untested). Useful for file move/delete/split refactors.
type FileImpactReport struct {
	Project            string                  `json:"project"`
	File               string                  `json:"file"`
	Depth              int                     `json:"depth"` // effective blast-radius depth (after the <=0 → 3 default)
	Indexed            bool                    `json:"indexed"`
	Found              bool                    `json:"found"` // the file has indexed nodes
	Symbols            int                     `json:"symbols"`
	DependentFiles     []string                `json:"dependent_files"`    // files with inbound call/reference/import evidence (imports may be package-scoped; inspect DependencyEvidence)
	BlastRadius        int                     `json:"blast_radius_count"` // transitively-affected symbols outside this file (P1-19: bare name was the int count in some reports; now always _count, with the list under blast_radius if needed)
	CoveringTests      []ImpactNode            `json:"covering_tests"`
	UntestedSymbols    []SymbolRef             `json:"untested_symbols"` // externally-called symbols with no covering test
	DependencyEvidence *FileDependenciesReport `json:"dependency_evidence,omitempty"`
	CallGraph          string                  `json:"call_graph"`      // resolved|name|unresolved|none across the analyzed symbols
	DeleteVerdict      string                  `json:"delete_verdict"`  // unsafe only for fresh confirmed file-scoped evidence; otherwise unknown
	SafeToDelete       bool                    `json:"safe_to_delete"`  // legacy compatibility field; conservatively false until dependency evidence is complete
	BreakingChange     bool                    `json:"breaking_change"` // an externally-called symbol is untested
	Stale              bool                    `json:"stale"`
	Resolution         string                  `json:"resolution,omitempty"`
	Note               string                  `json:"note,omitempty"`
	Next               []NextAction            `json:"next,omitempty"`
}

const (
	DeleteVerdictUnsafe  = "unsafe"
	DeleteVerdictUnknown = "unknown"
)

// FileImpact computes file-level dependency evidence plus the union of every
// exact defined symbol's callers, blast radius, and covering tests, framed as the
// questions an agent asks before touching a file — who depends on it, what
// breaks, and what is still unknown before removal.
func (svc *Service) FileImpact(cwd, file string, depth int) (*FileImpactReport, error) {
	if depth <= 0 {
		depth = 3
	}
	_, name, found, err := svc.project(cwd)
	if err != nil {
		return nil, err
	}
	rep := &FileImpactReport{
		Project: name, File: file, Depth: depth, Indexed: found,
		DependentFiles: []string{}, CoveringTests: []ImpactNode{}, UntestedSymbols: []SymbolRef{},
		CallGraph: CallGraphNone, DeleteVerdict: DeleteVerdictUnknown,
	}
	if !found {
		return rep, nil
	}

	syms, err := svc.Symbols(cwd, file)
	if err != nil {
		return nil, err
	}
	rep.File = syms.File // normalized project-relative path
	deps, err := svc.Dependencies(cwd, rep.File)
	if err != nil {
		return nil, err
	}
	rep.DependencyEvidence = deps
	rep.DependentFiles = append([]string(nil), deps.allDependentFiles...)
	rep.CallGraph = deps.CallGraph
	rep.Stale = deps.Stale
	rep.Found = deps.Found
	if !deps.Found {
		rep.Note = "no indexed nodes in this file (not indexed, excluded, or path not found)"
		return rep, nil
	}
	if deps.CallGraph == CallGraphUnresolved {
		rep.Resolution = "call dependency evidence is unavailable for at least one callable file — reindex with --precise before treating missing callers as absent"
	}
	defined := definableSymbols(syms.Symbols)
	rep.Symbols = len(defined)
	definedTotal := len(defined)
	if definedTotal == 0 {
		rep.Note = joinNote(rep.Note, "no callable/type symbols in this file; blast-radius and test analysis are empty, but file-level dependency evidence still applies")
	}

	if definedTotal > fileImpactMaxSymbols {
		rep.Note = joinNote(rep.Note, fmt.Sprintf("large file — analyzed the first %d of %d symbols", fileImpactMaxSymbols, definedTotal))
		defined = defined[:fileImpactMaxSymbols]
	}

	blast := map[string]bool{}
	seenTest := map[string]bool{}
	skipped := 0
	externalUntested := false
	candidateUntested := false

	for _, s := range defined {
		imp, ierr := svc.ImpactBySelector(cwd, SymbolSelector{
			File: s.File, StartLine: s.StartLine, FQN: s.FQN, Kind: s.Kind,
		}, depth)
		if ierr != nil || imp == nil || !imp.Found {
			// Count partial analysis so the report never promotes absence of
			// evidence into a deletion-safety claim.
			skipped++
			continue
		}
		if imp.Resolution != "" && rep.Resolution == "" {
			rep.Resolution = imp.Resolution
		}
		externalCallers := 0
		for _, c := range imp.DirectCallers {
			if c.File != rep.File {
				externalCallers++
			}
		}
		if externalCallers > 0 && imp.Untested && imp.Resolution == "" {
			targetKey := symKey(s.FQN, s.File, s.StartLine)
			switch {
			case deps.confirmedCallTargets[targetKey]:
				externalUntested = true
				rep.UntestedSymbols = append(rep.UntestedSymbols, s)
			case deps.candidateCallTargets[targetKey]:
				candidateUntested = true
			}
		}
		for _, b := range imp.BlastRadius {
			if b.File != rep.File {
				blast[symKey(b.FQN, b.File, b.StartLine)] = true
			}
		}
		for _, tn := range imp.Tests {
			if key := symKey(tn.FQN, tn.File, tn.StartLine); !seenTest[key] {
				seenTest[key] = true
				rep.CoveringTests = append(rep.CoveringTests, tn)
			}
		}
	}

	rep.BlastRadius = len(blast)

	applyFileDependencyVerdict(rep, deps)

	// breaking_change is still a narrower call/test signal. Withhold it when the
	// call graph or per-file analysis is incomplete.
	breakingWithheld := rep.Resolution != ""
	truncated := definedTotal > fileImpactMaxSymbols
	if truncated || skipped > 0 {
		breakingWithheld = true
		parts := []string{}
		if truncated {
			parts = append(parts, fmt.Sprintf("file has >%d symbols (analyzed first %d)", fileImpactMaxSymbols, fileImpactMaxSymbols))
		}
		if skipped > 0 {
			parts = append(parts, fmt.Sprintf("%d symbol(s) skipped (impact lookup failed)", skipped))
		}
		rep.Note = joinNote(rep.Note, "breaking_change withheld — "+strings.Join(parts, "; "))
	}
	if !breakingWithheld {
		rep.BreakingChange = externalUntested
	} else if rep.Resolution != "" {
		rep.Note = joinNote(rep.Note, "breaking_change is unavailable without a resolved call graph — reindex with --precise")
	}
	if !externalUntested && candidateUntested {
		rep.BreakingChange = false
		rep.Note = joinNote(rep.Note, "breaking_change withheld — untested external callers are name-based candidates, not confirmed call dependencies")
	}
	needsReindex := rep.Resolution != "" || deps.CandidateFileScopedTotal > 0 || deps.Stale
	if needsReindex {
		why := "refresh the stale dependency snapshot before assessing file-level change risk"
		args := map[string]any{"path": cwd}
		if rep.Resolution != "" || deps.CandidateFileScopedTotal > 0 {
			why = "resolve candidate dependency evidence before assessing file-level change risk"
			args["precise"] = true
		}
		rep.Next = append(rep.Next, nextAction("codemap_index",
			why, args))
	}
	if rep.DeleteVerdict == DeleteVerdictUnsafe || rep.BreakingChange {
		why := "review the real diff and selected regressions before changing a file with externally-called untested symbols"
		if rep.DeleteVerdict == DeleteVerdictUnsafe {
			why = "review the real diff and selected regressions before changing a file with confirmed inbound dependencies"
		}
		rep.Next = append(rep.Next, nextAction("codemap_review",
			why,
			map[string]any{"path": cwd, "depth": depth}))
	} else if len(rep.Next) < 2 {
		rep.Next = append(rep.Next, nextAction("codemap_related_files",
			"inspect broader co-change evidence before deleting a file whose dependency completeness is unknown",
			map[string]any{"path": cwd, "file": rep.File}))
	}
	return rep, nil
}

func applyFileDependencyVerdict(rep *FileImpactReport, deps *FileDependenciesReport) {
	// The legacy bool remains conservative. Only fresh, confirmed file-scoped
	// evidence can prove an exact file unsafe. Name-fanout candidates, stale
	// snapshots, and Go's representative-file import edge remain unknown.
	rep.SafeToDelete = false
	if !deps.Stale && deps.ConfirmedFileScopedTotal > 0 {
		rep.DeleteVerdict = DeleteVerdictUnsafe
		kinds := dependencyEvidenceKindsForConfidence(deps, DependencyConfidenceConfirmed)
		rep.Note = joinNote(rep.Note, fmt.Sprintf(
			"delete verdict is unsafe — %d confirmed %s relationship(s) enter this file in the current index snapshot",
			deps.ConfirmedFileScopedTotal, strings.Join(kinds, "/")))
		if deps.CandidateFileScopedTotal > 0 {
			rep.Note = joinNote(rep.Note, fmt.Sprintf(
				"%d additional file-scoped relationship(s) are candidates and are not part of the unsafe proof",
				deps.CandidateFileScopedTotal))
		}
		return
	}
	rep.DeleteVerdict = DeleteVerdictUnknown
	coverage := dependencyCoverageSummary(deps.Coverage)
	if deps.CandidateFileScopedTotal > 0 {
		if deps.Stale {
			rep.Note = joinNote(rep.Note, fmt.Sprintf(
				"delete verdict is unknown — %d inbound relationship(s) come from a stale index snapshot and must be refreshed before they can be confirmed; incomplete domains: %s",
				deps.CandidateFileScopedTotal, coverage))
			return
		}
		kinds := dependencyEvidenceKindsForConfidence(deps, DependencyConfidenceCandidate)
		rep.Note = joinNote(rep.Note, fmt.Sprintf(
			"delete verdict is unknown — %d %s relationship(s) are name-based candidates, not confirmed exact-file dependencies; reindex with --precise; incomplete domains: %s",
			deps.CandidateFileScopedTotal, strings.Join(kinds, "/"), coverage))
		return
	}
	if deps.PackageScopedEvidenceTotal > 0 {
		rep.Note = joinNote(rep.Note, fmt.Sprintf(
			"delete verdict is unknown — %d package-scoped import relationship(s) show package use but do not prove this exact file is required; incomplete domains: %s",
			deps.PackageScopedEvidenceTotal, coverage))
		return
	}
	rep.Note = joinNote(rep.Note, "delete verdict is unknown — no inbound dependency evidence was found, and missing evidence cannot prove safety while domains remain incomplete: "+coverage)
}

// definableSymbols keeps only the symbols a file "owns" for impact purposes —
// functions, methods, and types (not the file node itself or bare variables).
func definableSymbols(refs []SymbolRef) []SymbolRef {
	out := make([]SymbolRef, 0, len(refs))
	for _, r := range refs {
		switch r.Kind {
		case "function", "method", "type", "test":
			out = append(out, r)
		}
	}
	return out
}
