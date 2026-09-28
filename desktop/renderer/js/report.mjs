/* Copyright © 2026 abdul hamid <abdulachik> */

// Adaptive report renderer. Every codemap report lands here: a specialised
// renderer when the feature has one, and a shape-driven generic renderer for
// anything else — so a new flag or field still renders instead of vanishing.

import { h, copy, download, clear } from './dom.mjs'
import {
  badge,
  callGraphBadge,
  staleBadge,
  kindBadge,
  boolBadge,
  gateReasons,
  card,
  metric,
  barChart,
  table,
  symList,
  symRow,
  kv,
  callout,
  emptyState,
  errorBox,
  codeBlock,
  codeInline,
  jsonTree,
  jsonPanel,
  tabs,
  chip,
  nextChips,
  section,
  fmt,
  shortPath,
} from './components.mjs'
import { langFromPath } from './highlight.mjs'

const CALLOUT_KEYS = {
  note: 'info',
  resolution: 'warn',
  warning: 'warn',
  hint: 'info',
  reason: 'info',
  degraded: 'danger',
}

const HIDDEN_KEYS = new Set(['schema_version', 'next'])

export function renderReport(feat, result, ctx = {}) {
  const root = h('div.stack')
  if (!result) return root

  root.appendChild(resultHead(feat, result, ctx))

  if (!result.ok && !result.gateFailed) {
    root.appendChild(errorBox(result))
    if (result.stdout && !result.json) root.appendChild(h('pre.logbox', result.stdout))
    root.appendChild(rawPanel(feat, result))
    return root
  }

  const json = result.json
  if (!json && result.stdout !== undefined) {
    root.appendChild(textPanel(result, feat))
    root.appendChild(rawPanel(feat, result))
    return root
  }
  if (!json) {
    root.appendChild(emptyState({ title: 'No JSON in the response', note: result.stderr || 'The command produced no parsable output.' }))
    root.appendChild(rawPanel(feat, result))
    return root
  }

  for (const c of calloutsFor(json)) root.appendChild(c)

  const body = renderBody(feat, json, ctx, result)
  root.appendChild(body)

  const next = nextChips(json.next, (s) => ctx.onNext?.(s))
  if (next) root.appendChild(card({ title: 'Where to go next', body: next, tight: true }))

  root.appendChild(rawPanel(feat, result))
  return root
}

// ------------------------------------------------------------------- header

function resultHead(feat, result, ctx) {
  const json = result.json || {}
  const badges = [
    callGraphBadge(json.call_graph, json.resolution),
    json.stale === true ? staleBadge(true, json.staleness) : null,
    json.analysis_complete === false ? badge('analysis incomplete', 'danger', 'Review could not analyse every changed symbol — see the completeness counters.') : null,
    json.truncated === true ? badge('truncated', 'warn') : null,
    json.indexed === false ? badge('not indexed', 'warn') : null,
    json.found === false ? badge('no match', 'warn') : null,
    json.is_repo === false ? badge('not a git repo', 'plain') : null,
    result.gateFailed ? badge('gate failed · exit 6', 'danger') : null,
    json.complete === false ? badge('page incomplete', 'plain') : null,
  ].filter(Boolean)

  return h('div.result-head', [
    h('span.rh-title', feat ? feat.title : 'Result'),
    h('div.row.gap2', badges),
    h('div.rh-meta', [
      h('span', `exit ${result.exitCode ?? '?'}`),
      h('span', '·'),
      h('span', fmt.ms(result.ms)),
      h('span', '·'),
      h('span', fmt.bytes((result.stdout || '').length)),
      ctx.onGraph && graphPayloadFor(feat, json) ? h('button.btn.sm', { type: 'button', onclick: () => ctx.onGraph(graphPayloadFor(feat, json)) }, '⇶ Visualize') : null,
      h('button.btn.sm', { type: 'button', onclick: async (e) => { await copy(JSON.stringify(json, null, 2)); e.target.textContent = 'copied'; setTimeout(() => (e.target.textContent = 'Copy JSON'), 1000) } }, 'Copy JSON'),
      h('button.btn.sm', { type: 'button', onclick: () => download(`${(feat?.id || 'codemap')}-${Date.now()}.json`, JSON.stringify(json, null, 2)) }, 'Save'),
      h('button.btn.sm', { type: 'button', onclick: () => ctx.onRerun?.() }, '↻ Re-run'),
    ]),
  ])
}

function rawPanel(feat, result) {
  let open = false
  const body = h('div', { hidden: true }, jsonPanel(result, { title: feat?.id || 'report' }))
  const btn = h('button.btn.sm', {
    type: 'button',
    onclick: () => {
      open = !open
      body.hidden = !open
      btn.classList.toggle('on', open)
      btn.textContent = open ? '▾ Raw output' : '▸ Raw output'
    },
  }, '▸ Raw output')
  return h('div.stack', { style: 'gap:8px' }, [
    h('div.btn-row', [
      btn,
      h('button.btn.sm', { type: 'button', onclick: () => copy(result.command?.join(' ') || '') }, 'Copy command'),
      result.command ? h('code.inline', { title: 'argv as executed' }, result.command.map((a, i) => (i === 0 ? 'codemap' : a)).join(' ')) : null,
    ]),
    body,
  ])
}

function textPanel(result, feat) {
  const text = String(result.stdout || '')
  if (feat?.render === 'docs' || feat?.id === 'agent-playbook') {
    return markdownCard(text, feat.title)
  }
  return card({
    title: 'Output',
    actions: [h('button.btn.sm', { type: 'button', onclick: async (e) => { await copy(text); e.target.textContent = 'copied' } }, 'Copy')],
    body: h('pre.code', { style: 'white-space:pre-wrap' }, text || '(empty)'),
  })
}

async function markdownHost(text) {
  const { renderMarkdown } = await import('./markdown.mjs')
  return renderMarkdown(text)
}

function markdownCard(text, title) {
  const host = h('div', spinnerLine('rendering guide…'))
  markdownHost(text).then((node) => clear(host).appendChild(node))
  return card({ title, body: host })
}

function spinnerLine(label) {
  return h('div.row.gap2', [h('div.spinner'), h('span.small.muted', label)])
}

// ---------------------------------------------------------------- callouts

export function calloutsFor(json, keys = Object.keys(json)) {
  const out = []
  for (const key of keys) {
    const value = json[key]
    if (value === undefined || value === null || value === '' || value === false) continue
    if (CALLOUT_KEYS[key] && typeof value === 'string') {
      out.push(callout(CALLOUT_KEYS[key], key.replace(/_/g, ' '), value))
    }
  }
  if (Array.isArray(json.partial_errors) && json.partial_errors.length) {
    out.push(
      callout('danger', `${json.partial_errors.length} partial error(s)`, json.partial_errors.map((e) => (typeof e === 'string' ? e : `${e.kind || e.code || 'error'}: ${e.detail || e.message || JSON.stringify(e)}`)).join('\n')),
    )
  }
  if (Array.isArray(json.errors) && json.errors.length) {
    out.push(
      callout('warn', `${json.errors.length} file error(s)`, json.errors.slice(0, 20).map((e) => `${e.file || e.path || ''}: ${e.err || e.error || JSON.stringify(e)}`).join('\n')),
    )
  }
  if (json.warning) out.push(callout('warn', 'warning', String(json.warning)))
  if (json.tooling?.issues?.length) out.push(callout('warn', 'tooling', json.tooling.issues.map((i) => (typeof i === 'string' ? i : JSON.stringify(i))).join('\n')))
  return out
}

// -------------------------------------------------------------- dispatch

// codemap nests the full symbol record inside some wrappers (traverse hops
// carry `symbol` as an object alongside a durable `selector`); symOf flattens
// them so every renderer can treat a row as a flat symbol.
export { symOf } from './symbol.mjs'
import { symOf, isSymbolShaped as symbolShaped } from './symbol.mjs'

function renderBody(feat, json, ctx, result) {
  const kind = feat?.render || 'auto'
  const fn = RENDERERS[kind] || RENDERERS.auto
  try {
    const node = fn(json, ctx, feat, result)
    return node || RENDERERS.auto(json, ctx, feat, result)
  } catch (err) {
    return h('div.stack', [
      callout('danger', 'This view failed to render', `${err?.message || err}\nThe raw report is intact below.`),
      RENDERERS.auto(json, ctx, feat, result),
    ])
  }
}

export const RENDERERS = {
  auto: genericReport,
  status: renderStatus,
  doctor: renderDoctor,
  projects: renderProjects,
  config: renderConfig,
  index: renderIndex,
  coverage: renderCoverage,
  inconsistencies: renderInconsistencies,
  export: renderExport,
  search: renderSearch,
  grep: renderGrep,
  explore: renderExplore,
  readorder: renderReadOrder,
  map: renderMap,
  symlist: renderSymListReport,
  relation: renderRelation,
  references: renderReferences,
  path: renderPath,
  traverse: renderTraverse,
  dependencies: renderDependencies,
  related: renderRelated,
  impact: renderImpact,
  fileimpact: renderFileImpact,
  filecontext: renderFileContext,
  risk: renderRisk,
  refactor: renderRefactor,
  review: renderReview,
  context: renderContext,
  taskcontext: renderTaskContext,
  source: renderSource,
  annotations: renderAnnotations,
  agents: renderAgents,
  docs: (json, ctx, feat, result) => markdownCard(String(result?.stdout || ''), 'Guide'),
  text: (json, ctx, feat, result) => h('pre.code', { style: 'white-space:pre-wrap' }, String(result?.stdout || '')),
  cache: renderCache,
  daemon: renderDaemon,
  secrets: renderSecrets,
}

// ------------------------------------------------------------ shared parts

function symbolColumns(extra = []) {
  return [
    { key: 'kind', label: 'Kind', render: (r) => kindBadge(r.kind), sort: (a, b) => String(a.kind).localeCompare(String(b.kind)) },
    { key: 'symbol', label: 'Symbol', cls: 'code', render: (r) => h('span', [r.symbol || r.fqn || '—', r.fqn && r.fqn !== r.symbol ? h('span.dim', ` · ${r.fqn}`) : null]) },
    { key: 'file', label: 'File', cls: 'code', render: (r) => shortPath(r.file || r.relative_path || '') },
    { key: 'start_line', label: 'Line', cls: 'num' },
    ...extra,
  ]
}

function symbolTable(items, ctx, extra = [], opts = {}) {
  return table(symbolColumns(extra), items || [], {
    onRow: ctx.onSymbol
      ? (row) => ctx.onSymbol(row)
      : undefined,
    ...opts,
  })
}

function countsRow(pairs) {
  return h('div.row.gap3', pairs.filter(([, v]) => v !== undefined && v !== null).map(([k, v]) => h('span.badge', [h('span.dim', `${k}: `), fmt.num(v)])))
}

function groupByFile(items) {
  const map = new Map()
  if (!Array.isArray(items)) return []
  for (const it of items) {
    const key = it?.file || it?.relative_path || '(unknown)'
    if (!map.has(key)) map.set(key, [])
    map.get(key).push(it)
  }
  return [...map.entries()]
}

