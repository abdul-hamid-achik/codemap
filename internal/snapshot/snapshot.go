// Package snapshot serializes a project's code-intelligence slice to a portable,
// store-agnostic directory and restores it — the basis for stashing/restoring a
// branch's index without re-indexing (see the BD.* epic in BACKLOG). It writes
// graph nodes, edges, index state, call-graph coverage, and annotations as
// newline-delimited JSON in a
// deterministic order, so identical slices across branches serialize byte-for-byte
// identically and fcheap can content-dedup them. (Embedding vectors are a separate
// slice, BD.2b.) Edges reference nodes by their position in the sorted node list,
// not the volatile auto-increment DB id, so a snapshot is reproducible across
// re-indexings of the same code.
package snapshot

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/abdul-hamid-achik/codemap/internal/graph"
	"github.com/abdul-hamid-achik/codemap/internal/vector"
)

// SchemaVersion is bumped when the on-disk snapshot format changes incompatibly.
// call_graph_coverage is an optional additive v1 artifact: an absent manifest
// field/file is a legacy snapshot with conservatively unknown coverage.
const SchemaVersion = 1

const (
	fileManifest    = "snapshot.json"
	fileNodes       = "nodes.jsonl"
	fileEdges       = "edges.jsonl"
	fileIndexState  = "index_state.jsonl"
	fileCallGraph   = "call_graph_coverage.jsonl"
	fileAnnotations = "annotations.jsonl"
	fileVectors     = "vectors.jsonl"
)

// Manifest is snapshot.json — the header describing a snapshot directory.
type Manifest struct {
	SchemaVersion     int    `json:"schema_version"`
	Project           string `json:"project"`
	EmbeddingProfile  string `json:"embedding_profile"`
	BaseSHA           string `json:"base_sha"`
	Nodes             int    `json:"nodes"`
	Edges             int    `json:"edges"`
	IndexState        int    `json:"index_state"`
	CallGraphCoverage int    `json:"call_graph_coverage,omitempty"`
	Annotations       int    `json:"annotations"`
	Vectors           int    `json:"vectors"`
}

// snapNode is a node's content without its volatile identity (DB id, project id,
// timestamps, vec id) — those are re-assigned on import.
type snapNode struct {
	FilePath   string `json:"file_path"`
	Symbol     string `json:"symbol"`
	FQN        string `json:"fqn,omitempty"`
	Kind       string `json:"kind"`
	Language   string `json:"language"`
	StartLine  int    `json:"start_line"`
	EndLine    int    `json:"end_line"`
	Signature  string `json:"signature,omitempty"`
	Docstring  string `json:"docstring,omitempty"`
	SourceHash string `json:"source_hash,omitempty"`
}

// snapEdge references its endpoints by node index (position in the sorted node
// list), not the DB id, so the serialization is reproducible across re-indexings.
type snapEdge struct {
	Source     int     `json:"source"`
	Target     int     `json:"target"`
	EdgeType   string  `json:"edge_type"`
	Weight     float64 `json:"weight"`
	Provenance string  `json:"provenance,omitempty"`
}

// snapAnnotation drops the DB id and timestamp so annotations dedup by content.
type snapAnnotation struct {
	Kind       string `json:"kind"`
	Target     string `json:"target"`
	Source     string `json:"source"`
	ExternalID string `json:"external_id,omitempty"`
	Note       string `json:"note,omitempty"`
	Data       string `json:"data,omitempty"`
}

// snapVector carries an embedding by node POSITION (index into the sorted node
// list) so its node id is remapped on import the same way edges are — the raw
// vector + content + metadata travel along, so restore needs no re-embedding.
type snapVector struct {
	Pos     int             `json:"pos"`
	Vector  []float32       `json:"vector"`
	Content string          `json:"content,omitempty"`
	Meta    vector.NodeMeta `json:"meta"`
}

func nodeKey(n graph.Node) string {
	return n.FilePath + "\x00" + itoa(n.StartLine) + "\x00" + n.Symbol + "\x00" + n.FQN + "\x00" + n.Kind + "\x00" + n.Signature
}

