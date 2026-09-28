/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Force-directed call-graph canvas. Dependency-free: Verlet-ish springs +
// repulsion, drawn on a 2D canvas with pan/zoom, node dragging, hover and
// click-through into the source browser.

const ROLE_COLOR = {
  focus: 'var(--accent)',
  caller: 'var(--info)',
  callee: 'var(--ok)',
  related: 'var(--text-3)',
  test: 'var(--ok)',
}

const EDGE_COLOR = {
  calls: 'rgba(102,217,239,0.55)',
  references: 'rgba(180,142,240,0.55)',
  blast: 'rgba(240,178,100,0.45)',
  tests: 'rgba(95,211,154,0.5)',
  path: 'rgba(102,217,239,0.8)',
  imports: 'rgba(143,162,182,0.4)',
  seed: 'rgba(180,142,240,0.5)',
  edge: 'rgba(143,162,182,0.4)',
}

function cssVar(name, fallback) {
  const v = getComputedStyle(document.documentElement).getPropertyValue(name).trim()
  return v || fallback
}

export class GraphCanvas {
  constructor(host, { onSelect = null, onOpen = null } = {}) {
    this.host = host
    this.onSelect = onSelect
    this.onOpen = onOpen
    this.canvas = document.createElement('canvas')
    this.canvas.style.width = '100%'
    this.canvas.style.height = '100%'
    host.appendChild(this.canvas)
    this.ctx = this.canvas.getContext('2d')
    this.nodes = []
    this.edges = []
    this.byId = new Map()
    this.view = { x: 0, y: 0, k: 1 }
    this.hover = null
    this.selected = null
    this.dragging = null
    this.panning = false
    this.physics = true
    this.autoFit = true
    this.alpha = 1
    this.raf = 0
    this.lastMouse = { x: 0, y: 0 }
    this.resizeObserver = new ResizeObserver(() => this.resize())
    this.resizeObserver.observe(host)
    this.bind()
    this.resize()
    this.loop()
  }

  destroy() {
    cancelAnimationFrame(this.raf)
    this.resizeObserver.disconnect()
    this.canvas.remove()
  }

  resize() {
    const dpr = Math.min(2, window.devicePixelRatio || 1)
    const rect = this.host.getBoundingClientRect()
    const w = Math.max(80, rect.width)
    const h = Math.max(80, rect.height)
    const grew = w !== this.w || h !== this.h
    this.w = w
    this.h = h
    this.canvas.width = Math.round(this.w * dpr)
    this.canvas.height = Math.round(this.h * dpr)
    this.ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
    // A canvas created before its host was laid out (map view mounts the stage
    // late) must re-frame once the real size arrives.
    if (grew && this.autoFit && this.nodes.length) this.fit()
  }

  setData({ nodes = [], edges = [] }) {
    const w = this.w || 800
    const h = this.h || 600
    const maxIn = Math.max(1, ...nodes.map((n) => Number(n.in_degree) || 0))
    this.nodes = nodes.map((n, i) => {
      const angle = (i / Math.max(1, nodes.length)) * Math.PI * 2
      const rad = Math.min(w, h) * 0.28
      return {
        ...n,
        x: n.x ?? w / 2 + Math.cos(angle) * rad * (0.6 + Math.random() * 0.6),
        y: n.y ?? h / 2 + Math.sin(angle) * rad * (0.6 + Math.random() * 0.6),
        vx: 0,
        vy: 0,
        r: n.role === 'focus' ? 13 : 5 + 6 * Math.min(1, (Number(n.in_degree) || 0) / maxIn),
        color: ROLE_COLOR[n.role] || ROLE_COLOR.related,
      }
    })
    this.byId = new Map(this.nodes.map((n) => [n.id, n]))
    this.edges = edges
      .map((e) => ({ ...e, source: this.byId.get(e.source), target: this.byId.get(e.target) }))
      .filter((e) => e.source && e.target && e.source !== e.target)
    this.alpha = 1
    this.selected = null
    this.hover = null
    // Settle before the first frame: fitting an unsettled layout frames the
    // initial ring (or a stray isolated node) and the real structure ends up
    // drawn as a speck.
    if (this.physics) {
      for (let i = 0; i < 220; i++) this.step()
      this.alpha = 0.22
    }
    this.fit()
  }

  fit() {
    if (!this.nodes.length) return
    const xs = this.nodes.map((n) => n.x)
    const ys = this.nodes.map((n) => n.y)
    const minX = Math.min(...xs)
    const maxX = Math.max(...xs)
    const minY = Math.min(...ys)
    const maxY = Math.max(...ys)
    const pad = 70
    const k = Math.min(2.2, Math.max(0.25, Math.min((this.w - pad * 2) / Math.max(1, maxX - minX), (this.h - pad * 2) / Math.max(1, maxY - minY))))
    this.view.k = k
    this.view.x = this.w / 2 - ((minX + maxX) / 2) * k
    this.view.y = this.h / 2 - ((minY + maxY) / 2) * k
  }

