/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// The treemap layout is pure math, so it gets property-style tests: areas
// follow the weights, rectangles never overlap, and together they tile the
// container exactly.

import test from 'node:test'
import assert from 'node:assert/strict'

import { squarify, inset } from '../renderer/js/treemap.mjs'

const EPS = 1e-6
const area = (r) => r.w * r.h

function overlap(a, b) {
  const ox = Math.min(a.x + a.w, b.x + b.w) - Math.max(a.x, b.x)
  const oy = Math.min(a.y + a.h, b.y + b.h) - Math.max(a.y, b.y)
  return ox > EPS && oy > EPS ? ox * oy : 0
}

function seeded(seed) {
  let s = seed
  return () => {
    s = (s * 1664525 + 1013904223) % 4294967296
    return s / 4294967296
  }
}

test('areas are proportional to the weights', () => {
  const rect = { x: 10, y: 20, w: 600, h: 400 }
  const items = [{ value: 6 }, { value: 6 }, { value: 4 }, { value: 3 }, { value: 2 }, { value: 2 }, { value: 1 }]
  const total = items.reduce((s, i) => s + i.value, 0)
  const rects = squarify(items, rect)
  assert.equal(rects.length, items.length)
  rects.forEach((r, i) => {
    assert.ok(Math.abs(area(r) - (items[i].value / total) * rect.w * rect.h) < 1e-4, `item ${i} area`)
    assert.equal(r.item, items[i])
    assert.equal(r.index, i)
  })
})

test('rectangles stay inside the container, never overlap, and fill it', () => {
  const rnd = seeded(7)
  for (let round = 0; round < 40; round++) {
    const n = 1 + Math.floor(rnd() * 60)
    const items = Array.from({ length: n }, () => ({ value: 1 + Math.floor(rnd() * 500) }))
    const rect = { x: rnd() * 30, y: rnd() * 30, w: 120 + rnd() * 900, h: 80 + rnd() * 700 }
    const rects = squarify(items, rect)
    let sum = 0
    for (const r of rects) {
      assert.ok(r.x >= rect.x - EPS && r.y >= rect.y - EPS, 'starts inside')
      assert.ok(r.x + r.w <= rect.x + rect.w + EPS && r.y + r.h <= rect.y + rect.h + EPS, 'ends inside')
      sum += area(r)
    }
    assert.ok(Math.abs(sum - rect.w * rect.h) < 1e-3, `tiles the rect (round ${round})`)
    for (let i = 0; i < rects.length; i++) for (let j = i + 1; j < rects.length; j++) assert.equal(overlap(rects[i], rects[j]), 0, `no overlap ${i}/${j}`)
  }
})

test('the layout is deterministic and ties keep their input order', () => {
  const items = [{ value: 5 }, { value: 5 }, { value: 5 }, { value: 5 }, { value: 2 }]
  const rect = { x: 0, y: 0, w: 400, h: 300 }
  const a = squarify(items, rect)
  const b = squarify(items, rect)
  assert.deepEqual(a.map(({ x, y, w, h }) => [x, y, w, h]), b.map(({ x, y, w, h }) => [x, y, w, h]))
  // equal weights, so the first of the tied items is laid out before the second
  const first = a[0]
  const second = a[1]
  assert.ok(first.x < second.x || (first.x === second.x && first.y < second.y), 'earlier tie is placed first')
})

test('the largest item is placed first and gets the largest area', () => {
  const rects = squarify([{ value: 1 }, { value: 9 }, { value: 3 }], { x: 0, y: 0, w: 100, h: 100 })
  const [small, big, mid] = rects
  assert.ok(area(big) > area(mid) && area(mid) > area(small))
  assert.equal(big.x, 0)
  assert.equal(big.y, 0)
})

test('rows stay reasonably square', () => {
  const items = Array.from({ length: 24 }, (_, i) => ({ value: 100 - i * 3 }))
  const rects = squarify(items, { x: 0, y: 0, w: 800, h: 500 })
  const worst = Math.max(...rects.map((r) => Math.max(r.w / r.h, r.h / r.w)))
  assert.ok(worst < 6, `worst aspect ratio ${worst.toFixed(2)}`)
})

test('empty, zero and invalid inputs never throw or produce NaN', () => {
  assert.deepEqual(squarify([], { x: 0, y: 0, w: 10, h: 10 }), [])
  const zeros = squarify([{ value: 0 }, { value: -4 }, { value: NaN }], { x: 1, y: 2, w: 10, h: 10 })
  for (const r of zeros) assert.equal(area(r), 0)
  const mixed = squarify([{ value: 0 }, { value: 4 }], { x: 0, y: 0, w: 10, h: 10 })
  assert.equal(area(mixed[0]), 0)
  assert.ok(Math.abs(area(mixed[1]) - 100) < EPS)
  const flat = squarify([{ value: 1 }, { value: 2 }], { x: 0, y: 0, w: 0, h: 50 })
  for (const r of flat) assert.ok(Number.isFinite(r.x) && Number.isFinite(r.w) && Number.isFinite(r.h))
})

test('a single item fills the rectangle and inset keeps sizes non-negative', () => {
  const [only] = squarify([{ value: 3 }], { x: 5, y: 6, w: 70, h: 40 })
  assert.deepEqual([only.x, only.y, only.w, only.h], [5, 6, 70, 40])
  assert.deepEqual(inset({ x: 0, y: 0, w: 100, h: 60 }, 2, 20), { x: 2, y: 20, w: 96, h: 38 })
  const tiny = inset({ x: 0, y: 0, w: 3, h: 3 }, 4, 9)
  assert.ok(tiny.w === 0 && tiny.h === 0)
})
