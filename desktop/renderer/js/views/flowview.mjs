/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Flow: how one feature works, end to end. The call tree of an entry symbol in
// call order — an outline you can walk with the keyboard, a left-to-right
// diagram you can pan and zoom, the route it takes through the subsystems, and
// the code of whichever step you select.

import { h, clear, mount, copy } from '../dom.mjs'
import { badge, callout, callGraphBadge, staleBadge, errorBox, kindBadge, spinner, codeBlock, tabs, toast, fmt } from '../components.mjs'
import { runArgs } from '../runner.mjs'
import { state } from '../state.mjs'
import { langFromPath } from '../highlight.mjs'
import { subsystemColor } from '../palette.mjs'
import { routeStrip, featureCard, representativeFeatures, reportCallouts } from '../learn-ui.mjs'
import { runFlow, recentFlows, rememberFlow, loadFeatures, flowMarkdown, featureFlowTarget, featureLabel } from '../learn.mjs'
import { graphPayloadFor } from '../report.mjs'

const NODE_W = 196
const NODE_H = 34
const COL_W = 236
const ROW_H = 44
const DIAGRAM_CAP = 400

function firstLine(s) {
  return String(s || '').split('\n')[0].trim()
}

function clip(s, n) {
  const t = String(s || '')
  return t.length > n ? `${t.slice(0, n - 1)}…` : t
}

