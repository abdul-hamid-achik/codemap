/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Overview: "Get to know <project>". The learning home — a hero that says what
// the project is, then a six-step path (big picture, abilities, entry points,
// core concepts, one feature end to end, notes). Each step loads on its own, so
// one failing command never blanks the page.

import { h, clear, mount } from '../dom.mjs'
import { badge, callout, errorBox, kindBadge, spinner, callGraphBadge, staleBadge, fmt } from '../components.mjs'
import { state } from '../state.mjs'
import { languageColor, isSupport } from '../palette.mjs'
import { languageBar, roleBadges, featureCard, surfaceCounts, surfaceLabel, representativeFeatures } from '../learn-ui.mjs'
import {
  LEARN_STEPS, loadProgress, setStep, markStep, loadAtlas, loadFeatures, loadAnnotations, loadMapEntrypoints, clearLearnCache,
  dominantLanguage, languageShares, featureFlowTarget, featureLabel, shortNum, baseName,
} from '../learn.mjs'

const STEPS = {
  picture: { n: 1, title: 'The big picture', why: 'Start with the shape of the repository: which directories hold the product and which hold supporting material.' },
  abilities: { n: 2, title: 'What it can do', why: 'The commands, tools and routes that people and agents actually call.' },
  entry: { n: 3, title: 'Where execution starts', why: 'Entry points. Follow one and the rest of the code starts to make sense.' },
  concepts: { n: 4, title: 'Core concepts', why: 'The types and functions the rest of the code is built around, most depended-on first.' },
  trace: { n: 5, title: 'Trace one feature end to end', why: 'Pick a big feature and watch it travel through the codebase, step by step.' },
  notes: { n: 6, title: 'Keep notes', why: 'Pin what you learn to the code, so the next reader — human or agent — starts ahead.' },
}

// A directory with no description of its own still has a shape worth stating.
function plainFacts(d) {
  const lang = dominantLanguage(d)
  const share = languageShares(d)[0]?.share
  const files = Number(d.files) || 0
  return `${fmt.num(files)} file${files === 1 ? '' : 's'}${lang ? `, mostly ${lang}${share && share < 0.95 ? ` (${Math.round(share * 100)}%)` : ''}` : ''} · no description found`
}

const TYPE_KINDS = new Set(['type', 'class', 'interface', 'struct', 'enum', 'trait'])