function testCommandList(cmds) {
  if (!cmds?.length) return null
  return card({
    title: 'Test commands',
    body: h('div.stack', { style: 'gap:6px' }, cmds.map((c) => h('div.row.gap2', [codeInline(typeof c === 'string' ? c : JSON.stringify(c)), h('button.btn.sm', { type: 'button', onclick: (e) => copy(typeof c === 'string' ? c : JSON.stringify(c)).then(() => (e.target.textContent = 'copied')) }, 'copy')]))),
    tight: true,
  })
}

// ------------------------------------------------------------- renderers

function renderStatus(json, ctx) {
  const stale = json.stale || {}
  const staleTotal = (stale.changed || 0) + (stale.new || 0) + (stale.deleted || 0)
  const metrics = h('div.grid.c4', [
    metric('Files', fmt.num(json.files), 'indexed'),
    metric('Nodes', fmt.num(json.nodes), 'symbols'),
    metric('Edges', fmt.num(json.edges), `${fmt.num(json.precise_edges || 0)} precise`),
    metric('Vectors', json.vectors_known ? fmt.num(json.vectors) : '—', json.vectors_known ? 'embedded' : `backend: ${json.semantic_backend || '?'}`),
    metric('Drift', staleTotal ? fmt.num(staleTotal) : 'clean', staleTotal ? `${stale.changed || 0} changed · ${stale.new || 0} new · ${stale.deleted || 0} deleted` : 'working tree matches the index', staleTotal ? 'warn' : 'ok'),
  ])

  const langs = Object.entries(json.languages || {}).sort((a, b) => b[1] - a[1])
  const kinds = Object.entries(json.kinds || {}).sort((a, b) => b[1] - a[1])

  return h('div.stack', [
    card({ title: json.project || 'project', sub: json.root, body: h('div.stack', [metrics, h('div.divider'), h('div.grid.c2', [
      card({ title: 'Languages', body: langs.length ? barChart(langs.map(([k, v]) => ({ label: k, value: v }))) : emptyState({ note: 'no languages indexed' }), tight: true }),
      card({ title: 'Symbol kinds', body: kinds.length ? barChart(kinds.map(([k, v]) => ({ label: k, value: v })), { tone: 'ok' }) : emptyState({ note: 'no symbols' }), tight: true }),
    ])])}),
    card({
      title: 'Precise call-graph coverage',
      body: h('div.row.gap2', Object.entries(json.precise || {}).map(([lang, on]) => boolBadge(lang, on, { okWhen: true }))),
      tight: true,
    }),
    json.siblings?.length ? card({ title: 'Ecosystem siblings detected', body: h('div.pill-list', json.siblings.map((s) => badge(s, 'info'))), tight: true }) : null,
    h('div.btn-row', [
      staleTotal ? h('button.btn.primary', { type: 'button', onclick: () => ctx.onFeature?.('index') }, '↻ Reindex now') : null,
      h('button.btn', { type: 'button', onclick: () => ctx.onFeature?.('coverage') }, 'Precise coverage'),
      h('button.btn', { type: 'button', onclick: () => ctx.onFeature?.('inconsistencies') }, 'Check inconsistencies'),
      h('button.btn', { type: 'button', onclick: () => ctx.onFeature?.('doctor') }, 'Doctor'),
    ]),
  ])
}

function renderDoctor(json, ctx) {
  const checks = json.checks || []
  const failed = checks.filter((c) => !c.ok)
  return h('div.stack', [
    h('div.grid.c4', [
      metric('Checks', fmt.num(checks.length), 'probed'),
      metric('Ready', fmt.num(checks.length - failed.length), 'ok', 'ok'),
      metric('Missing', fmt.num(failed.length), failed.length ? 'capability disabled' : 'nothing missing', failed.length ? 'warn' : 'ok'),
    ]),
    card({
      title: 'Environment',
      body: h('div.symlist', checks.map((c) => h('div.symrow', { style: 'grid-template-columns:20px minmax(0,1fr)' }, [
        h('span.sk', { style: c.ok ? 'color:var(--ok)' : 'color:var(--warn)' }, c.ok ? '✓' : '!'),
        h('div', { style: 'min-width:0' }, [
          h('div', { style: 'color:var(--text);font-size:12px' }, c.name),
          h('div', { style: 'font-family:var(--font-mono);font-size:10.5px;color:var(--text-3);word-break:break-all' }, c.detail || ''),
          c.hint ? h('div', { style: 'font-size:11px;color:var(--warn);margin-top:2px' }, `→ ${c.hint}`) : null,
        ]),
      ]))),
    }),
    h('div.stack', { style: 'gap:6px' }, [
      h('h4', 'Paths'),
      kv([
        ['data_dir', json.data_dir],
        ['project_root', json.project_root],
      ]),
    ]),
    failed.length ? callout('warn', 'Some capabilities are unavailable', 'Each missing piece disables exactly one capability — the core graph still works. Install what you need and re-run doctor.') : callout('ok', 'Everything codemap needs is available', ''),
  ])
}

function renderProjects(json, ctx) {
  const rows = json.projects || []
  return table(
    [
      { key: 'name', label: 'Project', render: (r) => h('span.mono', r.name) },
      { key: 'path', label: 'Path', cls: 'code', render: (r) => shortPath(r.path || r.root || '') },
      { key: 'nodes', label: 'Nodes', cls: 'num', render: (r) => fmt.num(r.nodes) },
      { key: 'edges', label: 'Edges', cls: 'num', render: (r) => fmt.num(r.edges) },
      { key: 'files', label: 'Files', cls: 'num', render: (r) => fmt.num(r.files) },
      { key: 'registered', label: 'Registered', render: (r) => (r.registered === false ? badge('no', 'warn') : badge('yes', 'ok')) },
    ],
    rows,
    { onRow: (r) => ctx.onOpenProject?.(r.path || r.root), empty: 'No projects registered yet — run Init project.' },
  )
}

function renderConfig(json) {
  const groups = Object.entries(json)
  return h('div.grid.c2', groups.map(([name, value]) => card({
    title: name,
    body: flattenKV(value),
    tight: true,
  })))
}

function flattenKV(value, prefix = '') {
  const pairs = []
  const walk = (v, p) => {
    if (v && typeof v === 'object' && !Array.isArray(v)) {
      for (const [k, sub] of Object.entries(v)) walk(sub, p ? `${p}.${k}` : k)
      return
    }
    pairs.push([p || '(value)', Array.isArray(v) ? v.join(', ') : v === '' ? '(empty)' : String(v)])
  }
  walk(value, prefix)
  return kv(pairs)
}

function renderIndex(json, ctx) {
  const timings = [
    ['scan', json.scan_ms],
    ['extract', json.extract_ms],
    ['lsp', json.lsp_ms],
    ['imports', json.imports_ms],
    ['edges', json.edges_ms],
    ['formats', json.formats_ms],
    ['analyze', json.analyze_ms],
    ['node index', json.node_index_ms],
  ].filter(([, v]) => v !== undefined && v !== null)

  return h('div.stack', [
    h('div.grid.c4', [
      metric('Indexed', fmt.num(json.files_indexed), `of ${fmt.num(json.files_scanned)} scanned`, 'accent'),
      metric('Unchanged', fmt.num(json.files_unchanged), 'hash matched'),
      metric('Nodes', fmt.num(json.nodes), ''),
      metric('Edges', fmt.num(json.edges), ''),
      metric('Total time', fmt.ms(json.total_ms), json.embedded ? 'with embeddings' : 'structure only'),
    ]),
    json.degraded ? callout('danger', 'DEGRADED', json.degraded_reason || 'Required language server(s) unavailable — the graph is incomplete for skipped languages.') : null,
    timings.length
      ? card({ title: 'Phase timings', body: barChart(timings.map(([label, value]) => ({ label, value: Math.round(value) })), { tone: 'ok' }), tight: true })
      : null,
    json.languages ? card({ title: 'Languages', body: barChart(Object.entries(json.languages).sort((a, b) => b[1] - a[1]).map(([label, value]) => ({ label, value }))), tight: true }) : null,
    json.unsupported && Object.keys(json.unsupported).length
      ? card({ title: 'Unsupported / skipped', body: h('div.pill-list', Object.entries(json.unsupported).map(([lang, n]) => badge(`${lang}: ${n}`, 'plain'))), tight: true })
      : null,
    json.cache ? card({ title: 'fcheap cache', body: kv([['action', json.cache.action], ['stash_id', json.cache.stash_id], ['tree_hash', json.cache.tree_hash]]), tight: true }) : null,
    h('div.btn-row', [
      h('button.btn', { type: 'button', onclick: () => ctx.onFeature?.('status') }, 'View status'),
      h('button.btn', { type: 'button', onclick: () => ctx.onFeature?.('coverage') }, 'Precise coverage'),
      h('button.btn', { type: 'button', onclick: () => ctx.onFeature?.('index-precise') }, 'Run --precise'),
    ]),
  ])
}

function renderCoverage(json, ctx) {
  const total = json.total_files || 0
  const covered = json.covered_files || 0
  return h('div.stack', [
    h('div.grid.c4', [
      metric('Files', fmt.num(total), 'in the index'),
      metric('Covered', fmt.num(covered), `${fmt.pct(covered, total)} precise`, covered === total && total ? 'ok' : 'warn'),
      metric('Uncovered', fmt.num(total - covered), ''),
      metric('Stale', fmt.num(json.stale_files || 0), 'drifted on disk', json.stale_files ? 'warn' : 'ok'),
    ]),
    progressPair(covered, total),
    json.by_language?.length
      ? card({
          title: 'By language',
          body: table(
            [
              { key: 'language', label: 'Language', render: (r) => h('span.mono', r.language || r.lang || '—') },
              { key: 'total_files', label: 'Files', cls: 'num' },
              { key: 'covered_files', label: 'Covered', cls: 'num' },
              { key: 'pct', label: '%', cls: 'num', render: (r) => fmt.pct(r.covered_files ?? 0, r.total_files ?? 0) },
              { key: 'stale_files', label: 'Stale', cls: 'num' },
              { key: 'resolver', label: 'Resolver', render: (r) => (r.resolver ? codeInline(r.resolver) : '—') },
            ],
            json.by_language,
            { dense: true },
          ),
        })
      : null,
    json.by_directory?.length
      ? card({
          title: `By directory (worst-covered first)${json.by_directory_truncated ? ' — truncated' : ''}`,
          body: table(
            [
              { key: 'directory', label: 'Directory', cls: 'code', render: (r) => shortPath(r.directory || r.dir || '—') },
              { key: 'total_files', label: 'Files', cls: 'num' },
              { key: 'covered_files', label: 'Covered', cls: 'num' },
              { key: 'uncovered_files', label: 'Uncovered', cls: 'num' },
              { key: 'pct', label: '%', cls: 'num', render: (r) => fmt.pct(r.covered_files ?? 0, r.total_files ?? 0) },
            ],
            json.by_directory,
            { dense: true, onRow: (r) => ctx.onPrefill?.('coverage', { prefix: r.directory || r.dir }) },
          ),
        })
      : null,
    json.files?.length
      ? card({
          title: `Per-file detail (${json.files.length})`,
          body: table(
            [
              { key: 'file', label: 'File', cls: 'code', render: (r) => shortPath(r.file || r.path) },
              { key: 'language', label: 'Lang', render: (r) => h('span.mono', r.language || r.lang || '—') },
              { key: 'covered', label: 'Covered', render: (r) => boolBadge('', r.covered !== false && !!r.resolver, { okWhen: true }) },
              { key: 'resolver', label: 'Resolver', render: (r) => (r.resolver ? codeInline(r.resolver) : '—') },
              { key: 'resolved_at', label: 'When', render: (r) => h('span.mono.small', r.resolved_at || r.at || '—') },
              { key: 'stale', label: 'Stale', render: (r) => (r.stale ? badge('stale', 'warn') : null) },
            ],
            json.files,
            { dense: true, onRow: (r) => ctx.onFile?.(r.file || r.path) },
          ),
        })
      : null,
  ])
}