export function flowView(ctx, initial = {}) {
  let json = null
  let failed = null
  let entry = {}
  let depth = 4
  let maxNodes = 150
  let mode = 'outline'
  let selectedId = null
  let highlightSub = ''
  let runToken = 0
  let previewToken = 0
  const collapsed = new Set()
  const byId = new Map()
  const parentOf = new Map()
  let order = []
  const rows = new Map()
  const sourceCache = new Map()

  const host = h('div.view.flow-view')
  const entryHost = h('div')
  const headHost = h('div')
  const paneHost = h('div')
  const outlineEl = h('div.fo-tree', { role: 'tree', tabindex: '0', 'aria-label': 'call tree' })
  const diagramEl = h('div.fd-host', { hidden: true })
  const previewEl = h('div.fp-pane')
  const leftPane = h('div.fp-left', [outlineEl, diagramEl])

  // ------------------------------------------------------------------ entry

  const input = h('input', {
    type: 'text',
    class: 'flow-input',
    placeholder: 'a symbol (runReview) or file:line (cmd/codemap/query.go:690)',
    'aria-label': 'entry symbol or file:line',
    value: initial.at || initial.symbol || '',
    onkeydown: (e) => {
      if (e.key === 'Enter') submit()
    },
  })
  const depthSel = h('select', { 'aria-label': 'depth', onchange: (e) => (depth = Number(e.target.value)) }, [2, 3, 4, 5, 6].map((d) => h('option', { value: String(d), selected: d === depth }, `depth ${d}`)))
  const nodesInput = h('input', {
    type: 'number',
    class: 'flow-nodes',
    min: '10',
    max: '1000',
    value: String(maxNodes),
    'aria-label': 'max nodes',
    title: 'maximum steps to trace',
    onchange: (e) => (maxNodes = Math.max(10, Math.min(1000, Number(e.target.value) || 150))),
  })

  function submit() {
    const v = input.value.trim()
    if (!v) {
      toast('Type a symbol or a file:line to trace.', { tone: 'warn' })
      return
    }
    maxNodes = Math.max(10, Math.min(1000, Number(nodesInput.value) || 150))
    if (/^[^\s:]+:\d+$/.test(v)) start({ at: v, label: v })
    else start({ symbol: v, label: v })
  }

  function drawEntry() {
    const recents = recentFlows()
    mount(entryHost, h('div.flow-entry', [
      h('div.fe-row', [
        h('span.fe-glyph', '⇢'),
        input,
        depthSel,
        h('label.fe-nodes', [h('span', 'max steps'), nodesInput]),
        h('button.btn.primary', { type: 'button', onclick: submit }, 'Trace flow'),
      ]),
      recents.length
        ? h('div.fe-recent', [
            h('span.small.dim', 'recent'),
            recents.map((r) => h('button.chip', { type: 'button', title: r.at || r.symbol || r.label, onclick: () => start({ ...r }) }, clip(r.label, 34))),
          ])
        : null,
    ]))
  }

  // ---------------------------------------------------------------- running

  function parse() {
    byId.clear()
    parentOf.clear()
    order = []
    const walk = (n, parent) => {
      byId.set(n.id, n)
      if (parent) parentOf.set(n.id, parent.id)
      order.push(n.id)
      for (const c of n.children || []) walk(c, n)
    }
    if (json?.root) walk(json.root, null)
  }

  async function start(e) {
    entry = { ...e }
    input.value = e.at || e.symbol || e.fqn || e.label || ''
    if (e.depth) depth = e.depth
    depthSel.value = String(Math.min(6, Math.max(2, depth)))
    const token = ++runToken
    collapsed.clear()
    autoFolded = false
    selectedId = null
    highlightSub = ''
    mount(headHost, spinner(`following ${e.symbol || e.at || e.label}…`))
    clear(paneHost)
    const res = await runFlow({ selector: e.selector, at: e.at, symbol: e.symbol, depth, maxNodes })
    if (token !== runToken) return
    if (!res?.ok || !res.json) {
      failed = res
      json = null
    } else {
      failed = null
      json = res.json
      if (json.found !== false && json.root) {
        const lbl = e.label || json.root.fqn || json.root.symbol
        rememberFlow({ label: lbl, symbol: e.symbol || json.root.fqn, at: e.at || '', selector: e.selector || json.root.selector, description: e.description, depth }, state.project)
      }
    }
    parse()
    drawAll()
    if (json?.root) select(json.root.id, { scroll: false })
  }

  // ------------------------------------------------------------------ header

  function ambiguousText(n) {
    const exact = json?.call_graph === 'resolved'
    return `${fmt.num(n)} call${n === 1 ? '' : 's'} could not be pinned to one definition${exact ? '' : ' (name-based graph)'} — run \`codemap index --precise\` for exact edges.`
  }

  function drawHead() {
    if (!json && !failed) {
      clear(headHost)
      return
    }
    if (failed) {
      const notIndexed = failed.code === 'not_indexed' || failed.code === 'index_missing'
      mount(
        headHost,
        notIndexed
          ? callout('warn', 'This project is not indexed yet', 'Flow follows calls in the code graph. Index once and it works.', [h('button.btn.primary', { type: 'button', onclick: () => ctx.openFeature('index') }, 'Index now')])
          : errorBox(failed),
      )
      return
    }
    if (json.found === false || !json.root) {
      mount(headHost, [
        callout('warn', 'No definition found', `Nothing in the index matches “${entry.symbol || entry.at || entry.label || ''}”. Try the exact file:line, or look for the name first.`, [
          h('button.btn', { type: 'button', onclick: () => ctx.go('search', { query: String(entry.symbol || entry.label || '').split('.').pop(), engine: 'find' }) }, 'Search for it'),
        ]),
        ...reportCallouts(json),
      ])
      return
    }
    const root = json.root
    const feat = entry.feature
    const emitted = json.steps_emitted ?? order.length
    const total = json.steps_total ?? emitted
    const out = []
    out.push(
      h('section.card.flow-card', [
        h('div.fc-grid', [
          h('div.fh-main', [
            feat
              ? h('div.fh-feature', [h('span.fh-kicker', feat.surface || 'feature'), h('span.fh-label', featureLabel(feat)), feat.description ? h('span.fh-desc', feat.description) : null])
              : entry.description
                ? h('div.fh-feature', [h('span.fh-desc', entry.description)])
                : null,
            h('div.fh-title', [kindBadge(root.kind || 'function'), h('span.fh-name', root.fqn || root.symbol)]),
            root.doc ? h('div.fh-doc', firstLine(root.doc)) : null,
            h('div.row.gap2', { style: 'margin-top:8px' }, [
              root.file ? h('button.btn.sm.ghost.mono', { type: 'button', title: 'open in the source browser', onclick: () => ctx.openFile(root.file, root.start_line) }, `${root.file}:${root.start_line}`) : null,
              callGraphBadge(json.call_graph, json.resolution),
              json.stale ? staleBadge(true) : null,
              json.truncated ? badge(`${fmt.num(emitted)} of ${fmt.num(total)} steps`, 'warn', 'The trace hit the max-steps cap. Raise “max steps” or lower the depth.') : badge(`${fmt.num(emitted)} steps`, 'plain'),
              json.depth_truncated ? badge(`depth ${json.max_depth ?? depth} limit`, 'plain', 'Some callees below the deepest level are not shown.') : null,
              json.files?.length ? badge(`${json.files.length} files`, 'plain') : null,
            ].filter(Boolean)),
          ]),
        ]),
        json.subsystems?.length
          ? h('div.fh-route', { title: 'Subsystems in order of first appearance, with the number of steps in each. Click one to light it up.' }, [
              h('span.fh-route-label', 'Route'),
              routeStrip(json.subsystems, { onPick: (name) => { highlightSub = highlightSub === name ? '' : name; applyHighlight() }, active: highlightSub }),
            ])
          : null,
        json.ambiguous_calls
          ? h('div.fh-note', [h('span.fh-note-ico', 'ℹ'), h('span', ambiguousText(json.ambiguous_calls))])
          : null,
        json.notes?.length
          ? h('details.fh-notes', [h('summary', `${json.notes.length} note${json.notes.length === 1 ? '' : 's'} about this trace`), h('div', json.notes.join('\n'))])
          : null,
      ]),
    )
    mount(headHost, h('div.stack', { style: 'gap:10px' }, [...out, ...reportCallouts(json, { resolution: false, notes: false })]))
  }

  // -------------------------------------------------------------- the panes

  function drawPanes() {
    if (!json?.root) {
      drawEmpty()
      return
    }
    const toolbar = h('div.fp-toolbar', [
      tabs([{ id: 'outline', label: 'Outline' }, { id: 'diagram', label: 'Diagram' }], mode, (id) => setMode(id)),
      h('div.spacer'),
      h('button.btn.sm', { type: 'button', onclick: () => expandAll(true) }, 'Expand all'),
      h('button.btn.sm', { type: 'button', onclick: () => expandAll(false) }, 'Collapse'),
      h('button.btn.sm', { type: 'button', title: 'Copy a numbered markdown outline for an agent or your notes', onclick: async () => { await copy(flowMarkdown(json, { feature: entry.feature })); toast('copied as a brief — paste it into an agent chat or notes', { tone: 'ok' }) } }, 'Copy as brief'),
      h('button.btn.sm', { type: 'button', onclick: () => { const p = graphPayloadFor({ id: 'flow' }, json); if (p) ctx.onGraph(p); else toast('nothing to visualise', { tone: 'warn' }) } }, '⇶ Graph'),
    ])
    mount(paneHost, [
      toolbar,
      h('div.flow-panes', [leftPane, previewEl]),
      h('div.fp-legend.small.dim', [h('span', '≈ name-based candidate edge'), h('span', '↺ already shown above'), h('span', '+N alt: other definitions share this name'), h('span', '↑ ↓ ← → move · enter opens the source')]),
    ])
    buildOutline()
    applyVisibility()
    leftPane.classList.toggle('diagram', mode === 'diagram')
    outlineEl.hidden = mode !== 'outline'
    diagramEl.hidden = mode !== 'diagram'
    if (mode === 'diagram') drawDiagram(true)
  }

  function drawEmpty() {
    mount(paneHost, h('div.flow-empty', [
      h('div.fe-title', 'Pick something to follow'),
      h('p', 'A flow is the story of one entry point: what it calls, in the order it calls it, through which subsystems. Start from a feature, or type any symbol above.'),
      h('div.fe-suggest', { id: 'flow-suggest' }, spinner('finding good starting points…')),
    ]))
    loadFeatures().then((res) => {
      const host2 = paneHost.querySelector?.('#flow-suggest') || null
      if (!host2) return
      const list = res?.ok ? representativeFeatures(res.json?.features, 6) : []
      mount(host2, list.length
        ? [h('div.small.dim', 'big features worth tracing'), h('div.fe-cards', list.map((f) => featureCard(f, { compact: true, onOpen: () => start({ ...featureFlowTarget(f), label: featureLabel(f), description: f.description, feature: f }) })))]
        : h('div.small.muted', 'No features detected yet — type a symbol above.'))
    })
  }

  let autoFolded = false

  function setMode(id) {
    mode = id
    // a big tree is unreadable as one diagram: open it folded to two levels and
    // let the reader unfold what interests them (the outline shares the folds)
    if (id === 'diagram' && !autoFolded && order.length > 45) {
      autoFolded = true
      collapsed.clear()
      let foldAt = 1
      for (let d = 4; d >= 1; d--) {
        if (order.filter((nid) => byId.get(nid).depth <= d).length <= 45) {
          foldAt = d
          break
        }
      }
      for (const nid of order) {
        const n = byId.get(nid)
        if (n.depth >= foldAt && (n.children || []).length) collapsed.add(nid)
      }
    }
    drawPanes()
  }

  // ---------------------------------------------------------------- outline

  function badgesFor(n) {
    const out = []
    if (n.confidence === 'candidate') out.push(h('span.fo-b.cand', { title: 'candidate edge: matched by name, not proven by the compiler' }, '≈'))
    if (n.alternatives > 0) out.push(h('span.fo-b.alt', { title: `${n.alternatives} other definition${n.alternatives === 1 ? '' : 's'} share this name; the most plausible one was kept` }, `+${n.alternatives} alt`))
    if (n.leaf_reason === 'ambiguous') out.push(h('span.fo-b.amb', { title: 'several definitions match this call and none is clearly right' }, `ambiguous${n.candidates?.length ? ` · ${n.candidates.length}` : ''}`))
    if (n.repeat_of) out.push(h('button.fo-b.rep', { type: 'button', title: `already shown earlier — jump to it`, onclick: (e) => { e.stopPropagation(); select(n.repeat_of) } }, '↺ repeat'))
    if (n.cycle) out.push(h('span.fo-b.cyc', { title: 'this call closes a cycle' }, 'cycle'))
    const more = (n.children_total || 0) - (n.children || []).length
    if (more > 0 && n.leaf_reason !== 'repeat' && n.leaf_reason !== 'ambiguous') {
      out.push(h('span.fo-b.more', { title: n.leaf_reason === 'max_nodes' ? 'trimmed by the max-steps cap' : 'callees below the depth limit are not shown' }, `+${more} more`))
    }
    return out
  }

  function buildOutline() {
    clear(outlineEl)
    rows.clear()
    const frag = document.createDocumentFragment()
    for (const id of order) {
      const n = byId.get(id)
      const kids = (n.children || []).length > 0
      const twisty = h('button.fo-tw', {
        type: 'button',
        tabindex: '-1',
        'aria-label': kids ? 'toggle children' : null,
        onclick: (e) => {
          e.stopPropagation()
          if (kids) toggle(id)
        },
      }, kids ? '▾' : '')
      const row = h('div.fo-row', {
        role: 'treeitem',
        'aria-level': String(n.depth + 1),
        dataset: { id, sub: n.subsystem || '' },
        style: { paddingLeft: `${8 + n.depth * 16}px` },
        onclick: () => select(id, { focus: true }),
        ondblclick: () => openSource(n),
      }, [
        twisty,
        h('span.fo-num', n.depth === 0 ? '●' : String(n.call_order || '')),
        h('i.fo-dot', { style: { background: subsystemColor(n.subsystem) }, title: n.subsystem || 'unresolved' }),
        h('span.fo-name', { class: n.leaf_reason === 'ambiguous' ? 'fo-name amb' : 'fo-name' }, n.symbol || n.fqn || '(unresolved)'),
        n.doc ? h('span.fo-doc', firstLine(n.doc)) : null,
        h('span.fo-badges', badgesFor(n)),
      ])
      rows.set(id, row)
      frag.appendChild(row)
    }
    outlineEl.appendChild(frag)
  }

  function applyVisibility() {
    let skip = null
    for (const id of order) {
      const n = byId.get(id)
      const row = rows.get(id)
      if (!row) continue
      if (skip !== null && n.depth > skip) {
        row.hidden = true
        continue
      }
      skip = null
      row.hidden = false
      const kids = (n.children || []).length > 0
      if (kids) {
        row.setAttribute('aria-expanded', collapsed.has(id) ? 'false' : 'true')
        const tw = row.firstChild
        if (tw) tw.textContent = collapsed.has(id) ? '▸' : '▾'
        if (collapsed.has(id)) skip = n.depth
      }
    }
  }

  function applyHighlight() {
    for (const [id, row] of rows) {
      const n = byId.get(id)
      row.classList.toggle('dim', !!highlightSub && n.subsystem !== highlightSub)
    }
    drawHead()
    if (mode === 'diagram') drawDiagram(false)
  }

  function toggle(id, force) {
    const want = force === undefined ? !collapsed.has(id) : force
    if (want) collapsed.add(id)
    else collapsed.delete(id)
    applyVisibility()
    if (mode === 'diagram') drawDiagram(false)
  }

  function expandAll(expand) {
    collapsed.clear()
    if (!expand) for (const id of order) if ((byId.get(id).children || []).length && byId.get(id).depth >= 1) collapsed.add(id)
    applyVisibility()
    if (mode === 'diagram') drawDiagram(true)
    if (selectedId) select(selectedId)
  }

  function revealPath(id) {
    let p = parentOf.get(id)
    let changed = false
    while (p) {
      if (collapsed.delete(p)) changed = true
      p = parentOf.get(p)
    }
    if (changed) {
      applyVisibility()
      if (mode === 'diagram') drawDiagram(false)
    }
  }

  // Scroll only the outline itself; scrollIntoView would also drag the page.
  function keepInView(row) {
    const top = row.offsetTop
    const bottom = top + row.offsetHeight
    const view = outlineEl.clientHeight
    if (!(view > 0) || Number.isNaN(top)) return
    if (top < outlineEl.scrollTop + 4) outlineEl.scrollTop = Math.max(0, top - 8)
    else if (bottom > outlineEl.scrollTop + view - 4) outlineEl.scrollTop = bottom - view + 8
  }

  function visibleIds() {
    return order.filter((id) => rows.get(id) && !rows.get(id).hidden)
  }

  function select(id, { scroll = true, focus = false } = {}) {
    if (!byId.has(id)) return
    revealPath(id)
    selectedId = id
    for (const [rid, row] of rows) row.classList.toggle('sel', rid === id)
    const row = rows.get(id)
    if (row && scroll) keepInView(row)
    if (focus) outlineEl.focus?.()
    markDiagramSelection(scroll)
    showPreview(id)
  }

  outlineEl.addEventListener('keydown', (e) => {
    if (!selectedId) return
    const vis = visibleIds()
    const i = vis.indexOf(selectedId)
    const n = byId.get(selectedId)
    const kids = (n?.children || []).length > 0
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      if (i < vis.length - 1) select(vis[i + 1])
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      if (i > 0) select(vis[i - 1])
    } else if (e.key === 'ArrowRight') {
      e.preventDefault()
      if (kids && collapsed.has(selectedId)) toggle(selectedId, false)
      else if (kids) select(n.children[0].id)
    } else if (e.key === 'ArrowLeft') {
      e.preventDefault()
      if (kids && !collapsed.has(selectedId)) toggle(selectedId, true)
      else if (parentOf.has(selectedId)) select(parentOf.get(selectedId))
    } else if (e.key === 'Home') {
      e.preventDefault()
      select(vis[0])
    } else if (e.key === 'End') {
      e.preventDefault()
      select(vis[vis.length - 1])
    } else if (e.key === 'Enter') {
      e.preventDefault()
      openSource(n)
    }
  })

  function openSource(n) {
    if (n?.file) ctx.openSymbol({ ...n, file: n.file, start_line: n.start_line })
  }

  // ---------------------------------------------------------------- diagram

  let view = { x: 24, y: 24, k: 0.9 }
  let layout = null
  let svgEl = null
  let gView = null
  let panMoved = false

  function computeLayout() {
    const pos = new Map()
    const edges = []
    let leaf = 0
    let placed = 0
    let capped = false
    const place = (n, d, parent) => {
      if (placed >= DIAGRAM_CAP) {
        capped = true
        return null
      }
      placed++
      const shown = collapsed.has(n.id) ? [] : n.children || []
      const ys = []
      for (const c of shown) {
        const cy = place(c, d + 1, n)
        if (cy !== null) ys.push(cy)
      }
      const y = ys.length ? (ys[0] + ys[ys.length - 1]) / 2 : leaf++ * ROW_H
      pos.set(n.id, { x: d * COL_W, y, node: n })
      if (parent) edges.push({ from: parent.id, to: n.id })
      return y
    }
    if (json?.root) place(json.root, 0, null)
    let maxX = 0
    let maxY = 0
    for (const p of pos.values()) {
      maxX = Math.max(maxX, p.x + NODE_W)
      maxY = Math.max(maxY, p.y + NODE_H)
    }
    return { pos, edges, capped, w: maxX, h: maxY }
  }

  const SVG = (tag, props, ...kids) => h(`svg:${tag}`, props, ...kids)

  function applyView() {
    gView?.setAttribute('transform', `translate(${view.x.toFixed(1)} ${view.y.toFixed(1)}) scale(${view.k.toFixed(3)})`)
  }

  function hostSize() {
    const r = diagramEl.getBoundingClientRect()
    return { w: r.width || 600, h: r.height || 480 }
  }

  function fitView() {
    if (!layout) return
    const { w, h: hh } = hostSize()
    const k = Math.max(0.2, Math.min(1.05, (w - 48) / Math.max(1, layout.w), (hh - 48) / Math.max(1, layout.h)))
    view = { k, x: Math.max(16, (w - layout.w * k) / 2), y: Math.max(16, (hh - layout.h * k) / 2) }
    applyView()
  }

  function startView() {
    // readable by default: the root stays in sight at a legible scale and the
    // user pans; "fit" shows everything. A tree that fits is simply centred.
    const { w, h: hh } = hostSize()
    const rootPos = layout?.pos.get(json?.root?.id)
    let k = 0.9
    if (layout && layout.w * k + 40 > w) k = Math.max(0.62, (w - 40) / layout.w)
    if (layout && layout.h * k + 40 > hh && layout.h * 0.7 + 40 <= hh) k = Math.min(k, (hh - 40) / layout.h)
    let y = hh / 2 - ((rootPos?.y || 0) + NODE_H / 2) * k
    if (layout && layout.h * k + 40 < hh) y = (hh - layout.h * k) / 2
    view = { k, x: 20, y }
    applyView()
  }

  function drawDiagram(reset) {
    if (!json?.root) return
    layout = computeLayout()
    const onPath = new Set()
    for (let p = selectedId; p; p = parentOf.get(p)) onPath.add(p)

    const edgeEls = layout.edges.map((e) => {
      const a = layout.pos.get(e.from)
      const b = layout.pos.get(e.to)
      const x1 = a.x + NODE_W
      const y1 = a.y + NODE_H / 2
      const x2 = b.x
      const y2 = b.y + NODE_H / 2
      const dx = (x2 - x1) / 2
      const cand = b.node.confidence === 'candidate'
      return SVG('path', {
        class: `fd-edge${cand ? ' cand' : ''}${onPath.has(e.to) ? ' on' : ''}`,
        d: `M${x1} ${y1} C${x1 + dx} ${y1} ${x2 - dx} ${y2} ${x2} ${y2}`,
        dataset: { to: e.to },
      })
    })

    const nodeEls = [...layout.pos.values()].map(({ x, y, node: n }) => {
      const hiddenKids = collapsed.has(n.id) ? countDesc(n) : 0
      const dim = highlightSub && n.subsystem !== highlightSub
      return SVG('g', {
        class: `fd-node${n.id === selectedId ? ' sel' : ''}${n.confidence === 'candidate' ? ' cand' : ''}${n.leaf_reason === 'ambiguous' ? ' amb' : ''}${dim ? ' dim' : ''}`,
        transform: `translate(${x} ${y})`,
        dataset: { id: n.id },
        onclick: (e) => {
          e.stopPropagation()
          if (!panMoved) select(n.id, { scroll: false })
        },
        ondblclick: (e) => {
          e.stopPropagation()
          if ((n.children || []).length) toggle(n.id)
        },
      }, [
        SVG('title', null, `${n.fqn || n.symbol}${n.file ? `\n${n.file}:${n.start_line}` : ''}${n.doc ? `\n${firstLine(n.doc)}` : ''}`),
        SVG('rect', { class: 'fd-box', width: String(NODE_W), height: String(NODE_H), rx: '8' }),
        SVG('rect', { class: 'fd-stripe', width: '5', height: String(NODE_H - 10), x: '0', y: '5', rx: '2.5', style: { fill: subsystemColor(n.subsystem) } }),
        SVG('text', { class: 'fd-name', x: '14', y: '15' }, clip(n.symbol || n.fqn || '(unresolved)', 25)),
        SVG('text', { class: 'fd-sub', x: '14', y: '27' }, clip(n.subsystem || (n.leaf_reason === 'ambiguous' ? `ambiguous · ${n.candidates?.length || '?'} candidates` : ''), 30)),
        hiddenKids ? SVG('g', { transform: `translate(${NODE_W - 2} ${NODE_H / 2})` }, [SVG('circle', { class: 'fd-more', r: '10' }), SVG('text', { class: 'fd-more-t', y: '3.5' }, `+${hiddenKids > 99 ? '99' : hiddenKids}`)]) : null,
        n.repeat_of ? SVG('text', { class: 'fd-rep', x: String(NODE_W - 14), y: '14' }, '↺') : null,
      ])
    })

    gView = SVG('g', { class: 'fd-view' }, [SVG('g', { class: 'fd-edges' }, edgeEls), SVG('g', { class: 'fd-nodes' }, nodeEls)])
    svgEl = SVG('svg', { class: 'fd-svg', width: '100%', height: '100%', role: 'img', 'aria-label': 'call flow diagram' }, [gView])

    const controls = h('div.fd-controls', [
      h('button.icon-btn', { type: 'button', title: 'zoom in', onclick: () => zoomBy(1.25) }, '＋'),
      h('button.icon-btn', { type: 'button', title: 'zoom out', onclick: () => zoomBy(0.8) }, '－'),
      h('button.icon-btn', { type: 'button', title: 'fit everything', onclick: fitView }, '⤢'),
    ])
    const note = layout.capped ? h('div.fd-note', `Showing the first ${DIAGRAM_CAP} steps — collapse branches in the outline to see the rest.`) : h('div.fd-note', 'drag to pan · scroll to zoom · double-click a node to fold it')
    const prev = { ...view }
    mount(diagramEl, [svgEl, controls, note])
    wirePanZoom()
    if (reset) startView()
    else {
      view = prev
      applyView()
    }
  }

  function countDesc(n) {
    let c = 0
    const walk = (x) => {
      for (const k of x.children || []) {
        c++
        walk(k)
      }
    }
    walk(n)
    return c
  }

  function zoomBy(f, cx, cy) {
    const { w, h: hh } = hostSize()
    const mx = cx ?? w / 2
    const my = cy ?? hh / 2
    const k = Math.max(0.15, Math.min(2.6, view.k * f))
    view.x = mx - (mx - view.x) * (k / view.k)
    view.y = my - (my - view.y) * (k / view.k)
    view.k = k
    applyView()
  }

  function wirePanZoom() {
    let drag = null
    svgEl.addEventListener('wheel', (e) => {
      e.preventDefault()
      const r = diagramEl.getBoundingClientRect()
      zoomBy(Math.exp(-e.deltaY * 0.0016), e.clientX - r.left, e.clientY - r.top)
    }, { passive: false })
    svgEl.addEventListener('mousedown', (e) => {
      drag = { x: e.clientX, y: e.clientY, vx: view.x, vy: view.y }
      panMoved = false
      svgEl.classList.add('panning')
    })
    const move = (e) => {
      if (!drag) return
      const dx = e.clientX - drag.x
      const dy = e.clientY - drag.y
      if (Math.abs(dx) + Math.abs(dy) > 4) panMoved = true
      view.x = drag.vx + dx
      view.y = drag.vy + dy
      applyView()
    }
    const up = () => {
      if (!drag) return
      drag = null
      svgEl?.classList.remove('panning')
      setTimeout(() => (panMoved = false), 0)
    }
    window.addEventListener('mousemove', move)
    window.addEventListener('mouseup', up)
    diagramEl.__unwire?.()
    diagramEl.__unwire = () => {
      window.removeEventListener('mousemove', move)
      window.removeEventListener('mouseup', up)
    }
  }

  function markDiagramSelection(center) {
    if (mode !== 'diagram' || !svgEl) return
    // fold state may have changed, and the highlighted path with it
    drawDiagram(false)
    const p = layout?.pos.get(selectedId)
    if (center && p) {
      const { w, h: hh } = hostSize()
      const sx = view.x + p.x * view.k
      const sy = view.y + p.y * view.k
      if (sx < 20 || sx > w - NODE_W * view.k - 20 || sy < 20 || sy > hh - 60) {
        view.x = w / 2 - (p.x + NODE_W / 2) * view.k
        view.y = hh / 2 - (p.y + NODE_H / 2) * view.k
        applyView()
      }
    }
  }

  // ---------------------------------------------------------------- preview

  async function showPreview(id) {
    const n = byId.get(id)
    const token = ++previewToken
    if (!n) {
      mount(previewEl, h('div.small.muted', { style: 'padding:16px' }, 'Select a step to read its code.'))
      return
    }
    const head = previewHead(n)
    if (n.leaf_reason === 'ambiguous' && !n.file) {
      mount(previewEl, [head, ambiguousList(n)])
      return
    }
    if (!n.file) {
      mount(previewEl, [head, h('div.small.muted', { style: 'padding:14px' }, 'This call could not be resolved to a definition in the index (a library or generated code, perhaps).')])
      return
    }
    const key = `${n.file}:${n.start_line}`
    const body = h('div.fp-code', spinner('reading the source…'))
    mount(previewEl, [head, body])
    let cached = sourceCache.get(key)
    if (!cached) {
      const res = await runArgs(['source', '--at', key], { featureId: '__flow_source', cwd: state.project, quiet: true })
      cached = res?.ok ? res.json?.matches?.[0] || null : { error: res?.error || 'could not read the source' }
      if (res?.ok) sourceCache.set(key, cached)
    }
    if (token !== previewToken) return
    if (!cached || cached.error) {
      mount(body, callout('warn', 'No source body', cached?.error || 'The index has no body for this definition.'))
      return
    }
    const text = cached.source_omitted || !cached.source ? cached.signature || n.signature || '' : cached.source
    mount(body, codeBlock({ text, lang: langFromPath(n.file), startLine: cached.start_line || n.start_line || 1, maxLines: 500 }))
  }

  function previewHead(n) {
    const at = n.file ? `${n.file}:${n.start_line}` : ''
    const notes = []
    if (n.confidence === 'candidate') notes.push(badge('candidate edge', 'warn', 'Matched by name; run codemap index --precise for exact edges.'))
    if (n.alternatives) notes.push(badge(`+${n.alternatives} same-name definitions`, 'plain'))
    if (n.repeat_of) notes.push(h('button.btn.sm.ghost', { type: 'button', onclick: () => select(n.repeat_of) }, '↺ shown earlier — jump to it'))
    if (n.cycle) notes.push(badge('cycle', 'danger'))
    return h('div.fp-head', [
      h('div.fp-title', [n.kind ? kindBadge(n.kind) : null, h('span.fp-name', n.fqn || n.symbol || '(unresolved)')]),
      h('div.row.gap2', { style: 'margin-top:4px' }, [
        n.subsystem ? h('span.fp-sub', [h('i.fo-dot', { style: { background: subsystemColor(n.subsystem) } }), n.subsystem]) : null,
        at ? h('span.fp-at', at) : null,
        notes,
      ]),
      n.doc ? h('div.fp-doc', n.doc) : null,
      at
        ? h('div.btn-row', { style: 'margin-top:10px' }, [
            h('button.btn.sm.primary', { type: 'button', onclick: () => openSource(n) }, 'Open in source browser'),
            h('button.btn.sm', { type: 'button', title: 'Trace from this step instead', onclick: () => start({ selector: n.selector, symbol: n.fqn, label: n.symbol, at: at }) }, 'Flow from here'),
            h('button.btn.sm', { type: 'button', onclick: () => ctx.openFeature('context', { at: [at] }) }, 'Context'),
            h('button.btn.sm', { type: 'button', onclick: () => ctx.openFeature('impact', { at: [at] }) }, 'Impact'),
          ])
        : null,
    ])
  }

  function ambiguousList(n) {
    const c = n.candidates || []
    return h('div.fp-cands', [
      callout('info', 'Several definitions match this call', `${n.symbol} could be any of the ${c.length || n.alternatives || 'several'} definitions below. codemap did not guess; pick one to trace it, or index with --precise.`),
      c.map((cd) =>
        h('div.cand-row', [
          h('div.cand-main', [h('div.cand-sig', cd.signature || cd.selector?.fqn || n.symbol), h('div.cand-at', `${cd.file}:${cd.start_line}`)]),
          h('div.btn-row', [
            h('button.btn.sm', { type: 'button', onclick: () => ctx.openFile(cd.file, cd.start_line) }, 'source'),
            h('button.btn.sm.primary', { type: 'button', onclick: () => start({ selector: cd.selector, symbol: cd.selector?.fqn || n.symbol, label: n.symbol }) }, 'flow from here'),
          ]),
        ]),
      ),
    ])
  }

  // ------------------------------------------------------------------- misc

  function drawAll() {
    drawEntry()
    drawHead()
    drawPanes()
  }

  mount(host, h('div.stack', { style: 'gap:12px' }, [
    h('div.flow-top', [h('h1', { title: 'Follow one entry point end to end: what it calls, in order, through which parts of the codebase.' }, 'Flow'), entryHost]),
    headHost,
    paneHost,
  ]))
  drawEntry()
  drawPanes()
  if (initial.selector || initial.at || initial.symbol) start(initial)

  return {
    node: host,
    destroy() {
      diagramEl.__unwire?.()
    },
    navigate(opts = {}) {
      if (opts.selector || opts.at || opts.symbol) start(opts)
    },
    reload() {
      if (entry.selector || entry.at || entry.symbol) start(entry)
    },
    start,
    select,
    setMode,
    get mode() {
      return mode
    },
  }
}
