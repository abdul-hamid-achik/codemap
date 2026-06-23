# codemap — Code Knowledge Graph

> Local-first code intelligence that combines structural graph extraction (LSP + Tree-sitter)
> with semantic vector search (veclite) to give AI agents structural awareness of codebases.
> Exposed as an MCP server. Built on Abdul's existing tool ecosystem.

## The Problem

AI coding agents explore codebases through repeated file-reading and grep-searching,
consuming thousands of tokens per query without structural understanding. They can ask
vecgrep "find code like the auth handler" (semantic) but can't ask "who calls this function
and what tests cover it" (structural). They can ask LSP "find references" for one symbol
right now but can't ask "across all projects, what's the blast radius of changing this type"
or combine semantic similarity with structural traversal.

The gap is that **nobody combines structural code graph data with semantic vector search
and exposes both as a unified query layer for agents**.

## What Exists Already (Research)

### SCIP (Sourcegraph Code Intelligence Protocol)
Language-agnostic protobuf format for indexing source code. Powers Sourcegraph's precise
code navigation. Has indexers for Java, TypeScript, Go, Rust, Python, Ruby, C/C++, C#,
Dart, PHP. Used by CKB (Code Knowledge Backend) and GitLab code intelligence.
- Strength: Precise, language-agnostic, well-specified
- Weakness: Requires per-language SCIP indexers (heavyweight), no semantic search, no
  graph traversal queries, designed for navigation not agent queries
- GitHub: https://github.com/scip-code/scip (662 stars, Apache-2.0)

### Serena (oraios/serena)
MCP toolkit providing semantic code retrieval/editing using LSP as backend. 25.7k stars.
Supports 40+ languages via language servers. Exposes symbol lookup, find references,
find symbol, symbol overview, replace symbol body, safe delete as MCP tools.
- Strength: Real-time LSP accuracy, wide language support, agent-first tool design
- Weakness: Requires live LSP server running (not offline), no persistent graph,
  no semantic vector search, no cross-project queries, no blast radius computation
- GitHub: https://github.com/oraios/serena

### Codebase-Memory (arXiv:2603.27277v1, March 2026)
Tree-sitter-based knowledge graph exposed via MCP. Parses 66 languages, stores graph in
SQLite, 14 MCP tools. Reports 83% answer quality vs 92% file-exploration agent at 10x fewer
tokens and 2.1x fewer tool calls. Graph-native queries (hub detection, caller ranking)
match or exceed explorer on 19/31 languages.
- Key finding: Precompute structure → expose as tools → let agents query narrow facts
  instead of burning context on broad file reads
- Weakness: Tree-sitter only (no LSP precision), no semantic vector search, Python-based

### CKB (nyxCore-Systems/ckb)
Go-based code intelligence server. SCIP + LSP + tree-sitter backends. 107 MCP tools.
SQLite + FTS5 + semantic search (LIP v2.0 with TurboQuant embeddings). Compound operations
(explore, understand, prepareChange, batchGet). 83% token reduction via presets.
- Strength: Most feature-complete existing solution, Go-based, multi-backend
- Weakness: Closed-source-ish (commercial license for >$25k revenue), depends on external
  SCIP indexers, doesn't use a real vector DB (custom LIP instead), no graph database
  (SQLite tables only, no graph traversal engine)

### LogicLens (arXiv:2601.10773v1, January 2026)
Semantic code graph for multi-repository systems. Three phases: structural graph (Tree-sitter),
node enrichment (LLM summaries), entity nodes (domain concepts). ReAct agent + GraphRAG.
- Key insight: Domain entity nodes serve as hubs linking projects, enabling cross-repo
  workflow queries
- Weakness: Requires LLM enrichment at index time (expensive, non-deterministic), Python,
  uses Neo4j (heavyweight)

### Kuzu (embedded graph DB)
Lightweight graph database with full-text + vector indexes. Could serve as graph+vector
storage in one. But adds a new dependency; veclite + SQLite is more aligned with the
existing ecosystem.

