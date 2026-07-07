package app

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"github.com/abdul-hamid-achik/codemap/internal/git"
	"github.com/abdul-hamid-achik/codemap/internal/index"
)

// Review tuning. These bound work on a pathological changeset and decide when a
// changed symbol is "load-bearing" enough to flag as a hotspot.
const (
	reviewMaxSymbols        = 200 // cap Impact calls on a huge diff (then note the elision)
	reviewHotspotMinCallers = 8   // direct callers at/above which a changed symbol is a hotspot
	reviewGitTimeout        = 10 * time.Second
)

// ReviewOpts selects which diff to analyze. Mode is "working" (everything since
// HEAD — staged + unstaged + untracked), "staged" (the index only), or "since"
// (between Since and the working tree). Depth bounds each symbol's blast radius.
type ReviewOpts struct {
	Mode  string
	Since string
	Depth int
}

// ReviewFile is one changed file with how many of its changed lines mapped to
// indexed symbols — so an agent sees coverage of the diff itself, not just a list.
type ReviewFile struct {
	Path    string `json:"path"`
	Status  string `json:"status"`
	Symbols int    `json:"symbols"`
}

// ReviewReport is the diff-scoped intelligence bundle: the symbols a changeset
// touches, the union of their blast radius, the tests that cover them (regression
// test selection), the changed symbols that are untested or load-bearing, and the
// same freshness/resolution honesty signals every other codemap report carries.
// It is the keystone an agent harness queries after editing — "what did I just
// affect, and what should I run?" — in one call.
type ReviewReport struct {
	Project         string           `json:"project"`
	Mode            string           `json:"mode"`
	Since           string           `json:"since,omitempty"`
	Depth           int              `json:"depth"` // effective blast-radius depth (after the <=0 → 3 default)
	IsRepo          bool             `json:"is_repo"`
	Indexed         bool             `json:"indexed"`
	ChangedFiles    []ReviewFile     `json:"changed_files"`
	ChangedSymbols  []SymbolRef      `json:"changed_symbols"`
	BlastRadius     []ImpactNode     `json:"blast_radius"`
	CoveringTests   []ImpactNode     `json:"covering_tests"`
	UntestedSymbols []SymbolRef      `json:"untested_symbols"`   // changed symbols with no covering test (P1-19: bare "untested" name was the list here vs a bool elsewhere; renamed for consistency)
	Hotspots        []SymbolRef      `json:"hotspots,omitempty"` // changed symbols with many direct callers
	Stale           bool             `json:"stale"`
	Staleness       *index.Staleness `json:"staleness,omitempty"`
	Resolution      string           `json:"resolution,omitempty"` // human sentence set when some changed symbols' call graph is unresolved (TS/JS/Py without --precise)
	CallGraph       string           `json:"call_graph,omitempty"` // stable machine enum: resolved|name|unresolved|none — the worst (least-confident) across changed symbols
	// Risk is the aggregate change-risk band for the whole diff, folded from the
	// per-symbol risk signals over every changed symbol so a harness can gate
	// verification on ONE band (instead of fanning `risk` out per symbol). Absent
	// when the diff maps to no indexed symbols.
	Risk *ReviewRisk `json:"risk,omitempty"`
	Note string      `json:"note,omitempty"`
}

