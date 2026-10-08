/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Review desk: codemap's diff-scoped impact analysis next to the actual git
// diff, so "what did I change" and "what does it touch" sit in one place —
// plus `codemap affected` for the same scope, so the answer ends in the test
// files to run and the commands that run them.

import { h, clear, mount } from '../dom.mjs'
import { runArgs } from '../runner.mjs'
import { badge, callout, card, emptyState, metric, spinner, symList, toast, fmt, boolBadge, codeInline, download, copy, gateReasons } from '../components.mjs'
import { renderReport } from '../report.mjs'
import { state } from '../state.mjs'

export function reviewView(ctx) {
  const host = h('div.view')
  const out = h('div.stack')
  let mode = 'working'
  let since = 'main'
  let depth = 3
  let failOnRisk = ''
  let failOnUntested = false
  let failOnUncovered = false
  let lastJson = null

  function currentArgs() {
    const args = ['review', '--depth', String(depth)]
    if (mode === 'staged') args.push('--staged')
    if (mode === 'since') args.push('--since', since)
    if (failOnRisk) args.push('--fail-on-risk', failOnRisk)
    if (failOnUntested) args.push('--fail-on-untested')
    if (failOnUncovered) args.push('--fail-on-uncovered')
    return args
  }

  async function run() {
    mount(out, spinner('analysing the diff…'))
    const [res, diff, aff] = await Promise.all([
      runArgs(currentArgs(), { featureId: 'review' }),
      window.studio.gitDiff({ cwd: state.project, staged: mode === 'staged', since: mode === 'since' ? since : null }),
      runArgs(affectedArgs({ mode, since, depth }), { featureId: 'affected', quiet: true }),
    ])
    lastJson = res?.json || null
    render(res, diff, aff?.ok ? aff.json : null)
  }

  function render(res, diff, affected) {
    const json = res?.json || null
    const risk = json?.risk || {}
    const nodes = []

    nodes.push(
      h('div.view-head', [
        h('div.vh-main', [
          h('h1', [
            'Review desk',
            json ? badge(`mode: ${json.mode || mode}`, 'accent') : null,
            json?.analysis_complete === false ? badge('analysis incomplete', 'danger') : json ? badge('analysis complete', 'ok') : null,
            json?.stale ? badge('stale index', 'warn') : null,
          ]),
          h('div.vh-sub', 'Diff-scoped impact plus test selection: which symbols your changes touch, how far that reaches, which tests cover them, and one aggregate risk band you can gate on.'),
          h('div.cmdline', [h('span.dim', '$'), h('code', `codemap ${currentArgs().join(' ')} --json`)]),
          gates(),
        ]),
        h('div.vh-actions', controls()),
      ]),
    )

    if (res && !res.ok) {
      nodes.push(renderReport({ id: 'review', title: 'Review', render: 'review' }, res, ctx))
      mount(out, nodes)
      return
    }
    if (!json) {
      nodes.push(emptyState({ title: 'Run a review', note: 'Pick a diff scope and run the analysis.' }))
      mount(out, nodes)
      return
    }

    // ---- gate strip -------------------------------------------------------
    const gate = json.gate || {}
    const cov = json.coverage || null
    const blast = json.blast_radius || []
    const confirmed = blast.filter((n) => n.confidence === 'confirmed').length
    const tests = affected?.tests || []
    nodes.push(
      h('div.grid.c4', [
        metric('Aggregate risk', String(risk.level || 'unknown').toUpperCase(), `${risk.score !== undefined ? `score ${Number(risk.score).toFixed(2)} · ` : ''}${fmt.num((json.changed_files || []).length)} file(s), ${fmt.num(json.total_symbols || 0)} symbol(s)`, toneFor(risk.level)),
        metric('Test coverage', cov ? String(cov.verdict).toUpperCase() : '—', cov ? `${cov.covered_symbols} covered · ${cov.uncovered_symbols} uncovered · ${cov.unknown_symbols} unknown` : 'no non-test symbol assessed', coverageTone(cov?.verdict)),
        metric('Blast radius', fmt.num(blast.length), `depth ≤ ${json.depth ?? depth}${blast.length ? ` · ${fmt.num(confirmed)} confirmed` : ''}`),
        metric('Tests to run', affected ? fmt.num(tests.length) : '—', affected ? (affected.unmapped?.length ? `${affected.unmapped.length} changed file(s) unmapped` : 'every changed file mapped') : 'affected unavailable', affected && !affected.unmapped?.length ? 'ok' : ''),
      ]),
    )

    if (risk.level === 'unknown') {
      nodes.push(callout('warn', 'Risk is unknown, not low', 'At least one changed symbol has no usable call graph, or the analysis is incomplete. codemap never reports “unknown” as safe, and the risk gate never trips on it.'))
    }
    if (Array.isArray(risk.factors) && risk.factors.length) {
      nodes.push(card({ title: 'Why this risk', sub: 'factors behind the aggregate band, by severity', body: riskFactors(risk.factors), tight: true }))
    }
    if (gate.would_fail_on) {
      nodes.push(callout('danger', `Gate would fail: ${gateReasons(gate.would_fail_on)}`, 'A CI gate configured with these thresholds would exit 6 on this diff.'))
    }
    if (json.stale) {
      nodes.push(callout('warn', 'The index drifted from the working tree', `${json.staleness?.changed ?? 0} changed · ${json.staleness?.new ?? 0} new · ${json.staleness?.deleted ?? 0} deleted since the last index.`, [
        h('button.btn.sm.primary', { type: 'button', onclick: () => ctx.openFeature('index') }, 'Reindex'),
        h('button.btn.sm', { type: 'button', onclick: run }, 'Re-run review after'),
      ]))
    }
    if (Array.isArray(json.partial_errors) && json.partial_errors.length) {
      nodes.push(callout('danger', `${json.partial_errors.length} partial error(s)`, json.partial_errors.map((e) => (typeof e === 'string' ? e : `${e.kind || e.code || ''}: ${e.detail || e.message || JSON.stringify(e)}`)).join('\n')))
    }

    nodes.push(
      h('div.split', [
        card({
          title: 'The diff',
          sub: diff?.ok ? `${fmt.bytes(diff.bytes)}${diff.untracked?.length ? ` · ${diff.untracked.length} untracked file(s)` : ''}` : '',
          actions: [
            h('button.btn.sm', { type: 'button', onclick: () => copy(diff?.diff || '').then(() => toast('diff copied', { tone: 'ok' })) }, 'Copy'),
            h('button.btn.sm', { type: 'button', onclick: () => download(`review-${Date.now()}.diff`, diff?.diff || '') }, 'Save'),
          ],
          body: h('div.stack', [
            diff?.ok ? diffView(diff.diff) : callout('warn', 'No diff available', diff?.error || 'Not a git repository?'),
            diff?.untracked?.length ? h('details', [h('summary.small.muted', `${diff.untracked.length} untracked file(s) — outside the diff`), h('div.logbox', { style: 'margin-top:6px' }, diff.untracked.join('\n'))]) : null,
          ]),
          tight: true,
        }),
        h('div.stack', [
          (json.changed_files || []).length
            ? card({
                title: `Changed files (${json.changed_files.length})`,
                body: h('div.symlist', json.changed_files.map((f) => h('button.symrow', { type: 'button', style: 'grid-template-columns:minmax(0,1fr) auto', onclick: () => ctx.openFile(f.path || f.file) }, [
                  h('span.sn', f.path || f.file),
                  h('span.sp', [f.added !== undefined ? `+${f.added} ` : '', f.removed !== undefined ? `−${f.removed}` : '', f.mapped_symbols !== undefined ? ` · ${f.mapped_symbols} mapped symbol(s)` : ''].join('')),
                ]))),
                tight: true,
              })
            : callout('ok', 'No changed files', mode === 'staged' ? 'Nothing is staged.' : 'The working tree matches the selected ref.'),
          (json.changed_symbols || []).length
            ? card({ title: `Changed symbols (${json.changed_symbols.length})`, body: symList(json.changed_symbols, { onPick: ctx.openSymbol, meta: (s) => [s.risk?.level ? `risk ${s.risk.level}` : s.risk_level, s.blast_radius ? `blast ${Array.isArray(s.blast_radius) ? s.blast_radius.length : s.blast_radius}` : null, s.covering_tests ? `tests ${Array.isArray(s.covering_tests) ? s.covering_tests.length : s.covering_tests}` : null].filter(Boolean).join(' · ') }), tight: true })
            : null,
          (json.untested_symbols || []).length
            ? card({ title: `Untested changed symbols (${json.untested_symbols.length})`, body: symList(json.untested_symbols, { onPick: ctx.openSymbol }), tight: true })
            : null,
          (json.covering_tests || []).length ? card({ title: `Covering test functions (${json.covering_tests.length})`, body: symList(json.covering_tests, { onPick: ctx.openSymbol, meta: (t) => t.confidence || '' }), tight: true }) : null,
          json.test_commands?.length ? card({ title: 'Test commands', body: h('div.pill-list', json.test_commands.map((c) => codeInline(typeof c === 'string' ? c : JSON.stringify(c)))), tight: true }) : null,
        ]),
      ]),
    )

    nodes.push(testsCard(json, affected, ctx))

    nodes.push(card({ title: 'Full codemap report', sub: 'exactly what `codemap review --json` returned', body: renderReport({ id: 'review', title: 'Review', render: 'review', mcp: 'codemap_review' }, res, ctx) }))

    mount(out, nodes)
  }

  function controls() {
    return h('div.btn-row', [
      segmented(
        [
          { v: 'working', label: 'Working tree' },
          { v: 'staged', label: 'Staged' },
          { v: 'since', label: 'Since ref' },
        ],
        mode,
        (v) => {
          mode = v
          redrawControls()
        },
      ),
      mode === 'since' ? h('input', { type: 'text', value: since, style: 'width:130px', oninput: (e) => (since = e.target.value) }) : null,
      h('button.btn.primary', { type: 'button', onclick: run }, '▷ Run review'),
    ])
  }

  // CI gates: they never change the report, only the exit code a CI job would see.
  function gates() {
    return h('div.btn-row', { style: 'margin-top:8px' }, [
      h('span.small.muted', 'CI gates'),
      h('select', {
        style: 'width:auto',
        title: 'fail-on-risk gate',
        onchange: (e) => (failOnRisk = e.target.value),
      }, [
        h('option', { value: '', selected: failOnRisk === '' }, 'no risk gate'),
        ...['low', 'medium', 'high'].map((v) => h('option', { value: v, selected: failOnRisk === v }, `fail on risk ≥ ${v}`)),
      ]),
      h('label.check', [h('input', { type: 'checkbox', checked: failOnUntested, onchange: (e) => (failOnUntested = e.target.checked) }), 'fail on untested']),
      h('label.check', { title: 'Exit 6 only on known-uncovered symbols; unknown coverage never trips it' }, [h('input', { type: 'checkbox', checked: failOnUncovered, onchange: (e) => (failOnUncovered = e.target.checked) }), 'fail on uncovered']),
    ])
  }

  // Changing the scope re-runs the analysis: a different diff is a different
  // question, and caching a stale verdict here would be dishonest.
  function redrawControls() {
    run()
  }

  mount(host, out)
  run()

  return { node: host, run }
}

