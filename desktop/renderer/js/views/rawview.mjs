/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Raw command runner: the escape hatch. Any codemap argv, typed by hand, with
// the same structured rendering — which is what makes the app's coverage of the
// CLI total rather than "whatever the registry remembered".

import { h, clear, mount, copy, download } from '../dom.mjs'
import { runArgs } from '../runner.mjs'
import { badge, callout, card, emptyState, spinner, toast, chip, fmt } from '../components.mjs'
import { renderReport } from '../report.mjs'
import { FEATURES } from '../features.mjs'
import { state } from '../state.mjs'
import { parseArgv } from '../argv.mjs'

export { parseArgv }

export function rawView(ctx, initial = {}) {
  const host = h('div.view')
  const out = h('div.stack')
  const box = h('textarea', {
    rows: '3',
    spellcheck: 'false',
    placeholder: 'impact --at internal/app/review.go:123 --depth 4',
    value: initial.text || '',
  })
  let useJson = true
  let last = null
  let recent = state.settings.recentRaw || []

  const suggestions = ['status', 'doctor', 'coverage --uncovered --top 30', 'map --top-subsystems 20', 'hotspots --top 20', 'orphans --top 30', 'review --staged', 'inconsistencies', 'structural-manifest', 'read-order --top 10', 'grep --regex "TODO|FIXME" --top 40', 'context --at internal/app/review.go:123']

  async function run() {
    const line = box.value.trim()
    if (!line) {
      toast('type a command first', { tone: 'warn' })
      return
    }
    let argv = parseArgv(line)
    if (argv[0] === 'codemap') argv = argv.slice(1)
    if (!argv.length) return
    recent = [line, ...recent.filter((r) => r !== line)].slice(0, 12)
    state.settings.recentRaw = recent
    window.studio.settings.set({ recentRaw: recent })
    mount(out, head(), spinner(`running codemap ${argv.join(' ')}…`))
    const res = await runArgs(argv, { featureId: 'raw', json: useJson })
    last = { res, argv, line }
    render()
  }

  function head() {
    return h('div.stack', [
      h('div.view-head', { style: 'margin-bottom:0' }, [
        h('div.vh-main', [
          h('h1', ['Raw command', badge('any argv', 'accent')]),
          h('div.vh-sub', 'Runs against the active project directory. Exit codes follow codemap’s taxonomy: 0 answered · 1 operational · 2 not found · 3 index missing · 4 index corrupt · 5 not a repo · 6 gate failed.'),
        ]),
        h('div.vh-actions', [h('button.btn', { type: 'button', onclick: () => ctx.go('catalog') }, '≡ Feature catalog')]),
      ]),
      h('div.card', [
        h('div.card-body', [
          h('div.field', [h('label', ['codemap ', h('span.dim', '(do not type the binary name)')]), box]),
          h('div.row.gap2', { style: 'margin-top:10px' }, [
            h('label.check', [h('input', { type: 'checkbox', checked: useJson, onchange: (e) => (useJson = e.target.checked) }), '--json']),
            h('button.btn.primary', { type: 'button', onclick: run }, '▷ Run'),
            h('button.btn', { type: 'button', onclick: () => { box.value = ''; box.focus() } }, 'Clear'),
            h('div.spacer', { style: 'flex:1' }),
            h('span.small.dim', '⌘↵ to run'),
          ]),
          h('div.row.gap2', { style: 'margin-top:10px' }, [h('span.small.dim', 'Try:'), ...suggestions.map((s) => chip(s, () => { box.value = s; run() }))]),
          recent.length ? h('div.row.gap2', { style: 'margin-top:8px' }, [h('span.small.dim', 'Recent:'), ...recent.map((r) => chip(r, () => { box.value = r; run() }))]) : null,
        ]),
      ]),
    ])
  }

  function render() {
    const nodes = [head()]
    if (last) {
      const { res, argv, line } = last
      nodes.push(
        h('div.result-head', [
          h('span.rh-title', `codemap ${line}`),
          h('div.rh-meta', [
            badge(`exit ${res.exitCode ?? '?'}`, res.ok ? 'ok' : res.gateFailed ? 'warn' : 'danger'),
            h('span', fmt.ms(res.ms)),
            h('span', fmt.bytes((res.stdout || '').length)),
            h('button.btn.sm', { type: 'button', onclick: (e) => copy(`codemap ${line}`).then(() => (e.target.textContent = 'copied')) }, 'Copy'),
            h('button.btn.sm', { type: 'button', onclick: () => download(`raw-${Date.now()}.json`, res.json ? JSON.stringify(res.json, null, 2) : res.stdout || '') }, 'Save'),
          ]),
        ]),
      )
      const guessed = guessFeature(argv)
      if (guessed) nodes.push(callout('info', 'This maps to a panel', `codemap ${argv.join(' ')} is the “${guessed.title}” feature — it has a form, saved inputs and a tailored view.`, [h('button.btn.sm', { type: 'button', onclick: () => ctx.openFeature(guessed.id) }, `Open ${guessed.title}`)]))
      nodes.push(renderReport(guessed || { id: 'raw', title: 'Raw command', render: 'auto' }, res, ctx))
    } else {
      nodes.push(emptyState({ icon: '▷', title: 'Nothing run yet', note: 'Type any codemap arguments above. The registry-driven panels cover every documented command; this runner covers everything else, including flags the panels do not expose.' }))
    }
    mount(out, nodes)
  }

  box.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
      e.preventDefault()
      run()
    }
  })

  mount(host, out)
  render()
  if (initial.text) run()
  return { node: host }
}

function guessFeature(argv) {
  if (!argv?.length) return null
  for (let len = Math.min(3, argv.length); len >= 1; len--) {
    const head = argv.slice(0, len)
    const match = FEATURES.find((f) => f.cmd && f.cmd.length === len && f.cmd.every((c, i) => c === head[i]))
    if (match) return match
  }
  return null
}