func itoa(i int) string { return fmt.Sprintf("%d", i) }

// Export writes the project's slice into dir (created if needed): the graph
// (nodes/edges/index_state/call_graph_coverage/annotations) and, when vec != nil, its embedding
// vectors. Rows are emitted in deterministic order so identical slices
// hash-identically (fcheap dedups them).
func Export(g *graph.Store, vec *vector.Store, projectID int64, project, dir, profile, baseSHA string) (*Manifest, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	nodes, err := g.ProjectNodes(projectID)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(nodes, func(i, j int) bool { return nodeKey(nodes[i]) < nodeKey(nodes[j]) })
	idxOf := make(map[int64]int, len(nodes))
	for i, n := range nodes {
		idxOf[n.ID] = i
	}
	snodes := make([]any, len(nodes))
	for i, n := range nodes {
		snodes[i] = snapNode{
			FilePath: n.FilePath, Symbol: n.Symbol, FQN: n.FQN, Kind: n.Kind, Language: n.Language,
			StartLine: n.StartLine, EndLine: n.EndLine, Signature: n.Signature, Docstring: n.Docstring, SourceHash: n.SourceHash,
		}
	}
	if err := writeJSONL(filepath.Join(dir, fileNodes), snodes); err != nil {
		return nil, err
	}

	edges, err := g.ProjectEdges(projectID)
	if err != nil {
		return nil, err
	}
	sedges := make([]snapEdge, 0, len(edges))
	for _, e := range edges {
		si, sok := idxOf[e.SourceID]
		ti, tok := idxOf[e.TargetID]
		if !sok || !tok {
			continue // an endpoint outside the project slice — skip (shouldn't happen)
		}
		sedges = append(sedges, snapEdge{Source: si, Target: ti, EdgeType: e.EdgeType, Weight: e.Weight, Provenance: e.Provenance})
	}
	sort.SliceStable(sedges, func(i, j int) bool {
		a, b := sedges[i], sedges[j]
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		if a.Target != b.Target {
			return a.Target < b.Target
		}
		if a.EdgeType != b.EdgeType {
			return a.EdgeType < b.EdgeType
		}
		return a.Provenance < b.Provenance
	})
	eAny := make([]any, len(sedges))
	for i, e := range sedges {
		eAny[i] = e
	}
	if err := writeJSONL(filepath.Join(dir, fileEdges), eAny); err != nil {
		return nil, err
	}

	idx, err := g.ProjectIndexState(projectID)
	if err != nil {
		return nil, err
	}
	iAny := make([]any, len(idx))
	for i, e := range idx {
		iAny[i] = e
	}
	if err := writeJSONL(filepath.Join(dir, fileIndexState), iAny); err != nil {
		return nil, err
	}

	coverage, err := g.ProjectCallGraphCoverage(projectID)
	if err != nil {
		return nil, err
	}
	cAny := make([]any, len(coverage))
	for i, entry := range coverage {
		cAny[i] = entry
	}
	if err := writeJSONL(filepath.Join(dir, fileCallGraph), cAny); err != nil {
		return nil, err
	}

	anns, err := g.AllAnnotations(projectID)
	if err != nil {
		return nil, err
	}
	sanns := make([]snapAnnotation, len(anns))
	for i, a := range anns {
		sanns[i] = snapAnnotation{Kind: a.Kind, Target: a.Target, Source: a.Source, ExternalID: a.ExternalID, Note: a.Note, Data: a.Data}
	}
	sort.SliceStable(sanns, func(i, j int) bool { return annKey(sanns[i]) < annKey(sanns[j]) })
	aAny := make([]any, len(sanns))
	for i, a := range sanns {
		aAny[i] = a
	}
	if err := writeJSONL(filepath.Join(dir, fileAnnotations), aAny); err != nil {
		return nil, err
	}

	nVectors := 0
	if vec != nil {
		vrecs, verr := vec.IterByProject(project)
		if verr != nil {
			return nil, verr
		}
		svecs := make([]snapVector, 0, len(vrecs))
		for _, vr := range vrecs {
			pos, ok := idxOf[vr.Meta.NodeID]
			if !ok {
				continue // an embedding for a node not in this slice — skip
			}
			// P1-17 (B55): zero the volatile identity fields so the
			// serialized vector dedups on CONTENT across exports. NodeID
			// is per-restore (re-mapped by Import); Project is
			// re-stamped at import time. Both break fcheap's
			// byte-identical content dedup otherwise.
			vr.Meta.NodeID = 0
			vr.Meta.Project = ""
			svecs = append(svecs, snapVector{Pos: pos, Vector: vr.Vector, Content: vr.Content, Meta: vr.Meta})
		}
		sort.SliceStable(svecs, func(i, j int) bool { return svecs[i].Pos < svecs[j].Pos })
		vAny := make([]any, len(svecs))
		for i, v := range svecs {
			vAny[i] = v
		}
		if err := writeJSONL(filepath.Join(dir, fileVectors), vAny); err != nil {
			return nil, err
		}
		nVectors = len(svecs)
	}

	m := &Manifest{
		SchemaVersion: SchemaVersion, Project: project, EmbeddingProfile: profile, BaseSHA: baseSHA,
		Nodes: len(nodes), Edges: len(sedges), IndexState: len(idx), CallGraphCoverage: len(coverage), Annotations: len(anns), Vectors: nVectors,
	}
	if err := writeJSON(filepath.Join(dir, fileManifest), m); err != nil {
		return nil, err
	}
	return m, nil
}

