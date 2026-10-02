package app

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

// ReadOrderOpts selects how many entries to rank and an optional case-insensitive
// name/path filter (so "read order for http handling" narrows to matching symbols).
type ReadOrderOpts struct {
	Top             int
	Query           string
	EntrypointsOnly bool // internal orientation mode: exclude pure hubs before applying Top
	// IncludeTests ranks test-defined symbols and counts test callers toward
	// importance. Default false: tests (and their mocks) never outrank product code.
	IncludeTests bool
}

// ReadEntry is one ranked symbol in a suggested reading order, with the reason it
// earned its rank so an agent can decide whether to follow it.
type ReadEntry struct {
	Rank      int     `json:"rank"`
	Symbol    string  `json:"symbol"`
	FQN       string  `json:"fqn,omitempty"`
	Kind      string  `json:"kind"`
	File      string  `json:"file"`
	StartLine int     `json:"start_line"`
	Score     float64 `json:"score"`
	InDegree  int     `json:"in_degree"`
	// EffectiveInDegree is the importance input: name-based callers divided by the
	// number of same-named definitions, plus precise callers (see
	// graph.RankedHotspots). Equals in_degree for uniquely-named symbols.
	EffectiveInDegree float64 `json:"effective_in_degree"`
	Entrypoint        bool    `json:"entrypoint"`
	Reason            string  `json:"reason"`
}

// ReadOrderReport ranks the symbols an agent should read FIRST to understand a
// codebase — entrypoints (main, cmd, module roots, public API) and the
// load-bearing hubs (high call-graph in-degree) — newcomer's reading guide in one
// call. Resolution is set when there's no call graph (importance is unavailable, so
// the ranking leans on entrypoint heuristics only).
type ReadOrderReport struct {
	Project string      `json:"project"`
	Indexed bool        `json:"indexed"`
	Query   string      `json:"query,omitempty"`
	Entries []ReadEntry `json:"entries"`
	// TestsExcluded is true when test code was left out of the ranking (default).
	TestsExcluded bool   `json:"tests_excluded"`
	Resolution    string `json:"resolution,omitempty"`
	Note          string `json:"note,omitempty"`
	totalEntries  int
	truncated     bool
}

// Read-order scoring weights. Importance (call-graph centrality) and
// entrypoint-ness both matter; the weights keep a program's main and its busiest
// hub in the same top tier.
const (
	readWeightImportance = 0.6
	readWeightEntrypoint = 0.55
	readDefaultTop       = 20
)

