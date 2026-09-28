/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Run history: every codemap invocation this app made, replayable.

import { h, mount, clear } from '../dom.mjs'
import { badge, callout, card, emptyState, table, toast, fmt, chip } from '../components.mjs'
import { runArgs } from '../runner.mjs'
import { feature } from '../features.mjs'
import { state } from '../state.mjs'

export function historyView(ctx) {
  const host = h('div.view')
  const out = h('div.stack')
  let filter = ''

  async function render() {
    const all = await window.studio.settings.history()
    const q = filter.trim().toLowerCase()
    const rows = all
      .filter((r) => !q || `${r.featureId} ${(r.args || []).join(' ')}`.toLowerCase().includes(q))
      .map((r) => ({ ...r, feat: feature(r.featureId) }))

    const stats = {
      total: all.length,
      ok: all.filter((r) => r.ok).length,
      failed: all.filter((r) => !r.ok && !r.gateFailed).length,
      gates: all.filter((r) => r.gateFailed).length,
      ms: all.reduce((s, r) => s + (r.ms || 0), 0),
    }

    mount(
      out,
      h('div.stack', [
        h('div.view-head', [
          h('div.vh-main', [
            h('h1', ['Run history', badge(`${stats.total} run(s)`, 'accent')]),
            h('div.vh-sub', 'Everything Codemap Studio has executed against the codemap CLI in this workspace, with its exit code and duration. Re-run any entry, or open the panel it belongs to.'),
          ]),
          h('div.vh-actions', [
            h('input', { type: 'search', value: filter, placeholder: 'filter by feature or argument…', style: 'width:260px', oninput: (e) => { filter = e.target.value; render() } }),
            h('button.btn', { type: 'button', onclick: () => exportHistory(all) }, 'Export JSON'),
            h('button.btn.danger', { type: 'button', onclick: async () => { await window.studio.settings.clearHistory(); toast('history cleared', { tone: 'ok' }); render() } }, 'Clear'),
          ]),
        ]),
        h('div.grid.c4', [
          card({ title: 'Runs', body: h('div.metric', [h('div.m-label', 'total'), h('div.m-value', fmt.num(stats.total)), h('div.m-sub', 'kept: last 300')]), tight: true }),
          card({ title: 'Answered', body: h('div.metric.ok', [h('div.m-label', 'exit 0'), h('div.m-value', fmt.num(stats.ok)), h('div.m-sub', fmt.pct(stats.ok, stats.total) + ' of runs')]), tight: true }),
          card({ title: 'Failed', body: h('div.metric.danger', [h('div.m-label', 'exit 1–5'), h('div.m-value', fmt.num(stats.failed)), h('div.m-sub', 'operational / not found / index')]), tight: true }),
          card({ title: 'Wall time', body: h('div.metric.accent', [h('div.m-label', 'sum'), h('div.m-value', fmt.ms(stats.ms)), h('div.m-sub', stats.gates ? `${stats.gates} gate failure(s)` : 'no gate failures')]), tight: true }),
        ]),
        rows.length
          ? card({
              title: `Runs (${rows.length})`,
              body: table(
                [
                  { key: 'at', label: 'When', render: (r) => h('span.mono.small', { title: fmt.time(r.at) }, fmt.ago(r.at)) },
                  { key: 'featureId', label: 'Feature', render: (r) => h('span.mono', r.feat?.title || r.featureId) },
                  { key: 'args', label: 'argv', cls: 'code', render: (r) => h('span', { title: (r.args || []).join(' ') }, (r.args || []).join(' ') || '—') },
                  { key: 'exitCode', label: 'Exit', render: (r) => badge(String(r.exitCode ?? '?'), r.ok ? 'ok' : r.gateFailed ? 'warn' : 'danger') },
                  { key: 'code', label: 'Code', render: (r) => h('span.mono.small', r.code || '') },
                  { key: 'ms', label: 'Time', cls: 'num', render: (r) => fmt.ms(r.ms) },
                  { key: 'bytes', label: 'Bytes', cls: 'num', render: (r) => fmt.bytes(r.bytes || 0) },
                  {
                    key: 'act',
                    label: '',
                    sort: false,
                    render: (r) =>
                      h('div.btn-row', [
                        h('button.btn.sm', { type: 'button', onclick: (e) => { e.stopPropagation(); replay(r) } }, 're-run'),
                        r.feat ? h('button.btn.sm', { type: 'button', onclick: (e) => { e.stopPropagation(); ctx.openFeature(r.featureId) } }, 'panel') : null,
                      ]),
                  },
                ],
                rows,
                { dense: true },
              ),
            })
          : emptyState({ icon: '↺', title: 'No runs recorded', note: 'Run any feature and it will show up here.' }),
      ]),
    )
  }

  async function replay(row) {
    const args = (row.args || []).filter((a) => a !== '--json')
    toast(`re-running codemap ${args.join(' ')}`)
    const res = await runArgs(args, { featureId: row.featureId, cwd: row.cwd, json: !args.includes('--json') ? true : false })
    if (res?.ok && row.feat) ctx.openFeature(row.featureId, null, res)
    render()
  }

  async function exportHistory(all) {
    const r = await window.studio.save({ title: 'Export run history', defaultPath: `codemap-studio-history-${Date.now()}.json`, text: JSON.stringify(all, null, 2) })
    if (r?.ok) toast(`saved to ${r.path}`, { tone: 'ok' })
  }

  mount(host, out)
  render()
  return { node: host, reload: render }
}
