/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Unified search: one box, four codemap retrieval modes (name, indexed text,
// semantic, intent) run in parallel, each keeping its own honesty signals.

import { h, clear, mount, debounce } from '../dom.mjs'
import { runArgs } from '../runner.mjs'
import { badge, callout, card, emptyState, kindBadge, spinner, symRow, toast, fmt, shortPath, callGraphBadge, chip } from '../components.mjs'
import { state } from '../state.mjs'

const ENGINES = [
  { id: 'find', label: 'Name', hint: 'codemap find — symbol/FQN name search, offline, no embeddings', icon: '⌘' },
  { id: 'grep', label: 'Text', hint: 'codemap grep — exact text in indexed files, joined onto the enclosing symbol', icon: '⌕' },
  { id: 'semantic', label: 'Meaning', hint: 'codemap semantic — vector + BM25 hybrid over node source text', icon: '◈' },
  { id: 'explore', label: 'Intent', hint: 'codemap explore — intent query promoted to durable selectors plus exact neighborhoods', icon: '⇄' },
]

export function searchView(ctx, initial = {}) {
  const host = h('div.view')
  const results = h('div.stack')
  let query = initial.query || ''
  let engine = initial.engine || 'all'
  let top = 12
  let running = false
  let last = {}

  const input = h('input', {
    type: 'search',
    value: query,
    placeholder: 'Search symbols, text, meaning, or describe an intent…  (⌘K works from anywhere)',
    spellcheck: 'false',
    oninput: debounce((e) => {
      query = e.target.value
      if (query.trim().length >= 2 && state.settings.autoSearch !== false) run()
    }, 380),
    onkeydown: (e) => {
      if (e.key === 'Enter') {
        query = e.target.value
        run()
      }
    },
  })

  function engineArgs(id, q) {
    switch (id) {
      case 'find':
        return ['find', q, '--top', String(top * 2)]
      case 'grep':
        return ['grep', q, '--top', String(top * 2), '-i']
      case 'semantic':
        return ['semantic', q, '--top', String(top)]
      case 'explore':
        return ['explore', q, '--seeds', String(Math.min(6, top)), '--edges', '4', '--depth', '2']
      default:
        return null
    }
  }

  async function run() {
    const q = query.trim()
    if (!q || running) return
    running = true
    const recent = [q, ...(state.settings.recentSearches || []).filter((x) => x !== q)].slice(0, 10)
    state.settings.recentSearches = recent
    window.studio.settings.set({ recentSearches: recent })
    mount(results, spinner('searching…'))
    const ids = engine === 'all' ? ENGINES.map((e) => e.id) : [engine]
    const entries = await Promise.all(
      ids.map(async (id) => {
        const args = engineArgs(id, q)
        const res = await runArgs(args, { featureId: `__search_${id}` })
        return [id, res]
      }),
    )
    last = Object.fromEntries(entries)
    running = false
    render(q)
  }

  function render(q) {
    const nodes = []
    if (!q || !q.trim()) {
      nodes.push(
        h('div.empty', { style: 'padding:56px 24px' }, [
          h('div.e-ico', '⌕'),
          h('div.e-title', 'Four ways to ask codemap a question'),
          h('div.e-note', 'Name and text are exact and offline. Meaning is the vector + BM25 hybrid over indexed source. Intent promotes hits to durable selectors and joins their exact callers/callees/tests neighborhoods.'),
          h('div.row.gap2', { style: 'justify-content:center;margin-top:14px;flex-wrap:wrap' }, [
            ...[
              ['Review', 'find'],
              ['blast radius', 'grep'],
              ['where is the index stored', 'semantic'],
              ['how does review map a diff to symbols', 'explore'],
              ['TODO(security)', 'grep'],
              ['who calls Review', 'find'],
            ].map(([text, eng]) => chip([h('span.dim', `${eng}: `), text], () => { query = text; input.value = text; engine = 'all'; run() })),
          ]),
        ]),
      )
      mount(results, nodes)
      return
    }
    for (const e of ENGINES) {
      if (engine !== 'all' && engine !== e.id) continue
      const res = last[e.id]
      if (!res) continue
      nodes.push(enginePanel(e, q, res))
    }
    if (!nodes.length) nodes.push(emptyState({ title: 'Nothing to show', note: 'Type a query above and press Enter.' }))
    mount(results, nodes)
  }

  function enginePanel(e, q, res) {
    if (!res.ok) {
      return card({
        title: `${e.icon} ${e.label}`,
        sub: e.hint,
        body: callout(res.code === 'not_indexed' || res.code === 'index_missing' ? 'warn' : 'danger', res.code || 'failed', [h('div', res.error || ''), res.hint ? h('div.small.muted', { style: 'margin-top:4px' }, res.hint) : null]),
      })
    }
    const json = res.json || {}
    if (e.id === 'grep') return grepPanel(e, json)
    if (e.id === 'explore') return explorePanel(e, json)

    const hits = json.hits || json.results || json.matches || []
    return card({
      title: `${e.icon} ${e.label}`,
      sub: `${hits.length} hit(s) · ${e.hint}`,
      actions: [
        json.mode ? badge(`mode: ${json.mode}`, 'plain') : null,
        json.search_mode ? badge(`backend: ${json.search_mode}`, 'info') : null,
        callGraphBadge(json.call_graph),
        h('button.btn.sm', { type: 'button', onclick: () => ctx.openFeature(e.id === 'find' ? 'find' : 'semantic', e.id === 'find' ? { query: q } : { query: q }) }, 'Open feature'),
      ],
      body: hits.length
        ? h('div.symlist', hits.slice(0, top * 2).map((it) => symRow(it, { onPick: (x) => ctx.openSymbol(x), meta: (r) => [r.signature, r.score ? `score ${Number(r.score).toFixed(4)}` : null, r.matched_in ? `matched in ${r.matched_in}` : null].filter(Boolean).join(' · ') })))
        : emptyState({ note: `no ${e.label.toLowerCase()} hit for “${q}”` }),
    })
  }

  function grepPanel(e, json) {
    const hits = json.hits || []
    const groups = new Map()
    for (const hit of hits) {
      const f = hit.file || '(unknown)'
      if (!groups.has(f)) groups.set(f, [])
      groups.get(f).push(hit)
    }
    return card({
      title: `${e.icon} Text`,
      sub: `${json.total ?? hits.length} hit(s) across ${groups.size} file(s) · ${fmt.num(json.files_scanned)} indexed files scanned`,
      actions: [json.regex ? badge('regex', 'info') : badge('literal', 'plain'), json.ignore_case ? badge('-i', 'plain') : null, json.stale ? badge('stale set', 'warn') : null, h('button.btn.sm', { type: 'button', onclick: () => ctx.openFeature('grep', { pattern: query, ignore_case: true }) }, 'Open feature')],
      body: groups.size
        ? h('div.stack', { style: 'gap:8px;max-height:52vh;overflow:auto' }, [...groups.entries()].slice(0, 30).map(([file, rows]) => h('div.stack', { style: 'gap:2px' }, [
            h('div.row.gap2', [h('button.btn.sm.ghost', { type: 'button', onclick: () => ctx.openFile(file) }, h('span.mono', shortPath(file))), badge(`${rows.length}`, 'plain')]),
            ...rows.slice(0, 8).map((r) => h('button.symrow', {
              type: 'button',
              style: 'grid-template-columns:44px minmax(0,1fr)',
              onclick: () => ctx.openSymbol({ file: r.file || file, start_line: r.line, symbol: r.symbol || r.fqn, kind: r.kind, fqn: r.fqn }),
            }, [
              h('span.sk', { style: 'color:var(--accent)' }, String(r.line ?? '')),
              h('div', { style: 'min-width:0' }, [
                h('div.mono.small', { style: 'white-space:pre-wrap;word-break:break-word;color:var(--text)' }, (r.text || r.line_text || '').slice(0, 400)),
                r.symbol || r.fqn ? h('div.meta', `${r.kind || ''} ${r.fqn || r.symbol}${r.start_line ? `:${r.start_line}` : ''}`) : null,
              ]),
            ])),
          ])))
        : emptyState({ note: 'no exact text match in the indexed file set' }),
    })
  }

  function explorePanel(e, json) {
    const seeds = json.seeds || []
    const contexts = json.contexts || []
    return card({
      title: `${e.icon} Intent`,
      sub: `${seeds.length} seed(s), ${contexts.length} exact neighborhood(s) · ${e.hint}`,
      actions: [json.search_mode ? badge(`search: ${json.search_mode}`, 'info') : null, callGraphBadge(json.call_graph), h('button.btn.sm', { type: 'button', onclick: () => ctx.openFeature('explore', { query }) }, 'Open feature')],
      body: h('div.stack', [
        json.note ? callout('info', 'note', json.note) : null,
        seeds.length ? h('div.stack', { style: 'gap:4px' }, [h('h4', 'Seeds (durable selectors)'), h('div.symlist', seeds.map((s) => symRow(s, { onPick: (x) => ctx.openSymbol(x), meta: (r) => [r.signature, r.score ? `score ${Number(r.score).toFixed(3)}` : null].filter(Boolean).join(' · ') })))]) : null,
        contexts.length
          ? h('div.stack', { style: 'gap:8px' }, [
              h('h4', 'Neighborhoods'),
              ...contexts.map((c) => {
                const def = (c.definitions || [])[0]
                return h('div.card', { style: 'border-radius:9px' }, [
                  h('div.card-head', [
                    h('h3', def?.fqn || c.symbol || 'context'),
                    def ? h('span.small.dim', `${def.file}:${def.start_line}`) : null,
                    h('div.spacer'),
                    c.selector ? h('button.btn.sm', { type: 'button', onclick: () => ctx.openSymbol({ file: c.selector.file, start_line: c.selector.start_line, fqn: c.selector.fqn, kind: c.selector.kind, symbol: def?.symbol }) }, 'Open') : null,
                    c.selector ? h('button.btn.sm', { type: 'button', onclick: () => ctx.openFeature('context', { at: [`${c.selector.file}:${c.selector.start_line}`] }) }, 'Full context') : null,
                  ]),
                  h('div.card-body.tight', h('div.stack', { style: 'gap:6px' }, [
                    def?.doc ? h('div.small.muted', { style: 'white-space:pre-wrap' }, def.doc.slice(0, 400)) : null,
                    mini('callers', c.callers, c.callers_total),
                    mini('callees', c.callees, c.callees_total),
                    mini('references', c.references, c.references_total),
                    mini('tests', c.tests, c.tests_total),
                  ])),
                ])
              }),
            ])
          : null,
        !seeds.length && !contexts.length ? emptyState({ note: 'no seed was usable — try a more concrete phrase, or use Name/Text search' }) : null,
      ]),
    })
  }

  function mini(label, items, total) {
    if (!items || !items.length) return null
    return h('div.stack', { style: 'gap:2px' }, [
      h('div.small.dim', `${label}${total && total !== items.length ? ` (top ${items.length} of ${total})` : ` (${items.length})`}`),
      h('div.pill-list', items.slice(0, 6).map((it) => chip([kindBadge(it.kind), h('span.mono', ` ${it.symbol || it.fqn}`)], () => ctx.openSymbol(it)))),
    ])
  }

  const shell = h('div.stack', [
    h('div.view-head', [
      h('div.vh-main', [
        h('h1', 'Unified search'),
        h('div.vh-sub', 'Four retrieval modes over one store. Name and text are exact and offline; meaning is vector + BM25 hybrid; intent promotes hits to durable selectors and joins their exact neighborhoods.'),
      ]),
      h('div.vh-actions', [
        h('div.btn-row', [
          h('button.btn', { type: 'button', class: engine === 'all' ? 'btn on' : 'btn', onclick: () => { engine = 'all'; run() } }, 'All engines'),
          ...ENGINES.map((e) => h('button.btn', { type: 'button', class: engine === e.id ? 'btn on' : 'btn', title: e.hint, onclick: () => { engine = e.id; run() } }, e.label)),
        ]),
      ]),
    ]),
    h('div.card', [
      h('div.card-body', [
        h('div.field-row', [
          h('div', { style: 'flex:1;min-width:0' }, input),
          h('select', { style: 'width:auto', title: 'results per engine', onchange: (e) => { top = Number(e.target.value); run() } }, [5, 12, 25, 50].map((n) => h('option', { value: String(n), selected: top === n }, `top ${n}`))),
          h('button.btn.primary', { type: 'button', onclick: run }, '⌕ Search'),
        ]),
        h('div.row.gap2', { style: 'margin-top:8px' }, [
          h('span.small.dim', 'Recent:'),
          ...(state.settings.recentSearches || []).slice(0, 8).map((q) => chip(q, () => { query = q; input.value = q; run() })),
        ]),
      ]),
    ]),
    results,
  ])

  mount(host, shell)
  setTimeout(() => input.focus(), 30)
  if (query) run()

  return {
    node: host,
    search(q, eng) {
      query = q
      input.value = q
      if (eng) engine = eng
      run()
    },
  }
}
