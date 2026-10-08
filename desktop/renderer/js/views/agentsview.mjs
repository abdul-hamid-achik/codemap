/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Agents: one screen to make every AI coding harness on this machine use
// codemap. It joins `codemap agent list` (MCP registration + guidance file per
// harness) with a dry run of `codemap agent skill --harness all` (the portable
// using-codemap skill), and every write is previewed as a dry run first.

import { h, mount } from '../dom.mjs'
import { runArgs } from '../runner.mjs'
import { badge, callout, card, codeInline, copy, emptyState, metric, modal, shortPath, spinner, table, toast, fmt } from '../components.mjs'

// `agent list` and `agent skill` name Claude Code differently.
const SKILL_TO_LIST = { claude: 'claude-code' }
const LIST_TO_SKILL = { 'claude-code': 'claude' }

const PLAYBOOK_FORMATS = ['markdown', 'markdown-cli', 'claude-skill', 'cursor-rule', 'skill']

/** What a dry-run skill action means for a target that is already in place. */
export function skillState(target) {
  if (!target) return { label: 'not a skills harness', tone: 'plain', installed: null }
  switch (target.action) {
    case 'unchanged':
      return { label: 'installed', tone: 'ok', installed: true }
    case 'native':
      return { label: 'reads the library', tone: 'ok', installed: true }
    case 'updated':
      return { label: 'outdated', tone: 'warn', installed: true }
    case 'skipped':
      return { label: 'skipped', tone: 'warn', installed: false, reason: target.reason }
    default:
      return { label: 'not installed', tone: 'plain', installed: false }
  }
}

/** Library state from the dry run's top-level action. */
export function libraryState(action) {
  return { unchanged: ['installed', 'ok'], updated: ['outdated', 'warn'], created: ['not installed', 'plain'], skipped: ['hand-written skill in the way', 'warn'] }[action] || [action || '—', 'plain']
}

/**
 * One row per harness, joining MCP registration with skill state. `detected`
 * is the dry run without --harness: the skills harnesses installed here.
 */
export function harnessRows(list, skill, detected = null) {
  const here = new Set((detected?.targets || []).map((t) => t.harness))
  const rows = new Map()
  for (const d of Array.isArray(list) ? list : []) {
    rows.set(d.name, { name: d.name, present: !!d.present, registered: !!d.registered, config: d.config_path || '', mcp: true, skill: null, skillName: LIST_TO_SKILL[d.name] || null })
  }
  for (const t of skill?.targets || []) {
    const name = SKILL_TO_LIST[t.harness] || t.harness
    const row = rows.get(name) || { name, present: detected ? here.has(t.harness) : null, registered: null, config: '', mcp: false, skill: null }
    row.skill = t
    row.skillName = t.harness
    rows.set(name, row)
  }
  const out = [...rows.values()]
  for (const r of out) if (r.skill && !r.skillName) r.skillName = r.skill.harness
  // detected harnesses first, then the rest alphabetically
  return out.sort((a, b) => Number(b.present !== false) - Number(a.present !== false) || a.name.localeCompare(b.name))
}

