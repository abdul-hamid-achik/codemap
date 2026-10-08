/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// CLI bridge units: progress parsing, JSON recovery, binary candidates, and a
// real end-to-end spawn against the repository's own codemap binary.

import test from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, mkdtempSync, mkdirSync, writeFileSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

import { parseProgress, recoverJSON, candidateBinaries, runCodemap, EXIT_MEANING, parseVersion, preferInstalled, bundledBinary, resolveBinary } from '../electron/cli.mjs'

const here = path.dirname(fileURLToPath(import.meta.url))
const appRoot = path.resolve(here, '..')
const repo = path.resolve(appRoot, '..')
const bin = [path.join(repo, 'bin', 'codemap'), path.join(repo, 'codemap')].find((p) => existsSync(p)) || null

test('parseProgress reads N/M and percentage lines', () => {
  assert.deepEqual(parseProgress('embedding 12/340 nodes'), { done: 12, total: 340 })
  assert.deepEqual(parseProgress('[====>   ] 47%'), { done: 47, total: 100 })
  assert.equal(parseProgress('nothing numeric here'), null)
  assert.equal(parseProgress('9/0 makes no sense'), null)
})

test('recoverJSON finds the JSON document behind human preamble lines', () => {
  const text = 'indexing via daemon (pid 42; no live progress)…\n{"ok":true,"nodes":7}'
  assert.deepEqual(recoverJSON(text), { ok: true, nodes: 7 })
  assert.equal(recoverJSON('no json at all'), undefined)
  // braces inside strings must not confuse the scanner
  const tricky = 'warn: {} is not it\n{"a":"}{" ,"b":1}'
  const got = recoverJSON(tricky)
  assert.equal(got.b, 1)
})

test('candidateBinaries prefers the explicit path, then the dev checkout', () => {
  const list = candidateBinaries(appRoot, '/custom/codemap')
  assert.equal(list[0], '/custom/codemap')
  assert.ok(list.some((p) => p.endsWith(path.join('bin', 'codemap'))))
  assert.equal(new Set(list).size, list.length, 'no duplicates')
})

test('exit-code taxonomy matches the documented contract', () => {
  assert.deepEqual(EXIT_MEANING, {
    0: 'answered',
    1: 'operational',
    2: 'not_found',
    3: 'index_missing',
    4: 'index_corrupt',
    5: 'not_a_repo',
    6: 'gate_failed',
  })
})

test('runCodemap executes a real command and parses the report', { skip: !bin && 'no codemap binary in the repository' }, async () => {
  const res = await runCodemap({ binary: bin, args: ['version'], json: false, cwd: repo, timeoutMs: 30000 })
  assert.equal(res.ok, true)
  assert.match(res.stdout, /codemap (v\d|dev)/)
  assert.equal(res.exitCode, 0)
  assert.equal(res.code, 'answered')
})

test('runCodemap maps the structured failure envelope', { skip: !bin && 'no codemap binary in the repository' }, async () => {
  const res = await runCodemap({
    binary: bin,
    args: ['symbols', 'this/file/does/not/exist.go'],
    json: true,
    cwd: repo,
    timeoutMs: 30000,
  })
  assert.equal(res.ok, false)
  assert.equal(res.json?.ok, false)
  assert.ok(res.code, 'a code must come back')
  assert.ok(res.error, 'an error message must come back')
  assert.ok(res.exitCode > 0)
})

test('runCodemap reports a timeout instead of hanging', { skip: !bin && 'no codemap binary in the repository' }, async () => {
  const res = await runCodemap({ binary: bin, args: ['status', '--full'], json: true, cwd: repo, timeoutMs: 1 })
  assert.equal(res.ok, false)
  assert.equal(res.timedOut, true)
  assert.match(res.error, /timed out/)
})

