/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Today: the daily cockpit. It does not mirror the CLI surface; it answers
// the three questions a working session actually starts and ends with:
// what changed, what needs attention, and what should I run next?

import { h, mount, copy } from '../dom.mjs'
import { runArgs } from '../runner.mjs'
import {
  badge, callout, card, confirmDialog, emptyState, metric, spinner,
  symList, toast, fmt, callGraphBadge, staleBadge, kv,
} from '../components.mjs'
import { state } from '../state.mjs'
import { affectedArgs, testCommands } from './reviewview.mjs'

const MODES = [
  { v: 'working', label: 'Working tree' },
  { v: 'staged', label: 'Staged' },
  { v: 'since', label: 'Since ref' },
]

function staleCount(status, review) {
  const s = review?.staleness || status?.stale || {}
  if (typeof s === 'number') return s
  return (s.changed || 0) + (s.new || 0) + (s.deleted || 0)
}

function isIndexed(status, review) {
  if (review?.indexed === false) return false
  if (!status) return false
  return status.registered !== false && Number(status.nodes || 0) > 0
}

function gateWouldFail(review) {
  const w = review?.gate?.would_fail_on
  if (!w) return false
  if (typeof w === 'string') return !!w
  if (Array.isArray(w)) return w.length > 0
  return Object.values(w).some(Boolean)
}

/** One honest sentence over the current diff. Unknown is never called safe. */
export function todayVerdict({ status, review, affected } = {}) {
  if (!isIndexed(status, review)) {
    return {
      tone: 'danger',
      title: 'Index this project first',
      detail: 'Codemap cannot judge the working tree until the graph exists.',
    }
  }
  if (!review) {
    return {
      tone: 'warn',
      title: 'Review unavailable',
      detail: 'The diff analysis did not answer, so there is no verdict to trust yet.',
    }
  }
  if (review.stale || staleCount(status, review) > 0) {
    return {
      tone: 'warn',
      title: 'Refresh the index before trusting this verdict',
      detail: 'The working tree drifted since the last index; every diff-scoped answer below is provisional.',
    }
  }
  const changedFiles = (review.changed_files || []).length
  if (!changedFiles) {
    return {
      tone: 'ok',
      title: 'No local changes to review',
      detail: 'The selected diff scope is empty. Start a task brief or explore the codebase from here.',
    }
  }

  const cov = review.coverage || {}
  const uncovered = Number(cov.uncovered_symbols || 0)
  const untested = (review.untested_symbols || []).length
  if (review.risk?.level === 'high' || uncovered > 0 || untested > 0 || gateWouldFail(review)) {
    return {
      tone: 'danger',
      title: 'Needs attention before you ship',
      detail: 'Codemap found changed code with known test gaps, a high aggregate risk, or a gate that would fail.',
    }
  }
  if (review.analysis_complete === false || review.risk?.level === 'unknown' || cov.verdict === 'unknown' || Number(cov.unknown_symbols || 0) > 0) {
    return {
      tone: 'warn',
      title: 'No clean verdict yet',
      detail: 'Part of the change has no usable call graph or test link. Unknown is not safe; use precise relations for the symbols that matter.',
    }
  }
  const tests = (affected?.tests || []).length || (review.test_commands || []).length
  if (tests) {
    return {
      tone: 'accent',
      title: 'Ready to verify',
      detail: 'Codemap selected the tests that cover or import this change. Run them before calling the work done.',
    }
  }
  return {
    tone: 'ok',
    title: 'No red flags from codemap',
    detail: 'The completed diff analysis found no known-uncovered changed symbols. That is evidence from the graph, not proof the change is correct.',
  }
}

