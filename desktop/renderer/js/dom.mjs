/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Tiny DOM builder. Every view in the app is composed with `h()` so there is
// exactly one place that touches createElement/textContent — and no innerHTML
// with interpolated data anywhere in the renderer.

const SVG_NS = 'http://www.w3.org/2000/svg'

function setProps(node, props) {
  for (const [key, value] of Object.entries(props || {})) {
    if (value === null || value === undefined || value === false) continue
    if (key === 'class' || key === 'className') {
      node.setAttribute('class', String(value))
    } else if (key === 'style' && typeof value === 'object') {
      for (const [sk, sv] of Object.entries(value)) {
        if (sv === null || sv === undefined) continue
        // custom properties (--c) only take effect through setProperty
        if (sk.startsWith('--') && typeof node.style.setProperty === 'function') node.style.setProperty(sk, String(sv))
        else node.style[sk] = sv
      }
    } else if (key === 'dataset') {
      for (const [dk, dv] of Object.entries(value)) {
        if (dv !== null && dv !== undefined) node.dataset[dk] = String(dv)
      }
    } else if (key === 'text') {
      node.textContent = String(value)
    } else if (key === 'value') {
      node.value = value
    } else if (key === 'checked' || key === 'disabled' || key === 'hidden' || key === 'selected' || key === 'readOnly') {
      node[key] = !!value
    } else if (key.startsWith('on') && typeof value === 'function') {
      node.addEventListener(key.slice(2).toLowerCase(), value)
    } else if (key === 'ref' && typeof value === 'function') {
      value(node)
    } else if (value === true) {
      node.setAttribute(key, '')
    } else {
      node.setAttribute(key, String(value))
    }
  }
  return node
}

function append(node, child) {
  if (child === null || child === undefined || child === false || child === true) return
  if (Array.isArray(child)) {
    for (const c of child) append(node, c)
    return
  }
  if (child instanceof Node) {
    node.appendChild(child)
    return
  }
  node.appendChild(document.createTextNode(String(child)))
}

/**
 * h('div.card', {onclick}, ...children)
 * The tag supports `name#id.class.class`; a leading `svg:` prefix builds SVG.
 */
export function h(spec, props, ...children) {
  if (typeof props !== 'object' || props === null || props instanceof Node || Array.isArray(props)) {
    children = [props, ...children]
    props = null
  }
  const isSvg = spec.startsWith('svg:')
  const rest = isSvg ? spec.slice(4) : spec
  const [tagPart, ...classParts] = rest.split('.')
  const [tag, id] = tagPart.split('#')
  const node = isSvg
    ? document.createElementNS(SVG_NS, tag || 'svg')
    : document.createElement(tag || 'div')
  if (id) node.id = id
  if (classParts.length) {
    const cls = classParts.join(' ')
    props = { ...(props || {}), class: props?.class ? `${cls} ${props.class}` : cls }
  }
  setProps(node, props)
  append(node, children)
  return node
}

export function frag(...children) {
  const f = document.createDocumentFragment()
  append(f, children)
  return f
}

export function clear(node) {
  while (node && node.firstChild) node.removeChild(node.firstChild)
  return node
}

export function mount(node, ...children) {
  clear(node)
  append(node, children)
  return node
}

/** Event delegation: on(root, 'click', '.symrow', handler) */
export function on(root, type, selector, handler, opts) {
  const listener = (evt) => {
    const target = evt.target instanceof Element ? evt.target.closest(selector) : null
    if (target && root.contains(target)) handler(evt, target)
  }
  root.addEventListener(type, listener, opts)
  return () => root.removeEventListener(type, listener, opts)
}

export function escapeHTML(s) {
  return String(s ?? '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
}

export function download(name, text, mime = 'application/json') {
  const blob = new Blob([text], { type: mime })
  const url = URL.createObjectURL(blob)
  const a = h('a', { href: url, download: name })
  document.body.appendChild(a)
  a.click()
  a.remove()
  setTimeout(() => URL.revokeObjectURL(url), 4000)
}

export async function copy(text) {
  try {
    await navigator.clipboard.writeText(text)
    return true
  } catch {
    const ta = h('textarea', { value: text, style: 'position:fixed;opacity:0' })
    document.body.appendChild(ta)
    ta.select()
    const ok = document.execCommand('copy')
    ta.remove()
    return ok
  }
}

export function debounce(fn, ms = 220) {
  let t = null
  const wrapped = (...args) => {
    clearTimeout(t)
    t = setTimeout(() => fn(...args), ms)
  }
  wrapped.cancel = () => clearTimeout(t)
  return wrapped
}

export function throttle(fn, ms = 60) {
  let last = 0
  let timer = null
  return (...args) => {
    const now = Date.now()
    const remaining = ms - (now - last)
    if (remaining <= 0) {
      last = now
      fn(...args)
    } else if (!timer) {
      timer = setTimeout(() => {
        timer = null
        last = Date.now()
        fn(...args)
      }, remaining)
    }
  }
}

/** Fuzzy-ish subsequence match used by the command palette. */
export function fuzzyScore(needle, haystack) {
  if (!needle) return 0.0001
  const n = needle.toLowerCase()
  const s = (haystack || '').toLowerCase()
  if (!s) return -1
  const exact = s.indexOf(n)
  if (exact === 0) return 1000
  if (exact > 0) return 700 - Math.min(exact, 200)
  let i = 0
  let score = 0
  let streak = 0
  for (let j = 0; j < s.length && i < n.length; j++) {
    if (s[j] === n[i]) {
      i++
      streak++
      score += 10 + streak * 3
      if (j === 0 || /[^a-z0-9]/.test(s[j - 1])) score += 18
    } else {
      streak = 0
    }
  }
  return i === n.length ? score : -1
}
