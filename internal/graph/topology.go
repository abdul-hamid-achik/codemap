package graph

import (
	"path/filepath"
	"sort"
	"strings"
)

// TopologyOptions controls the bounded project architecture summary returned by
// ProjectTopology. Zero limits use conservative defaults.
type TopologyOptions struct {
	MaxSubsystems int
	MaxBridges    int
}

const (
	defaultTopologySubsystems = 50
	defaultTopologyBridges    = 100
)

// TopologySubsystem is one deterministic directory/package-shaped slice of a
// project. It is deliberately derived from source paths instead of pretending a
// heuristic clustering result is a compiler fact.
type TopologySubsystem struct {
	Name          string         `json:"name"`
	Files         int            `json:"files"`
	Symbols       int            `json:"symbols"`
	InternalEdges int            `json:"internal_edges"`
	InboundEdges  int            `json:"inbound_edges"`
	OutboundEdges int            `json:"outbound_edges"`
	Languages     map[string]int `json:"languages,omitempty"`
	Kinds         map[string]int `json:"kinds,omitempty"`
}

// TopologyBridge is an aggregated, directed relationship between two
// subsystems. Counts retain resolution provenance so callers do not confuse a
// name-based fan-out with an exact compiler/LSP relationship.
type TopologyBridge struct {
	From                 string         `json:"from"`
	To                   string         `json:"to"`
	EdgeType             string         `json:"edge_type"`
	Count                int            `json:"count"`
	Provenance           map[string]int `json:"provenance,omitempty"`
	SourceFiles          []string       `json:"source_files,omitempty"`
	SourceFilesTotal     int            `json:"source_files_total"`
	SourceFilesTruncated bool           `json:"source_files_truncated,omitempty"`
	TargetFiles          []string       `json:"target_files,omitempty"`
	TargetFilesTotal     int            `json:"target_files_total"`
	TargetFilesTruncated bool           `json:"target_files_truncated,omitempty"`
}

// ProjectTopology is a deterministic, bounded architecture projection over the
// stored graph. Subsystems are path-shaped; Bridges preserve edge direction.
type ProjectTopology struct {
	Strategy        string              `json:"strategy"`
	Subsystems      []TopologySubsystem `json:"subsystems"`
	Bridges         []TopologyBridge    `json:"bridges"`
	SubsystemsTotal int                 `json:"subsystems_total"`
	BridgesTotal    int                 `json:"bridges_total"`
	Truncated       bool                `json:"truncated,omitempty"`
}

type topologyBridgeKey struct {
	from, to, edgeType string
}

type topologyBridgeAccum struct {
	count       int
	provenance  map[string]int
	sourceFiles map[string]struct{}
	targetFiles map[string]struct{}
}

// ProjectArchitecture returns a compact map of the project's subsystems and
// the directed relationships crossing between them. Defines edges are omitted:
// they only restate file membership and would dominate the useful relations.
func (s *Store) ProjectArchitecture(projectID int64, opts TopologyOptions) (*ProjectTopology, error) {
	return s.projectArchitecture(projectID, opts, nil)
}