/** Ordered, bounded next actions for the current reports. */
export function todayActions({ status, review, affected, coverage, doctor } = {}) {
  const out = []
  const add = (priority, action) => out.push({ priority, ...action })
  const indexed = isIndexed(status, review)
  if (!indexed) {
    add(0, {
      id: 'index', tone: 'danger', title: 'Build the index',
      detail: 'Create the graph this cockpit and every other codemap answer reads.',
      action: { kind: 'index', precise: false }, button: 'Index now',
    })
    return finishActions(out)
  }
  if (review?.stale || staleCount(status, review) > 0) {
    add(10, {
      id: 'reindex', tone: 'warn', title: 'Reindex the working tree',
      detail: 'Refresh stale symbols and edges, then recompute Today.',
      action: { kind: 'index', precise: false }, button: 'Reindex',
    })
  }
  const commands = suggestedTestCommands(review, affected)
  if (commands.length) {
    add(20, {
      id: 'run-tests', tone: 'accent', title: 'Run the selected tests',
      detail: `${commands.length} command(s) derived from covering and affected tests.`,
      action: { kind: 'tests' }, button: 'Run tests',
    })
  }
  const uncovered = Number(review?.coverage?.uncovered_symbols || 0) + (review?.untested_symbols || []).length
  if (uncovered) {
    add(30, {
      id: 'inspect-uncovered', tone: 'danger', title: 'Inspect uncovered changes',
      detail: `${uncovered} changed symbol signal(s) have no covering test link.`,
      action: { kind: 'view', view: 'review' }, button: 'Inspect',
    })
  }
  if ((review?.changed_files || []).length) {
    add(40, {
      id: 'review-diff', tone: 'plain', title: 'Open the full review desk',
      detail: 'Read the actual diff beside blast radius, risk factors and gates.',
      action: { kind: 'view', view: 'review' }, button: 'Review',
    })
  }
  const totalFiles = Number(coverage?.total_files || 0)
  const coveredFiles = Number(coverage?.covered_files || 0)
  if (review?.analysis_complete === false || (totalFiles && coveredFiles < totalFiles)) {
    add(50, {
      id: 'precise-coverage', tone: 'warn', title: 'Close precise-coverage gaps',
      detail: totalFiles ? `${coveredFiles}/${totalFiles} files have a recorded precise pass.` : 'The review could not analyse every changed symbol.',
      action: { kind: 'feature', id: 'coverage' }, button: 'Coverage',
    })
  }
  const failedChecks = (doctor?.checks || []).filter((c) => !c.ok)
  if (failedChecks.length) {
    add(60, {
      id: 'doctor', tone: 'warn', title: 'Fix environment gaps',
      detail: failedChecks.map((c) => c.name).filter(Boolean).join(' · ') || `${failedChecks.length} doctor check(s) failed.`,
      action: { kind: 'feature', id: 'doctor' }, button: 'Doctor',
    })
  }
  if (!(review?.changed_files || []).length) {
    add(70, {
      id: 'explore', tone: 'accent', title: 'Start from an intent',
      detail: 'Describe the task and let codemap assemble the first exact neighborhoods.',
      action: { kind: 'view', view: 'search' }, button: 'Explore',
    })
  }
  add(80, {
    id: 'copy-brief', tone: 'plain', title: 'Copy an agent brief',
    detail: 'A compact Markdown summary of the diff, tests to run and trust limits.',
    action: { kind: 'brief' }, button: 'Copy brief',
  })
  return finishActions(out)
}

function finishActions(actions) {
  return actions
    .sort((a, b) => a.priority - b.priority)
    .slice(0, 6)
    .map(({ priority, ...action }) => action)
}