function progressPair(value, total) {
  const pctv = total ? Math.round((value / total) * 100) : 0
  return h('div.stack', { style: 'gap:4px' }, [
    h('div.bar', { class: `bar ${pctv >= 80 ? 'ok' : pctv >= 40 ? 'warn' : 'danger'}` }, [h('i', { style: { width: `${Math.max(2, pctv)}%` } })]),
    h('div.small.dim.mono', `${fmt.num(value)} / ${fmt.num(total)} files covered (${pctv}%)`),
  ])
}

function renderInconsistencies(json, ctx) {
  const dangling = json.dangling_annotations || []
  const nameEdges = json.name_call_edges_on_resolved_files || []
  const noNodes = json.coverage_without_nodes || []
  const total = dangling.length + nameEdges.length + noNodes.length
  const tile = (title, items, note, repair) =>
    card({
      title,
      sub: `${items.length}`,
      body: items.length
        ? h('div.stack', { style: 'gap:8px' }, [
            h('div.symlist', items.slice(0, 200).map((it) => h('div.symrow', { style: 'grid-template-columns:minmax(0,1fr)' }, [h('div.mono.small', { style: 'white-space:pre-wrap;word-break:break-word' }, typeof it === 'string' ? it : JSON.stringify(it))]))),
            repair ? callout('info', 'Repair', repair) : null,
          ])
        : emptyState({ icon: '✓', title: 'No contradictions', note }),
    })

  return h('div.stack', [
    total === 0
      ? callout('ok', 'Internally coherent', 'No contradiction class fired. An empty report is evidence of internal coherence, not of correctness.')
      : callout('warn', `${total} contradiction(s)`, 'Each class below names its repair. An empty class is evidence of coherence for that class only.'),
    json.stale === true ? staleBadge(true) : null,
    h('div.grid.c3', [
      tile('Dangling annotations', dangling, 'Every annotation still matches an indexed symbol.', 'codemap annotate --retarget <id> <new-target>, or codemap annotations --rm <id>'),
      tile('Name-based edges on precise files', nameEdges, 'No precise-resolved file still emits name-based call edges.', 're-run the precise pass: codemap index --precise'),
      tile('Coverage without nodes', noNodes, 'No coverage row points at a file with no indexed symbols.', 'reindex the project'),
    ]),
    h('div.btn-row', [
      dangling.length ? h('button.btn', { type: 'button', onclick: () => ctx.onFeature?.('annotations') }, 'Open annotations') : null,
      nameEdges.length ? h('button.btn', { type: 'button', onclick: () => ctx.onFeature?.('index-precise') }, 'Run --precise') : null,
      noNodes.length ? h('button.btn', { type: 'button', onclick: () => ctx.onFeature?.('index') }, 'Reindex') : null,
    ]),
  ])
}

function renderExport(json, ctx) {
  const records = json.records || []
  return h('div.stack', [
    countsRow([
      ['schema', json.schema_version],
      ['total', json.total_records],
      ['returned', json.returned_records],
      ['offset', json.offset],
      ['limit', json.limit],
      ['next_offset', json.next_offset],
    ]),
    json.index_fingerprint ? kv([['index_fingerprint', json.index_fingerprint], ['project_key', json.project_key], ['complete', String(json.complete)]]) : null,
    table(
      [
        { key: 'ordinal', label: '#', cls: 'num' },
        { key: 'kind', label: 'Kind', render: (r) => kindBadge(r.kind) },
        { key: 'fqn', label: 'FQN', cls: 'code' },
        { key: 'file', label: 'File', cls: 'code', render: (r) => shortPath(r.file) },
        { key: 'start_line', label: 'Line', cls: 'num' },
        { key: 'content_bytes', label: 'Bytes', cls: 'num', render: (r) => fmt.num(r.content_bytes ?? (r.content ? r.content.length : 0)) },
      ],
      records,
      { dense: true, onRow: (r) => ctx.onSymbol?.(r), empty: 'no records on this page' },
    ),
    h('div.btn-row', [
      json.complete === false
        ? h('button.btn', { type: 'button', onclick: () => ctx.onPrefill?.('export-symbols', { offset: json.next_offset }) }, `Next page (offset ${json.next_offset})`)
        : null,
      h('button.btn', { type: 'button', onclick: () => download(`structural-export-${Date.now()}.json`, JSON.stringify(json, null, 2)) }, 'Download page'),
    ]),
  ])
}

function renderSearch(json, ctx) {
  const hits = json.hits || json.results || []
  return h('div.stack', [
    h('div.row.gap3', [
      json.query ? badge(`query: ${json.query}`, 'accent') : null,
      json.mode ? badge(`mode: ${json.mode}`, 'plain') : null,
      json.search_mode ? badge(`backend: ${json.search_mode}`, 'info') : null,
      json.backend ? badge(`backend: ${json.backend}`, 'info') : null,
      badge(`${hits.length} hit(s)`, 'plain'),
    ]),
    hits.length
      ? h('div.symlist', hits.map((it) => symRow(it, {
          onPick: ctx.onSymbol,
          meta: (r) => [r.signature, r.score ? `score ${Number(r.score).toFixed(4)}` : null, r.matched_in ? `matched in ${r.matched_in}` : null].filter(Boolean).join(' · '),
        })))
      : emptyState({ title: 'No hits', note: 'Try a shorter query, or use grep for exact text and find for names.' }),
  ])
}

function renderGrep(json, ctx) {
  const hits = json.hits || []
  const groups = groupByFile(hits)
  return h('div.stack', [
    countsRow([
      ['pattern', json.pattern],
      ['regex', String(!!json.regex)],
      ['ignore_case', String(!!json.ignore_case)],
      ['total', json.total ?? hits.length],
      ['files scanned', json.files_scanned],
    ]),
    json.stale === true ? callout('warn', 'Indexed set may be stale', 'Reads are live from disk at query time, but a file added since the last index is not in the searched set yet.') : null,
    groups.length
      ? h('div.stack', groups.map(([file, rows]) => card({
          title: shortPath(file),
          sub: `${rows.length} hit(s)`,
          actions: [h('button.btn.sm', { type: 'button', onclick: () => ctx.onFile?.(file) }, 'Open file')],
          body: h('div.stack', { style: 'gap:6px' }, rows.slice(0, 40).map((r) => h('div.symrow', {
            style: 'grid-template-columns:44px minmax(0,1fr)',
            onclick: () => ctx.onSymbol?.({ file: r.file || file, start_line: r.line, symbol: r.symbol || r.fqn, kind: r.kind, fqn: r.fqn }),
          }, [
            h('span.sk', { style: 'color:var(--accent)' }, String(r.line ?? '')),
            h('div', { style: 'min-width:0' }, [
              h('div.mono.small', { style: 'white-space:pre-wrap;word-break:break-word;color:var(--text)' }, r.text || r.line_text || ''),
              r.symbol || r.fqn ? h('div.meta', [r.kind ? kindBadge(r.kind) : null, h('span', ` in ${r.fqn || r.symbol}${r.start_line ? `:${r.start_line}` : ''}`)]) : null,
            ]),
          ]))),
        })))
      : emptyState({ title: 'No matches', note: 'grep only searches the indexed file set — files with no registered extractor are invisible to it.' }),
  ])
}

function renderExplore(json, ctx) {
  const seeds = json.seeds || []
  const contexts = json.contexts || []
  return h('div.stack', [
    h('div.row.gap3', [
      badge(`intent: ${json.query}`, 'accent'),
      json.search_mode ? badge(`search: ${json.search_mode}`, 'info') : null,
      badge(`${seeds.length} seed(s)`, 'plain'),
      badge(`${contexts.length} neighborhood(s)`, 'plain'),
      callGraphBadge(json.call_graph),
    ]),
    seeds.length
      ? card({
          title: 'Seeds',
          body: h('div.symlist', seeds.map((s) => symRow(s, { onPick: ctx.onSymbol, meta: (r) => [r.signature, r.score ? `score ${Number(r.score).toFixed(3)}` : null].filter(Boolean).join(' · ') }))),
        })
      : null,
    contexts.length
      ? h('div.stack', contexts.map((c) => contextCard(c, ctx)))
      : null,
  ])
}

function contextCard(c, ctx) {
  const def = (c.definitions || [])[0]
  return card({
    title: def ? `${def.fqn || def.symbol}` : (c.symbol || 'context'),
    sub: def ? `${def.file}:${def.start_line}` : '',
    actions: [
      c.selector ? h('button.btn.sm', { type: 'button', onclick: () => ctx.onSymbol?.({ ...(def || {}), file: c.selector.file, start_line: c.selector.start_line, fqn: c.selector.fqn, kind: c.selector.kind }) }, 'Open') : null,
      h('button.btn.sm', { type: 'button', onclick: () => ctx.onPrefill?.('context', { at: `${c.selector?.file}:${c.selector?.start_line}` }) }, 'Full context'),
    ],
    body: h('div.stack', { style: 'gap:8px' }, [
      def?.doc ? h('div.small.muted', { style: 'white-space:pre-wrap' }, def.doc) : null,
      def?.signature ? codeBlock({ text: def.signature, lang: langFromPath(def.file), maxLines: 4 }) : null,
      relationBlock('Callers', c.callers, c.callers_total, ctx),
      relationBlock('Callees', c.callees, c.callees_total, ctx),
      relationBlock('Value references', c.references, c.references_total, ctx),
      relationBlock('Covering tests', c.tests, c.tests_total, ctx),
      c.test_commands?.length ? h('div.pill-list', c.test_commands.map((t) => codeInline(t))) : null,
    ]),
  })
}

function relationBlock(label, items, total, ctx, max = 25) {
  if (!items || !items.length) return null
  return h('div.stack', { style: 'gap:4px' }, [
    h('h4', `${label}${total && total !== items.length ? ` (top ${items.length} of ${total})` : ` (${items.length})`}`),
    h('div.symlist', items.slice(0, max).map((it) => symRow(it, { onPick: ctx.onSymbol }))),
  ])
}

