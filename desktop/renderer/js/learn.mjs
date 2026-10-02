/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Shared plumbing for the Learn views (Overview, Atlas, Features, Flow): cached
// loaders for the three learning commands, per-project persistence of the
// learning path and recent flows, node helpers, and the markdown exporters.
// Every field of every report is treated as optional — older binaries and
// unindexed projects must degrade to a message, never to an exception.

import { runArgs } from './runner.mjs'
import { state } from './state.mjs'

// -------------------------------------------------------------- persistence

function readJSON(key, fallback) {
  try {
    const raw = globalThis.localStorage?.getItem(key)
    return raw ? JSON.parse(raw) : fallback
  } catch {
    return fallback
  }
}

function writeJSON(key, value) {
  try {
    globalThis.localStorage?.setItem(key, JSON.stringify(value))
  } catch {
    /* private mode / quota: the learning state is a convenience, not data */
  }
}

export const LEARN_STEPS = ['picture', 'abilities', 'entry', 'concepts', 'trace', 'notes']

const progressKey = (project) => `codemap-learn:${project || ''}`
const flowsKey = (project) => `codemap-flows:${project || ''}`

export function loadProgress(project = state.project) {
  const raw = readJSON(progressKey(project), {})
  const out = {}
  for (const id of LEARN_STEPS) out[id] = !!raw?.[id]
  return out
}

export function setStep(id, value, project = state.project) {
  const p = loadProgress(project)
  p[id] = !!value
  writeJSON(progressKey(project), p)
  return p
}

export function markStep(id, project = state.project) {
  return setStep(id, true, project)
}

export function recentFlows(project = state.project) {
  const list = readJSON(flowsKey(project), [])
  return Array.isArray(list) ? list.filter((f) => f && f.label) : []
}

export function rememberFlow(entry, project = state.project) {
  if (!entry?.label) return recentFlows(project)
  const key = entry.at || entry.symbol || entry.label
  const next = [entry, ...recentFlows(project).filter((f) => (f.at || f.symbol || f.label) !== key)].slice(0, 10)
  writeJSON(flowsKey(project), next)
  return next
}

const PREF_KEY = 'codemap-atlas-prefs'

export function loadPrefs() {
  const p = readJSON(PREF_KEY, {})
  return {
    size: ['symbols', 'lines', 'files'].includes(p?.size) ? p.size : 'symbols',
    color: ['language', 'role', 'coupling', 'tests'].includes(p?.color) ? p.color : 'language',
    detail: p?.detail !== false,
  }
}

export function savePrefs(prefs) {
  writeJSON(PREF_KEY, prefs)
}

// ------------------------------------------------------------------ loaders

const cache = new Map()

export function clearLearnCache() {
  cache.clear()
}

function cached(name, args, force) {
  const key = `${state.project}\u0000${name}\u0000${args.join('\u0001')}`
  if (!force && cache.has(key)) return cache.get(key)
  const promise = runArgs(args, { featureId: `__learn_${name}`, cwd: state.project, quiet: true }).then((res) => {
    if (!res?.ok) cache.delete(key)
    return res
  })
  cache.set(key, promise)
  return promise
}

export function atlasArgs({ prefix = '', depth = 2, files = false, maxNodes = 0, keySymbols = 0 } = {}) {
  const a = ['atlas']
  if (prefix) a.push('--prefix', prefix)
  a.push('--depth', String(depth))
  if (files) a.push('--files')
  if (maxNodes) a.push('--max-nodes', String(maxNodes))
  if (keySymbols) a.push('--key-symbols', String(keySymbols))
  return a
}

export function loadAtlas(opts = {}, { force = false } = {}) {
  return cached('atlas', atlasArgs(opts), force)
}

export function loadFeatures({ force = false, args = [] } = {}) {
  return cached('features', ['features', ...args], force)
}

export function loadAnnotations({ force = false } = {}) {
  return cached('annotations', ['annotations'], force)
}

export function loadMapEntrypoints({ force = false } = {}) {
  return cached('map', ['map', '--top-subsystems', '1', '--top-bridges', '1', '--top-hubs', '1', '--top-entrypoints', '8'], force)
}

