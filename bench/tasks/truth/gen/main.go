// Command gen is the INDEPENDENT ground-truth generator for the codemap
// benchmark. It loads the pinned fixture with the Go type checker (go/types via
// golang.org/x/tools/go/packages) and derives caller/callee/definition/
// blast-radius/path/test-coverage answers directly from type information.
//
// INDEPENDENCE RULE (load-bearing — see bench/tasks/truth/README): this program
// imports NOTHING from codemap (no internal/graph, no codemap binary). Its
// oracle is the Go type checker itself — the same independent source gopls uses,
// used here directly so the derivation is auditable in one file. Grading the
// benchmark against codemap's own graph would make arm B win by construction;
// this generator exists to prevent exactly that.
//
// Usage:
//
//	go run ./bench/tasks/truth/gen \
//	    -fixture bench/fixtures/repo \
//	    -targets bench/tasks/truth/targets.json \
//	    -out bench/tasks/truth
//
// It writes NN_*.json truth files and prints a summary (set sizes, path
// reachability) so a human can sanity-check before committing.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

// Target addresses a symbol in the fixture. Recv empty => package-level func.
type Target struct {
	Pkg  string `json:"pkg"`  // Go package NAME (e.g. "plumbing", "git")
	Recv string `json:"recv"` // receiver type name without pointer star (methods)
	Name string `json:"name"` // function/method name
}

func (t Target) fqn() string {
	if t.Recv != "" {
		return t.Pkg + "." + t.Recv + "." + t.Name
	}
	return t.Pkg + "." + t.Name
}

// Targets is the committed selection of fixture symbols each task points at.
type Targets struct {
	FindDef       Target `json:"find_def"`
	Callers       Target `json:"callers"`
	Callees       Target `json:"callees"`
	BlastRadius   Target `json:"blast_radius"`
	CoveringTests Target `json:"covering_tests"`
	DeadCode      Target `json:"dead_code"`
	CallPathFrom  Target `json:"call_path_from"`
	CallPathTo    Target `json:"call_path_to"`
	FileOutline   string `json:"file_outline"` // path relative to fixture root
	RenameImpact  Target `json:"rename_impact"`
	CallersMethod Target `json:"callers_method"`
	BlastDepth    int    `json:"blast_depth"`
}

type model struct {
	fset    *token.FileSet
	defs    map[string]defSite         // fqn -> definition site
	out     map[string]map[string]bool // fqn -> callee fqns (outbound edges)
	in      map[string]map[string]bool // fqn -> caller fqns (inbound edges)
	isTest  map[string]bool            // fqn -> is a Test* function
	testNm  map[string]string          // fqn -> bare test name
	fixture string
}

type defSite struct {
	File string
	Line int
}

func main() {
	var fixture, targetsPath, out, explore string
	flag.StringVar(&fixture, "fixture", "bench/fixtures/repo", "fixture repo root")
	flag.StringVar(&targetsPath, "targets", "bench/tasks/truth/targets.json", "targets file")
	flag.StringVar(&out, "out", "bench/tasks/truth", "output dir for truth JSON")
	flag.StringVar(&explore, "explore", "", "dev: print pkg.symbols with inbound-caller count in [2,8]")
	flag.Parse()

	if explore != "" {
		m, err := loadModel(fixture)
		if err != nil {
			fmt.Fprintln(os.Stderr, "gen:", err)
			os.Exit(1)
		}
		m.explore(explore)
		return
	}

	if err := runGen(fixture, targetsPath, out); err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
}

// explore lists functions in package pkgName whose inbound caller count is in a
// tractable range, to help pick benchmark targets. Dev-only.
func (m *model) explore(pkgName string) {
	type row struct {
		fqn string
		n   int
	}
	var rows []row
	for fqn, callers := range m.in {
		if !strings.HasPrefix(fqn, pkgName+".") {
			continue
		}
		if n := len(callers); n >= 2 && n <= 8 {
			rows = append(rows, row{fqn, n})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].n < rows[j].n })
	for _, r := range rows {
		fmt.Printf("%3d  %s\n", r.n, r.fqn)
	}
}

