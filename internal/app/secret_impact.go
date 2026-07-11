package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"unicode/utf8"

	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

const (
	// MaxSecretKeyNames bounds one inventory/scan request after trimming and
	// deduplication. This is deliberately high enough for real applications but
	// low enough that an agent cannot accidentally turn a query into unbounded
	// regex and graph work.
	MaxSecretKeyNames = 256
	// MaxSecretKeyNameBytes bounds each key name in bytes. Secret values are not
	// accepted here; ordinary environment/vault key names are far shorter.
	MaxSecretKeyNameBytes = 256
)

// SecretImpactWithInventory merges explicitly supplied key names with the
// value-free inventory from tinyvault, then computes their code impact. The
// only tinyvault operation reachable here is `list --json`; secret values are
// never requested. prefix applies only to inventory keys.
func (svc *Service) SecretImpactWithInventory(ctx context.Context, cwd string, keys []string, depth int, vaultProject, prefix string) (*SecretImpactReport, error) {
	ctx = secretScanContext(ctx)
	merged, err := keyNamesWithInventory(ctx, keys, vaultProject, prefix)
	if err != nil {
		return nil, err
	}
	if len(merged) == 0 {
		return nil, errors.New("supply one or more secret key names or a via_vault project")
	}
	return svc.SecretImpactWithContext(ctx, cwd, merged, depth)
}

// RequiredKeysWithInventory is the inventory-aware form of RequiredKeys used
// by agent and human surfaces that accept tinyvault as a source of candidate
// key names. It shares SecretImpactWithInventory's value-free list-only path.
func (svc *Service) RequiredKeysWithInventory(ctx context.Context, cwd, entrypoint string, keys []string, depth int, vaultProject, prefix string) (*RequiredKeysReport, error) {
	ctx = secretScanContext(ctx)
	merged, err := keyNamesWithInventory(ctx, keys, vaultProject, prefix)
	if err != nil {
		return nil, err
	}
	if len(merged) == 0 {
		return nil, errors.New("supply one or more candidate key names or a via_vault project")
	}
	return svc.RequiredKeysWithContext(ctx, cwd, entrypoint, merged, depth)
}

func keyNamesWithInventory(ctx context.Context, keys []string, vaultProject, prefix string) ([]string, error) {
	merged := append([]string(nil), keys...)
	if vaultProject != "" {
		vaultKeys, err := listVaultKeyNames(ctx, vaultProject, prefix)
		if err != nil {
			return nil, err
		}
		merged = append(merged, vaultKeys...)
	}
	return validateSecretKeyNames(merged)
}

func listVaultKeyNames(ctx context.Context, project, prefix string) ([]string, error) {
	ctx = secretScanContext(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	tvault, err := exec.LookPath("tvault")
	if err != nil {
		return nil, errors.New("via_vault needs tinyvault: tvault not found on PATH")
	}
	args := []string{"-p", project, "list", "--json"}
	if prefix != "" {
		args = append(args, "--prefix", prefix)
	}
	out, err := exec.CommandContext(ctx, tvault, args...).Output()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("tvault list failed for project %q: %w", project, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var keys []string
	if err := json.Unmarshal(out, &keys); err != nil {
		return nil, fmt.Errorf("parse tvault list output: %w", err)
	}
	return keys, nil
}

func secretScanContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func validateSecretKeyNames(keys []string) ([]string, error) {
	seen := make(map[string]bool, len(keys))
	out := make([]string, 0, len(keys))
	for i, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" || seen[key] {
			continue
		}
		if !utf8.ValidString(key) {
			return nil, fmt.Errorf("secret key name %d is not valid UTF-8", i+1)
		}
		if len(key) > MaxSecretKeyNameBytes {
			return nil, fmt.Errorf("secret key name %d exceeds the %d-byte limit", i+1, MaxSecretKeyNameBytes)
		}
		if len(out) == MaxSecretKeyNames {
			return nil, fmt.Errorf("too many unique secret key names: maximum is %d", MaxSecretKeyNames)
		}
		seen[key] = true
		out = append(out, key)
	}
	return out, nil
}

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
	BlastRadius   int           `json:"blast_radius_count"`   // transitively-affected symbols (union over UsedBy)
	CoveringTests int           `json:"covering_tests_count"` // tests reaching any reader
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
	return svc.SecretImpactWithContext(context.Background(), cwd, keys, depth)
}

