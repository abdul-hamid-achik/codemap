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
}

// ReadEntry is one ranked symbol in a suggested reading order, with the reason it
// earned its rank so an agent can decide whether to follow it.
type ReadEntry struct {
	Rank       int     `json:"rank"`
	Symbol     string  `json:"symbol"`
	FQN        string  `json:"fqn,omitempty"`
	Kind       string  `json:"kind"`
	File       string  `json:"file"`
	StartLine  int     `json:"start_line"`
	Score      float64 `json:"score"`
	InDegree   int     `json:"in_degree"`
	Entrypoint bool    `json:"entrypoint"`
	Reason     string  `json:"reason"`
}

// ReadOrderReport ranks the symbols an agent should read FIRST to understand a
// codebase — entrypoints (main, cmd, module roots, public API) and the
// load-bearing hubs (high call-graph in-degree) — newcomer's reading guide in one
// call. Resolution is set when there's no call graph (importance is unavailable, so
// the ranking leans on entrypoint heuristics only).
type ReadOrderReport struct {
	Project      string      `json:"project"`
	Indexed      bool        `json:"indexed"`
	Query        string      `json:"query,omitempty"`
	Entries      []ReadEntry `json:"entries"`
	Resolution   string      `json:"resolution,omitempty"`
	Note         string      `json:"note,omitempty"`
	totalEntries int
	truncated    bool
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
// blend of call-graph in-degree (importance) and entrypoint heuristics (main,
// cmd/, module index files, exported roots), optionally filtered by a name/path
// query. Reuses g.Hotspots for in-degree and g.ProjectNodes for the candidate set.
func (svc *Service) ReadOrder(cwd string, opts ReadOrderOpts) (*ReadOrderReport, error) {
	top := opts.Top
	if top <= 0 {
		top = readDefaultTop
	}
	pid, name, found, err := svc.project(cwd)
	if err != nil {
		return nil, err
	}
	rep := &ReadOrderReport{Project: name, Indexed: found, Query: opts.Query, Entries: []ReadEntry{}}
	if !found {
		return rep, nil
	}
	g, _ := svc.s.Graph()

	// In-degree for EVERY called node (importance). The limit is high enough to
	// capture all call targets in normal projects, so absence from this map means a
	// genuine in-degree of 0 ("no internal callers") rather than "beyond the cap" —
	// the entrypoint heuristic below relies on that distinction.
	hs, err := g.Hotspots(pid, 100000)
	if err != nil {
		return nil, err
	}
	indeg := make(map[int64]int, len(hs))
	maxDeg := 0
	for _, h := range hs {
		indeg[h.Node.ID] = h.InDegree
		if h.InDegree > maxDeg {
			maxDeg = h.InDegree
		}
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
		if q != "" && !matchesQuery(n, q) {
			continue
		}
		deg := indeg[n.ID]
		imp := 0.0
		if maxDeg > 0 {
			imp = float64(deg) / float64(maxDeg)
		}
		ep, epReason := entrypointScore(n, deg)
		if opts.EntrypointsOnly && ep <= 0 {
			continue
		}
		score := readWeightImportance*imp + readWeightEntrypoint*ep
		if n.Kind == graph.KindFunction && n.Symbol == "main" {
			score += 1.0 // the program's entrypoint always leads the reading order
		}
		if score <= 0 {
			continue // a leaf with no callers and no entrypoint signal — not a starting point
		}
		entries = append(entries, ReadEntry{
			Symbol: n.Symbol, FQN: n.FQN, Kind: n.Kind, File: n.FilePath, StartLine: n.StartLine,
			Score: round3(score), InDegree: deg, Entrypoint: ep > 0,
			Reason: readReason(epReason, ep, imp, deg),
		})
	}

	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Score != entries[j].Score {
			return entries[i].Score > entries[j].Score
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

	if maxDeg == 0 {
		rep.Resolution = "no call graph — importance is unavailable, so this ranking uses entrypoint heuristics only; reindex with 'codemap index --precise' (TS/JS/Python) for call-graph importance"
	}
	if len(rep.Entries) == 0 {
		rep.Note = "no entrypoints or hubs found" + queryNote(opts.Query)
	}
	return rep, nil
}

// entrypointScore rates how much a node looks like a place to START reading, and
// why. Strongest: a program's main(); then entrypoint files (cmd/, module index);
// then exported roots (public API not called internally) and other exported symbols.
func entrypointScore(n graph.Node, indeg int) (float64, string) {
	base := strings.ToLower(filepath.Base(n.FilePath))
	switch {
	case n.Kind == graph.KindFunction && n.Symbol == "main":
		return 1.0, "program entrypoint — main()"
	case strings.Contains(n.FilePath, "/cmd/") || strings.HasPrefix(n.FilePath, "cmd/") || base == "main.go":
		return 0.85, "in an entrypoint package (cmd/main)"
	case base == "index.ts" || base == "index.tsx" || base == "index.js" || base == "index.jsx" || base == "index.mjs" || base == "__main__.py":
		return 0.8, "module entrypoint file"
	case isExportedName(n.Symbol) && indeg == 0:
		return 0.65, "exported root — public API / handler (no internal callers)"
	case isExportedName(n.Symbol):
		return 0.35, "exported (public API surface)"
	default:
		return 0, ""
	}
}

// readReason picks the dominant explanation: the entrypoint reason when it leads,
// otherwise the hub/centrality reason, mentioning both when both are strong.
func readReason(epReason string, ep, imp float64, indeg int) string {
	hub := ""
	if indeg > 0 {
		hub = fmt.Sprintf("central — %d caller(s)", indeg)
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
