/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Registry invariants + argv building + live CLI coverage audit.

import test from 'node:test'
import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { existsSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

import { FEATURES, GROUPS, APP_VIEWS, feature, buildArgs, defaultValues, commandLine, featuresByGroup, requiredFields } from '../renderer/js/features.mjs'

const here = path.dirname(fileURLToPath(import.meta.url))
const repo = path.resolve(here, '..', '..')

function findBinary() {
  for (const c of [path.join(repo, 'bin', 'codemap'), path.join(repo, 'codemap')]) if (existsSync(c)) return c
  try {
    return execFileSync('command', ['-v', 'codemap'], { encoding: 'utf8' }).trim() || null
  } catch {
    try {
      return execFileSync('bash', ['-lc', 'command -v codemap'], { encoding: 'utf8' }).trim() || null
    } catch {
      return null
    }
  }
}

test('every feature has an identity, a group and a blurb', () => {
  const ids = new Set()
  for (const f of FEATURES) {
    assert.ok(f.id, 'feature without id')
    assert.ok(!ids.has(f.id), `duplicate feature id: ${f.id}`)
    ids.add(f.id)
    assert.match(f.id, /^[a-z0-9-]+$/, `bad id shape: ${f.id}`)
    assert.ok(f.title, `${f.id}: missing title`)
    assert.ok(f.blurb, `${f.id}: missing blurb`)
    assert.ok(GROUPS.some((g) => g.id === f.group), `${f.id}: unknown group ${f.group}`)
    assert.equal(typeof f, 'object')
  }
  assert.ok(FEATURES.length >= 55, `expected a broad registry, got ${FEATURES.length}`)
})

test('every app view resolves to a real group and unique id', () => {
  const ids = new Set(APP_VIEWS.map((v) => v.id))
  assert.equal(ids.size, APP_VIEWS.length)
  for (const v of APP_VIEWS) assert.ok(v.label && v.icon, `${v.id}: missing label/icon`)
})

test('runnable features declare a command and valid argument specs', () => {
  for (const f of FEATURES) {
    if (!f.cmd) continue
    assert.ok(Array.isArray(f.cmd) && f.cmd.length, `${f.id}: cmd must be a non-empty array`)
    const names = new Set()
    for (const a of f.args || []) {
      assert.ok(a.name, `${f.id}: argument without a name`)
      assert.ok(!names.has(a.name), `${f.id}: duplicate argument name ${a.name}`)
      names.add(a.name)
      assert.ok(['pos', 'text', 'num', 'bool', 'select', 'csv', 'repeat', 'path'].includes(a.kind), `${f.id}.${a.name}: unknown kind ${a.kind}`)
      if (!a.pos && a.kind !== 'pos') assert.ok(a.flag || /^CODEMAP_[A-Z0-9_]+$/.test(a.env || ''), `${f.id}.${a.name}: non-positional needs a flag or a CODEMAP_* env`)
      if (a.kind === 'select') assert.ok(a.options?.length, `${f.id}.${a.name}: select needs options`)
    }
  }
})

test('buildArgs emits positionals then flags, honouring each kind', () => {
  const f = feature('impact')
  assert.deepEqual(buildArgs(f, {}), [])
  assert.deepEqual(buildArgs(f, { symbol: 'Review' }), ['Review'])
  assert.deepEqual(buildArgs(f, { at: ['a.go:1', 'b.go:2'] }), ['--at', 'a.go:1', '--at', 'b.go:2'])
  assert.deepEqual(buildArgs(f, { depth: 5, batch: true }), ['--batch', '--depth', '5'])
  assert.deepEqual(
    buildArgs(f, { symbol: 'Review', at: ['a.go:1'], depth: 4 }),
    ['Review', '--at', 'a.go:1', '--depth', '4'],
  )
})

test('bool flags are omitted when false, csv joins, repeat expands', () => {
  const index = feature('index')
  assert.deepEqual(buildArgs(index, { reindex: false, precise: true }), ['--precise'])
  assert.deepEqual(buildArgs(index, { exclude_extra: 'a,b' }), ['--exclude-extra', 'a,b'])
  assert.deepEqual(buildArgs(index, { exclude_extra: ['a', 'b', ''] }), ['--exclude-extra', 'a,b'])
  assert.deepEqual(buildArgs(index, { no_tips: true, no_embed: true }).sort(), ['--no-embed', '--no-tips'])

  const grep = feature('grep')
  assert.deepEqual(buildArgs(grep, { pattern: 'TODO(security)', regex: false, ignore_case: true, top: 20 }), ['TODO(security)', '-i', '--top', '20'])

  const docs = feature('docs')
  assert.deepEqual(buildArgs(docs, { topic: 'accuracy' }), ['accuracy'])
  assert.deepEqual(buildArgs(docs, { topic: '' }), [])

  const setup = feature('agent-setup')
  assert.deepEqual(buildArgs(setup, { harness: 'cursor', dry_run: true }), ['cursor', '--dry-run'])
})

test('multi-word positionals split into several arguments', () => {
  const path_ = feature('path')
  assert.deepEqual(buildArgs(path_, { from: 'app.Main', to: 'app.Save' }), ['app.Main', 'app.Save'])
  const symbols = feature('context')
  assert.deepEqual(buildArgs(symbols, { symbols: 'Run Helper Other' }), ['Run', 'Helper', 'Other'])
})

test('defaults are applied and required fields are declared', () => {
  const impact = feature('impact')
  const d = defaultValues(impact)
  assert.equal(d.depth, 3)
  assert.equal(d.batch, false)
  const req = requiredFields(feature('grep'))
  assert.deepEqual(req.map((r) => r.name), ['pattern'])
  assert.equal(commandLine(feature('status'), { full: true }), 'status --full --json')
  assert.equal(commandLine(feature('docs'), { topic: 'workflow' }, { json: false }), 'docs workflow')
})

test('featuresByGroup covers every declared group with at least one feature', () => {
  const byGroup = featuresByGroup()
  for (const g of GROUPS) {
    assert.ok(byGroup.get(g.id)?.length, `group ${g.id} has no features`)
  }
})

test('MCP tool names are unique except for documented presets', () => {
  // `index-precise` is a preset of the same MCP tool (precise:true), and
  // `context-batch` shares codemap_context's surface with several positionals.
  const SHARED = new Set(['codemap_index'])
  const seen = new Map()
  for (const f of FEATURES) {
    if (!f.mcp) continue
    if (seen.has(f.mcp)) {
      assert.ok(SHARED.has(f.mcp), `unexpected duplicate MCP mapping ${f.mcp}: ${seen.get(f.mcp)} and ${f.id}`)
      continue
    }
    seen.set(f.mcp, f.id)
  }
  assert.ok(seen.size >= 30, `expected most features to name their MCP tool, got ${seen.size}`)
})

test('live CLI audit: every command the binary advertises has a panel', { skip: !findBinary() && 'no codemap binary available' }, () => {
  const bin = findBinary()
  const help = execFileSync(bin, ['--help'], { encoding: 'utf8' })
  const section = help.split('Available Commands:')[1].split(/\nFlags:/)[0]
  const commands = section.split('\n').map((l) => /^\s{2,}([a-z][a-z0-9-]*)\s/.exec(l)?.[1]).filter(Boolean)
  assert.ok(commands.length > 30, `parsed only ${commands.length} commands from --help`)
  const missing = commands.filter((c) => c !== 'help' && !FEATURES.some((f) => (f.cmd || [])[0] === c))
  assert.deepEqual(missing, [], `CLI commands with no panel: ${missing.join(', ')}`)
})

test('live CLI audit: every nested subcommand has a panel', { skip: !findBinary() && 'no codemap binary available' }, () => {
  const bin = findBinary()
  const parents = ['agent', 'cache', 'config', 'daemon']
  for (const p of parents) {
    const help = execFileSync(bin, [p, '--help'], { encoding: 'utf8' })
    const section = help.split('Available Commands:')[1]?.split(/\nFlags:/)[0] || ''
    const subs = section.split('\n').map((l) => /^\s{2,}([a-z][a-z0-9-]*)\s/.exec(l)?.[1]).filter(Boolean)
    assert.ok(subs.length, `no subcommands parsed for ${p}`)
    const missing = subs.filter((s) => !FEATURES.some((f) => (f.cmd || []).join(' ') === `${p} ${s}`))
    assert.deepEqual(missing, [], `codemap ${p} subcommands with no panel: ${missing.join(', ')}`)
  }
})

test('live CLI audit: declared flags exist on the real command', { skip: !findBinary() && 'no codemap binary available' }, () => {
  const bin = findBinary()
  const problems = []
  for (const f of FEATURES) {
    if (!f.cmd?.length) continue
    let help = ''
    try {
      help = execFileSync(bin, [...f.cmd, '--help'], { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] })
    } catch {
      problems.push(`${f.id}: codemap ${f.cmd.join(' ')} --help failed`)
      continue
    }
    for (const a of f.args || []) {
      if (!a.flag) continue
      if (!help.includes(a.flag)) problems.push(`${f.id}: flag ${a.flag} is not in \`${f.cmd.join(' ')} --help\``)
    }
  }
  assert.deepEqual(problems, [], problems.join('\n'))
})