/** Args for `codemap flow` from whichever handle the caller has. */
export function flowArgs({ selector, at, symbol, depth = 4, maxNodes = 150 } = {}) {
  const a = ['flow']
  const pos = at || (selector?.file && selector.start_line ? `${selector.file}:${selector.start_line}` : '')
  if (pos) a.push('--at', pos)
  else if (symbol) a.push(symbol)
  else return null
  a.push('--depth', String(depth), '--max-nodes', String(maxNodes))
  return a
}

export function runFlow(opts) {
  const args = flowArgs(opts)
  if (!args) return Promise.resolve({ ok: false, code: 'operational', error: 'give a symbol or a file:line to trace', stdout: '', stderr: '' })
  return runArgs(args, { featureId: '__learn_flow', cwd: state.project, quiet: true })
}

// ----------------------------------------------------------------- helpers

export function isIndexedAtlas(json) {
  return !!json && json.indexed !== false && !!json.tree
}

export function shortNum(n) {
  const v = Number(n)
  if (!Number.isFinite(v)) return '—'
  if (Math.abs(v) < 1000) return String(Math.round(v))
  if (Math.abs(v) < 10000) return `${(v / 1000).toFixed(1).replace(/\.0$/, '')}k`
  if (Math.abs(v) < 1e6) return `${Math.round(v / 1000)}k`
  return `${(v / 1e6).toFixed(1).replace(/\.0$/, '')}M`
}

export function sizeOf(node, metric) {
  if (!node) return 0
  if (metric === 'lines') return Number(node.lines) || 0
  if (metric === 'files') return Number(node.files) || (node.type === 'file' ? 1 : 0)
  return Number(node.symbols) || 0
}

/** [{lang, count, share}] sorted by count, from a node's `languages` map. */
export function languageShares(node) {
  const entries = Object.entries(node?.languages || {}).filter(([, n]) => Number(n) > 0)
  const total = entries.reduce((s, [, n]) => s + Number(n), 0)
  return entries
    .map(([lang, count]) => ({ lang, count: Number(count), share: total ? Number(count) / total : 0 }))
    .sort((a, b) => b.count - a.count || a.lang.localeCompare(b.lang))
}

export function dominantLanguage(node) {
  return node?.language || languageShares(node)[0]?.lang || ''
}

export function findNode(tree, path) {
  if (!tree) return null
  if ((tree.path || '') === (path || '')) return tree
  for (const c of tree.children || []) {
    if (path === c.path || String(path).startsWith(`${c.path}/`)) {
      const hit = findNode(c, path)
      if (hit) return hit
    }
  }
  return null
}

export function walkNodes(tree, fn) {
  if (!tree) return
  fn(tree)
  for (const c of tree.children || []) walkNodes(c, fn)
}

export function baseName(path) {
  const s = String(path || '')
  return s.slice(s.lastIndexOf('/') + 1) || s
}

export function parentPath(path) {
  const s = String(path || '')
  const i = s.lastIndexOf('/')
  return i < 0 ? '' : s.slice(0, i)
}

/** Breadcrumb trail [{label, path}] from the project root to `path`. */
export function trail(project, path) {
  const out = [{ label: project || 'project', path: '' }]
  let acc = ''
  for (const part of String(path || '').split('/').filter(Boolean)) {
    acc = acc ? `${acc}/${part}` : part
    out.push({ label: part, path: acc })
  }
  return out
}

/** A handle the Flow view can run for a feature record. */
export function featureFlowTarget(f) {
  if (f?.handler?.selector) return { selector: f.handler.selector, symbol: f.handler.fqn || f.handler.symbol, inline: false }
  if (f?.handler?.file && f.handler.start_line) return { at: `${f.handler.file}:${f.handler.start_line}`, symbol: f.handler.symbol, inline: false }
  if (f?.registration?.file && f.registration.line) return { at: `${f.registration.file}:${f.registration.line}`, symbol: '', inline: true }
  return null
}

export function featureLabel(f) {
  return f?.invocation || f?.label || f?.id || ''
}

export function footprintSize(f) {
  return Number(f?.footprint?.symbols) || 0
}

// ------------------------------------------------------------- markdown out

const code = (s) => `\`${String(s ?? '').replace(/`/g, "'")}\``

