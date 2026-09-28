/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Dashboard: the landing view. One screen that answers "is this project
// indexed, is the index honest, and what should I look at first?" — assembled
// from several parallel codemap calls rather than a single cached blob.

import { h, clear, mount } from '../dom.mjs'
import { runArgs } from '../runner.mjs'
import { badge, callout, card, metric, barChart, symList, emptyState, spinner, fmt, staleBadge, callGraphBadge, kv, table } from '../components.mjs'
import { state } from '../state.mjs'

export function dashboardView(ctx) {
  const host = h('div.view')
  let loading = false

  async function load() {
    if (loading) return
    loading = true
    mount(host, h('div.stack', [heroSkeleton(), spinner('reading the index…')]))
    const cwd = state.project
    const [status, doctor, coverage, hotspots, orphans, inconsistencies, daemon, readOrder] = await Promise.all([
      runArgs(['status'], { featureId: '__dash_status', cwd }),
      runArgs(['doctor'], { featureId: '__dash_doctor', cwd }),
      runArgs(['coverage', '--top', '12'], { featureId: '__dash_coverage', cwd }),
      runArgs(['hotspots', '--top', '10'], { featureId: '__dash_hotspots', cwd }),
      runArgs(['orphans', '--top', '10'], { featureId: '__dash_orphans', cwd }),
      runArgs(['inconsistencies'], { featureId: '__dash_inconsistencies', cwd }),
      runArgs(['daemon', 'status'], { featureId: '__dash_daemon', cwd }),
      runArgs(['read-order', '--top', '8'], { featureId: '__dash_readorder', cwd }),
    ])
    loading = false
    render({ status, doctor, coverage, hotspots, orphans, inconsistencies, daemon, readOrder })
  }

  function render(r) {
    const s = r.status?.ok ? r.status.json : null
    const d = r.doctor?.ok ? r.doctor.json : null
    const cov = r.coverage?.ok ? r.coverage.json : null
    const git = state.git || {}
    const stale = s?.stale || {}
    const staleTotal = (stale.changed || 0) + (stale.new || 0) + (stale.deleted || 0)
    const checks = d?.checks || []
    const failedChecks = checks.filter((c) => !c.ok)
    const incon = r.inconsistencies?.ok ? r.inconsistencies.json : null
    const inconTotal = incon ? (incon.dangling_annotations?.length || 0) + (incon.name_call_edges_on_resolved_files?.length || 0) + (incon.coverage_without_nodes?.length || 0) : null

    const hero = h('div.hero-card', [
      h('div.hero-title', s?.project || 'No project'),
      h('div.hero-path', s?.root || state.project || '—'),
      h('div.hero-stats', [
        stat(fmt.num(s?.files ?? '—'), 'files'),
        stat(fmt.num(s?.nodes ?? '—'), 'nodes'),
        stat(fmt.num(s?.edges ?? '—'), 'edges'),
        stat(fmt.num(s?.precise_edges ?? 0), 'precise'),
        stat(git.branch || '—', 'branch'),
        stat(s?.semantic_backend || '—', 'semantic'),
      ]),
      h('div.row.gap2', { style: 'margin-top:16px' }, [
        s?.registered === false ? badge('not registered', 'danger') : badge('registered', 'ok'),
        staleTotal ? staleBadge(true, stale) : s ? badge('index fresh', 'ok') : null,
        cov ? badge(`precise coverage ${fmt.pct(cov.covered_files || 0, cov.total_files || 0)}`, (cov.covered_files || 0) === (cov.total_files || 0) && cov.total_files ? 'ok' : 'warn') : null,
        r.daemon?.json?.running ? badge('daemon running', 'ok') : badge('daemon stopped', 'plain'),
        git.changedCount ? badge(`${git.changedCount} uncommitted file(s)`, 'info') : git.isRepo ? badge('clean tree', 'ok') : null,
        failedChecks.length ? badge(`${failedChecks.length} capability gap(s)`, 'warn') : checks.length ? badge('environment ready', 'ok') : null,
        inconTotal === null ? null : inconTotal ? badge(`${inconTotal} contradiction(s)`, 'danger') : badge('internally coherent', 'ok'),
      ].filter(Boolean)),
    ])

    const actions = h('div.dash-actions', [
      tile('Reindex', staleTotal ? `${staleTotal} file(s) drifted` : 'incremental, hash-based', () => ctx.openFeature('index')),
      tile('Precise pass', 'exact call edges + coverage', () => ctx.openFeature('index-precise')),
      tile('Review diff', 'what changed, which tests', () => ctx.openFeature('review')),
      tile('Architecture map', 'subsystems + bridges', () => ctx.go('map')),
      tile('Explore intent', 'natural-language entry', () => ctx.openFeature('explore')),
      tile('Task brief', 'mode-scoped orientation', () => ctx.openFeature('task-context')),
      tile('Coverage gaps', 'which files lack precise edges', () => ctx.openFeature('coverage')),
      tile('Dead-code candidates', 'orphans, never proof', () => ctx.openFeature('orphans')),
    ])

    const out = h('div.stack', [
      h('div.view-head', [
        h('div.vh-main', [
          h('h1', 'Dashboard'),
          h('div.vh-sub', 'Everything codemap knows about this project right now — health, honesty signals, and the shortest path into the graph.'),
        ]),
        h('div.vh-actions', [
          h('button.btn', { type: 'button', onclick: () => ctx.go('catalog') }, '≡ Feature catalog'),
          h('button.btn', { type: 'button', onclick: () => ctx.go('source') }, '⌸ Source browser'),
          h('button.btn.primary', { type: 'button', onclick: load }, '↻ Refresh'),
        ]),
      ]),

      h('div.dash-hero', [
        hero,
        h('div.stack', [
          card({ title: 'Do this next', body: actions, tight: true }),
        ]),
      ]),

      !s
        ? callout('danger', 'No index for this project', r.status?.error || 'Run “Index” to build the graph, or pick a different project from the chip in the title bar.', [
            h('button.btn.primary', { type: 'button', onclick: () => ctx.openFeature('index') }, 'Index now'),
            h('button.btn', { type: 'button', onclick: () => ctx.openFeature('init') }, 'Register project'),
          ])
        : null,

      staleTotal && s
        ? callout('warn', 'The index has drifted from the working tree', `${stale.changed || 0} changed · ${stale.new || 0} new · ${stale.deleted || 0} deleted. Every result below is provisional until you reindex — codemap reports staleness, it never silently acts on it.`, [
            h('button.btn.primary', { type: 'button', onclick: () => ctx.openFeature('index') }, 'Reindex'),
            h('button.btn', { type: 'button', onclick: () => ctx.openFeature('daemon-start') }, 'Watch instead'),
          ])
        : null,

      h('div.grid.c2', [
        card({
          title: 'Environment',
          sub: `${checks.length - failedChecks.length}/${checks.length} ready`,
          actions: [h('button.btn.sm', { type: 'button', onclick: () => ctx.openFeature('doctor') }, 'details')],
          body: checks.length
            ? h('div.stack', { style: 'gap:4px' }, checks.map((c) => h('div.row.gap2', { style: 'align-items:baseline' }, [
                h('span', { style: c.ok ? 'color:var(--ok)' : 'color:var(--warn)' }, c.ok ? '✓' : '!'),
                h('span.small', { style: 'color:var(--text-2);min-width:0;flex:1' }, c.name),
                h('span.small.dim.mono.truncate', { style: 'max-width:44%' }, c.detail || ''),
              ])))
            : emptyState({ note: 'doctor did not run' }),
        }),
        card({
          title: 'Languages & symbol kinds',
          body: h('div.split', [
            h('div', [h('h4', 'Languages'), barChart(Object.entries(s?.languages || {}).sort((a, b) => b[1] - a[1]).slice(0, 8).map(([label, value]) => ({ label, value })))]),
            h('div', [h('h4', 'Kinds'), barChart(Object.entries(s?.kinds || {}).sort((a, b) => b[1] - a[1]).slice(0, 8).map(([label, value]) => ({ label, value })), { tone: 'ok' })]),
          ]),
        }),
      ]),

      h('div.grid.c2', [
        card({
          title: 'Precise call-graph coverage',
          sub: cov ? `${fmt.num(cov.covered_files || 0)} of ${fmt.num(cov.total_files || 0)} files` : '',
          actions: [h('button.btn.sm', { type: 'button', onclick: () => ctx.openFeature('coverage') }, 'details')],
          body: cov
            ? h('div.stack', [
                coverageBar(cov.covered_files || 0, cov.total_files || 0),
                cov.by_directory?.length
                  ? table(
                      [
                        { key: 'directory', label: 'Worst-covered directory', cls: 'code' },
                        { key: 'uncovered', label: 'Uncovered', cls: 'num', render: (row) => fmt.num(row.uncovered_files ?? (row.total_files || 0) - (row.covered_files || 0)) },
                        { key: 'pct', label: '%', cls: 'num', render: (row) => fmt.pct(row.covered_files ?? 0, row.total_files ?? 0) },
                      ],
                      cov.by_directory.slice(0, 8),
                      { dense: true },
                    )
                  : callout('ok', 'Full precise coverage', 'Every indexed file has a recorded precise pass.'),
                h('div.row.gap2', [
                  h('span.small.dim', 'A file without precise coverage still answers name-based queries — with candidate semantics, not exact ones.'),
                ]),
              ])
            : emptyState({ note: 'coverage did not run' }),
        }),
        card({
          title: 'Compiled-knowledge consistency',
          actions: [h('button.btn.sm', { type: 'button', onclick: () => ctx.openFeature('inconsistencies') }, 'details')],
          body: incon
            ? h('div.stack', [
                inconTotal === 0
                  ? callout('ok', 'No contradictions detected', incon.note || 'An empty report is evidence of internal coherence, not of correctness.')
                  : callout('danger', `${inconTotal} contradiction(s)`, [
                      h('div', `dangling annotations: ${incon.dangling_annotations?.length || 0}`),
                      h('div', `name-based edges on precise-resolved files: ${incon.name_call_edges_on_resolved_files?.length || 0}`),
                      h('div', `coverage rows without indexed nodes: ${incon.coverage_without_nodes?.length || 0}`),
                    ]),
              ])
            : emptyState({ note: 'inconsistencies did not run' }),
        }),
      ]),

      h('div.grid.c2', [
        card({
          title: 'Where to start reading',
          actions: [h('button.btn.sm', { type: 'button', onclick: () => ctx.openFeature('read-order') }, 'full ranking')],
          body: r.readOrder?.json?.entries?.length
            ? symList(r.readOrder.json.entries.slice(0, 8), { onPick: ctx.openSymbol, meta: (x) => [x.reason, x.score !== undefined ? `score ${Number(x.score).toFixed(2)}` : null].filter(Boolean).join(' · ') })
            : emptyState({ note: 'no ranked entrypoints' }),
        }),
        card({
          title: 'Hubs (most-referenced)',
          actions: [h('button.btn.sm', { type: 'button', onclick: () => ctx.openFeature('hotspots') }, 'all hotspots')],
          body: r.hotspots?.json?.hotspots?.length
            ? h('div.stack', [
                callGraphBadge(r.hotspots.json.call_graph, r.hotspots.json.resolution),
                symList(r.hotspots.json.hotspots.slice(0, 8), { onPick: ctx.openSymbol, meta: (x) => `fan-in ${fmt.num(x.in_degree)}${x.shared_name > 1 ? ` · ${x.shared_name} definitions share this name (candidates)` : ''}` }),
              ])
            : emptyState({ note: 'no hubs' }),
        }),
      ]),

      h('div.grid.c2', [
        card({
          title: 'Dead-code candidates',
          sub: 'never proof — reflection, DI and props are invisible to the graph',
          actions: [h('button.btn.sm', { type: 'button', onclick: () => ctx.openFeature('orphans') }, 'all orphans')],
          body: r.orphans?.json?.orphans?.length
            ? symList(r.orphans.json.orphans.slice(0, 8), { onPick: ctx.openSymbol })
            : emptyState({ icon: '✓', title: 'No orphans in the top slice' }),
        }),
        card({
          title: 'Working tree',
          actions: [h('button.btn.sm', { type: 'button', onclick: () => ctx.openFeature('review') }, 'review desk')],
          body: h('div.stack', [
            kv([
              ['branch', git.branch || '—'],
              ['uncommitted files', String(git.changedCount ?? 0)],
              ['local branches', String(git.branches?.length ?? 0)],
            ]),
            git.commits?.length
              ? h('div.stack', { style: 'gap:2px' }, git.commits.slice(0, 6).map((c) => h('div.row.gap2', { style: 'align-items:baseline' }, [
                  h('span.mono.small.dim', c.short),
                  h('span.small.truncate', { style: 'flex:1;color:var(--text-2)' }, c.subject),
                  h('span.small.dim.mono', c.date),
                ])))
              : null,
            git.changed?.length
              ? h('details', [h('summary.small.muted', `${git.changed.length} changed path(s)`), h('div.logbox', { style: 'margin-top:6px;max-height:160px' }, git.changed.slice(0, 200).join('\n'))])
              : null,
          ]),
        }),
      ]),
    ])

    mount(host, out)
  }

  load()
  return { node: host, reload: load }
}

function stat(value, label) {
  return h('div.hero-stat', [h('div.hs-v', String(value)), h('div.hs-l', label)])
}

function heroSkeleton() {
  return h('div.view-head', [h('div.vh-main', [h('div.skeleton', { style: 'width:220px;height:22px' }), h('div.skeleton', { style: 'width:320px;height:12px;margin-top:8px' })])])
}

function coverageBar(covered, total) {
  const pctv = total ? Math.round((covered / total) * 100) : 0
  return h('div.stack', { style: 'gap:4px' }, [
    h('div.bar', { class: `bar ${pctv >= 80 ? 'ok' : pctv >= 40 ? 'warn' : 'danger'}` }, [h('i', { style: { width: `${Math.max(2, pctv)}%` } })]),
    h('div.small.dim.mono', `${fmt.num(covered)} / ${fmt.num(total)} files (${pctv}%) have a recorded precise pass`),
  ])
}

function tile(title, desc, onClick) {
  return h('button.action-tile', { type: 'button', onclick: onClick }, [h('div.at-t', title), h('div.at-d', desc)])
}