// projectArchitecture's barrier is used only by the deterministic concurrency
// test; production callers use ProjectArchitecture.
func (s *Store) projectArchitecture(projectID int64, opts TopologyOptions, afterNodes func() error) (*ProjectTopology, error) {
	if opts.MaxSubsystems <= 0 {
		opts.MaxSubsystems = defaultTopologySubsystems
	}
	if opts.MaxBridges <= 0 {
		opts.MaxBridges = defaultTopologyBridges
	}

	nodes, edges, err := s.projectNodesAndEdgesSnapshot(projectID, afterNodes)
	if err != nil {
		return nil, err
	}

	byID := make(map[int64]Node, len(nodes))
	subsystems := make(map[string]*TopologySubsystem)
	for _, n := range nodes {
		byID[n.ID] = n
		name := topologySubsystemName(n.FilePath)
		sub := subsystems[name]
		if sub == nil {
			sub = &TopologySubsystem{Name: name, Languages: map[string]int{}, Kinds: map[string]int{}}
			subsystems[name] = sub
		}
		if n.Kind == KindFile {
			sub.Files++
		} else {
			sub.Symbols++
		}
		if n.Language != "" {
			sub.Languages[n.Language]++
		}
		if n.Kind != "" {
			sub.Kinds[n.Kind]++
		}
	}

	bridges := make(map[topologyBridgeKey]*topologyBridgeAccum)
	for _, e := range edges {
		if e.EdgeType == EdgeDefines {
			continue
		}
		src, srcOK := byID[e.SourceID]
		tgt, tgtOK := byID[e.TargetID]
		if !srcOK || !tgtOK {
			continue
		}
		from := topologySubsystemName(src.FilePath)
		to := topologySubsystemName(tgt.FilePath)
		if from == to {
			subsystems[from].InternalEdges++
			continue
		}
		subsystems[from].OutboundEdges++
		subsystems[to].InboundEdges++
		key := topologyBridgeKey{from: from, to: to, edgeType: e.EdgeType}
		acc := bridges[key]
		if acc == nil {
			acc = &topologyBridgeAccum{
				provenance: map[string]int{}, sourceFiles: map[string]struct{}{}, targetFiles: map[string]struct{}{},
			}
			bridges[key] = acc
		}
		acc.count++
		if e.Provenance != "" {
			acc.provenance[e.Provenance]++
		}
		acc.sourceFiles[src.FilePath] = struct{}{}
		acc.targetFiles[tgt.FilePath] = struct{}{}
	}

	out := &ProjectTopology{Strategy: "source_path"}
	for _, sub := range subsystems {
		out.Subsystems = append(out.Subsystems, *sub)
	}
	sort.Slice(out.Subsystems, func(i, j int) bool {
		iEdges := out.Subsystems[i].InboundEdges + out.Subsystems[i].OutboundEdges
		jEdges := out.Subsystems[j].InboundEdges + out.Subsystems[j].OutboundEdges
		if iEdges != jEdges {
			return iEdges > jEdges
		}
		if out.Subsystems[i].Symbols != out.Subsystems[j].Symbols {
			return out.Subsystems[i].Symbols > out.Subsystems[j].Symbols
		}
		return out.Subsystems[i].Name < out.Subsystems[j].Name
	})
	out.SubsystemsTotal = len(out.Subsystems)
	if len(out.Subsystems) > opts.MaxSubsystems {
		out.Subsystems = out.Subsystems[:opts.MaxSubsystems]
		out.Truncated = true
	}

	for key, acc := range bridges {
		sourceFiles := sortedTopologyKeys(acc.sourceFiles)
		targetFiles := sortedTopologyKeys(acc.targetFiles)
		const fileSampleLimit = 5
		bridge := TopologyBridge{
			From: key.from, To: key.to, EdgeType: key.edgeType, Count: acc.count,
			Provenance:  acc.provenance,
			SourceFiles: sourceFiles, SourceFilesTotal: len(sourceFiles),
			TargetFiles: targetFiles, TargetFilesTotal: len(targetFiles),
		}
		if len(bridge.SourceFiles) > fileSampleLimit {
			bridge.SourceFiles = bridge.SourceFiles[:fileSampleLimit]
			bridge.SourceFilesTruncated = true
		}
		if len(bridge.TargetFiles) > fileSampleLimit {
			bridge.TargetFiles = bridge.TargetFiles[:fileSampleLimit]
			bridge.TargetFilesTruncated = true
		}
		out.Bridges = append(out.Bridges, bridge)
	}
	sort.Slice(out.Bridges, func(i, j int) bool {
		if out.Bridges[i].Count != out.Bridges[j].Count {
			return out.Bridges[i].Count > out.Bridges[j].Count
		}
		if out.Bridges[i].From != out.Bridges[j].From {
			return out.Bridges[i].From < out.Bridges[j].From
		}
		if out.Bridges[i].To != out.Bridges[j].To {
			return out.Bridges[i].To < out.Bridges[j].To
		}
		return out.Bridges[i].EdgeType < out.Bridges[j].EdgeType
	})
	out.BridgesTotal = len(out.Bridges)
	if len(out.Bridges) > opts.MaxBridges {
		out.Bridges = out.Bridges[:opts.MaxBridges]
		out.Truncated = true
	}
	return out, nil
}

func topologySubsystemName(path string) string {
	clean := filepath.ToSlash(filepath.Clean(path))
	clean = strings.TrimPrefix(clean, "./")
	dir := filepath.ToSlash(filepath.Dir(clean))
	if dir == "." || dir == "" {
		return "(root)"
	}
	parts := strings.Split(dir, "/")
	if len(parts) >= 2 && (parts[0] == "internal" || parts[0] == "cmd" || parts[0] == "pkg") {
		return strings.Join(parts[:2], "/")
	}
	return parts[0]
}

func sortedTopologyKeys(in map[string]struct{}) []string {
	out := make([]string, 0, len(in))
	for key := range in {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