test('live CLI audit: the setup harness list matches codemap agent setup', { skip: !findBinary() && 'no codemap binary available' }, () => {
  let msg = ''
  try {
    execFileSync(findBinary(), ['agent', 'setup', '__none__'], { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] })
  } catch (err) {
    msg = String(err.stderr || err.stdout || '')
  }
  const valid = (/valid: ([a-z0-9, -]+)/.exec(msg)?.[1] || '').split(',').map((s) => s.trim()).filter(Boolean)
  assert.ok(valid.length, `could not parse the harness list from: ${msg}`)
  const declared = feature('agent-setup').args.find((a) => a.name === 'harness').options.map((o) => o.v)
  assert.deepEqual([...declared].sort(), [...valid].sort())
})

test('env fields become CODEMAP_* variables, never flags', async () => {
  const { buildEnv } = await import('../renderer/js/features.mjs')
  const f = feature('index-precise')
  const values = { ...defaultValues(f), precise_servers: 4 }
  assert.ok(!buildArgs(f, values).some((a) => /precise.servers/i.test(a)), 'no flag for an env field')
  assert.deepEqual(buildEnv(f, values), { CODEMAP_PRECISE_SERVERS: '4' })
  assert.deepEqual(buildEnv(f, defaultValues(f)), {}, '0 leaves the configured value alone')
  assert.ok(commandLine(f, values).startsWith('CODEMAP_PRECISE_SERVERS=4 index --precise'))
})
