package app

import (
	"fmt"
	"sort"
)

// reviewMaxSymbols is reused as the per-file analysis cap.
const fileImpactMaxSymbols = 200

// FileImpactReport answers "what happens if I change or delete THIS file?" — the
// file-level peer of impact (symbol) and review (changeset). It aggregates every
// symbol the file defines into: the other files that depend on it (call into it),
// the transitive blast radius, the tests that cover it, whether it's safe to delete
// (nothing external references it), and whether changing it is a breaking change (an
// externally-called symbol is untested). Useful for file move/delete/split refactors.
type FileImpactReport struct {
	Project         string       `json:"project"`
	File            string       `json:"file"`
	Depth           int          `json:"depth"` // effective blast-radius depth (after the <=0 → 3 default)
	Indexed         bool         `json:"indexed"`
	Found           bool         `json:"found"` // the file has indexed symbols
	Symbols         int          `json:"symbols"`
	DependentFiles  []string     `json:"dependent_files"` // other files whose code calls into this one
	BlastRadius     int          `json:"blast_radius"`    // transitively-affected symbols outside this file
	CoveringTests   []ImpactNode `json:"covering_tests"`
	UntestedSymbols []SymbolRef  `json:"untested_symbols"` // externally-called symbols with no covering test
	SafeToDelete    bool         `json:"safe_to_delete"`   // nothing outside the file references it
	BreakingChange  bool         `json:"breaking_change"`  // an externally-called symbol is untested
	Stale           bool         `json:"stale"`
	Resolution      string       `json:"resolution,omitempty"`
	Note            string       `json:"note,omitempty"`
}

// FileImpact computes file-level impact: the union of every defined symbol's
// callers, blast radius, and covering tests, framed as the questions an agent asks
// before touching a file — who depends on it, what breaks, and is it safe to remove.
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
	}
	if !found {
		return rep, nil
	}

	syms, err := svc.Symbols(cwd, file)
	if err != nil {
		return nil, err
	}
	rep.File = syms.File // normalized project-relative path
	defined := definableSymbols(syms.Symbols)
	if len(defined) == 0 {
		rep.Note = "no indexed symbols in this file (not indexed, or only declarations/comments)"
		return rep, nil
	}
	rep.Found = true
	rep.Symbols = len(defined)

	if st, serr := svc.Staleness(cwd); serr == nil && st != nil {
		rep.Stale = st.Any()
	}

	if len(defined) > fileImpactMaxSymbols {
		rep.Note = joinNote(rep.Note, fmt.Sprintf("large file — analyzed the first %d of %d symbols", fileImpactMaxSymbols, len(defined)))
		defined = defined[:fileImpactMaxSymbols]
	}

	depFiles := map[string]bool{}
	blast := map[string]bool{}
	seenTest := map[string]bool{}
	anyExternalCaller, externalUntested := false, false

	for _, s := range defined {
		target := s.Symbol
		if s.FQN != "" {
			target = s.FQN
		}
		imp, ierr := svc.Impact(cwd, target, depth)
		if (ierr != nil || imp == nil || !imp.Found) && target != s.Symbol {
			imp, ierr = svc.Impact(cwd, s.Symbol, depth)
		}
		if ierr != nil || imp == nil || !imp.Found {
			continue
		}
		if imp.Resolution != "" && rep.Resolution == "" {
			rep.Resolution = imp.Resolution
		}
		externalCallers := 0
		for _, c := range imp.DirectCallers {
			if c.File != rep.File {
				externalCallers++
				depFiles[c.File] = true
			}
		}
		if externalCallers > 0 {
			anyExternalCaller = true
			if imp.Untested && imp.Resolution == "" {
				externalUntested = true
				rep.UntestedSymbols = append(rep.UntestedSymbols, s)
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

	rep.DependentFiles = sortedKeys(depFiles)
	rep.BlastRadius = len(blast)
	// The delete/breaking verdicts are only trustworthy when the call graph was
	// actually resolved. For TS/JS/Python without --precise there are NO call edges,
	// so "no external callers" is unverified, not established — never claim a file is
	// safe to delete then (a false green that could delete live code). Leave both
	// verdicts false and let Resolution explain why.
	if rep.Resolution == "" {
		rep.SafeToDelete = !anyExternalCaller
		rep.BreakingChange = externalUntested
	} else {
		rep.Note = joinNote(rep.Note, "safe_to_delete / breaking_change are unavailable without a resolved call graph — reindex with --precise")
	}
	return rep, nil
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

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
