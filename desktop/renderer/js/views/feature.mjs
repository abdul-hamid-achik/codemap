/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// The generic feature panel: a form generated from the registry, a live
// command preview, a streaming log for long runs, and the adaptive report
// renderer for the result. This is what makes every codemap surface usable.

import { h, clear, mount } from '../dom.mjs'
import { buildForm } from './form.mjs'
import { commandLine, buildArgs } from '../features.mjs'
import { renderReport, graphPayloadFor } from '../report.mjs'
import { badge, callout, spinner, toast } from '../components.mjs'
import { runFeature, runArgs } from '../runner.mjs'
import { state } from '../state.mjs'

export function featureView(feat, ctx, initial = {}) {
  let advanced = initial.advanced || false
  let values = { ...(initial.values || {}) }
  let form = null
  let result = initial.result || null
  let running = false

  const cmdPreview = h('code')
  const resultHost = h('div')
  const logHost = h('div')
  const runBtn = h('button.btn.primary', { type: 'button', onclick: () => doRun() }, runLabel(feat))
  // runFeature passes runKey: feat.id, which the main process maps to the live run.
  const stopBtn = h('button.btn.danger', { type: 'button', text: 'Stop', hidden: true, onclick: () => window.studio.cancel(feat.id) })

  function refreshPreview() {
    const v = form ? form.values() : values
    cmdPreview.textContent = commandLine(feat, v)
  }

  function rebuildForm(adv) {
    form = buildForm(feat, values, {
      advanced: adv,
      onChange: (v) => {
        values = v
        refreshPreview()
      },
    })
    mount(formHost, form.node)
    refreshPreview()
  }

  const formHost = h('div')
  rebuildForm(advanced)

  formHost.addEventListener('submit-form', () => doRun())

  async function doRun(override) {
    if (running) return
    const v = override ? { ...form.values(), ...override } : form.values()
    values = v
    running = true
    runBtn.disabled = true
    stopBtn.hidden = false
    mount(resultHost, spinner(`running ${feat.cmd.join(' ')}…`))
    clear(logHost)
    let logBox = null
    if (feat.stream || feat.long) {
      logBox = h('div.logbox')
      mount(logHost, logBox)
    }
    const res = await runFeature(feat, v, { runKey: feat.id })
    running = false
    runBtn.disabled = false
    stopBtn.hidden = true
    result = res
    clear(logHost)
    render()
    if (res?.ok && ctx.onFeatureRun) ctx.onFeatureRun(feat, v, res)
    // offer the graph when this report carries relations
    if (res?.ok && feat.graph) {
      const payload = graphPayloadFor(feat, res.json)
      if (payload) ctx.setGraphPayload?.(payload)
    }
    return res
  }

  function render() {
    clear(resultHost)
    if (!result) {
      resultHost.appendChild(
        h('div.empty', [
          h('div.e-ico', '▷'),
          h('div.e-title', feat.run === false ? '' : 'Ready to run'),
          h('div.e-note', feat.doc || feat.blurb || ''),
        ]),
      )
      return
    }
    resultHost.appendChild(renderReport(feat, result, ctx))
  }

  const head = h('div.view-head', [
    h('div.vh-main', [
      h('h1', [
        feat.title,
        feat.mutating ? badge('writes state', 'warn', 'This command changes the index, cache, or files.') : null,
        feat.mcp ? badge(feat.mcp, 'info', 'Also exposed as an MCP tool') : null,
        feat.long ? badge('long-running', 'plain') : null,
      ]),
      h('div.vh-sub', feat.doc || feat.blurb || ''),
      h('div.cmdline', [h('span.dim', '$'), cmdPreview]),
    ]),
    h('div.vh-actions', [
      feat.graph ? h('button.btn', { type: 'button', onclick: () => ctx.go?.('graph') }, '⇶ Graph explorer') : null,
      h('button.btn', { type: 'button', onclick: () => ctx.go?.('catalog') }, '≡ All features'),
    ]),
  ])

  const advToggle = h('button.advanced-toggle', {
    type: 'button',
    onclick: () => {
      advanced = !advanced
      advToggle.textContent = advanced ? '− hide advanced flags' : `+ show all ${countFlags(feat)} flags`
      values = form.values()
      rebuildForm(advanced)
    },
  }, advanced ? '− hide advanced flags' : `+ show all ${countFlags(feat)} flags`)

  const runbar = h('div.runbar', [
    h('div.rb-fields', [formHost, advToggle]),
    h('div.rb-actions', [
      stopBtn,
      h('button.btn', { type: 'button', text: 'Reset', onclick: () => { form.reset(); values = form.values(); refreshPreview() } }),
      runBtn,
    ]),
  ])

  const root = h('div.view', [head, h('div.feature-panel', [runbar, logHost, resultHost])])

  render()
  setTimeout(() => form.focusFirst(), 30)

  return {
    node: root,
    run: doRun,
    setValues(patch) {
      values = { ...form.values(), ...patch }
      form.set(patch)
      refreshPreview()
    },
    getValues: () => form.values(),
    autorun: initial.autorun ? () => doRun(initial.runOverride) : null,
  }
}

function countFlags(feat) {
  return (feat.args || []).length
}

function runLabel(feat) {
  if (feat.mode === 'daemon') return '▶ Start daemon'
  if (feat.mutating) return '⚡ Run'
  return '▷ Run'
}

/** A minimal panel for features that only need one text input (palette runs). */
export function quickRun(feat, value, ctx) {
  const values = {}
  const primary = (feat.args || []).find((a) => a.primary || a.required || a.kind === 'pos')
  if (primary) values[primary.name] = value
  return runFeature(feat, values).then((res) => {
    if (res?.ok) toast(`${feat.title}: done`, { tone: 'ok' })
    return res
  })
}

export { buildArgs }
