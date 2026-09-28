/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Minimal DOM good enough to run the renderer under `node --test`. It is not a
// browser: it exists so report.mjs / components.mjs can be exercised against
// real codemap fixtures without Electron, which is what catches renderer
// regressions before they reach a window.

class ClassList {
  constructor(node) {
    this.node = node
    this.set = new Set()
  }
  add(...c) {
    for (const x of c) if (x) this.set.add(x)
  }
  remove(...c) {
    for (const x of c) this.set.delete(x)
  }
  contains(c) {
    return this.set.has(c)
  }
  toggle(c, force) {
    const on = force === undefined ? !this.set.has(c) : !!force
    if (on) this.set.add(c)
    else this.set.delete(c)
    return on
  }
  toString() {
    return [...this.set].join(' ')
  }
}

class Node {
  constructor(name) {
    this.nodeName = name
    this.childNodes = []
    this.parentNode = null
    this.attributes = {}
    this.style = {}
    this.dataset = {}
    this.listeners = new Map()
    this._classList = new ClassList(this)
    this._text = null
  }
  get classList() {
    return this._classList
  }
  get className() {
    return this._classList.toString()
  }
  set className(v) {
    this._classList = new ClassList(this)
    for (const c of String(v).split(/\s+/)) if (c) this._classList.add(c)
  }
  get firstChild() {
    return this.childNodes[0] || null
  }
  get children() {
    return this.childNodes.filter((c) => c instanceof Element)
  }
  appendChild(child) {
    if (child instanceof Fragment) {
      for (const c of [...child.childNodes]) this.appendChild(c)
      return child
    }
    child.parentNode = this
    this.childNodes.push(child)
    return child
  }
  removeChild(child) {
    const i = this.childNodes.indexOf(child)
    if (i >= 0) this.childNodes.splice(i, 1)
    child.parentNode = null
    return child
  }
  remove() {
    this.parentNode?.removeChild(this)
  }
  replaceWith(other) {
    if (!this.parentNode) return
    const i = this.parentNode.childNodes.indexOf(this)
    if (i >= 0) this.parentNode.childNodes[i] = other
    other.parentNode = this.parentNode
  }
  setAttribute(k, v) {
    this.attributes[k] = String(v)
    if (k === 'class') this.className = v
    if (k === 'id') this.id = v
    if (k === 'hidden') this.hidden = true
  }
  getAttribute(k) {
    return this.attributes[k] ?? null
  }
  addEventListener(type, fn) {
    if (!this.listeners.has(type)) this.listeners.set(type, new Set())
    this.listeners.get(type).add(fn)
  }
  removeEventListener(type, fn) {
    this.listeners.get(type)?.delete(fn)
  }
  dispatchEvent(evt) {
    const set = this.listeners.get(evt.type)
    if (set) for (const fn of [...set]) fn.call(this, evt)
    return true
  }
  get textContent() {
    if (this._text !== null) return this._text
    return this.childNodes.map((c) => c.textContent).join('')
  }
  set textContent(v) {
    this.childNodes = []
    this._text = v === null ? null : String(v)
  }
  contains(node) {
    if (node === this) return true
    return this.childNodes.some((c) => c.contains?.(node))
  }
  closest(selector) {
    let n = this
    while (n) {
      if (matches(n, selector)) return n
      n = n.parentNode
    }
    return null
  }
  querySelector(selector) {
    return this.queryAll(selector)[0] || null
  }
  querySelectorAll(selector) {
    return this.queryAll(selector)
  }
  queryAll(selector, acc = []) {
    for (const c of this.childNodes) {
      if (c instanceof Element) {
        if (matches(c, selector)) acc.push(c)
        c.queryAll(selector, acc)
      }
    }
    return acc
  }
  /** Depth-first text, used by tests to assert content without a real layout. */
  get innerText() {
    return this.textContent
  }
  scrollIntoView() {}
  getBoundingClientRect() {
    return { x: 0, y: 0, top: 0, left: 0, right: 1200, bottom: 800, width: 1200, height: 800 }
  }
  focus() {}
  select() {}
  click() {
    this.dispatchEvent({ type: 'click', target: this, preventDefault() {}, stopPropagation() {} })
  }
  getContext() {
    return canvasContext()
  }
}

/** No-op 2D context: enough for GraphCanvas to run its draw loop headlessly. */
function canvasContext() {
  const noop = () => {}
  return new Proxy(
    {
      canvas: null,
      fillStyle: '',
      strokeStyle: '',
      lineWidth: 1,
      font: '',
      textAlign: '',
      textBaseline: '',
      globalAlpha: 1,
      setTransform: noop,
      clearRect: noop,
      save: noop,
      restore: noop,
      translate: noop,
      scale: noop,
      rotate: noop,
      beginPath: noop,
      closePath: noop,
      moveTo: noop,
      lineTo: noop,
      arc: noop,
      fill: noop,
      stroke: noop,
      fillText: noop,
      measureText: () => ({ width: 8 }),
    },
    { get: (t, k) => (k in t ? t[k] : noop), set: (t, k, v) => ((t[k] = v), true) },
  )
}

