/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// App bootstrap: shell, router, sidebar, command palette, inspector and the
// shared context every view receives.

import { h, clear, mount, fuzzyScore, copy } from './dom.mjs'
import { state, bus, EVENTS, setView, openInspector, closeInspector } from './state.mjs'
import { FEATURES, GROUPS, APP_VIEWS, feature, defaultValues } from './features.mjs'
import { badge, callout, card, emptyState, kindBadge, spinner, symRow, toast, fmt, codeInline, chip, shortPath, callGraphBadge } from './components.mjs'
import { runArgs, refreshContext, loadProjects, followNext, argsToValues } from './runner.mjs'
import { graphPayloadFor } from './report.mjs'

import { dashboardView } from './views/dashboard.mjs'
import { catalogView } from './views/catalog.mjs'
import { searchView } from './views/searchview.mjs'
import { graphView } from './views/graphview.mjs'
import { sourceView } from './views/source.mjs'
import { reviewView } from './views/reviewview.mjs'
import { mapView } from './views/mapview.mjs'
import { mcpView } from './views/mcpview.mjs'
import { rawView } from './views/rawview.mjs'
import { historyView } from './views/historyview.mjs'
import { settingsView } from './views/settingsview.mjs'
import { onboardView } from './views/onboard.mjs'
import { featureView } from './views/feature.mjs'
import { overviewView } from './views/overview.mjs'
import { atlasView } from './views/atlasview.mjs'
import { featuresView } from './views/featuresview.mjs'
import { flowView } from './views/flowview.mjs'
import { clearLearnCache } from './learn.mjs'

const viewRoot = () => document.getElementById('view-root')
let current = null // {id, instance}
const instances = new Map()

// ------------------------------------------------------------------- context

const ctx = {
  go(view, opts) {
    route(view, opts)
  },
  openFeature(id, values, result) {
    const f = feature(id)
    if (!f) {
      toast(`unknown feature “${id}”`, { tone: 'error' })
      return
    }
    route('feature', { featureId: id, values, result })
  },
  onFeature: (id) => ctx.openFeature(id),
  openSymbol(sym) {
    if (!sym) return
    const file = sym.file || sym.selector?.file || sym.relative_path
    if (!file) {
      toast('that result carries no file position', { tone: 'warn' })
      return
    }
    const line = sym.start_line ?? sym.selector?.start_line ?? sym.line ?? 0
    showInspectorForSymbol(sym)
    if (current?.id !== 'source') route('source', { file, line })
    else instances.get('source')?.open(file, line)
  },
  onSymbol: (sym) => ctx.openSymbol(sym),
  openFile(path, line = 0) {
    if (!path) return
    if (current?.id !== 'source') route('source', { file: path, line })
    else instances.get('source')?.open(path, line)
  },
  onFile: (p) => ctx.openFile(p),
  onSymbolQuery(q) {
    route('search', { query: String(q).split('.').pop(), engine: 'find' })
  },
  onPrefill(id, values) {
    ctx.openFeature(id, values)
    const inst = instances.get(`feature:${id}`)
    if (inst && values) setTimeout(() => inst.setValues(values), 0)
  },
  onNext(s) {
    followNext(s)
  },
  onGraph(payload) {
    state.graph = payload
    route('graph', { mode: 'last' })
    setTimeout(() => instances.get('graph')?.load(payload, 'from the last report'), 0)
  },
  setGraphPayload(payload) {
    state.graph = payload
  },
  onRerun() {
    const inst = instances.get(current?.id)
    inst?.run?.() || inst?.reload?.()
  },
  onOpenProject(p) {
    if (p) ctx.setProject(p)
  },
  onMapView() {
    route('map')
  },
  async setProject(p) {
    if (!p) return
    const abs = await window.studio.project.use(p)
    if (abs?.error) {
      toast(abs.error, { tone: 'error' })
      return
    }
    state.project = typeof abs === 'string' ? abs : p
    state.tree = null
    await afterProjectChange()
  },
  async runSilent(args, id) {
    return runArgs(args, { featureId: id || '__silent', cwd: state.project })
  },
  renderInspector() {
    drawInspector()
  },
  applyTheme(theme) {
    document.documentElement.dataset.theme = theme === 'light' ? 'light' : 'dark'
    window.studio.theme(theme)
  },
  applyPrefs() {
    const s = state.settings
    document.body.dataset.density = s.density === 'compact' ? 'compact' : 'comfortable'
    document.body.dataset.fontsize = String(s.fontSize || 13)
  },
  feature,
  toast,
}

