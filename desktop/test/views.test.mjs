/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// View smoke tests: every app view must mount and finish its first async load
// without throwing, against the stubbed bridge. These catch import errors,
// bad destructuring and unguarded report access that unit tests cannot see.

import test from 'node:test'
import assert from 'node:assert/strict'

import { installDOM, stats } from './dom-stub.mjs'

installDOM()

const { state } = await import('../renderer/js/state.mjs')
const { FEATURES } = await import('../renderer/js/features.mjs')

state.project = '/tmp/project'
state.settings = { theme: 'dark', density: 'comfortable', fontSize: 13, autoRefresh: true, autoSearch: false, graphPhysics: false, recentProjects: [], recentSearches: [], recentRaw: [], timeoutMs: 1000, indexTimeoutMs: 1000 }
state.git = { isRepo: true, branch: 'main', changed: [], changedCount: 0, commits: [], branches: [] }
state.tree = { root: '/tmp/project', files: ['a/b.go', 'a/c_test.go', 'README.md'] }

const ctx = {
  go: () => {},
  openFeature: () => {},
  onFeature: () => {},
  openSymbol: () => {},
  onSymbol: () => {},
  openFile: () => {},
  onFile: () => {},
  onSymbolQuery: () => {},
  onPrefill: () => {},
  onNext: () => {},
  onGraph: () => {},
  onRerun: () => {},
  onOpenProject: () => {},
  onMapView: () => {},
  setGraphPayload: () => {},
  setProject: () => {},
  renderInspector: () => {},
  applyTheme: () => {},
  applyPrefs: () => {},
  runSilent: async () => ({ ok: true, json: {}, stdout: '', stderr: '', exitCode: 0, ms: 1, command: [] }),
  toast: () => {},
}

const settle = (ms = 60) => new Promise((r) => setTimeout(r, ms))

// Module namespace keys are sorted alphabetically, so pick the export by name
// rather than by "the first thing that looks like a view".
async function mountView(name, exportName, opts) {
  const mod = await import(`../renderer/js/views/${name}.mjs`)
  const fn = mod[exportName]
  assert.equal(typeof fn, 'function', `${name}: no ${exportName} export`)
  const instance = fn(ctx, opts || {})
  assert.ok(instance?.node, `${name}: view returned no node`)
  await settle()
  const s = stats(instance.node)
  assert.ok(s.count > 3, `${name}: rendered only ${s.count} nodes`)
  instance.destroy?.()
  return { instance, s }
}

test('dashboard mounts and renders its sections', async () => {
  const { s } = await mountView('dashboard', 'dashboardView')
  assert.ok(s.text.includes('Dashboard'), 'dashboard heading missing')
})

test('catalog lists every registered feature', async () => {
  const { s } = await mountView('catalog', 'catalogView')
  const missing = FEATURES.filter((f) => !s.text.includes(f.title))
  assert.deepEqual(missing.map((f) => f.id), [], 'catalog does not list every feature')
  assert.ok(s.text.includes('Feature catalog'))
})

test('search view mounts with an empty query', async () => {
  const { s } = await mountView('searchview', 'searchView', { query: '', engine: 'all' })
  assert.ok(s.text.includes('Unified search'))
})

test('graph explorer mounts without data', async () => {
  const { s } = await mountView('graphview', 'graphView', { mode: 'traverse', autobuild: false })
  assert.ok(s.text.includes('Graph explorer'))
})

test('source browser mounts and handles a failed read', async () => {
  const { s } = await mountView('source', 'sourceView', { file: 'a/b.go', line: 3 })
  assert.ok(s.text.includes('Source browser'))
})

test('review desk mounts', async () => {
  const { s } = await mountView('reviewview', 'reviewView')
  assert.ok(s.text.includes('Review desk'))
})

test('architecture map mounts', async () => {
  const { s } = await mountView('mapview', 'mapView')
  assert.ok(s.text.includes('Architecture map'))
})

test('MCP inspector mounts', async () => {
  const { s } = await mountView('mcpview', 'mcpView')
  assert.ok(s.text.includes('MCP server'))
})

test('raw runner mounts and parses its suggestions', async () => {
  const { s } = await mountView('rawview', 'rawView', {})
  assert.ok(s.text.includes('Raw command'))
})

test('history view mounts', async () => {
  const { s } = await mountView('historyview', 'historyView')
  assert.ok(s.text.includes('Run history'))
})

test('settings view mounts', async () => {
  const { s } = await mountView('settingsview', 'settingsView')
  assert.ok(s.text.includes('Settings'))
})

test('onboarding mounts and reports preconditions', async () => {
  const { s } = await mountView('onboard', 'onboardView')
  assert.ok(s.text.includes('Codemap Studio'))
})

test('the diff renderer splits files and marks added/removed lines', async () => {
  const { diffView } = await import('../renderer/js/views/reviewview.mjs')
  const diff = ['diff --git a/x.go b/x.go', 'index 111..222 100644', '--- a/x.go', '+++ b/x.go', '@@ -1,3 +1,4 @@', ' package main', '-const a = 1', '+const a = 2', '+const b = 3', ''].join('\n')
  const node = diffView(diff)
  const s = stats(node)
  assert.ok(s.text.includes('x.go'))
  assert.ok(s.text.includes('+const a = 2'))
  assert.ok(node.queryAll('.add').length >= 2, 'added lines must be marked')
  assert.ok(node.queryAll('.del').length >= 1, 'removed lines must be marked')
  assert.ok(stats(diffView('')).text.includes('No changes'))
})

test('every registry feature can build its panel form', async () => {
  const { buildForm } = await import('../renderer/js/views/form.mjs')
  const { defaultValues } = await import('../renderer/js/features.mjs')
  for (const f of FEATURES) {
    if (!f.args) continue
    const form = buildForm(f, defaultValues(f), { advanced: true })
    assert.ok(form.node, `${f.id}: form built nothing`)
    assert.deepEqual(form.values(), defaultValues(f), `${f.id}: form lost its defaults`)
    form.set({})
    form.reset()
  }
})
