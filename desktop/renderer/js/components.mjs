/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Shared UI primitives. Every view composes these so the app has one visual
// language: same badges, same tables, same callouts, same empty states.

import { h, clear, copy, download } from './dom.mjs'
import { tokenLines, langFromPath } from './highlight.mjs'
import { symOf } from './symbol.mjs'

export const fmt = {
  num(n) {
    if (n === null || n === undefined || Number.isNaN(Number(n))) return '—'
    return Number(n).toLocaleString('en-US')
  },
  ms(n) {
    const v = Number(n)
    if (!Number.isFinite(v)) return '—'
    if (v < 1000) return `${Math.round(v)}ms`
    if (v < 60000) return `${(v / 1000).toFixed(v < 10000 ? 2 : 1)}s`
    const m = Math.floor(v / 60000)
    const s = Math.round((v % 60000) / 1000)
    return `${m}m${String(s).padStart(2, '0')}s`
  },
  bytes(n) {
    const v = Number(n)
    if (!Number.isFinite(v)) return '—'
    if (v < 1024) return `${v} B`
    const units = ['KB', 'MB', 'GB']
    let x = v / 1024
    let i = 0
    while (x >= 1024 && i < units.length - 1) {
      x /= 1024
      i++
    }
    return `${x.toFixed(x < 10 ? 1 : 0)} ${units[i]}`
  },
  pct(part, total) {
    if (!total) return '—'
    return `${Math.round((part / total) * 100)}%`
  },
  ago(ts) {
    if (!ts) return '—'
    const s = Math.max(0, Math.round((Date.now() - ts) / 1000))
    if (s < 60) return `${s}s ago`
    if (s < 3600) return `${Math.round(s / 60)}m ago`
    if (s < 86400) return `${Math.round(s / 3600)}h ago`
    return `${Math.round(s / 86400)}d ago`
  },
  time(ts) {
    if (!ts) return ''
    return new Date(ts).toLocaleString(undefined, { hour12: false })
  },
}

// -------------------------------------------------------------------- badges

export function badge(text, tone = '', title = '') {
  return h('span.badge', { class: tone ? `badge ${tone}` : 'badge', title }, [tone ? h('i.bdot') : null, text])
}

const CG_TONE = { resolved: 'ok', name: 'warn', unresolved: 'danger', none: 'plain' }
const CG_LABEL = {
  resolved: 'call graph: resolved',
  name: 'call graph: name-based',
  unresolved: 'call graph: unresolved',
  none: 'no call graph',
}

export function callGraphBadge(cg, resolution = '') {
  if (!cg) return null
  return h(`span.badge.cg-${cg}`, { title: resolution || CG_LABEL[cg] || cg }, [h('i.bdot'), `call_graph: ${cg}`])
}

export function kindBadge(kind) {
  return h('span.kind', { class: `kind ${String(kind || '').toLowerCase()}` }, kind || '?')
}

export function staleBadge(stale, info) {
  if (stale === false || stale === undefined || stale === null) return null
  const detail = info && typeof info === 'object' ? `${info.changed ?? 0} changed · ${info.new ?? 0} new · ${info.deleted ?? 0} deleted` : ''
  return h('span.badge.warn', { title: `The working tree drifted since the last index. ${detail}` }, [h('i.bdot'), 'stale index'])
}

export function boolBadge(label, value, { okWhen = true } = {}) {
  const good = value === okWhen
  return h(`span.badge.${good ? 'ok' : 'warn'}`, [h('i.bdot'), `${label}: ${value === true ? 'yes' : value === false ? 'no' : String(value ?? '—')}`])
}

/**
 * codemap's review gate reports WHY it would fail as a structured object
 * ({incomplete_analysis, untested, risk_at_or_above:{low,medium,high}}).
 * Render it as a sentence instead of "[object Object]".
 */
