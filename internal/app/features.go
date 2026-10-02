package app

import (
	"fmt"
	"sort"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/extract/featuresrc"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

const (
	DefaultFeaturesTop    = 200
	MaxFeaturesTop        = 2000
	DefaultFootprintDepth = 3
	MaxFootprintDepth     = 6

	featureFootprintNodeCap   = 400
	featureFootprintSubsystem = 6
	featureNotesCap           = 20
	// featureSpecificCallers is the most production callers a footprint symbol
	// may have and still count as this feature's own code for test coverage.
	featureSpecificCallers = 5
)

// FeaturesOptions bounds and filters the capability inventory. Zero values use
// the documented defaults.
type FeaturesOptions struct {
	Kinds       []string // filter by kind (empty = all)
	Query       string   // case-insensitive substring over label/description/handler fqn/file
	Top         int
	NoFootprint bool
	Depth       int
}

// FeatureRegistration is where the framework registration itself lives.
type FeatureRegistration struct {
	File string `json:"file"`
	Line int    `json:"line"`
}

// FeatureHandler is the graph node that implements a feature.
type FeatureHandler struct {
	Symbol    string          `json:"symbol"`
	FQN       string          `json:"fqn,omitempty"`
	Kind      string          `json:"kind"`
	File      string          `json:"file"`
	StartLine int             `json:"start_line"`
	Selector  *SymbolSelector `json:"selector,omitempty"`
}

// FeatureSubsystem is one source-path subsystem inside a footprint.
type FeatureSubsystem struct {
	Name    string `json:"name"`
	Symbols int    `json:"symbols"`
}

// FeatureFootprint is the bounded set of code a handler reaches through calls.
type FeatureFootprint struct {
	Symbols    int                `json:"symbols"`
	Files      int                `json:"files"`
	Depth      int                `json:"depth"`
	Truncated  bool               `json:"truncated"`
	Subsystems []FeatureSubsystem `json:"subsystems"`
	// Tests counts distinct tests calling the handler or a footprint symbol that
	// is a direct, feature-specific callee of it (≤ featureSpecificCallers
	// production callers) — not shared infrastructure or deep store helpers.
	Tests          int `json:"tests"`
	AmbiguousEdges int `json:"ambiguous_edges"`
}

// Feature is one user-facing capability and where it lives.
type Feature struct {
	ID                string              `json:"id"`
	Kind              string              `json:"kind"`
	Surface           string              `json:"surface"`
	Label             string              `json:"label"`
	Invocation        string              `json:"invocation,omitempty"`
	Description       string              `json:"description"`
	DescriptionSource string              `json:"description_source"`
	Framework         string              `json:"framework"`
	Detector          string              `json:"detector"`
	Confidence        string              `json:"confidence"`
	Hidden            bool                `json:"hidden,omitempty"`
	Registration      FeatureRegistration `json:"registration"`
	Handler           *FeatureHandler     `json:"handler"`
	Parent            string              `json:"parent,omitempty"`
	Footprint         *FeatureFootprint   `json:"footprint,omitempty"`
}

// FeaturesReport is a bounded capability inventory of an indexed project.
type FeaturesReport struct {
	SchemaVersion  int            `json:"schema_version"`
	Project        string         `json:"project"`
	Indexed        bool           `json:"indexed"`
	Features       []Feature      `json:"features"`
	FeaturesTotal  int            `json:"features_total"`
	Truncated      bool           `json:"truncated"`
	ByKind         map[string]int `json:"by_kind"`
	Frameworks     []string       `json:"frameworks"`
	FootprintDepth int            `json:"footprint_depth"`
	CallGraph      string         `json:"call_graph"`
	Resolution     string         `json:"resolution,omitempty"`
	Stale          bool           `json:"stale"`
	Notes          []string       `json:"notes"`
	PartialErrors  []string       `json:"partial_errors"`
}

func normalizeFeaturesOptions(opts FeaturesOptions) (FeaturesOptions, error) {
	if opts.Top == 0 {
		opts.Top = DefaultFeaturesTop
	}
	if opts.Top < 1 || opts.Top > MaxFeaturesTop {
		return opts, fmt.Errorf("features top must be between 1 and %d", MaxFeaturesTop)
	}
	if opts.Depth == 0 {
		opts.Depth = DefaultFootprintDepth
	}
	if opts.Depth < 1 || opts.Depth > MaxFootprintDepth {
		return opts, fmt.Errorf("features depth must be between 1 and %d", MaxFootprintDepth)
	}
	var kinds []string
	for _, raw := range opts.Kinds {
		for _, k := range strings.Split(raw, ",") {
			k = strings.ToLower(strings.TrimSpace(k))
			if k == "" {
				continue
			}
			if featuresrc.KindRank(k) >= len(featuresrc.KindOrder) {
				return opts, fmt.Errorf("unknown feature kind %q (valid: %s)", k, strings.Join(featuresrc.KindOrder, ", "))
			}
			kinds = append(kinds, k)
		}
	}
	opts.Kinds = kinds
	opts.Query = strings.TrimSpace(opts.Query)
	return opts, nil
}

func newFeaturesReport(name string, indexed bool, depth int) *FeaturesReport {
	return &FeaturesReport{
		SchemaVersion: 1, Project: name, Indexed: indexed,
		Features: []Feature{}, ByKind: map[string]int{}, Frameworks: []string{},
		FootprintDepth: depth, CallGraph: CallGraphNone,
		Notes: []string{}, PartialErrors: []string{},
	}
}

// Features returns the capability inventory of an indexed project: every
// user-facing entry surface (CLI command, HTTP route, RPC/MCP tool, UI page,
// program), its handler symbol, its registration description and a bounded
// call footprint. Everything is read from the stored graph plus a bounded read
// of the indexed source files; nothing is reindexed.
func (svc *Service) Features(cwd string, opts FeaturesOptions) (*FeaturesReport, error) {
	opts, err := normalizeFeaturesOptions(opts)
	if err != nil {
		return nil, err
	}
	pid, name, found, err := svc.project(cwd)
	if err != nil {
		return nil, err
	}
	rep := newFeaturesReport(name, found, opts.Depth)
	if !found {
		return rep, nil
	}
	g, err := svc.s.Graph()
	if err != nil {
		return nil, err
	}
	proj, err := g.GetProjectByName(name)
	if err != nil {
		return nil, err
	}
	nodes, err := g.ProjectNodes(pid)
	if err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		rep.Indexed = false
		return rep, nil
	}

	scan := newFeatureScan(proj.Path, name, nodes)
	feats := scan.run()
	rep.Notes = append(rep.Notes, scan.notes...)
	rep.PartialErrors = append(rep.PartialErrors, capFeatureErrors(scan.errs)...)

	feats = filterFeatures(feats, opts)
	sortFeatures(feats)
	dedupeFeatureIDs(feats)

	rep.FeaturesTotal = len(feats)
	fw := map[string]bool{}
	for _, f := range feats {
		rep.ByKind[f.Kind]++
		if f.Framework != "" {
			fw[f.Framework] = true
		}
	}
	for k := range fw {
		rep.Frameworks = append(rep.Frameworks, k)
	}
	sort.Strings(rep.Frameworks)
	if len(feats) > opts.Top {
		feats = feats[:opts.Top]
		rep.Truncated = true
	}

	callNodes := map[int64]graph.Node{}
	if !opts.NoFootprint {
		edges, eErr := g.ProjectEdges(pid)
		if eErr != nil {
			rep.PartialErrors = append(rep.PartialErrors, "edges: "+eErr.Error())
		} else {
			fp := newFootprintGraph(nodes, edges)
			for i := range feats {
				seeds := scan.seedsOf(feats[i].handlerNodeID)
				if len(seeds) == 0 {
					continue
				}
				var visited []int64
				feats[i].Footprint, visited = fp.walk(seeds, opts.Depth)
				for _, id := range visited {
					callNodes[id] = fp.byID[id]
				}
			}
		}
	}
	for i := range feats {
		if h := scan.nodeByID[feats[i].handlerNodeID]; h != nil {
			callNodes[h.ID] = *h
		}
		rep.Features = append(rep.Features, feats[i].Feature)
	}

	cn := make([]graph.Node, 0, len(callNodes))
	for _, n := range callNodes {
		cn = append(cn, n)
	}
	rep.CallGraph = svc.callGraphStatus(g, pid, callableNodes(cn))
	rep.Resolution = featuresResolution(rep.CallGraph, cn, opts.NoFootprint)

	stale, sErr := svc.Staleness(cwd)
	if sErr != nil {
		rep.PartialErrors = append(rep.PartialErrors, fmt.Sprintf("staleness: %v", sErr))
	} else if stale != nil {
		rep.Stale = stale.Any()
	}
	return rep, nil
}

