/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Atlas: the codebase as a map. A squarified treemap of the current directory
// (children as framed tiles, their children drawn inside), coloured by
// language / role / coupling / test share, with a detail panel that explains
// whatever you point at in plain words. Zoom with a click; Esc zooms out.

import { h, clear, mount, copy, throttle } from '../dom.mjs'
import { badge, callout, errorBox, kindBadge, spinner, toast, fmt } from '../components.mjs'
import { state } from '../state.mjs'
import { squarify, inset } from '../treemap.mjs'
import { languageColor, roleStyle, heatMix, logScale, ROLE_LEGEND } from '../palette.mjs'
import { languageBar, roleBadges } from '../learn-ui.mjs'
import {
  loadAtlas, loadPrefs, savePrefs, isIndexedAtlas, sizeOf, shortNum, dominantLanguage, languageShares, findNode, walkNodes, baseName, parentPath, trail, atlasMarkdown,
} from '../learn.mjs'

const GAP = 1.5
const HEAD_H = 24

const px = (n) => `${Math.round(n * 10) / 10}px`
const UNIT = { symbols: 'symbols', lines: 'lines', files: 'files' }

export function atlasView(ctx, initial = {}) {
  const prefs = loadPrefs()
  let prefix = initial.prefix || ''
  let data = null
  let index = new Map()
  let maxInbound = 1
  let selected = null
  let loadToken = 0
  let failed = null

  const host = h('div.view.atlas-view')
  const crumbs = h('div.at-crumbs')
  const badges = h('div.row.gap2')
  const controls = h('div.at-controls')
  const tiles = h('div.at-tiles')
  const tooltip = h('div.at-tooltip', { hidden: true })
  const legend = h('div.at-legend')
  const loader = h('div.at-loader', { hidden: true }, [h('div.spinner'), h('span', 'reading the map…')])
  const overlay = h('div.at-overlay', { hidden: true })
  const mapEl = h('div.at-map', { tabindex: '-1' }, [tiles, overlay, tooltip, loader])
  const detail = h('aside.at-detail')
  const detailToggle = h('button.btn.sm', { type: 'button', title: 'show or hide the detail panel', onclick: () => setDetail(!prefs.detail) })
  const stage = h('div.at-stage', [mapEl, detail])

  mount(host, [
    h('div.at-head-row', [
      h('div.at-title', [h('h1', 'Atlas'), crumbs]),
      h('div.at-head-right', [badges, controls]),
    ]),
    legend,
    stage,
  ])
  setDetail(prefs.detail, false)

  // ---------------------------------------------------------------- loading

  async function load(path = prefix, dir = null, { force = false } = {}) {
    const token = ++loadToken
    loader.hidden = false
    mapEl.classList.add('busy')
    const res = await loadAtlas({ prefix: path, depth: 2, files: true, maxNodes: 3000 }, { force })
    if (token !== loadToken) return
    loader.hidden = true
    mapEl.classList.remove('busy')
    if (!res?.ok || !res.json?.tree) {
      failed = res?.ok ? { ...res, ok: false, code: 'operational', error: 'the atlas response carried no tree — is this an older codemap binary?' } : res || { ok: false, error: 'no response' }
      data = null
      drawAll()
      return
    }
    failed = null
    data = res.json
    prefix = data.prefix ?? path
    selected = null
    index = new Map()
    maxInbound = 1
    walkNodes(data.tree, (n) => {
      index.set(n.path || '', n)
      if (n !== data.tree) maxInbound = Math.max(maxInbound, Number(n.inbound) || 0)
    })
    drawAll()
    if (dir) animate(dir)
  }

  function zoomTo(path, originEl = null) {
    path = String(path || '').replace(/\/+$/, '')
    if (path === prefix && data) return
    if (originEl && mapEl.getBoundingClientRect) {
      const m = mapEl.getBoundingClientRect()
      const t = originEl.getBoundingClientRect()
      mapEl.style.setProperty?.('--ox', `${Math.round(t.left - m.left + t.width / 2)}px`)
      mapEl.style.setProperty?.('--oy', `${Math.round(t.top - m.top + t.height / 2)}px`)
    } else {
      mapEl.style.setProperty?.('--ox', '50%')
      mapEl.style.setProperty?.('--oy', '50%')
    }
    const dir = !prefix || path.startsWith(`${prefix}/`) ? 'in' : 'out'
    load(path, dir)
  }

  function zoomOut() {
    if (!prefix) return false
    zoomTo(parentPath(prefix))
    return true
  }

  function animate(dir) {
    tiles.classList.remove('zoom-in', 'zoom-out')
    void tiles.offsetWidth
    tiles.classList.add(dir === 'in' ? 'zoom-in' : 'zoom-out')
  }

  // ------------------------------------------------------------------ paint

  function paint(node, parent) {
    const mode = prefs.color
    if (mode === 'role') return roleStyle(node.roles)
    if (mode === 'coupling') {
      const t = logScale(node.inbound, maxInbound)
      return { color: t > 0.7 ? 'var(--danger)' : t > 0.4 ? 'var(--warn)' : 'var(--accent)', mix: heatMix(t) }
    }
    if (mode === 'tests') {
      if (node.type === 'file' && (node.roles || []).includes('tests')) return { color: 'var(--info)', mix: 30 }
      const src = node.type === 'file' ? parent || node : node
      const symbols = Number(src.symbols) || 0
      const share = symbols ? (Number(src.tests) || 0) / symbols : 0
      if (!symbols) return { color: 'var(--text-3)', mix: 14 }
      if (share === 0) return { color: 'var(--danger)', mix: 26 }
      if (share < 0.1) return { color: 'var(--warn)', mix: 34 }
      return { color: 'var(--ok)', mix: Math.round(28 + Math.min(1, share / 0.4) * 34) }
    }
    return { color: languageColor(dominantLanguage(node)), mix: node.type === 'file' ? 36 : 30 }
  }

  const weight = (n) => {
    const v = sizeOf(n, prefs.size)
    return v
  }

  function layoutItems(nodes, rect) {
    const live = nodes.filter((n) => weight(n) > 0)
    if (!live.length) return []
    const total = live.reduce((s, n) => s + weight(n), 0)
    const floor = total * 0.003
    return squarify(live.map((n) => ({ value: Math.max(weight(n), floor), node: n })), rect)
  }

  function tileName(n) {
    return baseName(n.path) + (n.type === 'dir' ? '/' : '')
  }

  function metricText(n) {
    return `${shortNum(sizeOf(n, prefs.size))}`
  }

  function makeTile(node, r, level, parent) {
    const w = r.w - GAP * 2
    const hh = r.h - GAP * 2
    if (w < 3 || hh < 3) return null
    const isDir = node.type === 'dir'
    const p = paint(node, parent)
    const roles = node.roles || []
    const testsOnly = roles.includes('tests') && !roles.includes('source')
    const el = h('div.at-tile', {
      class: `at-tile l${level} ${isDir ? 'dir' : 'file'}${testsOnly ? ' is-tests' : ''}${selected === node.path ? ' sel' : ''}`,
      tabindex: w > 24 && hh > 14 ? '0' : null,
      role: 'button',
      'aria-label': `${node.path} — ${metricText(node)} ${UNIT[prefs.size]}`,
      dataset: { path: node.path, type: node.type },
      style: { left: px(r.x + GAP), top: px(r.y + GAP), width: px(w), height: px(hh), '--c': p.color, '--m': `${p.mix}%` },
    })

    const roomy = w >= 64 && hh >= 44
    if (level === 1 && isDir && roomy && (node.children || []).length) {
      // framed group: header strip + the children laid out inside
      el.classList.add('group')
      const langs = languageShares(node)
      const strip = langs.length
        ? h('i.at-langstrip', { style: { background: stripGradient(langs) } })
        : null
      el.appendChild(
        h('div.at-head', [
          h('span.at-name', tileName(node)),
          h('span.at-metric', metricText(node)),
          strip,
        ]),
      )
      const inner = inset({ x: 0, y: 0, w, h: hh }, 3, HEAD_H)
      for (const cr of layoutItems(node.children, { x: 0, y: 0, w: inner.w, h: inner.h })) {
        const t = makeTile(cr.item.node, { x: cr.x + inner.x, y: cr.y + inner.y, w: cr.w, h: cr.h }, 2, node)
        if (t) el.appendChild(t)
      }
      return el
    }

    if (w >= 38 && hh >= 17) {
      el.appendChild(h('span.at-name', tileName(node)))
      if (w >= 58 && hh >= 34) el.appendChild(h('span.at-sub', `${metricText(node)} ${prefs.size === 'symbols' ? 'sym' : prefs.size}`))
    }
    return el
  }

  function stripGradient(langs) {
    let acc = 0
    const stops = langs.map((l) => {
      const from = acc
      acc += l.share * 100
      return `${languageColor(l.lang)} ${from.toFixed(1)}% ${acc.toFixed(1)}%`
    })
    return `linear-gradient(90deg, ${stops.join(', ')})`
  }

  function drawTiles() {
    clear(tiles)
    if (failed) return
    overlay.hidden = true
    if (!data) return
    const rect = mapEl.getBoundingClientRect()
    const W = rect.width || 800
    const H = rect.height || 520
    const root = data.tree
    const kids = (root.children || []).filter((c) => weight(c) > 0)
    if (!kids.length) {
      overlay.hidden = false
      mount(overlay, h('div.at-empty', [h('div.e-title', 'Nothing to draw here'), h('div.small.muted', root.type === 'file' ? 'This is a single file — open it in the source browser.' : 'This directory has no indexed symbols for the chosen size metric.')]))
      return
    }
    const frag = document.createDocumentFragment()
    for (const r of layoutItems(kids, { x: 0, y: 0, w: W, h: H })) {
      const t = makeTile(r.item.node, r, 1, root)
      if (t) frag.appendChild(t)
    }
    tiles.appendChild(frag)
  }

  // ----------------------------------------------------------------- chrome

  function drawCrumbs() {
    clear(crumbs)
    const parts = trail(data?.project || state.project.split('/').pop(), prefix)
    parts.forEach((p, i) => {
      if (i) crumbs.appendChild(h('span.sep', '›'))
      const last = i === parts.length - 1
      crumbs.appendChild(h(last ? 'span.crumb.on' : 'button.crumb', { type: last ? null : 'button', onclick: last ? null : () => zoomTo(p.path) }, p.label))
    })
  }

  function seg(options, current, onPick, label) {
    return h('div.seg', { role: 'group', 'aria-label': label }, [
      h('span.seg-label', label),
      options.map(([id, text]) => h('button.seg-btn', { type: 'button', class: id === current ? 'seg-btn on' : 'seg-btn', onclick: () => onPick(id) }, text)),
    ])
  }

  function drawControls() {
    mount(controls, [
      seg([['symbols', 'symbols'], ['lines', 'lines'], ['files', 'files']], prefs.size, (v) => { prefs.size = v; savePrefs(prefs); drawAll() }, 'Size by'),
      seg([['language', 'language'], ['role', 'role'], ['coupling', 'coupling'], ['tests', 'tests']], prefs.color, (v) => { prefs.color = v; savePrefs(prefs); drawAll() }, 'Colour by'),
      h('button.btn.sm', { type: 'button', title: 'Reload from the index', onclick: () => load(prefix, null, { force: true }) }, '↻'),
      detailToggle,
    ])
  }

  function drawBadges() {
    mount(badges, [
      data?.truncated ? badge('truncated', 'warn', 'The map hit its node cap — zoom into a directory to see everything.') : null,
      data?.stale ? badge('stale index', 'warn') : null,
      data ? badge(`${fmt.num(data.tree?.files ?? 0)} files · ${fmt.num(data.tree?.symbols ?? 0)} symbols`, 'plain') : null,
    ])
  }

  function drawLegend() {
    clear(legend)
    legend.hidden = !data
    if (!data) return
    const mode = prefs.color
    const sw = (color, mix, text) => h('span.lg', [h('i.sw', { style: { background: `color-mix(in srgb, ${color} ${mix}%, var(--surface-2))`, borderColor: `color-mix(in srgb, ${color} 70%, transparent)` } }), text])
    if (mode === 'language') {
      const totals = new Map()
      walkNodes(data.tree, (n) => {
        if (n === data.tree || n.type !== 'file') return
        const l = n.language || dominantLanguage(n)
        if (l) totals.set(l, (totals.get(l) || 0) + sizeOf(n, prefs.size))
      })
      const top = [...totals.entries()].sort((a, b) => b[1] - a[1]).slice(0, 7)
      mount(legend, [h('span.lg-title', 'language'), top.map(([l]) => sw(languageColor(l), 36, l)), h('span.lg-note', 'click a folder to zoom in · shift-click to inspect · esc zooms out')])
    } else if (mode === 'role') {
      mount(legend, [h('span.lg-title', 'role'), ROLE_LEGEND.map((r) => sw(r.color, r.mix, r.label))])
    } else if (mode === 'coupling') {
      mount(legend, [
        h('span.lg-title', 'inbound edges'),
        sw('var(--accent)', heatMix(0.1), 'few'),
        sw('var(--warn)', heatMix(0.55), 'some'),
        sw('var(--danger)', heatMix(0.95), 'many'),
        h('span.lg-note', 'how much other code depends on it'),
      ])
    } else {
      mount(legend, [
        h('span.lg-title', 'tests'),
        sw('var(--danger)', 26, 'none'),
        sw('var(--warn)', 34, 'thin (<10%)'),
        sw('var(--ok)', 52, 'covered'),
        sw('var(--info)', 30, 'test files'),
      ])
    }
  }

  function setDetail(open, save = true) {
    prefs.detail = !!open
    stage.classList.toggle('no-detail', !prefs.detail)
    detail.hidden = !prefs.detail
    detailToggle.textContent = prefs.detail ? 'Hide details ⟩' : '⟨ Details'
    if (save) {
      savePrefs(prefs)
      requestAnimationFrame(() => drawTiles())
    }
  }

  function drawAll() {
    drawCrumbs()
    drawControls()
    drawBadges()
    drawLegend()
    drawStates()
    drawTiles()
    drawDetail()
  }

  function drawStates() {
    if (!failed) return
    clear(tiles)
    overlay.hidden = false
    const notIndexed = failed.code === 'not_indexed' || failed.code === 'index_missing' || failed.json?.indexed === false
    mount(
      overlay,
      notIndexed
        ? callout('warn', 'This project is not indexed yet', 'The atlas is drawn from the code graph. Build the index once and the map appears.', [
            h('button.btn.primary', { type: 'button', onclick: () => ctx.openFeature('index') }, 'Index now'),
            h('button.btn', { type: 'button', onclick: () => ctx.go('dashboard') }, 'Open Health'),
          ])
        : errorBox(failed),
    )
  }

  // ----------------------------------------------------------------- detail

  function current() {
    if (!data) return null
    return (selected && index.get(selected)) || data.tree
  }

  function metricCell(label, value, title = '') {
    return h('div.dm', { title }, [h('b', typeof value === 'number' ? fmt.num(value) : value), h('span', label)])
  }

  function symbolRow(s) {
    return h('div.ds-row', [
      h('button.ds-main', { type: 'button', title: `${s.file}:${s.start_line}`, onclick: () => ctx.openSymbol(s) }, [
        kindBadge(s.kind || 'symbol'),
        h('span.ds-name', s.symbol || s.fqn),
        s.in_degree ? h('span.ds-deg', `${fmt.num(s.in_degree)} callers`) : null,
        s.doc ? h('span.ds-doc', String(s.doc).split('\n')[0]) : null,
      ]),
      h('button.btn.sm.ghost', { type: 'button', title: 'Trace this symbol end to end', onclick: () => ctx.go('flow', { selector: s.selector, symbol: s.fqn || s.symbol, label: s.symbol }) }, 'flow'),
    ])
  }

  function neighbourList(title, arrow, list, total) {
    if (!list?.length) return null
    const max = Math.max(1, ...list.map((n) => n.edges || 0))
    return h('div.dn', [
      h('h5', `${arrow} ${title}`),
      list.slice(0, 6).map((n) =>
        h('button.dn-row', { type: 'button', title: `zoom to ${n.path}`, onclick: () => zoomTo(n.path) }, [
          h('span.dn-bar', { style: { width: `${Math.max(4, ((n.edges || 0) / max) * 100)}%` } }),
          h('span.dn-path', n.path),
          h('span.dn-n', fmt.num(n.edges)),
        ]),
      ),
    ])
  }

  function drawDetail() {
    clear(detail)
    if (failed || !data) {
      detail.appendChild(h('div.small.muted', { style: 'padding:16px' }, failed ? 'No data for this directory.' : 'Reading the map…'))
      return
    }
    const n = current()
    const isRoot = n === data.tree
    const path = n.path || ''
    const kids = (n.children || []).filter((c) => weight(c) > 0).sort((a, b) => weight(b) - weight(a))

    detail.appendChild(
      h('div.dp', [
        h('div.dp-head', [
          h('div.dp-name', n.name || data.project || 'project'),
          h('div.dp-path', path || '(project root)'),
          h('div.row.gap2', { style: 'margin-top:8px' }, [
            n.type === 'file' ? badge('file', 'plain') : badge('directory', 'plain'),
            roleBadges(n.roles),
            n.language ? badge(n.language, 'plain') : null,
            selected && !isRoot ? h('button.btn.sm.ghost', { type: 'button', onclick: () => { selected = null; paintSelection(); drawDetail() } }, 'clear selection') : null,
          ]),
        ]),
        n.summary
          ? h('p.dp-summary', n.summary)
          : h('p.dp-summary.empty', [
              'No description found. ',
              n.type === 'file' ? 'Files rarely carry one.' : 'A package doc comment, or a README.md in this directory, would show up here.',
            ]),
        h('div.dp-metrics', [
          metricCell('files', n.files ?? 0),
          metricCell('symbols', n.symbols ?? 0),
          metricCell('lines', n.lines ?? 0),
          metricCell('tests', n.tests ?? 0, 'test symbols'),
          metricCell('inbound', n.inbound ?? 0, 'edges from other directories into this one'),
          metricCell('outbound', n.outbound ?? 0, 'edges from this directory out to others'),
          metricCell('internal', n.internal ?? 0, 'edges that stay inside'),
        ]),
        Object.keys(n.languages || {}).length ? h('div.dp-sec', [h('h4', 'Languages'), languageBar(n.languages, { legend: true, max: 5 })]) : null,
        n.key_symbols?.length ? h('div.dp-sec', [h('h4', 'Key symbols'), h('div.ds-list', n.key_symbols.slice(0, 8).map(symbolRow))]) : null,
        n.top_neighbors && (n.top_neighbors.in?.length || n.top_neighbors.out?.length)
          ? h('div.dp-sec', [h('h4', 'Neighbours'), neighbourList('used by', '←', n.top_neighbors.in), neighbourList('depends on', '→', n.top_neighbors.out)])
          : null,
        n.type === 'dir' && kids.length
          ? h('div.dp-sec', [
              h('h4', `Inside · ${kids.length}`),
              h('div.dc-list', kids.slice(0, 12).map((c) =>
                h('button.dc-row', { type: 'button', onclick: () => (c.type === 'dir' ? zoomTo(c.path) : (selected = c.path, paintSelection(), drawDetail())), title: c.summary || c.path }, [
                  h('i.dc-dot', { style: { background: languageColor(dominantLanguage(c)) } }),
                  h('span.dc-name', baseName(c.path) + (c.type === 'dir' ? '/' : '')),
                  h('span.dc-n', shortNum(sizeOf(c, prefs.size))),
                ]),
              )),
            ])
          : null,
        h('div.dp-actions', [
          n.type === 'dir' && !isRoot ? h('button.btn.sm.primary', { type: 'button', onclick: () => zoomTo(path) }, 'Zoom in') : null,
          n.type === 'file' ? h('button.btn.sm.primary', { type: 'button', onclick: () => ctx.openFile(path) }, 'Open source') : null,
          h('button.btn.sm', { type: 'button', onclick: () => ctx.go('features', { query: path }) }, 'Features here'),
          h('button.btn.sm', { type: 'button', onclick: () => ctx.openFeature('read-order', { query: path }) }, 'Read order here'),
          h('button.btn.sm', { type: 'button', onclick: () => ctx.go('source', { filter: path }) }, 'Browse files'),
          h('button.btn.sm', { type: 'button', onclick: async () => { await copy(atlasMarkdown(n, { project: data.project, children: kids })); toast('copied as markdown — paste it into an agent chat or your notes', { tone: 'ok' }) } }, 'Copy as markdown'),
        ]),
      ]),
    )
  }

  function paintSelection() {
    for (const el of tiles.querySelectorAll('.at-tile.sel')) el.classList.remove('sel')
    if (!selected) return
    for (const el of tiles.querySelectorAll('.at-tile')) {
      if (el.dataset.path === selected) el.classList.add('sel')
    }
  }

  // --------------------------------------------------------------- tooltip

  function showTip(el, e) {
    const node = el && index.get(el.dataset.path)
    if (!node) {
      tooltip.hidden = true
      return
    }
    mount(tooltip, [
      h('div.tt-path', node.path),
      node.summary ? h('div.tt-sum', node.summary) : null,
      h('div.tt-m', `${fmt.num(node.files ?? 0)} files · ${fmt.num(node.symbols ?? 0)} symbols · ${fmt.num(node.lines ?? 0)} lines${node.tests ? ` · ${fmt.num(node.tests)} tests` : ''}`),
      h('div.tt-hint', node.type === 'dir' ? 'click to zoom in · shift-click to inspect' : 'click to inspect'),
    ])
    tooltip.hidden = false
    const m = mapEl.getBoundingClientRect()
    const x = (e.clientX ?? 0) - m.left
    const y = (e.clientY ?? 0) - m.top
    const tw = 300
    tooltip.style.left = px(Math.max(8, Math.min(x + 16, m.width - tw - 8)))
    tooltip.style.top = px(Math.max(8, Math.min(y + 18, m.height - 120)))
  }

  mapEl.addEventListener('mousemove', (e) => {
    const t = e.target instanceof Element ? e.target.closest('.at-tile') : null
    if (t) showTip(t, e)
    else tooltip.hidden = true
  })
  mapEl.addEventListener('mouseleave', () => {
    tooltip.hidden = true
  })
  mapEl.addEventListener('click', (e) => {
    const t = e.target instanceof Element ? e.target.closest('.at-tile') : null
    if (!t) return
    const path = t.dataset.path
    const node = index.get(path)
    if (!node) return
    tooltip.hidden = true
    if (node.type === 'dir' && !e.shiftKey) {
      zoomTo(path, t)
    } else {
      selected = path
      paintSelection()
      drawDetail()
      if (!prefs.detail) setDetail(true)
    }
  })
  mapEl.addEventListener('keydown', (e) => {
    const t = e.target instanceof Element ? e.target.closest('.at-tile') : null
    if (!t) return
    const node = index.get(t.dataset.path)
    if (!node) return
    if (e.key === 'Enter') {
      e.preventDefault()
      if (node.type === 'dir') zoomTo(node.path, t)
      else {
        selected = node.path
        paintSelection()
        drawDetail()
      }
    } else if (e.key === ' ') {
      e.preventDefault()
      selected = node.path
      paintSelection()
      drawDetail()
    }
  })

  const onKey = (e) => {
    if (host.isConnected === false) return
    const tag = e.target?.tagName
    if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return
    if (e.metaKey || e.ctrlKey || e.altKey) return
    if (document.getElementById?.('palette') && !document.getElementById('palette').hidden) return
    if (e.key === 'Backspace' || e.key === 'Escape') {
      if (selected) {
        selected = null
        paintSelection()
        drawDetail()
        e.preventDefault()
      } else if (zoomOut()) e.preventDefault()
    }
  }
  document.addEventListener('keydown', onKey)

  const relayout = throttle(() => {
    if (data) drawTiles()
  }, 140)
  const ro = new ResizeObserver(() => relayout())
  ro.observe(mapEl)

  drawAll()
  load(prefix)

  return {
    node: host,
    reload: () => load(prefix, null, { force: true }),
    navigate(opts = {}) {
      if (opts.prefix !== undefined && opts.prefix !== prefix) zoomTo(opts.prefix)
    },
    destroy() {
      document.removeEventListener('keydown', onKey)
      ro.disconnect()
    },
    // exposed for the smoke driver
    zoomTo,
    get prefix() {
      return prefix
    },
  }
}
