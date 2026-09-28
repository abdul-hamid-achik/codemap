/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Settings: binary resolution, active project, timeouts, appearance, global
// codemap flags, and the resolved environment the app hands to every child.

import { h, mount, clear, copy } from '../dom.mjs'
import { badge, callout, card, kv, toast, codeInline, emptyState, fmt, chip } from '../components.mjs'
import { state } from '../state.mjs'

export function settingsView(ctx) {
  const host = h('div.view')
  const out = h('div.stack')
  let info = null
  let binaryInfo = null
  let version = null

  async function load() {
    mount(out, h('div.stack', [h('div.view-head', [h('div.vh-main', [h('h1', 'Settings')])]), h('div.spinner')]))
    info = await window.studio.info()
    version = await window.studio.binary.version()
    binaryInfo = await window.studio.binary.resolve(state.settings.binaryPath || '')
    render()
  }

  function render() {
    const s = state.settings
    mount(
      out,
      h('div.stack', [
        h('div.view-head', [
          h('div.vh-main', [
            h('h1', ['Settings', version?.ok ? badge(version.version?.split('\n')[0] || 'codemap', 'ok') : badge('binary not found', 'danger')]),
            h('div.vh-sub', 'Codemap Studio is a client: every answer comes from the codemap CLI it spawns. These settings decide which binary, which project, and how long a command may run.'),
          ]),
          h('div.vh-actions', [h('button.btn', { type: 'button', onclick: load }, '↻ Re-detect')]),
        ]),

        card({
          title: 'codemap binary',
          body: h('div.stack', [
            version?.ok
              ? callout('ok', 'Connected', `${version.binary}\n${version.version || ''}`)
              : callout('danger', 'No binary resolved', [
                  h('div', version?.error || 'codemap was not found on PATH or in the repository.'),
                  h('div.small.muted', { style: 'margin-top:6px' }, 'Install it (go install ./cmd/codemap, or a release tarball), then set the path below.'),
                  binaryInfo?.tried?.length ? h('details', { style: 'margin-top:6px' }, [h('summary.small.muted', 'paths tried'), h('pre.logbox', { style: 'margin-top:4px' }, binaryInfo.tried.join('\n'))]) : null,
                ]),
            h('div.field', [
              h('label', 'Explicit binary path (optional)'),
              h('div.field-row', [
                h('input', {
                  type: 'text',
                  value: s.binaryPath || '',
                  placeholder: '/Users/you/go/bin/codemap  (blank = auto-detect)',
                  onchange: async (e) => {
                    await window.studio.settings.set({ binaryPath: e.target.value.trim() })
                    state.settings.binaryPath = e.target.value.trim()
                    toast('binary path saved — re-detecting…')
                    load()
                  },
                }),
                h('button.btn', { type: 'button', onclick: async () => { const p = await window.studio.project.pick(); if (p) toast(`picked ${p} — choose the codemap binary inside it`, { tone: 'warn' }) } }, 'Browse…'),
              ]),
              h('div.hint', 'Auto-detection order: this setting → $CODEMAP_BIN → <repo>/bin/codemap → <repo>/codemap → login-shell PATH. A GUI app does not inherit your shell PATH, so Codemap Studio resolves it through your login shell once at startup.'),
            ]),
            h('div.field', [h('label', 'Environment handed to codemap'), h('pre.logbox', { style: 'max-height:150px' }, [
              `PATH=${(info?.env?.PATH || '').split(':').slice(0, 14).join('\n      ')}`,
              info?.env?.CODEMAP_DATA ? `CODEMAP_DATA=${info.env.CODEMAP_DATA}` : '',
              info?.env?.CODEMAP_CONFIG ? `CODEMAP_CONFIG=${info.env.CODEMAP_CONFIG}` : '',
              info?.env?.CODEMAP_SEMANTIC_BACKEND ? `CODEMAP_SEMANTIC_BACKEND=${info.env.CODEMAP_SEMANTIC_BACKEND}` : '',
            ].filter(Boolean).join('\n'))]),
          ]),
        }),

        card({
          title: 'Project',
          body: h('div.stack', [
            kv([
              ['active project', state.project || '(none)'],
              ['registered projects', String(state.projects.length)],
            ]),
            h('div.field', [
              h('label', 'Switch project'),
              h('div.field-row', [
                h('input', { type: 'text', value: state.project || '', placeholder: '/absolute/path/to/project', onchange: (e) => ctx.setProject(e.target.value.trim()) }),
                h('button.btn.primary', { type: 'button', onclick: async () => { const p = await window.studio.project.pick(); if (p) ctx.setProject(p) } }, 'Choose folder…'),
              ]),
            ]),
            s.recentProjects?.length
              ? h('div.row.gap2', [h('span.small.dim', 'Recent:'), ...s.recentProjects.map((p) => chip(shortName(p), () => ctx.setProject(p), { title: p }))])
              : null,
            state.projects.length
              ? card({
                  title: 'All registered projects',
                  body: h('div.symlist', state.projects.map((p) => h('button.symrow', { type: 'button', onclick: () => ctx.setProject(p.path || p.root) }, [
                    h('span.sn', p.name || p.path),
                    h('span.sp', `${fmt.num(p.nodes)} nodes · ${fmt.num(p.files)} files`),
                    h('span.meta', p.path || p.root),
                  ]))),
                  tight: true,
                })
              : null,
          ]),
        }),

        card({
          title: 'Execution',
          body: h('div.form-grid', [
            numberField('Query timeout (ms)', s.timeoutMs, async (v) => { await save({ timeoutMs: v }) }, 'Kills a query command after this long. 0 disables the limit.'),
            numberField('Index timeout (ms)', s.indexTimeoutMs, async (v) => { await save({ indexTimeoutMs: v }) }, 'A full --precise reindex of a large repo can take many minutes.'),
            h('div.field', [
              h('label', 'Global codemap flags'),
              h('input', { type: 'text', value: s.extraArgs || '', placeholder: '--embed-model nomic-embed-text --ollama-url http://localhost:11434', onchange: (e) => save({ extraArgs: e.target.value }) }),
              h('div.hint', 'Appended to every invocation — the same precedence as typing them on the command line (config file < env < flags).'),
            ]),
            boolField('Auto-refresh context on project switch', s.autoRefresh !== false, (v) => save({ autoRefresh: v })),
            boolField('Search as you type', s.autoSearch !== false, (v) => save({ autoSearch: v })),
            boolField('Graph physics (force layout keeps moving)', s.graphPhysics !== false, (v) => save({ graphPhysics: v })),
          ]),
        }),

        card({
          title: 'Appearance',
          body: h('div.form-grid', [
            h('div.field', [
              h('label', 'Theme'),
              h('select', { onchange: (e) => save({ theme: e.target.value }).then(() => ctx.applyTheme(e.target.value)) }, [
                h('option', { value: 'dark', selected: s.theme !== 'light' }, 'dark'),
                h('option', { value: 'light', selected: s.theme === 'light' }, 'light'),
              ]),
            ]),
            h('div.field', [
              h('label', 'Density'),
              h('select', { onchange: (e) => save({ density: e.target.value }).then(() => ctx.applyPrefs()) }, [
                h('option', { value: 'comfortable', selected: s.density !== 'compact' }, 'comfortable'),
                h('option', { value: 'compact', selected: s.density === 'compact' }, 'compact'),
              ]),
            ]),
            h('div.field', [
              h('label', 'Base font size'),
              h('select', { onchange: (e) => save({ fontSize: Number(e.target.value) }).then(() => ctx.applyPrefs()) }, [12, 13, 14, 15].map((n) => h('option', { value: String(n), selected: Number(s.fontSize) === n }, `${n}px`))),
            ]),
          ]),
        }),

        card({
          title: 'About this app',
          body: h('div.stack', [
            kv([
              ['app', `Codemap Studio ${info?.version || ''}`],
              ['electron', info?.electron || ''],
              ['chromium', info?.chrome || ''],
              ['node', info?.node || ''],
              ['platform', info?.platform || ''],
              ['settings file', info?.userData ? `${info.userData}/codemap-studio.json` : ''],
              ['renderer root', info?.appRoot || ''],
            ]),
            callout('info', 'How this app talks to codemap', 'Every panel spawns `codemap … --json` as a child process with the project directory as cwd, then renders the structured report. Nothing is reimplemented: the CLI stays the single source of truth, including its exit-code taxonomy and its {ok:false,error,code,hint} failure envelope. The MCP panel additionally speaks newline-delimited JSON-RPC to `codemap serve`.'),
            h('div.btn-row', [
              h('button.btn', { type: 'button', onclick: () => ctx.go('catalog') }, '≡ Feature catalog'),
              h('button.btn', { type: 'button', onclick: () => ctx.go('mcp') }, '⌁ MCP inspector'),
              h('button.btn', { type: 'button', onclick: () => ctx.openFeature('docs') }, 'Agent guide'),
              h('button.btn', { type: 'button', onclick: () => copy(JSON.stringify({ info, version }, null, 2)).then(() => toast('diagnostics copied', { tone: 'ok' })) }, 'Copy diagnostics'),
            ]),
          ]),
        }),
      ]),
    )
  }

  async function save(patch) {
    const next = await window.studio.settings.set(patch)
    Object.assign(state.settings, next)
    render()
  }

  mount(host, out)
  load()
  return { node: host, reload: load }
}

function shortName(p) {
  const parts = String(p).split('/')
  return parts[parts.length - 1] || p
}

function numberField(label, value, onChange, hint) {
  return h('div.field', [
    h('label', label),
    h('input', { type: 'number', value: String(value ?? ''), min: '0', step: '1000', onchange: (e) => onChange(Number(e.target.value)) }),
    hint ? h('div.hint', hint) : null,
  ])
}

function boolField(label, value, onChange) {
  return h('div.field', [h('label', label), h('label.check', [h('input', { type: 'checkbox', checked: !!value, onchange: (e) => onChange(e.target.checked) }), h('span', 'enabled')])])
}
