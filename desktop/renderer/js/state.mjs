/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Single mutable app state plus a tiny event bus. Views render from `state`
// and subscribe to the events they care about; nothing else holds truth.

export const state = {
  info: {},
  settings: {},
  project: '',
  projects: [],
  status: null,
  git: null,
  doctor: null,
  view: 'dashboard',
  featureId: null,
  values: {},
  lastResult: null,
  results: new Map(), // featureId -> last result
  running: 0,
  stream: [],
  inspector: { open: false, title: '', kind: '', payload: null },
  graph: { nodes: [], edges: [], focus: null, title: '', subtitle: '' },
  source: { file: '', line: 0, symbol: null },
  tree: null,
  boot: { binary: null, version: '', error: '' },
}

const handlers = new Map()

export const bus = {
  on(evt, fn) {
    if (!handlers.has(evt)) handlers.set(evt, new Set())
    handlers.get(evt).add(fn)
    return () => handlers.get(evt)?.delete(fn)
  },
  emit(evt, payload) {
    const set = handlers.get(evt)
    if (!set) return
    for (const fn of [...set]) {
      try {
        fn(payload)
      } catch (err) {
        console.error(`[bus:${evt}]`, err)
      }
    }
  },
}

export const EVENTS = {
  VIEW: 'view',
  PROJECT: 'project',
  STATUS: 'status',
  RESULT: 'result',
  RUNNING: 'running',
  STREAM: 'stream',
  INSPECTOR: 'inspector',
  GRAPH: 'graph',
  SOURCE: 'source',
  SETTINGS: 'settings',
  TREE: 'tree',
}

export function setView(view, opts = {}) {
  state.view = view
  if (opts.featureId !== undefined) state.featureId = opts.featureId
  bus.emit(EVENTS.VIEW, { view, ...opts })
}

export function setProject(p) {
  state.project = p
  bus.emit(EVENTS.PROJECT, p)
}

export function openInspector({ title, kind, payload }) {
  state.inspector = { open: true, title, kind, payload }
  bus.emit(EVENTS.INSPECTOR, state.inspector)
}

export function closeInspector() {
  state.inspector = { ...state.inspector, open: false }
  bus.emit(EVENTS.INSPECTOR, state.inspector)
}

export function setGraph(payload) {
  state.graph = { ...state.graph, ...payload }
  bus.emit(EVENTS.GRAPH, state.graph)
}

export function openSource({ file, line = 0, symbol = null, fqn = '' }) {
  state.source = { file, line, symbol, fqn }
  bus.emit(EVENTS.SOURCE, state.source)
}

/** Merge a codemap report into a graph payload the explorer can draw. */
export function pushGraph(payload) {
  setGraph(payload)
  setView('graph')
}