function shellQuotePath(file) {
  const s = String(file || '').replace(/\\/g, '/')
  if (/^[A-Za-z0-9_@%+=:,./-]+$/.test(s)) return s
  return `'${s.replace(/'/g, `'\\''`)}'`
}

function existingGoDirs(commands) {
  const dirs = new Set()
  for (const cmd of commands || []) {
    if (!String(cmd).startsWith('go test ')) continue
    for (const match of String(cmd).matchAll(/(?:^|\s)(\.\/[^\s']*)/g)) {
      dirs.add(match[1].replace(/^\.\//, '').replace(/\/$/, ''))
    }
  }
  return dirs
}

function commandsFromAffected(affected, coveredGoDirs = new Set()) {
  const goDirs = new Set()
  const out = new Set()
  for (const test of affected?.tests || []) {
    const file = String(test.file || '').replace(/\\/g, '/')
    if (!file) continue
    const ext = file.slice(file.lastIndexOf('.')).toLowerCase()
    if (ext === '.go') {
      const dir = file.includes('/') ? file.slice(0, file.lastIndexOf('/')) : ''
      goDirs.add(dir)
    } else if (['.ts', '.tsx', '.js', '.jsx'].includes(ext)) {
      out.add(`bun test ${shellQuotePath(file)}`)
    } else if (['.mjs', '.cjs'].includes(ext)) {
      out.add(`node --test ${shellQuotePath(file)}`)
    } else if (ext === '.py') {
      out.add(`pytest ${shellQuotePath(file)}`)
    }
  }
  for (const dir of [...goDirs].sort()) {
    if (!coveredGoDirs.has(dir)) out.add(dir ? `go test ./${dir}` : 'go test ./')
  }
  return [...out]
}

/** Review's own runnable commands first, then conservative affected-file commands. */
export function suggestedTestCommands(review, affected) {
  const out = []
  const seen = new Set()
  const push = (cmd) => {
    const s = String(cmd || '').trim()
    if (s && !seen.has(s)) {
      seen.add(s)
      out.push(s)
    }
  }
  for (const cmd of review?.test_commands || []) push(cmd)
  for (const next of review?.next || []) {
    if (next?.tool === 'terminal') push(next.args?.command)
  }
  for (const cmd of testCommands(review)) push(cmd.command)
  for (const cmd of commandsFromAffected(affected, existingGoDirs(out))) push(cmd)
  return out.slice(0, 10)
}

function symbolLine(s) {
  const name = s?.fqn || s?.symbol || '(symbol)'
  const where = s?.file ? ` (${s.file}${s.start_line ? `:${s.start_line}` : ''})` : ''
  return `- ${name}${where}`
}

/** Markdown an agent or PR description can consume without opening Studio. */
export function buildTodayBrief({ project = '', branch = '', status, review, affected, coverage, commands = [] } = {}) {
  const verdict = todayVerdict({ status, review, affected })
  const projectName = review?.project || String(project).split('/').filter(Boolean).pop() || 'project'
  const cov = review?.coverage || {}
  const lines = [
    `# Codemap brief — ${projectName}`,
    '',
    branch ? `Branch: ${branch}` : null,
    `Verdict: **${verdict.title}** — ${verdict.detail}`,
    '',
    '## Change',
    `- Files: ${(review?.changed_files || []).length}`,
    `- Symbols: ${review?.total_symbols ?? (review?.changed_symbols || []).length}`,
    `- Blast radius: ${(review?.blast_radius || []).length}`,
    `- Risk: ${review?.risk?.level || 'unknown'}${review?.risk?.score !== undefined ? ` (${Number(review.risk.score).toFixed(2)})` : ''}`,
    `- Coverage: ${cov.verdict || 'not assessed'} (${cov.covered_symbols ?? 0} covered · ${cov.uncovered_symbols ?? 0} uncovered · ${cov.unknown_symbols ?? 0} unknown)`,
    '',
    '## Changed symbols',
    ...((review?.changed_symbols || []).slice(0, 30).map(symbolLine)),
    (review?.changed_symbols || []).length ? null : '- None mapped by codemap.',
    '',
    '## Run these tests',
    commands.length ? '```sh' : 'No test command was selected.',
    ...commands,
    commands.length ? '```' : null,
    '',
    '## Trust limits',
    `- Call graph: ${review?.call_graph || affected?.call_graph || 'unknown'}. Unknown is not safe.`,
    `- Analysis complete: ${review?.analysis_complete === false ? 'no' : 'yes'}.`,
    coverage?.total_files ? `- Precise coverage: ${coverage.covered_files || 0}/${coverage.total_files} files.` : null,
    review?.stale ? '- The index was stale when this brief was generated; reindex before trusting it.' : null,
  ]
  return lines.filter((line) => line !== null && line !== undefined).join('\n')
}

function reviewArgs(mode, since) {
  const args = ['review', '--depth', '3']
  if (mode === 'staged') args.push('--staged')
  if (mode === 'since') args.push('--since', since || 'main')
  return args
}

function sortedChangedSymbols(review) {
  const untested = new Set((review?.untested_symbols || []).map((s) => s.fqn || s.symbol))
  return [...(review?.changed_symbols || [])].sort((a, b) => {
    const au = untested.has(a.fqn || a.symbol) ? 0 : 1
    const bu = untested.has(b.fqn || b.symbol) ? 0 : 1
    return au - bu || String(a.file || '').localeCompare(String(b.file || '')) || String(a.fqn || a.symbol || '').localeCompare(String(b.fqn || b.symbol || ''))
  })
}

