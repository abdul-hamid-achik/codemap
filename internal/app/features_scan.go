package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/extract/featuresrc"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

const (
	featureMaxSourceBytes = 1 << 20 // detectors parse whole files, but never huge ones
	featureMaxPackageDirs = 500
)

// featureScan reads the indexed source files of one project, runs the
// registration detectors and resolves their handlers onto graph nodes.
type featureScan struct {
	root, project string
	nodes         []graph.Node
	nodeByID      map[int64]*graph.Node
	byFile        map[string][]*graph.Node
	bySymbol      map[string][]*graph.Node
	files         []string // indexed, non-test, non-ignored source files (sorted)
	languages     map[string]int
	typeSeeds     map[int64][]int64
	nextCache     map[string]bool

	notes     []string
	extraNote int

	ambiguous   int
	ambiguousEx []string
	errs        []string
}

func newFeatureScan(root, project string, nodes []graph.Node) *featureScan {
	s := &featureScan{
		root: root, project: project, nodes: nodes,
		nodeByID:  make(map[int64]*graph.Node, len(nodes)),
		byFile:    map[string][]*graph.Node{},
		bySymbol:  map[string][]*graph.Node{},
		languages: map[string]int{},
		typeSeeds: map[int64][]int64{},
		nextCache: map[string]bool{},
		notes:     []string{}, errs: []string{},
	}
	seenFile := map[string]bool{}
	for i := range nodes {
		n := &nodes[i]
		s.nodeByID[n.ID] = n
		n.FilePath = filepath.ToSlash(n.FilePath)
		if n.Kind == graph.KindFile {
			if !seenFile[n.FilePath] {
				seenFile[n.FilePath] = true
				s.files = append(s.files, n.FilePath)
				if n.Language != "" {
					s.languages[n.Language]++
				}
			}
			continue
		}
		s.byFile[n.FilePath] = append(s.byFile[n.FilePath], n)
		if isFeatureTestNode(n) {
			continue
		}
		s.bySymbol[n.Symbol] = append(s.bySymbol[n.Symbol], n)
	}
	sort.Strings(s.files)
	return s
}

func (s *featureScan) note(msg string) {
	if len(s.notes) < featureNotesCap {
		s.notes = append(s.notes, msg)
		return
	}
	s.extraNote++
}

func (s *featureScan) readSource(rel string) ([]byte, bool) {
	f, err := os.Open(filepath.Join(s.root, filepath.FromSlash(rel)))
	if err != nil {
		s.errs = append(s.errs, fmt.Sprintf("%s: %v", rel, err))
		return nil, false
	}
	defer func() { _ = f.Close() }()
	if st, err := f.Stat(); err == nil && st.Size() > featureMaxSourceBytes {
		s.errs = append(s.errs, fmt.Sprintf("%s: skipped, larger than %d bytes", rel, featureMaxSourceBytes))
		return nil, false
	}
	b, err := io.ReadAll(io.LimitReader(f, featureMaxSourceBytes+1))
	if err != nil {
		s.errs = append(s.errs, fmt.Sprintf("%s: %v", rel, err))
		return nil, false
	}
	head := b
	if len(head) > 8000 {
		head = head[:8000]
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return nil, false // binary
	}
	return b, true
}

