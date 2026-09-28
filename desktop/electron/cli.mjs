/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Codemap CLI bridge: spawns `codemap --json`, maps the CLI's exit-code
// taxonomy onto a structured result, streams long-running output, and lets a
// caller cancel. This is the only place in the app that touches a subprocess.

import { spawn } from 'node:child_process'
import { existsSync } from 'node:fs'
import { homedir } from 'node:os'
import path from 'node:path'

export const EXIT_MEANING = {
  0: 'answered',
  1: 'operational',
  2: 'not_found',
  3: 'index_missing',
  4: 'index_corrupt',
  5: 'not_a_repo',
  6: 'gate_failed',
}

const MAX_CAPTURE = 4 * 1024 * 1024

function truncate(text) {
  if (text.length <= MAX_CAPTURE) return text
  return `${text.slice(0, MAX_CAPTURE)}\n… [truncated ${text.length - MAX_CAPTURE} bytes]`
}

// ---------------------------------------------------------------------------
// binary + environment resolution
// ---------------------------------------------------------------------------

// A GUI-launched Electron app on macOS does not inherit the user's shell PATH,
// so neither `codemap` nor the language servers it spawns would be found.
// Resolve a login-shell PATH once and hand it to every child process.
let cachedEnv = null

export async function shellEnv(timeoutMs = 6000) {
  if (cachedEnv) return cachedEnv
  const shell = process.env.SHELL || '/bin/zsh'
  const extra = [
    '/opt/homebrew/bin',
    '/opt/homebrew/sbin',
    '/usr/local/bin',
    path.join(homedir(), 'go/bin'),
    path.join(homedir(), '.asdf/shims'),
    path.join(homedir(), '.local/bin'),
    path.join(homedir(), '.bun/bin'),
  ].filter((p) => existsSync(p))

  let shellPath = ''
  try {
    shellPath = await new Promise((resolve) => {
      const child = spawn(shell, ['-lc', 'printf %s "$PATH"'], {
        env: process.env,
        stdio: ['ignore', 'pipe', 'ignore'],
      })
      let out = ''
      child.stdout.on('data', (d) => {
        out += d
      })
      const timer = setTimeout(() => child.kill('SIGKILL'), timeoutMs)
      child.on('error', () => {
        clearTimeout(timer)
        resolve('')
      })
      child.on('close', () => {
        clearTimeout(timer)
        resolve(out.trim())
      })
    })
  } catch {
    shellPath = ''
  }

  const parts = [...(shellPath ? shellPath.split(path.delimiter) : []), ...(process.env.PATH || '').split(path.delimiter), ...extra]
  const seen = new Set()
  const merged = []
  for (const p of parts) {
    if (!p || seen.has(p)) continue
    seen.add(p)
    merged.push(p)
  }
  cachedEnv = { ...process.env, PATH: merged.join(path.delimiter) }
  return cachedEnv
}

export function candidateBinaries(appRoot, explicit) {
  const names = process.platform === 'win32' ? ['codemap.exe'] : ['codemap']
  const list = []
  if (explicit) list.push(explicit)
  if (process.env.CODEMAP_BIN) list.push(process.env.CODEMAP_BIN)
  for (const n of names) {
    // dev checkout: <repo>/bin/codemap and <repo>/codemap
    list.push(path.join(appRoot, '..', 'bin', n))
    list.push(path.join(appRoot, '..', n))
    list.push(path.join(appRoot, 'bin', n))
  }
  const out = []
  for (const c of list) {
    if (c && !out.includes(c)) out.push(c)
  }
  return out
}

export async function resolveBinary({ appRoot, explicit, env }) {
  const candidates = candidateBinaries(appRoot, explicit)
  for (const c of candidates) {
    const abs = path.isAbsolute(c) ? c : path.resolve(appRoot, c)
    if (existsSync(abs)) return { binary: abs, source: 'path' }
  }
  // Fall back to a PATH lookup through the login shell environment.
  const finder = process.platform === 'win32' ? 'where' : 'command -v'
  try {
    const found = await new Promise((resolve) => {
      const child = spawn(process.env.SHELL || '/bin/sh', ['-lc', `${finder} codemap`], { env, stdio: ['ignore', 'pipe', 'ignore'] })
      let out = ''
      child.stdout.on('data', (d) => {
        out += d
      })
      child.on('error', () => resolve(''))
      child.on('close', () => resolve(out.trim().split('\n')[0] || ''))
    })
    if (found && existsSync(found)) return { binary: found, source: 'shell' }
  } catch {
    /* fall through */
  }
  return { binary: null, source: 'none', tried: candidates }
}

// ---------------------------------------------------------------------------
// running commands
// ---------------------------------------------------------------------------

const running = new Map()
let nextRunId = 1

/**
 * Run one codemap invocation.
 *
 * @param {object} opts
 * @param {string} opts.binary      absolute path to the codemap binary
 * @param {string[]} opts.args      argv after the binary (no `--json` needed)
 * @param {boolean} [opts.json]     append `--json` (default true)
 * @param {string} [opts.cwd]       working directory (usually the project root)
 * @param {number} [opts.timeoutMs] kill the process after this long (0 = never)
 * @param {(evt:{type:string,text?:string,done?:number,total?:number,file?:string})=>void} [opts.onEvent]
 * @returns {Promise<object>}
 */