function toneFor(level) {
  return { low: 'ok', medium: 'warn', high: 'danger' }[level] || ''
}

function coverageTone(verdict) {
  return { covered: 'ok', partial: 'warn', uncovered: 'danger' }[verdict] || ''
}

/** argv for `codemap affected` over the same diff scope as the review. */
export function affectedArgs({ mode = 'working', since = 'main', depth = 3 } = {}) {
  const args = ['affected', '--depth', String(depth)]
  if (mode === 'staged') args.push('--staged')
  if (mode === 'since' && since) args.push('--since', since)
  return args
}

/** The runnable test commands review suggests in next[] (tool "terminal"). */
export function testCommands(json) {
  return (json?.next || []).filter((n) => n?.tool === 'terminal' && n.args?.command).map((n) => ({ command: n.args.command, why: n.why || '' }))
}

/** Group affected tests by their strongest reason: covers, imports, changed. */
export function groupAffected(tests) {
  const out = { covers: [], imports: [], changed: [] }
  for (const t of tests || []) {
    const reasons = t.reasons || []
    const key = reasons.some((r) => r.startsWith('covers:')) ? 'covers' : reasons.some((r) => r.startsWith('imports:')) ? 'imports' : 'changed'
    out[key].push(t)
  }
  return out
}

function riskFactors(factors) {
  const rows = [...factors].sort((a, b) => (b.severity || 0) - (a.severity || 0))
  return h(
    'div.stack',
    { style: 'gap:8px' },
    rows.map((f) => {
      const sev = Number(f.severity) || 0
      return h('div', { style: 'display:grid;grid-template-columns:170px 120px minmax(0,1fr);gap:12px;align-items:center' }, [
        h('span.mono.small', String(f.factor || '').replace(/_/g, ' ')),
        h('div.bar', { class: `bar ${sev >= 0.7 ? 'danger' : sev >= 0.4 ? 'warn' : 'ok'}` }, [h('i', { style: { width: `${Math.max(4, sev * 100)}%` } })]),
        h('span.small', f.detail || ''),
      ])
    }),
  )
}