// --------------------------------------------------------------------- shell

function buildSidebar() {
  const host = document.querySelector('#sidebar .sidebar-scroll')
  const groups = new Map()
  for (const g of GROUPS) groups.set(g.id, [])
  for (const f of FEATURES) {
    if (f.view && f.run === false && APP_VIEWS.some((v) => v.id === f.view)) continue
    if (!groups.has(f.group)) groups.set(f.group, [])
    groups.get(f.group).push(f)
  }

  const items = []
  const viewEntry = (v) => ({ id: `view:${v.id}`, label: v.label, icon: v.icon, view: v.id })
  items.push(navGroup('Learn', APP_VIEWS.filter((v) => v.group === 'learn').map(viewEntry), 'learn'))
  items.push(navGroup('Workspace', APP_VIEWS.filter((v) => v.group === 'app').slice(0, 6).map(viewEntry), 'workspace'))
  for (const g of GROUPS) {
    const feats = groups.get(g.id) || []
    if (!feats.length) continue
    items.push(navGroup(g.label, feats.map((f) => ({ id: `feature:${f.id}`, label: f.title, icon: g.icon, featureId: f.id, mut: f.mutating, long: f.long })), g.id))
  }
  items.push(navGroup('App', [
    { id: 'view:catalog', label: 'Feature catalog', icon: '≡', view: 'catalog' },
    { id: 'view:history', label: 'Run history', icon: '↺', view: 'history' },
    { id: 'view:settings', label: 'Settings', icon: '⚙', view: 'settings' },
  ]))
  mount(host, items)

  host.addEventListener('click', (e) => {
    const btn = e.target.closest('[data-nav]')
    if (!btn) return
    const id = btn.dataset.nav
    if (id.startsWith('view:')) route(id.slice(5))
    else if (id.startsWith('feature:')) route('feature', { featureId: id.slice(8) })
  })
}

function navGroup(label, entries, gid) {
  return h('div.nav-group', { dataset: { group: gid || label } }, [
    h('div.nav-group-title', [label, h('span.count', String(entries.length))]),
    entries.map((e) =>
      h('button.nav-item', { type: 'button', dataset: { nav: e.id } }, [
        h('span.ico', e.icon || '·'),
        h('span.label', { title: e.label }, e.label),
        e.mut ? h('span.mut', '⚡') : null,
        e.long ? h('span.mut', '⏱') : null,
      ]),
    ),
  ])
}

function markActive() {
  const id = current?.id || ''
  for (const el of document.querySelectorAll('.nav-item')) {
    el.classList.toggle('active', el.dataset.nav === id || (id.startsWith('feature:') && el.dataset.nav === id))
  }
}

// -------------------------------------------------------------------- router

