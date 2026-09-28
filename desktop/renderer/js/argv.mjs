/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Shell-ish command line → argv. Quotes are respected, nothing is expanded:
// this is an argument splitter for the raw runner, not a shell.

export function parseArgv(text) {
  const out = []
  let cur = ''
  let quote = null
  let has = false
  const src = String(text || '')
  for (let i = 0; i < src.length; i++) {
    const ch = src[i]
    if (quote) {
      if (ch === '\\' && quote === '"' && src[i + 1]) {
        cur += src[++i]
        continue
      }
      if (ch === quote) {
        quote = null
        continue
      }
      cur += ch
      continue
    }
    if (ch === '"' || ch === "'") {
      quote = ch
      has = true
      continue
    }
    if (ch === '\\' && src[i + 1]) {
      cur += src[++i]
      has = true
      continue
    }
    if (/\s/.test(ch)) {
      if (cur || has) out.push(cur)
      cur = ''
      has = false
      continue
    }
    cur += ch
    has = true
  }
  if (cur || has) out.push(cur)
  return out
}

/** Quote an argv back into a pasteable command line. */
export function quoteArgv(argv) {
  return (argv || [])
    .map((a) => {
      const s = String(a)
      return /[\s"'\\]/.test(s) ? `"${s.replace(/(["\\])/g, '\\$1')}"` : s
    })
    .join(' ')
}
