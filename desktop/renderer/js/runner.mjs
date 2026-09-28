/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Runs codemap features from the renderer: builds argv from the feature
// registry, guards mutating commands, streams long ones, records the result in
// app state, and turns a report's `next[]` suggestions into runnable values.

import { state, bus, EVENTS, setView } from './state.mjs'
import { FEATURES, feature, buildArgs, defaultValues } from './features.mjs'
import { confirmDialog, toast } from './components.mjs'

const MCP_TO_FEATURE = new Map()
for (const f of FEATURES) {
  if (f.mcp && !MCP_TO_FEATURE.has(f.mcp)) MCP_TO_FEATURE.set(f.mcp, f.id)
}
// a few MCP tools map onto a specialised entry rather than the first match
MCP_TO_FEATURE.set('codemap_context_batch', 'context-batch')
MCP_TO_FEATURE.set('codemap_index', 'index')

export function featureForTool(tool) {
  if (!tool) return null
  const id = MCP_TO_FEATURE.get(tool)
  return id ? feature(id) : null
}

/** Translate an MCP-style args object into registry values for a feature. */
export function argsToValues(feat, args = {}) {
  const values = defaultValues(feat)
  const declared = new Map((feat.args || []).map((a) => [a.name, a]))
  for (const [k, v] of Object.entries(args || {})) {
    if (k === 'path' || k === 'cwd' || v === undefined || v === null) continue
    const spec = declared.get(k) || declared.get(String(k).replace(/_/g, '-'))
    if (!spec) continue
    if (spec.kind === 'bool') values[spec.name] = !!v
    else if (spec.kind === 'repeat' || (spec.kind === 'pos' && spec.multi)) values[spec.name] = Array.isArray(v) ? v : [v]
    else if (spec.kind === 'csv') values[spec.name] = Array.isArray(v) ? v.join(',') : v
    else if (spec.kind === 'num') values[spec.name] = Number(v)
    else if (spec.kind === 'select' && typeof v === 'object') values[spec.name] = JSON.stringify(v)
    else values[spec.name] = Array.isArray(v) ? v.join(' ') : v
  }
  return values
}

export function globalArgs() {
  const s = state.settings || {}
  const extra = String(s.extraArgs || '').trim()
  return extra ? extra.split(/\s+/) : []
}

/**
 * Execute argv against the active project.
 * @returns {Promise<object>} the normalized result
 */
export async function runArgs(args, { featureId = 'raw', cwd, json = true, stream = false, timeoutMs, runKey } = {}) {
  state.running++
  bus.emit(EVENTS.RUNNING, state.running)
  const started = Date.now()
  try {
    const result = await window.studio.run({
      featureId,
      args: [...args, ...globalArgs()],
      cwd: cwd || state.project,
      json,
      stream,
      timeoutMs,
      runKey,
    })
    result.featureId = featureId
    result.startedAt = started
    state.lastResult = result
    state.results.set(featureId, result)
    bus.emit(EVENTS.RESULT, result)
    if (!result.ok && !result.gateFailed) {
      toast(result.error || `exit ${result.exitCode}`, { tone: result.code === 'not_indexed' || result.code === 'index_missing' ? 'warn' : 'error', title: describeCode(result.code) })
    } else if (result.gateFailed) {
      toast('Gate failed (exit 6) — the report itself is complete and printed above.', { tone: 'warn', title: 'Gate verdict' })
    }
    return result
  } catch (err) {
    const result = { ok: false, code: 'operational', error: err?.message || String(err), command: args, stdout: '', stderr: '', ms: Date.now() - started, featureId }
    state.lastResult = result
    bus.emit(EVENTS.RESULT, result)
    toast(result.error, { tone: 'error', title: 'Run failed' })
    return result
  } finally {
    state.running = Math.max(0, state.running - 1)
    bus.emit(EVENTS.RUNNING, state.running)
  }
}

function describeCode(code) {
  switch (code) {
    case 'binary_missing':
      return 'codemap binary not found'
    case 'index_missing':
      return 'no index yet'
    case 'not_indexed':
      return 'not indexed'
    case 'index_corrupt':
      return 'index corrupt'
    case 'not_a_repo':
      return 'not a git repository'
    case 'not_found':
      return 'not found'
    case 'gate_failed':
      return 'gate failed'
    default:
      return 'command failed'
  }
}