## Design Principles

1. **Local-first** — everything runs on the machine, no cloud, no uploads
2. **Reuse existing infrastructure** — veclite for vectors, SQLite for graph, Ollama for
   embeddings, modelcontextprotocol/go-sdk for MCP server
3. **Agent-first** — MCP tools return narrow structured facts, not raw file dumps
4. **Incremental** — hash-based reindex (like vecgrep), not full rebuild every time
5. **Multi-backend extraction** — LSP for precision when available, Tree-sitter for speed
   and language coverage, SCIP as optional high-precision import
6. **Cross-project** — graph spans multiple registered projects, not just one repo
7. **Offline-capable** — indexed graph is queryable without LSP servers running
8. **Composable** — integrates with vecgrep (shared project registry), noted (notes about
   code), hunk (review context), tinyvault (credentials for remote LSP)

## Architecture

```
                    ┌─────────────────────────────────┐
                    │          Extraction Layer        │
                    │                                  │
                    │  ┌─────────┐  ┌──────────────┐   │
                    │  │ LSP     │  │ Tree-sitter  │   │
                    │  │ (gopls, │  │ (64+ langs)  │   │
                    │  │  ts_ls, │  │              │   │
                    │  │  lua_ls)│  │              │   │
                    │  └────┬────┘  └──────┬───────┘   │
                    │       │              │           │
                    │       ▼              ▼           │
                    │  ┌──────────────────────────┐    │
                    │  │   Structural Extractor   │    │
                    │  │  (unifies LSP + tree-sit) │    │
                    │  └────────────┬─────────────┘    │
                    └────────────────┼─────────────────┘
                                     │
                    ┌────────────────┼─────────────────┐
                    │           Storage Layer            │
                    │                │                   │
                    │   ┌────────────▼────────────┐     │
                    │   │       codemap index      │     │
                    │   │  ┌─────────┐ ┌────────┐ │     │
                    │   │  │ SQLite  │ │veclite │ │     │
                    │   │  │ (graph: │ │(vector:│ │     │
                    │   │  │ nodes,  │ │ code   │ │     │
                    │   │  │ edges,  │ │ embeds │ │     │
                    │   │  │ symbols)│ │ per    │ │     │
                    │   │  │         │ │ node)  │ │     │
                    │   │  └─────────┘ └────────┘ │     │
                    │   └─────────────────────────┘     │
                    └────────────────┼───────────────────┘
                                     │
                    ┌────────────────┼───────────────────┐
                    │           Query Layer              │
                    │                │                   │
                    │   ┌────────────▼────────────┐     │
                    │   │   codemap query engine   │     │
                    │   │  (graph traversal +     │     │
                    │   │   vector search +       │     │
                    │   │   hybrid fusion)        │     │
                    │   └────────────┬────────────┘     │
                    └────────────────┼───────────────────┘
                                     │
                    ┌────────────────┼───────────────────┐
                    │           Agent Layer              │
                    │                │                   │
                    │   ┌────────────▼────────────┐     │
                    │   │   codemap MCP server    │     │
                    │   │   (stdio, like vecgrep)  │     │
                    │   └─────────────────────────┘     │
                    └────────────────────────────────────┘
```

## Storage Schema

### SQLite (modernc.org/sqlite — pure Go, no CGO)

