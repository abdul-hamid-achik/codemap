/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Minimal, dependency-free Markdown → DOM renderer for the in-app guide
// (`codemap docs`, `agent playbook`). Builds nodes only — never innerHTML — so
// a guide that mentions <script> renders as text.

import { h } from './dom.mjs'
import { tokenize, langFromPath } from './highlight.mjs'

function codeBlock(text, lang) {
  const lines = tokenize(text, lang || '')
  const pre = h('pre')
  const code = h('code')
  for (const t of lines) code.appendChild(t.c ? h(`span.tok-${t.c}`, { text: t.t }) : document.createTextNode(t.t))
  pre.appendChild(code)
  return pre
}

const INLINE = /(`[^`]+`)|(\*\*[^*]+\*\*)|(\*[^*\n]+\*)|(_[^_\n]+_)|(\[[^\]]*\]\([^)\s]*\))|(https?:\/\/[^\s)]+)/g

function inline(text, opts = {}) {
  const out = []
  let last = 0
  INLINE.lastIndex = 0
  let m
  while ((m = INLINE.exec(text))) {
    if (m.index > last) out.push(text.slice(last, m.index))
    const tok = m[0]
    if (tok.startsWith('`')) {
      out.push(h('code', { text: tok.slice(1, -1) }))
    } else if (tok.startsWith('**')) {
      out.push(h('strong', { text: tok.slice(2, -2) }))
    } else if (tok.startsWith('*') || tok.startsWith('_')) {
      out.push(h('em', { text: tok.slice(1, -1) }))
    } else if (tok.startsWith('[')) {
      const lm = /\[([^\]]*)\]\(([^)\s]*)\)/.exec(tok)
      const label = lm ? lm[1] : tok
      const href = lm ? lm[2] : ''
      if (/^https?:/.test(href)) {
        out.push(h('a', { href, text: label, onclick: (e) => { e.preventDefault(); window.studio?.open(href) } }))
      } else {
        out.push(h('span', { text: label, title: href }))
      }
    } else {
      out.push(
        opts.links === false
          ? tok
          : h('a', {
              href: tok,
              text: tok,
              onclick: (e) => {
                e.preventDefault()
                window.studio?.open(tok)
              },
            }),
      )
    }
    last = m.index + tok.length
  }
  if (last < text.length) out.push(text.slice(last))
  return out
}

function splitRow(line) {
  return line
    .replace(/^\|/, '')
    .replace(/\|$/, '')
    .split('|')
    .map((c) => c.trim())
}

/**
 * @param {string} src markdown text
 * @returns {HTMLElement} a .markdown container
 */
export function renderMarkdown(src) {
  const root = h('div.markdown')
  const text = String(src ?? '').replace(/\r\n/g, '\n')
  const lines = text.split('\n')
  let i = 0

  const para = (buf) => {
    if (!buf.length) return
    const joined = buf.join(' ').trim()
    if (joined) root.appendChild(h('p', {}, inline(joined)))
    buf.length = 0
  }

  let buf = []
  while (i < lines.length) {
    const line = lines[i]

    // fenced code
    const fence = /^\s*(```|~~~)\s*([\w+-]*)\s*$/.exec(line)
    if (fence) {
      para(buf)
      const marker = fence[1]
      const lang = fence[2] || ''
      const body = []
      i++
      while (i < lines.length && !new RegExp(`^\\s*${marker}`).test(lines[i])) {
        body.push(lines[i])
        i++
      }
      i++
      root.appendChild(codeBlock(body.join('\n'), lang || langFromPath(`f.${lang}`)))
      continue
    }

    // heading
    const head = /^(#{1,6})\s+(.*)$/.exec(line)
    if (head) {
      para(buf)
      const level = Math.min(head[1].length, 4)
      root.appendChild(h(`h${level}`, {}, inline(head[2])))
      i++
      continue
    }

    // horizontal rule
    if (/^\s*(-{3,}|\*{3,}|_{3,})\s*$/.test(line)) {
      para(buf)
      root.appendChild(h('hr'))
      i++
      continue
    }

    // blockquote
    if (/^\s*>/.test(line)) {
      para(buf)
      const q = []
      while (i < lines.length && /^\s*>/.test(lines[i])) {
        q.push(lines[i].replace(/^\s*>\s?/, ''))
        i++
      }
      root.appendChild(h('blockquote', {}, inline(q.join(' '))))
      continue
    }

    // table
    if (/^\s*\|.*\|\s*$/.test(line) && i + 1 < lines.length && /^\s*\|?[\s:-]+\|[\s:|-]*$/.test(lines[i + 1])) {
      para(buf)
      const header = splitRow(line)
      i += 2
      const rows = []
      while (i < lines.length && /^\s*\|.*\|\s*$/.test(lines[i])) {
        rows.push(splitRow(lines[i]))
        i++
      }
      root.appendChild(
        h(
          'table',
          null,
          h('thead', null, h('tr', null, header.map((c) => h('th', null, inline(c))))),
          h('tbody', null, rows.map((r) => h('tr', null, header.map((_, ci) => h('td', null, inline(r[ci] || '')))))),
        ),
      )
      continue
    }

    // lists
    const ul = /^\s*[-*+]\s+(.*)$/.exec(line)
    const ol = /^\s*(\d+)[.)]\s+(.*)$/.exec(line)
    if (ul || ol) {
      para(buf)
      const ordered = !!ol
      const items = []
      while (i < lines.length) {
        const m2 = ordered ? /^\s*(\d+)[.)]\s+(.*)$/.exec(lines[i]) : /^\s*[-*+]\s+(.*)$/.exec(lines[i])
        if (!m2) {
          // allow one level of indented continuation
          if (/^\s{2,}\S/.test(lines[i]) && items.length) {
            items[items.length - 1] += ` ${lines[i].trim()}`
            i++
            continue
          }
          break
        }
        items.push(ordered ? m2[2] : m2[1])
        i++
      }
      root.appendChild(h(ordered ? 'ol' : 'ul', null, items.map((it) => h('li', null, inline(it)))))
      continue
    }

    if (!line.trim()) {
      para(buf)
      i++
      continue
    }

    buf.push(line)
    i++
  }
  para(buf)
  return root
}
