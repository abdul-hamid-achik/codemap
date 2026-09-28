/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Form builder for a feature's declared arguments. One implementation, so
// every one of the ~60 codemap surfaces gets the same input ergonomics:
// inline hints, defaults, repeatable chips, file pickers.

import { h, clear } from '../dom.mjs'
import { defaultValues } from '../features.mjs'

export function buildForm(feat, values, { onChange, advanced = false } = {}) {
  const state = { ...defaultValues(feat), ...(values || {}) }
  const nodes = []
  const listeners = new Set()
  if (onChange) listeners.add(onChange)
  const emit = () => {
    for (const fn of listeners) fn({ ...state })
  }

  for (const a of feat.args || []) {
    const isAdvanced = !!a.advanced || ['via_vault', 'embed_batch_size', 'embed_concurrency', 'embed_max_chars', 'max_file_bytes', 'force_extra', 'exclude', 'embed_cache_size', 'embed_max_in_flight', 'embed_rps', 'idle_timeout', 'debounce', 'max_content_bytes', 'files_from', 'selector'].includes(a.name)
    if (isAdvanced && !advanced) {
      nodes.push(hiddenField(a, state, emit))
      continue
    }
    nodes.push(field(a, state, emit))
  }

  return {
    node: h('div.form-grid', nodes),
    values: () => ({ ...state }),
    set(patch) {
      Object.assign(state, patch)
      for (const n of nodes) n.__sync?.(state)
      emit()
    },
    reset() {
      const fresh = defaultValues(feat)
      for (const k of Object.keys(state)) delete state[k]
      Object.assign(state, fresh)
      for (const n of nodes) n.__sync?.(state)
      emit()
    },
    onChange(fn) {
      listeners.add(fn)
      return () => listeners.delete(fn)
    },
    focusFirst() {
      const el = nodes.find((n) => n.__input)?.__input
      el?.focus()
    },
  }
}

function label(a) {
  return h('label', [a.label || a.name, a.required ? h('span.req', '*') : null])
}

function hint(a) {
  return a.hint ? h('div.hint', a.hint) : null
}

function hiddenField(a, state, emit) {
  // keep non-rendered values addressable so `set()` from a suggestion still works
  const node = h('div', { hidden: true })
  node.__sync = () => {}
  node.__makeVisible = () => {
    clear(node)
    const real = field(a, state, emit)
    node.hidden = false
    node.replaceWith(real)
  }
  return node
}

function field(a, state, emit) {
  switch (a.kind) {
    case 'bool':
      return boolField(a, state, emit)
    case 'select':
      return selectField(a, state, emit)
    case 'num':
      return numField(a, state, emit)
    case 'repeat':
      return repeatField(a, state, emit)
    case 'path':
      return pathField(a, state, emit)
    default:
      return textField(a, state, emit)
  }
}

function wrap(a, control, extra) {
  const node = h('div.field', [label(a), control, hint(a), extra])
  return node
}

function textField(a, state, emit) {
  const input = a.multiline
    ? h('textarea', { placeholder: a.placeholder || '', value: String(state[a.name] ?? ''), rows: '3', spellcheck: 'false' })
    : h('input', { type: 'text', placeholder: a.placeholder || '', value: String(state[a.name] ?? ''), spellcheck: 'false' })
  input.addEventListener('input', () => {
    state[a.name] = input.value
    emit()
  })
  input.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && !a.multiline && !e.metaKey && !e.ctrlKey) {
      e.preventDefault()
      input.dispatchEvent(new CustomEvent('submit-form', { bubbles: true }))
    }
  })
  const node = wrap(a, input)
  node.__input = input
  node.__sync = (s) => {
    if (input.value !== String(s[a.name] ?? '')) input.value = String(s[a.name] ?? '')
  }
  return node
}

