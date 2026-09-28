/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Feature catalog: every codemap capability the app integrates, grouped,
// searchable, and cross-checked against the binary's own `--help` output so the
// list can never silently drift from the CLI.

import { h, clear, mount, fuzzyScore } from '../dom.mjs'
import { FEATURES, GROUPS, APP_VIEWS, feature } from '../features.mjs'
import { badge, card, callout, emptyState, table, fmt, toast } from '../components.mjs'
import { runArgs } from '../runner.mjs'
import { state } from '../state.mjs'

const NESTED = {
  agent: ['list', 'playbook', 'setup'],
  cache: ['drop', 'export', 'import', 'list', 'restore', 'save'],
  config: ['path', 'show'],
  daemon: ['start', 'status', 'stop'],
}

export function catalogView(ctx) {
  const host = h('div.view')
  let filter = ''
  let mode = 'cards'
  let audit = null

  function matching() {
    if (!filter.trim()) return FEATURES
    const q = filter.trim()
    return FEATURES.map((f) => ({ f, s: Math.max(fuzzyScore(q, f.title), fuzzyScore(q, f.id), fuzzyScore(q, f.blurb), fuzzyScore(q, (f.cmd || []).join(' ')), fuzzyScore(q, f.mcp || ''), fuzzyScore(q, f.group)) }))
      .filter((x) => x.s > 0)
      .sort((a, b) => b.s - a.s)
      .map((x) => x.f)
  }

  function render() {
    const list = matching()
    const groups = new Map()
    for (const f of list) {
      if (!groups.has(f.group)) groups.set(f.group, [])
      groups.get(f.group).push(f)
    }

    const input = h('input', {
      type: 'search',
      value: filter,
      placeholder: 'Filter features… (try “precise”, “blast”, “cache”, “mcp”)',
      oninput: (e) => {
        filter = e.target.value
        render()
      },
    })
    setTimeout(() => {
      const el = host.querySelector('input[type=search]')
      if (el && !filter) el.focus()
    }, 20)

    const body =
      mode === 'cards'
        ? h(
            'div.catalog',
            GROUPS.filter((g) => groups.has(g.id)).map((g) =>
              h('div.cat-group', [
                h('div.section-head', [h('h2', `${g.icon}  ${g.label}`), h('span.small.dim', `${groups.get(g.id).length}`)]),
                h('div.cat-cards', groups.get(g.id).map((f) => featureCard(f, ctx))),
              ]),
            ),
          )
        : table(
            [
              { key: 'id', label: 'Feature', render: (r) => h('span.mono', r.id) },
              { key: 'title', label: 'Title' },
              { key: 'group', label: 'Group', render: (r) => badge(GROUPS.find((g) => g.id === r.group)?.label || r.group, 'plain') },
              { key: 'cmd', label: 'CLI', cls: 'code', render: (r) => (r.cmd?.length ? `codemap ${r.cmd.join(' ')}` : r.view ? `(view: ${r.view})` : '—') },
              { key: 'mcp', label: 'MCP tool', render: (r) => (r.mcp ? h('span.mono.small', r.mcp) : '—') },
              { key: 'args', label: 'Flags', cls: 'num', render: (r) => (r.args || []).length },
              { key: 'blurb', label: 'What it answers', render: (r) => h('span.small.muted', r.blurb) },
            ],
            list,
            { dense: true, onRow: (r) => ctx.openFeature(r.id) },
          )

    mount(
      host,
      h('div.stack', [
        h('div.view-head', [
          h('div.vh-main', [
            h('h1', [
              'Feature catalog',
              badge(`${FEATURES.length} features`, 'accent'),
              badge(`${GROUPS.length} groups`, 'plain'),
              badge(`${FEATURES.filter((f) => f.mcp).length} MCP tools`, 'info'),
            ]),
            h('div.vh-sub', 'Every capability codemap exposes — CLI command, MCP tool, and the flags this app wires for it. Nothing is hidden behind a hand-written subset.'),
          ]),
          h('div.vh-actions', [
            h('div.btn-row', [
              h('button.btn', { type: 'button', class: mode === 'cards' ? 'btn on' : 'btn', onclick: () => { mode = 'cards'; render() } }, 'Cards'),
              h('button.btn', { type: 'button', class: mode === 'table' ? 'btn on' : 'btn', onclick: () => { mode = 'table'; render() } }, 'Table'),
              h('button.btn', { type: 'button', onclick: () => runAudit() }, '⚖ Audit vs CLI'),
            ]),
          ]),
        ]),
        h('div.row.gap2', [h('div', { style: 'flex:1;min-width:220px' }, input), audit ? auditBadge() : null]),
        audit ? auditPanel() : null,
        list.length ? body : emptyState({ title: 'No feature matches', note: `Nothing in the registry matches “${filter}”.` }),
        appViewsCard(),
      ]),
    )
  }

  function auditBadge() {
    if (!audit) return null
    if (audit.loading) return badge('auditing…', 'accent')
    if (audit.error) return badge('audit failed', 'danger', audit.error)
    return audit.missing.length
      ? badge(`${audit.missing.length} CLI command(s) not wired`, 'danger')
      : badge(`all ${audit.commands.length} CLI commands wired`, 'ok')
  }

  function auditPanel() {
    if (!audit || audit.loading) return null
    if (audit.error) return callout('danger', 'Could not audit', audit.error)
    return card({
      title: 'CLI coverage audit',
      sub: `parsed from \`${audit.version || 'codemap --help'}\``,
      body: h('div.stack', [
        audit.missing.length
          ? callout('danger', 'Missing from the registry', audit.missing.map((m) => `codemap ${m}`).join('\n'))
          : callout('ok', 'Complete', 'Every command the binary advertises has a panel in this app.'),
        table(
          [
            { key: 'command', label: 'CLI command', render: (r) => h('span.mono', `codemap ${r.command}`) },
            { key: 'features', label: 'Panels', render: (r) => h('div.pill-list', r.features.map((id) => badge(id, 'accent'))) },
            { key: 'mcp', label: 'MCP tool', render: (r) => h('span.mono.small', r.mcp || '—') },
            { key: 'state', label: 'State', render: (r) => (r.features.length ? badge('wired', 'ok') : badge('missing', 'danger')) },
          ],
          audit.rows,
          { dense: true, onRow: (r) => r.features[0] && ctx.openFeature(r.features[0]) },
        ),
      ]),
    })
  }

  async function runAudit() {
    audit = { loading: true }
    render()
    const help = await runArgs(['--help'], { featureId: '__help', json: false, cwd: state.project })
    const ver = await runArgs(['version'], { featureId: '__ver', json: false, cwd: state.project })
    const text = help?.stdout || ''
    const section = text.split('Available Commands:')[1]?.split(/\n\s*\n|\nFlags:/)[0] || ''
    const commands = section
      .split('\n')
      .map((l) => /^\s{2,}([a-z][a-z0-9-]*)\s/.exec(l)?.[1])
      .filter(Boolean)
    if (!commands.length) {
      audit = { error: 'Could not parse “Available Commands” from codemap --help.', loading: false }
      render()
      return
    }
    const rows = commands
      .filter((c) => c !== 'help')
      .map((command) => {
        const feats = FEATURES.filter((f) => (f.cmd || [])[0] === command)
        return {
          command,
          features: feats.map((f) => f.id),
          mcp: feats.map((f) => f.mcp).filter(Boolean).join(', '),
          sub: NESTED[command] || [],
        }
      })
    const missing = rows.filter((r) => !r.features.length).map((r) => r.command)
    audit = { loading: false, rows, missing, commands, version: (ver?.stdout || '').trim().split('\n')[0] }
    render()
    if (missing.length) toast(`Audit: ${missing.length} CLI command(s) have no panel`, { tone: 'error', title: 'Incomplete coverage' })
    else toast(`Audit passed — all ${rows.length} CLI commands are wired (${FEATURES.length} panels)`, { tone: 'ok', title: 'CLI coverage' })
  }

  function appViewsCard() {
    return card({
      title: 'App surfaces',
      sub: 'Views that compose several features rather than mapping 1:1 onto a command',
      body: h('div.cat-cards', APP_VIEWS.map((v) => h('button.cat-card', { type: 'button', onclick: () => ctx.go(v.id) }, [
        h('div.cc-top', [h('span.cc-name', `${v.icon} ${v.id}`)]),
        h('div.cc-desc', v.label),
      ]))),
    })
  }

  render()
  return { node: host }
}

function featureCard(f, ctx) {
  return h('button.cat-card', { type: 'button', onclick: () => (f.view && f.run === false ? ctx.go(f.view) : ctx.openFeature(f.id)) }, [
    h('div.cc-top', [
      h('span.cc-name', f.cmd?.length ? `codemap ${f.cmd.join(' ')}` : f.id),
      h('div.spacer', { style: 'flex:1' }),
      f.mutating ? badge('writes', 'warn') : null,
      f.long ? badge('long', 'plain') : null,
    ]),
    h('div.cc-title', f.title),
    h('div.cc-desc', f.blurb),
    h('div.cc-foot', [
      f.mcp ? badge(f.mcp, 'info') : badge('CLI only', 'plain'),
      badge(`${(f.args || []).length} flag(s)`, 'plain'),
      f.graph ? badge('graph', 'accent') : null,
      f.render && f.render !== 'auto' ? badge(`view: ${f.render}`, 'plain') : null,
    ]),
  ])
}
