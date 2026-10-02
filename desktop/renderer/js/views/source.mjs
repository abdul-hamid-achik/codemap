/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Source browser: the "work" surface. File tree on the left, highlighted
// source on the right, symbol strip above it, and every position-aware codemap
// feature one click away from any line.

import { h, clear, mount, debounce } from '../dom.mjs'
import { runArgs } from '../runner.mjs'
import { badge, callout, card, codeBlock, chip, emptyState, kindBadge, spinner, symRow, toast, fmt, shortPath } from '../components.mjs'
import { langFromPath } from '../highlight.mjs'
import { state, openInspector } from '../state.mjs'

const MAX_TREE_ROWS = 4000

export function sourceView(ctx, initial = {}) {
  const host = h('div.view')
  const treeHost = h('div')
  const detailHost = h('div')
  let files = []
  let filtered = []
  let filterText = initial.filter || ''
  let current = null // {path, text, symbols, lang}
  let line = initial.line || 0

  async function loadTree() {
    if (state.tree && state.tree.root === state.project) {
      files = state.tree.files
      return drawTree()
    }
    mount(treeHost, spinner('listing project files…'))
    const t = await window.studio.project.tree(state.project)
    state.tree = t
    files = t.files || []
    drawTree()
  }

  function drawTree() {
    const q = filterText.trim().toLowerCase()
    filtered = q ? files.filter((f) => f.toLowerCase().includes(q)) : files
    const shown = filtered.slice(0, MAX_TREE_ROWS)
    const rows = shown.map((f) => {
      const dir = f.slice(0, f.lastIndexOf('/'))
      const name = f.slice(f.lastIndexOf('/') + 1)
      return h('button.ft-row', {
        type: 'button',
        class: current?.path === f ? 'ft-row on' : 'ft-row',
        title: f,
        onclick: () => openFile(f),
      }, [
        dir ? h('span.dir', `${dir}/`) : null,
        h('span.name', name),
      ])
    })
    mount(
      treeHost,
      h('div.filetree', [
        h('div.ft-filter', [
          h('input', {
            type: 'search',
            value: filterText,
            placeholder: `Filter ${files.length} files…`,
            oninput: debounce((e) => {
              filterText = e.target.value
              drawTree()
            }, 140),
          }),
        ]),
        filtered.length > MAX_TREE_ROWS ? h('div.ft-dir', `showing first ${MAX_TREE_ROWS} of ${filtered.length}`) : null,
        rows.length ? rows : h('div.ft-dir', 'no files match'),
      ]),
    )
  }

  async function openFile(p, opts = {}) {
    line = opts.line || 0
    mount(detailHost, spinner(`reading ${p}…`))
    const [fileRes, symRes] = await Promise.all([
      window.studio.fs.read(state.project, p),
      runArgs(['symbols', p], { featureId: '__symbols', cwd: state.project }).catch(() => null),
    ])
    if (!fileRes?.ok) {
      mount(detailHost, callout('danger', 'Cannot read file', fileRes?.error || 'unknown error'))
      return
    }
    current = {
      path: p,
      abs: fileRes.abs,
      text: fileRes.text,
      lang: langFromPath(p),
      symbols: symRes?.ok ? symRes.json.symbols || [] : [],
      symError: symRes?.ok ? null : symRes,
      truncated: fileRes.truncated,
    }
    drawTree()
    drawDetail()
    if (line) scrollToLine(line)
  }

  function scrollToLine(ln) {
    const el = detailHost.querySelector(`.code-line[data-line="${ln}"]`)
    el?.scrollIntoView({ block: 'center', behavior: 'smooth' })
  }

  function drawDetail() {
    if (!current) {
      mount(detailHost, emptyState({ icon: '⌸', title: 'Pick a file', note: 'Choose a file from the tree, or jump straight to a symbol from any list in the app.' }))
      return
    }
    const syms = current.symbols
    const marks = new Map()
    for (const s of syms) {
      if (s.start_line) marks.set(s.start_line, 'hl')
    }

    const strip = h('div.symbol-strip', [
      syms.length
        ? syms.map((s) =>
            chip(
              [kindBadge(s.kind), h('span.mono', ` ${s.symbol}`), h('span.dim', `:${s.start_line}`)],
              () => {
                line = s.start_line
                scrollToLine(line)
                selectSymbol(s)
              },
              { title: s.signature || s.fqn || '' },
            ),
          )
        : h('span.small.dim', current.symError ? 'this file is not indexed (no symbols)' : 'no symbols'),
    ])

    const actions = h('div.btn-row', [
      h('button.btn.sm', { type: 'button', onclick: () => window.studio.reveal(current.abs) }, 'Reveal in Finder'),
      h('button.btn.sm', { type: 'button', onclick: () => window.studio.open(current.abs) }, 'Open externally'),
      h('button.btn.sm', { type: 'button', onclick: () => ctx.openFeature('symbols', { file: current.path }) }, 'Symbols'),
      h('button.btn.sm', { type: 'button', onclick: () => ctx.openFeature('file-context', { file: current.path }) }, 'File context'),
      h('button.btn.sm', { type: 'button', onclick: () => ctx.openFeature('file-impact', { file: current.path }) }, 'File impact'),
      h('button.btn.sm', { type: 'button', onclick: () => ctx.openFeature('dependencies', { file: current.path }) }, 'Dependencies'),
      h('button.btn.sm', { type: 'button', onclick: () => ctx.openFeature('related-files', { file: current.path }) }, 'Related files'),
      h('button.btn.sm', { type: 'button', onclick: () => ctx.openFeature('annotate', { target: current.path }) }, 'Annotate'),
    ])

    const posBar = positionBar()

    const code = codeBlock({
      text: current.text,
      lang: current.lang,
      startLine: 1,
      marks,
      maxLines: 5000,
      onLineClick: (ln) => {
        line = ln
        drawPosition()
        scrollToLine(ln)
      },
    })

    mount(
      detailHost,
      h('div.stack', [
        h('div.source-head', [
          h('span.sh-path', current.path),
          badge(current.lang || 'text', 'plain'),
          badge(`${fmt.num(current.text.split('\n').length)} lines`, 'plain'),
          current.truncated ? badge('truncated (4 MB cap)', 'warn') : null,
          h('div.spacer', { style: 'flex:1' }),
          jumpInput(),
        ]),
        actions,
        posBar,
        syms.length ? card({ title: `Symbols (${syms.length})`, body: strip, tight: true }) : null,
        current.symError && current.symError.code === 'not_indexed'
          ? callout('warn', 'Not indexed', 'This file has no indexed symbols. Run “Index” to bring it into the graph.', [h('button.btn.sm.primary', { type: 'button', onclick: () => ctx.openFeature('index') }, 'Index now')])
          : null,
        h('div.code-scroll', code),
      ]),
    )
  }

  function jumpInput() {
    const input = h('input', {
      type: 'number',
      value: line || '',
      placeholder: 'line',
      min: '1',
      style: 'width:88px',
      onkeydown: (e) => {
        if (e.key === 'Enter') {
          line = Number(input.value) || 0
          drawPosition()
          scrollToLine(line)
        }
      },
    })
    return h('div.field-row', [input, h('button.btn.sm', { type: 'button', text: 'Go', onclick: () => { line = Number(input.value) || 0; drawPosition(); scrollToLine(line) } })])
  }

  function positionBar() {
    const bar = h('div.row.gap2')
    bar.__refresh = () => {
      clear(bar)
      const at = line ? `${current.path}:${line}` : ''
      bar.appendChild(h('span.small.dim', at ? `position ${at}` : 'click a line to pin a position'))
      if (!line) return
      bar.appendChild(
        h('div.btn-row', [
          h('button.btn.sm', { type: 'button', onclick: () => runAt('symbol-at') }, 'Resolve symbol'),
          h('button.btn.sm', { type: 'button', onclick: () => ctx.openFeature('context', { at: [at] }) }, 'Context'),
          h('button.btn.sm', { type: 'button', onclick: () => ctx.openFeature('impact', { at: [at] }) }, 'Impact'),
          h('button.btn.sm', { type: 'button', onclick: () => ctx.openFeature('callers', { at }) }, 'Callers'),
          h('button.btn.sm', { type: 'button', onclick: () => ctx.openFeature('callees', { at }) }, 'Callees'),
          h('button.btn.sm', { type: 'button', onclick: () => ctx.openFeature('risk', { at }) }, 'Risk'),
          h('button.btn.sm', { type: 'button', onclick: () => ctx.openFeature('refactor-plan', { at }) }, 'Refactor plan'),
          h('button.btn.sm', { type: 'button', onclick: () => ctx.openFeature('source', { at }) }, 'Source'),
          h('button.btn.sm', { type: 'button', onclick: () => ctx.openFeature('annotate', { target: current.path }) }, 'Annotate'),
        ]),
      )
    }
    bar.__refresh()
    positionBarEl = bar
    return bar
  }

  let positionBarEl = null
  function drawPosition() {
    positionBarEl?.__refresh?.()
  }

  async function runAt(id) {
    const at = `${current.path}:${line}`
    const args = id === 'symbol-at' ? ['symbol-at', at] : [id, '--at', at]
    const res = await runArgs(args, { featureId: `__src_${id}` })
    if (!res?.ok) return
    const json = res.json || {}
    const items = json.symbols || json.matches || json.results || (Array.isArray(json) ? json : [])
    openInspector({
      title: `${id} · ${at}`,
      kind: 'symbols',
      payload: { items, json, featureId: id },
    })
    ctx.renderInspector?.()
  }

  function selectSymbol(s) {
    openInspector({
      title: `${s.fqn || s.symbol}`,
      kind: 'symbol',
      payload: { symbol: s },
    })
    ctx.renderInspector?.()
  }

  mount(host, h('div.stack', [
    h('div.view-head', [
      h('div.vh-main', [
        h('h1', ['Source browser', current ? badge(shortPath(current.path), 'accent') : null]),
        h('div.vh-sub', 'Read indexed source with its symbols overlaid, then run any position-aware feature straight from a line.'),
      ]),
      h('div.vh-actions', [
        h('button.btn', { type: 'button', onclick: () => loadTree() }, '↻ Reload tree'),
        h('button.btn', { type: 'button', onclick: () => ctx.openFeature('grep') }, '⌕ Grep'),
        h('button.btn', { type: 'button', onclick: () => ctx.go('search') }, 'Search'),
      ]),
    ]),
    h('div.source-shell', [treeHost, detailHost]),
  ]))

  loadTree()
  if (initial.file) openFile(initial.file, { line: initial.line })

  return {
    node: host,
    open: (file, l) => openFile(file, { line: l }),
    navigate(opts = {}) {
      if (opts.filter !== undefined) {
        filterText = opts.filter || ''
        if (files.length) drawTree()
      }
    },
  }
}