function renderReadOrder(json, ctx) {
  const entries = json.entries || []
  return h('div.stack', [
    json.indexed === false ? callout('warn', 'Not indexed', 'Run index first.') : null,
    entries.length
      ? table(
          [
            { key: 'rank', label: '#', cls: 'num' },
            { key: 'kind', label: 'Kind', render: (r) => kindBadge(r.kind || (r.entrypoint ? 'entrypoint' : 'hub')) },
            { key: 'symbol', label: 'Symbol', cls: 'code', render: (r) => h('span', [r.symbol || r.fqn, r.fqn && r.fqn !== r.symbol ? h('span.dim', ` · ${r.fqn}`) : null]) },
            { key: 'file', label: 'File', cls: 'code', render: (r) => shortPath(r.file || '') },
            { key: 'start_line', label: 'Line', cls: 'num' },
            { key: 'score', label: 'Score', cls: 'num', render: (r) => (r.score !== undefined ? Number(r.score).toFixed(2) : '—') },
            { key: 'in_degree', label: 'Fan-in', cls: 'num' },
            { key: 'reason', label: 'Why start here', render: (r) => h('span.small.muted', r.reason || '') },
          ],
          entries,
          { onRow: (r) => ctx.onSymbol?.(r) },
        )
      : emptyState({ title: 'Nothing ranked', note: 'The index has no entrypoints or hubs for this filter.' }),
  ])
}

function renderMap(json, ctx) {
  const subsystems = json.subsystems || []
  const bridges = json.bridges || []
  return h('div.stack', [
    countsRow([
      ['strategy', json.strategy],
      ['subsystems', `${subsystems.length}/${json.subsystems_total ?? subsystems.length}`],
      ['bridges', `${bridges.length}/${json.bridges_total ?? bridges.length}`],
      ['hubs', `${(json.hubs || []).length}/${json.hubs_total ?? (json.hubs || []).length}`],
      ['entrypoints', `${(json.entrypoints || []).length}/${json.entrypoints_total ?? (json.entrypoints || []).length}`],
    ]),
    json.truncated ? callout('info', 'bounded result', 'This overview is deliberately truncated. Raise the --top-* limits to see more.') : null,
    subsystems.length
      ? card({
          title: 'Subsystems',
          actions: [h('button.btn.sm', { type: 'button', onclick: () => ctx.onMapView?.(json) }, '⬡ Open map view')],
          body: h('div.subsystem-grid', subsystems.map((s) => h('button.subsystem', { type: 'button', onclick: () => ctx.onPrefill?.('coverage', { prefix: s.name }) }, [
            h('div.ss-name', s.name),
            h('div.ss-stats', [
              h('span', [h('b', fmt.num(s.files)), ' files']),
              h('span', [h('b', fmt.num(s.symbols)), ' symbols']),
              h('span', [h('b', fmt.num(s.internal_edges)), ' internal']),
              h('span', [h('b', fmt.num(s.inbound_edges)), ' in']),
              h('span', [h('b', fmt.num(s.outbound_edges)), ' out']),
            ]),
            s.languages?.length ? h('div.pill-list', { style: 'margin-top:6px' }, s.languages.slice(0, 6).map((l) => badge(typeof l === 'string' ? l : l.language || JSON.stringify(l), 'plain'))) : null,
          ]))),
        })
      : null,
    bridges.length
      ? card({
          title: 'Cross-subsystem bridges',
          body: h('div', bridges.slice(0, 200).map((b) => h('div.bridge-row', [
            h('span.from', { title: b.from }, b.from),
            h('span.arrow', b.edge_type === 'imports' ? '⇢' : '→'),
            h('span.to', { title: b.to }, b.to),
            h('span', [badge(`${fmt.num(b.count)} ${b.edge_type || 'edges'}`, 'accent'), b.provenance ? badge(b.provenance, 'plain') : null]),
          ]))),
        })
      : null,
    json.hubs?.length ? card({ title: 'Hubs', body: symList(json.hubs, { onPick: ctx.onSymbol, meta: (r) => `fan-in ${fmt.num(r.in_degree)}${r.shared_name ? ` · ${r.shared_name} share this name` : ''}` }) }) : null,
    json.entrypoints?.length ? card({ title: 'Likely entrypoints', body: symList(json.entrypoints, { onPick: ctx.onSymbol, meta: (r) => [r.reason, r.score !== undefined ? `score ${Number(r.score).toFixed(2)}` : null].filter(Boolean).join(' · ') }) }) : null,
  ])
}

function renderSymListReport(json, ctx) {
  const items = json.symbols || json.results || json.hotspots || json.orphans || json.entries || json.matches || json.definitions || []
  const meta = []
  if (json.file) meta.push(badge(`file: ${json.file}`, 'accent'))
  if (json.query) meta.push(badge(`query: ${json.query}`, 'accent'))
  if (json.project) meta.push(badge(json.project, 'plain'))
  meta.push(badge(`${items.length} item(s)`, 'plain'))
  const extra = []
  if (items.some((i) => i.in_degree !== undefined)) extra.push({ key: 'in_degree', label: 'Fan-in', cls: 'num' })
  if (items.some((i) => i.shared_name)) extra.push({ key: 'shared_name', label: 'Same name', cls: 'num', render: (r) => (r.shared_name > 1 ? badge(`${r.shared_name}`, 'warn', 'Several definitions share this name — results are candidates.') : '1') })
  if (items.some((i) => i.score)) extra.push({ key: 'score', label: 'Score', cls: 'num', render: (r) => Number(r.score || 0).toFixed(3) })
  return h('div.stack', [
    h('div.row.gap2', meta),
    callGraphBadge(json.call_graph, json.resolution) ? h('div.row.gap2', [callGraphBadge(json.call_graph, json.resolution)]) : null,
    items.length ? symbolTable(items, ctx, extra) : emptyState({ title: 'Nothing here', note: json.note || '' }),
  ])
}

function renderRelation(json, ctx) {
  const items = json.results || []
  return h('div.stack', [
    h('div.row.gap3', [
      json.symbol ? badge(`symbol: ${json.symbol}`, 'accent') : null,
      json.selector ? codeInline(`${json.selector.file}:${json.selector.start_line}`) : null,
      badge(`${items.length} result(s)${json.total && json.total !== items.length ? ` of ${json.total}` : ''}`, 'plain'),
      callGraphBadge(json.call_graph, json.resolution),
      json.precise ? badge('precise', 'ok') : null,
    ]),
    items.length ? symbolTable(items, ctx, [{ key: 'shared_name', label: 'Same name', cls: 'num', render: (r) => (r.shared_name > 1 ? badge(`${r.shared_name}`, 'warn') : '—') }]) : emptyState({ title: 'No relations found', note: 'For TypeScript/JavaScript/Python a plain index has no name-based call edges — run index --precise to build the call graph.' }),
  ])
}

function renderReferences(json, ctx) {
  const refs = json.references || []
  return h('div.stack', [
    h('div.row.gap3', [
      json.symbol ? badge(`symbol: ${json.symbol}`, 'accent') : null,
      badge(`${refs.length} of ${json.references_total ?? refs.length} reference(s)`, 'plain'),
      badge(`confidence: ${json.confidence || '?'}`, json.confidence === 'full' ? 'ok' : 'warn'),
      badge(`coverage: ${json.coverage || '?'}`, json.coverage === 'complete' ? 'ok' : 'warn'),
      callGraphBadge(json.call_graph),
    ]),
    json.definitions?.length ? card({ title: 'Definitions considered', body: symList(json.definitions, { onPick: ctx.onSymbol }), tight: true }) : null,
    refs.length ? symbolTable(refs, ctx, [{ key: 'edge_type', label: 'Edge', render: (r) => badge(r.edge_type || 'references', 'info') }]) : emptyState({ icon: '∅', title: 'No indexed value-reference sites', note: json.note || 'Absence is not proof: dynamic wiring (reflection, DI containers, props) is invisible to the graph.' }),
  ])
}

function renderPath(json, ctx) {
  const path = json.path || []
  return h('div.stack', [
    h('div.row.gap3', [
      badge(`from: ${json.from}`, 'accent'),
      h('span', '→'),
      badge(`to: ${json.to}`, 'accent'),
      callGraphBadge(json.call_graph, json.resolution),
      json.found ? badge(`${path.length} hop(s)`, 'ok') : badge('no path found', 'warn'),
    ]),
    path.length
      ? h('div.symlist', path.map((p, i) => h('div.hop-row', { onclick: () => ctx.onSymbol?.(p) }, [
          h('span.hd', i === 0 ? 'start' : `${i}`),
          h('span.hn', [p.symbol || p.fqn, p.fqn && p.fqn !== p.symbol ? h('span.dim', ` · ${p.fqn}`) : null]),
          h('span.hm', `${shortPath(p.file || '')}:${p.start_line ?? ''}`),
        ])))
      : emptyState({ title: 'No indexed call path', note: 'The endpoints may be unreachable in the stored graph, or the call graph may be name-based for these languages. Try index --precise.' }),
  ])
}

function renderTraverse(json, ctx) {
  const hops = json.hops || []
  return h('div.stack', [
    h('div.row.gap3', [
      json.start ? codeInline(`${json.start.file}:${json.start.start_line}`) : null,
      badge(`direction: ${json.direction}`, 'plain'),
      badge(`depth ≤ ${json.depth_limit}`, 'plain'),
      badge(`nodes ≤ ${json.node_limit}`, 'plain'),
      badge(`${hops.length} hop(s)`, 'accent'),
      json.truncated ? badge('truncated', 'warn') : null,
      callGraphBadge(json.call_graph),
    ]),
    json.edge_types?.length ? h('div.pill-list', json.edge_types.map((t) => badge(t, 'info'))) : null,
    json.domains?.length
      ? card({
          title: 'Per-domain confidence',
          body: table(
            [
              { key: 'edge_type', label: 'Domain', render: (r) => codeInline(r.edge_type) },
              { key: 'confirmed', label: 'Confirmed', cls: 'num' },
              { key: 'candidate', label: 'Candidate', cls: 'num' },
              { key: 'conf', label: 'Confidence', render: (r) => badge(r.confidence || (r.candidate ? 'candidate' : 'confirmed'), r.confidence === 'confirmed' ? 'ok' : 'warn') },
            ],
            json.domains,
            { dense: true },
          ),
          tight: true,
        })
      : null,
    hops.length
      ? table(
          [
            { key: 'depth', label: 'd', cls: 'num' },
            { key: 'direction', label: 'Dir', render: (r) => badge(r.direction === 'incoming' ? '← in' : '→ out', r.direction === 'incoming' ? 'info' : 'plain') },
            { key: 'edge_type', label: 'Edge', render: (r) => codeInline(r.edge_type || '') },
            { key: 'symbol', label: 'Symbol', cls: 'code', render: (r) => { const s = symOf(r); return h('span', [s.symbol || s.fqn || '—', s.fqn && s.fqn !== s.symbol ? h('span.dim', ` · ${s.fqn}`) : null]) } },
            { key: 'file', label: 'File', cls: 'code', render: (r) => shortPath(symOf(r).file || '') },
            { key: 'line', label: 'Line', cls: 'num', render: (r) => symOf(r).start_line ?? '—' },
            { key: 'confidence', label: 'Confidence', render: (r) => badge(r.confidence || '?', r.confidence === 'confirmed' ? 'ok' : 'warn', r.confidence_reason || '') },
            { key: 'weight', label: 'Weight', cls: 'num', render: (r) => (r.weight !== undefined ? Number(r.weight).toFixed(2) : '—') },
          ],
          hops,
          { dense: true, onRow: (r) => ctx.onSymbol?.(symOf(r)) },
        )
      : emptyState({ title: 'No hops', note: 'Nothing matched the selected domains and direction from that definition.' }),
  ])
}