  toWorld(px, py) {
    return { x: (px - this.view.x) / this.view.k, y: (py - this.view.y) / this.view.k }
  }

  nodeAt(px, py) {
    const p = this.toWorld(px, py)
    let best = null
    let bestD = Infinity
    for (const n of this.nodes) {
      const d = Math.hypot(n.x - p.x, n.y - p.y)
      const hit = n.r / this.view.k + 6 / this.view.k
      if (d < Math.max(hit, 8 / this.view.k) && d < bestD) {
        best = n
        bestD = d
      }
    }
    return best
  }

  bind() {
    const c = this.canvas
    c.addEventListener('pointerdown', (e) => {
      c.setPointerCapture(e.pointerId)
      const rect = c.getBoundingClientRect()
      const px = e.clientX - rect.left
      const py = e.clientY - rect.top
      const n = this.nodeAt(px, py)
      if (n) {
        this.dragging = n
        n.fixed = true
        this.selected = n
        this.onSelect?.(n)
      } else {
        this.panning = true
      }
      this.lastMouse = { x: px, y: py }
      c.classList.add('dragging')
    })
    c.addEventListener('pointermove', (e) => {
      const rect = c.getBoundingClientRect()
      const px = e.clientX - rect.left
      const py = e.clientY - rect.top
      if (this.dragging) {
        const p = this.toWorld(px, py)
        this.dragging.x = p.x
        this.dragging.y = p.y
        this.dragging.vx = 0
        this.dragging.vy = 0
        this.alpha = Math.max(this.alpha, 0.35)
      } else if (this.panning) {
        this.view.x += px - this.lastMouse.x
        this.view.y += py - this.lastMouse.y
        this.autoFit = false
      } else {
        const n = this.nodeAt(px, py)
        if (n !== this.hover) {
          this.hover = n
          c.style.cursor = n ? 'pointer' : 'grab'
        }
      }
      this.lastMouse = { x: px, y: py }
    })
    const up = () => {
      if (this.dragging) this.dragging.fixed = false
      this.dragging = null
      this.panning = false
      c.classList.remove('dragging')
    }
    c.addEventListener('pointerup', up)
    c.addEventListener('pointercancel', up)
    c.addEventListener('dblclick', (e) => {
      const rect = c.getBoundingClientRect()
      const n = this.nodeAt(e.clientX - rect.left, e.clientY - rect.top)
      if (n) this.onOpen?.(n)
      else this.fit()
    })
    c.addEventListener('wheel', (e) => {
      e.preventDefault()
      this.autoFit = false
      const rect = c.getBoundingClientRect()
      const px = e.clientX - rect.left
      const py = e.clientY - rect.top
      const factor = Math.exp(-e.deltaY * 0.0015)
      const k = Math.min(4, Math.max(0.15, this.view.k * factor))
      const before = this.toWorld(px, py)
      this.view.k = k
      const after = this.toWorld(px, py)
      this.view.x += (after.x - before.x) * k
      this.view.y += (after.y - before.y) * k
    }, { passive: false })
  }

  step() {
    if (!this.physics || this.alpha < 0.004) return
    const nodes = this.nodes
    const n = nodes.length
    // Bigger graphs need a longer reach and longer springs or everything
    // collapses into one blob under the spring+centering forces.
    const repulse = n > 120 ? 1600 : n > 32 ? 5200 : 2600
    const cutoff = n > 32 ? 260000 : 90000
    for (let i = 0; i < n; i++) {
      const a = nodes[i]
      for (let j = i + 1; j < n; j++) {
        const b = nodes[j]
        let dx = b.x - a.x
        let dy = b.y - a.y
        let d2 = dx * dx + dy * dy
        if (d2 < 1) {
          dx = (Math.random() - 0.5) * 2
          dy = (Math.random() - 0.5) * 2
          d2 = dx * dx + dy * dy
        }
        if (d2 > cutoff) continue
        const f = (repulse / d2) * this.alpha
        const d = Math.sqrt(d2)
        const fx = (dx / d) * f
        const fy = (dy / d) * f
        a.vx -= fx
        a.vy -= fy
        b.vx += fx
        b.vy += fy
      }
    }
    const springLen = n > 32 ? 150 : 92
    for (const e of this.edges) {
      const dx = e.target.x - e.source.x
      const dy = e.target.y - e.source.y
      const d = Math.max(1, Math.hypot(dx, dy))
      const f = ((d - springLen) / d) * 0.055 * this.alpha
      const fx = dx * f
      const fy = dy * f
      e.source.vx += fx
      e.source.vy += fy
      e.target.vx -= fx
      e.target.vy -= fy
    }
    const cx = (this.w / 2 - this.view.x) / this.view.k
    const cy = (this.h / 2 - this.view.y) / this.view.k
    const centerPull = n > 32 ? 0.0009 : 0.0016
    for (const node of nodes) {
      if (node.fixed) continue
      node.vx += (cx - node.x) * centerPull * this.alpha
      node.vy += (cy - node.y) * centerPull * this.alpha
      node.vx *= 0.86
      node.vy *= 0.86
      node.x += node.vx
      node.y += node.vy
    }
    this.alpha *= 0.985
  }

