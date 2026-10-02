/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// The main-process runtime: binary + environment resolution and every IPC
// handler the renderer may call. Extracted from main.mjs so the smoke driver
// boots the exact same surface as the shipped app — no parallel stub to drift.

import { app, ipcMain, dialog, shell, nativeTheme } from 'electron'
import { spawn, execFile } from 'node:child_process'
import { readFileSync, statSync, readdirSync, writeFileSync, existsSync } from 'node:fs'
import path from 'node:path'

import { runCodemap, resolveBinary, shellEnv, cancelRun, activeRuns } from './cli.mjs'
import { inspectServer } from './mcp.mjs'

function runGit(args, cwd, timeoutMs = 15000) {
  return new Promise((resolve) => {
    execFile('git', args, { cwd, timeout: timeoutMs, maxBuffer: 8 * 1024 * 1024 }, (err, stdout, stderr) => {
      resolve({ ok: !err, stdout: stdout || '', stderr: stderr || '', code: err ? err.code || 1 : 0 })
    })
  })
}

function walk(root, dir, budget, out = []) {
  if (out.length >= budget) return out
  let entries = []
  try {
    entries = readdirSync(dir, { withFileTypes: true })
  } catch {
    return out
  }
  for (const e of entries) {
    if (out.length >= budget) break
    if (e.name === '.git' || e.name === 'node_modules' || e.name === 'dist' || e.name === '.glyphrun') continue
    const full = path.join(dir, e.name)
    if (e.isDirectory()) walk(root, full, budget, out)
    else if (e.isFile()) out.push(path.relative(root, full))
  }
  return out
}

/**
 * @param {object} o
 * @param {string} o.appRoot      the desktop/ directory
 * @param {import('./settings.mjs').Settings} o.settings
 * @param {() => import('electron').BrowserWindow|null} o.getWin
 * @param {boolean} [o.isDev]
 */
