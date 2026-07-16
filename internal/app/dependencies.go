package app

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

const (
	DependencyCoverageComplete    = "complete"
	DependencyCoveragePartial     = "partial"
	DependencyCoverageUnavailable = "unavailable"

	DependencyTargetFile    = "file"
	DependencyTargetPackage = "package"

	DependencyConfidenceConfirmed = "confirmed"
	DependencyConfidenceCandidate = "candidate"

	DependencyReasonPrecise      = "precise"
	DependencyReasonSamePackage  = "same_package"
	DependencyReasonNameFanout   = "name_fanout"
	DependencyReasonPackageScope = "package_scope"
	DependencyReasonStale        = "stale_snapshot"

	dependencyFileCap         = 25
	dependencySampleCap       = 3
	dependencyGlobalSampleCap = 50
)

// DependencyLocation is one source or target endpoint in a dependency sample.
// It deliberately excludes graph row IDs: file/symbol/FQN/line are stable and
// directly actionable by people and agents.
type DependencyLocation struct {
	File      string `json:"file"`
	Symbol    string `json:"symbol,omitempty"`
	FQN       string `json:"fqn,omitempty"`
	Kind      string `json:"kind"`
	Language  string `json:"language"`
	StartLine int    `json:"start_line,omitempty"`
}

// DependencySample is one bounded source→target example for an evidence group.
// TargetScope distinguishes file-specific evidence from Go's package-level
// import edge, which targets one representative file but cannot prove that exact
// file is required.
type DependencySample struct {
	Source           DependencyLocation `json:"source"`
	Target           DependencyLocation `json:"target"`
	TargetScope      string             `json:"target_scope"` // file|package
	Provenance       string             `json:"provenance"`
	Weight           float64            `json:"weight"`
	Confidence       string             `json:"confidence"`        // confirmed|candidate
	ConfidenceReason string             `json:"confidence_reason"` // precise|same_package|name_fanout|package_scope|stale_snapshot
}

// DependencyKindEvidence groups one dependent file's logical relationships by
// edge kind. ConfirmedTotal+CandidateTotal always equals Total; Samples is capped.
type DependencyKindEvidence struct {
	Kind               string             `json:"kind"` // calls|references|imports
	Total              int                `json:"total"`
	ConfirmedTotal     int                `json:"confirmed_total"`
	CandidateTotal     int                `json:"candidate_total"`
	FileScopedTotal    int                `json:"file_scoped_total"`
	PackageScopedTotal int                `json:"package_scoped_total"`
	Samples            []DependencySample `json:"samples"`
	SamplesTruncated   int                `json:"samples_truncated,omitempty"`
}

// DependentFileEvidence aggregates every inbound evidence kind from one file.
// Its confidence totals partition EvidenceTotal even when samples are capped.
type DependentFileEvidence struct {
	File               string                   `json:"file"`
	EvidenceTotal      int                      `json:"evidence_total"`
	ConfirmedTotal     int                      `json:"confirmed_total"`
	CandidateTotal     int                      `json:"candidate_total"`
	FileScopedTotal    int                      `json:"file_scoped_total"`
	PackageScopedTotal int                      `json:"package_scoped_total"`
	Kinds              []DependencyKindEvidence `json:"kinds"`
}

// DependencyDomainCoverage states how completely one relevant dependency
// domain is modeled. Status is complete|partial|unavailable; a complete query of
// stored rows does not upgrade a backend that only extracts part of the domain.
type DependencyDomainCoverage struct {
	Domain string `json:"domain"`
	Status string `json:"status"`
	Scope  string `json:"scope"`
	Note   string `json:"note"`
}

// FileDependencyCoverage is complete only when every deletion-relevant domain
// is complete. Current runtime/external/reference coverage intentionally keeps
// this false, so missing evidence is never promoted into a safe-delete claim.
type FileDependencyCoverage struct {
	Complete bool                       `json:"complete"`
	Domains  []DependencyDomainCoverage `json:"domains"`
}