// Review computes diff-scoped impact + test selection for the working tree. It
// reuses the existing primitives end to end: git for the diff, Symbols for
// hunk→symbol resolution, and Impact for each changed symbol's blast radius and
// covering tests. Never errors on a non-repo or unindexed project — it degrades to
// a plain changed-file list with a Note so an agent always gets an actionable answer.
func (svc *Service) Review(cwd string, opts ReviewOpts) (*ReviewReport, error) {
	if opts.Depth <= 0 {
		opts.Depth = 3
	}
	mode := opts.Mode
	if mode == "" {
		mode = "working"
	}
	_, name, err := svc.resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	rep := &ReviewReport{
		Project: name, Mode: mode, Since: opts.Since, Depth: opts.Depth,
		ChangedFiles: []ReviewFile{}, ChangedSymbols: []SymbolRef{},
		BlastRadius: []ImpactNode{}, CoveringTests: []ImpactNode{}, UntestedSymbols: []SymbolRef{},
	}
	_, _, indexed, perr := svc.project(cwd)
	if perr != nil {
		return nil, perr
	}
	rep.Indexed = indexed

	// Defensive validation of agent-supplied `--since` ref BEFORE any exec call —
	// option-injection guard (P0-03). A leading-dash ref is parsed as a git
	// option even past `--`; refuse here so we never write an arbitrary file via
	// --output or similar. Return a graceful Note instead of a hard error so the
	// harness always gets an actionable answer.
	if mode == "since" && !git.ValidRef(opts.Since) {
		rep.Note = fmt.Sprintf("invalid --since ref %q: must be non-empty and must not start with '-'; pass a commit, branch, or tag name", opts.Since)
		return rep, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), reviewGitTimeout)
	defer cancel()

	root, gerr := git.RepoRoot(ctx, cwd)
	if gerr != nil {
		rep.Note = "not a git repository — codemap review needs git to compute the diff"
		return rep, nil
	}
	rep.IsRepo = true

	changed, cerr := git.ChangedFiles(ctx, root, mode, opts.Since)
	if cerr != nil {
		return nil, fmt.Errorf("git diff failed: %w", cerr)
	}

	// Always report the changed files, even unindexed — the agent still sees what
	// moved. Without an index there is no graph to walk for impact/tests.
	if !rep.Indexed {
		for _, cf := range changed {
			rep.ChangedFiles = append(rep.ChangedFiles, ReviewFile{Path: cf.Path, Status: cf.Status})
		}
		rep.Note = joinNote(rep.Note, "project not indexed — run 'codemap index' for blast radius and test selection")
		return rep, nil
	}

	if st, serr := svc.Staleness(cwd); serr == nil && st != nil {
		rep.Staleness = st
		rep.Stale = st.Any()
	}

	// 1) Resolve each changed file's hunks to the enclosing indexed symbols.
	seenSym := map[string]bool{}
	for _, cf := range changed {
		rf := ReviewFile{Path: cf.Path, Status: cf.Status}
		if cf.Status != "D" {
			if syms := svc.symbolsForChangedFile(cwd, root, cf.Path); syms != nil {
				for _, s := range syms {
					if !symbolTouched(s, cf.Hunks) {
						continue
					}
					key := symKey(s.FQN, s.File, s.StartLine)
					if seenSym[key] {
						continue
					}
					seenSym[key] = true
					rep.ChangedSymbols = append(rep.ChangedSymbols, s)
					rf.Symbols++
				}
			}
		}
		rep.ChangedFiles = append(rep.ChangedFiles, rf)
	}

	truncated := 0
	if len(rep.ChangedSymbols) > reviewMaxSymbols {
		truncated = len(rep.ChangedSymbols) - reviewMaxSymbols
		rep.ChangedSymbols = rep.ChangedSymbols[:reviewMaxSymbols]
	}

	// 2) Union the blast radius + covering tests across changed symbols; flag the
	//    untested and load-bearing ones.
	seenBlast, seenTest := map[string]bool{}, map[string]bool{}
	imps := make([]*ImpactReport, 0, len(rep.ChangedSymbols))
	for _, s := range rep.ChangedSymbols {
		target := s.Symbol
		if s.FQN != "" {
			target = s.FQN
		}
		imp, ierr := svc.Impact(cwd, target, opts.Depth)
		if (ierr != nil || imp == nil || !imp.Found) && target != s.Symbol {
			imp, ierr = svc.Impact(cwd, s.Symbol, opts.Depth) // FQN missed → retry bare name
		}
		if ierr != nil || imp == nil || !imp.Found {
			continue
		}
		imps = append(imps, imp)
		if imp.Resolution != "" && rep.Resolution == "" {
			rep.Resolution = imp.Resolution
		}
		for _, b := range imp.BlastRadius {
			if key := symKey(b.FQN, b.File, b.StartLine); !seenBlast[key] {
				seenBlast[key] = true
				rep.BlastRadius = append(rep.BlastRadius, b)
			}
		}
		for _, tn := range imp.Tests {
			if key := symKey(tn.FQN, tn.File, tn.StartLine); !seenTest[key] {
				seenTest[key] = true
				rep.CoveringTests = append(rep.CoveringTests, tn)
			}
		}
		if imp.Untested && imp.Resolution == "" {
			rep.UntestedSymbols = append(rep.UntestedSymbols, s)
		}
		if len(imp.DirectCallers) >= reviewHotspotMinCallers {
			rep.Hotspots = append(rep.Hotspots, s)
		}
	}
	// Fold the per-symbol resolution + risk signals into one diff-scoped band.
	// call_graph is the worst (least-confident) across changed symbols — a
	// review is only as trustworthy as its least-resolved change. Risk reuses
	// the `risk` command's factor model, aggregated (max severity per factor)
	// and combined with probabilistic OR.
	if len(imps) > 0 {
		rep.CallGraph = worstCallGraph(imps)
		rep.Risk = aggregateReviewRisk(imps)
	}

	// Shallowest-first blast radius (the closest callers matter most), stable by file.
	sort.SliceStable(rep.BlastRadius, func(i, j int) bool {
		if rep.BlastRadius[i].Depth != rep.BlastRadius[j].Depth {
			return rep.BlastRadius[i].Depth < rep.BlastRadius[j].Depth
		}
		return rep.BlastRadius[i].File < rep.BlastRadius[j].File
	})

	if truncated > 0 {
		rep.Note = joinNote(rep.Note, fmt.Sprintf("large changeset — analyzed the first %d changed symbols (%d more elided; use a narrower --since)", reviewMaxSymbols, truncated))
	}
	if len(rep.ChangedSymbols) == 0 && len(changed) > 0 {
		rep.Note = joinNote(rep.Note, "changed lines don't map to indexed symbols (comments, imports, or unindexed/untracked files) — nothing to analyze")
	}
	return rep, nil
}

