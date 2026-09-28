/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// GraphCanvas headless: layout converges, hit-testing works, and merging two
// payloads does not duplicate nodes or edges.

import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import path from 'node:path'

import { installDOM } from './dom-stub.mjs'

installDOM()

const { GraphCanvas } = await import('../renderer/js/graph.mjs')
const { mergePayloads } = await import('../renderer/js/views/graphview.mjs')
const { graphPayloadFor } = await import('../renderer/js/report.mjs')
const { feature } = await import('../renderer/js/features.mjs')

function host() {
  const el = document.createElement('div')
  document.body.appendChild(el)
  return el
}

function sample() {
  return {
    nodes: [
      { id: 'a', label: 'a', kind: 'function', file: 'a.go', line: 1, role: 'focus', in_degree: 9 },
      { id: 'b', label: 'b', kind: 'function', file: 'b.go', line: 2, role: 'caller', in_degree: 1 },
      { id: 'c', label: 'c', kind: 'test', file: 'c_test.go', line: 3, role: 'callee', in_degree: 0 },
    ],
    edges: [
      { source: 'b', target: 'a', type: 'calls' },
      { source: 'a', target: 'c', type: 'calls' },
    ],
  }
}

test('the canvas lays out nodes and converges', async () => {
  const c = new GraphCanvas(host(), {})
  c.setData(sample())
  assert.equal(c.nodes.length, 3)
  assert.equal(c.edges.length, 2)
  const before = c.nodes.map((n) => ({ x: n.x, y: n.y }))
  for (let i = 0; i < 120; i++) c.step()
  const moved = c.nodes.some((n, i) => Math.abs(n.x - before[i].x) > 0.5 || Math.abs(n.y - before[i].y) > 0.5)
  assert.ok(moved, 'the force layout should move nodes')
  for (const n of c.nodes) {
    assert.ok(Number.isFinite(n.x) && Number.isFinite(n.y), `node ${n.id} drifted to ${n.x},${n.y}`)
  }
  assert.ok(c.alpha < 0.4, 'the simulation should cool down')
  c.draw()
  c.destroy()
})

test('edges to unknown nodes are dropped, focus nodes are larger', () => {
  const c = new GraphCanvas(host(), {})
  c.setData({ nodes: sample().nodes, edges: [...sample().edges, { source: 'a', target: 'ghost', type: 'calls' }] })
  assert.equal(c.edges.length, 2)
  const focus = c.nodes.find((n) => n.role === 'focus')
  assert.ok(focus.r > c.nodes.find((n) => n.role === 'callee').r, 'the focus node must be the most prominent')
  c.destroy()
})

test('fit() frames the layout inside the viewport', () => {
  const c = new GraphCanvas(host(), {})
  c.setData(sample())
  for (let i = 0; i < 60; i++) c.step()
  c.fit()
  assert.ok(c.view.k >= 0.25 && c.view.k <= 2.2, `zoom ${c.view.k} out of range`)
  for (const n of c.nodes) {
    const sx = n.x * c.view.k + c.view.x
    const sy = n.y * c.view.k + c.view.y
    assert.ok(sx > -200 && sx < c.w + 200 && sy > -200 && sy < c.h + 200, `node ${n.id} fell outside the view at ${sx},${sy}`)
  }
  c.destroy()
})

test('zoom keeps the point under the cursor anchored', () => {
  const c = new GraphCanvas(host(), {})
  c.setData(sample())
  c.fit()
  const before = c.toWorld(c.w / 2, c.h / 2)
  c.zoomBy(1.5)
  const after = c.toWorld(c.w / 2, c.h / 2)
  assert.ok(Math.abs(before.x - after.x) < 1 && Math.abs(before.y - after.y) < 1, 'centre-anchored zoom drifted')
  c.destroy()
})

test('merging payloads deduplicates nodes and edges', () => {
  const a = sample()
  const b = {
    nodes: [
      { id: 'a', label: 'a', kind: 'function', file: 'a.go', line: 1, role: 'related' },
      { id: 'd', label: 'd', kind: 'function', file: 'd.go', line: 4, role: 'caller' },
    ],
    edges: [
      { source: 'd', target: 'a', type: 'calls' },
      { source: 'b', target: 'a', type: 'calls' },
    ],
    title: 'b',
  }
  const merged = mergePayloads(a, b)
  assert.equal(merged.nodes.length, 4)
  assert.equal(merged.edges.length, 3)
  assert.equal(merged.title, 'b')
  assert.equal(mergePayloads(null, b), b)
  assert.equal(mergePayloads(a, null), a)
})

test('real reports produce drawable graphs with a single focus node', () => {
  for (const id of ['impact', 'callers', 'callees', 'context', 'traverse']) {
    const fixture = loadFixture(`${id.replace(/-/g, '_')}.json`)
    const g = graphPayloadFor(feature(id), fixture)
    assert.ok(g, `${id}: expected a graph payload`)
    assert.ok(g.nodes.length >= 2, `${id}: too few nodes`)
    const focus = g.nodes.filter((n) => n.role === 'focus')
    assert.ok(focus.length <= 1, `${id}: ${focus.length} focus nodes`)
    for (const e of g.edges) {
      assert.ok(g.nodes.some((n) => n.id === e.source), `${id}: edge from an unknown node`)
      assert.ok(g.nodes.some((n) => n.id === e.target), `${id}: edge to an unknown node`)
    }
  }
})

function loadFixture(name) {
  return JSON.parse(readFileSync(path.join(path.dirname(fileURLToPath(import.meta.url)), 'fixtures', name), 'utf8'))
}