function renderDependencies(json, ctx) {
  const coverage = json.coverage || {}
  const domains = coverage.domains || []
  return h('div.stack', [
    h('div.row.gap3', [
      codeInline(json.file || ''),
      badge(`${fmt.num(json.evidence_total)} evidence`, 'accent'),
      badge(`${fmt.num(json.confirmed_total)} confirmed`, 'ok'),
      badge(`${fmt.num(json.candidate_total)} candidate`, 'warn'),
      badge(`${fmt.num(json.dependents_total)} dependent file(s)`, 'plain'),
      json.stale ? staleBadge(true) : null,
      callGraphBadge(json.call_graph),
      coverage.complete ? badge('coverage complete', 'ok') : badge('coverage incomplete', 'warn'),
    ]),
    domains.length
      ? card({
          title: 'Evidence domains',
          body: table(
            [
              { key: 'domain', label: 'Domain', render: (r) => codeInline(r.domain || r.kind || r.name || '') },
              { key: 'complete', label: 'Complete', render: (r) => boolBadge('', !!r.complete) },
              { key: 'confirmed_total', label: 'Confirmed', cls: 'num' },
              { key: 'candidate_total', label: 'Candidate', cls: 'num' },
              { key: 'reason', label: 'Why', render: (r) => h('span.small.muted', r.reason || r.detail || '') },
            ],
            domains,
            { dense: true },
          ),
          tight: true,
        })
      : null,
    json.dependents?.length
      ? card({
          title: 'Dependent files',
          body: table(
            [
              { key: 'file', label: 'File', cls: 'code', render: (r) => shortPath(r.file) },
              { key: 'evidence_total', label: 'Evidence', cls: 'num' },
              { key: 'confirmed_total', label: 'Confirmed', cls: 'num' },
              { key: 'candidate_total', label: 'Candidate', cls: 'num' },
              { key: 'file_scoped_total', label: 'File-scoped', cls: 'num' },
              { key: 'package_scoped_total', label: 'Pkg-scoped', cls: 'num' },
              { key: 'kinds', label: 'Kinds', render: (r) => h('div.pill-list', (r.kinds || []).map((k) => badge(typeof k === 'string' ? k : k.kind || JSON.stringify(k), 'plain'))) },
            ],
            json.dependents,
            { dense: true, onRow: (r) => ctx.onFile?.(r.file) },
          ),
        })
      : null,
    json.samples?.length
      ? card({
          title: `Evidence samples (${json.samples.length}${json.samples_truncated ? ` · ${json.samples_truncated} more hidden` : ''})`,
          body: table(
            [
              { key: 'kind', label: 'Kind', render: (r) => badge(r.kind || r.edge_type || '', 'info') },
              { key: 'confidence', label: 'Confidence', render: (r) => badge(r.confidence || '?', r.confidence === 'confirmed' ? 'ok' : 'warn') },
              { key: 'from_file', label: 'From', cls: 'code', render: (r) => shortPath(r.from_file || r.from || '') },
              { key: 'symbol', label: 'Symbol', cls: 'code', render: (r) => r.symbol || r.fqn || '—' },
              { key: 'line', label: 'Line', cls: 'num', render: (r) => r.line ?? r.start_line ?? '—' },
              { key: 'reason', label: 'Reason', render: (r) => h('span.small.muted', r.reason || '') },
            ],
            json.samples,
            { dense: true, onRow: (r) => (r.from_file || r.from) && ctx.onFile?.(r.from_file || r.from) },
          ),
        })
      : null,
  ])
}

function renderRelated(json, ctx) {
  const items = json.related || []
  return h('div.stack', [
    h('div.row.gap3', [codeInline(json.file || ''), badge(`${items.length} related file(s)`, 'accent'), json.indexed === false ? badge('not indexed', 'warn') : null]),
    items.length
      ? table(
          [
            { key: 'relative_path', label: 'File', cls: 'code', render: (r) => shortPath(r.relative_path || r.file) },
            { key: 'reason', label: 'Why related', render: (r) => h('span.small.muted', r.reason || '') },
            { key: 'confidence', label: 'Confidence', render: (r) => badge(r.confidence || '?', r.confidence === 'confirmed' ? 'ok' : 'warn') },
          ],
          items,
          { onRow: (r) => ctx.onFile?.(r.relative_path || r.file) },
        )
      : emptyState({ title: 'No related files' }),
  ])
}

function renderImpact(json, ctx) {
  if (json.frames) return renderBatch(json, ctx)
  const blast = json.blast_radius || []
  const tests = json.tests || []
  const depths = new Map()
  for (const b of blast) depths.set(b.depth, (depths.get(b.depth) || 0) + 1)
  return h('div.stack', [
    h('div.row.gap3', [
      json.symbol ? badge(`symbol: ${json.symbol}`, 'accent') : null,
      json.selector ? codeInline(`${json.selector.file}:${json.selector.start_line}`) : null,
      callGraphBadge(json.call_graph),
    ]),
    h('div.grid.c4', [
      metric('Direct callers', fmt.num((json.direct_callers || []).length), ''),
      metric('Blast radius', fmt.num(blast.length), `depth ≤ ${json.depth ?? 3}`, blast.length > 12 ? 'warn' : ''),
      metric('Covering tests', fmt.num(tests.length), json.untested ? 'but symbol itself untested' : '', json.untested ? 'danger' : 'ok'),
      metric('Untested', json.untested ? 'YES' : 'no', json.untested ? 'no test reaches this symbol' : 'covered', json.untested ? 'danger' : 'ok'),
    ]),
    depths.size ? card({ title: 'Blast radius by depth', body: barChart([...depths.entries()].sort((a, b) => a[0] - b[0]).map(([d, n]) => ({ label: `depth ${d}`, value: n })), { tone: 'warn' }), tight: true }) : null,
    json.locations?.length ? card({ title: 'Definition(s) matched', body: symList(json.locations, { onPick: ctx.onSymbol }), tight: true }) : null,
    relationBlock('Direct callers', json.direct_callers, json.callers_total, ctx),
    blast.length ? card({ title: `Blast radius (${blast.length})`, body: symList(blast, { onPick: ctx.onSymbol, meta: (r) => `depth ${r.depth} · ${shortPath(r.file)}:${r.start_line}` }) }) : null,
    tests.length ? card({ title: `Covering tests (${tests.length})`, body: symList(tests, { onPick: ctx.onSymbol }) }) : callout('warn', 'No covering tests', 'Nothing in the indexed graph reaches this symbol from a test. That is a candidate signal, not proof of missing coverage.'),
    testCommandList(json.test_commands),
    json.ambiguous ? callout('warn', 'Ambiguous name', json.ambiguous) : null,
  ])
}

function renderBatch(json, ctx) {
  return h('div.stack', [
    countsRow([['frames', json.frames.length], ['ok', json.frames.filter((f) => f.ok !== false).length], ['failed', json.frames.filter((f) => f.ok === false).length]]),
    ...json.frames.map((f, i) => card({
      title: `Frame ${i + 1}${f.symbol ? ` · ${f.symbol}` : ''}${f.selector ? ` · ${f.selector.file}:${f.selector.start_line}` : ''}`,
      body: f.ok === false ? errorBox(f) : renderImpact(f, ctx),
    })),
  ])
}

function renderFileImpact(json, ctx) {
  const verdictTone = { safe: 'ok', unsafe: 'danger', unknown: 'warn' }[json.delete_verdict] || 'plain'
  return h('div.stack', [
    h('div.row.gap3', [
      codeInline(json.file || ''),
      badge(`delete verdict: ${json.delete_verdict || '?'}`, verdictTone),
      boolBadge('breaking change', json.breaking_change, { okWhen: false }),
      boolBadge('safe to delete', json.safe_to_delete, { okWhen: true }),
      json.stale ? staleBadge(true) : null,
      callGraphBadge(json.call_graph),
    ]),
    h('div.grid.c4', [
      metric('Symbols', fmt.num(json.symbols), 'defined in this file'),
      metric('Dependent files', fmt.num((json.dependent_files || []).length), ''),
      metric('Blast radius', fmt.num(json.blast_radius_count), `depth ≤ ${json.depth ?? 3}`),
      metric('Covering tests', fmt.num((json.covering_tests || []).length), (json.untested_symbols || []).length ? `${json.untested_symbols.length} untested symbol(s)` : 'all symbols covered', (json.untested_symbols || []).length ? 'warn' : 'ok'),
    ]),
    json.dependent_files?.length
      ? card({
          title: 'Dependent files',
          body: table(
            [
              { key: 'file', label: 'File', cls: 'code', render: (r) => shortPath(typeof r === 'string' ? r : r.file) },
              { key: 'evidence_total', label: 'Evidence', cls: 'num' },
              { key: 'kinds', label: 'Kinds', render: (r) => h('div.pill-list', (r.kinds || []).map((k) => badge(typeof k === 'string' ? k : k.kind || '', 'plain'))) },
            ],
            json.dependent_files,
            { dense: true, onRow: (r) => ctx.onFile?.(typeof r === 'string' ? r : r.file) },
          ),
        })
      : null,
    json.covering_tests?.length ? card({ title: 'Covering tests', body: symList(json.covering_tests, { onPick: ctx.onSymbol, meta: (r) => (r.heuristic ? 'heuristic match' : '') }) }) : null,
    json.untested_symbols?.length ? card({ title: 'Untested symbols', body: symList(json.untested_symbols, { onPick: ctx.onSymbol }) }) : null,
    json.dependency_evidence ? card({ title: 'Dependency evidence', body: renderDependencies(json.dependency_evidence, ctx), tight: true }) : null,
  ])
}

function renderFileContext(json, ctx) {
  return h('div.stack', [
    h('div.row.gap3', [codeInline(json.file || ''), callGraphBadge(json.call_graph), json.indexed === false ? badge('not indexed', 'warn') : null]),
    json.symbols?.length ? card({ title: `Symbols in file (${json.symbols.length})`, body: symbolTable(json.symbols, ctx), tight: true }) : null,
    json.impact ? card({ title: 'File impact', body: renderFileImpact(json.impact, ctx), tight: true }) : null,
    json.related_files?.length ? card({ title: `Related files (${json.related_files.length})`, body: renderRelated({ file: json.file, related: json.related_files }, ctx), tight: true }) : null,
  ])
}

function riskDial(level, score) {
  const pctv = Math.max(0, Math.min(1, Number(score) || 0))
  const color = { low: 'var(--ok)', medium: 'var(--warn)', high: 'var(--danger)', unknown: 'var(--text-dim)' }[level] || 'var(--text-dim)'
  const r = 50
  const c = 2 * Math.PI * r
  return h('div.risk-dial', [
    h('svg', { viewBox: '0 0 120 120', width: '118', height: '118' }, [
      h('circle', { cx: '60', cy: '60', r: String(r), fill: 'none', stroke: 'var(--surface-3)', 'stroke-width': '9' }),
      h('circle', {
        cx: '60',
        cy: '60',
        r: String(r),
        fill: 'none',
        stroke: color,
        'stroke-width': '9',
        'stroke-linecap': 'round',
        'stroke-dasharray': `${(c * pctv).toFixed(2)} ${c.toFixed(2)}`,
      }),
    ]),
    h('div', { style: 'text-align:center' }, [
      h(`div.rd-v.risk-${level || 'unknown'}`, score === undefined || score === null ? '?' : Number(score).toFixed(2)),
      h('div.rd-l', level || 'unknown'),
    ]),
  ])
}