export function createRuntime({ appRoot, settings, getWin, isDev = false }) {
  let resolved = { binary: null, source: 'none' }
  let envCache = null
  let daemonProc = null
  // runKey (the caller's handle, e.g. a feature id) → live runId, so a Stop button can
  // cancel a run it never saw start (non-streaming runs emit no events).
  const runIdsByKey = new Map()

  async function env() {
    if (!envCache) envCache = await shellEnv()
    return envCache
  }

  async function binary() {
    const e = await env()
    const want = settings?.data?.binaryPath || ''
    if (resolved.binary && (!want || resolved.binary === want)) return resolved.binary
    const r = await resolveBinary({ appRoot, explicit: want, env: e })
    resolved = r
    if (!r.binary) throw Object.assign(new Error('codemap binary not found'), { coded: 'binary_missing', tried: r.tried })
    return r.binary
  }

  function send(channel, payload) {
    const win = getWin()
    if (win && !win.isDestroyed()) win.webContents.send(channel, payload)
  }

  function projectDir(reqPath) {
    return path.resolve(reqPath || settings?.data?.projectPath || process.cwd())
  }

  function stopDaemon() {
    if (daemonProc) {
      daemonProc.child.kill('SIGTERM')
      daemonProc = null
    }
  }

  function registerIPC() {
    ipcMain.handle('app:info', async () => {
      const e = await env()
      return {
        version: app.getVersion(),
        electron: process.versions.electron,
        node: process.versions.node,
        chrome: process.versions.chrome,
        platform: `${process.platform} ${process.arch}`,
        appRoot,
        userData: app.getPath('userData'),
        env: {
          PATH: e.PATH,
          CODEMAP_DATA: e.CODEMAP_DATA || '',
          CODEMAP_CONFIG: e.CODEMAP_CONFIG || '',
          CODEMAP_SEMANTIC_BACKEND: e.CODEMAP_SEMANTIC_BACKEND || '',
        },
        binary: resolved,
        dev: isDev,
      }
    })

    ipcMain.handle('app:quit', () => {
      app.quit()
    })

    ipcMain.handle('settings:get', () => settings.get())
    ipcMain.handle('settings:all', () => settings.all())
    ipcMain.handle('settings:set', (_e, patch) => settings.update(patch))
    ipcMain.handle('settings:history', () => settings.history())
    ipcMain.handle('settings:clearHistory', () => settings.clearHistory())

    ipcMain.handle('binary:resolve', async (_e, explicit) => {
      const e = await env()
      const r = await resolveBinary({ appRoot, explicit: explicit || settings.data.binaryPath, env: e })
      resolved = r
      return r
    })

    ipcMain.handle('binary:version', async () => {
      try {
        const bin = await binary()
        // Not appRoot: packaged, it is <resources>/app.asar — a file, so spawn
        // fails with ENOTDIR and a working binary reads as "not found".
        const r = await runCodemap({ binary: bin, args: ['version'], json: false, cwd: app.getPath('home'), env: await env(), timeoutMs: 20000 })
        return { ok: r.ok, binary: bin, version: r.stdout.trim(), error: r.error }
      } catch (err) {
        return { ok: false, binary: null, error: err.message, tried: err.tried }
      }
    })

    // The one door every feature goes through.
    ipcMain.handle('codemap:run', async (_event, req) => {
      const { featureId, args = [], cwd, json = true, timeoutMs, stream = false, record = true } = req || {}
      let bin
      try {
        bin = await binary()
      } catch (err) {
        return {
          ok: false,
          code: err.coded || 'operational',
          error: err.message,
          hint: 'point Codemap Studio at your codemap binary in Settings',
          tried: err.tried,
          command: [],
          stdout: '',
          stderr: '',
          ms: 0,
        }
      }
      const dir = projectDir(cwd)
      const long = featureId === 'index' || featureId === 'index-precise'
      const limit = timeoutMs ?? (long ? settings.data.indexTimeoutMs : settings.data.timeoutMs)
      const result = await runCodemap({
        binary: bin,
        args,
        json,
        cwd: dir,
        env: await env(),
        timeoutMs: limit,
        onEvent: (evt) => {
          if (evt.type === 'start' && req?.runKey) runIdsByKey.set(req.runKey, evt.runId)
          if (stream) send('codemap:stream', { ...evt, featureId, runKey: req?.runKey })
        },
      })
      if (req?.runKey) runIdsByKey.delete(req.runKey)
      result.cwd = dir
      result.featureId = featureId
      if (record) {
        settings.addHistory({
          featureId,
          args,
          cwd: dir,
          ok: result.ok,
          gateFailed: !!result.gateFailed,
          exitCode: result.exitCode,
          code: result.code,
          ms: result.ms,
          bytes: (result.stdout || '').length,
        })
      }
      return result
    })

    ipcMain.handle('codemap:cancel', (_e, idOrKey) => cancelRun(typeof idOrKey === 'string' ? runIdsByKey.get(idOrKey) : idOrKey))
    ipcMain.handle('codemap:active', () => activeRuns())

    ipcMain.handle('project:pick', async () => {
      const r = await dialog.showOpenDialog(getWin(), {
        title: 'Choose a project to review',
        properties: ['openDirectory', 'createDirectory'],
        defaultPath: settings.data.projectPath || app.getPath('home'),
      })
      if (r.canceled || !r.filePaths.length) return null
      const p = r.filePaths[0]
      settings.rememberProject(p)
      return p
    })

    ipcMain.handle('project:use', (_e, p) => {
      if (!p) return null
      const abs = path.resolve(p)
      if (!existsSync(abs)) return { error: 'directory does not exist' }
      settings.rememberProject(abs)
      return abs
    })

    ipcMain.handle('project:recent', () => settings.data.recentProjects)

    ipcMain.handle('project:tree', async (_e, req) => {
      const root = projectDir(req?.cwd)
      const git = await runGit(['ls-files', '--cached', '--others', '--exclude-standard'], root)
      const files = git.ok && git.stdout.trim() ? git.stdout.split('\n').filter(Boolean) : walk(root, root, 4000)
      files.sort()
      return { root, isGit: git.ok, count: files.length, files: files.slice(0, 20000) }
    })

    ipcMain.handle('fs:read', async (_e, req) => {
      const root = projectDir(req?.cwd)
      const rel = String(req?.path || '')
      const abs = path.resolve(root, rel)
      if (!abs.startsWith(root)) return { ok: false, error: 'path escapes the project root' }
      if (!existsSync(abs)) return { ok: false, error: 'file not found' }
      const st = statSync(abs)
      if (!st.isFile()) return { ok: false, error: 'not a regular file' }
      const max = 4 * 1024 * 1024
      if (st.size > max) {
        const buf = Buffer.alloc(max)
        readFileSync(abs).copy(buf, 0, 0, max)
        return { ok: true, path: rel, abs, size: st.size, truncated: true, text: buf.toString('utf8'), mtime: st.mtimeMs }
      }
      return { ok: true, path: rel, abs, size: st.size, truncated: false, text: readFileSync(abs, 'utf8'), mtime: st.mtimeMs }
    })

    ipcMain.handle('fs:write', (_e, req) => {
      const target = String(req?.abs || '')
      if (!target) return { ok: false, error: 'no target path' }
      try {
        writeFileSync(target, String(req?.text ?? ''))
        return { ok: true, abs: target }
      } catch (err) {
        return { ok: false, error: err.message }
      }
    })

    ipcMain.handle('dialog:save', async (_e, req) => {
      const r = await dialog.showSaveDialog(getWin(), {
        title: req?.title || 'Save',
        defaultPath: req?.defaultPath || 'codemap-report.json',
        filters: req?.filters || [{ name: 'JSON', extensions: ['json'] }],
      })
      if (r.canceled || !r.filePath) return { ok: false, canceled: true }
      try {
        writeFileSync(r.filePath, String(req?.text ?? ''))
        return { ok: true, path: r.filePath }
      } catch (err) {
        return { ok: false, error: err.message }
      }
    })

    ipcMain.handle('shell:reveal', (_e, abs) => {
      if (abs && existsSync(abs)) shell.showItemInFolder(abs)
      return true
    })

    ipcMain.handle('shell:open', (_e, target) => {
      if (/^https?:/.test(String(target))) return shell.openExternal(String(target))
      if (target && existsSync(target)) return shell.openPath(String(target))
      return Promise.resolve(false)
    })

    ipcMain.handle('git:info', async (_e, req) => {
      const root = projectDir(req?.cwd)
      const [branch, status, log, branches] = await Promise.all([
        runGit(['rev-parse', '--abbrev-ref', 'HEAD'], root),
        runGit(['status', '--porcelain'], root),
        runGit(['log', '-n', '12', '--pretty=%H%x09%h%x09%ad%x09%s', '--date=short'], root),
        runGit(['for-each-ref', '--sort=-committerdate', '--format=%(refname:short)%09%(committerdate:short)', 'refs/heads'], root),
      ])
      const changed = status.ok ? status.stdout.split('\n').filter(Boolean) : []
      return {
        root,
        isRepo: branch.ok,
        branch: branch.ok ? branch.stdout.trim() : '',
        changedCount: changed.length,
        changed: changed.slice(0, 500),
        commits: log.ok
          ? log.stdout
              .split('\n')
              .filter(Boolean)
              .map((l) => {
                const [sha, short, date, ...rest] = l.split('\t')
                return { sha, short, date, subject: rest.join('\t') }
              })
          : [],
        branches: branches.ok
          ? branches.stdout
              .split('\n')
              .filter(Boolean)
              .map((l) => {
                const [name, date] = l.split('\t')
                return { name, date }
              })
          : [],
      }
    })

    // Real `git diff` text for the review desk — codemap classifies the diff,
    // this shows it.
    ipcMain.handle('git:diff', async (_e, req) => {
      const root = projectDir(req?.cwd)
      const mode = req?.staged ? 'staged' : req?.since ? 'since' : 'working'
      const args = ['diff', '--no-color', '--no-ext-diff', '-U3']
      if (mode === 'staged') args.push('--staged')
      else if (mode === 'since') args.push(String(req.since))
      const r = await runGit(args, root, 30000)
      if (!r.ok) return { ok: false, error: r.stderr || 'git diff failed', mode }
      let untracked = []
      if (mode === 'working') {
        const u = await runGit(['ls-files', '--others', '--exclude-standard'], root)
        untracked = u.ok ? u.stdout.split('\n').filter(Boolean).slice(0, 200) : []
      }
      return { ok: true, mode, diff: r.stdout, bytes: r.stdout.length, untracked }
    })

    // A long-lived child process the app owns: `codemap daemon start`.
    ipcMain.handle('daemon:launch', async (_e, req) => {
      const bin = await binary()
      const dir = projectDir(req?.cwd)
      const args = ['daemon', 'start', ...(req?.args || [])]
      const child = spawn(bin, args, { cwd: dir, env: await env(), stdio: ['ignore', 'pipe', 'pipe'] })
      const runId = `daemon-${child.pid}`
      daemonProc = { child, runId }
      const push = (streamName) => (d) => {
        for (const line of d.toString('utf8').split('\n')) {
          if (line.trim()) send('codemap:stream', { type: 'line', runId, stream: streamName, text: line })
        }
      }
      child.stdout.on('data', push('out'))
      child.stderr.on('data', push('err'))
      child.on('close', (code) => {
        send('codemap:stream', { type: 'end', runId, exitCode: code, ok: code === 0 })
        if (daemonProc?.runId === runId) daemonProc = null
      })
      return { ok: true, runId, pid: child.pid, command: [bin, ...args] }
    })

    ipcMain.handle('daemon:kill', () => {
      if (!daemonProc) return { ok: false, error: 'no daemon launched from this app' }
      const id = daemonProc.runId
      stopDaemon()
      return { ok: true, runId: id }
    })

    // Live MCP inspection: spawn `codemap serve`, handshake, list tools,
    // optionally call one, then close.
    ipcMain.handle('mcp:inspect', async (_e, req) => {
      let bin
      try {
        bin = await binary()
      } catch (err) {
        return { ok: false, error: err.message }
      }
      return inspectServer({
        binary: bin,
        profile: req?.profile || 'full',
        cwd: projectDir(req?.cwd),
        env: await env(),
        call: req?.call || null,
        timeoutMs: req?.timeoutMs || 60000,
      })
    })

    ipcMain.handle('theme:set', (_e, theme) => {
      nativeTheme.themeSource = theme === 'light' ? 'light' : 'dark'
      return nativeTheme.shouldUseDarkColors
    })
  }

  return { registerIPC, env, binary, projectDir, send, stopDaemon, resolved: () => resolved }
}
