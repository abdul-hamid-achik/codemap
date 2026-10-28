/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Bounded shell execution for commands Codemap Studio explicitly asks the
// user to run (today: the test commands codemap itself suggested). This is a
// main-process capability: the renderer can only start a command it displays
// verbatim, output is capped, every run has a timeout, and cancellation kills
// the whole POSIX process group where the platform supports it.

import { spawn } from 'node:child_process'

const MAX_OUTPUT = 256 * 1024
const active = new Map()
let nextID = 1

export function shellInvocation(command, platform = process.platform, env = process.env) {
  const cmd = String(command || '')
  if (platform === 'win32') {
    return {
      file: 'powershell.exe',
      args: ['-NoLogo', '-NoProfile', '-ExecutionPolicy', 'Bypass', '-Command', cmd],
    }
  }
  const shell = String(env?.SHELL || (platform === 'darwin' ? '/bin/zsh' : '/bin/bash'))
  return { file: shell, args: ['-lc', cmd] }
}

export function stripAnsi(text) {
  return String(text || '')
    .replace(/\u001b\[[0-9;?]*[ -/]*[@-~]/g, '')
    .replace(/\u001b\][^\u0007]*(?:\u0007|\u001b\\)/g, '')
    .replace(/\r(?!\n)/g, '\n')
}

function tail(text, max = MAX_OUTPUT) {
  return text.length > max ? text.slice(text.length - max) : text
}

function killTree(child, platform) {
  if (!child || child.killed) return
  if (platform !== 'win32' && child.pid) {
    try {
      process.kill(-child.pid, 'SIGTERM')
      return
    } catch {
      // The group may already be gone; fall through to the direct child.
    }
  }
  try {
    child.kill('SIGTERM')
  } catch {
    // Process already exited.
  }
}

export function activeShellRuns() {
  return [...active.values()].map(({ runId, command, cwd, startedAt }) => ({ runId, command, cwd, startedAt }))
}

export function cancelShellRun(runId) {
  const rec = active.get(runId)
  if (!rec) return false
  rec.cancelled = true
  killTree(rec.child, rec.platform)
  return true
}

export function runShellCommand({ command, cwd, env = process.env, timeoutMs = 600000, onEvent, platform = process.platform } = {}) {
  const cmd = String(command || '').trim()
  const startedAt = Date.now()
  if (!cmd) {
    return Promise.resolve({
      ok: false,
      runId: '',
      command: cmd,
      cwd: cwd || '',
      exitCode: null,
      signal: null,
      stdout: '',
      stderr: '',
      output: '',
      ms: 0,
      error: 'empty command',
      timedOut: false,
      cancelled: false,
    })
  }

  const runId = `term-${nextID++}`
  const invocation = shellInvocation(cmd, platform, env)
  return new Promise((resolve) => {
    let stdout = ''
    let stderr = ''
    let output = ''
    let timedOut = false
    let settled = false

    const child = spawn(invocation.file, invocation.args, {
      cwd: cwd || process.cwd(),
      env,
      detached: platform !== 'win32',
      windowsHide: true,
      stdio: ['ignore', 'pipe', 'pipe'],
    })
    const rec = { runId, command: cmd, cwd: cwd || '', startedAt, child, platform, cancelled: false }
    active.set(runId, rec)
    onEvent?.({ type: 'start', runId, command: cmd, cwd: rec.cwd })

    const timer = timeoutMs > 0
      ? setTimeout(() => {
          timedOut = true
          killTree(child, platform)
          setTimeout(() => {
            if (!settled) {
              try { child.kill('SIGKILL') } catch { /* already gone */ }
            }
          }, 1500).unref?.()
        }, timeoutMs)
      : null

    const push = (stream, chunk) => {
      const text = stripAnsi(chunk.toString('utf8'))
      if (!text) return
      if (stream === 'stderr') stderr = tail(stderr + text)
      else stdout = tail(stdout + text)
      output = tail(output + text)
      onEvent?.({ type: 'output', runId, stream, text })
    }
    child.stdout?.on('data', (d) => push('stdout', d))
    child.stderr?.on('data', (d) => push('stderr', d))

    const finish = (patch) => {
      if (settled) return
      settled = true
      if (timer) clearTimeout(timer)
      active.delete(runId)
      const result = {
        runId,
        command: cmd,
        cwd: rec.cwd,
        stdout,
        stderr,
        output,
        ms: Date.now() - startedAt,
        timedOut,
        cancelled: !!rec.cancelled,
        ...patch,
      }
      onEvent?.({ type: 'end', runId, ok: result.ok, exitCode: result.exitCode, timedOut, cancelled: result.cancelled })
      resolve(result)
    }

    child.on('error', (err) => {
      finish({ ok: false, exitCode: null, signal: null, error: err.message })
    })
    child.on('close', (code, signal) => {
      finish({
        ok: code === 0 && !timedOut && !rec.cancelled,
        exitCode: code,
        signal: signal || null,
        error: timedOut ? `timed out after ${timeoutMs}ms` : rec.cancelled ? 'cancelled' : undefined,
      })
    })
  })
}