/**
 * Run a registry feature with its form values.
 * Mutating features ask for confirmation first (unless opts.force).
 */
export async function runFeature(feat, values, opts = {}) {
  if (!feat) return null
  if (feat.mutating && !opts.force && !opts.skipConfirm) {
    const ok = await confirmDialog({
      title: `${feat.title} — this changes state`,
      message: feat.confirm || 'This command writes to the index, the cache, or files on disk.',
      confirmLabel: opts.confirmLabel || 'Run it',
      tone: 'danger',
    })
    if (!ok) return null
  }
  const args = buildArgs(feat, values)
  const missing = (feat.args || []).filter((a) => a.required && !String(values[a.name] ?? '').trim() && !(Array.isArray(values[a.name]) && values[a.name].length))
  if (missing.length) {
    toast(`Fill in: ${missing.map((m) => m.label).join(', ')}`, { tone: 'warn', title: 'Missing input' })
    return null
  }
  if (feat.mode === 'daemon') return launchDaemon(feat, values)
  return runArgs([...(feat.cmd || []), ...args], {
    featureId: feat.id,
    json: feat.json !== false,
    stream: !!feat.stream,
    cwd: opts.cwd,
    timeoutMs: opts.timeoutMs,
    runKey: opts.runKey,
  })
}

async function launchDaemon(feat, values) {
  const args = buildArgs(feat, values)
  const r = await window.studio.daemon.launch(state.project, args)
  if (r?.ok) toast(`daemon started (pid ${r.pid}) — output streams into the log below`, { tone: 'ok', title: 'Daemon' })
  else toast(r?.error || 'could not start the daemon', { tone: 'error', title: 'Daemon' })
  return { ok: !!r?.ok, json: r, stdout: '', stderr: '', command: ['codemap', ...feat.cmd, ...args], exitCode: 0, ms: 0, featureId: feat.id, daemon: true }
}

export function stopDaemon() {
  return window.studio.daemon.kill()
}

/** Follow a `next[]` suggestion: prefill the target feature and run it. */
export async function followNext(suggestion) {
  const feat = featureForTool(suggestion?.tool)
  if (!feat) {
    toast(`No panel for ${suggestion?.tool} — opening the raw runner with equivalent argv.`, { tone: 'warn' })
    return openRawFromTool(suggestion)
  }
  const values = argsToValues(feat, suggestion.args)
  setView('feature', { featureId: feat.id, values, autorun: true })
  return { feat, values }
}

function openRawFromTool(suggestion) {
  const tool = String(suggestion?.tool || '').replace(/^codemap_/, '').replace(/_/g, '-')
  const args = Object.entries(suggestion?.args || {})
    .filter(([k]) => k !== 'path')
    .map(([k, v]) => (typeof v === 'boolean' ? (v ? `--${k}` : '') : `--${k.replace(/_/g, '-')} ${typeof v === 'object' ? JSON.stringify(v) : v}`))
    .filter(Boolean)
    .join(' ')
  setView('raw', { text: `${tool} ${args}`.trim() })
}

/** Refresh the always-on context: status + git + projects. */
export async function refreshContext({ silent = true } = {}) {
  const cwd = state.project
  if (!cwd) return
  const [statusRes, git] = await Promise.all([
    runArgs(['status'], { featureId: '__status', cwd, json: true, stream: false }).catch(() => null),
    window.studio.git(cwd).catch(() => null),
  ])
  state.status = statusRes?.ok ? statusRes.json : null
  state.statusError = statusRes?.ok ? null : statusRes
  state.git = git
  bus.emit(EVENTS.STATUS, { status: state.status, git, error: state.statusError })
  if (!silent) toast('Context refreshed', { tone: 'ok' })
}

export async function loadProjects() {
  const r = await window.studio.run({ featureId: '__projects', args: ['projects', '--json'], cwd: state.project || undefined, json: true, record: false })
  if (r?.ok && Array.isArray(r.json?.projects)) {
    state.projects = r.json.projects
    bus.emit(EVENTS.PROJECT, state.project)
  }
  return state.projects
}