func runGen(fixture, targetsPath, out string) error {
	var tg Targets
	raw, err := os.ReadFile(targetsPath)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &tg); err != nil {
		return err
	}
	if tg.BlastDepth == 0 {
		tg.BlastDepth = 2
	}

	m, err := loadModel(fixture)
	if err != nil {
		return err
	}

	// 01 find_def
	ds, ok := m.defs[tg.FindDef.fqn()]
	if !ok {
		return fmt.Errorf("find_def target %s not found", tg.FindDef.fqn())
	}
	write(out, "01_find_def.json", map[string]any{"file": ds.File, "line": ds.Line})
	fmt.Printf("01 find_def %s -> %s:%d\n", tg.FindDef.fqn(), ds.File, ds.Line)

	// 02 callers
	callers := m.callersOf(tg.Callers.fqn())
	write(out, "02_callers.json", map[string]any{"callers": callers})
	fmt.Printf("02 callers of %s -> %d\n", tg.Callers.fqn(), len(callers))

	// 03 callees
	callees := setToSorted(m.out[tg.Callees.fqn()])
	write(out, "03_callees.json", map[string]any{"callees": callees})
	fmt.Printf("03 callees of %s -> %d\n", tg.Callees.fqn(), len(callees))

	// 04 blast_radius (inbound BFS, depth <= BlastDepth)
	blast := m.inboundBFS(tg.BlastRadius.fqn(), tg.BlastDepth)
	write(out, "04_blast_radius.json", map[string]any{"symbols": blast})
	fmt.Printf("04 blast_radius of %s (depth %d) -> %d\n", tg.BlastRadius.fqn(), tg.BlastDepth, len(blast))

	// 05 covering_tests (test funcs that transitively reach the symbol)
	tests := m.coveringTests(tg.CoveringTests.fqn())
	write(out, "05_covering_tests.json", map[string]any{"tests": tests})
	fmt.Printf("05 covering_tests of %s -> %d\n", tg.CoveringTests.fqn(), len(tests))

	// 06 dead_code (alive = has at least one caller)
	dcCallers := m.callersOf(tg.DeadCode.fqn())
	alive := len(dcCallers) > 0
	write(out, "06_dead_code.json", map[string]any{"alive": alive})
	fmt.Printf("06 dead_code of %s -> alive=%v (callers=%d)\n", tg.DeadCode.fqn(), alive, len(dcCallers))

	// 07 call_path (emit the forward-reachable subgraph from `from`; any valid
	// path from `from` uses only these edges, so contains_path can validate any
	// answer while the committed file stays bounded).
	edges := m.forwardEdges(tg.CallPathFrom.fqn())
	reach := m.reachable(tg.CallPathFrom.fqn(), tg.CallPathTo.fqn())
	write(out, "07_call_path.json", map[string]any{
		"from":  tg.CallPathFrom.fqn(),
		"to":    tg.CallPathTo.fqn(),
		"edges": edges,
	})
	fmt.Printf("07 call_path %s -> %s reachable=%v (edges=%d)\n", tg.CallPathFrom.fqn(), tg.CallPathTo.fqn(), reach, len(edges))
	if !reach {
		fmt.Printf("   WARNING: no path %s -> %s; pick a connected pair\n", tg.CallPathFrom.fqn(), tg.CallPathTo.fqn())
	}

	// 08 file_outline (exported package-level funcs in the file; go/parser-level)
	outline, err := m.exportedFuncsInFile(tg.FileOutline)
	if err != nil {
		return err
	}
	write(out, "08_file_outline.json", map[string]any{"functions": outline})
	fmt.Printf("08 file_outline %s -> %d\n", tg.FileOutline, len(outline))

	// 09 rename_impact (number of distinct call SITES of the method)
	sites := m.callSiteCount(tg.RenameImpact.fqn())
	write(out, "09_rename_impact.json", map[string]any{"count": sites})
	fmt.Printf("09 rename_impact of %s -> %d call sites\n", tg.RenameImpact.fqn(), sites)

	// 10 callers_method
	cm := m.callersOf(tg.CallersMethod.fqn())
	write(out, "10_callers_method.json", map[string]any{"callers": cm})
	fmt.Printf("10 callers_method of %s -> %d\n", tg.CallersMethod.fqn(), len(cm))

	return nil
}