function renderRisk(json, ctx) {
  const factors = json.factors || []
  return h('div.stack', [
    h('div.risk-hero', [
      riskDial(json.level, json.score),
      h('div.stack', { style: 'gap:8px' }, [
        h('div.row.gap3', [
          json.symbol ? badge(`symbol: ${json.symbol}`, 'accent') : null,
          json.selector ? codeInline(`${json.selector.file}:${json.selector.start_line}`) : null,
          badge(`callers: ${fmt.num(json.callers)}`, 'plain'),
          badge(`covering tests: ${fmt.num(json.covering_tests_count)}`, json.covering_tests_count ? 'ok' : 'danger'),
          callGraphBadge(json.call_graph),
        ]),
        factors.length
          ? table(
              [
                { key: 'factor', label: 'Factor', render: (r) => h('span.mono', r.factor || r.name || '') },
                { key: 'severity', label: 'Severity', render: (r) => badge(r.severity || '?', { high: 'danger', medium: 'warn', low: 'ok' }[r.severity] || 'plain') },
                { key: 'detail', label: 'Detail', render: (r) => h('span.small.muted', r.detail || '') },
              ],
              factors,
              { dense: true },
            )
          : callout('ok', 'No risk factors fired', ''),
        json.gate ? callout('info', 'Gate', `level ${json.gate.level ?? '?'} · would fail on: ${gateReasons(json.gate.would_fail_on)}`) : null,
        json.level === 'unknown' ? callout('warn', 'Unknown is not safe', 'A risk level of “unknown” means the call graph was not usable — it is never treated as low risk.') : null,
      ]),
    ]),
    h('div.btn-row', [
      h('button.btn', { type: 'button', onclick: () => ctx.onPrefill?.('impact', { at: json.selector ? `${json.selector.file}:${json.selector.start_line}` : '', symbol: json.symbol }) }, 'Open impact'),
      h('button.btn', { type: 'button', onclick: () => ctx.onPrefill?.('context', { at: json.selector ? `${json.selector.file}:${json.selector.start_line}` : '', symbol: json.symbol }) }, 'Open context'),
      h('button.btn', { type: 'button', onclick: () => ctx.onFeature?.('index-precise') }, 'Improve with --precise'),
    ]),
  ])
}

function renderRefactor(json, ctx) {
  const def = (json.definitions || [])[0]
  return h('div.stack', [
    h('div.row.gap3', [
      json.symbol ? badge(`symbol: ${json.symbol}`, 'accent') : null,
      json.selector ? codeInline(`${json.selector.file}:${json.selector.start_line}`) : null,
      badge(`${fmt.num(json.call_sites_total ?? (json.call_sites || []).length)} call site(s)`, 'plain'),
      badge(`${fmt.num(json.value_references_total ?? (json.value_references || []).length)} value ref(s)`, 'plain'),
      badge(`${fmt.num(json.move_sites?.length ?? 0)} import site(s)`, 'plain'),
      badge(`blast ${fmt.num(json.blast_radius)}`, 'warn'),
      callGraphBadge(json.call_graph),
    ]),
    def ? card({ title: 'Definition', body: h('div.stack', [
      def.signature ? codeBlock({ text: def.signature, lang: langFromPath(def.file), maxLines: 6 }) : null,
      def.doc ? h('div.small.muted', { style: 'white-space:pre-wrap' }, def.doc) : null,
      def.source && !def.source_omitted ? codeBlock({ text: def.source, lang: langFromPath(def.file), startLine: def.start_line || 1, maxLines: 400 }) : null,
      h('div.btn-row', [h('button.btn.sm', { type: 'button', onclick: () => ctx.onSymbol?.(def) }, 'Open in source browser')]),
    ]) }) : null,
    json.call_sites?.length ? card({ title: `Call sites to update (${json.call_sites.length}${json.call_sites_total && json.call_sites_total !== json.call_sites.length ? ` of ${json.call_sites_total}` : ''})`, body: symbolTable(json.call_sites, ctx) }) : null,
    json.value_references?.length ? card({ title: `Value references (${json.value_references.length})`, body: symbolTable(json.value_references, ctx) }) : null,
    json.move_sites?.length
      ? card({
          title: `Files whose imports a move updates (${json.move_sites.length})`,
          body: h('div.pill-list', json.move_sites.map((f) => chip(typeof f === 'string' ? f : f.file || JSON.stringify(f), () => ctx.onFile?.(typeof f === 'string' ? f : f.file)))),
        })
      : null,
    json.covering_tests?.length ? card({ title: `Covering tests to re-run (${json.covering_tests.length}${json.tests_total ? ` of ${json.tests_total}` : ''})`, body: symList(json.covering_tests, { onPick: ctx.onSymbol }) }) : callout('warn', 'No covering tests', 'A rename here has no test safety net in the indexed graph.'),
  ])
}

function renderReview(json, ctx) {
  const risk = json.risk || {}
  const changed = json.changed_files || []
  const symbols = json.changed_symbols || []
  const gate = json.gate || {}
  return h('div.stack', [
    h('div.risk-hero', [
      riskDial(risk.level, risk.score),
      h('div.stack', { style: 'gap:8px' }, [
        h('div.row.gap3', [
          badge(`mode: ${json.mode || 'working'}`, 'accent'),
          json.is_repo === false ? badge('not a git repo', 'warn') : null,
          json.indexed === false ? badge('not indexed', 'warn') : null,
          boolBadge('analysis complete', json.analysis_complete, { okWhen: true }),
          json.stale ? staleBadge(true, json.staleness) : null,
        ]),
        countsRow([
          ['changed files', changed.length],
          ['total symbols', json.total_symbols],
          ['analyzed', json.analyzed_symbols],
          ['truncated', json.truncated_symbols],
          ['blast radius', (json.blast_radius || []).length],
          ['covering tests', (json.covering_tests || []).length],
          ['untested', (json.untested_symbols || []).length],
        ]),
        (risk.factors || []).length
          ? h('div.pill-list', risk.factors.map((f) => badge(`${f.factor || f.name}: ${f.detail || f.severity || ''}`, { high: 'danger', medium: 'warn', low: 'ok' }[f.severity] || 'plain')))
          : null,
        gate && Object.keys(gate).length
          ? callout(gate.would_fail_on ? 'danger' : 'ok', 'Gate', `risk level ${gate.risk_level ?? '?'} · analysis_complete ${String(gate.analysis_complete)} · would fail on: ${gateReasons(gate.would_fail_on)}`)
          : null,
        risk.level === 'unknown' ? callout('warn', 'Unknown risk', 'At least one changed symbol has no usable call graph — the aggregate is honest, not optimistic.') : null,
      ]),
    ]),
    json.deletion_analysis && Object.keys(json.deletion_analysis).length
      ? card({ title: 'Deletion analysis', body: h('div.stack', [
          countsRow([['files', json.deletion_analysis.files], ['analyzed', json.deletion_analysis.analyzed], ['missing', json.deletion_analysis.missing]]),
          boolBadge('complete', json.deletion_analysis.complete, { okWhen: true }),
          json.deletion_analysis.source_last_index ? kv([['source at last index', String(json.deletion_analysis.source_last_index)]]) : null,
        ]), tight: true })
      : null,
    changed.length
      ? card({
          title: `Changed files (${changed.length})`,
          body: table(
            [
              { key: 'path', label: 'File', cls: 'code', render: (r) => shortPath(r.path || r.file) },
              { key: 'status', label: 'Status', render: (r) => badge(r.status || r.change || '?', r.status === 'deleted' ? 'danger' : 'plain') },
              { key: 'added', label: '+', cls: 'num' },
              { key: 'removed', label: '−', cls: 'num' },
              { key: 'mapped_symbols', label: 'Mapped symbols', cls: 'num', render: (r) => fmt.num(r.mapped_symbols ?? r.symbols ?? 0) },
              { key: 'changed_lines', label: 'Changed lines', cls: 'num' },
            ],
            changed,
            { dense: true, onRow: (r) => ctx.onFile?.(r.path || r.file) },
          ),
        })
      : callout('ok', 'No changed files', json.mode === 'staged' ? 'Nothing is staged.' : 'The working tree matches HEAD.') ,
    symbols.length
      ? card({
          title: `Changed symbols (${symbols.length})`,
          body: symbolTable(symbols, ctx, [
            { key: 'risk_level', label: 'Risk', render: (r) => badge(r.risk?.level || r.risk_level || '?', { high: 'danger', medium: 'warn', low: 'ok', unknown: 'plain' }[r.risk?.level || r.risk_level] || 'plain') },
            { key: 'risk_score', label: 'Score', cls: 'num', render: (r) => (r.risk?.score ?? r.risk_score ?? undefined) !== undefined ? Number(r.risk?.score ?? r.risk_score).toFixed(2) : '—' },
            { key: 'blast', label: 'Blast', cls: 'num', render: (r) => fmt.num(r.blast_radius?.length ?? r.blast_radius ?? 0) },
            { key: 'tests', label: 'Tests', cls: 'num', render: (r) => fmt.num(r.covering_tests?.length ?? r.tests ?? 0) },
          ]),
        })
      : null,
    json.blast_radius?.length ? card({ title: `Combined blast radius (${json.blast_radius.length})`, body: symList(json.blast_radius, { onPick: ctx.onSymbol, meta: (r) => `depth ${r.depth}` }) }) : null,
    json.covering_tests?.length ? card({ title: `Tests to run (${json.covering_tests.length})`, body: symList(json.covering_tests, { onPick: ctx.onSymbol }) }) : null,
    json.untested_symbols?.length ? card({ title: `Untested changed symbols (${json.untested_symbols.length})`, body: symList(json.untested_symbols, { onPick: ctx.onSymbol }) }) : null,
    testCommandList(json.test_commands),
  ])
}

