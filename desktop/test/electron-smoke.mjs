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

  // ---- walk every app view ----------------------------------------------
  const views = ['dashboard', 'catalog', 'search', 'source', 'graph', 'map', 'review', 'mcp', 'raw', 'history', 'settings']
  for (const v of views) {
    log(`view ${v}`)
    try {
      await evalIn(win, (name) => window.__studio.route(name, { fresh: true }), v)
      await wait(v === 'dashboard' || v === 'map' || v === 'review' ? 4200 : 1600)
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
