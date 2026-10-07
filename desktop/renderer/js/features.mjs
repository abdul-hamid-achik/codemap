/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// THE feature registry: one entry per codemap capability, mirroring the CLI
// surface exactly (commands, nested subcommands, and every documented flag).
// Views are generated from this table, so a feature added here is immediately
// runnable, reviewable in raw JSON, and reachable from the command palette.
//
// Field kinds:
//   pos     positional argument            {name,label,placeholder,required,multi}
//   text    --flag <value>
//   num     --flag <n>                     {def,min,max,step}
//   bool    --flag                         (present when checked)
//   select  --flag <one-of>                {options:[{v,label}]}
//   csv     --flag a,b,c                   (cobra `strings` flag)
//   repeat  --flag a --flag b              (cobra `stringArray`)
//   path    --flag <file>                  (opens a save/load dialog)

export const GROUPS = [
  { id: 'health', label: 'Status & config', icon: '◉' },
  { id: 'index', label: 'Index & freshness', icon: '⛁' },
  { id: 'search', label: 'Search & discovery', icon: '⌕' },
  { id: 'symbol', label: 'Symbols & source', icon: '§' },
  { id: 'graph', label: 'Graph relations', icon: '⇄' },
  { id: 'impact', label: 'Impact, risk & review', icon: '◎' },
  { id: 'secrets', label: 'Secrets & keys', icon: '⚿' },
  { id: 'knowledge', label: 'Knowledge & agents', icon: '✎' },
  { id: 'ops', label: 'Branches, cache & daemon', icon: '⌸' },
  { id: 'mcp', label: 'MCP & integrations', icon: '⌁' },
]

const AT = {
  kind: 'text',
  flag: '--at',
  name: 'at',
  label: 'At <file>:<line>',
  placeholder: 'internal/app/review.go:123',
  hint: 'Select one exact definition by source position instead of merging every symbol that shares the name.',
}
const DEPTH3 = { kind: 'num', flag: '--depth', name: 'depth', label: 'Depth', def: 3, min: 1, max: 10, hint: 'Max hops for the blast radius.' }
const TOP = (def, max) => ({ kind: 'num', flag: '--top', name: 'top', label: 'Top', def, min: 1, max, hint: 'Maximum results returned.' })
const SYMBOL_POS = (required = false) => ({
  kind: 'pos',
  name: 'symbol',
  label: 'Symbol',
  placeholder: 'app.Service.Review',
  required,
  hint: 'Name or fully-qualified name. Bare names merge every definition that shares them — use --at for one exact definition.',
})
const FILE_POS = (required = true) => ({
  kind: 'pos',
  name: 'file',
  label: 'File',
  placeholder: 'internal/app/review.go',
  required,
  hint: 'Project-relative path.',
})
const PRECISE = {
  kind: 'bool',
  flag: '--precise',
  name: 'precise',
  label: 'Precise (language server)',
  hint: 'Ask the language server (gopls / typescript-language-server / pyright) for exact relations instead of the stored name-based graph. Slower, but no same-name over-matching.',
}