function route(view, opts = {}) {
  let id = view
  let make = null
  if (view === 'feature') {
    const f = feature(opts.featureId)
    if (!f) {
      toast(`unknown feature “${opts.featureId}”`, { tone: 'error' })
      return
    }
    id = `feature:${f.id}`
    state.featureId = f.id
    make = () => featureView(f, ctx, { values: opts.values || null, result: opts.result || null, autorun: !!opts.autorun })
  } else {
    id = `view:${view}`
    state.featureId = null
    make = () => {
      switch (view) {
        case 'overview': return overviewView(ctx)
        case 'atlas': return atlasView(ctx, opts)
        case 'features': return featuresView(ctx, opts)
        case 'flow': return flowView(ctx, opts)
        case 'dashboard': return dashboardView(ctx)
        case 'catalog': return catalogView(ctx)
        case 'search': return searchView(ctx, opts)
        case 'graph': return graphView(ctx, opts)
        case 'source': return sourceView(ctx, opts)
        case 'review': return reviewView(ctx)
        case 'map': return mapView(ctx)
        case 'mcp': return mcpView(ctx)
        case 'raw': return rawView(ctx, opts)
        case 'history': return historyView(ctx)
        case 'settings': return settingsView(ctx)
        case 'onboard': return onboardView(ctx)
        default: return { node: emptyState({ title: `Unknown view: ${view}` }) }
      }
    }
  }

  if (current && instances.has(current.id) && current.id !== id) {
    // keep instances alive so returning to a view preserves its state
  }
  state.view = view
  const prev = instances.get(id)
  let instance
  if (prev && !opts.fresh) {
    instance = prev
    if (opts.values && instance.setValues) instance.setValues(opts.values)
    if (opts.file && instance.open) instance.open(opts.file, opts.line)
    if (opts.query && instance.search) instance.search(opts.query, opts.engine)
    if (opts.at && instance.setStart) instance.setStart(opts.at)
    instance.navigate?.(opts)
    if (opts.featureId && view !== 'feature') instance.reload?.()
  } else {
    instance = make()
    instances.set(id, instance)
  }
  current = { id, view, instance }
  mount(viewRoot(), instance.node)
  viewRoot().scrollTop = 0
  markActive()
  updateStatusbar()
  if (instance.autorun) setTimeout(() => instance.autorun(), 0)
  bus.emit(EVENTS.VIEW, { view, id, opts })
}

// ----------------------------------------------------------------- inspector

function showInspectorForSymbol(sym) {
  openInspector({ title: sym.fqn || sym.symbol || 'symbol', kind: 'symbol', payload: { symbol: sym } })
}

function drawInspector() {
  const panel = document.getElementById('inspector')
  const body = document.getElementById('inspector-body')
  const title = document.getElementById('inspector-title')
  const ins = state.inspector
  if (!ins?.open) {
    panel.hidden = true
    return
  }
  panel.hidden = false
  title.textContent = ins.title || 'Inspector'
  clear(body)
  if (ins.kind === 'symbol' && ins.payload?.symbol) body.appendChild(symbolInspector(ins.payload.symbol))
  else if (ins.kind === 'symbols' && ins.payload) body.appendChild(symbolsInspector(ins.payload))
  else body.appendChild(h('pre.code', JSON.stringify(ins.payload, null, 2)))
}

function symbolInspector(sym) {
  const at = `${sym.file || sym.selector?.file || ''}:${sym.start_line ?? sym.selector?.start_line ?? ''}`
  const quick = (label, featureId, values) => h('button.btn.sm', { type: 'button', onclick: () => ctx.openFeature(featureId, values) }, label)
  return h('div.stack', { style: 'padding:12px' }, [
    h('div.row.gap2', [kindBadge(sym.kind || 'symbol'), callGraphBadge(sym.call_graph)]),
    h('div', { style: 'font-family:var(--font-mono);font-size:13px;color:var(--text);word-break:break-all' }, sym.fqn || sym.symbol || '(unnamed)'),
    sym.file ? h('button.btn.sm.ghost', { type: 'button', style: 'justify-content:flex-start', onclick: () => ctx.openFile(sym.file, sym.start_line) }, h('span.mono.small', `${shortPath(sym.file)}:${sym.start_line ?? ''}`)) : null,
    sym.signature ? h('pre.code', { style: 'font-size:11.5px' }, sym.signature) : null,
    sym.doc ? h('div.small.muted', { style: 'white-space:pre-wrap;line-height:1.6' }, sym.doc) : null,
    sym.source && !sym.source_omitted ? h('details', [h('summary.small.muted', 'source body'), h('pre.code', { style: 'margin-top:6px;font-size:11.5px;max-height:280px' }, sym.source)]) : null,
    h('div.divider'),
    h('h4', 'Run on this symbol'),
    h('div.btn-row', [
      quick('Context', 'context', { at: [at] }),
      quick('Callers', 'callers', { at }),
      quick('Callees', 'callees', { at }),
      quick('Impact', 'impact', { at: [at] }),
      quick('Risk', 'risk', { at }),
      quick('References', 'references', { at }),
      quick('Refactor plan', 'refactor-plan', { at }),
      quick('Source', 'source', { at }),
      quick('Traverse', 'traverse', { at }),
      quick('Annotate', 'annotate', { target: sym.fqn || sym.symbol }),
    ]),
    sym.file ? h('div.btn-row', [
      quick('Symbols in file', 'symbols', { file: sym.file }),
      quick('File context', 'file-context', { file: sym.file }),
      quick('File impact', 'file-impact', { file: sym.file }),
      quick('Dependencies', 'dependencies', { file: sym.file }),
      quick('Related files', 'related-files', { file: sym.file }),
    ]) : null,
    h('div.btn-row', [
      h('button.btn.sm', { type: 'button', onclick: () => { const p = graphPayloadFor({ id: 'context' }, { selector: { file: sym.file, start_line: sym.start_line, fqn: sym.fqn, kind: sym.kind }, symbol: sym.symbol }); if (p) ctx.onGraph(p); else ctx.openFeature('traverse', { at }); } }, '⇶ Graph'),
      h('button.btn.sm', { type: 'button', onclick: () => copy(JSON.stringify(sym, null, 2)).then(() => toast('symbol copied', { tone: 'ok' })) }, 'Copy JSON'),
      sym.selector ? h('button.btn.sm', { type: 'button', onclick: () => copy(JSON.stringify(sym.selector)).then(() => toast('durable selector copied', { tone: 'ok' })) }, 'Copy selector') : null,
    ]),
  ])
}

