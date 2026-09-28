/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Review desk: codemap's diff-scoped impact analysis next to the actual git
// diff, so "what did I change" and "what does it touch" sit in one place.

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
  let lastJson = null

  function currentArgs() {
    const args = ['review', '--depth', String(depth)]
    if (mode === 'staged') args.push('--staged')
    if (mode === 'since') args.push('--since', since)
    if (failOnRisk) args.push('--fail-on-risk', failOnRisk)
    if (failOnUntested) args.push('--fail-on-untested')
    return args
  }

  async function run() {
    mount(out, spinner('analysing the diff…'))
    const [res, diff] = await Promise.all([
      runArgs(currentArgs(), { featureId: 'review' }),
      window.studio.gitDiff({ cwd: state.project, staged: mode === 'staged', since: mode === 'since' ? since : null }),
    ])
    lastJson = res?.json || null
    render(res, diff)
  }

  function render(res, diff) {
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
    nodes.push(
      h('div.grid.c4', [
        metric('Aggregate risk', String(risk.level || 'unknown').toUpperCase(), risk.score !== undefined ? `score ${Number(risk.score).toFixed(2)}` : '', toneFor(risk.level)),
        metric('Changed files', fmt.num((json.changed_files || []).length), `${fmt.num(json.total_symbols || 0)} symbol(s) mapped`),
        metric('Blast radius', fmt.num((json.blast_radius || []).length), `depth ≤ ${json.depth ?? depth}`),
        metric('Covering tests', fmt.num((json.covering_tests || []).length), (json.untested_symbols || []).length ? `${json.untested_symbols.length} changed symbol(s) untested` : 'every changed symbol covered', (json.untested_symbols || []).length ? 'danger' : 'ok'),
      ]),
    )

    if (risk.level === 'unknown') {
      nodes.push(callout('warn', 'Risk is unknown, not low', 'At least one changed symbol has no usable call graph, or the analysis is incomplete. codemap never reports “unknown” as safe, and the risk gate never trips on it.'))
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
          (json.covering_tests || []).length ? card({ title: `Tests to run (${json.covering_tests.length})`, body: symList(json.covering_tests, { onPick: ctx.openSymbol }), tight: true }) : null,
          json.test_commands?.length ? card({ title: 'Test commands', body: h('div.pill-list', json.test_commands.map((c) => codeInline(typeof c === 'string' ? c : JSON.stringify(c)))), tight: true }) : null,
        ]),
      ]),
    )

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
      h('select', {
        style: 'width:auto',
        title: 'fail-on-risk gate',
        onchange: (e) => (failOnRisk = e.target.value),
      }, [
        h('option', { value: '', selected: failOnRisk === '' }, 'no risk gate'),
        ...['low', 'medium', 'high'].map((v) => h('option', { value: v, selected: failOnRisk === v }, `fail on risk ≥ ${v}`)),
      ]),
      h('label.check', [h('input', { type: 'checkbox', checked: failOnUntested, onchange: (e) => (failOnUntested = e.target.checked) }), 'fail on untested']),
      h('button.btn.primary', { type: 'button', onclick: run }, '▷ Run review'),
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
