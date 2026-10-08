/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Processes: `codemap processes` as a browsable list of execution flows — one
// per entry point with a resolved handler (CLI command, route, RPC tool, page,
// program) — with the ordered steps of the selected one next to it. Nothing is
// stored: every flow is computed on demand from the graph.

import { h, mount } from '../dom.mjs'
import { runArgs } from '../runner.mjs'
import { badge, callGraphBadge, callout, card, emptyState, kindBadge, spinner, fmt } from '../components.mjs'
import { reportCallouts } from '../learn-ui.mjs'

export const PROCESS_KINDS = [
  { v: 'cli_command', label: 'CLI commands' },
  { v: 'http_route', label: 'HTTP routes' },
  { v: 'api_route', label: 'API routes' },
  { v: 'rpc_tool', label: 'RPC / MCP tools' },
  { v: 'page', label: 'Pages' },
  { v: 'program', label: 'Programs' },
]

export function processArgs({ query = '', kinds = [], top = 100, depth = 4, maxSteps = 40 } = {}) {
  const args = ['processes', '--top', String(top), '--depth', String(depth), '--max-steps', String(maxSteps)]
  if (kinds.length) args.push('--kind', kinds.join(','))
  if (query.trim()) args.push('--query', query.trim())
  return args
}

export function processesView(ctx, opts = {}) {
  const host = h('div.view')
  const out = h('div.stack')
  let query = opts.query || ''
  let kinds = []
  let depth = 4
  let json = null
  let selected = null

  async function load() {
    mount(out, [head(), spinner('tracing every entry point…')])
    const res = await runArgs(processArgs({ query, kinds, depth }), { featureId: 'processes', quiet: true })
    json = res?.ok ? res.json : null
    const list = json?.processes || []
    selected = list.find((p) => p.id === selected?.id) || list[0] || null
    render(res)
  }

  function head() {
    const input = h('input', {
      type: 'search',
      value: query,
      placeholder: 'how does signup work · review · index',
      style: 'width:280px',
      onkeydown: (e) => {
        if (e.key === 'Enter') {
          query = e.target.value
          load()
        }
      },
    })
    return h('div.view-head', [
      h('div.vh-main', [
        h('h1', ['Processes', json ? badge(`${fmt.num((json.processes || []).length)} of ${fmt.num(json.processes_total ?? 0)}`, 'accent') : null, json ? callGraphBadge(json.call_graph, json.resolution) : null]),
        h('div.vh-sub', 'Every entry point traced through its handler into the code it reaches, in call order. Pick one to read the steps; open it in Flow for the full tree with docs.'),
      ]),
      h('div.vh-actions', h('div.btn-row', [
        input,
        h('select', { style: 'width:auto', title: 'call depth', onchange: (e) => ((depth = Number(e.target.value)), load()) }, [2, 3, 4, 6, 8].map((d) => h('option', { value: String(d), selected: d === depth }, `depth ${d}`))),
        h('button.btn.primary', { type: 'button', onclick: () => ((query = input.value), load()) }, 'Search'),
      ])),
    ])
  }

  function kindChips() {
    const counts = new Map()
    for (const p of json?.processes || []) counts.set(p.kind, (counts.get(p.kind) || 0) + 1)
    return h('div.btn-row', [
      h('button.btn.sm', { type: 'button', class: kinds.length ? 'btn sm' : 'btn sm on', onclick: () => ((kinds = []), load()) }, 'All kinds'),
      ...PROCESS_KINDS.map((k) =>
        h('button.btn.sm', {
          type: 'button',
          class: kinds.includes(k.v) ? 'btn sm on' : 'btn sm',
          onclick: () => {
            kinds = kinds.includes(k.v) ? kinds.filter((x) => x !== k.v) : [...kinds, k.v]
            load()
          },
        }, kinds.length || !counts.get(k.v) ? k.label : `${k.label} · ${counts.get(k.v)}`),
      ),
    ])
  }

  function render(res) {
    const nodes = [head()]
    if (res && !res.ok) {
      nodes.push(callout(res.code === 'index_missing' || res.code === 'not_indexed' ? 'warn' : 'danger', 'codemap processes failed', res.error || '', [
        h('button.btn.sm.primary', { type: 'button', onclick: () => ctx.openFeature('index') }, 'Index this project'),
      ]))
      mount(out, nodes)
      return
    }
    nodes.push(kindChips())
    nodes.push(...reportCallouts(json, { resolution: false }))
    if (json?.stale) nodes.push(callout('warn', 'Stale index', 'Reindex before treating these flows as current.'))
    const list = json?.processes || []
    if (!list.length) {
      nodes.push(emptyState({ icon: '⇢', title: 'No processes', note: query ? `No entry flow matches “${query}”.` : 'No entry point with a resolved handler was found.' }))
      mount(out, nodes)
      return
    }
    const listHost = h('div.symlist', { style: 'max-height:72vh;overflow:auto' })
    const detail = h('div')
    const drawList = () =>
      mount(listHost, list.map((p) =>
        h('button.symrow', {
          type: 'button',
          class: p.id === selected?.id ? 'symrow active' : 'symrow',
          style: 'grid-template-columns:auto minmax(0,1fr) auto',
          onclick: () => {
            selected = p
            drawList()
            mount(detail, processDetail(p, ctx))
          },
        }, [kindBadge(kindLabel(p.kind)), h('span.sn', { title: p.id }, p.name), h('span.sp', `${p.steps_total ?? (p.steps || []).length} steps · ${(p.files || []).length} files`)]),
      ))
    drawList()
    mount(detail, selected ? processDetail(selected, ctx) : null)
    nodes.push(h('div.split', [card({ title: 'Entry points', body: listHost, tight: true }), detail]))
    if (json?.truncated) nodes.push(callout('info', 'More processes exist', 'Narrow with a query or a kind to see the rest.'))
    mount(out, nodes)
  }

  mount(host, out)
  load()
  return {
    node: host,
    reload: load,
    search(q) {
      query = q || ''
      load()
    },
  }
}