func (s *featureScan) run() []scoredFeature {
	var regs []featuresrc.Registration
	goDirs := map[string][]string{}
	var jsFiles, pyFiles []string
	jsDirs := map[string]bool{".": true}
	for _, p := range s.files {
		if isFeatureExcludedPath(p) {
			continue
		}
		switch strings.ToLower(path.Ext(p)) {
		case ".go":
			goDirs[path.Dir(p)] = append(goDirs[path.Dir(p)], p)
		case ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".mts", ".cts":
			jsFiles = append(jsFiles, p)
			for d := path.Dir(p); ; d = path.Dir(d) {
				jsDirs[d] = true
				if d == "." || d == "/" || d == "" {
					break
				}
			}
		case ".py":
			pyFiles = append(pyFiles, p)
		}
	}

	rootName := goModuleBase(s.root)
	if rootName == "" {
		rootName = s.project
	}
	dirs := make([]string, 0, len(goDirs))
	for d := range goDirs {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	for _, d := range dirs {
		var srcs []featuresrc.SourceFile
		for _, p := range goDirs[d] {
			if b, ok := s.readSource(p); ok {
				srcs = append(srcs, featuresrc.SourceFile{Path: p, Src: b})
			}
		}
		if len(srcs) == 0 {
			continue
		}
		r, errs := featuresrc.DetectGo(srcs, featuresrc.GoOptions{RootName: rootName})
		regs = append(regs, r...)
		s.errs = append(s.errs, errs...)
	}
	for _, p := range jsFiles {
		b, ok := s.readSource(p)
		if !ok {
			continue
		}
		regs = append(regs, featuresrc.DetectPattern(p, b)...)
		if s.isNextPackage(path.Dir(p)) {
			regs = append(regs, featuresrc.DetectNextPath(p, b)...)
		}
	}
	for _, p := range pyFiles {
		if b, ok := s.readSource(p); ok {
			regs = append(regs, featuresrc.DetectPattern(p, b)...)
		}
	}
	pkgDirs := make([]string, 0, len(jsDirs))
	for d := range jsDirs {
		if !featuresrc.IsIgnoredPath(d) {
			pkgDirs = append(pkgDirs, d)
		}
	}
	sort.Strings(pkgDirs)
	if len(pkgDirs) > featureMaxPackageDirs {
		s.note(fmt.Sprintf("package.json bin scan limited to %d directories", featureMaxPackageDirs))
		pkgDirs = pkgDirs[:featureMaxPackageDirs]
	}
	for _, d := range pkgDirs {
		rel := path.Join(d, "package.json")
		if _, err := os.Stat(filepath.Join(s.root, filepath.FromSlash(rel))); err != nil {
			continue
		}
		if b, ok := s.readSource(rel); ok {
			regs = append(regs, featuresrc.DetectPackageBin(rel, b)...)
		}
	}

	unsupported := []string{}
	for lang, n := range s.languages {
		switch lang {
		case "ruby", "lua", "gdscript":
			unsupported = append(unsupported, fmt.Sprintf("feature detection not implemented for %s (%d files)", lang, n))
		}
	}
	sort.Strings(unsupported)
	for _, u := range unsupported {
		s.note(u)
	}

	featuresrc.SortRegistrations(regs)
	out := make([]scoredFeature, 0, len(regs))
	for _, r := range regs {
		out = append(out, s.toFeature(r))
	}
	for _, r := range regs {
		if r.Framework == "express" {
			s.note("express routes are listed with router-relative paths; router.use mount prefixes are not resolved")
			break
		}
	}
	if s.ambiguous > 0 {
		s.note(fmt.Sprintf("%d handler references were ambiguous (several candidates in the same tier) and left unresolved, e.g. %s", s.ambiguous, strings.Join(s.ambiguousEx, ", ")))
	}
	if s.extraNote > 0 {
		s.notes = append(s.notes, fmt.Sprintf("%d further notes suppressed", s.extraNote))
	}
	return out
}

// goModuleBase returns the last element of the root go.mod module path.
func goModuleBase(root string) string {
	b, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return ""
	}
	if len(b) > 16<<10 {
		b = b[:16<<10]
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			mod := strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "module ")), `"`)
			parts := strings.Split(mod, "/")
			last := parts[len(parts)-1]
			if len(parts) > 1 && len(last) > 1 && last[0] == 'v' && strings.Trim(last[1:], "0123456789") == "" {
				last = parts[len(parts)-2]
			}
			return last
		}
	}
	return ""
}

// isNextPackage reports whether the nearest package.json at or above dir
// declares a dependency on next.
func (s *featureScan) isNextPackage(dir string) bool {
	if v, ok := s.nextCache[dir]; ok {
		return v
	}
	result := false
	for d := dir; ; d = path.Dir(d) {
		b, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(path.Join(d, "package.json"))))
		if err == nil {
			var pkg struct {
				Deps    map[string]json.RawMessage `json:"dependencies"`
				DevDeps map[string]json.RawMessage `json:"devDependencies"`
				Peer    map[string]json.RawMessage `json:"peerDependencies"`
			}
			if json.Unmarshal(b, &pkg) == nil {
				_, a := pkg.Deps["next"]
				_, b2 := pkg.DevDeps["next"]
				_, c := pkg.Peer["next"]
				result = a || b2 || c
			}
			break
		}
		if d == "." || d == "/" || d == "" {
			break
		}
	}
	s.nextCache[dir] = result
	return result
}

func isFeatureTestNode(n *graph.Node) bool {
	return n.Kind == graph.KindTest || isFeatureExcludedPath(n.FilePath)
}

// isFeatureExcludedPath reports paths that never hold product features: test
// code (graph.IsTestPath) and dependency, build-output or fixture trees.
func isFeatureExcludedPath(p string) bool {
	return graph.IsTestPath(p) || featuresrc.IsIgnoredPath(p)
}