export function gateReasons(w) {
  if (w === null || w === undefined || w === false) return 'nothing'
  if (typeof w === 'string') return w || 'nothing'
  if (Array.isArray(w)) return w.length ? w.join(', ') : 'nothing'
  const out = []
  if (w.incomplete_analysis) out.push('incomplete analysis')
  if (w.untested) out.push('untested changed symbols')
  if (w.uncovered) out.push('uncovered changed symbols')
  if (w.risk_at_or_above && typeof w.risk_at_or_above === 'object') {
    const levels = Object.entries(w.risk_at_or_above)
      .filter(([, v]) => v)
      .map(([k]) => k)
    if (levels.length) out.push(`risk at or above ${levels.join('/')}`)
  }
  for (const [k, v] of Object.entries(w)) {
    if (['incomplete_analysis', 'untested', 'uncovered', 'risk_at_or_above'].includes(k)) continue
    if (v) out.push(String(k))
  }
  return out.length ? out.join(' · ') : 'nothing'
}

// --------------------------------------------------------------------- card

export function card({ title, sub, actions, body, foot, cls = '', tight = false }) {
  return h(`section.card${cls ? ` ${cls}` : ''}`, [
    title || actions
      ? h('header.card-head', [
          title ? h('h3', title) : null,
          sub ? h('span.small.muted', sub) : null,
          h('div.spacer'),
          actions ? h('div.btn-row', actions) : null,
        ])
      : null,
    body ? h('div.card-body', { class: tight ? 'card-body tight' : 'card-body' }, body) : null,
    foot ? h('footer.card-foot', foot) : null,
  ])
}

export function metric(label, value, sub, tone = '') {
  return h(`div.metric${tone ? ` ${tone}` : ''}`, [
    h('div.m-label', label),
    h('div.m-value', String(value ?? '—')),
    sub ? h('div.m-sub', sub) : null,
  ])
}

export function barChart(rows, { max: forcedMax, tone = '' } = {}) {
  const max = forcedMax ?? Math.max(1, ...rows.map((r) => Number(r.value) || 0))
  return h(
    'div.barchart',
    rows.map((r) =>
      h('div.bc-row', [
        h('div.bc-l', { title: r.label }, r.label),
        h('div.bar', { class: `bar ${r.tone || tone}` }, [h('i', { style: { width: `${Math.max(1.5, (Number(r.value) / max) * 100)}%` } })]),
        h('div.bc-v', fmt.num(r.value)),
      ]),
    ),
  )
}

// -------------------------------------------------------------------- table

/**
 * columns: [{key, label, render?(row), cls?, sort?(a,b), align?}]
 * rows: array of objects
 */
export function table(columns, rows, { dense = false, onRow, empty = 'No rows', max = 5000 } = {}) {
  if (!rows || !rows.length) return emptyState({ note: empty })
  let sortKey = null
  let sortDir = 1
  const tbody = h('tbody')
  const draw = () => {
    clear(tbody)
    let data = rows.slice(0, max)
    if (sortKey) {
      const col = columns.find((c) => c.key === sortKey)
      const cmp = col?.sort || ((a, b) => String(a?.[sortKey] ?? '').localeCompare(String(b?.[sortKey] ?? '')))
      data = data.slice().sort((a, b) => cmp(a, b) * sortDir)
    }
    for (const row of data) {
      const tr = h(
        'tr',
        onRow ? { onclick: () => onRow(row), style: 'cursor:pointer' } : null,
        columns.map((c) => {
          const content = c.render ? c.render(row) : row?.[c.key]
          return h('td', { class: c.cls || (typeof content === 'number' ? 'num' : '') }, content ?? '—')
        }),
      )
      if (onRow) tr.dataset.rowKey = String(row?.__key ?? '')
      tbody.appendChild(tr)
    }
    if (rows.length > max) tbody.appendChild(h('tr', null, h('td', { colspan: String(columns.length), class: 'dim small', style: 'padding:8px 12px' }, `… ${rows.length - max} more rows hidden — narrow the query or use Raw JSON`)))
  }
  const head = h(
    'thead',
    null,
    h(
      'tr',
      null,
      columns.map((c) =>
        h(
          'th',
          {
            class: c.sort !== false ? 'sortable' : '',
            onclick:
              c.sort === false
                ? null
                : () => {
                    if (sortKey === c.key) sortDir = -sortDir
                    else {
                      sortKey = c.key
                      sortDir = 1
                    }
                    draw()
                  },
          },
          c.label,
        ),
      ),
    ),
  )
  const tbl = h(`table.tbl${dense ? '.dense' : ''}`, [head, tbody])
  draw()
  return h('div.table-wrap', tbl)
}