// FileDependenciesReport is the reusable public dependency-evidence report for
// file safety and refactor workflows. Dependents and per-kind samples are capped
// for agent token efficiency; totals/truncation make omitted evidence explicit.
type FileDependenciesReport struct {
	Project                    string                  `json:"project"`
	File                       string                  `json:"file"`
	Indexed                    bool                    `json:"indexed"`
	Found                      bool                    `json:"found"`
	Stale                      bool                    `json:"stale,omitempty"`
	CallGraph                  string                  `json:"call_graph"`
	EvidenceTotal              int                     `json:"evidence_total"`
	ConfirmedTotal             int                     `json:"confirmed_total"`
	CandidateTotal             int                     `json:"candidate_total"`
	SamplesTotal               int                     `json:"samples_total"`
	SamplesTruncated           int                     `json:"samples_truncated,omitempty"`
	FileScopedEvidenceTotal    int                     `json:"file_scoped_evidence_total"`
	ConfirmedFileScopedTotal   int                     `json:"confirmed_file_scoped_total"`
	CandidateFileScopedTotal   int                     `json:"candidate_file_scoped_total"`
	PackageScopedEvidenceTotal int                     `json:"package_scoped_evidence_total"`
	DependentsTotal            int                     `json:"dependents_total"`
	DependentsTruncated        int                     `json:"dependents_truncated,omitempty"`
	Dependents                 []DependentFileEvidence `json:"dependents"`
	Coverage                   FileDependencyCoverage  `json:"coverage"`
	Note                       string                  `json:"note,omitempty"`

	allDependentFiles        []string
	fileScopedKinds          map[string]bool
	confirmedFileScopedKinds map[string]bool
	candidateFileScopedKinds map[string]bool
	confirmedCallTargets     map[string]bool
	candidateCallTargets     map[string]bool
}

// Dependencies returns direct inbound call/reference/import evidence for file,
// grouped by dependent file and edge kind, plus explicit extraction coverage.
// Confirmed file-scoped evidence can prove a dependency; candidate evidence is a
// prompt to inspect or reindex precisely. Absence is never proof of safety while
// any relevant domain remains partial or unavailable.
func (svc *Service) Dependencies(cwd, file string) (*FileDependenciesReport, error) {
	pid, name, indexed, err := svc.project(cwd)
	if err != nil {
		return nil, err
	}
	rep := &FileDependenciesReport{
		Project: name, File: file, Indexed: indexed, CallGraph: CallGraphNone,
		Dependents: []DependentFileEvidence{},
		Coverage:   FileDependencyCoverage{Domains: []DependencyDomainCoverage{}},
	}
	if !indexed {
		return rep, nil
	}
	g, err := svc.s.Graph()
	if err != nil {
		return nil, err
	}
	project, err := g.GetProjectByID(pid)
	if err != nil {
		return nil, err
	}
	rep.File = projectRel(project.Path, cwd, file)
	targetNodes, err := g.NodesInFile(pid, rep.File)
	if err != nil {
		return nil, err
	}
	rep.Found = len(targetNodes) > 0

	projectNodes, err := g.ProjectNodes(pid)
	if err != nil {
		return nil, err
	}
	rep.CallGraph = svc.callGraphStatus(g, pid, callableNodes(projectNodes))
	rep.Coverage = dependencyCoverage(rep.CallGraph, projectNodes)
	if st, stErr := svc.Staleness(cwd); stErr == nil && st != nil {
		rep.Stale = st.Any()
	}
	if !rep.Found {
		rep.Note = "file has no indexed nodes; dependency absence cannot be evaluated"
		return rep, nil
	}

	edges, err := g.InboundFileDependencies(pid, rep.File)
	if err != nil {
		return nil, err
	}
	rep.groupDependencyEdges(edges, rep.Stale)
	switch {
	case rep.Stale && rep.EvidenceTotal > 0:
		rep.Note = "dependency evidence comes from a stale index snapshot and is candidate-only until reindexing confirms it"
	case rep.CandidateFileScopedTotal > 0:
		rep.Note = "some inbound file relationships are name-based candidates, not confirmed dependencies — reindex with --precise before treating them as exact"
	case !rep.Coverage.Complete:
		rep.Note = "dependency coverage is incomplete — positive evidence is actionable, but missing evidence does not prove this file is independent"
	}
	if !rep.Coverage.Complete && rep.Note != "" && !strings.Contains(rep.Note, "missing evidence") {
		rep.Note = joinNote(rep.Note, "missing evidence still does not prove this file is independent because dependency coverage is incomplete")
	}
	if rep.DependentsTruncated > 0 {
		rep.Note = joinNote(rep.Note, fmt.Sprintf("%d dependent file(s) omitted from the bounded response", rep.DependentsTruncated))
	}
	return rep, nil
}

