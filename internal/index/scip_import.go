package index

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"sort"

	"github.com/abdul-hamid-achik/codemap/internal/extract/scip"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

// scipEdgeWeight mirrors graph.WeightTreeSitter (0.7): SCIP resolves every
// reference to a stable symbol, but the import is one generation behind the
// indexer run and carries no call-role evidence. The edge provenance column
// carries the producer tag ("scip:<tool>"); the producer's version is recorded
// in scip_producers and stamped into every imported node's source_hash
// ("scip:<tool>@<version>") — nodes have no provenance column of their own.
const (
	scipEdgeWeight = 0.7
	scipProvPrefix = "scip:"
	scipSchemaSQL  = `CREATE TABLE IF NOT EXISTS scip_producers (
    project_id  INTEGER NOT NULL,
    producer    TEXT    NOT NULL,
    version     TEXT    NOT NULL,
    imported_at TEXT    NOT NULL,
    PRIMARY KEY (project_id, producer)
)`
	scipNodesSQL = `CREATE TABLE IF NOT EXISTS scip_nodes (
    node_id   INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL,
    producer  TEXT NOT NULL
)`
)

// importScipIndex imports a project-level .scip index (Options.ScipPath) into
// the project graph as ONE producer generation: the prior generation of this
// producer's nodes/edges is wiped inside the same transaction that writes the
// new ones, so the replacement is atomic. Validation (metadata, project root,
// relative paths) runs before any write, so an invalid file leaves the graph
// untouched. SCIP occurrences carry no call-role evidence: only symbol nodes,
// references, implements and derivable file imports are written — call edges
// are never emitted and the call graph stays owned by the native/LSP backends.
func (ix *Indexer) importScipIndex(ctx context.Context, projectID int64, root, scipPath string, res *Result) error {
	data, err := os.ReadFile(scipPath)
	if err != nil {
		return fmt.Errorf("read scip index %s: %w (point --scip at a .scip file produced by an indexer run over this project)", scipPath, err)
	}
	idx, err := scip.ReadIndex(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("parse %s: %w (not a valid .scip index — regenerate it with the indexer)", scipPath, err)
	}
	producer := scip.ProducerOf(idx)
	version := scip.VersionOf(idx)
	provTag := scipProvPrefix + producer
	nodeTag := provTag + "@" + version

	tx, err := ix.graph.BeginTx(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.Exec(scipSchemaSQL); err != nil {
		return fmt.Errorf("prepare scip import tables: %w", err)
	}
	if _, err := tx.Exec(scipNodesSQL); err != nil {
		return fmt.Errorf("prepare scip import tables: %w", err)
	}
	// Wipe this producer's prior generation: nodes registered in scip_nodes,
	// the edges touching them (no FK cascade), then the registration rows.
	// scip edges only ever connect scip-owned nodes, so this fully replaces
	// the generation without touching native/LSP-produced data.
	if _, err := tx.Exec(`DELETE FROM edges WHERE
		source_id IN (SELECT node_id FROM scip_nodes WHERE project_id = ? AND producer = ?) OR
		target_id IN (SELECT node_id FROM scip_nodes WHERE project_id = ? AND producer = ?)`,
		projectID, producer, projectID, producer); err != nil {
		return fmt.Errorf("wipe prior scip generation: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM nodes WHERE id IN
		(SELECT node_id FROM scip_nodes WHERE project_id = ? AND producer = ?)`, projectID, producer); err != nil {
		return fmt.Errorf("wipe prior scip generation: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM scip_nodes WHERE project_id = ? AND producer = ?`, projectID, producer); err != nil {
		return fmt.Errorf("wipe prior scip generation: %w", err)
	}

	rep := &ScipImportReport{Path: scipPath, Producer: producer, Version: version}
	imp := &scipImporter{
		tx: tx, projectID: projectID, producer: producer, provTag: provTag, nodeTag: nodeTag,
		symID: map[string]int64{}, fileNode: map[string]int64{},
		fileDefs: map[string][]defAt{}, seenEdge: map[edgeKey]bool{},
		rep: rep,
	}
	summary, err := scip.Import(idx, scip.Options{ProjectRoot: root}, imp.emit)
	if err != nil {
		return fmt.Errorf("import %s: %w", scipPath, err)
	}
	rep.Documents = summary.Documents
	if _, err := tx.Exec(`INSERT OR REPLACE INTO scip_producers(project_id, producer, version, imported_at)
		VALUES (?, ?, ?, datetime('now'))`, projectID, producer, version); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	tx = nil
	res.Scip = rep
	return nil
}

// ScipImportReport publishes per-domain completeness for a --scip import.
// UnresolvedReferences counts references whose target symbol has no definition
// in the index (external/dependency symbols) — honest coverage, not a failure.
type ScipImportReport struct {
	Path                 string `json:"path"`
	Producer             string `json:"producer"`
	Version              string `json:"version,omitempty"`
	Documents            int    `json:"documents"`
	Definitions          int    `json:"definitions"`
	References           int    `json:"references"`
	Implements           int    `json:"implements"`
	Imports              int    `json:"imports"`
	UnresolvedReferences int    `json:"unresolved_references,omitempty"`
	// CallEdges is always 0 and exists to make the honesty explicit: SCIP
	// occurrences are not call-role evidence, so call_graph coverage is never
	// claimed from an import (see docs/languages.md).
	CallEdges int `json:"call_edges"`
}

type defAt struct {
	line int
	id   int64
}

type edgeKey struct {
	src, tgt int64
	typ      string
}

type scipImporter struct {
	tx        *sql.Tx
	projectID int64
	producer  string
	provTag   string
	nodeTag   string
	symID     map[string]int64 // scip symbol → def node id
	fileNode  map[string]int64 // rel path → file node id
	fileDefs  map[string][]defAt
	seenEdge  map[edgeKey]bool
	defsDone  bool
	rep       *ScipImportReport
}

func (s *scipImporter) emit(b scip.Batch) error {
	switch b.Kind {
	case scip.BatchDefs:
		for _, d := range b.Defs {
			id, err := graph.AddNodeTx(s.tx, &graph.Node{
				ProjectID: s.projectID, FilePath: d.File, Symbol: d.Name, FQN: d.Symbol,
				Kind: d.Kind, Language: d.Language, StartLine: d.StartLine, EndLine: d.EndLine,
				SourceHash: s.nodeTag,
			})
			if err != nil {
				return err
			}
			s.symID[d.Symbol] = id
			if err := s.registerNode(id); err != nil {
				return err
			}
			s.fileDefs[d.File] = append(s.fileDefs[d.File], defAt{line: d.StartLine, id: id})
			s.rep.Definitions++
		}
		s.defsDone = true
		for f := range s.fileDefs {
			sort.Slice(s.fileDefs[f], func(i, j int) bool { return s.fileDefs[f][i].line < s.fileDefs[f][j].line })
		}
	case scip.BatchEdges:
		if !s.defsDone {
			return fmt.Errorf("scip import: edges emitted before definitions")
		}
		for _, r := range b.Refs {
			tgt, ok := s.symID[r.Symbol]
			if !ok {
				s.rep.UnresolvedReferences++
				continue
			}
			src := s.enclosingDef(r.File, r.Line)
			if err := s.addEdge(src, tgt, graph.EdgeReferences); err != nil {
				return err
			}
			s.rep.References++
		}
		for _, im := range b.Implements {
			src, ok := s.symID[im.Symbol]
			if !ok {
				continue
			}
			tgt, ok := s.symID[im.Target]
			if !ok {
				s.rep.UnresolvedReferences++
				continue
			}
			if err := s.addEdge(src, tgt, graph.EdgeImplements); err != nil {
				return err
			}
			s.rep.Implements++
		}
		for _, fi := range b.Imports {
			src, err := s.fileNodeTx(fi.FromFile)
			if err != nil {
				return err
			}
			tgt, err := s.fileNodeTx(fi.ToFile)
			if err != nil {
				return err
			}
			if err := s.addEdge(src, tgt, graph.EdgeImports); err != nil {
				return err
			}
			s.rep.Imports++
		}
	}
	return nil
}

// enclosingDef attributes a reference to the last definition at or before its
// line in the same file, falling back to the file node.
func (s *scipImporter) enclosingDef(file string, line int) int64 {
	defs := s.fileDefs[file]
	i := sort.Search(len(defs), func(i int) bool { return defs[i].line > line })
	if i == 0 {
		id, err := s.fileNodeTx(file)
		if err != nil {
			return 0
		}
		return id
	}
	return defs[i-1].id
}

func (s *scipImporter) fileNodeTx(file string) (int64, error) {
	if id, ok := s.fileNode[file]; ok {
		return id, nil
	}
	id, err := graph.AddNodeTx(s.tx, &graph.Node{
		ProjectID: s.projectID, FilePath: file, Symbol: file, FQN: file,
		Kind: graph.KindFile, Language: "file", SourceHash: s.nodeTag,
	})
	if err != nil {
		return 0, err
	}
	s.fileNode[file] = id
	if err := s.registerNode(id); err != nil {
		return 0, err
	}
	return id, nil
}

func (s *scipImporter) addEdge(src, tgt int64, typ string) error {
	k := edgeKey{src, tgt, typ}
	if s.seenEdge[k] {
		return nil
	}
	s.seenEdge[k] = true
	_, err := graph.AddEdgeProvTx(s.tx, src, tgt, typ, scipEdgeWeight, s.provTag)
	return err
}

// registerNode records a scip-owned node so the next import of the same
// producer can wipe exactly this generation (nodes + touching edges).
func (s *scipImporter) registerNode(id int64) error {
	if _, err := s.tx.Exec(`INSERT OR REPLACE INTO scip_nodes(node_id, project_id, producer)
		VALUES (?, ?, ?)`, id, s.projectID, s.producer); err != nil {
		return fmt.Errorf("register scip node: %w", err)
	}
	return nil
}