// --------------------------------------------------------------- symbol list

export function symRow(item, { onPick, meta } = {}) {
  const sym = symOf(item) || {}
  const file = sym.file || sym.relative_path || ''
  const line = sym.start_line
  const name = sym.symbol || sym.fqn || sym.name || file
  const fqn = sym.fqn && sym.fqn !== name ? sym.fqn : ''
  return h(
    'button.symrow',
    {
      type: 'button',
      title: sym.signature || sym.doc || `${file}:${line ?? ''}`,
      onclick: onPick ? () => onPick(sym) : null,
      dataset: { file, line: String(line ?? '') },
    },
    [
      sym.kind ? kindBadge(sym.kind) : h('span.sk', '·'),
      h('span.sn', [name, fqn ? h('span.fqn', `  ${fqn}`) : null]),
      h(
        'span.sp',
        [
          file ? shortPath(file) : '',
          line ? `:${line}` : '',
          sym.depth !== undefined && sym.depth !== null ? ` · d${sym.depth}` : '',
          sym.in_degree !== undefined ? ` · fan-in ${fmt.num(sym.in_degree)}` : '',
          sym.score ? ` · ${Number(sym.score).toFixed(3)}` : '',
        ]
          .filter(Boolean)
          .join(''),
      ),
      meta ? h('span.meta', meta(sym)) : sym.signature ? h('span.meta', sym.signature) : null,
    ],
  )
}

export function symList(items, opts = {}) {
  if (!items || !items.length) return emptyState({ note: opts.empty || 'Nothing matched.' })
  return h('div.symlist', items.map((it) => symRow(it, opts)))
}

export function shortPath(p) {
  const s = String(p || '')
  return s.length > 58 ? `…${s.slice(-56)}` : s
}

// --------------------------------------------------------------------- misc

export function kv(pairs, { mono = true } = {}) {
  const dl = h('dl.kv')
  for (const [k, v] of pairs) {
    if (v === undefined || v === null || v === '') continue
    dl.appendChild(h('dt', String(k)))
    dl.appendChild(h('dd', { class: mono ? '' : 'ui' }, typeof v === 'object' && !(v instanceof Node) ? JSON.stringify(v) : v))
  }
  return dl
}

export function callout(tone, title, text, actions) {
  const icons = { warn: '⚠', danger: '⛔', ok: '✓', info: 'ℹ', '': '›' }
  return h(`div.callout${tone ? ` ${tone}` : ''}`, [
    h('span.co-ico', icons[tone] || '›'),
    h('div', { style: 'min-width:0;flex:1' }, [
      title ? h('div', { style: 'color:var(--text);font-weight:600;margin-bottom:2px' }, title) : null,
      text ? h('div', { style: 'white-space:pre-wrap;word-break:break-word' }, text) : null,
      actions ? h('div.btn-row', { style: 'margin-top:8px' }, actions) : null,
    ]),
  ])
}

export function emptyState({ icon = '∅', title = 'Nothing here yet', note = '', actions = [] } = {}) {
  return h('div.empty', [h('div.e-ico', icon), h('div.e-title', title), note ? h('div.e-note', note) : null, actions.length ? h('div.btn-row', { style: 'justify-content:center' }, actions) : null])
}