func loadModel(fixture string) (*model, error) {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
			packages.NeedTypes | packages.NeedTypesInfo | packages.NeedDeps |
			packages.NeedImports,
		Dir:   fixture,
		Tests: true,
		Env:   append(os.Environ(), "GOFLAGS=-mod=mod"),
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return nil, fmt.Errorf("packages.Load: %w", err)
	}
	if packages.PrintErrors(pkgs) > 0 {
		// Type errors in some packages are tolerable; we proceed on what loaded.
		fmt.Fprintln(os.Stderr, "gen: note: some fixture packages had load errors (proceeding)")
	}

	m := &model{
		fset:    token.NewFileSet(),
		defs:    map[string]defSite{},
		out:     map[string]map[string]bool{},
		in:      map[string]map[string]bool{},
		isTest:  map[string]bool{},
		testNm:  map[string]string{},
		fixture: fixture,
	}
	// Reuse each package's own FileSet for positions.
	seen := map[*packages.Package]bool{}
	for _, p := range pkgs {
		m.processPackage(p, seen)
	}
	return m, nil
}

func (m *model) processPackage(p *packages.Package, seen map[*packages.Package]bool) {
	if p == nil || seen[p] || p.Types == nil || p.TypesInfo == nil {
		return
	}
	seen[p] = true
	fset := p.Fset
	for _, file := range p.Syntax {
		ast.Inspect(file, func(n ast.Node) bool {
			fd, ok := n.(*ast.FuncDecl)
			if !ok {
				return true
			}
			obj := p.TypesInfo.Defs[fd.Name]
			fn, ok := obj.(*types.Func)
			if !ok || fn.Pkg() == nil {
				return true
			}
			callerFQN := funcFQN(fn)
			if callerFQN == "" {
				return true
			}
			// Record definition site (first wins; strip test-variant dupes).
			if _, exists := m.defs[callerFQN]; !exists {
				pos := fset.Position(fd.Name.Pos())
				m.defs[callerFQN] = defSite{File: m.rel(pos.Filename), Line: pos.Line}
			}
			if isTestFunc(fd, fn) {
				m.isTest[callerFQN] = true
				m.testNm[callerFQN] = fd.Name.Name
			}
			// Walk the body for calls.
			if fd.Body != nil {
				ast.Inspect(fd.Body, func(cn ast.Node) bool {
					call, ok := cn.(*ast.CallExpr)
					if !ok {
						return true
					}
					if calleeFQN := m.resolveCallee(p, call); calleeFQN != "" {
						m.addEdge(callerFQN, calleeFQN)
					}
					return true
				})
			}
			return true
		})
	}
}

// resolveCallee maps a call expression to the FQN of the invoked function, using
// type info (precise; handles method calls on interfaces/pointers).
func (m *model) resolveCallee(p *packages.Package, call *ast.CallExpr) string {
	var id *ast.Ident
	switch f := call.Fun.(type) {
	case *ast.Ident:
		id = f
	case *ast.SelectorExpr:
		id = f.Sel
	default:
		return ""
	}
	obj := p.TypesInfo.Uses[id]
	fn, ok := obj.(*types.Func)
	if !ok || fn.Pkg() == nil {
		return ""
	}
	return funcFQN(fn)
}

func (m *model) addEdge(from, to string) {
	if from == to {
		return
	}
	if m.out[from] == nil {
		m.out[from] = map[string]bool{}
	}
	m.out[from][to] = true
	if m.in[to] == nil {
		m.in[to] = map[string]bool{}
	}
	m.in[to][from] = true
}

func (m *model) callersOf(fqn string) []string { return setToSorted(m.in[fqn]) }

func (m *model) inboundBFS(fqn string, depth int) []string {
	visited := map[string]bool{fqn: true}
	frontier := []string{fqn}
	var result []string
	for d := 0; d < depth; d++ {
		var next []string
		for _, f := range frontier {
			for caller := range m.in[f] {
				if !visited[caller] {
					visited[caller] = true
					result = append(result, caller)
					next = append(next, caller)
				}
			}
		}
		frontier = next
	}
	sort.Strings(result)
	return result
}

func (m *model) coveringTests(fqn string) []string {
	// Inbound BFS (unbounded), collect Test* functions, emit bare names.
	visited := map[string]bool{fqn: true}
	frontier := []string{fqn}
	nameSet := map[string]bool{}
	for len(frontier) > 0 {
		var next []string
		for _, f := range frontier {
			for caller := range m.in[f] {
				if visited[caller] {
					continue
				}
				visited[caller] = true
				next = append(next, caller)
				if m.isTest[caller] {
					nameSet[m.testNm[caller]] = true
				}
			}
		}
		frontier = next
	}
	return setToSorted(nameSet)
}