**nodes** — every code entity is a node:
```sql
CREATE TABLE nodes (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id  INTEGER NOT NULL,
    file_path   TEXT NOT NULL,        -- relative to project root
    symbol      TEXT,                  -- symbol name (may be empty for file nodes)
    fqn         TEXT,                  -- fully qualified name (cross-project unique)
    kind        TEXT NOT NULL,         -- file, function, class, method, type, variable, test, module
    language    TEXT NOT NULL,         -- go, typescript, lua, python, etc.
    start_line  INTEGER NOT NULL,
    end_line    INTEGER NOT NULL,
    signature   TEXT,                  -- function signature / type definition
    docstring   TEXT,                   -- comment/doc above the symbol
    source_hash TEXT NOT NULL,         -- sha256 of source text (for incremental reindex)
    vec_id      TEXT,                   -- veclite record ID (for semantic search)
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL,
    FOREIGN KEY (project_id) REFERENCES projects(id)
);

CREATE INDEX idx_nodes_project ON nodes(project_id);
CREATE INDEX idx_nodes_file ON nodes(project_id, file_path);
CREATE INDEX idx_nodes_fqn ON nodes(fqn);
CREATE INDEX idx_nodes_symbol ON nodes(symbol);
CREATE INDEX idx_nodes_kind ON nodes(kind);
```

**edges** — relationships between nodes:
```sql
CREATE TABLE edges (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    source_id   INTEGER NOT NULL,
    target_id   INTEGER NOT NULL,
    edge_type   TEXT NOT NULL,         -- calls, imports, implements, references, depends_on, tests, overrides, defines
    weight      REAL DEFAULT 1.0,      -- confidence/strength (LSP=1.0, tree-sitter=0.7)
    created_at  TEXT NOT NULL,
    FOREIGN KEY (source_id) REFERENCES nodes(id),
    FOREIGN KEY (target_id) REFERENCES nodes(id)
);

CREATE INDEX idx_edges_source ON edges(source_id, edge_type);
CREATE INDEX idx_edges_target ON edges(target_id, edge_type);
CREATE INDEX idx_edges_type ON edges(edge_type);
```

**projects** — registered projects (shared registry concept with vecgrep):
```sql
CREATE TABLE projects (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT UNIQUE NOT NULL,
    path        TEXT NOT NULL,         -- absolute path to project root
    language    TEXT,                  -- primary language
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);
```

**index_state** — incremental reindex tracking:
```sql
CREATE TABLE index_state (
    project_id  INTEGER NOT NULL,
    file_path   TEXT NOT NULL,
    file_hash   TEXT NOT NULL,
    indexed_at  TEXT NOT NULL,
    PRIMARY KEY (project_id, file_path)
);
```

### veclite — semantic vector store

One collection per project (or one global collection with project metadata in payloads).
Each node's source text is embedded via Ollama (nomic-embed-text, 768 dims) and stored in
veclite. The `vec_id` column in SQLite links back to the veclite record.

```go
// veclite collection: "codemap"
// Each record:
//   - Vector: embedding of the node's source text (function body, class definition, etc.)
//   - Metadata: { node_id, project, file, symbol, kind, language, start_line, end_line }
//   - Text: the source text (for BM25 keyword search via veclite's hybrid search)
```

This enables:
- `graph_semantic("JWT validation")` → veclite returns nearest nodes by meaning
- `graph_semantic("error handling without checking")` → veclite hybrid search (vector + BM25)
- Each result includes the SQLite node_id for immediate graph traversal

## Extraction Layer

### Strategy: Dual-backend with graceful degradation

```
For each file in project:
  1. If LSP server is available for this language AND project is open in LSP:
       → Use LSP for precise extraction (definitions, references, call hierarchy, type hierarchy)
       → Weight edges at 1.0 (high confidence)
  2. Else if tree-sitter grammar is available:
       → Use tree-sitter for structural extraction (function defs, class defs, imports, calls)
       → Weight edges at 0.7 (medium confidence — tree-sitter can't resolve cross-file refs)
  3. Else:
       → Skip (or do naive regex-based extraction as last resort)
```

### LSP Extraction (high precision)

Languages with LSP servers Abdul already has configured:
- **Go** — gopls (already enabled in nvim)
- **TypeScript/JavaScript** — ts_ls (already enabled)
- **Lua** — lua_ls (already enabled)
- **Python** — pyright (already enabled)
- **Ruby** — ruby_lsp (already enabled)
- **Templ** — templ (already enabled)
- **Vue** — vue_ls (already enabled)
- **HTML** — html (already enabled)
- **CSS/SCSS** — cssls (already enabled)
- **Markdown** — marksman (already enabled)