export function errorBox(result) {
  const code = result?.code || 'operational'
  return h('div.error-box', [
    h('div.e-title', [`${code}`, result?.gateFailed ? ' (gate failed — exit 6)' : ` (exit ${result?.exitCode ?? '?'})`]),
    h('div.e-msg', result?.error || result?.stderr || 'The command failed without a message.'),
    result?.hint ? h('div.e-hint', [h('strong', 'Hint: '), result.hint]) : null,
    result?.stderr && result.error
      ? h('details', { style: 'margin-top:8px' }, [h('summary.small.muted', 'stderr'), h('pre.logbox', { style: 'margin-top:6px' }, result.stderr)])
      : null,
  ])
}

export function spinner(label = '') {
  return h('div.row', { style: 'gap:8px;padding:14px 4px;color:var(--text-3)' }, [h('div.spinner'), label ? h('span.small', label) : null])
}

export function progress(value, total, { tone = '', indeterminate = false, label = '' } = {}) {
  const pctv = total > 0 ? Math.min(100, Math.max(0, (value / total) * 100)) : 0
  return h('div', { style: 'display:grid;gap:4px' }, [
    label ? h('div.small.muted', label) : null,
    h('div.bar', { class: `bar ${tone}${indeterminate ? ' indet' : ''}` }, [h('i', { style: { width: `${indeterminate ? 32 : pctv}%` } })]),
    total > 0 && !indeterminate ? h('div.small.dim.mono', `${fmt.num(value)} / ${fmt.num(total)} · ${Math.round(pctv)}%`) : null,
  ])
}

// --------------------------------------------------------------------- code

/**
 * Highlighted code with an optional gutter.
 * @param {object} o
 * @param {string} o.text
 * @param {string} [o.lang]
 * @param {number} [o.startLine] first line number in the gutter
 * @param {Set<number>|number[]} [o.highlight] absolute line numbers to mark
 * @param {number} [o.maxLines] cap rendered lines (large bodies stay usable)
 */
export function codeBlock({ text, lang, startLine = 1, highlight = null, maxLines = 4000, onLineClick = null, marks = null }) {
  const raw = String(text ?? '').replace(/\t/g, '    ')
  const lines = tokenLines(raw, lang || '')
  const total = lines.length
  const shown = lines.slice(0, maxLines)
  const hl = highlight instanceof Set ? highlight : new Set(highlight || [])
  const gutter = shown.map((_, i) => String(startLine + i)).join('\n')
  const body = h('div.code-lines')
  shown.forEach((toks, i) => {
    const ln = startLine + i
    const line = h(
      'span.code-line',
      {
        class: `code-line${hl.has(ln) ? ' hl' : ''}${marks?.get?.(ln) ? ` ${marks.get(ln)}` : ''}`,
        dataset: { line: String(ln) },
        onclick: onLineClick ? () => onLineClick(ln) : null,
        style: onLineClick ? 'cursor:pointer' : null,
      },
      toks.length ? toks.map((t) => (t.c ? h(`span.tok-${t.c}`, { text: t.t }) : document.createTextNode(t.t))) : [' '],
    )
    body.appendChild(line)
  })
  if (total > shown.length) {
    body.appendChild(h('span.code-line.dim', `… ${total - shown.length} more lines — open the file to see them all`))
  }
  return h('pre.code.with-lines', h('table.code-table', h('tbody', h('tr', [h('td.code-gutter', gutter), h('td', body)]))))
}

export function codeInline(text) {
  return h('code.inline', String(text ?? ''))
}

// ---------------------------------------------------------------- json tree

