package app

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

const (
	ReferenceCoveragePartial     = "partial"
	ReferenceCoverageUnavailable = "unavailable"
	ReferenceCoverageNone        = "none"

	ReferenceConfidenceMixed = "mixed"
	ReferenceConfidenceNone  = "none"

	referenceSiteCap       = 50
	referenceDefinitionCap = 25
)

// ReferenceSite is one enclosing symbol/file scope that stores or passes the
// selected function/method as a value. Source.StartLine is the declaration line
// of that enclosing scope, not the exact callback expression line; the graph's
// reference edge intentionally stores scope identity rather than live source
// coordinates.
type ReferenceSite struct {
	Source           SymbolRef `json:"source"`
	Confidence       string    `json:"confidence"`        // confirmed|candidate
	ConfidenceReason string    `json:"confidence_reason"` // precise|same_package|name_fanout|stale_snapshot
}

// ReferencesReport is the stable, bounded inbound value-reference contract.
// It is deliberately separate from RelationReport: references describe callback
// and registration wiring, never call edges.
type ReferencesReport struct {
	SchemaVersion        int                `json:"schema_version"`
	Project              string             `json:"project"`
	Symbol               string             `json:"symbol"`
	Selector             *SymbolSelector    `json:"selector,omitempty"`
	Found                bool               `json:"found"`
	Definitions          []SymbolSelector   `json:"definitions"`
	DefinitionsTotal     int                `json:"definitions_total"`
	DefinitionsTruncated int                `json:"definitions_truncated,omitempty"`
	References           []ReferenceSite    `json:"references"`
	ReferencesTotal      int                `json:"references_total"`
	ReferencesTruncated  int                `json:"references_truncated,omitempty"`
	Stale                bool               `json:"stale"`
	Confidence           string             `json:"confidence"` // confirmed|candidate|mixed|none
	Coverage             string             `json:"coverage"`   // partial|unavailable|none
	CallGraph            string             `json:"call_graph"` // independent call-coverage signal
	Resolution           string             `json:"resolution,omitempty"`
	Note                 string             `json:"note,omitempty"`
	Annotations          []graph.Annotation `json:"annotations,omitempty"`
}

// References returns scopes that use any matching definition as a function or
// method value. A name query intentionally unions same-named definitions; use
// ReferencesBySelector to select one definition.
func (svc *Service) References(cwd, symbol string) (*ReferencesReport, error) {
	rep := newReferencesReport(symbol)
	if !validSymbol(symbol) {
		rep.Resolution = "supply a non-empty symbol name"
		return rep, nil
	}
	g, err := svc.s.Graph()
	if err != nil {
		return nil, err
	}
	_, name, err := svc.resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	rep.Project = name
	p, err := g.GetProjectByName(name)
	if errors.Is(err, graph.ErrNotFound) {
		return rep, nil
	}
	if err != nil {
		return nil, err
	}
	symbol = canonicalSymbol(g, p.ID, symbol)
	rep.Symbol = symbol
	defs, err := g.FindNodesBySymbol(p.ID, symbol)
	if err != nil {
		return nil, err
	}
	return svc.finishReferences(cwd, g, p, rep, defs, func() ([]graph.InboundReference, int, error) {
		return g.References(p.ID, symbol, referenceSiteCap)
	})
}

// ReferencesBySelector returns inbound value-reference scopes for one exact
// definition. Selecting a target does not erase uncertainty in a stored
// name-fanout edge; those sites remain candidates.
func (svc *Service) ReferencesBySelector(cwd string, selector SymbolSelector) (*ReferencesReport, error) {
	res, err := svc.resolveSourceSelector(cwd, selector)
	if err != nil {
		return nil, err
	}
	rep := newReferencesReport("")
	rep.Project = res.projectName
	rep.Selector = &selector
	if res.project == nil || !res.found {
		return rep, nil
	}
	n := res.node
	rep.Symbol = n.Symbol
	rep.Selector = selectorForNode(n)
	return svc.finishReferences(cwd, res.graph, res.project, rep, []graph.Node{n}, func() ([]graph.InboundReference, int, error) {
		return res.graph.ReferencesOfNode(res.project.ID, n.ID, referenceSiteCap)
	})
}

func newReferencesReport(symbol string) *ReferencesReport {
	return &ReferencesReport{
		SchemaVersion: 1, Symbol: symbol,
		Definitions: []SymbolSelector{}, References: []ReferenceSite{},
		Confidence: ReferenceConfidenceNone, Coverage: ReferenceCoverageNone,
		CallGraph: CallGraphNone, Stale: false,
	}
}