// symbolsForChangedFile resolves a diff path (relative to the git root) to its
// indexed symbols, robust to a symlinked checkout. git's root comes back
// symlink-resolved (e.g. /private/var/…) while the project may have been indexed
// via the un-resolved cwd (/var/…), so a path built from the git root won't match
// the indexed paths. Try the project-relative path first (Symbols joins it to cwd,
// matching the index's path form), then fall back to the git-root-absolute path
// (covering a project nested below the repo root). nil when neither resolves.
func (svc *Service) symbolsForChangedFile(cwd, gitRoot, relPath string) []SymbolRef {
	if rep, err := svc.Symbols(cwd, relPath); err == nil && rep != nil && len(rep.Symbols) > 0 {
		return rep.Symbols
	}
	if rep, err := svc.Symbols(cwd, filepath.Join(gitRoot, relPath)); err == nil && rep != nil {
		return rep.Symbols
	}
	return nil
}

// symbolTouched reports whether any changed hunk overlaps the symbol's line span.
func symbolTouched(s SymbolRef, hunks []git.LineRange) bool {
	if s.StartLine == 0 {
		return false
	}
	end := s.EndLine
	if end < s.StartLine {
		end = s.StartLine
	}
	for _, h := range hunks {
		if h.Overlaps(s.StartLine, end) {
			return true
		}
	}
	return false
}

// symKey is the de-dup identity for a symbol/node across the union passes.
func symKey(fqn, file string, line int) string {
	return fqn + "\x00" + file + "\x00" + fmt.Sprint(line)
}