export async function runCodemap({ binary, args, json = true, cwd, env, timeoutMs = 0, onEvent }) {
  const argv = [...args]
  if (json && !argv.includes('--json')) argv.push('--json')
  const runId = nextRunId++
  const started = Date.now()

  return new Promise((resolve) => {
    let child
    try {
      child = spawn(binary, argv, {
        cwd: cwd || process.cwd(),
        env: env || process.env,
        stdio: ['ignore', 'pipe', 'pipe'],
      })
    } catch (err) {
      resolve({
        ok: false,
        exitCode: -1,
        code: 'operational',
        error: `failed to spawn codemap: ${err.message}`,
        hint: 'set the codemap binary path in Settings',
        command: [binary, ...argv],
        stdout: '',
        stderr: '',
        ms: 0,
        runId,
      })
      return
    }

    let stdout = ''
    let stderr = ''
    let settled = false
    let timer = null

    const emit = (evt) => {
      if (onEvent) {
        try {
          onEvent(evt)
        } catch {
          /* a broken listener must not kill the run */
        }
      }
    }

    running.set(runId, child)
    emit({ type: 'start', runId, command: [binary, ...argv] })

    const feed = (chunk, sink) => {
      const text = chunk.toString('utf8')
      sink === 'out' ? (stdout += text) : (stderr += text)
      for (const line of text.split('\n')) {
        if (!line.trim()) continue
        emit({ type: 'line', runId, stream: sink, text: line })
        const progress = parseProgress(line)
        if (progress) emit({ type: 'progress', runId, ...progress })
      }
    }

    child.stdout.on('data', (d) => feed(d, 'out'))
    child.stderr.on('data', (d) => feed(d, 'err'))

    const finish = (result) => {
      if (settled) return
      settled = true
      if (timer) clearTimeout(timer)
      running.delete(runId)
      emit({ type: 'end', runId, ok: result.ok, exitCode: result.exitCode })
      resolve(result)
    }

    if (timeoutMs > 0) {
      timer = setTimeout(() => {
        child.kill('SIGKILL')
        finish({
          ok: false,
          exitCode: -1,
          code: 'operational',
          error: `codemap timed out after ${Math.round(timeoutMs / 1000)}s`,
          hint: 'the command was killed; try a narrower query or a longer timeout in Settings',
          command: [binary, ...argv],
          stdout: truncate(stdout),
          stderr: truncate(stderr),
          ms: Date.now() - started,
          runId,
          timedOut: true,
        })
      }, timeoutMs)
    }

    child.on('error', (err) => {
      finish({
        ok: false,
        exitCode: -1,
        code: 'operational',
        error: err.message,
        hint: 'is the codemap binary executable?',
        command: [binary, ...argv],
        stdout: truncate(stdout),
        stderr: truncate(stderr),
        ms: Date.now() - started,
        runId,
      })
    })

    child.on('close', (exitCode, signal) => {
      const code = exitCode === null ? 'operational' : EXIT_MEANING[exitCode] || 'operational'
      let parsed = null
      let parseError = null
      const trimmed = stdout.trim()
      if (json && trimmed) {
        try {
          parsed = JSON.parse(trimmed)
        } catch (err) {
          // Some commands print human lines before the JSON (e.g. daemon notices);
          // retry on the last balanced JSON document in the stream.
          const recovered = recoverJSON(trimmed)
          if (recovered !== undefined) parsed = recovered
          else parseError = err.message
        }
      }

      // codemap's --json failure envelope: {ok:false,error,code,hint}
      if (parsed && parsed.ok === false) {
        finish({
          ok: false,
          exitCode: exitCode ?? -1,
          signal,
          code: parsed.code || code,
          error: parsed.error || 'command failed',
          hint: parsed.hint || '',
          json: parsed,
          command: [binary, ...argv],
          stdout: truncate(stdout),
          stderr: truncate(stderr),
          ms: Date.now() - started,
          runId,
        })
        return
      }

      const ok = exitCode === 0 && !parseError
      finish({
        ok,
        // exit 6 is a deliberate gate verdict on an otherwise complete report
        gateFailed: exitCode === 6,
        exitCode: exitCode ?? -1,
        signal,
        code: ok ? 'answered' : code,
        json: parsed,
        parseError,
        command: [binary, ...argv],
        stdout: truncate(stdout),
        stderr: truncate(stderr),
        ms: Date.now() - started,
        runId,
      })
    })
  })
}

export function cancelRun(runId) {
  const child = running.get(runId)
  if (!child) return false
  child.kill('SIGTERM')
  setTimeout(() => {
    if (running.has(runId)) child.kill('SIGKILL')
  }, 2000)
  return true
}

export function activeRuns() {
  return [...running.keys()]
}

// `index` prints "files: 12 scanned" style lines and, when a language server
// degraded, a DEGRADED banner. Surface any "N/M" or "N of M" pair as progress.
export function parseProgress(line) {
  const m = line.match(/(\d+)\s*\/\s*(\d+)/)
  if (m) {
    const done = Number(m[1])
    const total = Number(m[2])
    if (total > 0 && done <= total) return { done, total }
  }
  const pct = line.match(/(\d{1,3})%/)
  if (pct) return { done: Number(pct[1]), total: 100 }
  return null
}

export function recoverJSON(text) {
  // Some commands print human lines before the JSON (daemon notices, warnings)
  // and those lines can themselves contain braces. Scan every top-level
  // balanced block and keep the LAST one that parses — the report is always
  // the final document on stdout.
  let best
  for (let start = text.indexOf('{'); start !== -1; start = text.indexOf('{', start + 1)) {
    let depth = 0
    let inString = false
    let escaped = false
    for (let i = start; i < text.length; i++) {
      const ch = text[i]
      if (inString) {
        if (escaped) escaped = false
        else if (ch === '\\') escaped = true
        else if (ch === '"') inString = false
        continue
      }
      if (ch === '"') inString = true
      else if (ch === '{') depth++
      else if (ch === '}') {
        depth--
        if (depth === 0) {
          try {
            best = JSON.parse(text.slice(start, i + 1))
          } catch {
            /* not a document; keep scanning */
          }
          break
        }
      }
    }
  }
  return best
}