func (s *featureScan) toFeature(r featuresrc.Registration) scoredFeature {
	f := scoredFeature{Feature: Feature{
		Kind: r.Kind, Surface: featuresrc.Surface(r.Kind), Label: r.Label, Invocation: r.Invocation,
		Description: r.Description, DescriptionSource: r.DescSource, Framework: r.Framework,
		Detector: r.Detector, Confidence: r.Confidence, Hidden: r.Hidden,
		Registration: FeatureRegistration{File: r.File, Line: r.Line},
	}}
	if r.Parent != "" {
		f.Parent = featuresrc.KindCLICommand + ":" + r.Parent
	}
	if r.Handler != nil {
		if n := s.resolveHandler(r); n != nil {
			f.handlerNodeID = n.ID
			f.Handler = &FeatureHandler{
				Symbol: n.Symbol, FQN: n.FQN, Kind: n.Kind, File: n.FilePath, StartLine: n.StartLine,
				Selector: selectorForNode(*n),
			}
			if f.Description == "" {
				if doc := summarizeDoc(n.Docstring); doc != "" {
					f.Description, f.DescriptionSource = doc, featuresrc.DescDocstring
				}
			}
			if r.IsTypeHandler() {
				s.typeSeeds[n.ID] = s.methodsOf(n)
			}
		}
	}
	if r.ConfirmedIfHandler && f.Handler == nil {
		f.Confidence = featuresrc.Candidate
	}
	return f
}

func summarizeDoc(doc string) string {
	for _, line := range strings.Split(strings.TrimSpace(doc), "\n") {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "/*#! "))
		if line != "" {
			r := []rune(line)
			if len(r) > 240 {
				return string(r[:240]) + "…"
			}
			return line
		}
	}
	return ""
}

func (s *featureScan) methodsOf(t *graph.Node) []int64 {
	prefix := t.FQN + "."
	var ids []int64
	for _, n := range s.byFile[t.FilePath] {
		if n.Kind == graph.KindMethod && strings.HasPrefix(n.FQN, prefix) {
			ids = append(ids, n.ID)
		}
	}
	if len(ids) == 0 { // methods may live in sibling files of the same package
		dir := path.Dir(t.FilePath)
		for _, n := range s.bySymbolMethods(prefix) {
			if path.Dir(n.FilePath) == dir {
				ids = append(ids, n.ID)
			}
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func (s *featureScan) bySymbolMethods(fqnPrefix string) []*graph.Node {
	var out []*graph.Node
	for _, group := range s.bySymbol {
		for _, n := range group {
			if n.Kind == graph.KindMethod && strings.HasPrefix(n.FQN, fqnPrefix) {
				out = append(out, n)
			}
		}
	}
	return out
}

// seedsOf returns the footprint seed node ids for a resolved handler.
func (s *featureScan) seedsOf(id int64) []int64 {
	if id == 0 {
		return nil
	}
	if seeds, ok := s.typeSeeds[id]; ok {
		return seeds
	}
	return []int64{id}
}

func handlerKindOK(r featuresrc.Registration, n *graph.Node) bool {
	if r.IsTypeHandler() {
		return n.Kind == graph.KindType || n.Kind == graph.KindClass
	}
	return n.Kind == graph.KindFunction || n.Kind == graph.KindMethod
}

// resolveHandler maps a syntactic handler reference to exactly one graph node:
// same file first, then same directory (package), then project-unique. An
// ambiguous tier resolves to nothing — handlers are never guessed across
// packages.
func (s *featureScan) resolveHandler(r featuresrc.Registration) *graph.Node {
	ref := r.Handler
	var cands []*graph.Node
	for _, n := range s.bySymbol[ref.Name] {
		if handlerKindOK(r, n) {
			cands = append(cands, n)
		}
	}
	if len(cands) == 0 {
		return nil
	}
	if ref.Qualifier != "" {
		var q []*graph.Node
		for _, n := range cands {
			if n.FQN == ref.Qualifier+"."+ref.Name {
				q = append(q, n)
			}
		}
		if len(q) == 0 {
			return nil
		}
		cands = q
	}
	if ref.Recv != "" {
		var q []*graph.Node
		for _, n := range cands {
			if strings.HasSuffix(n.FQN, "."+ref.Recv+"."+ref.Name) || n.FQN == ref.Recv+"."+ref.Name {
				q = append(q, n)
			}
		}
		if len(q) > 0 {
			cands = q
		}
	}
	file := ref.File
	if file == "" {
		file = r.File
	}
	dir := path.Dir(file)
	tiers := [][]*graph.Node{{}, {}, cands}
	for _, n := range cands {
		if n.FilePath == file {
			tiers[0] = append(tiers[0], n)
		}
		if path.Dir(n.FilePath) == dir {
			tiers[1] = append(tiers[1], n)
		}
	}
	if ref.SameFileOnly {
		tiers = tiers[:1]
	}
	for _, tier := range tiers {
		switch len(tier) {
		case 0:
			continue
		case 1:
			return tier[0]
		default:
			s.ambiguous++
			if len(s.ambiguousEx) < 3 {
				s.ambiguousEx = append(s.ambiguousEx, fmt.Sprintf("%s %q -> %s (%d candidates)", r.Kind, r.Label, ref.Name, len(tier)))
			}
			return nil
		}
	}
	return nil
}