function numField(a, state, emit) {
  const input = h('input', {
    type: 'number',
    value: state[a.name] === '' || state[a.name] === undefined ? '' : String(state[a.name] ?? ''),
    placeholder: a.def !== undefined ? String(a.def) : '',
    min: a.min !== undefined ? String(a.min) : null,
    max: a.max !== undefined ? String(a.max) : null,
    step: a.step || '1',
  })
  input.addEventListener('input', () => {
    state[a.name] = input.value === '' ? '' : Number(input.value)
    emit()
  })
  const node = wrap(a, input, a.def !== undefined ? h('div.btn-row', { style: 'margin-top:4px' }, [
    h('button.btn.sm.ghost', { type: 'button', text: `use default (${a.def})`, onclick: () => { input.value = String(a.def); state[a.name] = a.def; emit() } }),
  ]) : null)
  node.__input = input
  node.__sync = (s) => {
    const v = s[a.name]
    input.value = v === '' || v === undefined || v === null ? '' : String(v)
  }
  return node
}

function selectField(a, state, emit) {
  const sel = h(
    'select',
    (a.options || []).map((o) => {
      const opt = typeof o === 'string' ? { v: o, label: o } : o
      return h('option', { value: opt.v, selected: String(state[a.name] ?? '') === String(opt.v) }, opt.label)
    }),
  )
  sel.addEventListener('change', () => {
    state[a.name] = sel.value
    emit()
  })
  const node = wrap(a, sel)
  node.__input = sel
  node.__sync = (s) => {
    sel.value = String(s[a.name] ?? '')
  }
  return node
}

function boolField(a, state, emit) {
  const box = h('input', { type: 'checkbox', checked: !!state[a.name] })
  const node = h('div.field', [
    h('label', a.label || a.name),
    h('label.check', [box, h('span.mono', a.flag || a.name)]),
    hint(a),
  ])
  box.addEventListener('change', () => {
    state[a.name] = box.checked
    emit()
  })
  node.__input = box
  node.__sync = (s) => {
    box.checked = !!s[a.name]
  }
  return node
}

function repeatField(a, state, emit) {
  const list = h('div.chipset')
  const input = h('input', { type: 'text', placeholder: a.placeholder || 'add…', spellcheck: 'false' })
  const redraw = () => {
    clear(list)
    const arr = Array.isArray(state[a.name]) ? state[a.name] : []
    arr.forEach((v, i) => {
      list.appendChild(
        h('button.chip', {
          type: 'button',
          title: 'remove',
          onclick: () => {
            state[a.name] = arr.filter((_, j) => j !== i)
            redraw()
            emit()
          },
        }, [h('span.mono', String(v)), h('span.x', '✕')]),
      )
    })
    if (!arr.length) list.appendChild(h('span.small.dim', 'none — the query falls back to a name'))
  }
  const add = () => {
    const v = input.value.trim()
    if (!v) return
    state[a.name] = [...(Array.isArray(state[a.name]) ? state[a.name] : []), v]
    input.value = ''
    redraw()
    emit()
  }
  input.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') {
      e.preventDefault()
      add()
    }
    if (e.key === 'Backspace' && !input.value && Array.isArray(state[a.name]) && state[a.name].length) {
      state[a.name] = state[a.name].slice(0, -1)
      redraw()
      emit()
    }
  })
  redraw()
  const node = wrap(a, h('div.stack', { style: 'gap:6px' }, [list, h('div.field-row', [input, h('button.btn.sm', { type: 'button', text: 'Add', onclick: add })])]))
  node.__input = input
  node.__sync = () => redraw()
  return node
}

function pathField(a, state, emit) {
  const input = h('input', { type: 'text', placeholder: a.placeholder || '/path/to/file', value: String(state[a.name] ?? ''), spellcheck: 'false' })
  input.addEventListener('input', () => {
    state[a.name] = input.value
    emit()
  })
  const browse = h('button.btn.sm', {
    type: 'button',
    text: 'Browse…',
    onclick: async () => {
      // reuse the project picker for directories, or ask for a path inline
      const p = await window.studio.project.pick()
      if (p) {
        input.value = p
        state[a.name] = p
        emit()
      }
    },
  })
  const node = wrap(a, h('div.field-row', [input, browse]))
  node.__input = input
  node.__sync = (s) => {
    input.value = String(s[a.name] ?? '')
  }
  return node
}