// capFeatureErrors bounds the per-file read/parse error list; an index whose
// files were deleted since indexing would otherwise report one error per file.
func capFeatureErrors(errs []string) []string {
	if len(errs) <= featureNotesCap {
		return errs
	}
	out := append([]string(nil), errs[:featureNotesCap]...)
	return append(out, fmt.Sprintf("%d further read errors suppressed (index may be stale — reindex)", len(errs)-featureNotesCap))
}

func featuresResolution(callGraph string, nodes []graph.Node, noFootprint bool) string {
	switch callGraph {
	case CallGraphNone:
		if len(nodes) == 0 {
			return ""
		}
		return "no call graph for the handlers' languages; footprints are unavailable"
	case CallGraphUnresolved:
		lang := ""
		for _, n := range nodes {
			if noNameBasedCallLang(n.Language) {
				lang = n.Language
				break
			}
		}
		head, _ := callGraphGap(lang)
		return head + " — footprints are incomplete; run 'codemap index --precise'"
	case CallGraphName:
		if noFootprint {
			return ""
		}
		return "name-based call graph: footprints follow name-matched calls; same-name callees that cannot be disambiguated by directory or subsystem are skipped (see ambiguous_edges)"
	}
	return ""
}

// scoredFeature carries the resolved handler node id next to the public
// Feature so footprints can be computed without re-resolving.
type scoredFeature struct {
	Feature
	handlerNodeID int64
}

