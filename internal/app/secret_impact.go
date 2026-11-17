package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	// TaintPaths are bounded forward call-graph paths from this key's readers to
	// name-matched sinks (see TaintPath). Always non-nil: a reader with no
	// reachable sink yields [], not an error.
	TaintPaths []TaintPath `json:"taint_paths"`
}

// TaintPath is one bounded candidate propagation path from a key-reading symbol
// to a symbol that can LEAK the value externally. Heuristic by design: the sink
// is matched BY NAME against taintSinks inside the reached symbol's body, and
// reaching a sink-bearing symbol does not prove the secret value itself flows
// there — this is a call-graph walk, not a PDG. Confidence is always
// "candidate".
type TaintPath struct {
	From       string   `json:"from"`       // reading symbol's fqn
	To         string   `json:"to"`         // sink-bearing symbol's fqn
	Hops       []string `json:"hops"`       // fqns from From to To, inclusive
	Sink       string   `json:"sink"`       // matched name from the documented taintSinks list
	Depth      int      `json:"depth"`      // call-graph edges between From and To
	Confidence string   `json:"confidence"` // always "candidate" — name-based, no data-flow proof
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
	// TaintSinks documents the additive name list taint_paths sinks are matched
	// against; TaintNote explains the heuristic and its limits.
	TaintSinks     []string `json:"taint_sinks,omitempty"`
	TaintNote      string   `json:"taint_note,omitempty"`
	TaintTruncated bool     `json:"taint_truncated,omitempty"` // more taint paths existed than the bound allows
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

	taintTruncated := false
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		ki := SecretKeyImpact{Key: key, UsedBy: []SecretUsage{}, TaintPaths: []TaintPath{}}
		seenSym := map[string]bool{}
		blast := map[int64]bool{} // affected nodes, deduped by id
		tests := map[string]bool{}
		readers := []graph.Node{} // resolved reader nodes, the taint-path roots
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
			readers = append(readers, n)
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
		if len(readers) > 0 {
			paths, truncated, terr := taintPathsFrom(ctx, g, p.ID, root, readers, depth)
			if terr != nil {
				return nil, terr
			}
			ki.TaintPaths = paths
			taintTruncated = taintTruncated || truncated
		}
		if len(ki.UsedBy) == 0 && len(ki.Unresolved) == 0 {
			rep.OrphanKeys = append(rep.OrphanKeys, key)
			continue
		}
		rep.Keys = append(rep.Keys, ki)
	}

	rep.TaintSinks = append([]string(nil), taintSinks...)
	rep.TaintNote = fmt.Sprintf("taint_paths are a bounded heuristic: from each key-reading symbol the forward call graph is walked up to %d hops and any reached symbol whose body mentions a sink name is reported; sinks are matched by name (%s) with no data-flow proof — this is not a PDG — so every path carries confidence \"candidate\"", depth, strings.Join(taintSinks, ", "))
	if taintTruncated {
		rep.TaintTruncated = true
		rep.TaintNote += fmt.Sprintf("; more than %d paths existed and taint_paths is truncated at %d", MaxTaintPaths, MaxTaintPaths)
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

// MaxTaintPaths bounds how many taint_paths a report carries; beyond it the
// list is truncated and taint_truncated records that more existed.
const MaxTaintPaths = 20

// taintSinks is the documented, additive sink-name list: a reached symbol whose
// body mentions one of these names is reported as a candidate leak. Matched as
// a plain substring of the symbol's source lines — deliberately coarse; new
// sinks are appended (additive JSON), never removed.
var taintSinks = []string{
	"http.PostForm", "http.Post",
	"fetch(", "axios.post", "requests.post",
	"fmt.Fprintf(w,", "os.WriteFile",
	"log.Print", "logrus.", "zap.",
	"slog.Info", "slog.Error", "slog.Warn",
	"print(", "console.log",
}

// taintPathsFrom walks the forward call graph (callees) from each key-reading
// symbol up to maxDepth hops and records paths to symbols whose bodies mention
// a taintSinks name. Cycle-safe: each path keeps its own on-path set, so a
// recursive call graph terminates while distinct routes still count. Value-free
// output — only fqns and the matched sink NAME, never line content.
func taintPathsFrom(ctx context.Context, g *graph.Store, projectID int64, root string, readers []graph.Node, maxDepth int) ([]TaintPath, bool, error) {
	w := &taintWalker{g: g, projectID: projectID, root: root, maxDepth: maxDepth, lines: map[string][]string{}, paths: []TaintPath{}}
	for i, r := range readers {
		if err := w.walk(ctx, r, nil, map[int64]bool{}); err != nil {
			return nil, false, err
		}
		if len(w.paths) >= MaxTaintPaths {
			if i < len(readers)-1 {
				w.truncated = true // unexplored readers may hold more paths
			}
			break
		}
	}
	return w.paths, w.truncated, nil
}

// taintWalker is the per-report state of one taint-path enumeration: a
// file→lines cache (each sink check reads the symbol's body, and fan-out
// fixtures touch the same files repeatedly) plus the bounded path list.
type taintWalker struct {
	g         *graph.Store
	projectID int64
	root      string
	maxDepth  int
	lines     map[string][]string
	paths     []TaintPath
	truncated bool
}

// walk DFS-enumerates forward paths through n. onPath is the current path's
// visited set: a node already on the path closes a cycle and is skipped, while
// the same node may appear on other paths.
func (w *taintWalker) walk(ctx context.Context, n graph.Node, path []graph.Node, onPath map[int64]bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if onPath[n.ID] {
		return nil // cycle: this path already visits n
	}
	onPath[n.ID] = true
	defer delete(onPath, n.ID)
	path = append(path, n)
	if len(w.paths) >= MaxTaintPaths {
		// At the cap: still sink-check reached nodes so a further sink flags
		// truncation, but record nothing and do not expand deeper.
		if w.sinkIn(n) != "" {
			w.truncated = true
		}
		return nil
	}
	if sink := w.sinkIn(n); sink != "" {
		hops := make([]string, len(path))
		for i, p := range path {
			hops[i] = taintNodeLabel(p)
		}
		w.paths = append(w.paths, TaintPath{
			From:       hops[0],
			To:         hops[len(hops)-1],
			Hops:       hops,
			Sink:       sink,
			Depth:      len(hops) - 1,
			Confidence: "candidate",
		})
	}
	if len(path)-1 >= w.maxDepth {
		return nil
	}
	callees, err := w.g.CalleesOfNode(w.projectID, n.ID)
	if err != nil {
		return err
	}
	for _, c := range callees {
		if err := w.walk(ctx, c, path, onPath); err != nil {
			return err
		}
	}
	return nil
}

// sinkIn returns the first taintSinks name mentioned in n's source body, or "".
// An unreadable file simply has no sinks — a heuristic must not fail the report.
func (w *taintWalker) sinkIn(n graph.Node) string {
	lines, ok := w.lines[n.FilePath]
	if !ok {
		if b, err := os.ReadFile(filepath.Join(w.root, n.FilePath)); err == nil {
			lines = strings.Split(string(b), "\n")
		}
		w.lines[n.FilePath] = lines
	}
	start, end := n.StartLine, n.EndLine
	if start < 1 {
		start = 1
	}
	if end > len(lines) {
		end = len(lines)
	}
	for i := start; i <= end; i++ {
		for _, sink := range taintSinks {
			if strings.Contains(lines[i-1], sink) {
				return sink
			}
		}
	}
	return ""
}

// taintNodeLabel is a node's path-facing identity: fqn when present, else symbol.
func taintNodeLabel(n graph.Node) string {
	if n.FQN != "" {
		return n.FQN
	}
	return n.Symbol
}
