/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// MCP inspector: actually spawns `codemap serve`, performs the JSON-RPC
// handshake, lists the registered tools for a profile, shows their schemas,
// and can call a tool live. This is the agent surface, verified end to end.

import { h, clear, mount, copy, download } from '../dom.mjs'
import { badge, callout, card, emptyState, spinner, table, toast, fmt, codeInline } from '../components.mjs'
import { FEATURES } from '../features.mjs'
import { state } from '../state.mjs'

export function mcpView(ctx) {
  const host = h('div.view')
  const out = h('div.stack')
  let profile = 'full'
  let inspecting = false
  let last = null
  let selected = null
  let compare = null

  async function inspect(opts = {}) {
    if (inspecting) return
    inspecting = true
    mount(out, spinner(`handshaking with codemap serve --profile ${profile}…`))
    const res = await window.studio.mcp.inspect({ profile, cwd: state.project, call: opts.call || null, timeoutMs: 60000 })
    inspecting = false
    if (opts.call) {
      last = { ...last, call: res.call }
    } else {
      last = res
    }
    selected = opts.keepSelection ? selected : (res.tools || [])[0] || null
    render()
    if (!res.ok) toast(res.error || 'handshake failed', { tone: 'error', title: 'MCP' })
    else if (!opts.call) toast(`${(res.tools || []).length} tools registered under profile “${profile}”`, { tone: 'ok', title: 'MCP' })
  }

  async function compareProfiles() {
    mount(out, spinner('comparing profiles…'))
    const results = await Promise.all(['agent', 'core', 'full'].map((p) => window.studio.mcp.inspect({ profile: p, cwd: state.project, timeoutMs: 60000 })))
    const sets = results.map((r, i) => ({ profile: ['agent', 'core', 'full'][i], ok: r.ok, tools: new Set((r.tools || []).map((t) => t.name)), error: r.error }))
    const all = new Set([...sets[0].tools, ...sets[1].tools, ...sets[2].tools])
    compare = {
      sets,
      rows: [...all].sort().map((name) => ({ name, agent: sets[0].tools.has(name), core: sets[1].tools.has(name), full: sets[2].tools.has(name) })),
    }
    render()
  }

  function render() {
    const nodes = []
    nodes.push(
      h('div.view-head', [
        h('div.vh-main', [
          h('h1', ['MCP server', last?.ok ? badge(`${(last.tools || []).length} tools`, 'accent') : null, last ? badge(`profile: ${profile}`, 'info') : null]),
          h('div.vh-sub', 'codemap serve speaks newline-delimited JSON-RPC 2.0 over stdio. This panel spawns it for real, completes the initialize handshake, lists the registered tools for the selected profile, and can call one with arbitrary arguments.'),
          h('div.cmdline', [h('span.dim', '$'), h('code', `codemap serve --profile ${profile}`)]),
        ]),
        h('div.vh-actions', [
          h('select', {
            style: 'width:auto',
            onchange: (e) => {
              profile = e.target.value
              inspect()
            },
          }, [
            h('option', { value: 'full', selected: profile === 'full' }, 'full — every tool'),
            h('option', { value: 'agent', selected: profile === 'agent' }, 'agent — exactly the taught workflow'),
            h('option', { value: 'core', selected: profile === 'core' }, 'core — lean compatibility set'),
          ]),
          h('button.btn.primary', { type: 'button', onclick: () => inspect() }, '⌁ Connect & list'),
          h('button.btn', { type: 'button', onclick: compareProfiles }, '⚖ Compare profiles'),
        ]),
      ]),
    )

    if (!last) {
      nodes.push(callout('info', 'Not connected yet', 'Press “Connect & list” to spawn the server and enumerate its tools. The connection is closed as soon as the list arrives — nothing stays resident.'))
      nodes.push(registryCoverage())
      mount(out, nodes)
      return
    }
    if (!last.ok) {
      nodes.push(callout('danger', 'Handshake failed', last.error || 'unknown error'))
      if (last.stderr) nodes.push(h('pre.logbox', last.stderr))
      mount(out, nodes)
      return
    }

    nodes.push(
      h('div.grid.c4', [
        card({ title: 'Server', body: h('div.stack', [kvPairs([['name', last.server?.name], ['version', last.server?.version]]), last.protocol ? badge(`protocol ${last.protocol}`, 'plain') : null]), tight: true }),
        card({ title: 'Tools', body: h('div.metric.accent', [h('div.m-label', 'registered'), h('div.m-value', fmt.num((last.tools || []).length)), h('div.m-sub', `profile ${profile}`)]), tight: true }),
        card({ title: 'Capabilities', body: h('div.pill-list', Object.keys(last.capabilities || {}).map((k) => badge(k, 'info'))), tight: true }),
        card({ title: 'Handshake', body: h('div.metric.ok', [h('div.m-label', 'round trip'), h('div.m-value', fmt.ms(last.ms)), h('div.m-sub', 'initialize + tools/list')]), tight: true }),
      ]),
    )

    if (last.instructions) nodes.push(card({ title: 'Server instructions (sent to every agent)', body: h('div.small', { style: 'white-space:pre-wrap;line-height:1.6' }, last.instructions), tight: true }))

    nodes.push(
      h('div.split', [
        card({
          title: `Registered tools (${(last.tools || []).length})`,
          actions: [h('button.btn.sm', { type: 'button', onclick: () => download(`mcp-tools-${profile}-${Date.now()}.json`, JSON.stringify(last.tools, null, 2)) }, 'Export schemas')],
          body: h('div.symlist', { style: 'max-height:64vh' }, (last.tools || []).map((t) => h('button.symrow', {
            type: 'button',
            class: selected?.name === t.name ? 'symrow' : 'symrow',
            style: selected?.name === t.name ? 'background:var(--accent-soft);grid-template-columns:minmax(0,1fr) auto' : 'grid-template-columns:minmax(0,1fr) auto',
            onclick: () => {
              selected = t
              render()
            },
          }, [
            h('span.sn', t.name),
            h('span.sp', `${schemaParams(t).length} param(s)`),
            h('span.meta', (t.description || '').split('\n')[0].slice(0, 200)),
          ]))),
          tight: true,
        }),
        selected ? toolPanel(selected) : card({ title: 'Tool detail', body: emptyState({ note: 'select a tool' }) }),
      ]),
    )

    if (last.call) nodes.push(callPanel(last.call))
    nodes.push(registryCoverage())
    if (compare) nodes.push(comparePanel())
    mount(out, nodes)
  }

  function toolPanel(tool) {
    const params = schemaParams(tool)
    const argsBox = h('textarea', { rows: '8', spellcheck: 'false', placeholder: '{\n  "path": "' + (state.project || '') + '"\n}' }, JSON.stringify(defaultArgs(tool), null, 2))
    return card({
      title: tool.name,
      sub: params.length ? `${params.length} parameter(s)` : 'no parameters',
      actions: [
        registryFeatureFor(tool.name) ? badge(`panel: ${registryFeatureFor(tool.name).id}`, 'accent') : badge('no CLI panel', 'warn'),
        h('button.btn.sm', { type: 'button', onclick: (e) => copy(JSON.stringify(tool, null, 2)).then(() => (e.target.textContent = 'copied')) }, 'Copy schema'),
        registryFeatureFor(tool.name) ? h('button.btn.sm', { type: 'button', onclick: () => ctx.openFeature(registryFeatureFor(tool.name).id) }, 'Open CLI panel') : null,
      ],
      body: h('div.stack', [
        h('div.small.muted', { style: 'white-space:pre-wrap;line-height:1.6' }, tool.description || '(no description)'),
        params.length
          ? table(
              [
                { key: 'name', label: 'Parameter', render: (r) => codeInline(r.name) },
                { key: 'type', label: 'Type', render: (r) => badge(r.type || '?', 'plain') },
                { key: 'required', label: 'Required', render: (r) => (r.required ? badge('required', 'warn') : badge('optional', 'plain')) },
                { key: 'description', label: 'Description', render: (r) => h('span.small.muted', r.description || '') },
              ],
              params,
              { dense: true },
            )
          : callout('info', 'No parameters', 'This tool takes no input (e.g. codemap_doctor, codemap_projects, codemap_docs).'),
        h('div.field', [h('label', 'Arguments (JSON)'), argsBox, h('div.hint', 'Sent verbatim as tools/call arguments. Project-scoped tools accept an optional path.')]),
        h('div.btn-row', [
          h('button.btn.primary', {
            type: 'button',
            onclick: async () => {
              let parsed
              try {
                parsed = argsBox.value.trim() ? JSON.parse(argsBox.value) : {}
              } catch (err) {
                toast(`invalid JSON: ${err.message}`, { tone: 'error' })
                return
              }
              await inspect({ call: { name: tool.name, arguments: parsed }, keepSelection: true })
            },
          }, '⌁ Call tool'),
          h('button.btn', { type: 'button', onclick: () => { argsBox.value = '{}'; } }, 'Clear'),
        ]),
        h('details', [h('summary.small.muted', 'Full input schema'), h('pre.code', { style: 'margin-top:6px;max-height:280px' }, JSON.stringify(tool.inputSchema || tool.input_schema || {}, null, 2))]),
      ]),
    })
  }

  function callPanel(call) {
    const text = call.error ? JSON.stringify(call.error, null, 2) : JSON.stringify(call.result, null, 2)
    const content = call.result?.content?.[0]?.text
    let parsed = null
    if (content) {
      try {
        parsed = JSON.parse(content)
      } catch {
        parsed = null
      }
    }
    return card({
      title: `Call result — ${call.name}`,
      sub: call.error ? 'error' : 'ok',
      actions: [h('button.btn.sm', { type: 'button', onclick: (e) => copy(text).then(() => (e.target.textContent = 'copied')) }, 'Copy')],
      body: h('div.stack', [
        call.error ? callout('danger', 'Tool returned an error', JSON.stringify(call.error)) : null,
        h('div.field', [h('label', 'Arguments sent'), h('pre.code', JSON.stringify(call.arguments || {}, null, 2))]),
        parsed
          ? h('div.field', [h('label', 'Structured content (parsed)'), h('div.json', { style: 'max-height:44vh' }, h('pre', { style: 'margin:0;white-space:pre-wrap;word-break:break-word' }, JSON.stringify(parsed, null, 2)))])
          : null,
        h('details', { open: !parsed }, [h('summary.small.muted', 'Raw MCP result'), h('pre.code', { style: 'margin-top:6px;max-height:340px' }, text)]),
      ]),
    })
  }

  function registryCoverage() {
    const tools = new Set((last?.tools || []).map((t) => t.name))
    const registryMcp = FEATURES.filter((f) => f.mcp)
    const missingPanels = [...tools].filter((t) => !registryMcp.some((f) => f.mcp === t))
    const notLive = registryMcp.filter((f) => last && !tools.has(f.mcp))
    return card({
      title: 'Registry ↔ server coverage',
      sub: last ? `profile “${profile}” exposes ${tools.size} tools; the app wires ${registryMcp.length} MCP-backed panels` : 'connect to compare against the live server',
      body: h('div.stack', [
        last
          ? missingPanels.length
            ? callout('warn', `${missingPanels.length} live tool(s) have no dedicated panel`, missingPanels.join(', '))
            : callout('ok', 'Every live tool has a panel', '')
          : null,
        last && notLive.length
          ? callout('info', `${notLive.length} panel(s) are not registered under this profile`, notLive.map((f) => `${f.mcp} (${f.id})`).join(', '))
          : null,
        table(
          [
            { key: 'id', label: 'Panel', render: (r) => h('button.btn.sm.ghost', { type: 'button', onclick: () => ctx.openFeature(r.id) }, r.id) },
            { key: 'mcp', label: 'MCP tool', render: (r) => codeInline(r.mcp) },
            { key: 'cli', label: 'CLI', cls: 'code', render: (r) => `codemap ${r.cmd.join(' ')}` },
            { key: 'live', label: 'Live in profile', render: (r) => (last ? (tools.has(r.mcp) ? badge('registered', 'ok') : badge('not in profile', 'plain')) : badge('unknown', 'plain')) },
          ],
          registryMcp,
          { dense: true },
        ),
      ]),
    })
  }

  function comparePanel() {
    return card({
      title: 'Profile comparison',
      sub: compare.sets.map((s) => `${s.profile}: ${s.ok ? s.tools.size : s.error}`).join(' · '),
      body: table(
        [
          { key: 'name', label: 'Tool', render: (r) => codeInline(r.name) },
          { key: 'agent', label: 'agent', render: (r) => dot(r.agent) },
          { key: 'core', label: 'core', render: (r) => dot(r.core) },
          { key: 'full', label: 'full', render: (r) => dot(r.full) },
        ],
        compare.rows,
        { dense: true },
      ),
    })
  }

  function dot(on) {
    return on ? badge('✓', 'ok') : h('span.dim', '—')
  }

  mount(host, out)
  render()
  return { node: host, reload: () => inspect() }
}

function kvPairs(pairs) {
  return h('dl.kv', pairs.filter(([, v]) => v).flatMap(([k, v]) => [h('dt', k), h('dd', String(v))]))
}

function schemaParams(tool) {
  const schema = tool.inputSchema || tool.input_schema || {}
  const props = schema.properties || {}
  const required = new Set(schema.required || [])
  return Object.entries(props).map(([name, spec]) => ({
    name,
    type: spec.type || (spec.anyOf ? spec.anyOf.map((a) => a.type).join('|') : '?'),
    required: required.has(name),
    description: spec.description || '',
  }))
}

function defaultArgs(tool) {
  const out = {}
  for (const p of schemaParams(tool)) {
    if (!p.required) continue
    if (p.name === 'path') out.path = state.project || ''
    else if (p.type === 'number' || p.type === 'integer') out[p.name] = 0
    else if (p.type === 'boolean') out[p.name] = false
    else out[p.name] = ''
  }
  return out
}

function registryFeatureFor(toolName) {
  return FEATURES.find((f) => f.mcp === toolName) || null
}
