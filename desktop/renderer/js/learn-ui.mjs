/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Presentational pieces shared by the Learn views and the generic report
// renderers: language/subsystem bars, role badges, the route strip, feature
// cards and a static call-tree list. No data fetching happens here.

import { h } from './dom.mjs'
import { badge, kindBadge, callout, fmt } from './components.mjs'
import { languageColor, subsystemColor } from './palette.mjs'
import { languageShares, shortNum, featureLabel, footprintSize, featureFlowTarget, dominantLanguage } from './learn.mjs'

// ----------------------------------------------------------------- bars

/** Stacked bar of a node's languages; each segment's width is its share. */
export function languageBar(languages, { legend = false, max = 4, thin = false } = {}) {
  const shares = languageShares({ languages })
  if (!shares.length) return h('div.langbar-empty.small.dim', 'no languages')
  const bar = h(
    'div.langbar',
    { class: thin ? 'langbar thin' : 'langbar', role: 'img', 'aria-label': shares.map((s) => `${s.lang} ${Math.round(s.share * 100)}%`).join(', ') },
    shares.map((s) => h('i', { style: { flexGrow: String(s.count), background: languageColor(s.lang) }, title: `${s.lang} · ${Math.round(s.share * 100)}%` })),
  )
  if (!legend) return bar
  const top = shares.slice(0, max)
  const rest = shares.slice(max)
  return h('div.langbar-wrap', [
    bar,
    h('div.lang-legend', [
      top.map((s) => h('span.ll', [h('i.sw', { style: { background: languageColor(s.lang) } }), s.lang, h('em', `${Math.round(s.share * 100)}%`)])),
      rest.length ? h('span.ll.dim', `+${rest.length} more`) : null,
    ]),
  ])
}

/** Thin bar of a footprint's subsystems, weighted by symbol count. */
export function subsystemBar(subsystems, { thin = true } = {}) {
  const list = (subsystems || []).filter((s) => Number(s.symbols) > 0)
  if (!list.length) return null
  return h(
    'div.subbar',
    { class: thin ? 'subbar thin' : 'subbar' },
    list.map((s) => h('i', { style: { flexGrow: String(s.symbols), background: subsystemColor(s.name) }, title: `${s.name} · ${s.symbols} symbol(s)` })),
  )
}

// --------------------------------------------------------------- badges

const ROLE_TONE = { source: 'accent', entrypoint: 'ok', tests: 'info', docs: 'warn' }

export function roleBadges(roles) {
  const list = Array.isArray(roles) ? roles : []
  return list.map((r) => badge(r, ROLE_TONE[r] || 'plain'))
}

export function surfaceBadge(surface) {
  const tone = { CLI: 'accent', 'RPC/MCP': 'info', HTTP: 'ok', Page: 'warn', Program: 'plain' }[surface] || 'plain'
  return badge(surface || 'feature', tone)
}

// ------------------------------------------------------------ route strip

/**
 * "It goes cmd → app → graph → git": the subsystems of a flow, in order of
 * first appearance, as connected chips with step counts.
 */
export function routeStrip(subsystems, { onPick = null, active = '' } = {}) {
  const list = subsystems || []
  if (!list.length) return null
  const items = []
  list.forEach((s, i) => {
    if (i) items.push(h('span.route-arrow', { 'aria-hidden': 'true' }, '→'))
    items.push(
      h(
        onPick ? 'button.route-chip' : 'span.route-chip',
        {
          type: onPick ? 'button' : null,
          class: `route-chip${active && active === s.name ? ' on' : ''}${active && active !== s.name ? ' off' : ''}`,
          title: `${s.name} · ${s.steps} step(s), first reached at depth ${s.first_depth ?? '?'}`,
          style: { '--c': subsystemColor(s.name) },
          onclick: onPick ? () => onPick(s.name) : null,
        },
        [h('i.dot'), h('span.rc-name', s.name || '(unknown)'), h('span.rc-n', String(s.steps ?? ''))],
      ),
    )
  })
  return h('div.route-strip', items)
}

// ------------------------------------------------------------- features

export function footprintChips(fp) {
  if (!fp) return null
  const chips = [
    [fmt.num(fp.symbols), 'symbols'],
    [fmt.num(fp.files), 'files'],
    [fmt.num((fp.subsystems || []).length), 'subsystems'],
    [fmt.num(fp.tests), 'tests'],
  ]
  return h('div.fp-chips', [
    chips.map(([n, label]) => h('span.fp-chip', [h('b', n), label])),
    fp.truncated ? h('span.fp-chip.warn', 'trimmed') : null,
  ])
}

const isHidden = (f) => !!f?.hidden || /^_/.test(String(f?.label || ''))

/**
 * One capability as a card. `onOpen` fires on click/Enter; the secondary
 * actions never bubble into it.
 */