function renderContext(json, ctx) {
  if (Array.isArray(json.items) || Array.isArray(json.contexts)) return renderContextBatch(json, ctx)
  const def = (json.definitions || [])[0]
  return h('div.stack', [
    h('div.row.gap3', [
      json.symbol ? badge(`symbol: ${json.symbol}`, 'accent') : null,
      json.selector ? codeInline(`${json.selector.file}:${json.selector.start_line}`) : null,
      badge(`callers ${json.callers_total ?? (json.callers || []).length}`, 'plain'),
      badge(`callees ${json.callees_total ?? (json.callees || []).length}`, 'plain'),
      badge(`tests ${json.tests_total ?? (json.tests || []).length}`, json.tests_total ? 'ok' : 'danger'),
      badge(`blast ${fmt.num(json.blast_radius)}`, 'warn'),
      callGraphBadge(json.call_graph),
    ]),
    def
      ? card({
          title: def.fqn || def.symbol,
          sub: `${def.file}:${def.start_line}${def.end_line ? `–${def.end_line}` : ''}`,
          actions: [
            h('button.btn.sm', { type: 'button', onclick: () => ctx.onSymbol?.(def) }, 'Open in browser'),
            h('button.btn.sm', { type: 'button', onclick: () => ctx.onPrefill?.('impact', { at: `${def.file}:${def.start_line}` }) }, 'Impact'),
            h('button.btn.sm', { type: 'button', onclick: () => ctx.onPrefill?.('risk', { at: `${def.file}:${def.start_line}` }) }, 'Risk'),
            h('button.btn.sm', { type: 'button', onclick: () => ctx.onPrefill?.('annotate', { target: def.fqn || def.symbol }) }, 'Annotate'),
          ],
          body: h('div.stack', [
            kindBadge(def.kind),
            def.doc ? h('div.small.muted', { style: 'white-space:pre-wrap;line-height:1.6' }, def.doc) : null,
            def.source && !def.source_omitted
              ? codeBlock({ text: def.source, lang: langFromPath(def.file), startLine: def.start_line || 1, maxLines: 600, onLineClick: (ln) => ctx.onSymbol?.({ ...def, start_line: ln }) })
              : def.source_omitted
                ? callout('info', 'Body omitted', 'Run with --brief off, or use the Source feature, to fetch the body.')
                : null,
          ]),
        })
      : emptyState({ title: 'No definition found', note: 'The symbol did not resolve to an indexed definition.' }),
    relationBlock('Callers', json.callers, json.callers_total, ctx),
    relationBlock('Callees', json.callees, json.callees_total, ctx),
    relationBlock('Value references', json.references, json.references_total, ctx),
    json.references_resolution ? callout('warn', 'reference coverage', json.references_resolution) : null,
    relationBlock('Covering tests', json.tests, json.tests_total, ctx),
    testCommandList(json.test_commands),
  ])
}

function renderContextBatch(json, ctx) {
  const items = json.items || json.contexts || []
  return h('div.stack', [
    countsRow([['symbols', items.length], ['shared callers', (json.shared_callers || []).length]]),
    json.shared_callers?.length ? card({ title: 'Shared callers', body: symList(json.shared_callers, { onPick: ctx.onSymbol }), tight: true }) : null,
    ...items.map((it) => card({ title: it.symbol || it.fqn || 'context', body: renderContext(it, ctx), tight: true })),
  ])
}

function renderTaskContext(json, ctx) {
  const blocks = []
  if (json.freshness) blocks.push(card({ title: 'Freshness', body: h('div.stack', [
    countsRow([['checked', String(json.freshness.checked)], ['stale', String(json.freshness.stale)]]),
    json.freshness.staleness ? countsRow([['changed', json.freshness.staleness.changed], ['new', json.freshness.staleness.new], ['deleted', json.freshness.staleness.deleted]]) : null,
  ]), tight: true }))
  if (json.explore) blocks.push(card({ title: 'Explore seeds', body: renderExplore(json.explore, ctx), tight: true }))
  if (json.contexts?.length) blocks.push(card({ title: `Contexts (${json.contexts.length})`, body: h('div.stack', json.contexts.map((c) => renderContext(c, ctx))), tight: true }))
  if (json.impact?.length) blocks.push(card({ title: `Impact drill-downs (${json.impact.length})`, body: h('div.stack', json.impact.map((c) => renderImpact(c, ctx))), tight: true }))
  if (json.related_files?.length) blocks.push(card({ title: 'Related files', body: renderRelated({ related: json.related_files }, ctx), tight: true }))
  return h('div.stack', [
    h('div.row.gap3', [
      badge(`task: ${json.task}`, 'accent'),
      badge(`mode: ${json.mode}`, 'info'),
      json.indexed === false ? badge('not indexed', 'warn') : null,
      callGraphBadge(json.call_graph),
    ]),
    ...blocks,
    genericReport(json, ctx, null, null, { skip: new Set(['freshness', 'explore', 'contexts', 'impact', 'related_files', 'task', 'mode', 'indexed', 'call_graph', 'note', 'schema_version', 'project', 'next']) }),
  ])
}

function renderSource(json, ctx) {
  const matches = json.matches || []
  if (!matches.length) return emptyState({ title: 'No source', note: 'Nothing matched that selector.' })
  return h('div.stack', matches.map((m) => card({
    title: m.fqn || m.symbol,
    sub: `${m.file}:${m.start_line}${m.end_line ? `–${m.end_line}` : ''}`,
    actions: [
      kindBadge(m.kind),
      h('button.btn.sm', { type: 'button', onclick: () => ctx.onSymbol?.(m) }, 'Open in browser'),
      h('button.btn.sm', { type: 'button', onclick: (e) => copy(m.source || '').then(() => (e.target.textContent = 'copied')) }, 'Copy source'),
    ],
    body: h('div.stack', [
      m.doc ? h('div.small.muted', { style: 'white-space:pre-wrap' }, m.doc) : null,
      m.source && !m.source_omitted
        ? codeBlock({ text: m.source, lang: langFromPath(m.file), startLine: m.start_line || 1, maxLines: 1200 })
        : callout('info', 'Body omitted', 'Re-run without --brief.'),
    ]),
  })))
}

function renderAnnotations(json, ctx) {
  const items = json.annotations || (Array.isArray(json) ? json : [])
  return h('div.stack', [
    json.project ? h('div.row.gap2', [badge(json.project, 'plain'), badge(`${items.length} annotation(s)`, 'accent')]) : null,
    items.length
      ? table(
          [
            { key: 'id', label: 'id', cls: 'num' },
            { key: 'source', label: 'Source', render: (r) => badge(r.source || 'note', 'info') },
            { key: 'target', label: 'Target', cls: 'code', render: (r) => r.target || r.symbol || (r.from && r.to ? `${r.from} → ${r.to}` : r.fqn || '—') },
            { key: 'note', label: 'Note', render: (r) => h('span.small', { style: 'white-space:pre-wrap' }, r.note || '') },
            { key: 'data', label: 'Data', render: (r) => (r.data ? codeInline(String(r.data).slice(0, 120)) : '—') },
            { key: 'external_id', label: 'External id', render: (r) => h('span.mono.small', r.external_id || '—') },
            { key: 'actions', label: '', sort: false, render: (r) => h('div.btn-row', [
              h('button.btn.sm', { type: 'button', onclick: (e) => { e.stopPropagation(); ctx.onPrefill?.('annotate', { retarget: r.id, target: '' }) } }, 'retarget'),
              h('button.btn.sm.danger', { type: 'button', onclick: (e) => { e.stopPropagation(); ctx.onPrefill?.('annotations', { rm: r.id }) } }, 'remove'),
            ]) },
          ],
          items,
          { onRow: (r) => (r.target || r.symbol) && ctx.onSymbolQuery?.(r.target || r.symbol) },
        )
      : emptyState({ icon: '✎', title: 'No annotations yet', note: 'Pin a note or external data onto a symbol or a from→to call path with the Annotate feature.' }),
    h('div.btn-row', [h('button.btn', { type: 'button', onclick: () => ctx.onFeature?.('annotate') }, '＋ New annotation')]),
  ])
}

function renderAgents(json, ctx, feat, result) {
  const rows = Array.isArray(json) ? json : json.harnesses || json.agents || []
  if (!rows.length) return genericReport(json, ctx, feat, result)
  return h('div.stack', [
    table(
      [
        { key: 'name', label: 'Harness', render: (r) => h('span.mono', r.name || r.id || '') },
        { key: 'detected', label: 'Detected here', render: (r) => boolBadge('', !!r.detected) },
        { key: 'registered', label: 'codemap registered', render: (r) => boolBadge('', !!r.registered) },
        { key: 'mcp', label: 'MCP config', render: (r) => (r.mcp_path || r.config ? codeInline(shortPath(r.mcp_path || r.config)) : '—') },
        { key: 'guidance', label: 'Guidance file', render: (r) => (r.guidance_path || r.playbook ? codeInline(shortPath(r.guidance_path || r.playbook)) : '—') },
        { key: 'profile', label: 'Profile', render: (r) => (r.profile ? badge(r.profile, 'info') : '—') },
        { key: 'act', label: '', sort: false, render: (r) => h('button.btn.sm', { type: 'button', onclick: (e) => { e.stopPropagation(); ctx.onPrefill?.('agent-setup', { harness: r.name || r.id, dry_run: true }) } }, 'setup (dry run)') },
      ],
      rows,
      { dense: true },
    ),
    callout('info', 'Registration writes config', 'agent setup writes the MCP server entry plus a generated guidance file for that harness. Always preview with --dry-run first; the guidance is generated from the same playbook the CLI prints, never hand-typed.'),
  ])
}

function renderCache(json, ctx) {
  const entries = json.entries || []
  return h('div.stack', [
    json.repo_hash ? kv([['repo hash', json.repo_hash]]) : null,
    entries.length
      ? table(
          [
            { key: 'tree_hash', label: 'Tree hash', cls: 'code', render: (r) => codeInline(String(r.tree_hash || r.tree || '').slice(0, 16)) },
            { key: 'branch', label: 'Branch', render: (r) => h('span.mono', r.branch || '—') },
            { key: 'saved_at', label: 'Saved', render: (r) => h('span.mono.small', r.saved_at || r.created_at || '—') },
            { key: 'nodes', label: 'Nodes', cls: 'num' },
            { key: 'stash_id', label: 'Stash', render: (r) => codeInline(String(r.stash_id || '').slice(0, 18)) },
            { key: 'act', label: '', sort: false, render: (r) => h('button.btn.sm.danger', { type: 'button', onclick: (e) => { e.stopPropagation(); ctx.onPrefill?.('cache-drop', { tree: r.tree_hash || r.tree }) } }, 'drop') },
          ],
          entries,
          { dense: true },
        )
      : emptyState({ icon: '⌸', title: 'No cached snapshots', note: 'Save the current index into the fcheap vault, or export a portable tarball for CI.' }),
    h('div.btn-row', [
      h('button.btn', { type: 'button', onclick: () => ctx.onFeature?.('cache-save') }, 'Save'),
      h('button.btn', { type: 'button', onclick: () => ctx.onFeature?.('cache-restore') }, 'Restore'),
      h('button.btn', { type: 'button', onclick: () => ctx.onFeature?.('cache-export') }, 'Export tarball'),
      h('button.btn', { type: 'button', onclick: () => ctx.onFeature?.('cache-import') }, 'Import tarball'),
    ]),
  ])
}

function renderDaemon(json, ctx) {
  const running = !!json.running
  return h('div.stack', [
    h('div.grid.c3', [
      metric('Daemon', running ? 'running' : 'stopped', running ? `pid ${json.pid ?? '?'}` : 'not watching', running ? 'ok' : 'warn'),
      metric('Watching', json.project || json.root || '—', ''),
      metric('Debounce', json.debounce || '—', 'coalescing window'),
    ]),
    json.error ? callout('warn', 'daemon', String(json.error)) : null,
    h('div.btn-row', [
      running ? h('button.btn.danger', { type: 'button', onclick: () => ctx.onFeature?.('daemon-stop') }, 'Stop daemon') : null,
      h('button.btn.primary', { type: 'button', onclick: () => ctx.onFeature?.('daemon-start') }, 'Start watching'),
      h('button.btn', { type: 'button', onclick: () => ctx.onFeature?.('index') }, 'Index once'),
    ]),
    genericReport(json, ctx, null, null, { skip: new Set(['running', 'error', 'project', 'root', 'debounce', 'pid']) }),
  ])
}