function symbolsInspector(payload) {
  const items = payload.items || []
  return h('div.stack', { style: 'padding:12px' }, [
    h('div.row.gap2', [badge(`${items.length} match(es)`, 'accent'), callGraphBadge(payload.json?.call_graph)]),
    items.length ? h('div.symlist', items.map((it) => symRow(it, { onPick: (s) => { showInspectorForSymbol(s); ctx.openFile(s.file, s.start_line) } }))) : emptyState({ note: payload.json?.error || 'nothing resolved' }),
    payload.json?.note ? callout('info', 'note', payload.json.note) : null,
    h('details', [h('summary.small.muted', 'raw'), h('pre.code', { style: 'font-size:11px;max-height:240px;margin-top:6px' }, JSON.stringify(payload.json, null, 2))]),
  ])
}

// ----------------------------------------------------------- command palette

const paletteState = { open: false, index: 0, items: [], query: '' }

function paletteItems(q) {
  const out = []
  const push = (item) => out.push(item)
  for (const v of APP_VIEWS) {
    const s = fuzzyScore(q, `${v.label} ${v.id}`)
    if (s > 0) push({ kind: 'view', score: s + 40, label: v.label, sub: `${v.icon} ${v.group === 'learn' ? 'learn view' : 'workspace view'}`, id: v.id, icon: v.icon })
  }
  for (const f of FEATURES) {
    const hay = `${f.title} ${f.id} ${f.blurb} ${(f.cmd || []).join(' ')} ${f.mcp || ''} ${f.group}`
    const s = fuzzyScore(q, hay)
    if (s > 0) push({ kind: 'feature', score: s + (q && f.id.startsWith(q.toLowerCase()) ? 200 : 0), label: f.title, sub: f.cmd?.length ? `codemap ${f.cmd.join(' ')}` : f.blurb, id: f.id, icon: f.mutating ? '⚡' : '▷' })
  }
  for (const p of state.settings.recentProjects || []) {
    const s = fuzzyScore(q, p)
    if (s > 0) push({ kind: 'project', score: s, label: p.split('/').pop(), sub: p, id: p, icon: '⌂' })
  }
  const files = (state.tree?.files || []).filter((f) => !q || f.toLowerCase().includes(q.toLowerCase())).slice(0, 8)
  for (const f of files) push({ kind: 'file', score: q ? 120 : 1, label: f.split('/').pop(), sub: f, id: f, icon: '⌸' })
  out.sort((a, b) => b.score - a.score)
  return out.slice(0, 40)
}

function openPalette(prefill = '') {
  const overlay = document.getElementById('palette')
  const input = document.getElementById('palette-input')
  overlay.hidden = false
  paletteState.open = true
  input.value = prefill
  paletteState.query = prefill
  paletteState.index = 0
  drawPalette()
  setTimeout(() => {
    input.focus()
    input.select()
  }, 10)
}