LSP queries used:
- `textDocument/documentSymbol` — all symbols in a file (functions, classes, types, vars)
- `textDocument/definition` — where is this symbol defined?
- `textDocument/references` — who references this symbol?
- `textDocument/callHierarchy/incomingCalls` — who calls this function?
- `textDocument/callHierarchy/outgoingCalls` — what does this function call?
- `textDocument/typeDefinition` — what type is this?
- `textDocument/implementation` — what implements this interface?
- `textDocument/hover` — type info, signature, documentation

LSP extraction runs headless: start the language server as a subprocess, open the project,
send queries, collect results, shut down. This is the same pattern Serena uses.

### Tree-sitter Extraction (broad coverage)

For languages without LSP, or as a faster first pass:
- Parse each file with tree-sitter grammar
- Extract: function definitions, class definitions, type definitions, import statements,
  call expressions, variable declarations
- Tree-sitter can identify syntactic structure but can't resolve cross-file references
  (it doesn't know if `foo()` in file A calls the `foo` defined in file B)
- Edges from tree-sitter are marked with weight=0.7

Tree-sitter Go bindings: `github.com/smacker/go-tree-sitter` or `github.com/tree-sitter/go-tree-sitter`

### SCIP Import (optional high-precision)

If a project has been indexed with SCIP (e.g., via scip-typescript, scip-python), codemap
can import the SCIP index directly. This gives the highest precision (comparable to LSP)
without needing a live LSP server. SCIP indexers are run as build-time steps, not at query
time.

This is optional — codemap works fine with just LSP + tree-sitter. SCIP is for projects
that already have SCIP indexing set up (or want the extra precision for large monorepos).

### Node Identification

Cross-file and cross-project matching requires stable identifiers:

- **Go**: FQN = `package.FunctionName` or `package.TypeName.MethodName`
- **TypeScript**: FQN = `module/ClassName.methodName` (resolved via import paths)
- **Python**: FQN = `module.ClassName.method_name`
- **Lua**: FQN = `module.function_name` or `module.table.function_name`
- **Tree-sitter only**: Use `file_path:symbol_name` as a weaker identifier (may cause
  false matches across files with same-named symbols — acceptable with weight=0.7)

## Query Layer

### Graph Traversal Queries (SQLite)

```
graph_callers(symbol)        — incoming calls (edges where type=calls AND target=node)
graph_callees(symbol)        — outgoing calls (edges where type=calls AND source=node)
graph_references(symbol)     — all references (edges where type=references)
graph_imports(file)          — dependency graph (edges where type=imports)
graph_implementations(iface) — implementations (edges where type=implements)
graph_overrides(method)      — overridden methods (edges where type=overrides)
graph_test_coverage(symbol)  — tests that call this code (edges where type=tests)
graph_blast_radius(symbol)  — transitive callers up to N hops (BFS on calls edges)
graph_path(from, to)         — shortest call path between two symbols
graph_hotspots()             — nodes with highest incoming edge count (hub detection)
graph_orphans()              — nodes with zero incoming edges (dead code detection)
```

### Semantic Queries (veclite)

```
graph_semantic(query)                — veclite hybrid search (vector + BM25)
graph_similar_nodes(node_id)         — find structurally+semantically similar code
graph_semantic_callers(query, depth) — semantic search then expand N hops up the call graph
graph_semantic_callees(query, depth) — semantic search then expand N hops down the call graph
```

The killer query: `graph_semantic_callers("authentication middleware", 2)`:
1. veclite finds nodes semantically matching "authentication middleware"
2. SQLite graph traversal expands 2 hops up the call graph from those nodes
3. Returns: "Here's auth-like code, and here's everything that calls into it (2 levels up)"

### Hybrid Fusion Queries

```
graph_impact_analysis(symbol)  — combine blast radius + test coverage:
  1. Find transitive callers (blast radius)
  2. For each caller, check if any test calls it (test coverage)
  3. Return: affected code + which tests cover it + which is untested

graph_refactor_plan(symbol)    — combine semantics + structure:
  1. Find semantically similar code (may need same refactor)
  2. Find all callers (what breaks)
  3. Find all tests (what to update)
  4. Return: complete refactor impact assessment
```

## MCP Server

### Tools (initial set — 15 tools)

```
# Project management
codemap_init        — register/activate a project (like vecgrep_init)
codemap_index       — index or reindex a project (incremental, hash-based)
codemap_status      — index statistics (nodes, edges, coverage, languages)

# Graph traversal
codemap_callers     — who calls this symbol?
codemap_callees     — what does this symbol call?
codemap_references   — all references to this symbol
codemap_blast_radius — transitive callers up to N hops
codemap_test_coverage — which tests cover this code path?
codemap_path         — call path between two symbols

# Semantic
codemap_semantic     — semantic search across the graph
codemap_similar      — find similar code to a given symbol or text

# Hybrid
codemap_impact       — impact analysis (blast radius + test coverage + untested paths)
codemap_search       — combined semantic + structural search with filters

# Info
codemap_symbols      — list symbols in a file (like LSP documentSymbol)
codemap_dependencies — dependency graph for a file or module
```

### MCP Tool Example: codemap_impact

```json
// Request
{
  "symbol": "authenticateUser",
  "project": "blankcode",
  "depth": 3
}

// Response
{
  "symbol": "authenticateUser",
  "file": "apps/api/src/modules/auth/auth.service.ts",
  "lines": "45-82",
  "callers": [
    { "symbol": "AuthController.login", "file": "...", "lines": "12-30", "depth": 1 },
    { "symbol": "AuthMiddleware.handle", "file": "...", "lines": "5-15", "depth": 1 },
    { "symbol": "AuthGuard.canActivate", "file": "...", "lines": "8-20", "depth": 2 },
    { "symbol": "RequestHandler", "file": "...", "lines": "100-120", "depth": 3 }
  ],
  "tests": [
    { "symbol": "auth.service.test.ts > login success", "file": "...", "covers": "AuthController.login" },
    { "symbol": "auth.e2e.test.ts > valid credentials", "file": "...", "covers": "RequestHandler" }
  ],
  "untested_callers": [
    { "symbol": "AuthMiddleware.handle", "reason": "no test calls this path" }
  ],
  "semantically_similar": [
    { "symbol": "verifyToken", "file": "...", "similarity": 0.89 },
    { "symbol": "validateSession", "file": "...", "similarity": 0.82 }
  ]
}
```

This single response replaces what would take an agent 10-15 file reads + grep searches.

## Ecosystem Integration

### veclite (direct dependency)
- codemap imports `github.com/abdul-hamid-achik/veclite` as a Go module
- Uses veclite's `Open()`, `CreateCollection()`, `Insert()`, `Search()`, `HybridSearch()`
- The veclite collection stores node embeddings with metadata linking back to SQLite node IDs
- File lock behavior inherited from veclite (same pattern as vecgrep — one process owns the DB)

### vecgrep (sibling — shared registry)
- codemap and vecgrep share the project registry format (`~/.codemap/projects/` or
  `~/.vecgrep/projects/` — TBD, possibly a unified `~/.codemap/` location)
- codemap can use vecgrep's chunker (`internal/index/chunker.go`) for consistent code splitting
- An agent can combine both: vecgrep for "find similar code" and codemap for "what calls it"
- Future: codemap could embed vecgrep as a library for its semantic search component

### noted (notes about code)
- An agent using codemap can create noted entries about code structure discovered
- Example: "This module has a circular dependency: A→B→C→A" (detected via graph_path)
- noted's semantic search (also veclite-backed) can find notes about specific code regions

### hunk (review context)
- When an agent makes changes, codemap's `codemap_impact` output can be attached as
  `--agent-context` JSON sidecar to hunk review sessions
- The review sees: the diff (hunk) + what's affected (codemap) = complete review context

### tinyvault (credentials)
- If LSP servers need credentials (e.g., private package registries), codemap can pull
  them from tinyvault via `vault_run_with_secrets` pattern

### Hermes / MCP clients
- codemap registers as an MCP server in Hermes config.yaml:
  ```yaml
  codemap:
    command: codemap
    args:
    - serve
    - --mcp
    enabled: true
  ```
- Also installable in Copilot, Codex, OpenCode, Claude Code, Forge, Coder, local-agent,
  vecai — same pattern as hunk-review skill installation

### vecai / local-agent (agent consumers)
- vecai can use codemap as an MCP server for structural queries
- local-agent can connect to codemap's MCP server alongside vecgrep and noted
- Both agents gain structural awareness without changing their agent loops

## Project Structure

```
codemap/
├── cmd/codemap/              # CLI entrypoint (Cobra, same as vecgrep/noted)
│   └── main.go
├── internal/
│   ├── config/               # Hierarchical config (same pattern as vecgrep)
│   │   ├── config.go
│   │   └── resolution.go
│   ├── graph/                # SQLite graph store
│   │   ├── store.go          # Open/Close, CRUD for nodes/edges
│   │   ├── schema.go          # SQL schema + migrations
│   │   ├── queries.go        # Graph traversal queries (BFS, DFS, shortest path)
│   │   └── graph_test.go
│   ├── extract/              # Code structure extraction
│   │   ├── extractor.go       # Interface: Extractor interface
│   │   ├── lsp/               # LSP-based extraction
│   │   │   ├── client.go      # Headless LSP client (start server, send queries)
│   │   │   ├── gopls.go       # Go-specific LSP config
│   │   │   ├── tsserver.go    # TypeScript LSP config
│   │   │   └── extract.go     # Unified LSP extraction logic
│   │   ├── treesitter/        # Tree-sitter-based extraction
│   │   │   ├── parser.go      # Tree-sitter parser setup
│   │   │   ├── queries.go     # Language-specific tree-sitter queries
│   │   │   └── extract.go     # Structural extraction from AST
│   │   ├── scip/              # Optional SCIP index import
│   │   │   └── import.go      # Parse SCIP protobuf, populate graph
│   │   └── unified.go         # Merge LSP + tree-sitter results, deduplicate
│   ├── embed/                 # Embedding provider (reuse vecgrep pattern)
│   │   ├── provider.go        # Interface
│   │   └── ollama.go          # Ollama embedding (nomic-embed-text)
│   ├── index/                 # Indexing engine
│   │   ├── indexer.go         # Walk files, extract, embed, store
│   │   ├── incremental.go     # Hash-based incremental reindex
│   │   └── chunker.go         # Code chunker (adapted from vecgrep)
│   ├── search/                # Query engine
│   │   ├── semantic.go        # veclite-backed semantic search
│   │   ├── structural.go      # SQLite-backed graph traversal
│   │   ├── hybrid.go          # Combined semantic + structural queries
│   │   └── impact.go          # Impact analysis (blast radius + tests)
│   ├── mcp/                   # MCP server
│   │   └── server.go          # MCP tool definitions + handlers
│   ├── app/                   # Shared CLI/MCP service layer
│   │   └── service.go
│   └── version/
│       └── version.go
├── go.mod
├── go.sum
├── Taskfile.yml               # task doctor, task setup, task build, task test
├── .golangci.yml
├── .goreleaser.yaml
├── README.md
├── AGENTS.md
├── CLAUDE.md
├── glyphrun.config.yml        # E2E behavior specs (like vecgrep/noted)
└── docs/                      # VitePress docs (if needed)
```

## Dependencies

```
# Go module
go 1.25+

# Direct dependencies
github.com/modelcontextprotocol/go-sdk   # MCP server (same as vecgrep)
github.com/abdul-hamid-achik/veclite     # Vector database (our own)
modernc.org/sqlite                       # Pure-Go SQLite (same as noted)
github.com/spf13/cobra                    # CLI framework (same as vecgrep/noted)
github.com/smacker/go-tree-sitter         # Tree-sitter bindings (or tree-sitter/go-tree-sitter)

# LSP communication
github.com/sourcegraph/jsonrpc2           # JSON-RPC for LSP protocol (or go.lsp.dev/jsonrpc2)

# Optional (SCIP import)
github.com/scip-code/scip/bindings/go     # SCIP protobuf bindings

# Dev tools
air                                      # Hot reload
golangci-lint                            # Linting
glyph                                    # E2E specs (glyphrun)
task                                     # Task runner
```

## Implementation Phases

### Phase 1: Foundation (MVP)
- Project structure + go.mod + Taskfile
- SQLite schema + graph store (nodes, edges, projects, index_state)
- Tree-sitter extraction for Go + TypeScript (the two main languages in Abdul's projects)
- Ollama embedding integration (reuse vecgrep's embed package pattern)
- veclite collection for semantic search
- Basic CLI: `codemap init`, `codemap index`, `codemap status`
- Basic MCP server with 5 tools: init, index, status, semantic, callers

### Phase 2: LSP Integration
- Headless LSP client (start gopls/ts_ls as subprocess, JSON-RPC communication)
- LSP extraction: documentSymbol, definition, references, callHierarchy
- Unified extractor (merge LSP precision + tree-sitter coverage)
- More MCP tools: callees, references, blast_radius, test_coverage
- Incremental reindexing (hash-based, like vecgrep)

### Phase 3: Hybrid Queries
- `codemap_impact` — blast radius + test coverage fusion
- `codemap_semantic_callers` — semantic search + graph expansion
- `codemap_refactor_plan` — complete refactor impact assessment
- `codemap_path` — shortest call path between two symbols
- `codemap_hotspots` / `codemap_orphans` — graph analytics

### Phase 4: Polish & Distribution
- SCIP import support (optional high-precision backend)
- More tree-sitter languages (Lua, Python, Ruby, Vue, Templ, Markdown)
- Glyphrun E2E specs (like vecgrep/noted/teak)
- Homebrew tap distribution
- VitePress documentation site (codemap.dev — pending domain)
- Cross-project queries (query the graph across multiple registered projects)

### Phase 5: Deep Integration
- Hermes MCP config integration
- vecai MCP auto-detection (like it detects vecgrep)
- Hunk `--agent-context` JSON sidecar generation from codemap queries
- Noted integration (create notes about graph findings: circular deps, dead code, etc.)
- Shared project registry with vecgrep (unified `~/.codemap/` or `~/.agents/` location)

## Edge Cases & Pitfalls

### Multi-process contention
Same issue as vecgrep: if multiple MCP clients spawn codemap servers, they fight over
the veclite lock. Solutions:
- Same as vecgrep: one process owns the DB, others fail gracefully
- Better: codemap serve --mcp should NOT open the DB at startup (lazy open on first query)
- Best: shared daemon mode (like hunk daemon) — one codemap process serves multiple clients

### LSP server lifecycle
- LSP servers are slow to start (gopls: 2-5s, ts_ls: 3-10s for large projects)
- Keep LSP server alive between extractions (don't start/stop per file)
- Timeout on LSP startup (if project doesn't compile, LSP may hang)
- LSP may produce stale data if files change during extraction — lock the working tree

### Tree-sitter vs LSP conflicts
- If both extract the same file, deduplicate by FQN
- LSP edges take precedence (weight=1.0 over tree-sitter's 0.7)
- Tree-sitter may produce false edges (same-named functions in different files)
- FQN matching resolves most conflicts; residual false positives are acceptable at weight=0.7

### Large monorepos
- Indexing 50k+ files takes time — parallel extraction (worker pool, like Codebase-Memory)
- SQLite WAL mode for concurrent read during indexing
- Batch inserts (1000-row transactions, like CKB)
- Skip files >1MB, skip generated files (*.min.js, *.gen.go, node_modules, vendor)

### Circular dependencies
- `graph_path` must detect cycles (don't infinite loop)
- BFS with visited set
- Report cycles as a special result type ("circular: A→B→C→A")

## Naming

- Project: `codemap`
- Binary: `codemap`
- MCP server: `codemap serve --mcp`
- Go module: `github.com/abdul-hamid-achik/codemap`
- Homebrew: `abdul-hamid-achik/tap/codemap`
- Domain (pending): `codemap.dev`
- Data dir: `~/.codemap/` (projects, config — parallel to `~/.vecgrep/`)

## Comparison with Existing Tools

| Feature | codemap | Serena | CKB | Codebase-Memory | vecgrep |
|---|---|---|---|---|---|
| Structural graph | ✅ (SQLite) | ❌ (live LSP only) | ✅ (SQLite) | ✅ (SQLite) | ❌ |
| Semantic search | ✅ (veclite) | ❌ | ✅ (LIP) | ❌ | ✅ (veclite) |
| Offline queries | ✅ | ❌ (needs live LSP) | ✅ | ✅ | ✅ |
| Cross-project | ✅ | ❌ (per-project) | ❌ | ❌ | ✅ (global registry) |
| MCP server | ✅ | ✅ | ✅ | ✅ | ✅ |
| Language support | LSP (10+) + tree-sitter (64+) | LSP (40+) | SCIP (10) + LSP + tree-sitter | tree-sitter (66) | regex-based |
| Local-first | ✅ | ✅ | ✅ | ✅ | ✅ |
| Impact analysis | ✅ | ❌ | ✅ | ✅ | ❌ |
| Test coverage | ✅ | ❌ | ❌ | ❌ | ❌ |
| Incremental index | ✅ (hash-based) | ❌ (live) | ✅ | ❌ | ✅ |
| Built on own infra | veclite + SQLite | — | SQLite + custom LIP | SQLite | veclite |
| License | MIT | MIT | Commercial | Open source | MIT |

## What Makes codemap Different

1. **Built on veclite** — uses a real vector database (not a custom embedding solution).
   veclite is already battle-tested in vecgrep, noted, and vidtrace.

2. **Dual extraction** — LSP for precision where available, tree-sitter for broad coverage.
   Not either/or like Serena (LSP only) or Codebase-Memory (tree-sitter only).

3. **Cross-project graph** — the graph spans all registered projects. An agent can ask
   "which of my projects have a function called authenticateUser and what calls them"
   — no existing tool does this.

4. **Semantic + structural fusion** — the killer queries combine veclite semantic search
   with SQLite graph traversal. "Find auth-like code, then show me what calls it."
   Neither Serena, CKB, nor Codebase-Memory combine a real vector DB with graph traversal.

5. **Ecosystem-native** — integrates with vecgrep (shared project registry), noted (code
   notes), hunk (review context), tinyvault (LSP credentials), Hermes (MCP config).
   Every tool in Abdul's ecosystem benefits.

## Next Steps

1. Scaffold the project (`mkdir -p ~/projects/codemap`, init go.mod, Taskfile)
2. Implement SQLite schema + graph store (Phase 1)
3. Implement tree-sitter extraction for Go (simplest LSP language to test against)
4. Implement veclite integration for semantic search
5. Build the MCP server with 5 initial tools
6. Test on blankcode (NestJS + Nuxt 3 monorepo — good complexity for validation)
7. Add LSP extraction (gopls first, then ts_ls)
8. Iterate on hybrid queries (codemap_impact is the flagship)