export function featureCard(f, { onOpen = null, actions = null, indent = 0, compact = false, highlight = '' } = {}) {
  const target = featureFlowTarget(f)
  const fp = f.footprint
  const label = featureLabel(f)
  const card = h(
    'div.fcard',
    {
      class: `fcard${isHidden(f) ? ' muted' : ''}${compact ? ' compact' : ''}${indent ? ' nested' : ''}`,
      role: 'button',
      tabindex: '0',
      dataset: { id: f.id, kind: f.kind },
      style: indent ? { marginLeft: `${Math.min(indent, 3) * 22}px` } : null,
      title: target ? `Trace ${label} end to end` : 'No handler found for this feature',
      onclick: onOpen ? () => onOpen(f) : null,
      onkeydown: (e) => {
        if (e.key === 'Enter' && onOpen) {
          e.preventDefault()
          onOpen(f)
        }
      },
    },
    [
      h('div.fc-top', [
        h('span.fc-label', highlight ? markText(label, highlight) : label),
        surfaceBadge(f.surface),
        f.framework ? badge(f.framework, 'plain') : null,
        f.confidence && f.confidence !== 'confirmed' ? badge(f.confidence, 'warn', 'The detector is not certain this is a real entry point.') : compact ? null : badge('confirmed', 'plain', 'Found by reading the registration call in the source.'),
        !target ? badge('no handler', 'plain') : f.handler ? null : badge('registration only', 'plain', 'No separate handler function was found (a group command or an inline closure); tracing starts at the registration site.'),
        h('div.spacer'),
        actions ? h('div.fc-actions', { onclick: (e) => e.stopPropagation() }, actions) : null,
      ]),
      f.description ? h('div.fc-desc', highlight ? markText(f.description, highlight) : f.description) : null,
      !compact && fp
        ? h('div.fc-foot', [footprintChips(fp), subsystemBar(fp.subsystems)])
        : null,
      compact && fp ? h('div.fc-foot', [footprintChips(fp)]) : null,
      !compact && f.registration?.file ? h('div.fc-file', `${f.registration.file}:${f.registration.line ?? ''}`) : null,
    ],
  )
  return card
}

function markText(text, needle) {
  const s = String(text ?? '')
  const q = String(needle ?? '').trim()
  if (!q) return s
  const i = s.toLowerCase().indexOf(q.toLowerCase())
  if (i < 0) return s
  return [s.slice(0, i), h('mark', s.slice(i, i + q.length)), s.slice(i + q.length)]
}

/** Count of features by surface, ordered the way people think about them. */
export const SURFACE_ORDER = ['CLI', 'RPC/MCP', 'HTTP', 'Page', 'Program']

export function surfaceCounts(features) {
  const counts = new Map()
  for (const f of features || []) {
    const s = f.surface || 'Other'
    counts.set(s, (counts.get(s) || 0) + 1)
  }
  return [...counts.entries()].sort((a, b) => {
    const ia = SURFACE_ORDER.indexOf(a[0])
    const ib = SURFACE_ORDER.indexOf(b[0])
    return (ia < 0 ? 99 : ia) - (ib < 0 ? 99 : ib) || b[1] - a[1]
  })
}

export function surfaceLabel(surface, n = 2) {
  const plural = { CLI: 'CLI commands', 'RPC/MCP': 'RPC / MCP tools', HTTP: 'HTTP routes', Page: 'pages', Program: 'programs' }
  const single = { CLI: 'CLI command', 'RPC/MCP': 'RPC / MCP tool', HTTP: 'HTTP route', Page: 'page', Program: 'program' }
  return (n === 1 ? single : plural)[surface] || surface
}

/** The features with the biggest footprints, handler-bearing first. */
export function representativeFeatures(features, n = 5, { skipKinds = [] } = {}) {
  return (features || [])
    .filter((f) => f.footprint && featureFlowTarget(f) && !skipKinds.includes(f.kind) && !isHidden(f))
    .sort((a, b) => footprintSize(b) - footprintSize(a) || featureLabel(a).localeCompare(featureLabel(b)))
    .slice(0, n)
}

// ------------------------------------------------------------ static tree

/** A compact read-only call tree (used by the generic report renderer). */
export function flowList(root, { max = 60, onPick = null } = {}) {
  const rows = []
  const walk = (n, depth) => {
    if (rows.length >= max) return
    rows.push(
      h(
        onPick ? 'button.fl-row' : 'div.fl-row',
        { type: onPick ? 'button' : null, style: { paddingLeft: `${10 + depth * 14}px` }, onclick: onPick && n.file ? () => onPick(n) : null },
        [
          h('i.dot', { style: { background: subsystemColor(n.subsystem) } }),
          n.kind ? kindBadge(n.kind) : null,
          h('span.fl-name', n.symbol || n.fqn || '(unresolved)'),
          n.file ? h('span.fl-loc', `${n.file}:${n.start_line}`) : null,
        ],
      ),
    )
    for (const c of n.children || []) walk(c, depth + 1)
  }
  walk(root, 0)
  return h('div.fl-list', rows)
}

export { dominantLanguage, shortNum }

// -------------------------------------------------------------- callouts

/** Notes, resolution text and partial errors as callouts; every field optional. */
export function reportCallouts(json, { resolution = true, notes = true } = {}) {
  const out = []
  if (!json) return out
  if (resolution && typeof json.resolution === 'string' && json.resolution) out.push(callout('warn', 'resolution', json.resolution))
  if (notes && Array.isArray(json.notes) && json.notes.length) out.push(callout('info', json.notes.length === 1 ? 'note' : 'notes', json.notes.map((n) => (typeof n === 'string' ? n : JSON.stringify(n))).join('\n')))
  if (typeof json.note === 'string' && json.note) out.push(callout('info', 'note', json.note))
  if (Array.isArray(json.partial_errors) && json.partial_errors.length) {
    out.push(callout('danger', `${json.partial_errors.length} partial error(s)`, json.partial_errors.map((e) => (typeof e === 'string' ? e : `${e.kind || e.code || 'error'}: ${e.detail || e.message || JSON.stringify(e)}`)).join('\n')))
  }
  return out
}
