/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Features: what the software can do. Every CLI command, RPC/MCP tool, HTTP
// route, page and program the detectors found, with its description and the
// footprint of code it touches. Click a card to watch it run (Flow).

import { h, clear, mount, debounce } from '../dom.mjs'
import { badge, callout, errorBox, spinner, tabs, toast, fmt } from '../components.mjs'
import { state } from '../state.mjs'
import { loadFeatures, featureFlowTarget, featureLabel, footprintSize } from '../learn.mjs'
import { featureCard, surfaceCounts, SURFACE_ORDER, reportCallouts } from '../learn-ui.mjs'

const PAGE = 400

export function featuresView(ctx, initial = {}) {
  let data = null
  let failed = null
  let surface = 'all'
  let query = initial.query || ''
  let sort = 'name'
  let limit = PAGE

  const host = h('div.view.features-view')
  const headHost = h('div')
  const barHost = h('div')
  const notesHost = h('div')
  const listHost = h('div.fc-list')

  const search = h('input', {
    type: 'search',
    class: 'fv-search',
    value: query,
    placeholder: 'Search name, description or file…',
    'aria-label': 'Search features',
    oninput: debounce((e) => {
      query = e.target.value
      limit = PAGE
      drawList()
    }, 120),
  })

  async function load(force = false) {
    mount(listHost, spinner('detecting features…'))
    const res = await loadFeatures({ force })
    if (!res?.ok || !res.json) {
      failed = res || { ok: false, error: 'no response' }
      data = null
    } else {
      failed = null
      data = res.json
    }
    drawAll()
  }

  const features = () => data?.features || []

  function matches(f) {
    if (surface !== 'all' && (f.surface || 'Other') !== surface) return false
    const q = query.trim().toLowerCase()
    if (!q) return true
    const hay = [featureLabel(f), f.label, f.description, f.handler?.fqn, f.handler?.file, f.registration?.file, f.framework].filter(Boolean).join('\n').toLowerCase()
    return hay.includes(q)
  }

  function ordered(list) {
    const flat = !!query.trim() || sort !== 'name'
    if (flat) {
      const out = list.slice()
      if (sort === 'footprint') out.sort((a, b) => footprintSize(b) - footprintSize(a) || featureLabel(a).localeCompare(featureLabel(b)))
      else if (sort === 'surface') out.sort((a, b) => SURFACE_ORDER.indexOf(a.surface) - SURFACE_ORDER.indexOf(b.surface) || featureLabel(a).localeCompare(featureLabel(b)))
      else out.sort((a, b) => featureLabel(a).localeCompare(featureLabel(b)))
      return out.map((f) => ({ f, depth: 0 }))
    }
    // group sub-commands under their parent, like the CLI's own tree
    const ids = new Set(list.map((f) => f.id))
    const kids = new Map()
    const roots = []
    for (const f of list) {
      if (f.parent && ids.has(f.parent)) {
        if (!kids.has(f.parent)) kids.set(f.parent, [])
        kids.get(f.parent).push(f)
      } else roots.push(f)
    }
    const byLabel = (a, b) => (a.surface === b.surface ? featureLabel(a).localeCompare(featureLabel(b)) : SURFACE_ORDER.indexOf(a.surface) - SURFACE_ORDER.indexOf(b.surface))
    const out = []
    const walk = (f, depth) => {
      out.push({ f, depth })
      for (const c of (kids.get(f.id) || []).sort(byLabel)) walk(c, depth + 1)
    }
    for (const r of roots.sort(byLabel)) walk(r, 0)
    return out
  }

  function openFlow(f) {
    const target = featureFlowTarget(f)
    if (!target) {
      toast(`“${featureLabel(f)}” has no handler to trace`, { tone: 'warn' })
      return
    }
    if (target.inline) toast('The handler is inline at the registration site — tracing from there.', { tone: 'info', title: featureLabel(f) })
    ctx.go('flow', { ...target, label: featureLabel(f), description: f.description, feature: f })
  }

  function cardActions(f) {
    const hd = f.handler || {}
    const at = hd.file && hd.start_line ? `${hd.file}:${hd.start_line}` : ''
    if (!at) return null
    return [
      h('button.btn.sm.ghost', { type: 'button', title: 'Open the handler in the source browser', onclick: () => ctx.openSymbol({ ...hd, file: hd.file, start_line: hd.start_line }) }, 'source'),
      h('button.btn.sm.ghost', { type: 'button', title: 'Everything about the handler', onclick: () => ctx.openFeature('context', { at: [at] }) }, 'context'),
      h('button.btn.sm.ghost', { type: 'button', title: 'Blast radius of changing the handler', onclick: () => ctx.openFeature('impact', { at: [at] }) }, 'impact'),
    ]
  }

  function drawHead() {
    const total = data?.features_total ?? features().length
    mount(headHost, h('div.view-head', [
      h('div.vh-main', [
        h('h1', ['Features', data ? badge(`${fmt.num(total)} found`, 'accent') : null, data?.truncated ? badge('truncated', 'warn', 'The list hit its cap — narrow it with a search.') : null]),
        h('div.vh-sub', 'Everything this software can do — its commands, tools, routes, pages and programs — with the slice of code each one touches. Click any card to follow it end to end.'),
        data?.frameworks?.length ? h('div.row.gap2', { style: 'margin-top:10px' }, [h('span.small.dim', 'detected via'), data.frameworks.map((f) => badge(f, 'plain'))]) : null,
      ]),
      h('div.vh-actions', [
        h('button.btn', { type: 'button', onclick: () => ctx.go('atlas') }, '▦ Atlas'),
        h('button.btn.primary', { type: 'button', onclick: () => load(true) }, '↻ Refresh'),
      ]),
    ]))
  }

  function drawBar() {
    if (!data) {
      clear(barHost)
      return
    }
    const counts = surfaceCounts(features())
    const items = [{ id: 'all', label: 'All', count: features().length }, ...counts.map(([s, n]) => ({ id: s, label: s === 'Program' ? 'Programs' : s === 'Page' ? 'Pages' : s, count: n }))]
    mount(barHost, h('div.fv-bar', [
      tabs(items, surface, (id) => {
        surface = id
        limit = PAGE
        drawBar()
        drawList()
      }),
      h('div.fv-tools', [
        search,
        h('label.fv-sort', [
          h('span', 'Sort'),
          h('select', { onchange: (e) => { sort = e.target.value; drawList() } }, [
            ['name', 'name'],
            ['footprint', 'footprint size'],
            ['surface', 'surface'],
          ].map(([v, t]) => h('option', { value: v, selected: sort === v }, t))),
        ]),
      ]),
    ]))
  }

  function drawNotes() {
    const callouts = reportCallouts(data, { resolution: false, notes: false })
    const about = []
    if (data?.resolution) about.push(data.resolution)
    for (const n of data?.notes || []) about.push(n)
    mount(notesHost, [
      callouts,
      about.length ? h('details.fv-about', [h('summary.small.muted', `About these numbers · ${data.call_graph === 'name' ? 'name-based call graph' : data.call_graph || 'call graph unknown'}`), h('div.small.muted', { style: 'margin-top:6px;line-height:1.6' }, about.join('\n'))]) : null,
    ])
  }

  function drawList() {
    clear(listHost)
    if (failed) {
      const notIndexed = failed.code === 'not_indexed' || failed.code === 'index_missing' || failed.json?.indexed === false
      listHost.appendChild(
        notIndexed
          ? callout('warn', 'This project is not indexed yet', 'Feature detection reads the code graph. Index once and the inventory appears.', [h('button.btn.primary', { type: 'button', onclick: () => ctx.openFeature('index') }, 'Index now')])
          : errorBox(failed),
      )
      return
    }
    if (!data) return
    if (data.indexed === false) {
      listHost.appendChild(callout('warn', 'This project is not indexed yet', 'Feature detection reads the code graph.', [h('button.btn.primary', { type: 'button', onclick: () => ctx.openFeature('index') }, 'Index now')]))
      return
    }
    const rows = ordered(features().filter(matches))
    if (!rows.length) {
      listHost.appendChild(
        h('div.empty', [
          h('div.e-ico', '✦'),
          h('div.e-title', features().length ? 'Nothing matches' : 'No features detected'),
          h('div.e-note', features().length ? 'Try a different search, or clear the surface filter.' : 'The detectors found no commands, tools, routes or programs. Frameworks other than cobra, the Go MCP SDK, HTTP muxes and Next.js are not detected yet.'),
        ]),
      )
      return
    }
    const shown = rows.slice(0, limit)
    listHost.appendChild(h('div.fv-count.small.dim', `${fmt.num(rows.length)} feature${rows.length === 1 ? '' : 's'}${query.trim() ? ` matching “${query.trim()}”` : ''}`))
    const frag = document.createDocumentFragment()
    for (const { f, depth } of shown) frag.appendChild(featureCard(f, { onOpen: openFlow, actions: cardActions(f), indent: depth, highlight: query.trim() }))
    listHost.appendChild(frag)
    if (rows.length > shown.length) {
      listHost.appendChild(h('div.fv-more', h('button.btn', { type: 'button', onclick: () => { limit += PAGE; drawList() } }, `Show ${Math.min(PAGE, rows.length - shown.length)} more of ${fmt.num(rows.length - shown.length)} remaining`)))
    }
  }

  function drawAll() {
    drawHead()
    drawBar()
    drawNotes()
    drawList()
  }

  mount(host, h('div.stack', { style: 'gap:14px' }, [headHost, barHost, notesHost, listHost]))
  drawHead()
  load()

  return {
    node: host,
    reload: () => load(true),
    navigate(opts = {}) {
      if (opts.query !== undefined) {
        query = opts.query
        search.value = query
        limit = PAGE
        surface = 'all'
        drawBar()
        drawList()
      }
    },
  }
}
