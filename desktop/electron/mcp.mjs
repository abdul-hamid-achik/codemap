/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Minimal newline-delimited JSON-RPC 2.0 client for `codemap serve`.
// MCP stdio is newline-delimited (NOT Content-Length framed) — this is the one
// transport detail that decides whether a server connects at all.

import { spawn } from 'node:child_process'

export async function mcpSession({ binary, args = [], cwd, env, timeoutMs = 30000 }) {
  const child = spawn(binary, args, { cwd, env, stdio: ['pipe', 'pipe', 'pipe'] })
  let buffer = ''
  let stderr = ''
  const pending = new Map()
  let nextId = 1
  let closed = false

  child.stderr.on('data', (d) => {
    stderr += d.toString('utf8').slice(0, 20000)
  })
  child.stdout.on('data', (d) => {
    buffer += d.toString('utf8')
    let idx
    while ((idx = buffer.indexOf('\n')) >= 0) {
      const line = buffer.slice(0, idx).trim()
      buffer = buffer.slice(idx + 1)
      if (!line) continue
      let msg
      try {
        msg = JSON.parse(line)
      } catch {
        continue
      }
      if (msg.id !== undefined && pending.has(msg.id)) {
        const { resolve, timer } = pending.get(msg.id)
        clearTimeout(timer)
        pending.delete(msg.id)
        resolve(msg)
      }
    }
  })
  child.on('close', () => {
    closed = true
    for (const [id, { resolve, timer }] of pending) {
      clearTimeout(timer)
      resolve({ id, error: { code: -32000, message: `server closed: ${stderr.slice(0, 400)}` } })
    }
    pending.clear()
  })

  const request = (method, params) =>
    new Promise((resolve, reject) => {
      if (closed) {
        reject(new Error('server closed'))
        return
      }
      const id = nextId++
      const payload = `${JSON.stringify({ jsonrpc: '2.0', id, method, params })}\n`
      const timer = setTimeout(() => {
        pending.delete(id)
        reject(new Error(`${method} timed out after ${timeoutMs}ms`))
      }, timeoutMs)
      pending.set(id, { resolve, timer })
      child.stdin.write(payload)
    })

  const notify = (method, params) => {
    if (!closed) child.stdin.write(`${JSON.stringify({ jsonrpc: '2.0', method, params })}\n`)
  }

  const close = () => {
    if (!closed) {
      try {
        child.stdin.end()
      } catch {
        /* already gone */
      }
      child.kill('SIGTERM')
    }
  }

  return { request, notify, close, pid: child.pid, get stderr() { return stderr } }
}

/**
 * Full inspection round: initialize → initialized → tools/list, optionally
 * followed by one tools/call. Always closes the server afterwards.
 */
export async function inspectServer({ binary, profile = 'full', cwd, env, call = null, timeoutMs = 30000 }) {
  const args = ['serve', '--profile', profile]
  const s = await mcpSession({ binary, args, cwd, env, timeoutMs })
  const started = Date.now()
  try {
    const init = await s.request('initialize', {
      protocolVersion: '2025-06-18',
      capabilities: {},
      clientInfo: { name: 'codemap-studio', version: '0.1.0' },
    })
    if (init.error) throw new Error(init.error.message || 'initialize failed')
    s.notify('notifications/initialized', {})
    const list = await s.request('tools/list', {})
    if (list.error) throw new Error(list.error.message || 'tools/list failed')
    let callResult = null
    if (call?.name) {
      callResult = await s.request('tools/call', { name: call.name, arguments: call.arguments || {} })
    }
    return {
      ok: true,
      ms: Date.now() - started,
      profile,
      command: ['codemap', ...args],
      server: init.result?.serverInfo || null,
      protocol: init.result?.protocolVersion || null,
      instructions: init.result?.instructions || '',
      capabilities: init.result?.capabilities || {},
      tools: list.result?.tools || [],
      call: callResult ? { name: call.name, arguments: call.arguments || {}, result: callResult.result ?? null, error: callResult.error ?? null } : null,
      stderr: s.stderr,
    }
  } catch (err) {
    return { ok: false, ms: Date.now() - started, profile, command: ['codemap', ...args], error: err.message, stderr: s.stderr }
  } finally {
    s.close()
  }
}