export function jsonTree(value, { depth = 0, open = 2, key = '' } = {}) {
  const isObj = value !== null && typeof value === 'object'
  if (!isObj) {
    const cls = typeof value === 'string' ? 'jstr' : typeof value === 'number' ? 'jnum' : typeof value === 'boolean' ? 'jbool' : 'jnull'
    return h('div.jrow', [
      key ? h('span.jkey', `${key}:`) : null,
      h(`span.${cls}`, typeof value === 'string' ? JSON.stringify(value) : String(value)),
    ])
  }
  const entries = Array.isArray(value) ? value.map((v, i) => [String(i), v]) : Object.entries(value)
  const collapsed = depth >= open
  const kids = h('div.jkids', { class: collapsed ? 'jkids collapsed' : 'jkids' })
  for (const [k, v] of entries) kids.appendChild(jsonTree(v, { depth: depth + 1, open, key: k }))
  const bracket = Array.isArray(value) ? [`[`, `]`] : [`{`, `}`]
  const tog = h('span.jtog', {
    text: collapsed ? '▸' : '▾',
    onclick: (e) => {
      e.stopPropagation()
      const nowCollapsed = kids.classList.toggle('collapsed')
      tog.textContent = nowCollapsed ? '▸' : '▾'
    },
  })
  return h('div', [
    h('div.jrow', [
      tog,
      key ? h('span.jkey', `${key}:`) : null,
      h('span.jnull', bracket[0]),
      h('span.jcount', `${entries.length} ${Array.isArray(value) ? 'items' : 'keys'}`),
    ]),
    kids,
    h('div.jrow', [h('span.jtog'), h('span.jnull', bracket[1])]),
  ])
}

export function jsonPanel(result, { title = 'Raw JSON' } = {}) {
  const text = result?.json ? JSON.stringify(result.json, null, 2) : result?.stdout || ''
  return h('div.stack', { style: 'gap:8px' }, [
    h('div.btn-row', [
      h('button.btn.sm', { type: 'button', onclick: async (e) => { await copy(text); flash(e.target, 'copied') } }, 'Copy'),
      h('button.btn.sm', { type: 'button', onclick: () => download(`${title.replace(/\W+/g, '-').toLowerCase()}-${Date.now()}.json`, text) }, 'Download'),
      h('span.small.dim', `${fmt.bytes(text.length)}`),
    ]),
    result?.json ? h('div.json', jsonTree(result.json)) : h('pre.code', text || '(no output)'),
  ])
}

async function flash(node, label) {
  if (!node) return
  const old = node.textContent
  node.textContent = label
  node.classList.add('on')
  setTimeout(() => {
    node.textContent = old
    node.classList.remove('on')
  }, 1100)
}

// --------------------------------------------------------------------- tabs

export function tabs(items, active, onChange) {
  return h(
    'div.tabs',
    items.map((t) =>
      h(
        'button.tab',
        {
          type: 'button',
          class: t.id === active ? 'tab on' : 'tab',
          onclick: () => onChange(t.id),
        },
        [t.label, t.count !== undefined && t.count !== null ? h('span.n', fmt.num(t.count)) : null],
      ),
    ),
  )
}

// -------------------------------------------------------------------- chips

export function chip(label, onClick, { title = '', cls = '' } = {}) {
  return h('button.chip', { type: 'button', class: `chip ${cls}`, title, onclick: onClick }, label)
}

export function nextChips(next, onRun) {
  if (!next || !next.length) return null
  return h(
    'div.stack',
    { style: 'gap:6px' },
    h('h4', 'Suggested next'),
    h(
      'div.next-chips',
      next.map((n) => {
        const args = Array.isArray(n.args) ? n.args.join(' ') : n.args && typeof n.args === 'object' ? Object.entries(n.args).filter(([k]) => k !== 'path').map(([k, v]) => `--${k.replace(/_/g, '-')} ${typeof v === 'object' ? JSON.stringify(v) : v}`).join(' ') : ''
        return chip([h('span.mono', n.tool || n.command || 'next'), n.why ? h('span.dim', ` — ${n.why}`) : null], () => onRun?.(n), {
          title: args ? `${n.tool || ''} ${args}`.trim() : n.tool || '',
        })
      }),
    ),
  )
}