func filterFeatures(in []scoredFeature, opts FeaturesOptions) []scoredFeature {
	kinds := map[string]bool{}
	for _, k := range opts.Kinds {
		kinds[k] = true
	}
	q := strings.ToLower(opts.Query)
	out := in[:0:0]
	for _, f := range in {
		if len(kinds) > 0 && !kinds[f.Kind] {
			continue
		}
		if q != "" {
			hay := strings.ToLower(f.Label + "\n" + f.Description + "\n" + f.Registration.File)
			if f.Handler != nil {
				hay += "\n" + strings.ToLower(f.Handler.FQN+"\n"+f.Handler.Symbol+"\n"+f.Handler.File)
			}
			if !strings.Contains(hay, q) {
				continue
			}
		}
		out = append(out, f)
	}
	return out
}

func sortFeatures(fs []scoredFeature) {
	sort.SliceStable(fs, func(i, j int) bool {
		a, b := fs[i], fs[j]
		if ra, rb := featuresrc.KindRank(a.Kind), featuresrc.KindRank(b.Kind); ra != rb {
			return ra < rb
		}
		if a.Label != b.Label {
			return a.Label < b.Label
		}
		if a.Registration.File != b.Registration.File {
			return a.Registration.File < b.Registration.File
		}
		return a.Registration.Line < b.Registration.Line
	})
}

func dedupeFeatureIDs(fs []scoredFeature) {
	seen := map[string]int{}
	for i := range fs {
		base := fs[i].Kind + ":" + fs[i].Label
		seen[base]++
		if n := seen[base]; n > 1 {
			fs[i].ID = fmt.Sprintf("%s#%d", base, n)
		} else {
			fs[i].ID = base
		}
	}
}