type classifiedDependencyEdge struct {
	edge       graph.FileDependencyEdge
	scope      string
	confidence string
	reason     string
}

type dependencyKindGroup struct {
	edges []classifiedDependencyEdge
}

func (rep *FileDependenciesReport) groupDependencyEdges(edges []graph.FileDependencyEdge, stale bool) {
	grouped := map[string]map[string]*dependencyKindGroup{}
	rep.EvidenceTotal = len(edges)
	rep.fileScopedKinds = map[string]bool{}
	rep.confirmedFileScopedKinds = map[string]bool{}
	rep.candidateFileScopedKinds = map[string]bool{}
	rep.confirmedCallTargets = map[string]bool{}
	rep.candidateCallTargets = map[string]bool{}
	for _, edge := range edges {
		scope := dependencyTargetScope(edge)
		confidence, reason := dependencyConfidence(edge, scope, stale)
		classified := classifiedDependencyEdge{edge: edge, scope: scope, confidence: confidence, reason: reason}
		if confidence == DependencyConfidenceConfirmed {
			rep.ConfirmedTotal++
		} else {
			rep.CandidateTotal++
		}
		if edge.EdgeType == graph.EdgeCalls {
			targetKey := symKey(edge.Target.FQN, edge.Target.File, edge.Target.StartLine)
			if confidence == DependencyConfidenceConfirmed {
				rep.confirmedCallTargets[targetKey] = true
			} else {
				rep.candidateCallTargets[targetKey] = true
			}
		}
		if scope == DependencyTargetFile {
			rep.FileScopedEvidenceTotal++
			rep.fileScopedKinds[edge.EdgeType] = true
			if confidence == DependencyConfidenceConfirmed {
				rep.ConfirmedFileScopedTotal++
				rep.confirmedFileScopedKinds[edge.EdgeType] = true
			} else {
				rep.CandidateFileScopedTotal++
				rep.candidateFileScopedKinds[edge.EdgeType] = true
			}
		} else {
			rep.PackageScopedEvidenceTotal++
		}
		byKind := grouped[edge.Source.File]
		if byKind == nil {
			byKind = map[string]*dependencyKindGroup{}
			grouped[edge.Source.File] = byKind
		}
		kg := byKind[edge.EdgeType]
		if kg == nil {
			kg = &dependencyKindGroup{}
			byKind[edge.EdgeType] = kg
		}
		kg.edges = append(kg.edges, classified)
	}

	files := make([]string, 0, len(grouped))
	for file := range grouped {
		files = append(files, file)
	}
	sort.Slice(files, func(i, j int) bool {
		iConfirmed := dependencyGroupsHaveConfirmed(grouped[files[i]])
		jConfirmed := dependencyGroupsHaveConfirmed(grouped[files[j]])
		if iConfirmed != jConfirmed {
			return iConfirmed
		}
		return files[i] < files[j]
	})
	rep.allDependentFiles = append([]string(nil), files...)
	rep.DependentsTotal = len(files)
	if len(files) > dependencyFileCap {
		rep.DependentsTruncated = len(files) - dependencyFileCap
		files = files[:dependencyFileCap]
	}

	sampleBudget := dependencyGlobalSampleCap
	for _, file := range files {
		dep := DependentFileEvidence{File: file, Kinds: []DependencyKindEvidence{}}
		kinds := make([]string, 0, len(grouped[file]))
		for kind := range grouped[file] {
			kinds = append(kinds, kind)
		}
		sort.Slice(kinds, func(i, j int) bool {
			iConfirmed := dependencyEdgesHaveConfirmed(grouped[file][kinds[i]].edges)
			jConfirmed := dependencyEdgesHaveConfirmed(grouped[file][kinds[j]].edges)
			if iConfirmed != jConfirmed {
				return iConfirmed
			}
			return kinds[i] < kinds[j]
		})
		for _, kind := range kinds {
			edges := grouped[file][kind].edges
			sort.SliceStable(edges, func(i, j int) bool {
				return edges[i].confidence == DependencyConfidenceConfirmed && edges[j].confidence != DependencyConfidenceConfirmed
			})
			kg := DependencyKindEvidence{Kind: kind, Total: len(edges), Samples: []DependencySample{}}
			for i, classified := range edges {
				if classified.confidence == DependencyConfidenceConfirmed {
					kg.ConfirmedTotal++
					dep.ConfirmedTotal++
				} else {
					kg.CandidateTotal++
					dep.CandidateTotal++
				}
				if classified.scope == DependencyTargetFile {
					kg.FileScopedTotal++
					dep.FileScopedTotal++
				} else {
					kg.PackageScopedTotal++
					dep.PackageScopedTotal++
				}
				if i < dependencySampleCap && sampleBudget > 0 {
					kg.Samples = append(kg.Samples, dependencySample(classified))
					sampleBudget--
					rep.SamplesTotal++
				}
			}
			kg.SamplesTruncated = len(edges) - len(kg.Samples)
			dep.EvidenceTotal += len(edges)
			dep.Kinds = append(dep.Kinds, kg)
		}
		rep.Dependents = append(rep.Dependents, dep)
	}
	rep.SamplesTruncated = rep.EvidenceTotal - rep.SamplesTotal
}