test('runCodemap streams lines to the caller', { skip: !bin && 'no codemap binary in the repository' }, async () => {
  const events = []
  await runCodemap({ binary: bin, args: ['version'], json: false, cwd: repo, timeoutMs: 30000, onEvent: (e) => events.push(e) })
  assert.ok(events.some((e) => e.type === 'start'))
  assert.ok(events.some((e) => e.type === 'line'))
  assert.ok(events.some((e) => e.type === 'end'))
})

test('a missing binary resolves to a structured operational error', async () => {
  const res = await runCodemap({ binary: '/nonexistent/codemap', args: ['version'], json: false, cwd: repo, timeoutMs: 5000 })
  assert.equal(res.ok, false)
  assert.equal(res.code, 'operational')
  assert.ok(res.error)
})

test('a packaged app never short-circuits to its bundled binary as a dev candidate', () => {
  const list = candidateBinaries('/Applications/Codemap Studio.app/Contents/Resources/app.asar', '')
  assert.ok(!list.some((p) => p.includes(`Resources${path.sep}bin`)), `bundled path leaked into candidates: ${list}`)
})

test('parseVersion reads release versions and rejects dev builds', () => {
  assert.deepEqual(parseVersion('codemap version v0.69.0 (052dd52) 2026-10-02T06:04:15Z'), [0, 69, 0])
  assert.deepEqual(parseVersion('codemap version v1.2.10-3-gabc'), [1, 2, 10])
  assert.equal(parseVersion('codemap version dev (none) unknown'), null)
})

test('the installed codemap wins unless it is provably older than the bundled one', () => {
  assert.equal(preferInstalled([0, 69, 0], [0, 69, 0]), true, 'same version keeps the installed binary')
  assert.equal(preferInstalled([0, 70, 1], [0, 69, 0]), true, 'newer installed binary wins')
  assert.equal(preferInstalled([0, 68, 0], [0, 69, 0]), false, 'older installed binary yields to the bundled one')
  assert.equal(preferInstalled(null, [0, 69, 0]), true, 'a dev build is trusted')
  assert.equal(preferInstalled([0, 68, 0], null), true, 'unknown bundled version never displaces the installed one')
})

test('resolveBinary uses the bundled binary when no newer codemap is installed', async () => {
  const resources = mkdtempSync(path.join(tmpdir(), 'cm-resources-'))
  const name = process.platform === 'win32' ? 'codemap.exe' : 'codemap'
  mkdirSync(path.join(resources, 'bin'))
  writeFileSync(path.join(resources, 'bin', name), '#!/bin/sh\necho "codemap version v99.0.0"\n', { mode: 0o755 })
  try {
    assert.equal(bundledBinary(resources), path.join(resources, 'bin', name))
    const emptyPath = { ...process.env, PATH: mkdtempSync(path.join(tmpdir(), 'cm-nopath-')), SHELL: '/bin/sh' }
    const saved = process.env.CODEMAP_BIN
    delete process.env.CODEMAP_BIN
    const shell = process.env.SHELL
    process.env.SHELL = '/bin/sh'
    try {
      const r = await resolveBinary({ appRoot: path.join(resources, 'app.asar'), explicit: '', env: emptyPath, resourcesPath: resources })
      assert.equal(r.source, 'bundled')
      assert.equal(r.binary, path.join(resources, 'bin', name))
    } finally {
      if (saved !== undefined) process.env.CODEMAP_BIN = saved
      process.env.SHELL = shell
    }
  } finally {
    rmSync(resources, { recursive: true, force: true })
  }
})

test('codemapEnv only lets CODEMAP_* settings through from the renderer', async () => {
  const { codemapEnv } = await import('../electron/cli.mjs')
  assert.deepEqual(codemapEnv({ CODEMAP_PRECISE_SERVERS: 4, PATH: '/evil', NODE_OPTIONS: '--x', codemap_lower: '1', CODEMAP_NULL: null }), { CODEMAP_PRECISE_SERVERS: '4' })
  assert.deepEqual(codemapEnv(undefined), {})
})
