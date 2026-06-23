package graph

// schemaVersion is bumped whenever schemaSQL changes in a way that requires a
// migration. The current version is stored in SQLite's PRAGMA user_version.
const schemaVersion = 1

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

CREATE TABLE IF NOT EXISTS edges (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    source_id   INTEGER NOT NULL,
    target_id   INTEGER NOT NULL,
    edge_type   TEXT NOT NULL,
    weight      REAL NOT NULL DEFAULT 1.0,
    created_at  TEXT NOT NULL,
    FOREIGN KEY (source_id) REFERENCES nodes(id) ON DELETE CASCADE,
    FOREIGN KEY (target_id) REFERENCES nodes(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_edges_source ON edges(source_id, edge_type);
CREATE INDEX IF NOT EXISTS idx_edges_target ON edges(target_id, edge_type);
CREATE INDEX IF NOT EXISTS idx_edges_type   ON edges(edge_type);

CREATE TABLE IF NOT EXISTS index_state (
    project_id  INTEGER NOT NULL,
    file_path   TEXT NOT NULL,
    file_hash   TEXT NOT NULL,
    indexed_at  TEXT NOT NULL,
    PRIMARY KEY (project_id, file_path),
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
);
`