function closePalette() {
  document.getElementById('palette').hidden = true
  paletteState.open = false
}

function drawPalette(extra = []) {
  const list = document.getElementById('palette-list')
  const q = paletteState.query
  const items = q.trim() ? [...paletteItems(q), ...extra] : [...paletteItems(''), ...extra]
  paletteState.items = items
  clear(list)
  if (!items.length) {
    list.appendChild(h('div.palette-empty', [`Nothing matches “${q}”. `, h('button.btn.sm', { type: 'button', onclick: () => { closePalette(); ctx.go('raw', { text: q, fresh: true }) } }, `Run “${q}” in the raw runner`)]))
    return
  }
  items.forEach((it, i) => {
    list.appendChild(
      h('button.palette-row', {
        type: 'button',
        'aria-selected': i === paletteState.index ? 'true' : 'false',
        onclick: (e) => activatePalette(i, e.shiftKey),
        onmousemove: () => {
          paletteState.index = i
          paintSelection()
        },
      }, [
        h('span.p-ico', it.icon || '·'),
        h('span.p-main', [h('div.p-title', it.label), it.sub ? h('div.p-sub', it.sub) : null]),
        h('span.p-kind', it.kind),
      ]),
    )
  })
  const sel = list.children[paletteState.index]
  sel?.scrollIntoView({ block: 'nearest' })
}

function paintSelection() {
  const list = document.getElementById('palette-list')
  ;[...list.children].forEach((el, i) => el.setAttribute('aria-selected', i === paletteState.index ? 'true' : 'false'))
  list.children[paletteState.index]?.scrollIntoView({ block: 'nearest' })
}

let symbolTimer = null
function onPaletteInput(value) {
  paletteState.query = value
  paletteState.index = 0
  drawPalette()
  clearTimeout(symbolTimer)
  const q = value.trim()
  if (q.length < 2) return
  symbolTimer = setTimeout(async () => {
    if (paletteState.query !== value) return
    const res = await runArgs(['find', q, '--top', '8'], { featureId: '__palette_find' })
    if (paletteState.query !== value || !res?.ok) return
    const extra = (res.json?.hits || []).map((hit) => ({
      kind: 'symbol',
      score: 500,
      label: `${hit.fqn || hit.symbol}`,
      sub: `${hit.kind || ''} · ${hit.file}:${hit.start_line}`,
      icon: '§',
      symbol: hit,
    }))
    drawPalette(extra)
  }, 260)
}

function activatePalette(i, openOnly = false) {
  const it = paletteState.items[i]
  if (!it) return
  closePalette()
  switch (it.kind) {
    case 'view':
      ctx.go(it.id)
      break
    case 'feature':
      ctx.openFeature(it.id)
      break
    case 'project':
      ctx.setProject(it.id)
      break
    case 'file':
      ctx.openFile(it.id)
      break
    case 'symbol':
      ctx.openSymbol(it.symbol)
      break
    default:
      break
  }
}

// ---------------------------------------------------------------- statusbar

function updateStatusbar(extra) {
  const left = document.getElementById('sb-left')
  const center = document.getElementById('sb-center')
  const right = document.getElementById('sb-right')
  const s = state.status
  const git = state.git
  clear(left)
  left.appendChild(document.createTextNode(extra || (state.project ? `${state.project}` : 'no project selected')))
  clear(center)
  if (s) {
    center.append(
      chipText(`${fmt.num(s.nodes)} nodes`),
      chipText(`${fmt.num(s.edges)} edges`),
      chipText(`${fmt.num(s.files)} files`),
      chipText(`precise ${fmt.num(s.precise_edges || 0)}`),
    )
    const st = s.stale || {}
    const total = (st.changed || 0) + (st.new || 0) + (st.deleted || 0)
    center.appendChild(chipText(total ? `stale: ${total}` : 'fresh', total ? 'warn' : 'ok'))
  }
  if (git?.branch) center.appendChild(chipText(` ${git.branch}${git.changedCount ? ` · ${git.changedCount} dirty` : ''}`))
  clear(right)
  const ver = state.boot.version ? state.boot.version.split('\n')[0] : 'codemap not found'
  right.appendChild(h('span', ver))
  right.appendChild(h('button', { type: 'button', title: 'toggle theme', onclick: () => { const next = state.settings.theme === 'light' ? 'dark' : 'light'; state.settings.theme = next; window.studio.settings.set({ theme: next }); ctx.applyTheme(next) } }, '◐'))
  updateHealthChip()
}

