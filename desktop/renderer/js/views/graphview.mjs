/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Graph explorer: turn any relation-bearing report into an interactive
// force-directed canvas, and grow it from a node without leaving the view.

import { h, clear, mount } from '../dom.mjs'
import { GraphCanvas } from '../graph.mjs'
import { runArgs } from '../runner.mjs'
import { badge, callout, kindBadge, spinner, toast, fmt, shortPath } from '../components.mjs'
import { graphPayloadFor } from '../report.mjs'
import { state, setGraph } from '../state.mjs'

export function graphView(ctx, initial = {}) {
  const host = h('div.view', { style: 'height:100%' })
  const hud = h('div.graph-hud')
  const detail = h('div')
  const busy = h('div', { style: 'position:absolute;inset:0;display:grid;place-items:center;background:color-mix(in srgb, var(--bg) 62%, transparent);z-index:3' }, spinner('walking the graph…'))
  busy.hidden = true
  const stage = h('div.graph-stage', [
    hud,
    h('div.graph-legend', [
      legend('var(--accent)', 'focus'),
      legend('var(--info)', 'caller / incoming'),
      legend('var(--ok)', 'callee / test'),
      legend('var(--text-3)', 'related'),
      legend('rgba(240,178,100,0.95)', 'blast radius'),
      legend('rgba(180,142,240,0.95)', 'value reference'),
    ]),
    h('div.graph-controls', [
      h('button.icon-btn', { type: 'button', title: 'zoom in', onclick: () => canvas?.zoomBy(1.25) }, '＋'),
      h('button.icon-btn', { type: 'button', title: 'zoom out', onclick: () => canvas?.zoomBy(0.8) }, '－'),
      h('button.icon-btn', { type: 'button', title: 'fit to view', onclick: () => { canvas?.fit() } }, '⤢'),
      h('button.icon-btn', { type: 'button', title: 're-run the layout', onclick: () => canvas?.reheat(1) }, '↻'),
    ]),
    detail,
    busy,
  ])

  let canvas = null
  let mode = initial.mode || 'traverse'
  let values = {
    at: initial.at || '',
    symbol: initial.symbol || '',
    depth: initial.depth ?? 2,
    limit: initial.limit ?? 60,
    direction: initial.direction || 'both',
    edge_types: initial.edge_types || '',
  }
  let lastPayload = null

  function ensureCanvas() {
    if (canvas) return canvas
    canvas = new GraphCanvas(stage, {
      onSelect: (node) => drawDetail(node),
      onOpen: (node) => ctx.openSymbol({ file: node.file, start_line: node.line, symbol: node.label, fqn: node.fqn, kind: node.kind }),
    })
    return canvas
  }

  function drawHud(payload) {
    mount(
      hud,
      payload && payload.nodes.length
        ? [h('div.gh', payload.title || 'graph'), h('div.gh', `${payload.nodes.length} nodes · ${payload.edges.length} edges`), payload.subtitle ? h('div.gh', payload.subtitle) : null]
        : [h('div.gh', 'no graph loaded')],
    )
  }

  function setData(payload, meta) {
    lastPayload = payload
    setGraph(payload || { nodes: [], edges: [] })
    const c = ensureCanvas()
    c.setData(payload || { nodes: [], edges: [] })
    if (payload?.nodes?.length) {
      c.fit()
      c.reheat(1)
    }
    drawHud(payload)
    if (!payload || !payload.nodes.length) {
      mount(
        detail,
        h('div.graph-node-detail', callout('warn', 'Nothing to draw', meta || 'That query returned no relations. For plain TypeScript/JavaScript/Python a name-based index has no call edges — run “Index --precise” first.')),
      )
      return
    }
    mount(
      detail,
      h('div.graph-node-detail', [
        h('div.row.gap2', { style: 'margin-bottom:6px' }, [h('strong', { style: 'font-size:12px' }, payload.title || 'graph'), h('span.small.dim', 'click a node · double-click opens the source')]),
        h('div.small.muted', meta || ''),
      ]),
    )
  }

  function drawDetail(node) {
    const related = (lastPayload?.edges || []).filter((e) => e.source === node.id || e.target === node.id)
    mount(
      detail,
      h('div.graph-node-detail', [
        h('div.row.gap2', [kindBadge(node.kind || 'symbol'), h('strong', { style: 'font-size:12px;word-break:break-all' }, node.fqn || node.label)]),
        h('div.small.mono.dim', { style: 'margin:4px 0 8px;word-break:break-all' }, `${node.file}:${node.line || '?'}`),
        node.in_degree !== undefined ? h('div.small', `fan-in ${fmt.num(node.in_degree)}`) : null,
        node.depth !== undefined ? h('div.small', `depth ${node.depth}`) : null,
        h('div.btn-row', { style: 'margin:8px 0' }, [
          h('button.btn.sm.primary', { type: 'button', onclick: () => ctx.openSymbol({ file: node.file, start_line: node.line, symbol: node.label, fqn: node.fqn, kind: node.kind }) }, 'Open source'),
          h('button.btn.sm', { type: 'button', onclick: () => expand(node) }, '⇢ Expand'),
          h('button.btn.sm', { type: 'button', onclick: () => ctx.openFeature('context', { at: [`${node.file}:${node.line}`] }) }, 'Context'),
          h('button.btn.sm', { type: 'button', onclick: () => ctx.openFeature('impact', { at: [`${node.file}:${node.line}`] }) }, 'Impact'),
        ]),
        related.length
          ? h('div.stack', { style: 'gap:4px' }, [
              h('h4', `${related.length} edge(s)`),
              h(
                'div.symlist',
                { style: 'max-height:190px' },
                related.slice(0, 30).map((e) => {
                  const otherId = e.source === node.id ? e.target : e.source
                  const other = lastPayload.nodes.find((n) => n.id === otherId)
                  if (!other) return null
                  return h('div.symrow', { style: 'grid-template-columns:14px minmax(0,1fr)', onclick: () => { if (canvas) canvas.selected = other; drawDetail(other) } }, [
                    h('span.sk', e.source === node.id ? '→' : '←'),
                    h('div', { style: 'min-width:0' }, [
                      h('div.sn', other.label),
                      h('div.meta', [badge(e.type || 'edge', 'info'), e.confidence ? badge(e.confidence, e.confidence === 'confirmed' ? 'ok' : 'warn') : null, h('span', ` ${shortPath(other.file)}:${other.line || ''}`)]),
                    ]),
                  ])
                }),
              ),
            ])
          : h('div.small.dim', 'no edges on this node'),
      ]),
    )
  }

  async function expand(node) {
    toast(`expanding ${node.label}…`)
    const res = await runArgs(['traverse', '--at', `${node.file}:${node.line}`, '--depth', '1', '--limit', '40', '--direction', 'both'], { featureId: '__graph_expand' })
    if (!res?.ok) return
    const extra = graphPayloadFor({ id: 'traverse' }, res.json)
    if (!extra) {
      toast('nothing new to add', { tone: 'warn' })
      return
    }
    setData(mergePayloads(lastPayload, extra), `merged ${extra.nodes.length} node(s) around ${node.label}`)
  }

  async function build() {
    busy.hidden = false
    try {
      let payload = null
      let meta = ''
      if (mode === 'last') {
        payload = state.graph?.nodes?.length ? state.graph : null
        meta = payload ? 'the last relation-bearing report' : 'no previous report carried relations yet'
      } else if (mode === 'traverse') {
        const at = values.at.trim()
        if (!at) {
          setData(null, 'Set a position first (file:line) — or pick any symbol from a list elsewhere in the app and choose “Visualize”.')
          return
        }
        const args = ['traverse', '--at', at, '--depth', String(values.depth), '--limit', String(values.limit), '--direction', values.direction]
        if (values.edge_types.trim()) args.push('--edge-types', values.edge_types.trim())
        const res = await runArgs(args, { featureId: '__graph_traverse' })
        if (!res?.ok) {
          setData(null, res?.error || 'traverse failed')
          return
        }
        payload = graphPayloadFor({ id: 'traverse' }, res.json)
        meta = `traverse from ${at} · ${res.json?.hops?.length || 0} hop(s) · ${res.json?.truncated ? 'truncated by --limit · ' : ''}call_graph ${res.json?.call_graph || '?'}`
      } else if (mode === 'context') {
        const target = values.symbol.trim()
        if (!target) {
          setData(null, 'Enter a symbol name, or a file:line position.')
          return
        }
        const args = target.includes(':') ? ['context', '--at', target] : ['context', target]
        const res = await runArgs(args, { featureId: '__graph_context' })
        if (!res?.ok) {
          setData(null, res?.error || 'context failed')
          return
        }
        payload = graphPayloadFor({ id: 'context' }, res.json)
        meta = `context for ${target} · call_graph ${res.json?.call_graph || '?'}`
      } else if (mode === 'map') {
        const res = await runArgs(['map', '--top-subsystems', '40', '--top-hubs', '40', '--top-entrypoints', '20'], { featureId: '__graph_map' })
        if (res?.ok) {
          payload = mapPayload(res.json)
          meta = 'architecture map: hubs + entrypoints'
        } else {
          setData(null, res?.error || 'map failed')
          return
        }
      }
      setData(payload, meta)
    } finally {
      busy.hidden = true
    }
  }

  const controlsHost = h('div.row.gap2')
  function drawControls() {
    mount(
      controlsHost,
      h('div.row.gap2', [
        segmented(
          [
            { v: 'traverse', label: 'Traverse' },
            { v: 'context', label: 'Symbol context' },
            { v: 'map', label: 'Architecture' },
            { v: 'last', label: 'Last report' },
          ],
          mode,
          (v) => {
            mode = v
            drawControls()
          },
        ),
        mode === 'context'
          ? h('input', { type: 'text', value: values.symbol, placeholder: 'symbol or file:line', style: 'width:250px', oninput: (e) => (values.symbol = e.target.value) })
          : mode === 'traverse'
            ? h('input', { type: 'text', value: values.at, placeholder: 'internal/app/review.go:123', style: 'width:290px', oninput: (e) => (values.at = e.target.value) })
            : null,
        mode === 'traverse'
          ? h('div.field-row', [
              selectInput(values.direction, ['both', 'outgoing', 'incoming'], (v) => (values.direction = v), 'direction'),
              numberInput(values.depth, 1, 6, (v) => (values.depth = v), 'depth'),
              numberInput(values.limit, 5, 500, (v) => (values.limit = v), 'node limit'),
              h('input', { type: 'text', value: values.edge_types, placeholder: 'edge types (csv)', style: 'width:180px', oninput: (e) => (values.edge_types = e.target.value) }),
            ])
          : null,
        h('button.btn.primary', { type: 'button', onclick: build }, '▷ Build graph'),
        h('button.btn', {
          type: 'button',
          class: state.settings.graphPhysics === false ? 'btn' : 'btn on',
          onclick: (e) => {
            const on = state.settings.graphPhysics === false
            state.settings.graphPhysics = on
            canvas?.setPhysics(on)
            e.target.classList.toggle('on', on)
            window.studio.settings.set({ graphPhysics: on })
          },
        }, 'Physics'),
      ]),
    )
  }
  drawControls()

  mount(
    host,
    h('div.graph-shell', [
      h('div.view-head', { style: 'margin-bottom:0' }, [
        h('div.vh-main', [
          h('h1', 'Graph explorer'),
          h('div.vh-sub', 'Walk typed relations from one exact source definition. Node size is fan-in, colour is role, arrows follow edge direction. Drag to rearrange, scroll to zoom, double-click to open the source.'),
        ]),
        h('div.vh-actions', [h('button.btn', { type: 'button', onclick: () => ctx.go('map') }, '⬡ Architecture map'), h('button.btn', { type: 'button', onclick: () => ctx.go('source') }, '⌸ Source browser')]),
      ]),
      controlsHost,
      stage,
    ]),
  )

  ensureCanvas()
  drawHud(state.graph?.nodes?.length ? state.graph : null)
  if (state.graph?.nodes?.length && mode === 'last') setData(state.graph, 'carried over from the last report')
  else if (initial.autobuild !== false && (values.at || values.symbol || mode === 'map')) build()
  else setData(null, 'Pick a mode and build a graph, or click “Visualize” on any relation-bearing report.')

  return {
    node: host,
    destroy: () => canvas?.destroy(),
    load(payload, meta) {
      mode = 'last'
      drawControls()
      setData(payload, meta || 'from the last report')
    },
    setStart(at) {
      values.at = at
      mode = 'traverse'
      drawControls()
      build()
    },
  }
}

