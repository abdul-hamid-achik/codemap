/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// First-run / recovery view. Shown whenever a precondition is missing, and it
// says exactly which one — never a blank screen.

import { h, mount } from '../dom.mjs'
import { badge, callout, card, spinner, codeInline, toast } from '../components.mjs'
import { FEATURES } from '../features.mjs'
import { state } from '../state.mjs'

export function onboardView(ctx) {
  const host = h('div.view')
  const out = h('div.stack')

  async function render() {
    mount(out, h('div.onboard', h('div.onboard-card', spinner('checking preconditions…'))))
    const info = await window.studio.info()
    const version = await window.studio.binary.version()
    const hasBinary = !!version?.ok
    const hasProject = !!state.project
    let status = null
    if (hasBinary && hasProject) {
      const r = await ctx.runSilent(['status'], '__onboard_status')
      status = r?.ok ? r.json : null
    }
    const indexed = !!status?.registered && (status?.nodes ?? 0) > 0

    const steps = [
      {
        title: 'codemap binary',
        done: hasBinary,
        detail: hasBinary ? `${version.binary} · ${(version.version || '').split('\n')[0]}` : `not found${info?.binary?.tried ? ` (tried ${info.binary.tried.length} path(s))` : ''}`,
        action: hasBinary ? null : { label: 'Settings → set the path', run: () => ctx.go('settings') },
      },
      {
        title: 'Choose a project',
        done: hasProject,
        detail: hasProject ? state.project : 'no project directory selected',
        action: hasProject ? null : { label: 'Choose folder…', run: async () => { const p = await window.studio.project.pick(); if (p) ctx.setProject(p) } },
      },
      {
        title: 'Index the project',
        done: indexed,
        detail: indexed ? `${status.nodes} nodes · ${status.edges} edges · ${status.files} files` : status ? 'registered but empty — run index' : hasProject ? 'checking…' : 'pick a project first',
        action: hasProject && !indexed ? { label: 'Run codemap index', run: () => ctx.openFeature('index') } : null,
      },
      {
        title: 'Environment for precise + semantic',
        done: null,
        detail: 'optional: gopls / typescript-language-server / pyright / Ollama',
        action: { label: 'Run doctor', run: () => ctx.openFeature('doctor') },
      },
    ]

    mount(
      out,
      h('div.onboard', [
        h('div.onboard-card', [
          h('h1', ['◈ Codemap Studio']),
          h('div.vh-sub', { style: 'color:var(--text-3);font-size:13px;line-height:1.6' }, [
            'A desktop workbench over the codemap code graph. Every capability the CLI exposes is wired here — ',
            h('strong', { style: 'color:var(--text-2)' }, `${FEATURES.length} panels`),
            ' across structural queries, semantic search, impact and risk analysis, review, annotations, branch/cache operations and the MCP server itself.',
          ]),
          h('div.onboard-steps', steps.map(stepRow)),
          !hasBinary
            ? callout('warn', 'codemap is required', 'This app is a client — it never reimplements the graph. Install codemap (or point Settings at an existing binary) to continue.', [
                h('button.btn.primary', { type: 'button', onclick: () => ctx.go('settings') }, 'Open settings'),
              ])
            : !hasProject
              ? callout('info', 'Pick the project you want to review', 'Codemap keeps its index centrally under $XDG_DATA_HOME/codemap, so one binary serves every registered project.', [
                  h('button.btn.primary', { type: 'button', onclick: async () => { const p = await window.studio.project.pick(); if (p) ctx.setProject(p) } }, 'Choose folder…'),
                ])
              : !indexed
                ? callout('info', 'Build the index', 'Run “Index” once. Add --precise to get exact call edges for Go, TypeScript, JavaScript and Python.', [
                    h('button.btn.primary', { type: 'button', onclick: () => ctx.openFeature('index') }, 'Index now'),
                    h('button.btn', { type: 'button', onclick: () => ctx.openFeature('index-precise') }, 'Index --precise'),
                  ])
                : callout('ok', 'Ready', 'Head to the Dashboard.', [h('button.btn.primary', { type: 'button', onclick: () => ctx.go('dashboard') }, 'Open dashboard')]),
          h('div.divider'),
          h('div.stack', { style: 'gap:6px' }, [
            h('h4', 'What you can do here'),
            ...[
              ['Understand a codebase', 'Dashboard → read order, architecture map, hotspots'],
              ['Answer “what breaks if I change this?”', 'Impact, file impact, refactor plan, risk'],
              ['Review a diff before pushing', 'Review desk: blast radius, covering tests, one risk band, CI gates'],
              ['Find code by meaning or by exact text', 'Unified search across four retrieval modes'],
              ['Keep the compiled knowledge honest', 'Coverage, inconsistencies, staleness, doctor'],
              ['Wire codemap into your agents', 'MCP inspector, harness registration, playbook, agent guide'],
            ].map(([t, d]) => h('div.row.gap2', { style: 'align-items:baseline' }, [h('span.small', { style: 'color:var(--text-2);min-width:290px' }, t), h('span.small.dim', d)])),
          ]),
        ]),
      ]),
    )
  }

  function stepRow(s) {
    return h('div.onboard-step', { class: s.done ? 'onboard-step done' : 'onboard-step' }, [
      h('div.os-n', s.done === null ? '?' : s.done ? '✓' : '·'),
      h('div', { style: 'min-width:0' }, [h('div.os-t', s.title), h('div.os-d', s.detail)]),
      s.action ? h('button.btn.sm', { type: 'button', onclick: s.action.run }, s.action.label) : null,
    ])
  }

  mount(host, out)
  render()
  return { node: host, reload: render }
}
