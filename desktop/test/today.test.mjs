/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

import test from 'node:test'
import assert from 'node:assert/strict'

import { installDOM, stats } from './dom-stub.mjs'

installDOM()

const { state } = await import('../renderer/js/state.mjs')
const today = await import('../renderer/js/views/todayview.mjs')
const { historyView } = await import('../renderer/js/views/historyview.mjs')
const terminal = await import('../electron/terminal.mjs')

state.project = '/tmp/project'
state.settings = { theme: 'dark', autoSearch: false, recentSearches: [] }
state.git = { isRepo: true, branch: 'main', changed: [' M internal/app/review.go'], changedCount: 1, commits: [], branches: [] }

const review = {
  project: 'demo',
  mode: 'working',
  changed_files: [{ path: 'internal/app/review.go', status: 'M', symbols: 1 }],
  changed_symbols: [
    { symbol: 'Review', fqn: 'app.Service.Review', kind: 'method', file: 'internal/app/review.go', start_line: 42 },
    { symbol: 'Helper', fqn: 'app.Helper', kind: 'function', file: 'internal/app/helper.go', start_line: 9 },
  ],
  untested_symbols: [
    { symbol: 'Helper', fqn: 'app.Helper', kind: 'function', file: 'internal/app/helper.go', start_line: 9 },
  ],
  covering_tests: [
    { symbol: 'TestReview', fqn: 'app.TestReview', kind: 'test', file: 'internal/app/review_test.go', start_line: 12 },
  ],
  blast_radius: [{ symbol: 'Caller', fqn: 'app.Caller', kind: 'function', file: 'internal/app/caller.go', start_line: 3, confidence: 'confirmed' }],
  analysis_complete: true,
  total_symbols: 2,
  stale: false,
  risk: { level: 'medium', score: 0.54, factors: [{ factor: 'untested_changes', severity: 0.9, detail: '1 changed symbol has no covering test' }] },
  coverage: { verdict: 'partial', covered_symbols: 1, uncovered_symbols: 1, unknown_symbols: 0 },
  test_commands: ["go test ./internal/app -run '^(TestReview)$'"],
  next: [
    { tool: 'terminal', args: { command: "go test ./internal/app -run '^(TestReview)$'" }, why: 'run covering tests' },
  ],
}

const affected = {
  tests: [
    { file: 'internal/app/review_test.go', reasons: ['covers:app.Service.Review'] },
    { file: 'web/app.test.ts', reasons: ['imports:web/app.ts'] },
    { file: 'desktop/test/views.test.mjs', reasons: ['changed'] },
    { file: 'py/test_app.py', reasons: ['covers:app.Helper'] },
  ],
  unmapped: [],
  call_graph: 'name',
  analysis_complete: true,
}

test('today verdict never calls unknown or stale work safe', () => {
  assert.equal(today.todayVerdict({ status: null, review: null }).tone, 'danger')
  assert.match(today.todayVerdict({ status: { registered: true, nodes: 10 }, review: { ...review, stale: true } }).title, /refresh/i)
  assert.match(today.todayVerdict({ status: { registered: true, nodes: 10 }, review: { ...review, untested_symbols: [], coverage: { verdict: 'unknown', covered_symbols: 0, uncovered_symbols: 0, unknown_symbols: 2 }, risk: { level: 'unknown', score: 0 } } }).title, /clean verdict/i)
  assert.match(today.todayVerdict({ status: { registered: true, nodes: 10 }, review: { ...review, changed_files: [], changed_symbols: [] } }).title, /no local changes/i)
})

test('today actions prioritize verification and uncovered work', () => {
  const actions = today.todayActions({
    status: { registered: true, nodes: 100, stale: { changed: 2, new: 0, deleted: 0 } },
    review,
    affected,
    coverage: { covered_files: 3, total_files: 10 },
    doctor: { checks: [{ name: 'Ollama', ok: false, detail: 'missing model' }] },
  })
  assert.deepEqual(actions.map((a) => a.id).slice(0, 4), ['reindex', 'run-tests', 'inspect-uncovered', 'review-diff'])
  assert.ok(actions.some((a) => a.id === 'precise-coverage'))
  assert.ok(actions.some((a) => a.id === 'doctor'))
})

test('suggested test commands combine codemap commands with affected files', () => {
  assert.deepEqual(today.suggestedTestCommands(review, affected), [
    "go test ./internal/app -run '^(TestReview)$'",
    'bun test web/app.test.ts',
    'node --test desktop/test/views.test.mjs',
    'pytest py/test_app.py',
  ])
})