// SecretImpactWithContext is SecretImpact with cancellation propagated through
// inventory-independent source scanning and the per-usage graph expansion.
func (svc *Service) SecretImpactWithContext(ctx context.Context, cwd string, keys []string, depth int) (*SecretImpactReport, error) {
	ctx = secretScanContext(ctx)
	keys, err := validateSecretKeyNames(keys)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if depth <= 0 {
		depth = 3
	}
	g, err := svc.s.Graph()
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
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
	projectNodes, err := g.ProjectNodes(p.ID)
	if err != nil {
		return nil, err
	}
	projectCallGraph := svc.callGraphStatus(g, p.ID, callableNodes(projectNodes))
	rep.Precise = projectCallGraph == CallGraphResolved
	files, err := g.IndexedFiles(p.ID)
	if err != nil {
		return nil, err
	}
	sitesByKey, err := scanLiteralUsagesForKeys(ctx, root, files, keys)
	if err != nil {
		return nil, err
	}

	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		ki := SecretKeyImpact{Key: key, UsedBy: []SecretUsage{}}
		seenSym := map[string]bool{}
		blast := map[int64]bool{} // affected nodes, deduped by id
		tests := map[string]bool{}
		for _, site := range sitesByKey[key] {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
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
			if err := ctx.Err(); err != nil {
				return nil, err
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

	if projectCallGraph == CallGraphUnresolved {
		rep.Note = "blast radius is unresolved for at least one callable file; reindex with 'codemap index --precise' before using this as a rotation gate"
	} else if !rep.Precise {
		rep.Note = "blast radius is name-based and may over-count (a same-named method merges all defs); reindex with 'codemap index --precise' for exact figures"
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if st, serr := svc.Staleness(cwd); serr == nil && st != nil && st.Any() {
		rep.Stale = true
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return rep, nil
}

// RequiredKeysReport is the minimal set of secret keys an entrypoint's transitive
// call tree actually reads — for least-privilege sealing (seal/inject only these).
type RequiredKeysReport struct {
	Project      string   `json:"project"`
	Entrypoint   string   `json:"entrypoint"`
	Found        bool     `json:"found"`
	RequiredKeys []string `json:"required_keys"`
	Note         string   `json:"note,omitempty"`
}

// RequiredKeys returns the subset of candidateKeys that are read by entrypoint or
// anything in its transitive callee set (forward call-graph closure to depth hops).
// This is the EI.13 least-privilege seal scope: tinyvault can then seal/export only
// the keys this code path needs. Value-free — keys are names; codemap only scans
// its own indexed source.
func (svc *Service) RequiredKeys(cwd, entrypoint string, candidateKeys []string, depth int) (*RequiredKeysReport, error) {
	return svc.RequiredKeysWithContext(context.Background(), cwd, entrypoint, candidateKeys, depth)
}

// RequiredKeysWithContext is RequiredKeys with bounded key validation, one-pass
// indexed-source scanning, and cancellation between source and graph operations.
func (svc *Service) RequiredKeysWithContext(ctx context.Context, cwd, entrypoint string, candidateKeys []string, depth int) (*RequiredKeysReport, error) {
	ctx = secretScanContext(ctx)
	candidateKeys, err := validateSecretKeyNames(candidateKeys)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if depth <= 0 {
		depth = 5 // least-privilege wants a deeper closure than the blast-radius default
	}
	g, err := svc.s.Graph()
	if err != nil {
		return nil, err
	}
	root, name, err := svc.resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	rep := &RequiredKeysReport{Project: name, Entrypoint: entrypoint, RequiredKeys: []string{}}
	p, err := g.GetProjectByName(name)
	if errors.Is(err, graph.ErrNotFound) {
		return rep, nil
	}
	if err != nil {
		return nil, err
	}
	closure, err := g.CalleeClosure(p.ID, entrypoint, depth)
	if err != nil {
		return nil, err
	}
	if len(closure) == 0 {
		return rep, nil // entrypoint not in the graph
	}
	rep.Found = true
	projectNodes, err := g.ProjectNodes(p.ID)
	if err != nil {
		return nil, err
	}
	closureNodes := make([]graph.Node, 0, len(closure))
	for _, n := range callableNodes(projectNodes) {
		if closure[n.ID] {
			closureNodes = append(closureNodes, n)
		}
	}
	closureCallGraph := svc.callGraphStatus(g, p.ID, closureNodes)
	files, err := g.IndexedFiles(p.ID)
	if err != nil {
		return nil, err
	}
	sitesByKey, err := scanLiteralUsagesForKeys(ctx, root, files, candidateKeys)
	if err != nil {
		return nil, err
	}
	for _, key := range candidateKeys {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for _, site := range sitesByKey[key] {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if n, ok, nerr := g.NodeAtLine(p.ID, site.File, site.Line); nerr == nil && ok && closure[n.ID] {
				rep.RequiredKeys = append(rep.RequiredKeys, key)
				break
			}
		}
	}
	if closureCallGraph == CallGraphUnresolved {
		rep.Note = "callee closure is unresolved for at least one file; reindex with --precise before using this as an exact least-privilege set"
	} else if closureCallGraph != CallGraphResolved {
		rep.Note = "callee closure is name-based; reindex with --precise for an exact least-privilege set"
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return rep, nil
}