// ReadOrder ranks functions/methods by where an agent should start reading: a
// blend of call-graph centrality (importance) and entrypoint heuristics (main,
// cmd/, wired handlers, module index files, exported roots), optionally filtered
// by a name/path query. Importance is the EFFECTIVE in-degree from
// g.RankedHotspots — tests do not count as callers or candidates (unless
// opts.IncludeTests) and name-based fan-out is divided across same-named
// definitions. The raw in_degree is still reported unchanged.
func (svc *Service) ReadOrder(cwd string, opts ReadOrderOpts) (*ReadOrderReport, error) {
	top := opts.Top
	if top <= 0 {
		top = readDefaultTop
	}
	pid, name, found, err := svc.project(cwd)
	if err != nil {
		return nil, err
	}
	rep := &ReadOrderReport{Project: name, Indexed: found, Query: opts.Query, Entries: []ReadEntry{}, TestsExcluded: !opts.IncludeTests}
	if !found {
		return rep, nil
	}
	g, _ := svc.s.Graph()

	// Centrality for EVERY called node. Absence from this map means a genuine
	// in-degree of 0 ("no internal callers") — the entrypoint heuristic below relies
	// on that distinction.
	ranked, err := g.RankedHotspots(pid, graph.HotspotRankOptions{IncludeTests: opts.IncludeTests})
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]graph.RankedHotspot, len(ranked))
	maxEff := 0.0
	for _, h := range ranked {
		byID[h.Node.ID] = h
		if h.EffectiveInDegree > maxEff {
			maxEff = h.EffectiveInDegree
		}
	}
	wired, err := g.WiredTargetCounts(pid, opts.IncludeTests)
	if err != nil {
		return nil, err
	}
	defs, err := g.SymbolDefCounts(pid)
	if err != nil {
		return nil, err
	}

	nodes, err := g.ProjectNodes(pid)
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(strings.TrimSpace(opts.Query))

	var entries []ReadEntry
	for _, n := range nodes {
		if n.Kind != graph.KindFunction && n.Kind != graph.KindMethod {
			continue // only callable symbols are ranked; tests/types/vars/files excluded
		}
		if !opts.IncludeTests && graph.IsTestNode(n) {
			continue // test files, mocks and testdata are never starting points
		}
		if q != "" && !matchesQuery(n, q) {
			continue
		}
		h := byID[n.ID]
		deg, eff := h.InDegree, h.EffectiveInDegree
		imp := 0.0
		if maxEff > 0 {
			imp = eff / maxEff
		}
		// A value reference only evidences a registration when the name is unique:
		// references are name-based, so a shared name could be a different function.
		isWired := wired[n.ID] > 0 && defs[n.Symbol] <= 1 && deg == 0
		ep, epReason := entrypointScore(n, deg, isWired)
		if opts.EntrypointsOnly && ep <= 0 {
			continue
		}
		score := readWeightImportance*imp + readWeightEntrypoint*ep
		if n.Kind == graph.KindFunction && n.Symbol == "main" && !isAuxiliaryMain(n) {
			score += 1.0 // the program's entrypoint always leads the reading order
		}
		if score <= 0 {
			continue // a leaf with no callers and no entrypoint signal — not a starting point
		}
		entries = append(entries, ReadEntry{
			Symbol: n.Symbol, FQN: n.FQN, Kind: n.Kind, File: n.FilePath, StartLine: n.StartLine,
			Score: round3(score), InDegree: deg, EffectiveInDegree: round2(eff), Entrypoint: ep > 0,
			Reason: readReason(epReason, ep, imp, deg, eff, h.SharedDefs),
		})
	}

	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Score != entries[j].Score {
			return entries[i].Score > entries[j].Score
		}
		if entries[i].EffectiveInDegree != entries[j].EffectiveInDegree {
			return entries[i].EffectiveInDegree > entries[j].EffectiveInDegree
		}
		if entries[i].InDegree != entries[j].InDegree {
			return entries[i].InDegree > entries[j].InDegree
		}
		if entries[i].File != entries[j].File {
			return entries[i].File < entries[j].File
		}
		if entries[i].StartLine != entries[j].StartLine {
			return entries[i].StartLine < entries[j].StartLine
		}
		if entries[i].FQN != entries[j].FQN {
			return entries[i].FQN < entries[j].FQN
		}
		if entries[i].Kind != entries[j].Kind {
			return entries[i].Kind < entries[j].Kind
		}
		return entries[i].Symbol < entries[j].Symbol
	})
	rep.totalEntries = len(entries)
	if len(entries) > top {
		entries = entries[:top]
		rep.truncated = true
	}
	for i := range entries {
		entries[i].Rank = i + 1
	}
	rep.Entries = entries

	if maxEff == 0 {
		rep.Resolution = "no call graph — importance is unavailable, so this ranking uses entrypoint heuristics only; reindex with 'codemap index --precise' (TS/JS/Python) for call-graph importance"
	}
	if len(rep.Entries) == 0 {
		rep.Note = "no entrypoints or hubs found" + queryNote(opts.Query)
	}
	return rep, nil
}

// auxiliaryDirs are directory names whose main() is a helper program (benchmark
// harness, example, script, dev tool), not the project's real entrypoint.
var auxiliaryDirs = map[string]bool{
	"bench": true, "benchmarks": true, "benchmark": true, "examples": true, "example": true,
	"scripts": true, "tools": true, "hack": true, "testdata": true,
}

// isAuxiliaryMain reports whether a main() is an auxiliary program. A Go main
// under the top-level cmd/ directory, or at the repo root, is primary; otherwise
// any path segment in auxiliaryDirs (this covers internal/tools) marks it
// auxiliary. A JavaScript/TypeScript function named main is a script helper
// (smoke tests, build scripts), never the program entrypoint.
func isAuxiliaryMain(n graph.Node) bool {
	switch strings.ToLower(n.Language) {
	case "javascript", "typescript", "tsx", "jsx":
		return true
	}
	clean := filepath.ToSlash(n.FilePath)
	dir := filepath.ToSlash(filepath.Dir(clean))
	if dir == "." || dir == "" {
		return false
	}
	segs := strings.Split(dir, "/")
	if segs[0] == "cmd" {
		return false
	}
	for _, s := range segs {
		if auxiliaryDirs[strings.ToLower(s)] {
			return true
		}
	}
	return false
}

