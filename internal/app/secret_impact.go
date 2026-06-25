package app

import (
	"errors"

	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

// SecretUsage is one symbol that reads a secret key (or an unresolved usage site).
// Confidence: "string" (a Go string literal — exact) or "code" (a non-comment line
// in another language — heuristic).
type SecretUsage struct {
	Symbol     string `json:"symbol,omitempty"`
	FQN        string `json:"fqn,omitempty"`
	Kind       string `json:"kind,omitempty"`
	File       string `json:"file"`
	Line       int    `json:"line"`
	Confidence string `json:"confidence"`
}

// SecretKeyImpact is the code blast radius of one secret key.
type SecretKeyImpact struct {
	Key           string        `json:"key"`
	UsedBy        []SecretUsage `json:"used_by"`              // symbols that read the key
	Unresolved    []SecretUsage `json:"unresolved,omitempty"` // usage sites with no enclosing symbol
	BlastRadius   int           `json:"blast_radius"`         // transitively-affected symbols (union over UsedBy)
	CoveringTests int           `json:"covering_tests"`       // tests reaching any reader
	Untested      bool          `json:"untested"`             // read by code no test reaches
}

// SecretImpactReport answers "what code breaks if I rotate these keys?" — value-free
// (only key NAMES, symbols, and file:line; never a secret value or a line's content).
type SecretImpactReport struct {
	Project    string            `json:"project"`
	Indexed    bool              `json:"indexed"`
	Precise    bool              `json:"precise"`         // false → blast radius is name-based and may over-count
	Stale      bool              `json:"stale,omitempty"` // index drifted from disk; reindex before trusting a rotation
	Keys       []SecretKeyImpact `json:"keys"`
	OrphanKeys []string          `json:"orphan_keys,omitempty"` // no code usages found — VERIFY before treating as dead (dynamic os.Getenv(prefix+x) is invisible)
	Note       string            `json:"note,omitempty"`
}

// SecretImpact computes the code blast radius of each secret key NAME: it scans the
// indexed source for string-literal usages of the key (scanLiteralUsages — comments
// excluded), resolves each to its enclosing symbol, and unions the transitive
// callers + covering tests. It NEVER reads secret values — keys are names supplied
// by the caller; codemap only scans its own indexed source. Frame the result as
// CANDIDATE usage + impact (precise blast radius needs --precise + first-class
// env-read nodes), not an authoritative rotation gate — hence Precise/Stale surface.
func (svc *Service) SecretImpact(cwd string, keys []string, depth int) (*SecretImpactReport, error) {
	if depth <= 0 {
		depth = 3
	}
	g, err := svc.s.Graph()
	if err != nil {
		return nil, err
	}
	root, name, err := svc.resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	rep := &SecretImpactReport{Project: name, Keys: []SecretKeyImpact{}}
	p, err := g.GetProjectByName(name)
	if errors.Is(err, graph.ErrNotFound) {
		return rep, nil // indexed:false
	}
	if err != nil {
		return nil, err
	}
	rep.Indexed = true
	rep.Precise = svc.hasPreciseEdges(g, p.ID)
	files, err := g.IndexedFiles(p.ID)
	if err != nil {
		return nil, err
	}

	for _, key := range keys {
		ki := SecretKeyImpact{Key: key, UsedBy: []SecretUsage{}}
		seenSym := map[string]bool{}
		blast := map[int64]bool{} // affected nodes, deduped by id
		tests := map[string]bool{}
		for _, site := range scanLiteralUsages(root, files, key) {
			n, ok, nerr := g.NodeAtLine(p.ID, site.File, site.Line)
			if nerr != nil {
				return nil, nerr
			}
			if !ok {
				ki.Unresolved = append(ki.Unresolved, SecretUsage{File: site.File, Line: site.Line, Confidence: site.Confidence})
				continue
			}
			u := SecretUsage{Symbol: n.Symbol, FQN: n.FQN, Kind: n.Kind, File: site.File, Line: site.Line, Confidence: site.Confidence}
			symKey := n.FQN
			if symKey == "" {
				symKey = n.Symbol
			}
			if seenSym[symKey] {
				continue
			}
			seenSym[symKey] = true
			ki.UsedBy = append(ki.UsedBy, u)
			// What breaks if this reader changes: its transitive callers + covering tests.
			if radius, rerr := g.BlastRadius(p.ID, n.Symbol, depth); rerr == nil {
				for _, nd := range radius {
					blast[nd.Node.ID] = true
				}
			}
			for _, t := range heuristicTestCoverage(g, p.ID, root, n.Symbol) {
				tests[t.File+"\x00"+t.Symbol] = true
			}
		}
		ki.BlastRadius = len(blast)
		ki.CoveringTests = len(tests)
		ki.Untested = len(ki.UsedBy) > 0 && len(tests) == 0
		if len(ki.UsedBy) == 0 && len(ki.Unresolved) == 0 {
			rep.OrphanKeys = append(rep.OrphanKeys, key)
			continue
		}
		rep.Keys = append(rep.Keys, ki)
	}

	if !rep.Precise {
		rep.Note = "blast radius is name-based and may over-count (a same-named method merges all defs); reindex with 'codemap index --precise' for exact figures"
	}
	if st, serr := svc.Staleness(cwd); serr == nil && st != nil && st.Any() {
		rep.Stale = true
	}
	return rep, nil
}
