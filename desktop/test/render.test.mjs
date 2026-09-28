/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Every real codemap report shape must render. Fixtures are trimmed captures of
// `codemap <cmd> --json` against this repository, so a renderer that throws or
// silently drops a section fails here rather than in a window.

import test from 'node:test'
import assert from 'node:assert/strict'
import { readdirSync, readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

import { installDOM, stats } from './dom-stub.mjs'

installDOM()

const { renderReport, genericReport, calloutsFor, graphPayloadFor, RENDERERS } = await import('../renderer/js/report.mjs')
const { FEATURES, feature } = await import('../renderer/js/features.mjs')
const { tokenLines, tokenize, langFromPath } = await import('../renderer/js/highlight.mjs')
const { renderMarkdown } = await import('../renderer/js/markdown.mjs')
const { parseArgv, quoteArgv } = await import('../renderer/js/argv.mjs')

const here = path.dirname(fileURLToPath(import.meta.url))
const fixtureDir = path.join(here, 'fixtures')
const fixtures = readdirSync(fixtureDir).filter((f) => f.endsWith('.json')).sort()

function load(name) {
  return JSON.parse(readFileSync(path.join(fixtureDir, name), 'utf8'))
}

function featureForFixture(name) {
  const id = name.replace(/\.json$/, '').replace(/_/g, '-')
  return { id: feature(id) ? id : null, feat: feature(id) }
}

const ctx = {
  onSymbol: () => {},
  onFile: () => {},
  onFeature: () => {},
  onPrefill: () => {},
  onNext: () => {},
  onGraph: () => {},
  onRerun: () => {},
  onOpenProject: () => {},
  onSymbolQuery: () => {},
  onMapView: () => {},
  renderInspector: () => {},
}

test('fixtures exist and cover most of the registry', () => {
  assert.ok(fixtures.length >= 35, `expected a broad fixture set, got ${fixtures.length}`)
  const unmapped = fixtures.map((f) => featureForFixture(f)).filter((x) => !x.id).map((x) => x)
  assert.equal(unmapped.length, 0, `fixtures with no registry feature: ${JSON.stringify(unmapped)}`)
})

test('every fixture renders through its feature panel without throwing', () => {
  for (const name of fixtures) {
    const json = load(name)
    const { id, feat } = featureForFixture(name)
    assert.ok(feat, `${name}: no feature registered`)
    // the symbol_at fixture is codemap's structured failure envelope
    const ok = json.ok !== false
    const result = {
      ok,
      json,
      exitCode: ok ? 0 : 2,
      code: ok ? 'answered' : json.code || 'not_found',
      error: ok ? undefined : json.error,
      hint: ok ? undefined : json.hint,
      stdout: JSON.stringify(json),
      stderr: '',
      ms: 12,
      command: ['codemap', ...feat.cmd, '--json'],
    }
    let node
    assert.doesNotThrow(() => {
      node = renderReport(feat, result, ctx)
    }, `${name}: renderer threw`)
    const s = stats(node)
    assert.ok(s.count > 5, `${name}: rendered almost nothing (${s.count} nodes)`)
    assert.ok(s.chars > 0, `${name}: rendered no text`)
  }
})

test('the failure envelope renders as an error, not an empty view', () => {
  const json = load('symbol_at.json')
  assert.equal(json.ok, false)
  const node = renderReport(feature('symbol-at'), { ok: false, json, exitCode: 2, code: json.code, error: json.error, hint: json.hint, stdout: '', stderr: '', ms: 3, command: ['codemap', 'symbol-at'] }, ctx)
  const text = node.textContent
  assert.ok(text.includes(json.code), 'error code missing from the rendered output')
  assert.ok(text.includes(json.error), 'error message missing')
})

test('every declared renderer id exists', () => {
  for (const f of FEATURES) {
    if (!f.render) continue
    assert.ok(RENDERERS[f.render], `${f.id}: render "${f.render}" has no renderer`)
  }
})

test('the generic renderer handles shapes no specialised view claims', () => {
  const samples = [
    { a: 1, b: 'two', c: true, d: null },
    { items: [{ name: 'x', n: 1 }, { name: 'y', n: 2 }] },
    { nested: { deep: { deeper: [1, 2, 3] } } },
    [],
    ['a', 'b'],
    { symbols: [{ symbol: 'S', fqn: 'p.S', kind: 'function', file: 'a/b.go', start_line: 10 }] },
  ]
  for (const s of samples) {
    let node
    assert.doesNotThrow(() => {
      node = genericReport(s, ctx)
    }, `generic renderer threw on ${JSON.stringify(s).slice(0, 60)}`)
    assert.ok(stats(node).count > 1)
  }
})

test('callouts surface note/resolution/hint/partial_errors but never invent them', () => {
  assert.equal(calloutsFor({}).length, 0)
  assert.equal(calloutsFor({ note: 'hello' }).length, 1)
  assert.equal(calloutsFor({ resolution: 'name-based' }).length, 1)
  assert.equal(calloutsFor({ partial_errors: [{ kind: 'x', detail: 'y' }] }).length, 1)
  assert.equal(calloutsFor({ note: '' }).length, 0)
})

test('graph payloads come out of relation-bearing reports', () => {
  const impact = graphPayloadFor(feature('impact'), load('impact.json'))
  assert.ok(impact && impact.nodes.length > 1, 'impact should produce a graph')
  assert.ok(impact.edges.length > 0)
  const focus = impact.nodes.filter((n) => n.role === 'focus')
  assert.equal(focus.length, 1, 'exactly one focus node')

  const traverse = graphPayloadFor(feature('traverse'), load('traverse.json'))
  assert.ok(traverse && traverse.nodes.length >= 2)

  const callers = graphPayloadFor(feature('callers'), load('callers.json'))
  assert.ok(callers && callers.edges.every((e) => e.type === 'calls'))

  const pathGraph = graphPayloadFor(feature('path'), load('path.json'))
  // this fixture found no path, so there is nothing to draw
  assert.equal(pathGraph, null)

  assert.equal(graphPayloadFor(feature('status'), load('status.json')), null, 'a status report carries no relations')
})

test('the highlighter never loses or invents source text', () => {
  const samples = [
    ['go', 'package main\n\n// note\nfunc Add(a, b int) int {\n\ts := "x\\ty"\n\treturn a + b // tail\n}\n'],
    ['typescript', 'export async function f<T>(x: T): Promise<T> { /* c */ return x }\n'],
    ['python', 'def f(a, b=1):\n    # comment\n    return a + b\n'],
    ['ruby', 'class A\n  def b; "s"; end # c\nend\n'],
    ['lua', 'local function f(a) --[[ block ]] return a end -- line\n'],
    ['yaml', 'key: value\nlist:\n  - a\n  - b\n# comment\n'],
    ['sql', "SELECT a, 'lit' FROM t WHERE x = 1; -- c\n"],
    ['json', '{"a": 1, "b": [true, null, "s"]}\n'],
    ['markdown', '# Title\n\ntext **bold** `code`\n\n```go\nx := 1\n```\n'],
    ['unknownext', 'plain text ✓ ünïcøde\n'],
  ]
  for (const [lang, src] of samples) {
    const toks = tokenize(src, lang)
    assert.equal(toks.map((t) => t.t).join(''), src, `${lang}: token stream must round-trip exactly`)
    const lines = tokenLines(src, lang)
    assert.equal(lines.map((l) => l.map((t) => t.t).join('')).join('\n'), src, `${lang}: tokenLines must reconstruct the source exactly`)
  }
})

test('langFromPath maps the languages codemap indexes', () => {
  const cases = { 'a.go': 'go', 'a.ts': 'typescript', 'a.tsx': 'tsx', 'a.js': 'javascript', 'a.jsx': 'jsx', 'a.py': 'python', 'a.rb': 'ruby', 'a.lua': 'lua', 'a.sql': 'sql', 'a.yml': 'yaml', 'a.md': 'markdown', 'a.json': 'json', 'a.vue': 'typescript', 'a.gd': 'gdscript', 'a.sh': 'shell' }
  for (const [p, want] of Object.entries(cases)) assert.equal(langFromPath(p), want, p)
})

test('markdown renders headings, code, lists and tables without injecting markup', () => {
  const node = renderMarkdown('# Title\n\nsome `code` and **bold**\n\n- a\n- b\n\n```go\nfunc main() {}\n```\n\n| h | i |\n| - | - |\n| 1 | 2 |\n\n<script>alert(1)</script>\n')
  const s = stats(node)
  assert.ok(s.count > 10)
  assert.ok(node.textContent.includes('Title'))
  assert.ok(node.textContent.includes('<script>alert(1)</script>'), 'HTML in markdown must stay text')
  assert.ok(!node.querySelector('script'), 'no script element may be created')
})

test('parseArgv round-trips quoted arguments', () => {
  assert.deepEqual(parseArgv('impact --at a.go:1 --depth 4'), ['impact', '--at', 'a.go:1', '--depth', '4'])
  assert.deepEqual(parseArgv('grep "TODO(security)" -i'), ['grep', 'TODO(security)', '-i'])
  assert.deepEqual(parseArgv("semantic 'where is config loaded' --top 5"), ['semantic', 'where is config loaded', '--top', '5'])
  assert.deepEqual(parseArgv('annotate Sym --note "a b \\"c\\""'), ['annotate', 'Sym', '--note', 'a b "c"'])
  assert.deepEqual(parseArgv(''), [])
  assert.deepEqual(parseArgv('  '), [])
  assert.deepEqual(parseArgv(quoteArgv(['a b', 'c"d'])), ['a b', 'c"d'])
})