function testsCard(json, affected, ctx) {
  const cmds = testCommands(json)
  if (!affected && !cmds.length) return null
  const tests = affected?.tests || []
  const groups = groupAffected(tests)
  const label = { covers: 'Cover a changed symbol', imports: 'Import a changed file', changed: 'Changed test files' }
  const row = (t) =>
    h('button.symrow', { type: 'button', style: 'grid-template-columns:minmax(0,1fr) auto', onclick: () => ctx.openFile(t.file) }, [
      h('span.sn', t.file),
      h('span.sp', { title: (t.reasons || []).join('\n') }, summarizeReasons(t.reasons)),
    ])
  return card({
    title: `Run these tests${tests.length ? ` (${tests.length})` : ''}`,
    sub: affected ? `codemap affected · call graph ${affected.call_graph || '—'}${affected.analysis_complete === false ? ' · selection may be incomplete' : ''}` : '',
    actions: [
      tests.length ? h('button.btn.sm', { type: 'button', onclick: () => copy(tests.map((t) => t.file).join('\n')).then(() => toast(`${tests.length} test path(s) copied`, { tone: 'ok' })) }, 'Copy paths') : null,
      h('button.btn.sm', { type: 'button', onclick: () => ctx.openFeature('affected') }, 'Open in Affected'),
    ],
    body: h('div.stack', [
      cmds.length
        ? h('div.stack', { style: 'gap:6px' }, cmds.map((c) =>
            h('div.cmdline', { title: c.why }, [
              h('span.dim', '$'),
              h('code', { style: 'flex:1;overflow:auto' }, c.command),
              h('button.btn.sm', { type: 'button', onclick: () => copy(c.command).then(() => toast('command copied', { tone: 'ok' })) }, 'Copy'),
            ]),
          ))
        : null,
      affected?.analysis_complete === false ? callout('warn', 'The selection may be incomplete', affected.note || 'Part of the diff has no usable call graph or the index is stale — absent tests are not proof that nothing else breaks.') : null,
      ...['covers', 'imports', 'changed'].filter((k) => groups[k].length).map((k) =>
        h('details', { open: k === 'covers' || tests.length <= 30 }, [
          h('summary.small.muted', `${label[k]} (${groups[k].length})`),
          h('div.symlist', { style: 'margin-top:6px' }, groups[k].slice(0, 300).map(row)),
        ]),
      ),
      affected?.unmapped?.length ? h('details', [h('summary.small.muted', `${affected.unmapped.length} changed file(s) the index could not map`), h('div.logbox', { style: 'margin-top:6px' }, affected.unmapped.map((u) => (typeof u === 'string' ? u : u.file || JSON.stringify(u))).join('\n'))]) : null,
      affected && !tests.length ? emptyState({ icon: '∅', title: 'No tests selected', note: 'No test covers, imports, or is part of this diff.' }) : null,
    ]),
    tight: true,
  })
}

