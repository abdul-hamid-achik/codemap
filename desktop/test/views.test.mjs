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
  assert.ok(s.text.includes('Health'), 'dashboard heading missing')
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

// ------------------------------------------------------------------ Learn

import { readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const here = path.dirname(fileURLToPath(import.meta.url))
const fixture = (name) => JSON.parse(readFileSync(path.join(here, 'fixtures', `${name}.json`), 'utf8'))

/** Serve real (trimmed) reports for the learning commands while `fn` runs. */
async function withLearnData(fn, { fail = false } = {}) {
  const real = window.studio.run
  const calls = []
  window.studio.run = async (req) => {
    const cmd = req.args[0]
    calls.push(req.args)
    const base = { stdout: '', stderr: '', exitCode: 0, ms: 3, command: ['codemap', ...req.args] }
    if (fail) return { ...base, ok: false, code: 'not_indexed', error: 'project is not indexed', exitCode: 3 }
    if (cmd === 'atlas') return { ...base, ok: true, json: fixture('atlas') }
    if (cmd === 'features') return { ...base, ok: true, json: fixture('features') }
    if (cmd === 'flow') return { ...base, ok: true, json: fixture('flow') }
    if (cmd === 'annotations') return { ...base, ok: true, json: { project: 'codemap', annotations: [{ id: 1, source: 'note', target: 'app.Service', note: 'central hub' }] } }
    if (cmd === 'source') return { ...base, ok: true, json: { matches: [{ symbol: 'runReview', file: 'cmd/codemap/query.go', start_line: 690, source: 'func runReview() {\n\treturn nil\n}' }] } }
    return { ...base, ok: true, json: {} }
  }
  try {
    const { clearLearnCache } = await import('../renderer/js/learn.mjs')
    clearLearnCache()
    return await fn(calls)
  } finally {
    window.studio.run = real
  }
}

test('the Learn views are registered ahead of the workspace views', async () => {
  const { APP_VIEWS } = await import('../renderer/js/features.mjs')
  const learn = APP_VIEWS.filter((v) => v.group === 'learn').map((v) => v.id)
  assert.deepEqual(learn, ['overview', 'atlas', 'features', 'flow'])
  assert.equal(APP_VIEWS.findIndex((v) => v.group === 'learn'), 0, 'Learn comes first')
})

test('overview shows the hero, six steps and persists exploration per project', async () => {
  await withLearnData(async () => {
    const { overviewView } = await import('../renderer/js/views/overview.mjs')
    const { loadProgress } = await import('../renderer/js/learn.mjs')
    localStorage.clear()
    const inst = overviewView(ctx)
    await settle(120)
    const s = stats(inst.node)
    assert.ok(s.text.includes('Get to know'), 'hero eyebrow')
    assert.ok(s.text.includes('codemap'), 'project name')
    assert.equal(inst.node.queryAll('.lp-step').length, 6, 'six learning steps')
    assert.ok(s.text.includes('0/6'), 'progress starts at zero')
    assert.ok(inst.node.queryAll('.od-row').length >= 2, 'top-level directories listed')
    assert.ok(inst.node.queryAll('.fcard').length >= 2, 'feature cards listed')
    assert.ok(inst.node.queryAll('.kc-row').length >= 1, 'key symbols listed')
    // ticking a step persists under the project key and updates the bar
    inst.node.queryAll('.lp-check')[1].click()
    assert.equal(loadProgress().abilities, true)
    assert.ok(stats(inst.node).text.includes('1/6'))
    assert.ok(localStorage.getItem(`codemap-learn:${state.project}`), 'stored per project path')
  })
})

test('overview degrades to an inline call to action when the project is not indexed', async () => {
  await withLearnData(async () => {
    const { overviewView } = await import('../renderer/js/views/overview.mjs')
    const inst = overviewView(ctx)
    await settle(120)
    const s = stats(inst.node)
    assert.ok(s.text.includes('not indexed'), 'says why')
    assert.equal(inst.node.queryAll('.lp-step').length, 6, 'the path is still there')
  }, { fail: true })
})

test('atlas draws a treemap tile per directory and explains the selection', async () => {
  await withLearnData(async (calls) => {
    const { atlasView } = await import('../renderer/js/views/atlasview.mjs')
    const inst = atlasView(ctx, {})
    await settle(120)
    assert.ok(calls.some((a) => a[0] === 'atlas' && a.includes('--files') && a.includes('--depth') && a.includes('--max-nodes')), 'asks for depth-2 file-level atlas')
    const tiles = inst.node.queryAll('.at-tile')
    assert.ok(tiles.length >= 4, `expected several tiles, got ${tiles.length}`)
    for (const t of tiles) {
      assert.match(t.style.width, /px$/)
      assert.ok(parseFloat(t.style.width) > 0 && parseFloat(t.style.height) > 0)
    }
    const text = stats(inst.node).text
    assert.ok(text.includes('Key symbols'), 'detail panel shows key symbols')
    assert.ok(text.includes('Copy as markdown'))
    assert.ok(text.includes('language'), 'legend')
    inst.destroy?.()
  })
})

test('atlas shows onboarding when nothing is indexed', async () => {
  await withLearnData(async () => {
    const { atlasView } = await import('../renderer/js/views/atlasview.mjs')
    const inst = atlasView(ctx, {})
    await settle(120)
    assert.ok(stats(inst.node).text.includes('not indexed yet'))
    inst.destroy?.()
  }, { fail: true })
})

test('features lists cards, nests sub-commands and filters by search', async () => {
  await withLearnData(async () => {
    const { featuresView } = await import('../renderer/js/views/featuresview.mjs')
    const inst = featuresView(ctx, {})
    await settle(120)
    const cards = inst.node.queryAll('.fcard')
    assert.equal(cards.length, fixture('features').features.length)
    assert.ok(inst.node.queryAll('.nested').length >= 2, 'sub-commands are indented under their parent')
    assert.ok(stats(inst.node).text.includes('codemap agent list'))
    inst.navigate({ query: 'annotate' })
    const filtered = inst.node.queryAll('.fcard')
    assert.ok(filtered.length >= 1 && filtered.length < cards.length, 'search narrows the list')
  })
})

test('flow traces an entry into an outline, a route strip and a diagram', async () => {
  await withLearnData(async (calls) => {
    const { flowView } = await import('../renderer/js/views/flowview.mjs')
    const inst = flowView(ctx, { symbol: 'runReview', label: 'runReview' })
    await settle(160)
    assert.ok(calls.some((a) => a[0] === 'flow'), 'ran codemap flow')
    const json = fixture('flow')
    let n = 0
    ;(function count(x) { n++; (x.children || []).forEach(count) })(json.root)
    assert.equal(inst.node.queryAll('.fo-row').length, n, 'one outline row per step')
    assert.ok(inst.node.queryAll('.route-chip').length >= 2, 'route strip chips')
    assert.ok(inst.node.queryAll('.amb').length >= 1, 'ambiguous placeholder badge')
    assert.ok(inst.node.queryAll('.rep').length >= 1, 'repeat badge')
    assert.ok(stats(inst.node).text.includes('could not be pinned to one definition'), 'ambiguity explained in words')
    inst.setMode('diagram')
    await settle(40)
    assert.equal(inst.node.queryAll('.fd-node').length, n, 'one diagram node per step')
    assert.ok(inst.node.queryAll('.fd-edge').length === n - 1, 'one edge per parent link')
    inst.destroy?.()
  })
})

test('flow with no entry offers somewhere to start', async () => {
  await withLearnData(async () => {
    const { flowView } = await import('../renderer/js/views/flowview.mjs')
    const inst = flowView(ctx, {})
    await settle(80)
    assert.ok(stats(inst.node).text.includes('Pick something to follow'))
    inst.destroy?.()
  })
})

test('the flow graph payload feeds the existing graph explorer', async () => {
  const { graphPayloadFor } = await import('../renderer/js/report.mjs')
  const { feature } = await import('../renderer/js/features.mjs')
  const p = graphPayloadFor(feature('flow'), fixture('flow'))
  assert.ok(p && p.nodes.length > 3 && p.edges.length >= 3)
  assert.equal(p.nodes.filter((x) => x.role === 'focus').length, 1)
  assert.ok(p.edges.every((e) => e.type === 'calls'))
})

test('architecture map hides non-source subsystems and light bridges by default', async () => {
  const { isNonSourceSubsystem, autoMinEdges } = await import('../renderer/js/views/mapview.mjs')
  for (const name of ['specs', 'docs', '.github', 'bench', 'internal/testdata', 'desktop/test']) assert.ok(isNonSourceSubsystem(name), name)
  for (const name of ['internal/app', 'cmd/codemap', 'desktop']) assert.ok(!isNonSourceSubsystem(name), name)
  const bridges = Array.from({ length: 100 }, (_, i) => ({ count: 100 - i }))
  assert.equal(autoMinEdges(bridges, 40), 61, 'keeps about the 40 heaviest')
  assert.equal(autoMinEdges(bridges.slice(0, 10), 40), 1, 'small maps keep everything')
})

test('palette gives languages and subsystems stable, distinct hues', async () => {
  const { languageColor, subsystemColor, resetSubsystemColors, isSupport } = await import('../renderer/js/palette.mjs')
  assert.equal(languageColor('go'), languageColor('Go'))
  assert.notEqual(languageColor('go'), languageColor('typescript'))
  resetSubsystemColors()
  const names = ['cmd/codemap', 'internal/app', 'internal/graph', 'internal/git', 'internal/index', 'internal/config', 'internal/extract']
  const colors = names.map(subsystemColor)
  assert.equal(new Set(colors).size, names.length, 'no collisions among a handful of subsystems')
  assert.equal(subsystemColor('internal/app'), colors[1], 'stable on repeat')
  assert.ok(isSupport(['tests', 'config']) && !isSupport(['source']) && !isSupport(['docs', 'entrypoint']))
})

// ------------------------------------------------------------- run button

test('clicking Run on a feature panel runs it, and Stop cancels it by feature id', async () => {
  const { featureView } = await import('../renderer/js/views/feature.mjs')
  const realRun = window.studio.run
  const realCancel = window.studio.cancel
  const runs = []
  const cancels = []
  let release
  window.studio.run = (req) => {
    runs.push(req)
    return new Promise((resolve) => {
      release = () => resolve({ ok: true, json: { project: 'p', nodes: 1 }, stdout: '', stderr: '', exitCode: 0, ms: 1, command: ['codemap', ...req.args] })
    })
  }
  window.studio.cancel = async (key) => {
    cancels.push(key)
    return true
  }
  try {
    const view = featureView(FEATURES.find((f) => f.id === 'status'), ctx, {})
    const buttons = []
    const walk = (n) => {
      if (n?.tagName === 'BUTTON') buttons.push(n)
      for (const c of n?.childNodes || []) walk(c)
    }
    walk(view.node)
    const run = buttons.find((b) => /Run/.test(b.textContent))
    const stop = buttons.find((b) => b.textContent === 'Stop')
    assert.ok(run, 'the panel renders a Run button')
    run.click()
    await new Promise((r) => setTimeout(r, 0))
    assert.equal(runs.length, 1, 'a click on Run must start the command (it used to do nothing)')
    assert.deepEqual(runs[0].args.slice(0, 1), ['status'])
    stop.click()
    assert.deepEqual(cancels, ['status'], 'Stop cancels the run through its feature id')
    release()
    await new Promise((r) => setTimeout(r, 0))
  } finally {
    window.studio.run = realRun
    window.studio.cancel = realCancel
  }
})
