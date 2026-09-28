/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Dependency-free syntax highlighter. Tokenizes with one alternation regex per
// language family, then splits the token stream into lines so callers can draw
// a gutter and highlight individual lines. Output is plain data ({t, c}) — the
// view layer turns it into DOM nodes, so nothing here can inject markup.

const KW = {
  go: 'break case chan const continue default defer else fallthrough for func go goto if import interface map package range return select struct switch type var nil true false iota make new len cap append copy delete panic recover print println any comparable error byte rune int int8 int16 int32 int64 uint uint8 uint16 uint32 uint64 uintptr float32 float64 string bool complex64 complex128',
  typescript:
    'abstract as async await break case catch class const constructor continue debugger declare default delete do else enum export extends false finally for from function get if implements import in infer instanceof interface is keyof let module namespace new null of package private protected public readonly return require satisfies set static super switch this throw true try type typeof var void while with yield await as any string number boolean symbol unknown never object bigint satisfies override',
  javascript:
    'async await break case catch class const continue debugger default delete do else export extends false finally for from function get if import in instanceof let new null of return set static super switch this throw true try typeof var void while with yield',
  python:
    'and as assert async await break class continue def del elif else except False finally for from global if import in is lambda None nonlocal not or pass raise return True try while with yield match case self cls',
  ruby: 'alias and begin break case class def defined? do else elsif end ensure false for if in module next nil not or redo rescue retry return self super then true undef unless until when while yield require require_relative attr_accessor attr_reader attr_writer include extend prepend',
  lua: 'and break do else elseif end false for function goto if in local nil not or repeat return then true until while',
  sql: 'select from where insert into values update set delete create table view index alter drop join left right inner outer full on group by order having limit offset distinct as and or not null is in exists between like case when then else end union all with returns primary key foreign references constraint default begin commit rollback',
  yaml: 'true false null yes no on off',
  shell: 'if then else elif fi for while do done case esac function return in export local set shift echo exit cd command sudo',
}

// Order matters: comments/strings before identifiers, longer operators first.
const RULES = {
  clike: [
    ['com', /\/\/[^\n]*/y],
    ['com', /\/\*[\s\S]*?(?:\*\/|$)/y],
    ['str', /`(?:\\[\s\S]|[^\\`])*`?/y],
    ['str', /"(?:\\[\s\S]|[^\\"])*"?/y],
    ['str', /'(?:\\[\s\S]|[^\\'])*'?/y],
    ['num', /\b0[xXoObB][0-9a-fA-F_]+\b|\b\d[\d_]*(?:\.\d[\d_]*)?(?:[eE][+-]?\d+)?\b/y],
    ['pre', /^\s*#\s*\w+[^\n]*/y],
    ['id', /[A-Za-z_$][A-Za-z0-9_$]*/y],
    ['op', /<<=|>>=|\.\.\.|[-+*/%&|^!<>=]=?|&&|\|\||<<|>>|[-+*/%&|^!~?:;.,()\[\]{}]/y],
  ],
  hash: [
    ['com', /#[^\n]*/y],
    ['str', /"(?:\\[\s\S]|[^\\"])*"?/y],
    ['str', /'(?:\\[\s\S]|[^\\'])*'?/y],
    ['num', /\b0[xX][0-9a-fA-F_]+\b|\b\d[\d_]*(?:\.\d+)?\b/y],
    ['id', /[A-Za-z_$@][A-Za-z0-9_$]*[?!]?/y],
    ['op', /<=>|=>|[-+*/%&|^!<>=]=?|&&|\|\||\.\.|\?\.|[-+*/%&|^!~?:;.,()\[\]{}]/y],
  ],
  lua: [
    ['com', /--\[\[[\s\S]*?(?:\]\]|$)/y],
    ['com', /--[^\n]*/y],
    ['str', /\[\[[\s\S]*?(?:\]\]|$)/y],
    ['str', /"(?:\\[\s\S]|[^\\"])*"?/y],
    ['str', /'(?:\\[\s\S]|[^\\'])*'?/y],
    ['num', /\b0[xX][0-9a-fA-F]+\b|\b\d+(?:\.\d+)?(?:[eE][+-]?\d+)?\b/y],
    ['id', /[A-Za-z_][A-Za-z0-9_]*/y],
    ['op', /[-+*/%^#<>=~]=?|\.\.|[:;.,()\[\]{}]/y],
  ],
  sql: [
    ['com', /--[^\n]*/y],
    ['com', /\/\*[\s\S]*?(?:\*\/|$)/y],
    ['str', /'(?:''|[^'])*'?/y],
    ['str', /"(?:""|[^"])*"?/y],
    ['num', /\b\d+(?:\.\d+)?\b/y],
    ['id', /[A-Za-z_][A-Za-z0-9_$]*/y],
    ['op', /<>|!=|<=|>=|\|\||[-+*/%=<>()\[\]{}.,;:|]/y],
  ],
  yaml: [
    ['com', /#[^\n]*/y],
    ['key', /-?\s*[A-Za-z0-9_./$-]+(?=\s*:)/y],
    ['str', /"(?:\\[\s\S]|[^\\"])*"?/y],
    ['str', /'(?:''|[^'])*'?/y],
    ['num', /\b-?\d+(?:\.\d+)?\b/y],
    ['id', /[A-Za-z_][A-Za-z0-9_.$-]*/y],
    ['op', /[:\-|>&*!,\[\]{}?]/y],
  ],
  json: [
    ['key', /"(?:\\[\s\S]|[^\\"])*"(?=\s*:)/y],
    ['str', /"(?:\\[\s\S]|[^\\"])*"?/y],
    ['num', /-?\b\d+(?:\.\d+)?(?:[eE][+-]?\d+)?\b/y],
    ['bool', /\b(?:true|false|null)\b/y],
    ['op', /[:,\[\]{}]/y],
  ],
  md: [
    ['com', /^<!--[\s\S]*?-->/my],
    ['key', /^#{1,6}[^\n]*/my],
    ['fn', /```[\s\S]*?(?:```|$)/y],
    ['str', /`[^`\n]*`/y],
    ['type', /\*\*[^*\n]+\*\*|__[^_\n]+__/y],
    ['num', /\[[^\]\n]*\]\([^)\n]*\)/y],
    ['op', /^[-*+]\s|^\d+\.\s/my],
    ['id', /[A-Za-z0-9_]+/y],
  ],
}