// ------------------------------------------------------------------ toasts

let toastHost = null
export function toast(message, { tone = '', title = '', ms = 3600 } = {}) {
  toastHost = toastHost || document.getElementById('toasts')
  if (!toastHost) return
  const node = h(`div.toast${tone ? ` ${tone}` : ''}`, [title ? h('strong', title) : null, message])
  toastHost.appendChild(node)
  const kill = () => node.remove()
  node.onclick = kill
  setTimeout(kill, ms)
  return kill
}

// ------------------------------------------------------------------- modal

export function modal({ title, body, actions = [], onClose, width }) {
  const root = document.getElementById('modal-root')
  const close = () => {
    overlay.remove()
    document.removeEventListener('keydown', onKey)
    onClose?.()
  }
  const onKey = (e) => {
    if (e.key === 'Escape') {
      e.stopPropagation()
      close()
    }
  }
  const panel = h('div.modal', { style: width ? { width: `min(${width}, 94vw)` } : null }, [
    h('div.modal-head', [h('div.modal-title', title), h('div.spacer', { style: 'flex:1' }), h('button.icon-btn', { type: 'button', text: '✕', onclick: close })]),
    h('div.modal-body', body),
    actions.length ? h('div.modal-foot', actions) : null,
  ])
  const overlay = h('div.modal-overlay', {
    onclick: (e) => {
      if (e.target === overlay) close()
    },
  }, panel)
  root.appendChild(overlay)
  document.addEventListener('keydown', onKey, true)
  return { close, panel }
}

export function confirmDialog({ title, message, confirmLabel = 'Run', tone = 'primary' }) {
  return new Promise((resolve) => {
    let done = false
    const finish = (v) => {
      if (done) return
      done = true
      m.close()
      resolve(v)
    }
    const m = modal({
      title,
      body: h('div.stack', [h('div', { style: 'line-height:1.6;color:var(--text-2)' }, message)]),
      actions: [
        h('button.btn', { type: 'button', text: 'Cancel', onclick: () => finish(false) }),
        h('button.btn', { type: 'button', class: `btn ${tone}`, text: confirmLabel, onclick: () => finish(true) }),
      ],
      onClose: () => finish(false),
    })
  })
}

export function promptDialog({ title, label, value = '', placeholder = '', multiline = false }) {
  return new Promise((resolve) => {
    let done = false
    const input = multiline
      ? h('textarea', { value, placeholder })
      : h('input', { type: 'text', value, placeholder })
    const finish = (v) => {
      if (done) return
      done = true
      m.close()
      resolve(v)
    }
    const m = modal({
      title,
      body: h('div.field', [h('label', label), input]),
      actions: [
        h('button.btn', { type: 'button', text: 'Cancel', onclick: () => finish(null) }),
        h('button.btn.primary', { type: 'button', text: 'OK', onclick: () => finish(input.value) }),
      ],
      onClose: () => finish(null),
    })
    setTimeout(() => input.focus(), 30)
    input.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' && !multiline) finish(input.value)
    })
  })
}

/** A labelled section with an optional right-hand control row. */
export function section(title, body, { actions = null, sub = '' } = {}) {
  return h('div.section', [
    title
      ? h('div.section-head', [h('h2', title), sub ? h('span.small.dim', sub) : null, h('div.spacer'), actions])
      : null,
    body,
  ])
}

export function highlightText(text, needle) {
  const s = String(text ?? '')
  const q = String(needle ?? '').trim()
  if (!q) return s
  const i = s.toLowerCase().indexOf(q.toLowerCase())
  if (i < 0) return s
  return [s.slice(0, i), h('mark', { style: 'background:var(--accent-soft);color:var(--text);border-radius:2px;padding:0 1px' }, s.slice(i, i + q.length)), s.slice(i + q.length)]
}

export { copy, download }
export { langFromPath }
