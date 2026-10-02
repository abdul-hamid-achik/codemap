/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Squarified treemap (Bruls, Huijsing & van Wijk, 2000). Pure and DOM-free so
// the layout is unit-testable: give it weighted items and a rectangle, get one
// rectangle back per item, in input order, whose areas are proportional to the
// weights and which together tile the rectangle exactly.

/**
 * @param {{value:number}[]} items weights (non-positive weights get an empty rect)
 * @param {{x:number,y:number,w:number,h:number}} rect the area to tile
 * @returns {{item:object,index:number,x:number,y:number,w:number,h:number}[]} one rect per input item
 */
export function squarify(items, rect) {
  const out = items.map((item, index) => ({ item, index, x: rect.x, y: rect.y, w: 0, h: 0 }))
  const live = out
    .map((r) => ({ r, v: Math.max(0, Number(r.item?.value) || 0) }))
    .filter((e) => e.v > 0)
    // largest first; ties keep their input order so the layout is stable
    .sort((a, b) => b.v - a.v || a.r.index - b.r.index)
  const total = live.reduce((s, e) => s + e.v, 0)
  if (!live.length || !(rect.w > 0) || !(rect.h > 0) || !(total > 0)) return out

  const scale = (rect.w * rect.h) / total
  let x = rect.x
  let y = rect.y
  let w = rect.w
  let h = rect.h
  let row = []
  let rowArea = 0

  const worst = (areas, sum, side) => {
    if (!areas.length || sum <= 0) return Infinity
    const max = Math.max(...areas)
    const min = Math.min(...areas)
    const s2 = side * side
    return Math.max((s2 * max) / (sum * sum), (sum * sum) / (s2 * min))
  }

  const flush = (last) => {
    if (!row.length) return
    if (w >= h) {
      // lay the row out as a column on the left edge
      const colW = last ? w : Math.min(w, rowArea / h)
      let cy = y
      row.forEach((e, i) => {
        const ih = i === row.length - 1 ? y + h - cy : e.area / colW
        Object.assign(e.r, { x, y: cy, w: colW, h: ih })
        cy += ih
      })
      x += colW
      w -= colW
    } else {
      // lay the row out as a strip along the top edge
      const rowH = last ? h : Math.min(h, rowArea / w)
      let cx = x
      row.forEach((e, i) => {
        const iw = i === row.length - 1 ? x + w - cx : e.area / rowH
        Object.assign(e.r, { x: cx, y, w: iw, h: rowH })
        cx += iw
      })
      y += rowH
      h -= rowH
    }
    row = []
    rowArea = 0
  }

  for (const e of live) {
    const entry = { r: e.r, area: e.v * scale }
    const side = Math.min(w, h)
    const current = worst(row.map((x2) => x2.area), rowArea, side)
    const withNext = worst([...row.map((x2) => x2.area), entry.area], rowArea + entry.area, side)
    if (!row.length || withNext <= current) {
      row.push(entry)
      rowArea += entry.area
    } else {
      flush(false)
      row.push(entry)
      rowArea += entry.area
    }
  }
  flush(true)
  return out
}

/** Shrink a rect by a uniform padding (never below zero size). */
export function inset(rect, pad, top = pad) {
  const w = Math.max(0, rect.w - pad * 2)
  const h = Math.max(0, rect.h - pad - top)
  return { x: rect.x + pad, y: rect.y + top, w, h }
}