function chipText(text, tone) {
  return h('span', { class: tone === 'warn' ? 'badge warn' : tone === 'ok' ? 'badge ok' : 'badge plain', style: 'height:16px;font-size:9.5px' }, text)
}

function updateHealthChip() {
  const chipEl = document.getElementById('health-chip')
  const s = state.status
  if (!s) {
    chipEl.dataset.state = 'missing'
    chipEl.textContent = 'no index'
    return
  }
  const st = s.stale || {}
  const total = (st.changed || 0) + (st.new || 0) + (st.deleted || 0)
  chipEl.dataset.state = state.running ? 'busy' : total ? 'stale' : 'fresh'
  clear(chipEl)
  chipEl.appendChild(h('span.dot'))
  chipEl.appendChild(document.createTextNode(total ? `${total} file(s) drifted` : state.running ? 'working…' : 'index fresh'))
}

// --------------------------------------------------------------------- boot

async function boot() {
  if (navigator.platform?.toLowerCase().includes('mac') || /mac/i.test(navigator.userAgent)) document.body.classList.add('mac')
  const [info, settings] = await Promise.all([window.studio.info(), window.studio.settings.all()])
  state.info = info
  state.settings = settings
  ctx.applyTheme(settings.theme)
  ctx.applyPrefs()
  state.boot.version = ''

  const version = await window.studio.binary.version()
  state.boot = { binary: version?.binary || null, version: version?.version || '', error: version?.ok ? '' : version?.error || 'not found' }

  // pick a project: remembered → first registered → repo we were launched from
  let project = settings.projectPath
  if (!project) {
    const projects = await loadProjects()
    project = projects[0]?.path || projects[0]?.root || ''
  }
  if (project) await window.studio.project.use(project)
  state.project = project || ''
  state.recentProjects = settings.recentProjects || []

  document.getElementById('boot').remove()
  document.getElementById('app').hidden = false

  buildSidebar()
  wireChrome()
  wireBus()

  const needsOnboard = !state.boot.binary || !state.project
  if (needsOnboard) route('onboard')
  else {
    mount(viewRoot(), h('div.view', spinner('opening the project…')))
    await afterProjectChange()
  }
  document.getElementById('side-version').textContent = `${state.boot.version ? state.boot.version.split('\n')[0] : 'codemap: not found'} · studio ${info.version}`
  updateStatusbar()
}

async function afterProjectChange() {
  updateStatusbar()
  const chipEl = document.getElementById('project-chip')
  clear(chipEl)
  chipEl.appendChild(document.createTextNode(state.project ? state.project.split('/').pop() : 'no project'))
  chipEl.title = state.project || 'choose a project'
  await refreshContext()
  await loadProjects()
  updateStatusbar()
  updateHealthChip()
  // a different project invalidates everything the Learn views cached
  clearLearnCache()
  for (const id of ['view:overview', 'view:atlas', 'view:features', 'view:flow']) {
    instances.get(id)?.destroy?.()
    instances.delete(id)
  }
  route(landingView(), { fresh: true })
}

// An indexed project opens on the Overview; anything else needs the Health
// dashboard's "index it" actions first.
function landingView() {
  const s = state.status
  return s && s.registered !== false && (s.nodes ?? 0) > 0 ? 'overview' : 'dashboard'
}