func dependencySample(classified classifiedDependencyEdge) DependencySample {
	edge := classified.edge
	return DependencySample{
		Source:           dependencyLocation(edge.Source),
		Target:           dependencyLocation(edge.Target),
		TargetScope:      classified.scope,
		Provenance:       edge.Provenance,
		Weight:           edge.Weight,
		Confidence:       classified.confidence,
		ConfidenceReason: classified.reason,
	}
}

func dependencyConfidence(edge graph.FileDependencyEdge, scope string, stale bool) (string, string) {
	if stale {
		return DependencyConfidenceCandidate, DependencyReasonStale
	}
	if scope == DependencyTargetPackage {
		return DependencyConfidenceCandidate, DependencyReasonPackageScope
	}
	if edge.Provenance == graph.ProvPrecise {
		return DependencyConfidenceConfirmed, DependencyReasonPrecise
	}
	// The Go parser marks unqualified same-package references at full weight after
	// narrowing them to the caller's directory. Qualified calls retain the lower
	// syntactic weight because their receiver type is unknown and they fan out to
	// every same-named definition across packages.
	if edge.Source.Language == "go" && edge.Target.Language == "go" &&
		edge.Weight >= graph.WeightLSP && filepath.Dir(edge.Source.File) == filepath.Dir(edge.Target.File) {
		return DependencyConfidenceConfirmed, DependencyReasonSamePackage
	}
	return DependencyConfidenceCandidate, DependencyReasonNameFanout
}

func dependencyEdgesHaveConfirmed(edges []classifiedDependencyEdge) bool {
	for _, edge := range edges {
		if edge.confidence == DependencyConfidenceConfirmed {
			return true
		}
	}
	return false
}

func dependencyGroupsHaveConfirmed(groups map[string]*dependencyKindGroup) bool {
	for _, group := range groups {
		if dependencyEdgesHaveConfirmed(group.edges) {
			return true
		}
	}
	return false
}

func dependencyLocation(n graph.FileDependencyNode) DependencyLocation {
	return DependencyLocation{
		File: n.File, Symbol: n.Symbol, FQN: n.FQN, Kind: n.Kind,
		Language: n.Language, StartLine: n.StartLine,
	}
}

func dependencyTargetScope(edge graph.FileDependencyEdge) string {
	if edge.EdgeType == graph.EdgeImports && edge.Source.Language == "go" {
		return DependencyTargetPackage
	}
	return DependencyTargetFile
}

// referencePersistingLanguages are the languages whose extractors persist
// name-based reference AND import edges (gosrc value refs + Go imports;
// tsscan JSX/framework refs + TS/JS/Vue import specifiers; rubysrc/luasrc
// call refs + require imports). Python's LSP backend still extracts symbols
// only, so a Python-only file keeps the unavailable status.
var referencePersistingLanguages = map[string]bool{
	"go": true, "typescript": true, "javascript": true, "vue": true,
	"ruby": true, "lua": true,
}

func anyLanguage(present map[string]bool, supported map[string]bool) bool {
	for lang := range present {
		if supported[lang] {
			return true
		}
	}
	return false
}

