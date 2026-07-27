package graph

// schemaVersion is bumped whenever schemaSQL changes in a way that requires a
// migration. The current version is stored in SQLite's PRAGMA user_version.
// v2 adds annotations, v3 edge provenance, v4 composite query indexes, v5
// per-file precise call-graph coverage, and v6 idempotent annotation keys.
const schemaVersion = 6

// Edge provenance: how an edge's target was resolved. Name-based fan-out (the
// fast default) tags 'name'; the opt-in go/types pass tags 'precise' and
// physically supersedes the 'name' edges of cleanly type-checked sources.
const (
	ProvName    = "name"
	ProvPrecise = "precise"
)

// Annotation target kinds.
const (
	AnnotationNode = "node" // attached to a symbol (FQN)
	AnnotationPath = "path" // attached to a call path "<from> -> <to>"
)

// Node kinds.
const (
	KindFile     = "file"
	KindFunction = "function"
	KindMethod   = "method"
	KindType     = "type"
	KindClass    = "class"
	KindVariable = "variable"
	KindTest     = "test"
	KindModule   = "module"
	KindSelector = "selector"
)

// Edge types.
const (
	EdgeCalls      = "calls"
	EdgeImports    = "imports"
	EdgeImplements = "implements"
	EdgeReferences = "references"
	EdgeDependsOn  = "depends_on"
	EdgeTests      = "tests"
	EdgeOverrides  = "overrides"
	EdgeDefines    = "defines"
	// EdgeStyles: a JSX className / HTML class attribute → the CSS selector node
	// defining that class or id. Name-resolved (candidate weight), never part of
	// the call graph.
	EdgeStyles = "styles"
)

// Edge weights by extraction confidence.
const (
	WeightLSP        = 1.0 // precise (resolved cross-file references)
	WeightTreeSitter = 0.7 // syntactic only (may misattribute same-named symbols)
)

const schemaSQL = `
CREATE TABLE IF NOT EXISTS projects (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT UNIQUE NOT NULL,
    path        TEXT NOT NULL,
    language    TEXT,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS nodes (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id  INTEGER NOT NULL,
    file_path   TEXT NOT NULL,
    symbol      TEXT,
    fqn         TEXT,
    kind        TEXT NOT NULL,
    language    TEXT NOT NULL,
    start_line  INTEGER NOT NULL,
    end_line    INTEGER NOT NULL,
    signature   TEXT,
    docstring   TEXT,
    source_hash TEXT NOT NULL,
    vec_id      TEXT,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL,
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_nodes_project ON nodes(project_id);
CREATE INDEX IF NOT EXISTS idx_nodes_file    ON nodes(project_id, file_path);
CREATE INDEX IF NOT EXISTS idx_nodes_fqn     ON nodes(fqn);
CREATE INDEX IF NOT EXISTS idx_nodes_symbol  ON nodes(symbol);
CREATE INDEX IF NOT EXISTS idx_nodes_kind    ON nodes(kind);
-- P1-13 (O18): composite indexes for the hot lookups (every query pairs
-- project_id with symbol or fqn). The single-column indexes above are
-- kept for backward compat; the composites are the ones the planner picks.
CREATE INDEX IF NOT EXISTS idx_nodes_proj_sym ON nodes(project_id, symbol);
CREATE INDEX IF NOT EXISTS idx_nodes_proj_fqn ON nodes(project_id, fqn);

CREATE TABLE IF NOT EXISTS edges (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    source_id   INTEGER NOT NULL,
    target_id   INTEGER NOT NULL,
    edge_type   TEXT NOT NULL,
    weight      REAL NOT NULL DEFAULT 1.0,
    provenance  TEXT NOT NULL DEFAULT 'name',
    created_at  TEXT NOT NULL,
    FOREIGN KEY (source_id) REFERENCES nodes(id) ON DELETE CASCADE,
    FOREIGN KEY (target_id) REFERENCES nodes(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_edges_source ON edges(source_id, edge_type);
CREATE INDEX IF NOT EXISTS idx_edges_target ON edges(target_id, edge_type);
CREATE INDEX IF NOT EXISTS idx_edges_type   ON edges(edge_type);
-- idx_edges_source_prov is created in migrate() after the provenance column is
-- guaranteed (it can't live here: on a pre-v3 edges table the column doesn't
-- exist yet when schemaSQL runs).

CREATE TABLE IF NOT EXISTS index_state (
    project_id  INTEGER NOT NULL,
    file_path   TEXT NOT NULL,
    file_hash   TEXT NOT NULL,
    indexed_at  TEXT NOT NULL,
    PRIMARY KEY (project_id, file_path),
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
);

-- A row means call resolution completed successfully for this file, even when
-- the file has no outgoing calls. Edge provenance alone cannot represent leaf
-- files and must never be used as a project-wide precision proxy.
CREATE TABLE IF NOT EXISTS call_graph_coverage (
    project_id  INTEGER NOT NULL,
    file_path   TEXT NOT NULL,
    resolver    TEXT NOT NULL,
    resolved_at TEXT NOT NULL,
    PRIMARY KEY (project_id, file_path),
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_call_graph_coverage_project
    ON call_graph_coverage(project_id);

-- User-attached knowledge: notes + external data (DB rows, findings) pinned to a
-- symbol ('node') or a call path ('path'). Keyed by project, NOT by node row id,
-- so annotations survive reindex (which only wipes nodes/edges).
CREATE TABLE IF NOT EXISTS annotations (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id  INTEGER NOT NULL,
    kind        TEXT NOT NULL,
    target      TEXT NOT NULL,
    source      TEXT NOT NULL DEFAULT 'note',
    external_id TEXT,
    note        TEXT,
    data        TEXT,
    created_at  TEXT NOT NULL,
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_annotations_target  ON annotations(project_id, kind, target);
CREATE INDEX IF NOT EXISTS idx_annotations_project ON annotations(project_id);
`