function wireChrome() {
  document.getElementById('settings-btn').onclick = () => ctx.go('settings')
  document.getElementById('palette-btn').onclick = () => openPalette()
  document.getElementById('inspector-close').onclick = () => {
    closeInspector()
    drawInspector()
  }
  document.getElementById('inspector-pop').onclick = () => {
    const ins = state.inspector
    if (ins?.payload?.symbol) ctx.openSymbol(ins.payload.symbol)
  }
  document.getElementById('project-chip').onclick = async () => {
    const p = await window.studio.project.pick()
    if (p) await ctx.setProject(p)
  }
  document.getElementById('health-chip').onclick = () => ctx.openFeature('status')
  document.getElementById('run-indicator').onclick = () => toast(`${state.running} command(s) in flight`, { title: 'Running' })

  const input = document.getElementById('palette-input')
  input.addEventListener('input', (e) => onPaletteInput(e.target.value))
  input.addEventListener('keydown', (e) => {
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      paletteState.index = Math.min(paletteState.items.length - 1, paletteState.index + 1)
      paintSelection()
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      paletteState.index = Math.max(0, paletteState.index - 1)
      paintSelection()
    } else if (e.key === 'Enter') {
      e.preventDefault()
      activatePalette(paletteState.index, e.shiftKey)
    } else if (e.key === 'Escape') {
      e.preventDefault()
      closePalette()
    }
  })
  document.getElementById('palette').addEventListener('mousedown', (e) => {
    if (e.target.id === 'palette') closePalette()
  })

  document.addEventListener('keydown', (e) => {
    const mod = e.metaKey || e.ctrlKey
    if (mod && e.key.toLowerCase() === 'k') {
      e.preventDefault()
      if (paletteState.open) closePalette()
      else openPalette()
      return
    }
    if (mod && e.key.toLowerCase() === 'f' && !paletteState.open) {
      e.preventDefault()
      ctx.go('search')
      return
    }
    if (mod && e.key.toLowerCase() === 'p') {
      e.preventDefault()
      ctx.go('catalog')
      return
    }
    if (mod && /^[1-6]$/.test(e.key)) {
      e.preventDefault()
      const map = ['dashboard', 'search', 'graph', 'source', 'review', 'map']
      ctx.go(map[Number(e.key) - 1])
      return
    }
    if (e.key === 'Escape') {
      if (paletteState.open) closePalette()
      else if (state.inspector.open) {
        closeInspector()
        drawInspector()
      }
    }
    if (e.key === 'r' && mod && e.shiftKey) {
      e.preventDefault()
      ctx.onRerun()
    }
  })

  window.studio.on('ui:goto', (p) => ctx.go(p?.view || 'dashboard'))
  window.studio.on('ui:palette', () => openPalette())
  window.studio.on('app:error', (p) => toast(p.message, { tone: 'error', title: 'App error' }))
}

function wireBus() {
  bus.on(EVENTS.STATUS, () => {
    updateStatusbar()
    updateHealthChip()
  })
  bus.on(EVENTS.RUNNING, (n) => {
    const el = document.getElementById('run-indicator')
    el.hidden = !n
    if (n) el.textContent = `● ${n} running`
    updateHealthChip()
  })
  bus.on(EVENTS.INSPECTOR, () => drawInspector())
  bus.on(EVENTS.RESULT, (res) => {
    if (res.ok && (res.featureId === 'index' || res.featureId === 'index-precise')) clearLearnCache()
    const cmd = (res.command || []).slice(1).join(' ')
    updateStatusbar(`$ codemap ${cmd} → exit ${res.exitCode} in ${fmt.ms(res.ms)}`)
  })
  window.studio.on('codemap:stream', (evt) => {
    if (evt.type === 'progress') {
      const el = document.getElementById('run-indicator')
      el.hidden = false
      el.textContent = `● ${Math.round((evt.done / Math.max(1, evt.total)) * 100)}%`
    }
    if (evt.type === 'end') updateHealthChip()
  })
}

boot().catch((err) => {
  const bootEl = document.getElementById('boot')
  if (bootEl) {
    mount(bootEl, h('div.stack', { style: 'max-width:520px;text-align:center' }, [
      h('div.boot-mark', 'codemap studio'),
      callout('danger', 'Startup failed', String(err?.message || err)),
    ]))
  }
  console.error(err)
})

// Debug/test handle: lets the smoke driver (and a curious user in DevTools)
// route views and read app state without clicking through the UI.
window.__studio = { state, ctx, route, FEATURES, GROUPS, openPalette, instances, boot }