export function agentsView(ctx) {
  const host = h('div.view')
  const out = h('div.stack')
  let global = false
  let format = 'markdown'

  async function load() {
    mount(out, [head(), spinner('asking each harness…')])
    const [list, skill, detected] = await Promise.all([
      runArgs(['agent', 'list'], { featureId: 'agent-list', quiet: true }),
      runArgs(['agent', 'skill', '--harness', 'all', '--dry-run'], { featureId: 'agent-skill', quiet: true }),
      runArgs(['agent', 'skill', '--dry-run'], { featureId: 'agent-skill', quiet: true }),
    ])
    render(list, skill, detected?.ok ? detected.json : null)
  }

  function head() {
    return h('div.view-head', [
      h('div.vh-main', [
        h('h1', 'Agents'),
        h('div.vh-sub', 'Make the AI coding harnesses on this machine use codemap: register the MCP server (with a generated guidance file) and install the using-codemap skill. Every write is shown as a dry run first, and only files codemap wrote are ever replaced.'),
      ]),
      h('div.vh-actions', h('div.btn-row', [
        h('label.check', { title: 'Write user-level config where the harness has one (default: this project’s files)' }, [h('input', { type: 'checkbox', checked: global, onchange: (e) => (global = e.target.checked) }), 'user-level config']),
        h('button.btn', { type: 'button', onclick: load }, '↻ Refresh'),
      ])),
    ])
  }

  function render(listRes, skillRes, detectedSkill) {
    const list = listRes?.ok ? listRes.json : null
    const skill = skillRes?.ok ? skillRes.json : null
    const rows = harnessRows(list, skill, detectedSkill)
    const detected = rows.filter((r) => r.present)
    const registered = rows.filter((r) => r.registered)
    const skilled = rows.filter((r) => r.skill && skillState(r.skill).installed)
    const [libLabel, libTone] = libraryState(skill?.action)
    const missingSkill = rows.filter((r) => r.skill && r.present !== false && !skillState(r.skill).installed && r.skill.action !== 'skipped')

    const nodes = [head()]
    if (!listRes?.ok) nodes.push(callout('danger', 'codemap agent list failed', listRes?.error || 'unknown error'))
    if (!skillRes?.ok) nodes.push(callout('danger', 'codemap agent skill failed', skillRes?.error || 'unknown error'))

    nodes.push(
      h('div.grid.c4', [
        metric('Harnesses detected', fmt.num(detected.length), `of ${rows.length} known`),
        metric('MCP registered', fmt.num(registered.length), registered.length ? registered.map((r) => r.name).join(', ') : 'none yet', registered.length ? 'ok' : ''),
        metric('Skill installed', fmt.num(skilled.length), skill ? `${skill.targets?.length || 0} skills harnesses` : '—', skilled.length ? 'ok' : ''),
        metric('Skill library', libLabel, skill?.skill ? shortPath(skill.skill) : '', libTone),
      ]),
    )

    if (skill && (skill.action !== 'unchanged' || missingSkill.length)) {
      nodes.push(callout('info', 'Install the skill everywhere it is missing', 'One command writes the skill to the shared library and links it into every detected skills harness.', [
        h('button.btn.sm.primary', { type: 'button', onclick: () => skillAction([], false) }, 'Preview install'),
      ]))
    }

    nodes.push(
      card({
        title: 'Harnesses',
        sub: 'MCP = `codemap agent setup` · skill = `codemap agent skill`',
        body: rows.length
          ? table(
              [
                { key: 'name', label: 'Harness', render: (r) => h('span.mono', r.name) },
                { key: 'present', label: 'Detected', render: (r) => (r.present === null ? badge('—', 'plain') : badge(r.present ? 'yes' : 'no', r.present ? 'ok' : 'plain')) },
                {
                  key: 'registered',
                  label: 'MCP server',
                  render: (r) => (r.mcp ? h('span', { title: r.config }, badge(r.registered ? 'registered' : 'not registered', r.registered ? 'ok' : 'plain')) : badge('n/a', 'plain', 'register this harness’s MCP server by hand')),
                },
                {
                  key: 'skill',
                  label: 'using-codemap skill',
                  render: (r) => {
                    const s = skillState(r.skill)
                    return h('span', { title: s.reason || r.skill?.path || '' }, badge(s.label, s.tone))
                  },
                },
                { key: 'config', label: 'Config', render: (r) => (r.config ? codeInline(shortPath(r.config)) : r.skill?.path ? codeInline(shortPath(r.skill.path)) : '—') },
                {
                  key: 'act',
                  label: '',
                  sort: false,
                  render: (r) => {
                    const s = skillState(r.skill)
                    return h('div.btn-row', [
                      r.mcp ? h('button.btn.sm', { type: 'button', onclick: (e) => (e.stopPropagation(), setupAction(r.name)) }, r.registered ? 'Re-register' : 'Register') : null,
                      r.skill && r.skill.action !== 'native' && !s.installed ? h('button.btn.sm', { type: 'button', onclick: (e) => (e.stopPropagation(), skillAction([r.skillName], false)) }, 'Install skill') : null,
                      r.skill && r.skill.action !== 'native' && s.installed ? h('button.btn.sm', { type: 'button', onclick: (e) => (e.stopPropagation(), skillAction([r.skillName], true)) }, 'Remove skill') : null,
                    ])
                  },
                },
              ],
              rows,
              { dense: true },
            )
          : emptyState({ title: 'No harness information', note: 'codemap agent list returned nothing.' }),
        tight: true,
      }),
    )

    nodes.push(playbookCard())
    mount(out, nodes)
  }

  // ---- actions: always a dry run first, applied from the preview -----------

  async function setupAction(name) {
    const base = ['agent', 'setup', name]
    if (global) base.push('--global')
    const dry = await runArgs([...base, '--dry-run'], { featureId: 'agent-setup' })
    if (!dry?.ok) return
    const plan = dry.json || {}
    const writes = plan.written || []
    preview({
      title: `Register codemap with ${name}`,
      body: setupPlan(plan),
      apply: writes.length ? 'Write these files' : null,
      idle: (plan.snippets || []).length ? null : undefined,
      onApply: async () => {
        const res = await runArgs(base, { featureId: 'agent-setup' })
        if (res?.ok) toast(`${name}: ${writes.length} file(s) written`, { tone: 'ok', title: 'Agents' })
        load()
      },
    })
  }

  async function skillAction(harnesses, remove) {
    const base = ['agent', 'skill']
    if (harnesses.length) base.push('--harness', harnesses.join(','))
    if (remove) base.push('--remove')
    const dry = await runArgs([...base, '--dry-run'], { featureId: 'agent-skill' })
    if (!dry?.ok) return
    const plan = dry.json || {}
    const changes = (plan.targets || []).filter((t) => !['unchanged', 'native', 'skipped'].includes(t.action))
    const libChanges = plan.action && plan.action !== 'unchanged' && plan.action !== 'skipped'
    preview({
      title: remove ? `Remove the skill from ${harnesses.join(', ')}` : harnesses.length ? `Install the skill for ${harnesses.join(', ')}` : 'Install the skill everywhere',
      body: skillPlan(plan),
      apply: changes.length || libChanges ? (remove ? 'Remove' : 'Install') : null,
      danger: remove,
      onApply: async () => {
        const res = await runArgs(base, { featureId: 'agent-skill' })
        if (res?.ok) toast(remove ? 'skill removed' : 'skill installed', { tone: 'ok', title: 'Agents' })
        load()
      },
    })
  }

  // `idle` replaces the "nothing to change" note; null drops it (the plan is
  // manual steps codemap does not write, like a Claude Code plugin install).
  function preview({ title, body, apply, danger = false, idle, onApply }) {
    const m = modal({
      title,
      width: '760px',
      body: h('div.stack', [body, apply || idle === null ? null : callout('ok', 'Nothing to change', 'This harness is already in the state this action would produce.')]),
      actions: [
        h('button.btn', { type: 'button', onclick: () => m.close() }, apply ? 'Cancel' : 'Close'),
        apply ? h('button.btn', { type: 'button', class: `btn ${danger ? 'danger' : 'primary'}`, onclick: () => (m.close(), onApply()) }, apply) : null,
      ],
    })
  }

  // ---- playbook --------------------------------------------------------------

  function playbookCard() {
    const body = h('div')
    const show = async () => {
      mount(body, spinner('rendering the playbook…'))
      const res = await runArgs(['agent', 'playbook', '--format', format], { featureId: 'agent-playbook', json: false, quiet: true })
      const text = res?.stdout || res?.error || ''
      mount(body, h('div.stack', [
        h('div.btn-row', [h('button.btn.sm', { type: 'button', onclick: () => copy(text).then(() => toast('playbook copied', { tone: 'ok' })) }, 'Copy')]),
        h('pre.code', { style: 'max-height:420px;white-space:pre-wrap' }, text),
      ]))
    }
    const select = h('select', { style: 'width:auto', onchange: (e) => ((format = e.target.value), show()) }, PLAYBOOK_FORMATS.map((v) => h('option', { value: v, selected: v === format }, v)))
    mount(body, h('div.btn-row', [h('button.btn.sm', { type: 'button', onclick: show }, 'Show playbook')]))
    return card({
      title: 'Playbook',
      sub: 'the generated “when to use codemap” guidance — for wiring a harness that is not listed',
      actions: [select],
      body,
    })
  }

  mount(host, out)
  load()
  return { node: host, reload: load }
}