func (m *model) reachable(from, to string) bool {
	visited := map[string]bool{from: true}
	stack := []string{from}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur == to {
			return true
		}
		for callee := range m.out[cur] {
			if !visited[callee] {
				visited[callee] = true
				stack = append(stack, callee)
			}
		}
	}
	return false
}

// forwardEdges returns every call edge inside the set of functions reachable
// from `from` (including `from`), sorted deterministically.
func (m *model) forwardEdges(from string) [][]string {
	reachable := map[string]bool{from: true}
	stack := []string{from}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for callee := range m.out[cur] {
			if !reachable[callee] {
				reachable[callee] = true
				stack = append(stack, callee)
			}
		}
	}
	var edges [][]string
	for f := range reachable {
		for to := range m.out[f] {
			if reachable[to] {
				edges = append(edges, []string{f, to})
			}
		}
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i][0] != edges[j][0] {
			return edges[i][0] < edges[j][0]
		}
		return edges[i][1] < edges[j][1]
	})
	return edges
}

// callSiteCount counts distinct caller functions invoking fqn (an approximation
// of "call sites" at function granularity — matches how the prompt is framed).
func (m *model) callSiteCount(fqn string) int { return len(m.in[fqn]) }

func (m *model) exportedFuncsInFile(relFile string) ([]string, error) {
	full := filepath.Join(m.fixture, relFile)
	var names []string
	for fqn, ds := range m.defs {
		if ds.File != relFile {
			continue
		}
		// package-level exported funcs only (no receiver, exported name).
		parts := strings.Split(fqn, ".")
		if len(parts) != 2 { // pkg.Name (methods are pkg.Recv.Name)
			continue
		}
		name := parts[1]
		if ast.IsExported(name) {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		// Confirm the file exists so an empty result is not a silent typo.
		if _, err := os.Stat(full); err != nil {
			return nil, fmt.Errorf("file_outline: %s: %w", relFile, err)
		}
	}
	sort.Strings(names)
	return names, nil
}

func (m *model) rel(abs string) string {
	fixAbs, _ := filepath.Abs(m.fixture)
	if r, err := filepath.Rel(fixAbs, abs); err == nil && !strings.HasPrefix(r, "..") {
		return filepath.ToSlash(r)
	}
	return filepath.ToSlash(abs)
}

// funcFQN builds pkg.Recv.Method or pkg.Func from a *types.Func.
func funcFQN(fn *types.Func) string {
	pkg := fn.Pkg()
	if pkg == nil {
		return ""
	}
	pkgName := pkg.Name()
	sig, ok := fn.Type().(*types.Signature)
	if !ok {
		return pkgName + "." + fn.Name()
	}
	if recv := sig.Recv(); recv != nil {
		return pkgName + "." + recvTypeName(recv.Type()) + "." + fn.Name()
	}
	return pkgName + "." + fn.Name()
}

func recvTypeName(t types.Type) string {
	switch v := t.(type) {
	case *types.Pointer:
		return recvTypeName(v.Elem())
	case *types.Named:
		return v.Obj().Name()
	default:
		return types.TypeString(t, func(*types.Package) string { return "" })
	}
}

// isTestFunc recognises both standard Go tests (func TestXxx(t *testing.T)) and
// gocheck-style suite test methods (func (s *Suite) TestXxx(c *check.C)), which
// go-git uses heavily. Either way the bare name starts with "Test".
func isTestFunc(fd *ast.FuncDecl, fn *types.Func) bool {
	if !strings.HasPrefix(fn.Name(), "Test") {
		return false
	}
	sig, ok := fn.Type().(*types.Signature)
	if !ok || sig.Params().Len() != 1 {
		return false
	}
	pt := types.TypeString(sig.Params().At(0).Type(), nil)
	// Standard test: *testing.T. Suite test method: single param (gocheck *C).
	return strings.Contains(pt, "testing.T") || fd.Recv != nil
}

func setToSorted(s map[string]bool) []string {
	out := make([]string, 0, len(s))
	for k := range s {
		out = append(out, k)
	}
	sort.Strings(out)
	if out == nil {
		out = []string{}
	}
	return out
}

func write(dir, name string, v map[string]any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	_ = os.WriteFile(filepath.Join(dir, name), append(b, '\n'), 0o644)
}