function renderSecrets(json, ctx, feat, result) {
  const keys = json.keys || json.results || json.required_keys || []
  const isRequired = (feat?.id === 'required-keys')
  return h('div.stack', [
    callout('info', 'Value-free by contract', 'codemap only ever reports key NAMES. No secret value is read, stored, or transmitted.'),
    json.entrypoint ? kv([['entrypoint', json.entrypoint], ['depth', json.depth]]) : null,
    isRequired
      ? h('div.pill-list', (json.required || json.keys || []).map((k) => badge(typeof k === 'string' ? k : k.key || k.name || JSON.stringify(k), 'ok')))
      : null,
    keys.length && !isRequired
      ? h('div.stack', keys.map((k) => card({
          title: k.key || k.name || '(key)',
          sub: `${(k.readers || []).length} reader(s) · blast ${fmt.num(k.blast_radius?.length ?? k.blast_radius ?? 0)}`,
          body: h('div.stack', [
            k.readers?.length ? symList(k.readers, { onPick: ctx.onSymbol }) : emptyState({ note: 'no indexed reader' }),
            k.covering_tests?.length ? card({ title: 'Covering tests', body: symList(k.covering_tests, { onPick: ctx.onSymbol }), tight: true }) : null,
          ]),
        })))
      : null,
    genericReport(json, ctx, feat, result, { skip: new Set(['keys', 'results', 'required_keys', 'required', 'entrypoint', 'depth']) }),
  ])
}

// --------------------------------------------------------------- generic

/**
 * Shape-driven renderer: unknown reports still get a useful, honest view.
 * Arrays of symbol-shaped objects become clickable lists, arrays of objects
 * become tables, nested objects become cards, scalars become key/value rows.
 */
export function genericReport(json, ctx, feat, result, { skip = new Set(), depth = 0 } = {}) {
  if (json === null || json === undefined) return emptyState({ note: 'empty result' })
  if (Array.isArray(json)) return renderValueArray(json, ctx, depth)
  if (typeof json !== 'object') return h('div.json', jsonTree(json))

  const out = []
  const scalars = []
  for (const [key, value] of Object.entries(json)) {
    if (skip.has(key) || HIDDEN_KEYS.has(key)) continue
    if (value === undefined || value === null) continue
    if (CALLOUT_KEYS[key] && typeof value === 'string') continue // already shown as a callout
    if (Array.isArray(value)) {
      if (!value.length) {
        scalars.push([key, '(empty list)'])
        continue
      }
      out.push(h('div.section', [
        h('div.section-head', [h('h2', humanKey(key)), h('span.small.dim', `${value.length} item(s)`)]),
        renderValueArray(value, ctx, depth),
      ]))
      continue
    }
    if (typeof value === 'object') {
      out.push(card({ title: humanKey(key), body: genericReport(value, ctx, feat, result, { depth: depth + 1 }), tight: true }))
      continue
    }
    scalars.push([key, formatScalar(key, value)])
  }
  if (scalars.length) out.unshift(card({ title: depth === 0 ? 'Summary' : 'Fields', body: kv(scalars), tight: true }))
  if (!out.length) return emptyState({ note: 'The report carried no displayable fields — check Raw output.' })
  return h('div.stack', out)
}

function formatScalar(key, value) {
  if (typeof value === 'boolean') return value ? 'yes' : 'no'
  if (typeof value === 'number') return /(_ms|_at)$/.test(key) ? (/_ms$/.test(key) ? fmt.ms(value) : fmt.time(value)) : fmt.num(value)
  const s = String(value)
  return s.length > 240 ? h('span', { style: 'white-space:pre-wrap;word-break:break-word' }, s) : s
}

function renderValueArray(items, ctx, depth = 0) {
  if (!items.length) return emptyState({ note: 'empty list' })
  const rows = items.map((it) => symOf(it)).filter(Boolean)
  const first = rows[0]
  if (typeof first !== 'object' || first === null) {
    return h('div.pill-list', rows.slice(0, 400).map((v) => badge(String(v), 'plain')))
  }
  if (symbolShaped(first)) {
    return h('div.symlist', rows.slice(0, 800).map((it) => symRow(it, { onPick: ctx.onSymbol, meta: symbolMeta })))
  }
  const columns = inferColumns(rows)
  return table(columns, rows, { dense: depth > 0, max: 1000, onRow: (row) => {
    const file = row.file || row.path || row.relative_path
    if (file && ctx.onFile) ctx.onFile(file)
  } })
}

function symbolMeta(r) {
  return [r.signature, r.reason, r.doc ? r.doc.slice(0, 140) : null, r.score ? `score ${Number(r.score).toFixed(3)}` : null].filter(Boolean).join(' · ')
}

const PREFERRED = ['kind', 'symbol', 'fqn', 'name', 'file', 'path', 'relative_path', 'start_line', 'line', 'language', 'lang', 'confidence', 'call_graph', 'reason', 'detail', 'note', 'severity', 'status']

function inferColumns(items) {
  const keys = new Set()
  for (const it of items.slice(0, 40)) for (const k of Object.keys(it || {})) keys.add(k)
  const ordered = PREFERRED.filter((k) => keys.has(k)).concat([...keys].filter((k) => !PREFERRED.includes(k)))
  return ordered.slice(0, 9).map((key) => ({
    key,
    label: humanKey(key),
    cls: items.every((it) => typeof it?.[key] === 'number') ? 'num' : key === 'file' || key === 'path' || key === 'relative_path' ? 'code' : '',
    render: (row) => renderCell(row?.[key], key, row),
  }))
}

function renderCell(value, key, row) {
  if (value === undefined || value === null || value === '') return '—'
  if (typeof value === 'boolean') return boolBadge('', value)
  if (Array.isArray(value)) return value.length ? h('div.pill-list', value.slice(0, 6).map((v) => badge(typeof v === 'object' ? (v.name || v.symbol || v.kind || JSON.stringify(v)) : String(v), 'plain'))) : '—'
  if (typeof value === 'object') {
    if ((key === 'symbol' || key === 'fqn') && (value.symbol || value.fqn)) return String(value.symbol || value.fqn)
    return codeInline(JSON.stringify(value).slice(0, 90))
  }
  if (key === 'call_graph') return callGraphBadge(value)
  if (key === 'confidence' || key === 'severity' || key === 'level' || key === 'provenance') {
    const tone = { confirmed: 'ok', resolved: 'ok', low: 'ok', candidate: 'warn', medium: 'warn', name: 'warn', high: 'danger', unresolved: 'danger' }[value] || 'plain'
    return badge(String(value), tone)
  }
  if (key === 'kind') return kindBadge(value)
  if (typeof value === 'number') return /_ms$/.test(key) ? fmt.ms(value) : fmt.num(value)
  const s = String(value)
  if (key === 'file' || key === 'path' || key === 'relative_path') return shortPath(s)
  if (s.length > 110) return h('span.small', { title: s }, `${s.slice(0, 108)}…`)
  return s
}

function humanKey(key) {
  return String(key).replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase())
}

// ------------------------------------------------- graph payload extraction

/** Build a {nodes, edges} payload from any report that carries relations. */
export function graphPayloadFor(feat, json) {
  if (!json || typeof json !== 'object') return null
  const nodes = new Map()
  const edges = []
  const addNode = (o, role) => {
    if (!o || typeof o !== 'object') return null
    const file = o.file || o.selector?.file || o.relative_path
    const line = o.start_line ?? o.selector?.start_line ?? o.line
    if (!file) return null
    const id = `${file}:${line ?? 0}:${o.fqn || o.symbol || ''}`
    if (!nodes.has(id)) {
      nodes.set(id, {
        id,
        label: o.symbol || o.fqn || file.split('/').pop() || id,
        fqn: o.fqn || o.symbol || '',
        kind: o.kind || 'symbol',
        file,
        line: line ?? 0,
        role,
        in_degree: o.in_degree,
        depth: o.depth,
      })
    } else if (role === 'focus') {
      nodes.get(id).role = 'focus'
    }
    return id
  }

  const focusId = addNode(json.selector ? { ...json.selector, symbol: json.symbol, kind: json.selector.kind } : null, 'focus')
    || (json.locations?.[0] ? addNode(json.locations[0], 'focus') : null)
    || (json.definitions?.[0] ? addNode(json.definitions[0], 'focus') : null)
    || (json.start ? addNode({ ...json.start, symbol: json.start.fqn }, 'focus') : null)
    || (json.symbol && json.file ? addNode({ symbol: json.symbol, file: json.file, start_line: json.start_line }, 'focus') : null)

  // Several reports reuse a relation key for a COUNT (context/refactor-plan
  // carry blast_radius as a number), so every consumer must type-check first.
  const addEdges = (items, type, direction) => {
    if (!Array.isArray(items)) return
    for (const it of items) {
      if (!it || typeof it !== 'object') continue
      const id = addNode(it, direction === 'in' ? 'caller' : direction === 'out' ? 'callee' : 'related')
      if (!id) continue
      if (focusId) edges.push(direction === 'in' ? { source: id, target: focusId, type } : { source: focusId, target: id, type })
      else if (it.parent_selector) {
        const pid = addNode({ ...it.parent_selector, symbol: it.symbol }, 'related')
        if (pid) edges.push({ source: pid, target: id, type })
      }
    }
  }

  if (feat?.id === 'traverse' || json.hops) {
    for (const raw of json.hops || []) {
      const hop = symOf(raw)
      const id = addNode(hop, 'related')
      const pid = hop.parent_selector ? addNode({ ...hop.parent_selector }, 'related') : focusId
      if (id && pid) edges.push({ source: hop.direction === 'incoming' ? id : pid, target: hop.direction === 'incoming' ? pid : id, type: hop.edge_type || 'edge', confidence: hop.confidence })
    }
  } else {
    addEdges(json.callers || json.direct_callers, 'calls', 'in')
    addEdges(json.callees, 'calls', 'out')
    addEdges(json.results, feat?.id === 'callers' ? 'calls' : feat?.id === 'callees' ? 'calls' : 'edge', feat?.id === 'callers' ? 'in' : 'out')
    addEdges(json.references, 'references', 'in')
    addEdges(json.blast_radius, 'blast', 'in')
    addEdges(json.tests || json.covering_tests, 'tests', 'in')
    addEdges(json.path, 'path', 'out')
    addEdges(json.call_sites, 'calls', 'in')
    addEdges(json.value_references, 'references', 'in')
    addEdges(json.seeds, 'seed', 'out')
  }

  // path is a chain, not a star
  if (json.path?.length > 1) {
    edges.length = 0
    nodes.clear()
    let prev = null
    for (const p of json.path) {
      const id = addNode(p, prev === null ? 'focus' : 'related')
      if (prev && id) edges.push({ source: prev, target: id, type: 'calls' })
      prev = id
    }
  }

  if (nodes.size < 2) return null
  return {
    nodes: [...nodes.values()],
    edges,
    title: json.symbol || json.file || json.query || feat?.title || 'graph',
    subtitle: `${nodes.size} nodes · ${edges.length} edges · call_graph ${json.call_graph || '?'}`,
  }
}