export const FEATURES = [
  // ------------------------------------------------------------------ health
  {
    id: 'dashboard',
    group: 'health',
    title: 'Dashboard',
    blurb: 'Index health, staleness, language mix, hubs, orphans and one-click next steps.',
    view: 'dashboard',
    run: false,
    mcp: null,
  },
  {
    id: 'status',
    group: 'health',
    title: 'Index status',
    blurb: 'Nodes, edges, files, languages, kinds, precise coverage and working-tree drift.',
    cmd: ['status'],
    args: [
      { kind: 'bool', flag: '--full', name: 'full', label: 'Full (count vectors)', hint: 'Includes the local vector-store count; may use substantial memory.' },
      { kind: 'bool', flag: '--skip-stale', name: 'skip_stale', label: 'Skip staleness walk', hint: 'Faster readiness probe: do not hash the working tree.' },
    ],
    render: 'status',
    mcp: 'codemap_status',
  },
  {
    id: 'doctor',
    group: 'health',
    title: 'Doctor',
    blurb: 'Toolchains, language servers and embeddings — what is ready and what each missing piece disables.',
    cmd: ['doctor'],
    args: [{ kind: 'pos', name: 'path', label: 'Path', placeholder: '(project root)', required: false, hint: 'Language-server probes run with this directory as cwd so asdf/mise/nvm pins match.' }],
    render: 'doctor',
    mcp: 'codemap_doctor',
  },
  {
    id: 'projects',
    group: 'health',
    title: 'Projects',
    blurb: 'Every project registered with codemap and its index size.',
    cmd: ['projects'],
    args: [],
    render: 'projects',
    mcp: 'codemap_projects',
  },
  {
    id: 'config-show',
    group: 'health',
    title: 'Resolved config',
    blurb: 'The effective configuration after file, env and flag precedence.',
    cmd: ['config', 'show'],
    args: [],
    render: 'config',
    mcp: null,
  },
  {
    id: 'config-path',
    group: 'health',
    title: 'Config path',
    blurb: 'Which config file codemap resolved (global or CODEMAP_CONFIG override).',
    cmd: ['config', 'path'],
    args: [],
    mcp: null,
  },
  {
    id: 'version',
    group: 'health',
    title: 'Version',
    blurb: 'codemap version, commit and build date.',
    cmd: ['version'],
    args: [],
    json: false,
    mcp: null,
  },

  // ------------------------------------------------------------------- index
  {
    id: 'init',
    group: 'index',
    title: 'Init project',
    blurb: 'Register the current directory as a codemap project.',
    cmd: ['init'],
    args: [{ kind: 'bool', flag: '--local', name: 'local', label: '--local marker', hint: 'Drop a .codemap marker so a repo-local codemap.yaml is found from subdirectories. The index stays central.' }],
    mutating: true,
    mcp: 'codemap_init',
  },
  {
    id: 'index',
    group: 'index',
    title: 'Index',
    blurb: 'Extract the graph and embed nodes (incremental, hash-based).',
    cmd: ['index'],
    args: [
      { kind: 'pos', name: 'path', label: 'Path', placeholder: '(current project)', required: false },
      { kind: 'bool', flag: '--reindex', name: 'reindex', label: 'Full reindex', hint: 'Wipe and rebuild the whole project index.' },
      { kind: 'bool', flag: '--precise', name: 'precise', label: 'Precise call edges', hint: 'Resolve call edges exactly (Go via go/types; TS/JS/Python via callHierarchy). Gives the LSP languages a real call graph.' },
      { kind: 'bool', flag: '--no-embed', name: 'no_embed', label: 'Skip embeddings', hint: 'Index structure only — no Ollama calls, no vectors.' },
      { kind: 'bool', flag: '--no-lsp', name: 'no_lsp', label: 'Skip language servers' },
      { kind: 'bool', flag: '--no-tips', name: 'no_tips', label: 'Suppress tips', def: true },
      { kind: 'bool', flag: '--cache', name: 'cache', label: 'Use fcheap cache', def: true, hint: 'Auto-restore before --reindex, auto-save after indexing.' },
      { kind: 'bool', flag: '--watch', name: 'watch', label: 'Then watch (daemon)', hint: 'After indexing, start the daemon to keep the index fresh.' },
      { kind: 'csv', flag: '--exclude', name: 'exclude', label: 'Exclude (replaces defaults)', placeholder: '.git,node_modules', hint: 'Path globs to skip, REPLACING the built-in defaults.' },
      { kind: 'csv', flag: '--exclude-extra', name: 'exclude_extra', label: 'Exclude extra', placeholder: 'migrations,**/testdata', hint: 'Extra globs appended to the configured excludes.' },
      { kind: 'csv', flag: '--force-extra', name: 'force_extra', label: 'Force re-extract', placeholder: 'internal/extract/**', hint: 'Re-extract these globs even when the content hash is unchanged (recovery from a degraded language-server run).' },
      { kind: 'num', flag: '--max-file-bytes', name: 'max_file_bytes', label: 'Max file bytes', def: 1048576, min: 0 },
      { kind: 'num', flag: '--embed-batch-size', name: 'embed_batch_size', label: 'Embed batch size', def: 64, min: 1 },
      { kind: 'num', flag: '--embed-concurrency', name: 'embed_concurrency', label: 'Embed concurrency', def: 4, min: 1 },
      { kind: 'num', flag: '--embed-max-chars', name: 'embed_max_chars', label: 'Embed max chars', def: 0, min: 0, hint: 'Cap per-node embed text (0 = no cap).' },
      { kind: 'text', flag: '--via-vault', name: 'via_vault', label: 'Via tvault', placeholder: 'tvault run -p myproj', hint: 'Re-run indexing inside tvault so registry creds reach the language servers.' },
    ],
    render: 'index',
    mutating: true,
    stream: true,
    long: true,
    mcp: 'codemap_index',
  },
  {
    id: 'index-precise',
    group: 'index',
    title: 'Index --precise',
    blurb: 'The exact-resolution pass: Go via go/types, TS/JS/Python via callHierarchy.',
    cmd: ['index', '--precise', '--no-tips'],
    args: [
      { kind: 'pos', name: 'path', label: 'Path', placeholder: '(current project)', required: false },
      { kind: 'bool', flag: '--reindex', name: 'reindex', label: 'Full reindex' },
      { kind: 'bool', flag: '--no-embed', name: 'no_embed', label: 'Skip embeddings' },
    ],
    render: 'index',
    mutating: true,
    stream: true,
    long: true,
    preset: { precise: true },
    mcp: 'codemap_index',
  },
  {
    id: 'coverage',
    group: 'index',
    title: 'Precise coverage',
    blurb: 'Per-file precise call-graph coverage rolled up by language and directory.',
    cmd: ['coverage'],
    args: [
      { kind: 'bool', flag: '--files', name: 'files', label: 'Include per-file list' },
      { kind: 'bool', flag: '--uncovered', name: 'uncovered', label: 'Uncovered only' },
      { kind: 'text', flag: '--lang', name: 'lang', label: 'Language', placeholder: 'go | typescript | python' },
      { kind: 'text', flag: '--prefix', name: 'prefix', label: 'Path prefix', placeholder: 'internal/' },
      TOP(200, 2000),
    ],
    render: 'coverage',
    mcp: 'codemap_coverage',
  },
  {
    id: 'inconsistencies',
    group: 'index',
    title: 'Inconsistencies',
    blurb: 'Where the compiled knowledge contradicts itself or the working tree.',
    doc: 'Reports dangling annotations, name-based call edges surviving on precise-resolved files, and coverage rows for files with no indexed nodes. An empty report is evidence of internal coherence, not of correctness.',
    cmd: ['inconsistencies'],
    args: [],
    render: 'inconsistencies',
    mcp: null,
  },
  {
    id: 'structural-manifest',
    group: 'index',
    title: 'Structural manifest',
    blurb: 'Lightweight identity + freshness preflight for consumers of export-symbols.',
    cmd: ['structural-manifest'],
    args: [],
    mcp: null,
  },
  {
    id: 'export-symbols',
    group: 'index',
    title: 'Export symbols',
    blurb: 'Deterministic paginated structural feed (codemap.structural-export.v1/v2) for sibling tools.',
    cmd: ['export-symbols'],
    args: [
      { kind: 'num', flag: '--offset', name: 'offset', label: 'Offset', def: 0, min: 0 },
      { kind: 'num', flag: '--limit', name: 'limit', label: 'Limit', def: 100, min: 1, max: 5000 },
      { kind: 'num', flag: '--max-content-bytes', name: 'max_content_bytes', label: 'Max content bytes', def: 16384, min: 0, max: 262144 },
      { kind: 'csv', flag: '--files', name: 'files', label: 'Files (v2 filtered export)', placeholder: 'internal/app/review.go' },
      { kind: 'path', flag: '--files-from', name: 'files_from', label: 'Files from (list file)', mode: 'open' },
    ],
    render: 'export',
    mcp: null,
  },

  // ------------------------------------------------------------------ search
  {
    id: 'find',
    group: 'search',
    title: 'Find (name)',
    blurb: 'Find symbols by name — fast, offline, no embeddings needed.',
    cmd: ['find'],
    args: [{ kind: 'pos', name: 'query', label: 'Query', placeholder: 'Handler', required: true }, TOP(50, 500)],
    render: 'search',
    primary: 'query',
    mcp: 'codemap_find',
  },
  {
    id: 'grep',
    group: 'search',
    title: 'Grep (indexed text)',
    blurb: 'Exact text search over indexed file content, joined onto each hit’s enclosing symbol.',
    cmd: ['grep'],
    args: [
      { kind: 'pos', name: 'pattern', label: 'Pattern', placeholder: 'TODO(security)', required: true },
      { kind: 'bool', flag: '--regex', name: 'regex', label: 'Regex (RE2)', hint: 'Interpret the pattern as a Go RE2 regular expression instead of a literal substring.' },
      { kind: 'bool', flag: '-i', name: 'ignore_case', label: 'Ignore case' },
      TOP(100, 1000),
    ],
    render: 'grep',
    primary: 'pattern',
    mcp: 'codemap_grep',
  },
  {
    id: 'semantic',
    group: 'search',
    title: 'Semantic search',
    blurb: 'Search the code graph by meaning (vector + BM25 hybrid).',
    cmd: ['semantic'],
    args: [
      { kind: 'pos', name: 'query', label: 'Query', placeholder: 'authentication logic', required: true },
      TOP(10, 100),
      {
        kind: 'select',
        flag: '--backend',
        name: 'backend',
        label: 'Backend',
        options: [
          { v: '', label: '(config default)' },
          { v: 'fallback', label: 'fallback — local, then vecgrep' },
          { v: 'local', label: 'local — veclite only' },
          { v: 'vecgrep', label: 'vecgrep — delegated owner' },
        ],
      },
      {
        kind: 'select',
        flag: '--fusion',
        name: 'fusion',
        label: 'Fusion',
        options: [
          { v: '', label: '(config default)' },
          { v: 'auto', label: 'auto — classify query shape' },
          { v: 'balanced', label: 'balanced — equal weights' },
        ],
      },
    ],
    render: 'search',
    primary: 'query',
    mcp: 'codemap_semantic',
  },
  {
    id: 'explore',
    group: 'search',
    title: 'Explore (intent)',
    blurb: 'Turn an intent query into durable selectors plus bounded exact structural neighborhoods.',
    cmd: ['explore'],
    args: [
      { kind: 'pos', name: 'query', label: 'Intent', placeholder: 'how does review map a diff to symbols', required: true },
      { kind: 'num', flag: '--seeds', name: 'seeds', label: 'Seeds', def: 5, min: 1, max: 10 },
      { kind: 'num', flag: '--edges', name: 'edges', label: 'Edges per seed', def: 5, min: 1, max: 20 },
      { kind: 'num', flag: '--depth', name: 'depth', label: 'Blast depth', def: 2, min: 1, max: 10 },
    ],
    render: 'explore',
    primary: 'query',
    mcp: 'codemap_explore',
  },
  {
    id: 'read-order',
    group: 'search',
    title: 'Read order',
    blurb: 'Where to start reading: ranked entrypoints and load-bearing hubs.',
    cmd: ['read-order'],
    args: [{ kind: 'pos', name: 'query', label: 'Filter (name/path)', placeholder: '(everything)', required: false }, TOP(20, 200)],
    render: 'readorder',
    primary: 'query',
    mcp: 'codemap_read_order',
  },
  {
    id: 'atlas',
    group: 'search',
    title: 'Atlas report',
    blurb: 'A hierarchical map of the codebase: directories and files with size, language, role, coupling, plain-language summaries and key symbols.',
    cmd: ['atlas'],
    args: [
      { kind: 'text', flag: '--prefix', name: 'prefix', label: 'Prefix (directory)', placeholder: '(project root)', hint: 'Zoom into a project-relative directory.' },
      { kind: 'num', flag: '--depth', name: 'depth', label: 'Depth', def: 2, min: 1, max: 8, hint: 'Directory levels below the prefix to expand.' },
      { kind: 'bool', flag: '--files', name: 'files', label: 'Include files', hint: 'Add file leaves to every expanded directory.' },
      { kind: 'num', flag: '--max-nodes', name: 'max_nodes', label: 'Max nodes', def: 1500, min: 1, max: 20000, hint: 'Maximum tree nodes to emit.' },
      { kind: 'num', flag: '--key-symbols', name: 'key_symbols', label: 'Key symbols', def: 5, min: 0, max: 20, hint: 'Key symbols per directory/file.' },
    ],
    render: 'atlas',
    view: 'atlas',
    primary: 'prefix',
    mcp: 'codemap_atlas',
  },
  {
    id: 'features',
    group: 'search',
    title: 'Feature inventory',
    blurb: 'What the software can do: CLI commands, RPC/MCP tools, HTTP routes, pages and programs, each with its handler, description and footprint.',
    cmd: ['features'],
    args: [
      { kind: 'csv', flag: '--kind', name: 'kind', label: 'Kinds', placeholder: 'cli_command,rpc_tool', hint: 'Only these kinds: program, cli_command, rpc_tool, http_route, api_route, page.' },
      { kind: 'text', flag: '--query', name: 'query', label: 'Query', placeholder: 'index', hint: 'Case-insensitive substring over label, description, handler and file.' },
      TOP(200, 2000),
      { kind: 'bool', flag: '--no-footprint', name: 'no_footprint', label: 'Skip footprints', hint: 'Faster: do not walk each feature’s call tree.' },
      { kind: 'num', flag: '--depth', name: 'depth', label: 'Footprint depth', def: 3, min: 1, max: 6, hint: 'Footprint call-walk depth.' },
    ],
    render: 'features',
    view: 'features',
    primary: 'query',
    mcp: 'codemap_features',
  },
  {
    id: 'processes',
    group: 'search',
    title: 'Processes',
    blurb: 'Execution flows from every entry point: route or command, handler, then the service chain it reaches, in call order.',
    cmd: ['processes'],
    args: [
      { kind: 'csv', flag: '--kind', name: 'kind', label: 'Kinds', placeholder: 'http_route,cli_command', hint: 'Only these entry kinds: program, cli_command, rpc_tool, http_route, api_route, page.' },
      { kind: 'text', flag: '--query', name: 'query', label: 'Query', placeholder: 'how does signup work', hint: 'Keep processes whose name or steps match these content words.' },
      TOP(50, 200),
      { kind: 'num', flag: '--depth', name: 'depth', label: 'Depth', def: 4, min: 1, max: 8, hint: 'Maximum call depth per process.' },
      { kind: 'num', flag: '--max-steps', name: 'max_steps', label: 'Max steps', def: 40, min: 1, max: 200, hint: 'Maximum steps per process.' },
    ],
    render: 'processes',
    primary: 'query',
    mcp: 'codemap_processes',
  },
  {
    id: 'flow',
    group: 'search',
    title: 'Call flow',
    blurb: 'How one entry point works end to end: its call tree in call order, with docs, subsystems, confidence and ambiguity collapse.',
    cmd: ['flow'],
    args: [
      SYMBOL_POS(false),
      AT,
      { kind: 'num', flag: '--depth', name: 'depth', label: 'Depth', def: 4, min: 1, max: 8, hint: 'Maximum call depth.' },
      { kind: 'num', flag: '--max-nodes', name: 'max_nodes', label: 'Max nodes', def: 120, min: 1, max: 1000, hint: 'Maximum steps to emit.' },
      { kind: 'bool', flag: '--include-tests', name: 'include_tests', label: 'Include tests', hint: 'Include test functions and test-file helpers.' },
    ],
    render: 'flow',
    view: 'flow',
    primary: 'symbol',
    graph: true,
    mcp: 'codemap_flow',
  },
  {
    id: 'map',
    group: 'search',
    title: 'Architecture map',
    blurb: 'Bounded overview: subsystems, directed cross-subsystem bridges, hubs, entrypoints.',
    cmd: ['map'],
    args: [
      { kind: 'num', flag: '--top-subsystems', name: 'top_subsystems', label: 'Subsystems', def: 50, min: 1, max: 500 },
      { kind: 'num', flag: '--top-bridges', name: 'top_bridges', label: 'Bridges', def: 100, min: 1, max: 1000 },
      { kind: 'num', flag: '--top-hubs', name: 'top_hubs', label: 'Hubs', def: 20, min: 1, max: 200 },
      { kind: 'num', flag: '--top-entrypoints', name: 'top_entrypoints', label: 'Entrypoints', def: 10, min: 1, max: 200 },
    ],
    render: 'map',
    view: 'map',
    mcp: 'codemap_map',
  },
  {
    id: 'hotspots',
    group: 'search',
    title: 'Hotspots',
    blurb: 'The most-referenced symbols (hubs), with same-name inflation flagged.',
    cmd: ['hotspots'],
    args: [TOP(20, 200)],
    render: 'symlist',
    mcp: 'codemap_hotspots',
  },
  {
    id: 'orphans',
    group: 'search',
    title: 'Orphans',
    blurb: 'Functions and methods with no callers — dead-code candidates, never proof.',
    cmd: ['orphans'],
    args: [TOP(50, 500)],
    render: 'symlist',
    mcp: 'codemap_orphans',
  },

  // ------------------------------------------------------------------ symbol
  {
    id: 'symbols',
    group: 'symbol',
    title: 'Symbols in file',
    blurb: 'Every symbol a file defines: functions, types, methods, tests, sections, keys.',
    cmd: ['symbols'],
    args: [FILE_POS()],
    render: 'symlist',
    primary: 'file',
    mcp: 'codemap_symbols',
  },
  {
    id: 'symbol-at',
    group: 'symbol',
    title: 'Symbol at position',
    blurb: 'Resolve file:line positions to their enclosing symbol (FQN, kind, range).',
    cmd: ['symbol-at'],
    args: [{ kind: 'pos', name: 'positions', label: 'file:line', placeholder: 'internal/app/review.go:123', required: true, multi: true, hint: 'Repeatable — one argument per position.' }],
    render: 'symlist',
    primary: 'at',
    mcp: 'codemap_symbol_at',
  },
  {
    id: 'source',
    group: 'symbol',
    title: 'Source',
    blurb: 'The body behind a symbol’s signature.',
    cmd: ['source'],
    args: [
      SYMBOL_POS(),
      AT,
      { kind: 'bool', flag: '--brief', name: 'brief', label: 'Brief (no body)', hint: 'Keep signature/doc/location and drop the source body (source_omitted:true).' },
    ],
    render: 'source',
    primary: 'symbol',
    graph: true,
    mcp: 'codemap_source',
  },
  {
    id: 'context',
    group: 'symbol',
    title: 'Context',
    blurb: 'Everything about a symbol in one call: definition, callers, callees, references, covering tests, blast radius.',
    cmd: ['context'],
    args: [
      { kind: 'pos', name: 'symbols', label: 'Symbol(s)', placeholder: 'Review', required: false, multi: true, hint: 'Pass several to batch them with shared callers.' },
      { kind: 'repeat', flag: '--at', name: 'at', label: 'At <file>:<line>', placeholder: 'internal/app/review.go:123', hint: 'Repeatable. Pass several to batch exact definitions.' },
      { kind: 'bool', flag: '--brief', name: 'brief', label: 'Brief (no bodies)' },
      DEPTH3,
    ],
    render: 'context',
    primary: 'symbol',
    graph: true,
    mcp: 'codemap_context',
  },
  {
    id: 'context-batch',
    group: 'symbol',
    title: 'Context batch',
    blurb: 'Bounded multi-symbol context with shared callers — the batch planner.',
    cmd: ['context'],
    args: [
      { kind: 'pos', name: 'symbols', label: 'Symbols (several)', placeholder: 'Review Impact Service', required: true, multi: true },
      { kind: 'bool', flag: '--brief', name: 'brief', label: 'Brief (no bodies)' },
      DEPTH3,
    ],
    render: 'context',
    primary: 'symbol',
    mcp: 'codemap_context_batch',
  },
  {
    id: 'task-context',
    group: 'symbol',
    title: 'Task context',
    blurb: 'One-call, mode-scoped orientation for a task (understand | change | debug).',
    doc: 'The task text is used verbatim as the retrieval query — codemap never interprets intent. Every section keeps its own caps and honesty signals; staleness is reported, never acted on.',
    cmd: ['task-context'],
    args: [
      { kind: 'pos', name: 'task', label: 'Task', placeholder: 'make review fail closed on stale indexes', required: true },
      {
        kind: 'select',
        flag: '--mode',
        name: 'mode',
        label: 'Mode',
        def: 'understand',
        options: [
          { v: 'understand', label: 'understand — freshness + explore neighborhoods' },
          { v: 'change', label: 'change — contexts + impact drill-downs + related files' },
          { v: 'debug', label: 'debug — explore + caller/callee-emphasized contexts' },
        ],
      },
      { kind: 'repeat', flag: '--at', name: 'at', label: 'At <file>:<line>', placeholder: 'internal/app/review.go:123', hint: 'Up to 25; requires --mode change or debug.' },
    ],
    render: 'taskcontext',
    primary: 'task',
    mcp: 'codemap_task_context',
  },

  // ------------------------------------------------------------------- graph
  {
    id: 'callers',
    group: 'graph',
    title: 'Callers',
    blurb: 'Functions and methods that call a symbol.',
    cmd: ['callers'],
    args: [SYMBOL_POS(), AT, PRECISE],
    render: 'relation',
    primary: 'symbol',
    graph: true,
    mcp: 'codemap_callers',
  },
  {
    id: 'callees',
    group: 'graph',
    title: 'Callees',
    blurb: 'Functions and methods a symbol calls.',
    cmd: ['callees'],
    args: [SYMBOL_POS(), AT, PRECISE],
    render: 'relation',
    primary: 'symbol',
    graph: true,
    mcp: 'codemap_callees',
  },
  {
    id: 'references',
    group: 'graph',
    title: 'Value references',
    blurb: 'Enclosing scopes that use a function or method as a value (callback / registration wiring).',
    doc: 'Follows value-reference edges, not calls. Each result is the enclosing function, method or file scope — not an exact expression line. An empty result does not prove there is no dynamic wiring.',
    cmd: ['references'],
    args: [SYMBOL_POS(), AT],
    render: 'references',
    primary: 'symbol',
    graph: true,
    mcp: 'codemap_references',
  },
  {
    id: 'path',
    group: 'graph',
    title: 'Call path',
    blurb: 'The shortest indexed call path between two symbols.',
    cmd: ['path'],
    args: [
      { kind: 'pos', name: 'from', label: 'From', placeholder: 'app.Main', required: true },
      { kind: 'pos', name: 'to', label: 'To', placeholder: 'app.Store.Save', required: true },
    ],
    render: 'path',
    graph: true,
    mcp: 'codemap_path',
  },
  {
    id: 'traverse',
    group: 'graph',
    title: 'Traverse',
    blurb: 'Walk typed graph relations from one exact source definition, with per-edge confidence.',
    cmd: ['traverse'],
    args: [
      { kind: 'text', flag: '--at', name: 'at', label: 'At <file>:<line>', placeholder: 'internal/app/review.go:123', required: true },
      {
        kind: 'select',
        flag: '--direction',
        name: 'direction',
        label: 'Direction',
        def: 'both',
        options: [
          { v: 'both', label: 'both' },
          { v: 'outgoing', label: 'outgoing' },
          { v: 'incoming', label: 'incoming' },
        ],
      },
      { kind: 'csv', flag: '--edge-types', name: 'edge_types', label: 'Edge types', placeholder: 'calls,references,imports', hint: 'CSV of relation domains to walk.' },
      { kind: 'num', flag: '--depth', name: 'depth', label: 'Depth', def: 2, min: 1, max: 6 },
      { kind: 'num', flag: '--limit', name: 'limit', label: 'Node limit', def: 60, min: 1, max: 500 },
    ],
    render: 'traverse',
    primary: 'at',
    graph: true,
    mcp: 'codemap_traverse',
  },
  {
    id: 'dependencies',
    group: 'graph',
    title: 'File dependencies',
    blurb: 'Inbound call, reference and import evidence for a file, with per-domain coverage.',
    cmd: ['dependencies'],
    args: [FILE_POS()],
    render: 'dependencies',
    primary: 'file',
    mcp: 'codemap_dependencies',
  },
  {
    id: 'related-files',
    group: 'graph',
    title: 'Related files',
    blurb: 'Files related to a file through the call/test graph.',
    cmd: ['related-files'],
    args: [FILE_POS()],
    render: 'related',
    primary: 'file',
    mcp: 'codemap_related_files',
  },

  // ------------------------------------------------------------------ impact
  {
    id: 'review',
    group: 'impact',
    title: 'Review',
    blurb: 'Diff-scoped impact + test selection: what your changes affect, and which tests to run.',
    doc: 'Folds one aggregate risk band from every changed symbol so a harness can gate on a single call. analysis_complete and the symbol counts expose staleness and the 200-symbol work cap.',
    cmd: ['review'],
    args: [
      { kind: 'bool', flag: '--staged', name: 'staged', label: 'Staged only', hint: 'Review the git index instead of the whole working tree.' },
      { kind: 'text', flag: '--since', name: 'since', label: 'Since ref', placeholder: 'main', hint: 'Review everything changed since this git ref (committed + uncommitted).' },
      DEPTH3,
      {
        kind: 'select',
        flag: '--fail-on-risk',
        name: 'fail_on_risk',
        label: 'Gate: fail on risk',
        options: [
          { v: '', label: '(no gate)' },
          { v: 'low', label: 'low or above' },
          { v: 'medium', label: 'medium or above' },
          { v: 'high', label: 'high' },
        ],
        hint: 'Exit 6 when the aggregate risk level reaches the threshold. “unknown” never trips it.',
      },
      { kind: 'bool', flag: '--fail-on-untested', name: 'fail_on_untested', label: 'Gate: fail on untested', hint: 'Exit 6 when any changed symbol has no covering test.' },
    ],
    render: 'review',
    view: 'review',
    mutating: false,
    mcp: 'codemap_review',
  },
  {
    id: 'affected',
    group: 'impact',
    title: 'Affected tests',
    blurb: 'Changed files → the test files to run: covering tests, test files that import them, and changed tests.',
    doc: 'With no files and no source flag it uses the working tree. Each test carries its reasons (covers:<symbol>, imports:<file>, changed); unmapped lists changed files the index could not map; call_graph and analysis_complete say how far to trust the selection. The CLI also reads paths from --stdin (not available in the app).',
    cmd: ['affected'],
    args: [
      { kind: 'pos', name: 'files', label: 'Changed files', placeholder: 'internal/app/review.go', multi: true, hint: 'Project-relative paths, one argument each. Leave empty to use the git diff.' },
      { kind: 'bool', flag: '--staged', name: 'staged', label: 'Staged only', hint: 'Use the git index instead of the whole working tree.' },
      { kind: 'text', flag: '--since', name: 'since', label: 'Since ref', placeholder: 'main', hint: 'Use every file changed since this git ref (committed + uncommitted).' },
      { kind: 'text', flag: '--filter', name: 'filter', label: 'Filter glob', placeholder: '*_test.go', hint: 'Only report test files matching this glob.' },
      { kind: 'num', flag: '--depth', name: 'depth', label: 'Depth', def: 3, min: 1, max: 10, hint: 'Max hops for the call-graph and import walks.' },
    ],
    render: 'affected',
    mutating: false,
    mcp: 'codemap_affected',
  },
  {
    id: 'impact',
    group: 'impact',
    title: 'Impact',
    blurb: 'Blast radius (transitive callers) plus covering tests for a symbol.',
    cmd: ['impact'],
    args: [
      SYMBOL_POS(),
      { kind: 'repeat', flag: '--at', name: 'at', label: 'At <file>:<line>', placeholder: 'internal/app/review.go:123', hint: 'Repeatable — pass several to batch impact across frames.' },
      { kind: 'bool', flag: '--batch', name: 'batch', label: 'Always batch envelope', hint: 'Return the stable batch frame envelope even for one --at.' },
      DEPTH3,
      { kind: 'repeat', flag: '--selector', name: 'selector', label: 'Selector JSON', placeholder: '{"file":"…","fqn":"…","kind":"method"}', hint: 'Durable source selector; survives reindex better than a line number.' },
    ],
    render: 'impact',
    primary: 'symbol',
    graph: true,
    mcp: 'codemap_impact',
  },
  {
    id: 'file-impact',
    group: 'impact',
    title: 'File impact',
    blurb: 'Who depends on this file, its blast radius, and whether it is safe to change or delete.',
    cmd: ['file-impact'],
    args: [FILE_POS(), DEPTH3],
    render: 'fileimpact',
    primary: 'file',
    mcp: 'codemap_file_impact',
  },
  {
    id: 'file-context',
    group: 'impact',
    title: 'File context',
    blurb: 'Orient on a file in one call: symbols, file-level impact, related files.',
    cmd: ['file-context'],
    args: [FILE_POS(), DEPTH3],
    render: 'filecontext',
    primary: 'file',
    mcp: 'codemap_file_context',
  },
  {
    id: 'risk',
    group: 'impact',
    title: 'Risk',
    blurb: 'Change-risk score: untested + fan-in + cross-package spread + ambiguity in one number.',
    cmd: ['risk'],
    args: [
      SYMBOL_POS(),
      AT,
      DEPTH3,
      {
        kind: 'select',
        flag: '--fail-on-risk',
        name: 'fail_on_risk',
        label: 'Gate: fail on risk',
        options: [
          { v: '', label: '(no gate)' },
          { v: 'low', label: 'low or above' },
          { v: 'medium', label: 'medium or above' },
          { v: 'high', label: 'high' },
        ],
      },
    ],
    render: 'risk',
    primary: 'symbol',
    mcp: 'codemap_risk',
  },
  {
    id: 'refactor-plan',
    group: 'impact',
    title: 'Refactor plan',
    blurb: 'Plan a rename/move: call sites, value references, dependent files, covering tests, blast radius.',
    cmd: ['refactor-plan'],
    args: [SYMBOL_POS(), AT, DEPTH3],
    render: 'refactor',
    primary: 'symbol',
    mcp: 'codemap_refactor_plan',
  },

  // ----------------------------------------------------------------- secrets
  {
    id: 'secret-impact',
    group: 'secrets',
    title: 'Secret impact',
    blurb: 'Code blast radius of rotating secret key names — value-free, names only.',
    cmd: ['secret-impact'],
    args: [
      { kind: 'pos', name: 'keys', label: 'Key names', placeholder: 'STRIPE_KEY,DATABASE_URL', required: false, multi: true, hint: 'One argument per key name. Never values.' },
      { kind: 'num', flag: '--depth', name: 'depth', label: 'Depth', def: 3, min: 1, max: 10 },
      { kind: 'text', flag: '--via-vault', name: 'via_vault', label: 'Via tvault', placeholder: 'tvault -p myproj list', hint: 'Fetch key NAMES from tvault instead of passing them.' },
      { kind: 'text', flag: '--prefix', name: 'prefix', label: 'Prefix filter', placeholder: 'STRIPE_' },
    ],
    render: 'secrets',
    mcp: 'codemap_secret_impact',
  },
  {
    id: 'required-keys',
    group: 'secrets',
    title: 'Required keys',
    blurb: 'Least-privilege key set an entrypoint’s call tree actually reads (for tvault seal/export).',
    cmd: ['required-keys'],
    args: [
      { kind: 'pos', name: 'entrypoint', label: 'Entrypoint', placeholder: 'main.main', required: true },
      { kind: 'csv', flag: '--keys', name: 'keys', label: 'Candidate keys', placeholder: 'STRIPE_KEY,DATABASE_URL' },
      { kind: 'num', flag: '--depth', name: 'depth', label: 'Callee depth', def: 5, min: 1, max: 10 },
      { kind: 'text', flag: '--via-vault', name: 'via_vault', label: 'Via tvault', placeholder: 'tvault -p myproj list' },
      { kind: 'text', flag: '--prefix', name: 'prefix', label: 'Prefix filter', placeholder: 'STRIPE_' },
    ],
    render: 'secrets',
    mcp: 'codemap_required_keys',
  },

  // --------------------------------------------------------------- knowledge
  {
    id: 'annotations',
    group: 'knowledge',
    title: 'Annotations',
    blurb: 'List pinned notes and external data — all, for a symbol, or for a from→to path.',
    cmd: ['annotations'],
    args: [
      { kind: 'pos', name: 'target', label: 'Symbol (or from)', placeholder: '(all)', required: false },
      { kind: 'pos', name: 'to', label: 'To (for a path)', placeholder: '', required: false },
      { kind: 'num', flag: '--rm', name: 'rm', label: 'Remove id', min: 1, hint: 'Remove the annotation with this id instead of listing.' },
    ],
    render: 'annotations',
    mutating: true,
    mcp: 'codemap_annotations',
  },
  {
    id: 'annotate',
    group: 'knowledge',
    title: 'Annotate',
    blurb: 'Attach a note and/or external data to a symbol or a call path; --retarget repoints after a rename.',
    cmd: ['annotate'],
    args: [
      { kind: 'pos', name: 'target', label: 'Symbol (or from)', placeholder: 'app.Service.Review', required: true, hint: 'Pass two positionals (from, to) to annotate a call path instead.' },
      { kind: 'pos', name: 'to', label: 'To (path annotation)', placeholder: '', required: false },
      { kind: 'text', flag: '--note', name: 'note', label: 'Note', placeholder: 'load-bearing: gates every release', multiline: true },
      { kind: 'text', flag: '--data', name: 'data', label: 'Data payload', placeholder: '{"table":"users"}', multiline: true, hint: 'Opaque payload, e.g. JSON from a DB query.' },
      {
        kind: 'select',
        flag: '--source',
        name: 'source',
        label: 'Source',
        def: 'note',
        options: ['note', 'vecgrep', 'tinyvault', 'fcheap', 'vidtrace', 'cairntrace', 'glyphrun', 'mongosh', 'postgres'].map((v) => ({ v, label: v })),
      },
      { kind: 'text', flag: '--external-id', name: 'external_id', label: 'External id', placeholder: 'jira-1234', hint: 'Caller-owned idempotency key, unique within project + source.' },
      { kind: 'num', flag: '--retarget', name: 'retarget', label: 'Retarget id', min: 1, hint: 'Repoint this annotation id at the target above instead of attaching a new one.' },
    ],
    render: 'annotations',
    mutating: true,
    confirm: 'This writes to the annotation store.',
    mcp: 'codemap_annotate',
  },
  {
    id: 'docs',
    group: 'knowledge',
    title: 'Agent guide',
    blurb: 'The canonical in-band guide to codemap (overview, workflow, commands, annotations, accuracy, ecosystem).',
    cmd: ['docs'],
    args: [
      {
        kind: 'select',
        flag: null,
        name: 'topic',
        label: 'Topic',
        pos: true,
        options: ['', 'overview', 'workflow', 'commands', 'annotations', 'accuracy', 'ecosystem', 'formats'].map((v) => ({ v, label: v || '(all)' })),
      },
    ],
    render: 'docs',
    json: false,
    mcp: 'codemap_docs',
  },
  {
    id: 'agent-list',
    group: 'knowledge',
    title: 'Harness list',
    blurb: 'Known AI coding harnesses, whether each is detected here, and whether codemap is registered.',
    cmd: ['agent', 'list'],
    args: [],
    render: 'agents',
    mcp: null,
  },
  {
    id: 'agent-playbook',
    group: 'knowledge',
    title: 'Playbook',
    blurb: 'The canonical “when to use codemap” playbook, in the format a harness expects.',
    cmd: ['agent', 'playbook'],
    args: [
      {
        kind: 'select',
        flag: '--format',
        name: 'format',
        label: 'Format',
        def: 'markdown',
        options: ['markdown', 'markdown-cli', 'claude-skill', 'cursor-rule'].map((v) => ({ v, label: v })),
      },
    ],
    render: 'docs',
    json: false,
    mcp: null,
  },
  {
    id: 'agent-setup',
    group: 'knowledge',
    title: 'Register with harness',
    blurb: 'Wire codemap (MCP server + playbook) into a harness’ config and guidance files.',
    cmd: ['agent', 'setup'],
    args: [
      {
        kind: 'select',
        flag: null,
        name: 'harness',
        label: 'Harness',
        pos: true,
        required: true,
        options: ['claude-code', 'cursor', 'codex', 'gemini', 'cline', 'zed', 'vscode', 'opencode', 'aider'].map((v) => ({ v, label: v })),
      },
      { kind: 'bool', flag: '--dry-run', name: 'dry_run', label: 'Dry run', def: true, hint: 'Print every planned write, change nothing.' },
      { kind: 'bool', flag: '--global', name: 'global', label: 'User-level config', hint: 'Write user-level config where the harness has one (default: project-scoped files).' },
      { kind: 'bool', flag: '--no-playbook', name: 'no_playbook', label: 'MCP server only', hint: 'Skip the guidance file.' },
    ],
    mutating: true,
    confirm: 'This writes config and guidance files for the selected harness. Use --dry-run first.',
    render: 'agents',
    mcp: null,
  },

  // --------------------------------------------------------------------- ops
  {
    id: 'branch-status',
    group: 'ops',
    title: 'Branch status',
    blurb: 'The git branch/commit state used to key per-branch index snapshots (read-only).',
    cmd: ['branch-status'],
    args: [{ kind: 'pos', name: 'path', label: 'Path', placeholder: '(project root)', required: false }],
    mcp: 'codemap_branch_status',
  },
  {
    id: 'branch-switch',
    group: 'ops',
    title: 'Branch switch',
    blurb: 'Switch the code index to a git branch: snapshot the old, restore or reindex the new.',
    cmd: ['branch-switch'],
    args: [
      { kind: 'text', flag: '--to', name: 'to', label: 'To branch', placeholder: '(current git branch)' },
      { kind: 'text', flag: '--from', name: 'from', label: 'From branch', placeholder: '(last active branch)' },
      { kind: 'text', flag: '--root', name: 'root', label: 'Repo root', placeholder: '(cwd)' },
      { kind: 'bool', flag: '--install-hook', name: 'install_hook', label: 'Install post-checkout hook', hint: 'Auto-switch the index on every branch checkout.' },
    ],
    mutating: true,
    confirm: 'Snapshots the current index and switches it to another branch.',
    mcp: 'codemap_branch_switch',
  },
  {
    id: 'branch-snapshot',
    group: 'ops',
    title: 'Branch snapshot',
    blurb: 'Stash the current branch’s index into fcheap so it can be restored on switch-back.',
    cmd: ['branch-snapshot'],
    args: [
      { kind: 'text', flag: '--branch', name: 'branch', label: 'Branch', placeholder: '(current git branch)' },
      { kind: 'text', flag: '--root', name: 'root', label: 'Repo root', placeholder: '(cwd)' },
    ],
    mutating: true,
    mcp: null,
  },
  {
    id: 'cache-list',
    group: 'ops',
    title: 'Cache list',
    blurb: 'Cached index snapshots for this repo, keyed by working-tree hash.',
    cmd: ['cache', 'list'],
    args: [{ kind: 'bool', flag: '--rebuild', name: 'rebuild', label: 'Rebuild from fcheap', hint: 'Reconstruct the list if the local pointer file was lost.' }],
    render: 'cache',
    mcp: 'codemap_cache_list',
  },
  {
    id: 'cache-save',
    group: 'ops',
    title: 'Cache save',
    blurb: 'Stash the current index into the fcheap content-addressed cache.',
    cmd: ['cache', 'save'],
    args: [],
    mutating: true,
    mcp: 'codemap_cache_save',
  },
  {
    id: 'cache-restore',
    group: 'ops',
    title: 'Cache restore',
    blurb: 'Restore a cached index matching the current working tree.',
    cmd: ['cache', 'restore'],
    args: [],
    mutating: true,
    mcp: 'codemap_cache_restore',
  },
  {
    id: 'cache-drop',
    group: 'ops',
    title: 'Cache drop',
    blurb: 'Drop one cached index (by tree hash) or all of them for this repo.',
    cmd: ['cache', 'drop'],
    args: [
      { kind: 'text', flag: '--tree', name: 'tree', label: 'Tree hash', placeholder: '(from cache list)' },
      { kind: 'bool', flag: '--all', name: 'all', label: 'Drop ALL for this repo' },
    ],
    mutating: true,
    confirm: 'Drops cached index data from the fcheap vault.',
    mcp: 'codemap_cache_drop',
  },
  {
    id: 'cache-export',
    group: 'ops',
    title: 'Cache export',
    blurb: 'Export the current index to a portable, team/CI-shareable tar.gz.',
    cmd: ['cache', 'export'],
    args: [{ kind: 'pos', name: 'file', label: 'Archive', placeholder: 'codemap-index.tar.gz', required: true, mode: 'save' }],
    mutating: true,
    mcp: null,
  },
  {
    id: 'cache-import',
    group: 'ops',
    title: 'Cache import',
    blurb: 'Import a portable index tarball — the fresh-checkout CI case, no re-indexing.',
    cmd: ['cache', 'import'],
    args: [
      { kind: 'pos', name: 'file', label: 'Archive', placeholder: 'codemap-index.tar.gz', required: true, mode: 'open' },
      { kind: 'bool', flag: '--force', name: 'force', label: 'Force (tree hash mismatch)', hint: 'Import even when the archive’s tree hash does not match the working tree.' },
    ],
    mutating: true,
    confirm: 'Replaces this project’s index with the contents of the archive.',
    mcp: null,
  },
  {
    id: 'daemon-status',
    group: 'ops',
    title: 'Daemon status',
    blurb: 'Whether the background indexer is running and what it watches.',
    cmd: ['daemon', 'status'],
    args: [],
    render: 'daemon',
    mcp: null,
  },
  {
    id: 'daemon-start',
    group: 'ops',
    title: 'Daemon start',
    blurb: 'Watch the working tree and keep the index fresh (runs in the foreground here).',
    cmd: ['daemon', 'start'],
    args: [
      { kind: 'text', flag: '--debounce', name: 'debounce', label: 'Debounce', placeholder: '500ms' },
      { kind: 'text', flag: '--idle-timeout', name: 'idle_timeout', label: 'Idle timeout', placeholder: '0 (never)' },
      { kind: 'bool', flag: '--precise', name: 'precise', label: 'Keep precise edges current' },
      { kind: 'bool', flag: '--no-embed', name: 'no_embed', label: 'Structure only' },
      { kind: 'csv', flag: '--exclude-extra', name: 'exclude_extra', label: 'Exclude extra' },
      { kind: 'num', flag: '--embed-cache-size', name: 'embed_cache_size', label: 'Embed cache size', def: 4096, min: 0 },
      { kind: 'num', flag: '--embed-max-in-flight', name: 'embed_max_in_flight', label: 'Embed in flight', def: 2, min: 1 },
      { kind: 'num', flag: '--embed-rps', name: 'embed_rps', label: 'Embed req/s', def: 0, min: 0, hint: '0 = unlimited.' },
    ],
    mode: 'daemon',
    mutating: true,
    stream: true,
    mcp: null,
  },
  {
    id: 'daemon-stop',
    group: 'ops',
    title: 'Daemon stop',
    blurb: 'Stop the running background indexer.',
    cmd: ['daemon', 'stop'],
    args: [],
    mutating: true,
    mcp: null,
  },
  {
    id: 'completion',
    group: 'ops',
    title: 'Shell completion',
    blurb: 'Generate the autocompletion script for bash, zsh, fish or powershell.',
    cmd: ['completion'],
    args: [
      {
        kind: 'select',
        flag: null,
        name: 'shell',
        label: 'Shell',
        pos: true,
        required: true,
        def: 'zsh',
        options: ['bash', 'zsh', 'fish', 'powershell'].map((v) => ({ v, label: v })),
      },
    ],
    json: false,
    render: 'text',
    mcp: null,
  },

  // --------------------------------------------------------------------- mcp
  {
    id: 'mcp',
    group: 'mcp',
    title: 'MCP server',
    blurb: 'Inspect the stdio MCP server live: profiles, registered tools, schemas, and direct tool calls.',
    cmd: ['serve'],
    args: [
      {
        kind: 'select',
        flag: '--profile',
        name: 'profile',
        label: 'Profile',
        def: 'full',
        options: [
          { v: 'full', label: 'full — every tool (45)' },
          { v: 'agent', label: 'agent — exactly the taught workflow' },
          { v: 'core', label: 'core — lean compatibility set' },
        ],
      },
    ],
    view: 'mcp',
    mode: 'mcp',
    mcp: null,
  },
  {
    id: 'raw',
    group: 'mcp',
    title: 'Raw command',
    blurb: 'Run any codemap argv by hand — the escape hatch that guarantees full CLI coverage.',
    view: 'raw',
    run: false,
    mcp: null,
  },
]