// inEntrypointPackage reports whether a node lives in a command package: under a
// cmd/ directory or in a main.go file.
func inEntrypointPackage(n graph.Node) bool {
	base := strings.ToLower(filepath.Base(n.FilePath))
	return strings.Contains(n.FilePath, "/cmd/") || strings.HasPrefix(n.FilePath, "cmd/") || base == "main.go"
}

// entrypointScore rates how much a node looks like a place to START reading, and
// why. Strongest: a program's main() under cmd/ or the repo root; then functions
// wired by value as handlers in a command package (cobra RunE: runX, referenced
// but never called directly); then module index files; then exported roots
// (public API not called internally) and other exported symbols. Other symbols in
// a command package (helpers, and ALL methods such as Error/String on a main
// type) are not entrypoints and score 0. A main() under
// bench/examples/scripts/tools/hack is ranked as an auxiliary program
// entrypoint, below the real one.
func entrypointScore(n graph.Node, indeg int, wired bool) (float64, string) {
	base := strings.ToLower(filepath.Base(n.FilePath))
	cmdPkg := inEntrypointPackage(n)
	isFunc := n.Kind == graph.KindFunction
	switch {
	case isFunc && n.Symbol == "main" && isAuxiliaryMain(n):
		return 0.5, "auxiliary program entrypoint — main() under a bench/examples/tools directory"
	case isFunc && n.Symbol == "main":
		return 1.0, "program entrypoint — main()"
	case isFunc && cmdPkg && wired:
		return 0.9, "wired handler — registered by value (e.g. a cobra RunE) in an entrypoint package"
	case base == "index.ts" || base == "index.tsx" || base == "index.js" || base == "index.jsx" || base == "index.mjs" || base == "__main__.py":
		return 0.8, "module entrypoint file"
	case cmdPkg: // helpers, methods and values in a command package are neither entrypoints nor public API
		return 0, ""
	case !publicSurface(n):
		return 0, ""
	case isExportedName(n.Symbol) && indeg == 0:
		return 0.65, "exported root — public API / handler (no internal callers)"
	case isExportedName(n.Symbol):
		return 0.35, "exported (public API surface)"
	default:
		return 0, ""
	}
}

// publicSurface reports whether a callable can be part of a package's public API.
// For Go a method is public only when both the method and its receiver type are
// exported (FQN pkg.Type.Method), so Error/String on an unexported type are not.
func publicSurface(n graph.Node) bool {
	if n.Kind != graph.KindMethod || n.Language != "go" {
		return true
	}
	parts := strings.Split(n.FQN, ".")
	if len(parts) < 3 {
		return true
	}
	return isExportedName(parts[len(parts)-2])
}

// readReason picks the dominant explanation: the entrypoint reason when it leads,
// otherwise the hub/centrality reason, mentioning both when both are strong.
func readReason(epReason string, ep, imp float64, indeg int, eff float64, sharedDefs int) string {
	hub := ""
	if indeg > 0 {
		hub = fmt.Sprintf("central — %d caller(s)", indeg)
		if sharedDefs > 1 && eff < float64(indeg)-0.05 {
			hub = fmt.Sprintf("central — %d caller(s), ~%.1f effective (name shared by %d)", indeg, eff, sharedDefs)
		}
	}
	switch {
	case ep > 0 && readWeightEntrypoint*ep >= readWeightImportance*imp:
		if hub != "" {
			return epReason + "; also " + hub
		}
		return epReason
	case hub != "":
		if epReason != "" {
			return hub + "; also " + epReason
		}
		return hub
	default:
		return epReason
	}
}

// isExportedName treats a leading uppercase letter as "exported/public" — exact for
// Go, a reasonable public-surface heuristic for the other languages.
func isExportedName(sym string) bool {
	if sym == "" {
		return false
	}
	// For a method written as Type.Method, judge the method name itself.
	if i := strings.LastIndex(sym, "."); i >= 0 && i+1 < len(sym) {
		sym = sym[i+1:]
	}
	r := []rune(sym)[0]
	return unicode.IsUpper(r)
}

func matchesQuery(n graph.Node, qLower string) bool {
	return strings.Contains(strings.ToLower(n.Symbol), qLower) ||
		strings.Contains(strings.ToLower(n.FQN), qLower) ||
		strings.Contains(strings.ToLower(n.FilePath), qLower)
}

func queryNote(q string) string {
	if strings.TrimSpace(q) == "" {
		return ""
	}
	return fmt.Sprintf(" matching %q", q)
}

func round3(f float64) float64 {
	return float64(int(f*1000+0.5)) / 1000
}

func round2(f float64) float64 {
	return float64(int(f*100+0.5)) / 100
}
