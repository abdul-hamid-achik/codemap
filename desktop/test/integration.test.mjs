/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Live end-to-end proof: every read-only feature in the registry is executed
// against the real codemap binary on this repository, and must answer — either
// a report (exit 0) or codemap's own structured envelope (exit 2/3/5). Anything
// else (exit 1 operational, exit 4 corrupt, spawn failure, unparseable output)
// means the app surfaced something the CLI could not answer.
//
// Mutating and long-running features are exercised by test/features.test.mjs
// (their argv is asserted against the binary's own --help) instead of executed.

import test from 'node:test'
import assert from 'node:assert/strict'
import { existsSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

import { runCodemap } from '../electron/cli.mjs'
import { feature } from '../renderer/js/features.mjs'

const appRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const repo = path.resolve(appRoot, '..')
const bin = [path.join(repo, 'bin', 'codemap'), path.join(repo, 'codemap')].find((p) => existsSync(p)) || null

const HONEST_FAILURES = new Set([2, 3, 5]) // not found / index missing / not a repo
const DEGRADABLE = new Set(['semantic', 'explore', 'task-context']) // depend on an embedding owner

async function run(args, json = true) {
  return runCodemap({ binary: bin, args, json, cwd: repo, timeoutMs: 120000 })
}

/** Discover real inputs so the test adapts to whatever this repo indexes. */
async function probe() {
  const status = await run(['status'])
  assert.equal(status.ok, true, `codemap status failed: ${status.error || status.stderr}`)
  const exp = await run(['export-symbols', '--limit', '1'])
  assert.equal(exp.ok, true, `codemap export-symbols failed: ${exp.error || exp.stderr}`)
  const rec = exp.json.records[0]
  const symbols = await run(['symbols', rec.file])
  const syms = symbols.json?.symbols || []
  const callable = syms.find((s) => ['function', 'method', 'type', 'class', 'test'].includes(s.kind)) || syms[0] || rec
  const readOrder = await run(['read-order', '--top', '3'])
  const entry = readOrder.json?.entries?.[0]
  const hot = await run(['hotspots', '--top', '3'])
  const hub = hot.json?.hotspots?.[0]
  return {
    status: status.json,
    file: rec.file,
    line: callable.start_line || rec.start_line || 1,
    pos: `${rec.file}:${callable.start_line || rec.start_line || 1}`,
    symbol: callable.symbol || callable.fqn,
    fqn: callable.fqn || callable.symbol,
    otherFqn: hub?.fqn || entry?.fqn || callable.fqn,
    entrypoint: entry?.fqn || entry?.symbol || hub?.fqn || callable.fqn,
  }
}

test('every read-only feature answers against the live binary', { skip: !bin && 'no codemap binary in the repository', timeout: 600000 }, async () => {
  const p = await probe()
  const cases = [
    ['status', ['status']],
    ['doctor', ['doctor']],
    ['projects', ['projects']],
    ['config-show', ['config', 'show']],
    ['config-path', ['config', 'path']],
    ['version', ['version'], { json: false }],
    ['coverage', ['coverage', '--top', '5']],
    ['inconsistencies', ['inconsistencies']],
    ['structural-manifest', ['structural-manifest']],
    ['export-symbols', ['export-symbols', '--limit', '2']],
    ['find', ['find', p.symbol, '--top', '3']],
    ['grep', ['grep', p.symbol, '--top', '3']],
    ['semantic', ['semantic', 'index', '--top', '3']],
    ['explore', ['explore', p.symbol, '--seeds', '2', '--edges', '2']],
    ['read-order', ['read-order', '--top', '5']],
    ['map', ['map', '--top-subsystems', '5', '--top-bridges', '5', '--top-hubs', '5', '--top-entrypoints', '3']],
    ['hotspots', ['hotspots', '--top', '5']],
    ['orphans', ['orphans', '--top', '5']],
    ['symbols', ['symbols', p.file]],
    ['symbol-at', ['symbol-at', p.pos]],
    ['source', ['source', '--at', p.pos]],
    ['context', ['context', '--at', p.pos]],
    ['context-batch', ['context', p.symbol]],
    ['task-context', ['task-context', `understand ${p.symbol}`, '--mode', 'understand']],
    ['callers', ['callers', '--at', p.pos]],
    ['callees', ['callees', '--at', p.pos]],
    ['references', ['references', '--at', p.pos]],
    ['path', ['path', p.fqn, p.otherFqn]],
    ['traverse', ['traverse', '--at', p.pos, '--depth', '1', '--limit', '10']],
    ['dependencies', ['dependencies', p.file]],
    ['related-files', ['related-files', p.file]],
    ['review', ['review']],
    ['impact', ['impact', '--at', p.pos]],
    ['file-impact', ['file-impact', p.file]],
    ['file-context', ['file-context', p.file]],
    ['risk', ['risk', '--at', p.pos]],
    ['refactor-plan', ['refactor-plan', '--at', p.pos]],
    ['secret-impact', ['secret-impact', 'STRIPE_KEY', '--depth', '2']],
    ['required-keys', ['required-keys', p.entrypoint, '--keys', 'STRIPE_KEY']],
    ['annotations', ['annotations']],
    ['docs', ['docs', 'overview'], { json: false }],
    ['agent-list', ['agent', 'list']],
    ['agent-playbook', ['agent', 'playbook', '--format', 'markdown'], { json: false }],
    ['branch-status', ['branch-status']],
    ['cache-list', ['cache', 'list']],
    ['daemon-status', ['daemon', 'status']],
    ['completion', ['completion', 'zsh'], { json: false }],
  ]

  const failures = []
  const answered = []
  for (const [id, args, opts = {}] of cases) {
    const feat = feature(id)
    assert.ok(feat, `test case "${id}" has no registry feature`)
    // the argv the test runs must be reachable from the registry's own command
    assert.deepEqual(args.slice(0, feat.cmd.length), feat.cmd, `${id}: test argv does not start with the registered command`)
    const res = await run(args, opts.json !== false)
    const honest = res.exitCode === 0 || (HONEST_FAILURES.has(res.exitCode) && res.json?.ok === false)
    const degraded = DEGRADABLE.has(id) && res.exitCode === 1 && /(embed|ollama|vecgrep|semantic|backend)/i.test(`${res.error || ''} ${res.stderr || ''}`)
    if (honest || degraded) {
      answered.push(`${id}(exit ${res.exitCode})`)
      continue
    }
    failures.push(`${id}: exit ${res.exitCode} code=${res.code} error=${res.error || res.parseError || '(none)'} stderr=${(res.stderr || '').slice(0, 200)}`)
  }
  assert.deepEqual(failures, [], `features that did not answer:\n${failures.join('\n')}`)
  assert.ok(answered.length >= 45, `expected at least 45 live features, got ${answered.length}`)
})

test('the registry exposes panels for features this test cannot run safely', { skip: !bin && 'no codemap binary in the repository' }, () => {
  // Mutating / blocking surfaces are wired but not executed here; assert they
  // exist so removing one from the registry fails loudly.
  for (const id of ['init', 'index', 'index-precise', 'annotate', 'agent-setup', 'branch-switch', 'branch-snapshot', 'cache-save', 'cache-restore', 'cache-drop', 'cache-export', 'cache-import', 'daemon-start', 'daemon-stop', 'mcp', 'raw']) {
    assert.ok(feature(id), `registry lost the ${id} panel`)
  }
})