function legend(color, label) {
  return h('span.lg', [h('span.sw', { style: { background: color } }), label])
}

function segmented(options, value, onChange) {
  return h('div.btn-row', options.map((o) => h('button.btn.sm', { type: 'button', class: o.v === value ? 'btn sm on' : 'btn sm', onclick: () => onChange(o.v) }, o.label)))
}

function selectInput(value, options, onChange, title) {
  return h('select', { title, style: 'width:auto', onchange: (e) => onChange(e.target.value) }, options.map((o) => h('option', { value: o, selected: o === value }, o)))
}

function numberInput(value, min, max, onChange, title) {
  return h('input', { type: 'number', value: String(value), min: String(min), max: String(max), title, style: 'width:76px', oninput: (e) => onChange(Number(e.target.value)) })
}

function mapPayload(json) {
  const nodes = []
  const seen = new Set()
  const add = (o, role) => {
    if (!o?.file) return
    const id = `${o.file}:${o.start_line ?? 0}:${o.fqn || o.symbol || ''}`
    if (seen.has(id)) return
    seen.add(id)
    nodes.push({
      id,
      label: o.symbol || o.fqn || o.file.split('/').pop(),
      fqn: o.fqn || o.symbol || '',
      kind: o.kind || 'symbol',
      file: o.file,
      line: o.start_line ?? 0,
      role,
      in_degree: o.in_degree,
    })
  }
  for (const hub of json.hubs || []) add(hub, 'caller')
  for (const ep of json.entrypoints || []) add(ep, 'focus')
  return nodes.length > 1 ? { nodes, edges: [], title: 'architecture map', subtitle: `${nodes.length} hubs + entrypoints` } : null
}

export function mergePayloads(a, b) {
  if (!a || !a.nodes?.length) return b
  if (!b || !b.nodes?.length) return a
  const nodes = new Map(a.nodes.map((n) => [n.id, { ...n }]))
  for (const n of b.nodes) if (!nodes.has(n.id)) nodes.set(n.id, { ...n })
  const edgeKey = (e) => `${e.source}|${e.target}|${e.type}`
  const edges = new Map(a.edges.map((e) => [edgeKey(e), e]))
  for (const e of b.edges) if (!edges.has(edgeKey(e)) && nodes.has(e.source) && nodes.has(e.target)) edges.set(edgeKey(e), e)
  return { nodes: [...nodes.values()], edges: [...edges.values()], title: b.title || a.title, subtitle: `${nodes.size} nodes · ${edges.size} edges (merged)` }
}