// Import restores a snapshot dir INTO the project with id projectID (already
// registered). It wipes the project's nodes + index state + call-graph coverage,
// bulk-reinserts the snapshot's nodes/edges/index-state/coverage, and MERGES annotations (adds those not
// already present — never deletes existing ones). It refuses if the snapshot's
// embedding_profile disagrees with wantProfile (never mix models). Both empty
// profiles are treated as compatible. When vec != nil, the project's embeddings
// are replaced with the snapshot's (re-inserted with remapped node ids — no
// re-embedding).
//
// P1-17 (B50/O62/O100): every jsonl file is read+validated against the
// manifest's counts BEFORE WipeProject so a truncated/missing file is a
// no-op (the import fails and the project is left untouched). Pre-fix the
// wipe ran first — a truncated nodes.jsonl left a wiped, empty project
// that the fallback reindex silently re-built, hiding the corruption.
// On a post-commit error the wipe is re-applied (with index_state cleared
// via WipeProject) so the fallback reindex sees a cold project instead
// of a half-restored one.
func Import(g *graph.Store, vec *vector.Store, projectID int64, project, dir, wantProfile string) (*Manifest, error) {
	var m Manifest
	if err := readJSON(filepath.Join(dir, fileManifest), &m); err != nil {
		return nil, fmt.Errorf("read snapshot manifest: %w", err)
	}
	if m.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("snapshot schema v%d != supported v%d", m.SchemaVersion, SchemaVersion)
	}
	if wantProfile != "" && m.EmbeddingProfile != "" && m.EmbeddingProfile != wantProfile {
		return nil, fmt.Errorf("snapshot embedding profile %q != current %q — refusing to mix models", m.EmbeddingProfile, wantProfile)
	}

	// Pre-validate every jsonl against the manifest. An absent optional file
	// (vectors) is fine — readJSONL returns no error on os.ErrNotExist and
	// leaves the slice nil/empty, which the manifest count check then catches.
	var snodes []snapNode
	if err := readJSONL(filepath.Join(dir, fileNodes), &snodes); err != nil {
		return nil, fmt.Errorf("read nodes.jsonl: %w", err)
	}
	var sedges []snapEdge
	if err := readJSONL(filepath.Join(dir, fileEdges), &sedges); err != nil {
		return nil, fmt.Errorf("read edges.jsonl: %w", err)
	}
	var idxState []graph.IndexEntry
	if err := readJSONL(filepath.Join(dir, fileIndexState), &idxState); err != nil {
		return nil, fmt.Errorf("read index_state.jsonl: %w", err)
	}
	var coverage []graph.CallGraphCoverageEntry
	if err := readJSONL(filepath.Join(dir, fileCallGraph), &coverage); err != nil {
		return nil, fmt.Errorf("read call_graph_coverage.jsonl: %w", err)
	}
	var sanns []snapAnnotation
	if err := readJSONL(filepath.Join(dir, fileAnnotations), &sanns); err != nil {
		return nil, fmt.Errorf("read annotations.jsonl: %w", err)
	}
	var svecs []snapVector
	if err := readJSONL(filepath.Join(dir, fileVectors), &svecs); err != nil {
		return nil, fmt.Errorf("read vectors.jsonl: %w", err)
	}
	if len(snodes) != m.Nodes {
		return nil, fmt.Errorf("snapshot corrupt: nodes.jsonl has %d rows, manifest says %d", len(snodes), m.Nodes)
	}
	if len(sedges) != m.Edges {
		return nil, fmt.Errorf("snapshot corrupt: edges.jsonl has %d rows, manifest says %d", len(sedges), m.Edges)
	}
	if len(idxState) != m.IndexState {
		return nil, fmt.Errorf("snapshot corrupt: index_state.jsonl has %d rows, manifest says %d", len(idxState), m.IndexState)
	}
	if len(coverage) != m.CallGraphCoverage {
		return nil, fmt.Errorf("snapshot corrupt: call_graph_coverage.jsonl has %d rows, manifest says %d", len(coverage), m.CallGraphCoverage)
	}
	if len(sanns) != m.Annotations {
		return nil, fmt.Errorf("snapshot corrupt: annotations.jsonl has %d rows, manifest says %d", len(sanns), m.Annotations)
	}
	if len(svecs) != m.Vectors {
		return nil, fmt.Errorf("snapshot corrupt: vectors.jsonl has %d rows, manifest says %d", len(svecs), m.Vectors)
	}

	if err := g.WipeProject(projectID); err != nil {
		return nil, err
	}

	// Batch the node + edge + index-state re-insertion in one transaction so the
	// full restore is one fsync, not N. The WipeProject above is its own
	// transaction (atomic delete), so a failure here leaves an empty project —
	// which is correct (a failed restore shouldn't leave a half-populated graph).
	tx, err := g.BeginTx(context.Background())
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }() // safe: Commit renders this a no-op

	newID := make([]int64, len(snodes))
	for i, sn := range snodes {
		n := &graph.Node{
			ProjectID: projectID, FilePath: sn.FilePath, Symbol: sn.Symbol, FQN: sn.FQN, Kind: sn.Kind,
			Language: sn.Language, StartLine: sn.StartLine, EndLine: sn.EndLine,
			Signature: sn.Signature, Docstring: sn.Docstring, SourceHash: sn.SourceHash,
		}
		id, err := graph.AddNodeTx(tx, n)
		if err != nil {
			return nil, err
		}
		newID[i] = id
	}

	for _, e := range sedges {
		if e.Source < 0 || e.Source >= len(newID) || e.Target < 0 || e.Target >= len(newID) {
			return nil, fmt.Errorf("snapshot edge references node index out of range")
		}
		if _, err := graph.AddEdgeProvTx(tx, newID[e.Source], newID[e.Target], e.EdgeType, e.Weight, e.Provenance); err != nil {
			return nil, err
		}
	}

	for _, e := range idxState {
		if err := graph.SetFileHashTx(tx, projectID, e.FilePath, e.FileHash); err != nil {
			return nil, err
		}
	}
	for _, entry := range coverage {
		if err := graph.MarkCallGraphResolvedTx(tx, projectID, entry.FilePath, entry.Resolver); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	existing, err := g.AllAnnotations(projectID)
	if err != nil {
		// P1-17 (B50): a post-commit error must leave a COLD project so
		// the fallback reindex sees an empty graph. Re-apply WipeProject
		// (the caller is told the import failed and will reindex).
		_ = g.WipeProject(projectID)
		return nil, err
	}
	have := make(map[string]bool, len(existing))
	for _, a := range existing {
		have[annKey(snapAnnotation{Kind: a.Kind, Target: a.Target, Source: a.Source, ExternalID: a.ExternalID, Note: a.Note, Data: a.Data})] = true
	}
	for _, a := range sanns {
		if have[annKey(a)] {
			continue // already present — merge, don't duplicate or blow away
		}
		if _, _, err := g.UpsertAnnotation(projectID, graph.Annotation{Kind: a.Kind, Target: a.Target, Source: a.Source, ExternalID: a.ExternalID, Note: a.Note, Data: a.Data}); err != nil {
			_ = g.WipeProject(projectID)
			return nil, err
		}
	}

	if vec != nil {
		if _, err := vec.DeleteByProject(project); err != nil { // clear stale vectors before restore
			_ = g.WipeProject(projectID)
			return nil, err
		}
		for _, sv := range svecs {
			if sv.Pos < 0 || sv.Pos >= len(newID) {
				_ = g.WipeProject(projectID)
				return nil, fmt.Errorf("snapshot vector references node index out of range")
			}
			meta := sv.Meta
			// P1-17 (B55): zero the volatile identity fields so the
			// serialized vector dedups on CONTENT across restorers.
			// meta.NodeID is re-assigned to the freshly-inserted node
			// id (the one the restored graph will see), and meta.Project
			// is re-stamped from the importing project; both are
			// otherwise volatile (per-restore) and break fcheap's
			// byte-identical content dedup.
			meta.NodeID = newID[sv.Pos] // remap to the new node id
			meta.Project = project
			if _, err := vec.Insert(sv.Vector, sv.Content, meta); err != nil {
				_ = g.WipeProject(projectID)
				return nil, err
			}
		}
		if err := vec.Sync(); err != nil {
			_ = g.WipeProject(projectID)
			return nil, err
		}
	}
	return &m, nil
}

