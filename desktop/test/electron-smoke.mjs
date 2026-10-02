/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Electron smoke driver. Boots the real app in a real window against the real
// codemap binary, walks every view, exercises live features, collects page
// errors, and writes screenshots. Run with: node test/smoke.mjs

import { app, BrowserWindow } from 'electron'
import { writeFileSync, mkdirSync, existsSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

import { createRuntime } from '../electron/runtime.mjs'
import { Settings } from '../electron/settings.mjs'

const here = path.dirname(fileURLToPath(import.meta.url))
const appRoot = path.resolve(here, '..')
const repo = path.resolve(appRoot, '..')
const outDir = process.env.SMOKE_OUT || '/tmp/codemap-studio-smoke'
const userData = path.join(outDir, 'userdata')
const shots = path.join(outDir, 'shots')
mkdirSync(userData, { recursive: true })
mkdirSync(shots, { recursive: true })

const project = process.env.SMOKE_PROJECT || repo

// deterministic, isolated settings so the smoke run never touches the user's
writeFileSync(
  path.join(userData, 'codemap-studio.json'),
  JSON.stringify(
    {
      binaryPath: [path.join(repo, 'bin', 'codemap'), path.join(repo, 'codemap')].find((p) => existsSync(p)) || '',
      projectPath: project,
      recentProjects: [project],
      theme: process.env.SMOKE_THEME || 'dark',
      density: 'comfortable',
      fontSize: 13,
      autoRefresh: true,
      autoSearch: false,
      graphPhysics: true,
      history: [],
    },
    null,
    2,
  ),
)

app.setPath('userData', userData)

const errors = []
const logs = []
const results = { ok: true, views: [], features: [], errors, shots: [] }

async function shoot(win, name) {
  const img = await win.webContents.capturePage()
  const file = path.join(shots, `${name}.png`)
  writeFileSync(file, img.toPNG())
  results.shots.push(file)
}

async function evalIn(win, fn, ...args) {
  const source = `(${fn.toString()})(${args.map((a) => JSON.stringify(a)).join(',')})`
  return win.webContents.executeJavaScript(source, true)
}

const wait = (ms) => new Promise((r) => setTimeout(r, ms))
const log = (msg) => process.stdout.write(`[smoke] ${msg}\n`)

app.whenReady().then(async () => {
  // Same runtime the shipped app uses — no parallel IPC stub to drift.
  const settings = new Settings(userData)
  let winRef = null
  const runtime = createRuntime({ appRoot, settings, getWin: () => winRef, isDev: false })
  runtime.registerIPC()

  const win = new BrowserWindow({
    width: 1600,
    height: 1000,
    show: false,
    backgroundColor: '#0a0c0f',
    webPreferences: {
      preload: path.join(appRoot, 'electron', 'preload.mjs'),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: false,
      offscreen: true,
    },
  })
  winRef = win

  win.webContents.on('console-message', (_e, level, message, line, source) => {
    logs.push({ level, message: String(message).slice(0, 400), line, source })
    if (level >= 3) errors.push(`console.error: ${message}`)
  })
  win.webContents.on('render-process-gone', (_e, details) => {
    errors.push(`renderer gone: ${details.reason}`)
  })
  win.webContents.on('preload-error', (_e, p, err) => errors.push(`preload error ${p}: ${err?.message}`))
  process.on('uncaughtException', (e) => errors.push(`main uncaught: ${e?.message}`))

  await win.loadFile(path.join(appRoot, 'renderer', 'index.html'))

  log('window loaded')
  // ---- boot assertions ---------------------------------------------------
  await wait(2500)
  const booted = await evalIn(win, () => ({
    hasBridge: typeof window.studio === 'object',
    hasDebug: typeof window.__studio === 'object',
    appVisible: !document.getElementById('app')?.hidden,
    project: window.__studio?.state?.project || '',
    binary: window.__studio?.state?.boot?.binary || '',
    version: (window.__studio?.state?.boot?.version || '').split('\n')[0],
    features: window.__studio?.FEATURES?.length || 0,
    navItems: document.querySelectorAll('.nav-item').length,
    view: window.__studio?.current || document.querySelector('.view-root')?.firstElementChild?.className || '',
  }))
  results.boot = booted
  if (!booted.hasBridge) errors.push('window.studio bridge missing')
  if (!booted.hasDebug) errors.push('window.__studio debug handle missing')
  if (!booted.appVisible) errors.push('app shell never became visible')
  if (!booted.binary) errors.push(`no codemap binary resolved: ${JSON.stringify(booted)}`)
  if (booted.project !== project) errors.push(`project not applied: ${booted.project}`)
  if (booted.navItems < 40) errors.push(`sidebar only rendered ${booted.navItems} nav items`)

  await shoot(win, '01-boot')


  // ---- Learn: the project lands on Overview, and every Learn view draws ------
  const only = process.env.SMOKE_ONLY || ''
  const learnSection = async () => {
    const landed = await evalIn(win, () => ({ view: window.__studio.state.view, hero: !!document.querySelector('.ov-hero'), active: document.querySelector('.nav-item.active')?.dataset?.nav || '' }))
    results.landing = landed
    if (landed.view !== 'overview' || !landed.hero) errors.push(`indexed project did not land on Overview: ${JSON.stringify(landed)}`)
    log('learn: overview')
    await wait(3500)
    const ov = await evalIn(win, () => ({
      steps: document.querySelectorAll('.lp-step').length,
      dirs: document.querySelectorAll('.od-row').length,
      cards: document.querySelectorAll('.lp-step .fcard').length,
      concepts: document.querySelectorAll('.kc-row').length,
      errorBoxes: document.querySelectorAll('.view-root .error-box').length,
      summary: (document.querySelector('.ov-summary')?.textContent || '').slice(0, 80),
    }))
    results.overview = ov
    if (ov.steps !== 6) errors.push(`overview rendered ${ov.steps} steps`)
    if (ov.dirs < 3) errors.push(`overview big-picture listed ${ov.dirs} directories`)
    if (ov.cards < 3) errors.push(`overview feature cards: ${ov.cards}`)
    if (ov.concepts < 2) errors.push(`overview core concepts: ${ov.concepts}`)
    if (ov.errorBoxes) errors.push(`overview shows ${ov.errorBoxes} error box(es)`)
    await shoot(win, 'learn-01-overview')
    // tick a step, then scroll to show the path
    await evalIn(win, () => { document.querySelectorAll('.lp-check')[0].click(); document.querySelector('.view-root').scrollTop = 520 })
    await wait(500)
    await shoot(win, 'learn-02-overview-path')

    log('learn: atlas')
    await evalIn(win, () => window.__studio.route('atlas', { fresh: true }))
    await wait(3200)
    const at = await evalIn(win, () => {
      const tiles = [...document.querySelectorAll('.at-tile')]
      return { tiles: tiles.length, groups: document.querySelectorAll('.at-tile.group').length, labelled: tiles.filter((t) => t.querySelector('.at-name')).length, legend: (document.querySelector('.at-legend')?.textContent || '').slice(0, 60), detail: (document.querySelector('.at-detail .dp-name')?.textContent || ''), crumbs: document.querySelector('.at-crumbs')?.textContent || '' }
    })
    results.atlas = at
    if (at.tiles < 8) errors.push(`atlas drew ${at.tiles} tiles`)
    if (at.groups < 2) errors.push(`atlas drew ${at.groups} framed groups`)
    await shoot(win, 'learn-03-atlas-root')

    for (const mode of ['role', 'coupling', 'tests']) {
      await evalIn(win, (m) => { [...document.querySelectorAll('.seg-btn')].find((b) => b.textContent === m)?.click() }, mode)
      await wait(500)
      await shoot(win, `learn-04-atlas-color-${mode}`)
    }
    await evalIn(win, () => { [...document.querySelectorAll('.seg-btn')].find((b) => b.textContent === 'language')?.click() })

    await evalIn(win, () => window.__studio.instances.get('view:atlas').zoomTo('internal'))
    await wait(2500)
    const zoomed = await evalIn(win, () => ({ crumbs: document.querySelector('.at-crumbs')?.textContent || '', tiles: document.querySelectorAll('.at-tile').length, detail: document.querySelector('.at-detail .dp-name')?.textContent || '', keys: document.querySelectorAll('.at-detail .ds-row').length }))
    results.atlasZoom = zoomed
    if (!zoomed.crumbs.includes('internal')) errors.push(`atlas did not zoom into internal: ${JSON.stringify(zoomed)}`)
    if (zoomed.tiles < 8) errors.push(`zoomed atlas drew ${zoomed.tiles} tiles`)
    await shoot(win, 'learn-05-atlas-internal')

    // select a package through the same path a shift-click takes
    await evalIn(win, () => { const t = [...document.querySelectorAll('.at-tile')].find((x) => x.dataset.path === 'internal/app'); t?.dispatchEvent(new MouseEvent('click', { bubbles: true, shiftKey: true })) })
    await wait(600)
    await shoot(win, 'learn-06-atlas-selected')

    // "Browse files" from the detail panel filters the source tree
    await evalIn(win, () => window.__studio.ctx.go('source', { filter: 'internal/app', fresh: true }))
    await wait(2200)
    const browse = await evalIn(win, () => ({ rows: document.querySelectorAll('.ft-row').length, off: [...document.querySelectorAll('.ft-row')].filter((r) => !(r.textContent || '').includes('internal/app')).length }))
    results.browse = browse
    if (browse.rows < 5 || browse.off) errors.push(`source filter from Atlas did not narrow the tree: ${JSON.stringify(browse)}`)

    // the command palette finds the Learn views
    for (const q of ['atlas', 'overview', 'flow']) {
      await evalIn(win, (x) => window.__studio.openPalette(x), q)
      await wait(500)
      const hit = await evalIn(win, () => [...document.querySelectorAll('.palette-row')].some((r) => r.querySelector('.p-kind')?.textContent === 'view'))
      if (!hit) errors.push(`palette found no view for "${q}"`)
      if (q === 'atlas') await shoot(win, 'learn-palette')
      await evalIn(win, () => { document.getElementById('palette').hidden = true })
    }

    log('learn: features')
    await evalIn(win, () => window.__studio.route('features', { fresh: true }))
    await wait(3000)
    const fe = await evalIn(win, () => ({ cards: document.querySelectorAll('.fcard').length, tabs: [...document.querySelectorAll('.tabs .tab')].map((t) => t.textContent), nested: document.querySelectorAll('.fcard.nested').length, errors: document.querySelectorAll('.view-root .error-box').length }))
    results.features_view = fe
    if (fe.cards < 20) errors.push(`features view listed ${fe.cards} cards`)
    if (fe.errors) errors.push('features view shows an error box')
    await shoot(win, 'learn-07-features')

    log('learn: flow from the first feature')
    const flowed = await evalIn(win, async () => {
      const mod = await import('./js/learn.mjs')
      const res = await mod.loadFeatures()
      const list = (res.json?.features || []).filter((f) => f.kind === 'cli_command' && f.handler && f.footprint?.symbols > 20)
      const f = list.find((x) => /review/.test(x.label)) || list[0]
      const t = mod.featureFlowTarget(f)
      window.__studio.route('flow', { fresh: true, ...t, label: mod.featureLabel(f), description: f.description, feature: f })
      return mod.featureLabel(f)
    })
    results.flowFeature = flowed
    await wait(3500)
    const fl = await evalIn(win, () => ({ rows: document.querySelectorAll('.fo-row').length, chips: document.querySelectorAll('.route-chip').length, preview: document.querySelectorAll('.fp-code .code-line').length, name: document.querySelector('.fh-name')?.textContent || '', errors: document.querySelectorAll('.view-root .error-box').length }))
    results.flow = fl
    if (fl.rows < 10) errors.push(`flow outline has ${fl.rows} rows`)
    if (fl.chips < 2) errors.push(`flow route strip has ${fl.chips} chips`)
    if (fl.preview < 3) errors.push(`flow code preview has ${fl.preview} lines`)
    await shoot(win, 'learn-08-flow-outline')
    // the panes are the point of the page: scroll them into full view for the rest
    await evalIn(win, () => { const r = document.querySelector('.view-root'); r.scrollTop += document.querySelector('.fp-toolbar').getBoundingClientRect().top - r.getBoundingClientRect().top - 12 })
    await wait(300)
    await shoot(win, 'learn-08b-flow-panes')
    // keyboard: walk down a few steps, then collapse and reopen
    await evalIn(win, () => { const t = document.querySelector('.fo-tree'); t.focus(); for (const k of ['ArrowDown', 'ArrowDown', 'ArrowDown']) t.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true })) })
    await wait(1500)
    await shoot(win, 'learn-09-flow-selected')
    await evalIn(win, () => window.__studio.instances.get('view:flow').setMode('diagram'))
    await wait(1200)
    const dg = await evalIn(win, () => ({ nodes: document.querySelectorAll('.fd-node').length, edges: document.querySelectorAll('.fd-edge').length }))
    results.diagram = dg
    if (dg.nodes < 10 || dg.edges < 9) errors.push(`flow diagram drew ${dg.nodes} nodes / ${dg.edges} edges`)
    await shoot(win, 'learn-10-flow-diagram')

    // both themes for the two visual centrepieces
    await evalIn(win, () => window.__studio.ctx.applyTheme('light'))
    await wait(600)
    await shoot(win, 'learn-11-flow-diagram-light')
    await evalIn(win, () => window.__studio.instances.get('view:flow').setMode('outline'))
    await wait(400)
    await shoot(win, 'learn-12-flow-outline-light')
    await evalIn(win, () => window.__studio.route('atlas', {}))
    await wait(900)
    await shoot(win, 'learn-13-atlas-light')
    await evalIn(win, () => window.__studio.route('overview', { fresh: true }))
    await wait(3500)
    await shoot(win, 'learn-14-overview-light')
    await evalIn(win, () => window.__studio.ctx.applyTheme('dark'))
    await wait(300)
  }
  try {
    await learnSection()
  } catch (err) {
    errors.push(`learn section failed: ${err?.message || err}`)
  }
  if (only === 'learn') {
    results.errors = errors
    results.logs = logs.filter((l) => l.level >= 2).slice(0, 40)
    results.ok = errors.length === 0
    writeFileSync(path.join(outDir, 'results.json'), JSON.stringify(results, null, 2))
    process.stdout.write(`SMOKE_RESULT ${JSON.stringify({ ok: results.ok, errorCount: errors.length, errors: errors.slice(0, 20), shots: results.shots.length })}\n`)
    await wait(300)
    app.exit(results.ok ? 0 : 1)
    return
  }

  // ---- walk every app view ----------------------------------------------
  const views = ['overview', 'atlas', 'features', 'flow', 'dashboard', 'catalog', 'search', 'source', 'graph', 'map', 'review', 'mcp', 'raw', 'history', 'settings']
  for (const v of views) {
    log(`view ${v}`)
    try {
      await evalIn(win, (name) => window.__studio.route(name, { fresh: true }), v)
      await wait(v === 'review' ? 9000 : v === 'dashboard' || v === 'map' ? 4200 : v === 'overview' || v === 'atlas' || v === 'features' ? 3000 : 1600)
      const info = await evalIn(win, () => {
        const root = document.querySelector('.view-root')
        return { nodes: root ? root.querySelectorAll('*').length : 0, text: (root?.textContent || '').slice(0, 120) }
      })
      results.views.push({ view: v, ...info })
      if (info.nodes < 10) errors.push(`view ${v} rendered only ${info.nodes} elements`)
      await shoot(win, `view-${v}`)
    } catch (err) {
      errors.push(`view ${v} failed: ${err?.message || err}`)
    }
  }

  // ---- run real features through their panels ---------------------------
  const featureCases = [
    ['status', {}],
    ['doctor', {}],
    ['coverage', { top: 10 }],
    ['hotspots', { top: 8 }],
    ['find', { query: 'Review', top: 8 }],
    ['grep', { pattern: 'blast radius', ignore_case: true, top: 8 }],
    ['semantic', { query: 'review changed files', top: 5 }],
    ['map', { top_subsystems: 12, top_hubs: 8 }],
    ['read-order', { top: 8 }],
    ['inconsistencies', {}],
    ['projects', {}],
    ['config-show', {}],
    ['annotations', {}],
    ['agent-list', {}],
    ['review', {}],
    ['explore', { query: 'index a project', seeds: 3, edges: 3 }],
  ]
  for (const [id, values] of featureCases) {
    log(`feature ${id}`)
    try {
      const r = await evalIn(
        win,
        async (fid, vals) => {
          const mod = await import('./js/features.mjs')
          const f = mod.feature(fid)
          if (!f) return { ok: false, error: `unknown feature ${fid}` }
          const runner = await import('./js/runner.mjs')
          const res = await runner.runFeature(f, { ...mod.defaultValues(f), ...vals }, { force: true, skipConfirm: true })
          return {
            ok: !!res?.ok,
            exitCode: res?.exitCode,
            code: res?.code,
            error: res?.error || '',
            keys: res?.json ? Object.keys(res.json).slice(0, 12) : [],
            bytes: (res?.stdout || '').length,
            ms: res?.ms,
          }
        },
        id,
        values,
      )
      results.features.push({ id, ...r })
      if (!r.ok) errors.push(`feature ${id} failed: exit ${r.exitCode} code=${r.code} ${r.error}`)
    } catch (err) {
      errors.push(`feature ${id} threw: ${err?.message || err}`)
    }
  }

  // ---- panel rendering: open a feature panel and let it draw --------------
  for (const [id, values, shot] of [
    ['impact', { symbol: 'Review' }, 'panel-impact'],
    ['context', { symbols: 'Review' }, 'panel-context'],
    ['file-context', { file: 'internal/app/review.go' }, 'panel-file-context'],
    ['dependencies', { file: 'internal/app/review.go' }, 'panel-dependencies'],
    ['risk', { symbol: 'Review' }, 'panel-risk'],
    ['callers', { at: 'internal/app/review.go:123' }, 'panel-callers'],
    ['traverse', { at: 'internal/app/review.go:123', depth: 2, limit: 30 }, 'panel-traverse'],
    ['refactor-plan', { symbol: 'Review' }, 'panel-refactor'],
    ['task-context', { task: 'understand the review pipeline', mode: 'understand' }, 'panel-task-context'],
    ['coverage', { top: 20, files: true }, 'panel-coverage'],
    ['annotations', {}, 'panel-annotations'],
    ['docs', { topic: 'workflow' }, 'panel-docs'],
  ]) {
    log(`panel ${id}`)
    try {
      // Mutating features ask for confirmation by design; the driver accepts
      // the modal so the panel actually runs, and a timeout keeps one hung
      // command from hanging the whole suite.
      const runPromise = evalIn(
        win,
        async (fid, vals) => {
          const mod = await import('./js/features.mjs')
          window.__studio.ctx.openFeature(fid, vals)
          const inst = window.__studio.instances.get(`feature:${fid}`)
          if (inst?.run) await inst.run({ ...mod.defaultValues(mod.feature(fid)), ...vals })
          return true
        },
        id,
        values,
      )
      const confirmPromise = evalIn(win, () =>
        new Promise((resolve) => {
          const t = setInterval(() => {
            const btns = [...document.querySelectorAll('.modal-foot button')]
            if (btns.length) {
              clearInterval(t)
              btns[btns.length - 1].click()
              resolve('confirmed')
            }
          }, 100)
          setTimeout(() => {
            clearInterval(t)
            resolve('no-modal')
          }, 5000)
        }),
      ).catch(() => 'no-modal')
      const outcome = await Promise.race([runPromise.then(() => 'done'), wait(60000).then(() => 'timeout')])
      if (outcome === 'timeout') {
        errors.push(`panel ${id} timed out after 60s`)
        continue
      }
      await confirmPromise
      await wait(2600)
      const info = await evalIn(win, () => {
        const root = document.querySelector('.view-root')
        return {
          nodes: root.querySelectorAll('*').length,
          hasErrorBox: !!root.querySelector('.error-box'),
          errorText: root.querySelector('.error-box .e-msg')?.textContent || '',
          text: (root.textContent || '').slice(0, 160),
        }
      })
      results.views.push({ view: `panel:${id}`, ...info })
      if (info.nodes < 40) errors.push(`panel ${id} rendered only ${info.nodes} elements`)
      if (info.hasErrorBox) errors.push(`panel ${id} rendered an error box: ${info.errorText}`)
      await shoot(win, shot)
    } catch (err) {
      errors.push(`panel ${id} failed: ${err?.message || err}`)
    }
  }

  log("graph explorer")
  // ---- graph explorer with real data -------------------------------------
  try {
    await evalIn(win, async () => {
      window.__studio.route('graph', { fresh: true, mode: 'traverse', at: 'internal/app/review.go:123', depth: 2, limit: 40 })
      return true
    })
    await wait(5000)
    const g = await evalIn(win, () => ({ nodes: window.__studio.state.graph.nodes?.length || 0, edges: window.__studio.state.graph.edges?.length || 0 }))
    results.graph = g
    if (g.nodes < 2) errors.push(`graph explorer drew ${g.nodes} nodes`)
    await shoot(win, 'view-graph-live')
  } catch (err) {
    errors.push(`graph explorer failed: ${err?.message || err}`)
  }

  log("source browser")
  // ---- source browser with a real file -----------------------------------
  try {
    await evalIn(win, () => {
      window.__studio.route('source', { fresh: true, file: 'internal/app/review.go', line: 123 })
      return true
    })
    await wait(2600)
    const s = await evalIn(win, () => {
      const root = document.querySelector('.view-root')
      return {
        codeLines: root.querySelectorAll('.code-line').length,
        highlighted: root.querySelectorAll('[class*="tok-"]').length,
        symbolChips: root.querySelectorAll('.symbol-strip .chip').length,
        treeRows: root.querySelectorAll('.ft-row').length,
      }
    })
    results.source = s
    if (s.codeLines < 50) errors.push(`source browser rendered ${s.codeLines} code lines`)
    if (s.highlighted < 10) errors.push(`source browser highlighted only ${s.highlighted} tokens`)
    if (s.symbolChips < 1) errors.push('source browser found no symbols for the file')
    if (s.treeRows < 20) errors.push(`file tree only rendered ${s.treeRows} rows`)
    await shoot(win, 'view-source-live')
  } catch (err) {
    errors.push(`source browser failed: ${err?.message || err}`)
  }

  log("palette")
  // ---- command palette ----------------------------------------------------
  try {
    await evalIn(win, () => {
      window.__studio.openPalette('impact')
      return true
    })
    await wait(700)
    const p = await evalIn(win, () => ({ rows: document.querySelectorAll('.palette-row').length, visible: !document.getElementById('palette').hidden }))
    results.palette = p
    if (!p.visible || p.rows < 1) errors.push(`palette did not open with results: ${JSON.stringify(p)}`)
    await shoot(win, 'palette')
    await evalIn(win, () => document.getElementById('palette').hidden = true)
  } catch (err) {
    errors.push(`palette failed: ${err?.message || err}`)
  }

  log("mcp inspector")
  // ---- MCP inspector: live handshake --------------------------------------
  try {
    await evalIn(win, () => {
      window.__studio.route('mcp', { fresh: true })
      return true
    })
    await wait(1500)
    // the real JSON-RPC path: the main process spawns `codemap serve`
    const live = await evalIn(win, async () => {
      const res = await window.studio.mcp.inspect({ profile: 'full' })
      return {
        ok: !!res.ok,
        tools: (res.tools || []).length,
        server: res.server || null,
        ms: res.ms,
        error: res.error || '',
        first: (res.tools || []).slice(0, 4).map((t) => t.name),
      }
    })
    results.mcpLive = live
    if (!live.ok) errors.push(`MCP handshake failed: ${live.error}`)
    if (live.tools < 40) errors.push(`MCP full profile registered only ${live.tools} tools`)
    // the panel's own connect button must populate the tool list
    const clicked = await evalIn(win, () => {
      const btn = [...document.querySelectorAll('.view-root button')].find((b) => (b.textContent || '').includes('Connect & list'))
      if (btn) btn.click()
      return !!btn
    })
    if (!clicked) errors.push('MCP panel has no Connect & list button')
    await wait(11000)
    const m = await evalIn(win, () => {
      const root = document.querySelector('.view-root')
      return { rows: root.querySelectorAll('.symrow').length, text: (root.textContent || '').slice(0, 200) }
    })
    results.mcp = { ...m, liveTools: live.tools, server: live.server }
    if (m.rows < 40) errors.push(`MCP inspector rendered only ${m.rows} tool rows after connecting`)
    await shoot(win, 'view-mcp-live')
  } catch (err) {
    errors.push(`MCP inspector failed: ${err?.message || err}`)
  }

  log("light theme")
  // ---- light theme --------------------------------------------------------
  try {
    await evalIn(win, () => {
      window.__studio.ctx.applyTheme('light')
      window.__studio.route('dashboard', { fresh: true })
      return true
    })
    await wait(4200)
    await shoot(win, 'theme-light')
    await evalIn(win, () => window.__studio.ctx.applyTheme('dark'))
  } catch (err) {
    errors.push(`light theme failed: ${err?.message || err}`)
  }

  results.errors = errors
  results.logs = logs.filter((l) => l.level >= 2).slice(0, 40)
  results.ok = errors.length === 0
  writeFileSync(path.join(outDir, 'results.json'), JSON.stringify(results, null, 2))
  process.stdout.write(`SMOKE_RESULT ${JSON.stringify({ ok: results.ok, errorCount: errors.length, errors: errors.slice(0, 20), shots: results.shots.length })}\n`)
  await wait(300)
  app.exit(results.ok ? 0 : 1)
})

app.on('window-all-closed', () => app.exit(1))