const LANG_MAP = {
  go: 'clike',
  golang: 'clike',
  typescript: 'clike',
  tsx: 'clike',
  javascript: 'clike',
  jsx: 'clike',
  mjs: 'clike',
  cjs: 'clike',
  c: 'clike',
  cpp: 'clike',
  cs: 'clike',
  java: 'clike',
  rust: 'clike',
  swift: 'clike',
  gdscript: 'hash',
  python: 'hash',
  py: 'hash',
  ruby: 'hash',
  rb: 'hash',
  shell: 'hash',
  sh: 'hash',
  bash: 'hash',
  zsh: 'hash',
  yaml: 'yaml',
  yml: 'yaml',
  lua: 'lua',
  sql: 'sql',
  json: 'json',
  jsonc: 'json',
  markdown: 'md',
  md: 'md',
  toml: 'hash',
}

const TYPEY = new Set(['typescript', 'go', 'java', 'cs', 'rust', 'swift', 'c', 'cpp'])

const WS = /[ \t\r]*\n?/y

function familyFor(lang) {
  const l = String(lang || '').toLowerCase()
  const fam = LANG_MAP[l]
  return RULES[fam] ? fam : null
}

function langName(lang) {
  const l = String(lang || '').toLowerCase()
  if (l === 'go' || l === 'golang') return 'go'
  if (['ts', 'tsx', 'typescript', 'mts', 'cts'].includes(l)) return 'typescript'
  if (['js', 'jsx', 'javascript', 'mjs', 'cjs'].includes(l)) return 'javascript'
  if (['py', 'python'].includes(l)) return 'python'
  if (['rb', 'ruby'].includes(l)) return 'ruby'
  if (['yml', 'yaml'].includes(l)) return 'yaml'
  if (['md', 'markdown'].includes(l)) return 'markdown'
  if (['sh', 'bash', 'zsh', 'shell'].includes(l)) return 'shell'
  return KW[l] ? l : ''
}

/**
 * Tokenize source into [{t: text, c: css-class-suffix}].
 * Class names map onto .tok-* rules in components.css.
 */
export function tokenize(code, lang) {
  const text = String(code ?? '')
  const family = familyFor(lang)
  if (!family || !text) return [{ t: text, c: '' }]
  const rules = RULES[family]
  const name = langName(lang)
  const kw = new Set((KW[name] || '').split(/\s+/).filter(Boolean))
  const typey = TYPEY.has(name)
  const out = []
  const push = (t, c) => {
    const last = out[out.length - 1]
    if (last && last.c === c) last.t += t
    else out.push({ t, c })
  }
  let pos = 0
  const len = text.length
  while (pos < len) {
    WS.lastIndex = pos
    const ws = WS.exec(text)
    if (ws && ws[0].length) {
      push(ws[0], '')
      pos += ws[0].length
      continue
    }
    let matched = false
    for (const [cls, re] of rules) {
      re.lastIndex = pos
      const m = re.exec(text)
      if (m && m[0].length) {
        let c = cls
        if (cls === 'id') {
          const word = m[0]
          if (kw.has(word)) c = 'key'
          else if (typey && /^[A-Z]/.test(word)) c = 'type'
          else if (text[pos + word.length] === '(') c = 'fn'
          else c = ''
        }
        push(m[0], c)
        pos += m[0].length
        matched = true
        break
      }
    }
    if (!matched) {
      // an unexpected rune (unicode punctuation, emoji…): keep it verbatim
      push(text[pos], '')
      pos++
    }
  }
  return out
}

export function langFromPath(p) {
  const ext = String(p || '').split('.').pop()?.toLowerCase()
  if (!ext) return ''
  if (ext === 'go') return 'go'
  if (['ts', 'mts', 'cts'].includes(ext)) return 'typescript'
  if (ext === 'tsx') return 'tsx'
  if (['js', 'mjs', 'cjs'].includes(ext)) return 'javascript'
  if (ext === 'jsx') return 'jsx'
  if (ext === 'py') return 'python'
  if (ext === 'rb') return 'ruby'
  if (ext === 'lua') return 'lua'
  if (ext === 'sql') return 'sql'
  if (['yml', 'yaml'].includes(ext)) return 'yaml'
  if (ext === 'md') return 'markdown'
  if (ext === 'json') return 'json'
  if (ext === 'vue') return 'typescript'
  if (ext === 'gd') return 'gdscript'
  if (['sh', 'bash', 'zsh'].includes(ext)) return 'shell'
  if (ext === 'toml') return 'toml'
  return ext
}

/** Split a token stream into per-line arrays, keeping classes across newlines. */
export function tokenLines(code, lang) {
  const lines = [[]]
  for (const tok of tokenize(code, lang)) {
    const parts = tok.t.split('\n')
    for (let i = 0; i < parts.length; i++) {
      if (i > 0) lines.push([])
      if (parts[i]) lines[lines.length - 1].push({ t: parts[i], c: tok.c })
    }
  }
  return lines
}