function kindLabel(kind) {
  return String(kind || '').replace(/_/g, ' ')
}

/** The steps of one process, indented by call depth, each opening its source. */
export function processDetail(p, ctx) {
  const steps = p.steps || []
  return card({
    title: p.name,
    sub: `${kindLabel(p.kind)} · ${p.entry?.file || ''}${p.entry?.start_line ? `:${p.entry.start_line}` : ''}`,
    actions: [
      callGraphBadge(p.call_graph),
      h('button.btn.sm', { type: 'button', onclick: () => ctx.openFile(p.entry?.file, p.entry?.start_line || 0) }, 'Entry source'),
      h('button.btn.sm.primary', { type: 'button', onclick: () => ctx.go('flow', { selector: p.entry, symbol: p.entry?.fqn, label: p.name }) }, '⇢ Open in Flow'),
    ],
    body: h('div.stack', [
      h('div.fl-list', steps.map((s, i) =>
        h('button.fl-row', { type: 'button', style: { paddingLeft: `${10 + (s.depth || 0) * 14}px` }, onclick: () => ctx.openSymbol(s) }, [
          h('span.dim.mono', { style: 'min-width:2.4em;text-align:right' }, String(i + 1)),
          s.kind ? kindBadge(s.kind) : null,
          h('span.fl-name', s.symbol || s.fqn || '(unresolved)'),
          s.file ? h('span.fl-loc', `${s.file}:${s.start_line}`) : null,
        ]),
      )),
      p.truncated ? callout('info', `${p.steps_total - steps.length} more step(s)`, 'Open it in Flow to see the whole tree.') : null,
      (p.files || []).length ? h('details', [h('summary.small.muted', `${p.files.length} file(s) touched`), h('div.symlist', { style: 'margin-top:6px' }, p.files.map((f) => h('button.symrow', { type: 'button', onclick: () => ctx.openFile(f) }, h('span.sn', f))))]) : null,
    ]),
    tight: true,
  })
}