export const APP_VIEWS = [
  { id: 'overview', group: 'learn', label: 'Overview', icon: '◉' },
  { id: 'atlas', group: 'learn', label: 'Atlas', icon: '▦' },
  { id: 'features', group: 'learn', label: 'Features', icon: '✦' },
  { id: 'flow', group: 'learn', label: 'Flow', icon: '⇢' },
  { id: 'dashboard', group: 'app', label: 'Health', icon: '◈' },
  { id: 'search', group: 'app', label: 'Unified search', icon: '⌕' },
  { id: 'graph', group: 'app', label: 'Graph explorer', icon: '⇄' },
  { id: 'source', group: 'app', label: 'Source browser', icon: '⌸' },
  { id: 'review', group: 'app', label: 'Review desk', icon: '◎' },
  { id: 'map', group: 'app', label: 'Architecture map', icon: '⬡' },
  { id: 'catalog', group: 'app', label: 'Feature catalog', icon: '≡' },
  { id: 'history', group: 'app', label: 'Run history', icon: '↺' },
  { id: 'settings', group: 'app', label: 'Settings', icon: '⚙' },
]

const byId = new Map(FEATURES.map((f) => [f.id, f]))

export function feature(id) {
  return byId.get(id) || null
}

export function runnableFeatures() {
  return FEATURES.filter((f) => f.cmd && f.cmd.length)
}