export function atlasMarkdown(node, { project = '', children = [] } = {}) {
  if (!node) return ''
  const lines = []
  const title = node.path || project || node.name || 'project'
  lines.push(`## ${title}`)
  if (node.summary) lines.push('', node.summary)
  const langs = languageShares(node).slice(0, 5).map((l) => `${l.lang} ${Math.round(l.share * 100)}%`)
  lines.push('', `- ${node.files ?? 0} files · ${node.symbols ?? 0} symbols · ${node.lines ?? 0} lines · ${node.tests ?? 0} tests`)
  if (langs.length) lines.push(`- languages: ${langs.join(', ')}`)
  if (node.roles?.length) lines.push(`- roles: ${node.roles.join(', ')}`)
  if (node.inbound || node.outbound) lines.push(`- coupling: ${node.inbound ?? 0} inbound · ${node.outbound ?? 0} outbound · ${node.internal ?? 0} internal edges`)
  const kids = children.length ? children : node.children || []
  if (kids.length) {
    lines.push('', '### Contents')
    for (const c of kids.slice(0, 60)) {
      const tail = c.summary ? ` — ${c.summary}` : ''
      lines.push(`- ${code(c.path || c.name)}${c.type === 'dir' ? '/' : ''}${tail} (${c.symbols ?? 0} symbols)`)
    }
  }
  if (node.key_symbols?.length) {
    lines.push('', '### Key symbols')
    for (const s of node.key_symbols) {
      lines.push(`- ${code(s.fqn || s.symbol)} (${s.kind || 'symbol'}) — ${code(`${s.file}:${s.start_line}`)}${s.doc ? ` — ${s.doc}` : ''}`)
    }
  }
  const nb = node.top_neighbors || {}
  if (nb.in?.length || nb.out?.length) {
    lines.push('', '### Neighbours')
    if (nb.in?.length) lines.push(`- used by: ${nb.in.map((n) => `${code(n.path)} (${n.edges})`).join(', ')}`)
    if (nb.out?.length) lines.push(`- depends on: ${nb.out.map((n) => `${code(n.path)} (${n.edges})`).join(', ')}`)
  }
  return lines.join('\n')
}

export function flowMarkdown(flow, { feature = null } = {}) {
  if (!flow?.root) return ''
  const lines = []
  const root = flow.root
  lines.push(`## Flow: ${feature ? featureLabel(feature) : root.fqn || root.symbol}`)
  if (feature?.description) lines.push('', feature.description)
  lines.push('', `Entry ${code(root.fqn || root.symbol)} at ${code(`${root.file}:${root.start_line}`)}`)
  if (flow.subsystems?.length) lines.push('', `Route: ${flow.subsystems.map((s) => `${s.name} (${s.steps})`).join(' → ')}`)
  if (flow.call_graph) lines.push(`Call graph: ${flow.call_graph}${flow.ambiguous_calls ? ` · ${flow.ambiguous_calls} ambiguous call(s)` : ''}`)
  lines.push('')
  let n = 0
  const walk = (node, depth) => {
    n++
    const pad = '  '.repeat(depth)
    const where = node.file ? ` — ${code(`${node.file}:${node.start_line}`)}` : ''
    const flags = [node.confidence === 'candidate' ? 'candidate' : '', node.alternatives ? `+${node.alternatives} alternatives` : '', node.leaf_reason === 'ambiguous' ? 'ambiguous' : '', node.repeat_of ? `repeat of ${node.repeat_of}` : '', node.cycle ? 'cycle' : ''].filter(Boolean)
    lines.push(`${pad}${depth === 0 ? '1.' : `${node.call_order || 1}.`} ${code(node.fqn || node.symbol)}${where}${flags.length ? ` _(${flags.join(', ')})_` : ''}`)
    if (node.doc) lines.push(`${pad}   ${String(node.doc).split('\n')[0]}`)
    for (const c of node.children || []) walk(c, depth + 1)
  }
  walk(root, 0)
  if (flow.truncated) lines.push('', `_Trimmed: ${flow.steps_emitted ?? n} of ${flow.steps_total ?? '?'} steps shown._`)
  return lines.join('\n')
}