test('today brief is a paste-ready, honesty-preserving summary', () => {
  const brief = today.buildTodayBrief({
    project: '/tmp/project',
    branch: 'main',
    status: { registered: true, nodes: 100, edges: 200, files: 20 },
    review,
    affected,
    coverage: { covered_files: 3, total_files: 10 },
    commands: today.suggestedTestCommands(review, affected),
  })
  assert.match(brief, /# Codemap brief — demo/)
  assert.match(brief, /app\.Service\.Review/)
  assert.match(brief, /go test \.\/internal\/app/)
  assert.match(brief, /unknown is not safe/i)
  assert.match(brief, /3\/10 files/)
})

test('terminal shell invocation is platform-specific and output is cleaned', () => {
  assert.deepEqual(terminal.shellInvocation('npm test', 'win32', {}), {
    file: 'powershell.exe',
    args: ['-NoLogo', '-NoProfile', '-ExecutionPolicy', 'Bypass', '-Command', 'npm test'],
  })
  assert.deepEqual(terminal.shellInvocation('go test ./...', 'darwin', { SHELL: '/bin/zsh' }), {
    file: '/bin/zsh',
    args: ['-lc', 'go test ./...'],
  })
  assert.equal(terminal.stripAnsi('\u001b[31mfailed\u001b[0m'), 'failed')
})

test('shell runner captures output, exit code and timeout', async () => {
  const ok = await terminal.runShellCommand({
    command: process.platform === 'win32' ? "Write-Output 'studio-terminal-ok'" : "printf 'studio-terminal-ok\\n'",
    cwd: '/tmp',
    timeoutMs: 5000,
  })
  assert.equal(ok.ok, true)
  assert.equal(ok.exitCode, 0)
  assert.match(ok.output, /studio-terminal-ok/)

  const slow = await terminal.runShellCommand({
    command: process.platform === 'win32' ? 'Start-Sleep -Seconds 2' : 'sleep 2',
    cwd: '/tmp',
    timeoutMs: 50,
  })
  assert.equal(slow.ok, false)
  assert.equal(slow.timedOut, true)
})

test('today view mounts with verdict, actions and runnable tests', async () => {
  const realRun = window.studio.run
  const calls = []
  window.studio.run = async (req) => {
    calls.push(req.args)
    const cmd = req.args[0]
    const base = { stdout: '', stderr: '', exitCode: 0, ms: 2, command: ['codemap', ...req.args] }
    if (cmd === 'status') return { ...base, ok: true, json: { project: 'demo', registered: true, nodes: 100, edges: 200, files: 20, stale: { changed: 0, new: 0, deleted: 0 } } }
    if (cmd === 'review') return { ...base, ok: true, json: review }
    if (cmd === 'affected') return { ...base, ok: true, json: affected }
    if (cmd === 'coverage') return { ...base, ok: true, json: { covered_files: 3, total_files: 10 } }
    if (cmd === 'doctor') return { ...base, ok: true, json: { checks: [{ name: 'Ollama', ok: false, detail: 'missing model' }] } }
    return { ...base, ok: true, json: {} }
  }
  window.studio.terminal = {
    run: async () => ({ ok: true, exitCode: 0, output: 'ok', ms: 4 }),
    cancel: async () => true,
  }

  try {
    const ctx = {
      go: () => {}, openFeature: () => {}, openSymbol: () => {}, openFile: () => {}, toast: () => {},
    }
    const inst = today.todayView(ctx)
    await new Promise((r) => setTimeout(r, 80))
    const s = stats(inst.node)
    assert.match(s.text, /Today/)
    assert.match(s.text, /Needs attention before you ship/)
    assert.match(s.text, /Do next/)
    assert.match(s.text, /Run these tests/)
    assert.match(s.text, /go test \.\/internal\/app/)
    assert.ok(calls.some((a) => a[0] === 'review'))
    assert.ok(calls.some((a) => a[0] === 'affected'))
    inst.destroy?.()
  } finally {
    window.studio.run = realRun
  }
})

test('history never replays a shell row as codemap argv', async () => {
  const realHistory = window.studio.settings.history
  window.studio.settings.history = async () => [{
    at: Date.now(),
    featureId: 'terminal',
    args: ['go test ./internal/app'],
    cwd: '/tmp/project',
    ok: true,
    gateFailed: false,
    exitCode: 0,
    code: '',
    ms: 12,
    bytes: 20,
  }]
  try {
    const inst = historyView({ go: () => {}, openFeature: () => {} })
    await new Promise((r) => setTimeout(r, 20))
    const s = stats(inst.node)
    assert.match(s.text, /Terminal/)
    assert.match(s.text, /Today/)
    assert.equal(s.text.includes('re-run'), false)
  } finally {
    window.studio.settings.history = realHistory
  }
})