class Element extends Node {
  constructor(tag) {
    super(tag.toUpperCase())
    this.tagName = tag.toUpperCase()
    this.localName = tag
    this.id = ''
    this.value = ''
  }
}

class TextNode extends Node {
  constructor(text) {
    super('#text')
    this._text = String(text)
  }
  get textContent() {
    return this._text
  }
  set textContent(v) {
    this._text = String(v)
  }
}

class Fragment extends Node {
  constructor() {
    super('#fragment')
  }
}

function matches(node, selector) {
  if (!selector) return false
  if (selector.startsWith('.')) return node.classList.contains(selector.slice(1))
  if (selector.startsWith('#')) return node.id === selector.slice(1)
  const attr = /^\[([^\]=]+)(?:=["']?([^\]"']*)["']?)?\]$/.exec(selector)
  if (attr) {
    const v = node.attributes[attr[1]] ?? node.dataset?.[attr[1]]
    return attr[2] === undefined ? v !== undefined : String(v) === attr[2]
  }
  if (selector.includes('.')) {
    const [tag, ...cls] = selector.split('.')
    return (!tag || node.localName === tag) && cls.every((c) => node.classList.contains(c))
  }
  return node.localName === selector
}

class Document extends Node {
  constructor() {
    super('#document')
    this.documentElement = new Element('html')
    this.body = new Element('body')
    this.head = new Element('head')
    this.documentElement.appendChild(this.head)
    this.documentElement.appendChild(this.body)
    this.appendChild(this.documentElement)
    this.byId = new Map()
  }
  createElement(tag) {
    return new Element(tag)
  }
  createElementNS(_ns, tag) {
    return new Element(tag)
  }
  createTextNode(text) {
    return new TextNode(text)
  }
  createDocumentFragment() {
    return new Fragment()
  }
  getElementById(id) {
    return this.queryAll(`#${id}`)[0] || this.byId.get(id) || null
  }
}

export function installDOM() {
  const document = new Document()
  const win = {
    document,
    devicePixelRatio: 1,
    navigator: { platform: 'linux', userAgent: 'node', clipboard: { writeText: async () => true } },
    studio: {
      settings: { set: async () => ({}), all: async () => ({}), history: async () => [] },
      open: async () => true,
      save: async () => ({ ok: true, path: '/tmp/x' }),
      reveal: () => true,
      project: { pick: async () => null, use: async (p) => p, tree: async () => ({ files: [], root: '' }) },
      fs: { read: async () => ({ ok: false, error: 'stub' }) },
      run: async () => ({ ok: true, json: {}, stdout: '', stderr: '', exitCode: 0, ms: 1, command: [] }),
      git: async () => ({ isRepo: false, branch: '', changed: [], commits: [], branches: [], changedCount: 0 }),
      gitDiff: async () => ({ ok: true, diff: '', bytes: 0, untracked: [] }),
      mcp: { inspect: async () => ({ ok: true, tools: [] }) },
      info: async () => ({ version: '0.0.0-test', env: {} }),
      binary: { resolve: async () => ({ binary: '/bin/codemap' }), version: async () => ({ ok: true, version: 'test' }) },
      theme: async () => true,
      on: () => () => {},
      daemon: { launch: async () => ({ ok: true, pid: 1 }), kill: async () => ({ ok: true }) },
      cancel: async () => true,
      active: async () => [],
    },
  }
  globalThis.window = win
  globalThis.document = document
  globalThis.Node = Node
  globalThis.Element = Element
  // Node >= 21 ships a getter-only `navigator`; redefine rather than assign.
  try {
    Object.defineProperty(globalThis, 'navigator', { value: win.navigator, configurable: true, writable: true })
  } catch {
    /* keep the platform navigator */
  }
  globalThis.getComputedStyle = () => ({ getPropertyValue: () => '' })
  globalThis.requestAnimationFrame = (fn) => setTimeout(() => fn(Date.now()), 0)
  globalThis.cancelAnimationFrame = (id) => clearTimeout(id)
  globalThis.ResizeObserver = class {
    observe() {}
    disconnect() {}
  }
  globalThis.Blob = class {
    constructor(parts) {
      this.parts = parts
    }
  }
  globalThis.URL = globalThis.URL || {}
  globalThis.URL.createObjectURL = () => 'blob:stub'
  globalThis.URL.revokeObjectURL = () => {}
  globalThis.CustomEvent = class {
    constructor(type, init = {}) {
      this.type = type
      Object.assign(this, init)
    }
  }
  return { document, window: win }
}

/** Count rendered elements and total text length — a cheap "did it draw" check. */
export function stats(node) {
  let count = 0
  let chars = 0
  const walk = (n) => {
    count++
    if (n instanceof TextNode) chars += (n.textContent || '').length
    for (const c of n.childNodes || []) walk(c)
  }
  walk(node)
  return { count, chars, text: node.textContent || '' }
}