func (svc *Service) finishReferences(
	cwd string,
	g *graph.Store,
	p *graph.Project,
	rep *ReferencesReport,
	defs []graph.Node,
	query func() ([]graph.InboundReference, int, error),
) (*ReferencesReport, error) {
	rep.Found = len(defs) > 0
	rep.DefinitionsTotal = len(defs)
	for _, n := range capSlice(defs, referenceDefinitionCap) {
		rep.Definitions = append(rep.Definitions, *selectorForNode(n))
	}
	rep.DefinitionsTruncated = rep.DefinitionsTotal - len(rep.Definitions)
	if !rep.Found {
		return rep, nil
	}

	rep.CallGraph = svc.callGraphStatus(g, p.ID, defs)
	rep.Coverage, rep.Resolution = referenceCoverage(defs)
	if len(defs) > 1 && rep.Selector == nil {
		rep.Note = fmt.Sprintf("%q matches %d definitions — value-reference sites merge all of them; use --at or a selector to choose one", rep.Symbol, len(defs))
	}
	if rep.DefinitionsTruncated > 0 {
		rep.Note = joinNote(rep.Note, fmt.Sprintf("showing %d of %d matching definitions", len(rep.Definitions), rep.DefinitionsTotal))
	}

	if st, stErr := svc.Staleness(cwd); stErr == nil && st != nil {
		rep.Stale = st.Any()
	}
	sites, total, err := query()
	if err != nil {
		return nil, err
	}
	rep.ReferencesTotal = total
	rep.ReferencesTruncated = total - len(sites)
	if total == 0 {
		rep.Note = joinNote(rep.Note, "no indexed value-reference sites found — absence is not proof that this symbol has no callback or registration wiring")
	}
	confirmed, candidate := 0, 0
	for _, site := range sites {
		confidence, reason := referenceSiteConfidence(site, rep.Stale, defs)
		if confidence == DependencyConfidenceConfirmed {
			confirmed++
		} else {
			candidate++
		}
		rep.References = append(rep.References, ReferenceSite{
			Source: nodeToRef(site.Source), Confidence: confidence, ConfidenceReason: reason,
		})
	}
	switch {
	case confirmed > 0 && candidate > 0:
		rep.Confidence = ReferenceConfidenceMixed
	case confirmed > 0:
		rep.Confidence = DependencyConfidenceConfirmed
	case candidate > 0:
		rep.Confidence = DependencyConfidenceCandidate
	default:
		rep.Confidence = ReferenceConfidenceNone
	}
	if rep.Stale {
		rep.Note = joinNote(rep.Note, "index is stale — stored reference sites are candidates and missing sites may be newer than the snapshot")
	}
	if rep.ReferencesTruncated > 0 {
		rep.Note = joinNote(rep.Note, fmt.Sprintf(
			"showing %d of %d enclosing reference scopes; both text and JSON reports are intentionally bounded",
			len(rep.References), rep.ReferencesTotal))
	}
	if rep.Selector != nil && len(defs) == 1 {
		rep.Annotations = nodeAnnotationsFor(g, p.ID, defs[0].FQN, defs[0].Symbol)
	} else {
		rep.Annotations = symbolAnnotations(g, p.ID, rep.Symbol)
	}
	return rep, nil
}

func referenceSiteConfidence(site graph.InboundReference, stale bool, defs []graph.Node) (string, string) {
	if stale {
		return DependencyConfidenceCandidate, DependencyReasonStale
	}
	if site.Ambiguous || site.Weight < graph.WeightLSP {
		return DependencyConfidenceCandidate, DependencyReasonNameFanout
	}
	if site.Provenance == graph.ProvPrecise {
		return DependencyConfidenceConfirmed, DependencyReasonPrecise
	}
	// Parser reference edges use full weight when an unqualified name narrows to
	// the source package. A selector such as pkg.Handler or x.Handler is also
	// syntactically represented by the bare name Handler, though, and may fall
	// back to a unique definition in another package. Do not call that exact just
	// because it is unique: require an actual same-directory Go definition.
	if site.Source.Language == "go" {
		for _, def := range defs {
			if def.Language == "go" && filepath.Dir(site.Source.FilePath) == filepath.Dir(def.FilePath) {
				return DependencyConfidenceConfirmed, DependencyReasonSamePackage
			}
		}
	}
	return DependencyConfidenceCandidate, DependencyReasonNameFanout
}

func referenceCoverage(defs []graph.Node) (string, string) {
	if len(defs) == 0 {
		return ReferenceCoverageNone, ""
	}
	languages := map[string]bool{}
	for _, n := range defs {
		languages[n.Language] = true
	}
	nonGo := make([]string, 0, len(languages))
	for lang := range languages {
		if lang != "go" {
			nonGo = append(nonGo, lang)
		}
	}
	sort.Strings(nonGo)
	const goLimit = "Go callback/registration patterns are indexed as enclosing symbol or file scopes, not exact expression lines; an empty result does not prove no wiring"
	if languages["go"] {
		if len(nonGo) == 0 {
			return ReferenceCoveragePartial, "value-reference coverage is partial: " + goLimit
		}
		return ReferenceCoveragePartial, fmt.Sprintf(
			"value-reference coverage is partial: %s; coverage is unavailable for %s",
			goLimit, strings.Join(nonGo, ", "))
	}
	return ReferenceCoverageUnavailable, fmt.Sprintf(
		"value-reference coverage is unavailable for %s; an empty result does not prove no wiring",
		strings.Join(nonGo, ", "))
}