export function todayView(ctx) {
  const host = h('div.view.today')
  const out = h('div.stack')
  let mode = 'working'
  let since = 'main'
  let data = null
  let loading = false
  let loadSeq = 0
  let indexing = false
  let task = ''
  let taskMode = 'change'
  let selectedCommand = ''
  const term = { running: false, runId: '', output: '', status: 'No test run yet.' }
  let termHost = null
  let commandInput = null
  let offTerminal = null

  async function load() {
    const seq = ++loadSeq
    loading = true
    mount(out, h('div.stack', [header(), spinner('reading the working tree and index…')]))
    const cwd = state.project

    // Phase 1 is the verdict itself. Render it as soon as status + review
    // answer; affected can legitimately take much longer on a large diff and
    // should not leave the daily home blank behind a spinner.
    const [statusRes, reviewRes] = await Promise.all([
      runArgs(['status'], { featureId: '__today_status', cwd, quiet: true }),
      runArgs(reviewArgs(mode, since), { featureId: 'review', cwd, quiet: true }),
    ])
    if (seq !== loadSeq) return
    data = {
      statusRes,
      reviewRes,
      affectedRes: null,
      coverageRes: null,
      doctorRes: null,
      status: statusRes?.ok ? statusRes.json : null,
      review: reviewRes?.ok || reviewRes?.gateFailed ? reviewRes.json : null,
      affected: null,
      coverage: null,
      doctor: null,
      detailsLoading: true,
    }
    let commands = suggestedTestCommands(data.review, data.affected)
    if (!selectedCommand || !commands.includes(selectedCommand)) selectedCommand = commands[0] || ''
    loading = false
    render()

    const [affectedRes, coverageRes, doctorRes] = await Promise.all([
      runArgs(affectedArgs({ mode, since, depth: 3 }), { featureId: 'affected', cwd, quiet: true }),
      runArgs(['coverage', '--top', '8'], { featureId: '__today_coverage', cwd, quiet: true }),
      runArgs(['doctor'], { featureId: '__today_doctor', cwd, quiet: true }),
    ])
    if (seq !== loadSeq) return
    data = {
      ...data,
      affectedRes,
      coverageRes,
      doctorRes,
      affected: affectedRes?.ok ? affectedRes.json : null,
      coverage: coverageRes?.ok ? coverageRes.json : null,
      doctor: doctorRes?.ok ? doctorRes.json : null,
      detailsLoading: false,
    }
    commands = suggestedTestCommands(data.review, data.affected)
    if (!selectedCommand || !commands.includes(selectedCommand)) selectedCommand = commands[0] || ''
    render()
  }

  function header() {
    return h('div.view-head', [
      h('div.vh-main', [
        h('h1', ['Today', state.git?.branch ? badge(state.git.branch, 'plain') : null]),
        h('div.vh-sub', 'Start and finish a change here: what changed, what it can break, what to run, and what codemap still cannot prove.'),
      ]),
      h('div.vh-actions', [
        segmented(),
        mode === 'since' ? h('input', { type: 'text', value: since, style: 'width:120px', oninput: (e) => (since = e.target.value), onkeydown: (e) => { if (e.key === 'Enter') load() } }) : null,
        h('button.btn.primary', { type: 'button', onclick: load }, '↻ Refresh'),
      ]),
    ])
  }

  function segmented() {
    return h('div.btn-row', MODES.map((m) => h('button.btn.sm', {
      type: 'button',
      class: mode === m.v ? 'btn sm on' : 'btn sm',
      onclick: () => { mode = m.v; load() },
    }, m.label)))
  }

  function render() {
    if (!data) return
    const { status, review, affected, coverage, doctor } = data
    const verdict = todayVerdict({ status, review, affected })
    const commands = suggestedTestCommands(review, affected)
    const actions = todayActions({ status, review, affected, coverage, doctor })
    const git = state.git || {}

    mount(out, h('div.stack', [
      header(),
      verdictStrip(verdict, status, review, affected, data.detailsLoading),
      taskStarter(),
      h('div.today-grid', [
        h('div.stack', [
          actionsCard(actions),
          changedSymbolsCard(review),
          changedFilesCard(review),
        ]),
        h('div.stack', [
          testsCard(commands, affected),
          trustCard(status, review, coverage, doctor),
          briefCard(commands),
        ]),
      ]),
      continueCard(git),
    ]))
  }

  function verdictStrip(verdict, status, review, affected, detailsLoading = false) {
    const stale = staleCount(status, review)
    return h('section.today-verdict', { dataset: { tone: verdict.tone } }, [
      h('div.today-verdict-main', [
        h('div.row.gap2', [
          badge(verdict.tone === 'ok' ? 'ready' : verdict.tone === 'danger' ? 'attention' : 'check first', verdict.tone),
          callGraphBadge(review?.call_graph || affected?.call_graph, review?.resolution),
          stale ? staleBadge(true, review?.staleness || status?.stale) : status ? badge('index fresh', 'ok') : null,
          review?.analysis_complete === false ? badge('analysis incomplete', 'danger') : null,
        ].filter(Boolean)),
        h('h2', verdict.title),
        h('p', verdict.detail),
      ]),
      h('div.today-metrics', [
        metric('Changed files', fmt.num((review?.changed_files || []).length), `${fmt.num(review?.total_symbols || 0)} mapped symbol(s)`),
        metric('Risk', String(review?.risk?.level || 'unknown').toUpperCase(), review?.risk?.score !== undefined ? `score ${Number(review.risk.score).toFixed(2)}` : '', verdict.tone === 'danger' ? 'danger' : ''),
        metric('Coverage', String(review?.coverage?.verdict || '—').toUpperCase(), review?.coverage ? `${review.coverage.covered_symbols} covered · ${review.coverage.uncovered_symbols} uncovered · ${review.coverage.unknown_symbols} unknown` : 'not assessed'),
        metric('Tests to run', detailsLoading && !affected ? '…' : fmt.num((affected?.tests || []).length), detailsLoading && !affected ? 'selecting affected tests…' : `${fmt.num((review?.blast_radius || []).length)} in blast radius`),
      ]),
    ])
  }

  function taskStarter() {
    const input = h('input', {
      type: 'text',
      value: task,
      placeholder: 'What are you working on? e.g. make Temporal answer-change retries safer',
      oninput: (e) => (task = e.target.value),
      onkeydown: (e) => { if (e.key === 'Enter') buildTaskBrief() },
    })
    return card({
      title: 'Start a task',
      sub: 'Codemap assembles the first brief; you stay in the editor and agent you already use.',
      body: h('div.today-task', [
        input,
        h('select', { title: 'Task mode', onchange: (e) => (taskMode = e.target.value) }, [
          h('option', { value: 'change', selected: taskMode === 'change' }, 'change'),
          h('option', { value: 'understand', selected: taskMode === 'understand' }, 'understand'),
          h('option', { value: 'debug', selected: taskMode === 'debug' }, 'debug'),
        ]),
        h('button.btn.primary', { type: 'button', onclick: buildTaskBrief }, 'Build brief'),
        h('button.btn', { type: 'button', onclick: exploreTask }, 'Explore'),
      ]),
      tight: true,
    })
  }

  async function buildTaskBrief() {
    const text = task.trim()
    if (!text) return toast('Describe the task first.', { tone: 'warn' })
    const res = await runArgs(['task-context', text, '--mode', taskMode], { featureId: 'task-context', cwd: state.project })
    if (res?.ok) ctx.openFeature('task-context', { task: text, mode: taskMode, at: [] }, res)
  }

  function exploreTask() {
    const text = task.trim()
    if (!text) return toast('Describe the task first.', { tone: 'warn' })
    ctx.go('search', { query: text, engine: 'explore' })
  }

  function actionsCard(actions) {
    return card({
      title: 'Do next',
      sub: 'the shortest useful path from this exact state',
      body: actions.length
        ? h('div.today-actions', actions.map((action) => h('div.today-action', [
            h('span', { class: `today-dot ${action.tone || ''}` }),
            h('div.today-action-main', [
              h('div.today-action-title', action.title),
              h('div.small.dim', action.detail),
            ]),
            h('button.btn.sm', { type: 'button', onclick: () => performAction(action) }, action.button || 'Open'),
          ])))
        : emptyState({ icon: '✓', title: 'Nothing queued', note: 'No codemap action is suggested for this state.' }),
      tight: true,
    })
  }

  async function performAction(action) {
    const target = action.action || {}
    if (target.kind === 'view') return ctx.go(target.view)
    if (target.kind === 'feature') return ctx.openFeature(target.id)
    if (target.kind === 'brief') return copyBrief(suggestedTestCommands(data.review, data.affected))
    if (target.kind === 'tests') {
      termHost?.scrollIntoView?.({ block: 'center' })
      if (selectedCommand) return runTerminal(selectedCommand)
      return toast('No runnable test command was selected.', { tone: 'warn' })
    }
    if (target.kind === 'index') return runIndex(!!target.precise)
  }

  async function runIndex(precise) {
    if (indexing) return
    const args = ['index', ...(precise ? ['--precise'] : [])]
    const ok = await confirmDialog({
      title: precise ? 'Run precise index?' : 'Reindex this project?',
      message: h('div.stack', [
        h('div', precise ? 'This runs the exact-resolution pass and can take a while on a large repository.' : 'This refreshes symbols and edges from the current working tree.'),
        h('code.inline', `codemap ${args.join(' ')}`),
      ]),
      confirmLabel: precise ? 'Index --precise' : 'Reindex',
      tone: 'primary',
    })
    if (!ok) return
    indexing = true
    toast(precise ? 'Precise index started…' : 'Reindex started…')
    const res = await runArgs(args, { featureId: precise ? 'index-precise' : 'index', cwd: state.project, stream: true })
    indexing = false
    if (res?.ok) {
      toast('Index refreshed. Recomputing Today…', { tone: 'ok' })
      await load()
    }
  }

  function changedSymbolsCard(review) {
    const symbols = sortedChangedSymbols(review)
    const untested = new Set((review?.untested_symbols || []).map((s) => s.fqn || s.symbol))
    return card({
      title: `Changed symbols (${symbols.length})`,
      sub: 'uncovered first; click to read the source with its wiring',
      actions: [h('button.btn.sm', { type: 'button', onclick: () => ctx.go('review') }, 'Full review')],
      body: symbols.length
        ? symList(symbols.slice(0, 12), {
            onPick: ctx.openSymbol,
            meta: (s) => untested.has(s.fqn || s.symbol) ? 'no covering test link' : (s.signature || ''),
          })
        : emptyState({ icon: '∅', title: 'No mapped symbols', note: 'Docs/config-only changes and unindexed files have no symbols to analyse.' }),
      tight: true,
    })
  }

  function changedFilesCard(review) {
    const files = review?.changed_files || []
    return card({
      title: `Changed files (${files.length})`,
      body: files.length
        ? h('div.symlist', files.slice(0, 16).map((f) => h('button.symrow', {
            type: 'button',
            style: 'grid-template-columns:minmax(0,1fr) auto',
            onclick: () => ctx.openFile(f.path || f.file),
          }, [
            h('span.sn', f.path || f.file),
            h('span.sp', `${f.status || ''}${f.symbols !== undefined ? ` · ${f.symbols} symbol(s)` : ''}`),
          ])))
        : emptyState({ icon: '✓', title: 'Clean scope', note: 'Nothing changed in the selected diff scope.' }),
      tight: true,
    })
  }

  function testsCard(commands, affected) {
    termHost = h('div.stack')
    commandInput = h('textarea', {
      rows: '2',
      value: selectedCommand,
      spellcheck: 'false',
      placeholder: 'A suggested test command will appear here. You can edit it before running.',
      oninput: (e) => (selectedCommand = e.target.value),
    })
    drawTerminal()
    return card({
      title: data?.detailsLoading && !affected ? 'Run these tests' : `Run these tests (${(affected?.tests || []).length})`,
      sub: data?.detailsLoading && !affected ? 'selecting affected tests…' : affected ? `selected by codemap affected · call graph ${affected.call_graph || '—'}` : 'affected unavailable',
      actions: [
        commands.length ? h('button.btn.sm', { type: 'button', onclick: () => copy(commands.join('\n')).then(() => toast('test commands copied', { tone: 'ok' })) }, 'Copy commands') : null,
        h('button.btn.sm', { type: 'button', onclick: () => ctx.openFeature('affected') }, 'Affected'),
      ],
      body: h('div.stack', [
        affected?.analysis_complete === false
          ? callout('warn', 'Selection may be incomplete', affected.note || 'Part of this diff has no usable call graph or the index is stale.')
          : null,
        data?.detailsLoading && !commands.length
          ? spinner('selecting affected tests…')
          : commands.length
            ? h('div.today-commands', commands.map((cmd) => h('button.today-command', {
              type: 'button',
              class: cmd === selectedCommand ? 'today-command on' : 'today-command',
              onclick: () => { selectedCommand = cmd; render() },
            }, [h('span.dim', '$'), h('code', cmd)])))
          : emptyState({ icon: '∅', title: 'No tests selected', note: 'No test covers, imports, or is part of this diff.' }),
        h('div.field', [
          h('label', 'Command to run in this project'),
          commandInput,
          h('div.hint', 'Runs in your login shell from the project root. Studio shows the exact command for confirmation first.'),
        ]),
        termHost,
        (affected?.tests || []).length
          ? h('details', [
              h('summary.small.muted', `${affected.tests.length} selected test file(s)`),
              h('div.symlist', { style: 'margin-top:6px' }, affected.tests.slice(0, 30).map((t) => h('button.symrow', {
                type: 'button', style: 'grid-template-columns:minmax(0,1fr) auto', onclick: () => ctx.openFile(t.file),
              }, [h('span.sn', t.file), h('span.sp', (t.reasons || []).join(' · '))]))),
            ])
          : null,
      ]),
      tight: true,
    })
  }

  function drawTerminal() {
    if (!termHost) return
    mount(termHost, h('div.stack', [
      h('div.btn-row', [
        h('button.btn.primary', {
          type: 'button',
          disabled: term.running || !selectedCommand.trim() ? 'disabled' : null,
          onclick: () => runTerminal((commandInput?.value || selectedCommand).trim()),
        }, term.running ? 'Running…' : '▷ Run command'),
        term.running ? h('button.btn.danger', { type: 'button', onclick: stopTerminal }, 'Stop') : null,
        h('button.btn', { type: 'button', onclick: () => { term.output = ''; term.status = 'Output cleared.'; drawTerminal() } }, 'Clear'),
        h('span.small.dim', term.status),
      ]),
      term.output ? h('pre.logbox.today-terminal-output', term.output) : null,
    ]))
  }

  async function runTerminal(command) {
    const cmd = String(command || '').trim()
    if (!cmd || term.running) return
    if (!window.studio.terminal?.run) return toast('The integrated test runner is unavailable in this build.', { tone: 'error' })
    const ok = await confirmDialog({
      title: 'Run this command?',
      message: h('div.stack', [
        h('div', 'It will run in your login shell from the project root, with your normal environment.'),
        h('pre.logbox', cmd),
      ]),
      confirmLabel: 'Run command',
      tone: 'danger',
    })
    if (!ok) return
    selectedCommand = cmd
    term.running = true
    term.output = ''
    term.status = 'Running…'
    drawTerminal()
    try {
      const res = await window.studio.terminal.run({ command: cmd, cwd: state.project, timeoutMs: 600000 })
      term.runId = res?.runId || term.runId
      if (res?.output) term.output = res.output
      term.status = res?.cancelled
        ? 'Cancelled.'
        : res?.timedOut
          ? `Timed out after ${fmt.ms(res.ms)}.`
          : `Exited ${res?.exitCode ?? '?'} in ${fmt.ms(res?.ms)}.`
      if (!res?.ok && !res?.cancelled) toast(res?.error || `Command exited ${res?.exitCode ?? '?'}`, { tone: 'warn', title: 'Test command finished' })
    } catch (err) {
      term.status = 'Could not start the command.'
      term.output += `\n${err?.message || String(err)}`
    } finally {
      term.running = false
      drawTerminal()
    }
  }

  async function stopTerminal() {
    if (term.runId) await window.studio.terminal?.cancel?.(term.runId)
  }

  function trustCard(status, review, coverage, doctor) {
    const checks = doctor?.checks || []
    const failed = checks.filter((c) => !c.ok)
    const pct = coverage?.total_files ? Math.round(((coverage.covered_files || 0) / coverage.total_files) * 100) : null
    return card({
      title: 'Trust & coverage',
      sub: 'what could make the verdict weaker than it looks',
      actions: [
        h('button.btn.sm', { type: 'button', onclick: () => ctx.openFeature('coverage') }, 'Coverage'),
        h('button.btn.sm', { type: 'button', onclick: () => ctx.openFeature('doctor') }, 'Doctor'),
      ],
      body: h('div.stack', [
        data?.detailsLoading ? spinner('checking precise coverage and environment…') : null,
        kv([
          ['call graph', review?.call_graph || 'unknown'],
          ['analysis complete', review?.analysis_complete === false ? 'no' : 'yes'],
          ['precise files', coverage ? `${fmt.num(coverage.covered_files || 0)} / ${fmt.num(coverage.total_files || 0)}${pct !== null ? ` (${pct}%)` : ''}` : 'unknown'],
          ['semantic backend', status?.semantic_backend || 'unknown'],
          ['environment', checks.length ? `${checks.length - failed.length}/${checks.length} ready` : 'unknown'],
        ]),
        pct !== null && pct < 100
          ? h('div.btn-row', [
              h('button.btn.sm.primary', { type: 'button', onclick: () => runIndex(true) }, 'Run precise pass'),
              h('span.small.dim', 'Exact edges replace name-based candidates file by file.'),
            ])
          : null,
        failed.length
          ? callout('warn', 'Capability gaps', failed.map((c) => `${c.name}: ${c.detail || 'not ready'}`).join('\n'))
          : checks.length ? callout('ok', 'Environment ready', 'Doctor found the toolchains and optional services it probes.') : null,
        review?.resolution ? h('div.small.dim', review.resolution) : null,
      ]),
      tight: true,
    })
  }

  function briefCard(commands) {
    return card({
      title: 'Share this state',
      sub: 'for an agent, a PR, or future you',
      body: h('div.stack', [
        h('div.small.muted', 'Copy a compact Markdown brief with the changed symbols, selected tests and trust limits — no screenshots, no raw JSON dump.'),
        h('div.btn-row', [
          h('button.btn.primary', { type: 'button', onclick: () => copyBrief(commands) }, 'Copy agent brief'),
          h('button.btn', { type: 'button', onclick: () => ctx.openFeature('docs') }, 'Agent guide'),
          h('button.btn', { type: 'button', onclick: () => ctx.go('agents') }, 'Agent setup'),
        ]),
      ]),
      tight: true,
    })
  }

  async function copyBrief(commands) {
    const brief = buildTodayBrief({
      project: state.project,
      branch: state.git?.branch || '',
      status: data?.status,
      review: data?.review,
      affected: data?.affected,
      coverage: data?.coverage,
      commands,
    })
    await copy(brief)
    toast('Agent brief copied.', { tone: 'ok' })
  }

  function continueCard(git) {
    return card({
      title: 'Continue from here',
      body: h('div.btn-row', [
        h('button.btn', { type: 'button', onclick: () => ctx.go('review') }, '◎ Review desk'),
        h('button.btn', { type: 'button', onclick: () => ctx.go('search') }, '⌕ Search'),
        h('button.btn', { type: 'button', onclick: () => ctx.go('processes') }, '⇶ Processes'),
        h('button.btn', { type: 'button', onclick: () => ctx.go('flow') }, '⇢ Flow'),
        h('button.btn', { type: 'button', onclick: () => ctx.go('dashboard') }, '◈ Health'),
        h('button.btn', { type: 'button', onclick: () => ctx.go('overview') }, 'Learn this repo'),
        git?.changedCount ? h('span.small.dim', `${git.changedCount} uncommitted path(s) in the working tree`) : null,
      ]),
      tight: true,
    })
  }

  if (window.studio.on) {
    offTerminal = window.studio.on('terminal:stream', (evt) => {
      if (!evt || (term.runId && evt.runId !== term.runId)) return
      if (evt.type === 'start') term.runId = evt.runId
      if (evt.type === 'output' && evt.text) {
        term.output = (term.output + evt.text).slice(-200000)
        drawTerminal()
      }
    })
  }

  mount(host, out)
  load()
  return {
    node: host,
    reload: load,
    destroy() {
      offTerminal?.()
    },
  }
}