func annKey(a snapAnnotation) string {
	return a.Kind + "\x00" + a.Target + "\x00" + a.Source + "\x00" + a.ExternalID + "\x00" + a.Note + "\x00" + a.Data
}

// --- jsonl/json io ---

func writeJSONL(path string, items []any) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	for _, it := range items {
		if err := enc.Encode(it); err != nil { // Encode appends a newline
			return err
		}
	}
	return w.Flush()
}

func readJSONL(path string, out any) error {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil // an absent optional file is an empty set
	}
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	// out must be a non-nil pointer to a slice; decode line by line via reflection-
	// free generic handling by the callers' concrete types.
	switch v := out.(type) {
	case *[]snapNode:
		return decodeLines(f, func(b []byte) error {
			var x snapNode
			if err := json.Unmarshal(b, &x); err != nil {
				return err
			}
			*v = append(*v, x)
			return nil
		})
	case *[]snapEdge:
		return decodeLines(f, func(b []byte) error {
			var x snapEdge
			if err := json.Unmarshal(b, &x); err != nil {
				return err
			}
			*v = append(*v, x)
			return nil
		})
	case *[]graph.IndexEntry:
		return decodeLines(f, func(b []byte) error {
			var x graph.IndexEntry
			if err := json.Unmarshal(b, &x); err != nil {
				return err
			}
			*v = append(*v, x)
			return nil
		})
	case *[]graph.CallGraphCoverageEntry:
		return decodeLines(f, func(b []byte) error {
			var x graph.CallGraphCoverageEntry
			if err := json.Unmarshal(b, &x); err != nil {
				return err
			}
			*v = append(*v, x)
			return nil
		})
	case *[]snapAnnotation:
		return decodeLines(f, func(b []byte) error {
			var x snapAnnotation
			if err := json.Unmarshal(b, &x); err != nil {
				return err
			}
			*v = append(*v, x)
			return nil
		})
	case *[]snapVector:
		return decodeLines(f, func(b []byte) error {
			var x snapVector
			if err := json.Unmarshal(b, &x); err != nil {
				return err
			}
			*v = append(*v, x)
			return nil
		})
	default:
		return fmt.Errorf("readJSONL: unsupported target type %T", out)
	}
}

func decodeLines(f *os.File, onLine func([]byte) error) error {
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024) // allow long lines (big source/docstrings)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		if err := onLine(line); err != nil {
			return err
		}
	}
	return sc.Err()
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