function summarizeReasons(reasons = []) {
  const covers = reasons.filter((r) => r.startsWith('covers:'))
  if (covers.length) return covers.length === 1 ? covers[0].replace('covers:', 'covers ') : `covers ${covers.length} symbols`
  const imports = reasons.filter((r) => r.startsWith('imports:'))
  if (imports.length) return imports.length === 1 ? imports[0].replace('imports:', 'imports ') : `imports ${imports.length} files`
  return reasons.join(', ')
}

function segmented(options, value, onChange) {
  return h('div.btn-row', options.map((o) => h('button.btn.sm', { type: 'button', class: o.v === value ? 'btn sm on' : 'btn sm', onclick: () => onChange(o.v) }, o.label)))
}

/** Render a unified diff with per-line +/- colouring, split per file. */
export function diffView(diffText) {
  const text = String(diffText || '')
  if (!text.trim()) return emptyState({ icon: '✓', title: 'No changes', note: 'The selected scope has an empty diff.' })
  const files = []
  let cur = null
  for (const line of text.split('\n')) {
    if (line.startsWith('diff --git ')) {
      cur = { header: line.replace(/^diff --git a\//, '').replace(/ b\/.*$/, ''), lines: [] }
      files.push(cur)
      continue
    }
    if (!cur) {
      cur = { header: '(preamble)', lines: [] }
      files.push(cur)
    }
    cur.lines.push(line)
  }
  return h(
    'div.stack',
    { style: 'gap:10px;max-height:74vh;overflow:auto' },
    files.map((f) => {
      const adds = f.lines.filter((l) => l.startsWith('+') && !l.startsWith('+++')).length
      const dels = f.lines.filter((l) => l.startsWith('-') && !l.startsWith('---')).length
      const body = h('pre.code', { style: 'max-height:340px' })
      for (const l of f.lines) {
        const cls = l.startsWith('+') && !l.startsWith('+++') ? 'add' : l.startsWith('-') && !l.startsWith('---') ? 'del' : l.startsWith('@@') ? 'hl' : ''
        body.appendChild(h('span.code-line', { class: `code-line ${cls}` }, l || ' '))
      }
      return h('div.diff-file', [
        h('div.diff-file-head', [
          h('span.df-path', f.header),
          h('span.df-meta', [adds ? badge(`+${adds}`, 'ok') : null, dels ? badge(`−${dels}`, 'danger') : null]),
        ]),
        h('div.diff-file-body', body),
      ])
    }),
  )
}