  draw() {
    const ctx = this.ctx
    ctx.clearRect(0, 0, this.w, this.h)
    ctx.save()
    ctx.translate(this.view.x, this.view.y)
    ctx.scale(this.view.k, this.view.k)

    // edges
    ctx.lineWidth = 1.2 / this.view.k
    for (const e of this.edges) {
      const active = this.selected && (e.source === this.selected || e.target === this.selected)
      ctx.strokeStyle = active ? cssVar('--accent', '#66d9ef') : EDGE_COLOR[e.type] || EDGE_COLOR.edge
      ctx.globalAlpha = active ? 0.95 : this.selected ? 0.28 : 0.72
      ctx.beginPath()
      ctx.moveTo(e.source.x, e.source.y)
      ctx.lineTo(e.target.x, e.target.y)
      ctx.stroke()
      // arrow head
      const dx = e.target.x - e.source.x
      const dy = e.target.y - e.source.y
      const d = Math.max(1, Math.hypot(dx, dy))
      const ux = dx / d
      const uy = dy / d
      const tipX = e.target.x - ux * (e.target.r + 2)
      const tipY = e.target.y - uy * (e.target.r + 2)
      const size = 5 / Math.sqrt(this.view.k)
      ctx.beginPath()
      ctx.moveTo(tipX, tipY)
      ctx.lineTo(tipX - ux * size - uy * size * 0.5, tipY - uy * size + ux * size * 0.5)
      ctx.lineTo(tipX - ux * size + uy * size * 0.5, tipY - uy * size - ux * size * 0.5)
      ctx.closePath()
      ctx.fillStyle = ctx.strokeStyle
      ctx.fill()
    }
    ctx.globalAlpha = 1

    // nodes
    const text = cssVar('--text', '#dde5ef')
    // Dense graphs only label what matters until you zoom in — otherwise the
    // star layout turns into a wall of overlapping text.
    const showAllLabels = this.nodes.length <= 24 || this.view.k > 1.15
    for (const node of this.nodes) {
      const dim = this.selected && this.selected !== node && !this.edges.some((e) => (e.source === this.selected && e.target === node) || (e.target === this.selected && e.source === node))
      ctx.globalAlpha = dim ? 0.34 : 1
      const isHover = node === this.hover || node === this.selected
      ctx.beginPath()
      ctx.arc(node.x, node.y, node.r, 0, Math.PI * 2)
      ctx.fillStyle = node.color || cssVar('--text-3', '#7d8a9c')
      ctx.fill()
      if (node.role === 'focus') {
        ctx.lineWidth = 2.4 / this.view.k
        ctx.strokeStyle = cssVar('--bg', '#0a0c0f')
        ctx.stroke()
      }
      if (isHover) {
        ctx.beginPath()
        ctx.arc(node.x, node.y, node.r + 3.5 / this.view.k, 0, Math.PI * 2)
        ctx.lineWidth = 1.4 / this.view.k
        ctx.strokeStyle = cssVar('--accent', '#66d9ef')
        ctx.stroke()
      }
      if (showAllLabels || isHover || node.role === 'focus') {
        ctx.font = `${isHover ? 600 : 400} ${11 / this.view.k}px 'IBM Plex Mono', monospace`
        ctx.fillStyle = text
        ctx.textAlign = 'center'
        ctx.textBaseline = 'top'
        const label = node.label.length > 34 ? `${node.label.slice(0, 33)}…` : node.label
        ctx.fillText(label, node.x, node.y + node.r + 3 / this.view.k)
      }
    }
    ctx.globalAlpha = 1
    ctx.restore()
  }

  loop() {
    this.step()
    this.draw()
    this.raf = requestAnimationFrame(() => this.loop())
  }

  reheat(a = 0.8) {
    this.alpha = Math.max(this.alpha, a)
  }

  setPhysics(on) {
    this.physics = !!on
    if (on) this.reheat(0.6)
  }

  zoomBy(f) {
    this.autoFit = false
    const k = Math.min(4, Math.max(0.15, this.view.k * f))
    const cx = this.w / 2
    const cy = this.h / 2
    const before = this.toWorld(cx, cy)
    this.view.k = k
    const after = this.toWorld(cx, cy)
    this.view.x += (after.x - before.x) * k
    this.view.y += (after.y - before.y) * k
  }
}