export function overviewView(ctx) {
  let progress = loadProgress()
  const host = h('div.view.overview')
  const heroHost = h('div', { style: 'display:grid;gap:14px' })
  const progHost = h('div')
  const bodies = {}
  const cards = {}
  const checks = {}

  // ------------------------------------------------------------ progress

  function explored() {
    return LEARN_STEPS.filter((id) => progress[id]).length
  }

  function drawProgress() {
    const n = explored()
    mount(progHost, h('div.ov-progress', [
      h('div.op-text', [h('b', `${n}/${LEARN_STEPS.length}`), h('span', n === LEARN_STEPS.length ? 'explored — you know your way around' : 'explored')]),
      h('div.op-bar', { role: 'progressbar', 'aria-valuemin': '0', 'aria-valuemax': String(LEARN_STEPS.length), 'aria-valuenow': String(n) }, LEARN_STEPS.map((id) => h('i', { class: progress[id] ? 'on' : '' }))),
    ]))
    for (const id of LEARN_STEPS) {
      cards[id]?.classList.toggle('done', !!progress[id])
      if (checks[id]) {
        checks[id].textContent = progress[id] ? '✓' : String(STEPS[id].n)
        checks[id].setAttribute('aria-pressed', progress[id] ? 'true' : 'false')
        checks[id].title = progress[id] ? 'Explored — click to unmark' : 'Click to mark as explored'
      }
    }
  }

  function mark(id) {
    progress = markStep(id)
    drawProgress()
  }

  function toggle(id) {
    progress = setStep(id, !progress[id])
    drawProgress()
  }

  // ------------------------------------------------------------- scaffold

  function stepCard(id) {
    const s = STEPS[id]
    const body = h('div.lp-body', spinner('loading…'))
    bodies[id] = body
    checks[id] = h('button.lp-check', { type: 'button', onclick: () => toggle(id) }, String(s.n))
    cards[id] = h('section.lp-step', { dataset: { step: id } }, [
      h('div.lp-rail', checks[id]),
      h('div.lp-main', [
        h('header.lp-head', [h('div', [h('h3', s.title), h('p.lp-why', s.why)])]),
        body,
      ]),
    ])
    return cards[id]
  }

  const failBody = (id, res) => {
    const notIndexed = res?.code === 'not_indexed' || res?.code === 'index_missing' || res?.json?.indexed === false
    mount(bodies[id], notIndexed
      ? h('div.small.muted', 'Not available until the project is indexed.')
      : errorBox(res || { error: 'no response' }))
  }

  // ----------------------------------------------------------------- hero

  function drawHero(res) {
    if (!res?.ok || !res.json) {
      const notIndexed = res?.code === 'not_indexed' || res?.code === 'index_missing' || res?.json?.indexed === false
      mount(heroHost, [
        h('section.ov-hero', [
          h('div.ov-hero-main', [
            h('div.ov-eyebrow', 'Get to know'),
            h('h1.ov-name', state.project.split('/').pop() || 'this project'),
            notIndexed
              ? callout('warn', 'This project is not indexed yet', 'Learn reads the code graph. Build the index once, then the map, the feature list and the traces all appear.', [
                  h('button.btn.primary', { type: 'button', onclick: () => ctx.openFeature('index') }, 'Index now'),
                  h('button.btn', { type: 'button', onclick: () => ctx.go('dashboard') }, 'Open Health'),
                ])
              : errorBox(res || { error: 'no response' }),
          ]),
        ]),
      ])
      return
    }
    const a = res.json
    const t = a.totals || a.tree || {}
    const st = state.status?.stale
    const staleTotal = st ? (st.changed || 0) + (st.new || 0) + (st.deleted || 0) : 0
    const stats = [
      [t.files, 'files'],
      [t.symbols, 'symbols'],
      [t.lines, 'lines'],
      [t.tests, 'tests'],
    ]
    mount(heroHost, [
      h('section.ov-hero', [
        h('div.ov-hero-main', [
          h('div.ov-eyebrow', 'Get to know'),
          h('h1.ov-name', a.project || state.project.split('/').pop()),
          a.summary
            ? h('p.ov-summary', a.summary)
            : h('p.ov-summary.empty', 'No project description found. A README.md whose first paragraph says what this is for would show up here.'),
          h('div.ov-stats', stats.map(([v, l]) => h('div.ov-stat', [h('b', shortNum(v ?? 0)), h('span', l)]))),
          h('div.row.gap2', { style: 'margin-top:14px' }, [
            callGraphBadge(a.call_graph, a.resolution),
            staleTotal || a.stale ? staleBadge(true, st) : badge('index fresh', 'ok'),
            a.summary_source ? badge(`from ${a.summary_source}`, 'plain', 'where the description came from') : null,
          ].filter(Boolean)),
        ]),
        h('div.ov-hero-side', [
          h('h4', 'Languages'),
          Object.keys(t.languages || {}).length ? languageBar(t.languages, { legend: true, max: 6 }) : h('div.small.muted', 'no languages indexed'),
          h('div.btn-row', { style: 'margin-top:16px' }, [
            h('button.btn.primary', { type: 'button', onclick: () => ctx.go('atlas') }, '▦ Open the Atlas'),
            h('button.btn', { type: 'button', onclick: () => ctx.go('features') }, '✦ Features'),
            h('button.btn', { type: 'button', onclick: () => ctx.go('flow') }, '⇢ Flow'),
          ]),
        ]),
      ]),
      staleTotal
        ? callout('warn', 'The index has drifted from the working tree', `${st.changed || 0} changed · ${st.new || 0} new · ${st.deleted || 0} deleted. What you read here is provisional until you reindex.`, [
            h('button.btn.primary', { type: 'button', onclick: () => ctx.openFeature('index') }, 'Reindex'),
          ])
        : null,
    ])
  }

  // ----------------------------------------------------------------- steps

  function drawPicture(res) {
    if (!res?.ok || !res.json?.tree) return failBody('picture', res)
    const dirs = (res.json.tree.children || []).filter((c) => c.type === 'dir')
    if (!dirs.length) {
      mount(bodies.picture, h('div.small.muted', 'This repository has no sub-directories — the whole thing is one level. Open the Atlas to browse its files.'))
      return
    }
    const max = Math.max(1, ...dirs.map((d) => Number(d.symbols) || 0))
    const row = (d) =>
      h('button.od-row', {
        type: 'button',
        class: `od-row${isSupport(d.roles) ? ' support' : ''}`,
        title: `Open ${d.path} in the Atlas`,
        onclick: () => {
          mark('picture')
          ctx.go('atlas', { prefix: d.path })
        },
      }, [
        h('span.od-name', `${baseName(d.path)}/`),
        h('span.od-sum', d.summary ? d.summary : h('em', plainFacts(d))),
        h('span.od-bar', h('i', { style: { width: `${Math.max(3, ((Number(d.symbols) || 0) / max) * 100)}%`, background: languageColor(dominantLanguage(d)) } })),
        h('span.od-n', [h('b', shortNum(d.symbols ?? 0)), ' symbols']),
        h('span.od-roles', roleBadges(d.roles)),
      ])
    const source = dirs.filter((d) => !isSupport(d.roles)).sort((a, b) => (b.symbols || 0) - (a.symbols || 0))
    const support = dirs.filter((d) => isSupport(d.roles)).sort((a, b) => (b.symbols || 0) - (a.symbols || 0))
    mount(bodies.picture, [
      h('div.od-list', source.map(row)),
      support.length ? h('div.od-support-title', 'Supporting material — tests, docs, config, benchmarks') : null,
      support.length ? h('div.od-list.support', support.map(row)) : null,
      h('div.btn-row', { style: 'margin-top:12px' }, [h('button.btn', { type: 'button', onclick: () => { mark('picture'); ctx.go('atlas') } }, 'Explore the whole map →')]),
    ])
  }

  function flowFor(f) {
    const target = featureFlowTarget(f)
    if (!target) return null
    return { ...target, label: featureLabel(f), description: f.description, feature: f }
  }

  function openFeatureFlow(stepId, f) {
    const opts = flowFor(f)
    if (!opts) {
      ctx.toast?.(`“${featureLabel(f)}” has no handler to trace`, { tone: 'warn' })
      return
    }
    mark(stepId)
    ctx.go('flow', opts)
  }

  function drawAbilities(res) {
    if (!res?.ok || !res.json) return failBody('abilities', res)
    const features = res.json.features || []
    if (!features.length) {
      mount(bodies.abilities, h('div.small.muted', 'No commands, tools, routes or programs were detected. The detectors know cobra, the Go MCP SDK, HTTP muxes and Next.js so far.'))
      return
    }
    const counts = surfaceCounts(features)
    const top = representativeFeatures(features, 5)
    mount(bodies.abilities, [
      h('div.ab-counts', counts.map(([s, n]) => h('button.ab-count', { type: 'button', onclick: () => { mark('abilities'); ctx.go('features') } }, [h('b', fmt.num(n)), h('span', surfaceLabel(s, n))]))),
      top.length ? h('div.small.dim', { style: 'margin:14px 0 8px' }, 'The five that touch the most code') : null,
      h('div.fc-list.compact', top.map((f) => featureCard(f, { compact: true, onOpen: (x) => openFeatureFlow('abilities', x) }))),
      h('div.btn-row', { style: 'margin-top:12px' }, [h('button.btn', { type: 'button', onclick: () => { mark('abilities'); ctx.go('features') } }, `See all ${fmt.num(features.length)} features →`)]),
    ])
  }

  async function drawEntry(res) {
    if (!res?.ok || !res.json) return failBody('entry', res)
    const programs = (res.json.features || []).filter((f) => f.kind === 'program')
    if (programs.length) {
      mount(bodies.entry, [
        h('div.fc-list.compact', programs.map((f) => featureCard(f, { compact: true, onOpen: (x) => openFeatureFlow('entry', x) }))),
      ])
      return
    }
    mount(bodies.entry, spinner('looking for likely entrypoints…'))
    const map = await loadMapEntrypoints()
    const eps = map?.ok ? map.json?.entrypoints || [] : []
    if (!eps.length) {
      mount(bodies.entry, h('div.small.muted', 'No program entry points were found, and the graph names no likely entrypoints either. Try the Features list for commands and routes.'))
      return
    }
    mount(bodies.entry, [
      h('div.small.dim', { style: 'margin-bottom:8px' }, 'No `main` programs were detected; these are the most likely entrypoints in the graph.'),
      h('div.ep-list', eps.slice(0, 6).map((s) =>
        h('button.ep-row', { type: 'button', onclick: () => { mark('entry'); ctx.go('flow', { selector: s.selector || { file: s.file, start_line: s.start_line, fqn: s.fqn, kind: s.kind }, symbol: s.fqn || s.symbol, label: s.symbol || s.fqn }) } }, [
          kindBadge(s.kind || 'function'),
          h('span.ep-name', s.symbol || s.fqn),
          h('span.ep-where', `${s.file}:${s.start_line}`),
          s.reason ? h('span.ep-why', s.reason) : null,
        ]),
      )),
    ])
  }

  function drawConcepts(res) {
    if (!res?.ok || !res.json?.tree) return failBody('concepts', res)
    const seen = new Set()
    const syms = []
    const add = (s, dir) => {
      const key = `${s.file}:${s.start_line}`
      if (seen.has(key)) return
      seen.add(key)
      syms.push({ ...s, dir })
    }
    for (const d of res.json.tree.children || []) for (const s of d.key_symbols || []) add(s, d.path)
    for (const s of res.json.tree.key_symbols || []) add(s, '')
    syms.sort((a, b) => (TYPE_KINDS.has(b.kind) ? 1 : 0) - (TYPE_KINDS.has(a.kind) ? 1 : 0) || (b.in_degree || 0) - (a.in_degree || 0))
    const top = syms.slice(0, 8)
    if (!top.length) {
      mount(bodies.concepts, h('div.small.muted', 'No key symbols yet. Without precise call edges codemap falls back to exported or documented symbols — run `codemap index --precise` for a better ranking.'))
      return
    }
    mount(bodies.concepts, h('div.kc-list', top.map((s) =>
      h('div.kc-row', [
        h('button.kc-main', { type: 'button', title: `${s.file}:${s.start_line}`, onclick: () => { mark('concepts'); ctx.openSymbol(s) } }, [
          kindBadge(s.kind || 'symbol'),
          h('span.kc-name', s.fqn || s.symbol),
          s.doc ? h('span.kc-doc', String(s.doc).split('\n')[0]) : h('span.kc-doc.empty', 'undocumented'),
          h('span.kc-dir', s.dir || ''),
          s.in_degree ? h('span.kc-deg', `${fmt.num(s.in_degree)} callers`) : null,
        ]),
        h('button.btn.sm.ghost', { type: 'button', title: 'Trace this end to end', onclick: () => { mark('concepts'); ctx.go('flow', { selector: s.selector, symbol: s.fqn || s.symbol, label: s.symbol }) } }, 'flow'),
      ]),
    )))
  }

  function drawTrace(res) {
    if (!res?.ok || !res.json) return failBody('trace', res)
    let picks = representativeFeatures(res.json.features, 3, { skipKinds: ['program'] })
    if (!picks.length) picks = representativeFeatures(res.json.features, 3)
    if (!picks.length) {
      mount(bodies.trace, [
        h('div.small.muted', 'No feature with a traceable handler yet. Type any symbol into Flow to follow it.'),
        h('div.btn-row', { style: 'margin-top:10px' }, [h('button.btn', { type: 'button', onclick: () => ctx.go('flow') }, 'Open Flow')]),
      ])
      return
    }
    mount(bodies.trace, [
      h('div.fc-list', picks.map((f) => featureCard(f, { onOpen: (x) => openFeatureFlow('trace', x) }))),
    ])
  }

  function drawNotes(res) {
    if (!res?.ok || !res.json) return failBody('notes', res)
    const items = res.json.annotations || []
    const latest = items.slice().sort((a, b) => (b.id || 0) - (a.id || 0)).slice(0, 3)
    mount(bodies.notes, [
      h('div.nt-top', [
        h('div.nt-count', [h('b', fmt.num(items.length)), h('span', items.length === 1 ? 'note pinned to the code' : 'notes pinned to the code')]),
        h('div.btn-row', [
          h('button.btn.primary', { type: 'button', onclick: () => { mark('notes'); ctx.openFeature('annotate') } }, '＋ Add a note'),
          items.length ? h('button.btn', { type: 'button', onclick: () => { mark('notes'); ctx.openFeature('annotations') } }, 'All notes') : null,
        ]),
      ]),
      latest.length
        ? h('div.nt-list', latest.map((a) =>
            h('div.nt-row', [
              badge(a.source || 'note', 'info'),
              h('span.nt-target', a.target || a.symbol || (a.from && a.to ? `${a.from} → ${a.to}` : a.fqn || '')),
              h('span.nt-note', a.note || ''),
            ]),
          ))
        : h('div.small.muted', { style: 'margin-top:10px' }, 'Nothing pinned yet. When something surprises you — a hidden invariant, a gotcha, a reason — attach it to the symbol it explains.'),
    ])
  }

  // ------------------------------------------------------------------ load

  function load({ force = false } = {}) {
    if (force) clearLearnCache()
    progress = loadProgress()
    mount(heroHost, h('section.ov-hero.loading', [h('div.skeleton', { style: 'width:120px;height:12px' }), h('div.skeleton', { style: 'width:260px;height:30px;margin-top:12px' }), h('div.skeleton', { style: 'width:70%;height:14px;margin-top:16px' })]))
    for (const id of LEARN_STEPS) mount(bodies[id], spinner('loading…'))
    drawProgress()

    const atlas = loadAtlas({ depth: 1, maxNodes: 400 }, { force })
    const features = loadFeatures({ force })
    const notes = loadAnnotations({ force })
    const guard = (fn) => (res) => {
      try {
        fn(res)
      } catch (err) {
        console.error('[overview]', err)
      }
    }
    atlas.then(guard(drawHero))
    atlas.then(guard(drawPicture))
    atlas.then(guard(drawConcepts))
    features.then(guard(drawAbilities))
    features.then(guard(drawEntry))
    features.then(guard(drawTrace))
    notes.then(guard(drawNotes))
  }

  mount(host, h('div.stack', { style: 'gap:22px' }, [
    heroHost,
    h('div.lp', [
      h('div.lp-title', [h('h2', 'Learning path'), progHost]),
      h('div.lp-steps', LEARN_STEPS.map(stepCard)),
    ]),
  ]))
  load()

  return {
    node: host,
    reload: () => load({ force: true }),
  }
}
