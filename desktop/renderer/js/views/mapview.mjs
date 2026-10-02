/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Architecture map: `codemap map` drawn as a subsystem graph (bridges become
// directed edges weighted by edge count) with the full bounded report below.

import { h, mount, clear } from '../dom.mjs'
import { GraphCanvas } from '../graph.mjs'
import { runArgs } from '../runner.mjs'
import { badge, callout, card, emptyState, spinner, toast, fmt } from '../components.mjs'
import { renderReport } from '../report.mjs'
import { state } from '../state.mjs'

// Subsystems that hold specs, docs, CI config, benchmarks or fixtures rather
// than product code: hidden by default so the real architecture is legible.
const NON_SOURCE = new Set(['specs', 'spec', 'docs', 'doc', '.github', '.gitlab', '.circleci', '.vscode', 'bench', 'benchmarks', 'benchmark', 'examples', 'example', 'test', 'tests', 'testdata', 'fixtures', 'e2e', 'scripts', 'schemas', 'assets', 'build', 'dist', 'node_modules', 'vendor'])

export function isNonSourceSubsystem(name) {
  return String(name || '')
    .split('/')
    .some((part) => NON_SOURCE.has(part.toLowerCase()))
}

const MIN_EDGE_CHOICES = [['auto', 'heaviest ~40 bridges'], ['1', 'all bridges'], ['5', '≥ 5 edges'], ['10', '≥ 10 edges'], ['25', '≥ 25 edges'], ['50', '≥ 50 edges'], ['100', '≥ 100 edges'], ['250', '≥ 250 edges']]

// "auto" keeps roughly the 40 heaviest bridges: enough to show the shape, few
// enough that the picture is not a hairball.
export function autoMinEdges(bridges, keep = 40) {
  const counts = (bridges || []).map((b) => Number(b.count) || 0).sort((a, b) => b - a)
  if (counts.length <= keep) return 1
  return Math.max(1, counts[keep - 1])
}

export function mapView(ctx) {
  const host = h('div.view')
  const out = h('div.stack')
  const hud = h('div.graph-hud')
  const detail = h('div')
  const busy = h('div', { style: 'position:absolute;inset:0;display:grid;place-items:center;background:color-mix(in srgb, var(--bg) 60%, transparent);z-index:3' }, spinner('building the architecture overview…'))
  busy.hidden = true
  const stage = h('div.map-canvas', { style: 'position:relative;min-height:440px' }, [hud, detail, busy])
  let canvas = null
  let limits = { subsystems: 40, bridges: 120, hubs: 20, entrypoints: 12 }
  let last = null
  let minEdges = 'auto'
  let showNonSource = false

  function ensureCanvas() {
    if (!canvas) {
      canvas = new GraphCanvas(stage, {
        onSelect: (n) => drawSubsystem(n.raw),
        onOpen: (n) => ctx.openFeature('coverage', { prefix: n.label }),
      })
    }
    return canvas
  }

  function drawSubsystem(s) {
    if (!s) return
    mount(
      detail,
      h('div.graph-node-detail', [
        h('strong', { style: 'font-size:12px;word-break:break-all' }, s.name),
        h('div.small.muted', { style: 'margin:4px 0 8px' }, `${fmt.num(s.files)} files · ${fmt.num(s.symbols)} symbols`),
        h('div.row.gap2', [
          badge(`${fmt.num(s.internal_edges)} internal`, 'plain'),
          badge(`${fmt.num(s.inbound_edges)} inbound`, 'info'),
          badge(`${fmt.num(s.outbound_edges)} outbound`, 'warn'),
        ]),
        s.languages?.length ? h('div.pill-list', { style: 'margin-top:8px' }, s.languages.slice(0, 8).map((l) => badge(typeof l === 'string' ? l : l.language || JSON.stringify(l), 'plain'))) : null,
        h('div.btn-row', { style: 'margin-top:10px' }, [
          h('button.btn.sm.primary', { type: 'button', onclick: () => ctx.openFeature('coverage', { prefix: s.name }) }, 'Coverage here'),
          h('button.btn.sm', { type: 'button', onclick: () => ctx.openFeature('read-order', { query: s.name }) }, 'Read order'),
          h('button.btn.sm', { type: 'button', onclick: () => ctx.go('source', { filter: s.name }) }, 'Browse files'),
        ]),
      ]),
    )
  }

  function toGraph(json) {
    const all = json.subsystems || []
    const kept = all.filter((s) => showNonSource || !isNonSourceSubsystem(s.name))
    const hiddenSubsystems = all.length - kept.length
    const nodes = kept.map((s, i) => ({
      id: s.name,
      label: s.name.split('/').slice(-2).join('/') || s.name,
      fqn: s.name,
      kind: 'file',
      file: s.name,
      line: 0,
      role: i === 0 ? 'focus' : 'related',
      // node size follows how much code the subsystem holds
      in_degree: s.symbols || 0,
      raw: s,
    }))
    const ids = new Set(nodes.map((n) => n.id))
    const between = (json.bridges || []).filter((b) => ids.has(b.from) && ids.has(b.to) && b.from !== b.to)
    const threshold = minEdges === 'auto' ? autoMinEdges(between) : Number(minEdges) || 1
    const shown = between.filter((b) => (Number(b.count) || 0) >= threshold)
    const edges = shown.map((b) => ({ source: b.from, target: b.to, type: b.edge_type || 'calls', confidence: b.provenance === 'precise' ? 'confirmed' : 'candidate', weight: b.count }))
    return {
      nodes,
      edges,
      hiddenSubsystems,
      hiddenBridges: between.length - shown.length,
      threshold,
      title: json.project || 'architecture',
      subtitle: `${nodes.length} subsystems · ${edges.length} bridges${between.length - shown.length ? ` (${between.length - shown.length} below ${threshold} edges hidden)` : ''} · strategy ${json.strategy || '?'}`,
    }
  }

  async function load(refetch = true) {
    const args = ['map', '--top-subsystems', String(limits.subsystems), '--top-bridges', String(limits.bridges), '--top-hubs', String(limits.hubs), '--top-entrypoints', String(limits.entrypoints)]
    let res = last
    if (refetch || !last) {
      busy.hidden = false
      res = await runArgs(args, { featureId: 'map' })
      busy.hidden = true
    }
    last = res
    const nodes = []
    nodes.push(
      h('div.view-head', [
        h('div.vh-main', [
          h('h1', ['Architecture map', res?.json ? badge(`${res.json.subsystems_total ?? (res.json.subsystems || []).length} subsystems`, 'accent') : null, res?.json?.truncated ? badge('bounded', 'warn') : null]),
          h('div.vh-sub', 'A deterministic, source-path-shaped overview: subsystems, the directed bridges between them, the hubs that hold the graph together, and the likely entrypoints. Nothing here is inferred from compiler facts.'),
          h('div.cmdline', [h('span.dim', '$'), h('code', `codemap ${args.join(' ')} --json`)]),
        ]),
        h('div.vh-actions', [
          h('select', { style: 'width:auto', onchange: (e) => { limits.subsystems = Number(e.target.value); load(true) } }, [20, 40, 80, 150, 300].map((n) => h('option', { value: String(n), selected: limits.subsystems === n }, `${n} subsystems`))),
          h('select', { style: 'width:auto', onchange: (e) => { limits.bridges = Number(e.target.value); load(true) } }, [60, 120, 300, 600, 1000].map((n) => h('option', { value: String(n), selected: limits.bridges === n }, `${n} bridges`))),
          h('select', { style: 'width:auto', title: 'hide bridges lighter than this', 'aria-label': 'minimum edges per bridge', onchange: (e) => { minEdges = e.target.value; load(false) } }, MIN_EDGE_CHOICES.map(([v, t]) => h('option', { value: v, selected: minEdges === v }, t))),
          h('label.check', { title: 'specs, docs, CI config, benchmarks and fixtures' }, [h('input', { type: 'checkbox', checked: showNonSource, onchange: (e) => { showNonSource = e.target.checked; load(false) } }), h('span', 'show non-source')]),
          h('button.btn.primary', { type: 'button', onclick: () => load(true) }, '↻ Refresh'),
          h('button.btn', { type: 'button', onclick: () => ctx.go('graph') }, '⇄ Graph explorer'),
        ]),
      ]),
    )

    if (!res?.ok || !res.json) {
      nodes.push(callout('danger', 'map failed', res?.error || 'no data'))
      mount(out, nodes)
      return
    }

    const json = res.json
    const g = toGraph(json)
    nodes.push(stage)
    if (json.resolution) nodes.push(callout('warn', 'resolution', json.resolution))
    if (json.stale) nodes.push(callout('warn', 'stale index', 'The overview reflects the last index, not the current working tree.'))
    nodes.push(renderReport({ id: 'map', title: 'Architecture map', render: 'map', mcp: 'codemap_map' }, res, { ...ctx, onMapView: () => toast('already on the map view') }))
    // Mount first: the canvas needs its host laid out before it can frame the
    // layout, otherwise it sizes itself against a 0×0 rect.
    mount(out, nodes)
    if (g.nodes.length > 1) {
      const c = ensureCanvas()
      c.setData(g)
      c.fit()
      c.reheat(0.4)
      mount(hud, [h('div.gh', g.title), h('div.gh', g.subtitle), g.hiddenSubsystems ? h('div.gh', `${g.hiddenSubsystems} non-source subsystem(s) hidden — tick “show non-source” to include them`) : null, h('div.gh', 'node size = symbols · click a subsystem · double-click drills into its coverage')])
    } else {
      canvas?.setData({ nodes: [], edges: [] })
      mount(hud, [h('div.gh', 'not enough subsystems to draw')])
    }
  }

  mount(host, out)
  load(true)
  return {
    node: host,
    destroy: () => canvas?.destroy(),
    reload: () => load(true),
  }
}