export function featuresByGroup() {
  const out = new Map()
  for (const g of GROUPS) out.set(g.id, [])
  for (const f of FEATURES) {
    if (!out.has(f.group)) out.set(f.group, [])
    out.get(f.group).push(f)
  }
  return out
}

/**
 * Turn user-entered values into argv for a feature.
 * Positionals keep declaration order; flags are emitted in declaration order so
 * the command line shown in the UI is stable and diffable.
 */
export function buildArgs(feat, values = {}) {
  const args = []
  for (const a of feat.args || []) {
    const raw = values[a.name]
    if (a.kind === 'pos' || a.pos) {
      if (raw === undefined || raw === null) continue
      const list = Array.isArray(raw) ? raw : typeof raw === 'string' && a.multi ? splitMulti(raw) : [raw]
      for (const v of list) {
        const s = String(v ?? '').trim()
        if (s) args.push(s)
      }
      continue
    }
    if (raw === undefined || raw === null || raw === '') continue
    if (a.kind === 'bool') {
      if (raw === true || raw === 'true' || raw === 'on' || raw === 1) args.push(a.flag)
      continue
    }
    if (a.kind === 'repeat') {
      const list = Array.isArray(raw) ? raw : splitMulti(raw)
      for (const v of list) {
        const s = String(v ?? '').trim()
        if (s) args.push(a.flag, s)
      }
      continue
    }
    if (a.kind === 'csv') {
      const list = Array.isArray(raw) ? raw : String(raw).split(',')
      const clean = list.map((s) => String(s ?? '').trim()).filter(Boolean)
      if (clean.length) args.push(a.flag, clean.join(','))
      continue
    }
    if (a.kind === 'num') {
      const n = Number(raw)
      if (!Number.isNaN(n)) args.push(a.flag, String(n))
      continue
    }
    args.push(a.flag, String(raw))
  }
  return args
}

// "a b c" or "a,b,c" or newline separated → several positional values.
function splitMulti(raw) {
  return String(raw)
    .split(/[\n,]|\s{2,}|\s+/)
    .map((s) => s.trim())
    .filter(Boolean)
}

export function defaultValues(feat) {
  const out = {}
  for (const a of feat.args || []) {
    if (a.def !== undefined) out[a.name] = a.def
    else if (a.kind === 'bool') out[a.name] = false
    else out[a.name] = a.kind === 'repeat' ? [] : ''
  }
  if (feat.preset) Object.assign(out, feat.preset)
  return out
}

export function requiredFields(feat) {
  return (feat.args || []).filter((a) => a.required)
}

/** Human-readable argv for the current values (the app runs it with cwd = project). */
export function commandLine(feat, values, { json = true } = {}) {
  const parts = [...(feat.cmd || []), ...buildArgs(feat, values)]
  if (json !== false && feat.json !== false) parts.push('--json')
  return parts.join(' ')
}

/** Full argv array, ready to hand to the runner. */
export function argv(feat, values, { json = true } = {}) {
  const parts = [...(feat.cmd || []), ...buildArgs(feat, values)]
  if (json !== false && feat.json !== false) parts.push('--json')
  return parts
}