function setupPlan(plan) {
  const writes = plan.written || []
  return h('div.stack', [
    writes.length
      ? table(
          [
            { key: 'action', label: 'Action', render: (w) => badge(w.action, w.action === 'unchanged' ? 'plain' : 'info') },
            { key: 'path', label: 'File', render: (w) => codeInline(shortPath(w.path)) },
          ],
          writes,
          { dense: true },
        )
      : null,
    ...(plan.snippets || []).map((s) =>
      h('div.stack', { style: 'gap:6px' }, [
        callout('warn', 'Do this yourself', s.reason || ''),
        s.path ? h('div.small.muted', ['file: ', codeInline(shortPath(s.path))]) : null,
        h('div.btn-row', [h('button.btn.sm', { type: 'button', onclick: () => copy(s.content || '').then(() => toast('copied', { tone: 'ok' })) }, 'Copy')]),
        h('pre.code', { style: 'white-space:pre-wrap' }, s.content || ''),
      ]),
    ),
    ...(plan.notes || []).map((n) => h('div.small.muted', n)),
  ])
}

function skillPlan(plan) {
  return h('div.stack', [
    h('div.small', ['library ', codeInline(shortPath(plan.skill || '')), ' → ', badge(plan.action || '—', plan.action === 'unchanged' ? 'plain' : 'info')]),
    (plan.targets || []).length
      ? table(
          [
            { key: 'harness', label: 'Harness', render: (t) => h('span.mono', t.harness) },
            { key: 'action', label: 'Action', render: (t) => badge(t.action, t.action === 'skipped' ? 'warn' : ['unchanged', 'native'].includes(t.action) ? 'plain' : 'info') },
            { key: 'path', label: 'Path', render: (t) => codeInline(shortPath(t.path || '')) },
            { key: 'reason', label: 'Note', render: (t) => t.reason || '—' },
          ],
          plan.targets,
          { dense: true },
        )
      : emptyState({ icon: '✎', title: 'No skills harness detected', note: 'Install Claude Code, Codex, OpenCode, Hermes or omp, or pick one explicitly.' }),
  ])
}