func dependencyCoverage(callGraph string, nodes []graph.Node) FileDependencyCoverage {
	languages := map[string]bool{}
	for _, node := range nodes {
		languages[node.Language] = true
	}
	domains := []DependencyDomainCoverage{callDependencyCoverage(callGraph)}
	if anyLanguage(languages, referencePersistingLanguages) {
		domains = append(domains,
			DependencyDomainCoverage{
				Domain: "references", Status: DependencyCoveragePartial, Scope: "indexed_project",
				Note: "Go function/method values used as callbacks or fields, JSX component usage, and framework-wiring references are indexed; general type/value uses are not",
			},
			DependencyDomainCoverage{
				Domain: "imports", Status: DependencyCoveragePartial, Scope: "indexed_project",
				Note: "in-project Go package imports (one representative file per package) and TS/JS/Ruby/Lua relative imports are indexed; they prove use, not that the file is required",
			},
		)
	} else {
		domains = append(domains,
			DependencyDomainCoverage{
				Domain: "references", Status: DependencyCoverageUnavailable, Scope: "indexed_project",
				Note: "the active language backends do not persist general value/type references",
			},
			DependencyDomainCoverage{
				Domain: "imports", Status: DependencyCoverageUnavailable, Scope: "indexed_project",
				Note: "the active language backends do not currently persist import edges",
			},
		)
	}
	domains = append(domains,
		DependencyDomainCoverage{
			Domain: "runtime_wiring", Status: DependencyCoverageUnavailable, Scope: "indexed_project",
			Note: "reflection, dependency injection, configuration, generated registration, and other dynamic wiring are not modeled completely",
		},
		DependencyDomainCoverage{
			Domain: "external_consumers", Status: DependencyCoverageUnavailable, Scope: "outside_indexed_project",
			Note: "consumers outside this indexed project are not represented by local graph edges",
		},
	)
	coverage := FileDependencyCoverage{Complete: true, Domains: domains}
	for _, domain := range domains {
		if domain.Status != DependencyCoverageComplete {
			coverage.Complete = false
			break
		}
	}
	return coverage
}

func callDependencyCoverage(callGraph string) DependencyDomainCoverage {
	domain := DependencyDomainCoverage{Domain: "calls", Scope: "indexed_project"}
	switch callGraph {
	case CallGraphResolved:
		domain.Status = DependencyCoverageComplete
		domain.Note = "static call resolution completed for every indexed callable file; runtime dispatch remains a separate incomplete domain"
	case CallGraphName:
		domain.Status = DependencyCoveragePartial
		domain.Note = "the Go name graph can over-match selectors and cannot prove the absence of inbound calls"
	case CallGraphUnresolved:
		domain.Status = DependencyCoverageUnavailable
		domain.Note = "at least one callable file has no usable call graph; reindex with --precise"
	default:
		domain.Status = DependencyCoverageUnavailable
		domain.Note = "there are no callable definitions with classified call coverage"
	}
	return domain
}

func dependencyEvidenceKinds(rep *FileDependenciesReport, fileScopedOnly bool) []string {
	if fileScopedOnly && len(rep.fileScopedKinds) > 0 {
		out := make([]string, 0, len(rep.fileScopedKinds))
		for kind := range rep.fileScopedKinds {
			out = append(out, kind)
		}
		sort.Strings(out)
		return out
	}
	seen := map[string]bool{}
	for _, dependent := range rep.Dependents {
		for _, kind := range dependent.Kinds {
			if fileScopedOnly && kind.FileScopedTotal == 0 {
				continue
			}
			seen[kind.Kind] = true
		}
	}
	out := make([]string, 0, len(seen))
	for kind := range seen {
		out = append(out, kind)
	}
	sort.Strings(out)
	return out
}

func dependencyEvidenceKindsForConfidence(rep *FileDependenciesReport, confidence string) []string {
	var source map[string]bool
	switch confidence {
	case DependencyConfidenceConfirmed:
		source = rep.confirmedFileScopedKinds
	case DependencyConfidenceCandidate:
		source = rep.candidateFileScopedKinds
	}
	out := make([]string, 0, len(source))
	for kind := range source {
		out = append(out, kind)
	}
	sort.Strings(out)
	return out
}

func incompleteDependencyDomains(coverage FileDependencyCoverage) []string {
	var out []string
	for _, domain := range coverage.Domains {
		if domain.Status != DependencyCoverageComplete {
			out = append(out, domain.Domain+"="+domain.Status)
		}
	}
	return out
}

func dependencyCoverageSummary(coverage FileDependencyCoverage) string {
	return strings.Join(incompleteDependencyDomains(coverage), ", ")
}
