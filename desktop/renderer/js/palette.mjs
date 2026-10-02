/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// One colour vocabulary for the whole app. Languages, subsystems and roles map
// onto the categorical tokens (--cat-1 … --cat-12) defined in theme.css, so the
// same language is the same hue in Atlas, Features, Flow and the Overview, in
// both themes. Nothing here hard-codes a colour value.

export const CAT_COUNT = 12

export function catVar(i) {
  const n = ((Math.trunc(i) % CAT_COUNT) + CAT_COUNT) % CAT_COUNT
  return `var(--cat-${n + 1})`
}

// Fixed hues for languages codemap indexes; anything else hashes into the set.
const LANGUAGE_SLOT = {
  go: 0,
  typescript: 5,
  tsx: 5,
  javascript: 2,
  jsx: 2,
  vue: 3,
  python: 3,
  ruby: 4,
  lua: 1,
  rust: 6,
  java: 6,
  kotlin: 8,
  swift: 6,
  c: 9,
  cpp: 9,
  csharp: 1,
  php: 1,
  sql: 9,
  shell: 7,
  bash: 7,
  gdscript: 9,
  css: 8,
  scss: 8,
  html: 6,
  yaml: 11,
  json: 11,
  toml: 11,
  markdown: 10,
}

function hash(str) {
  let h = 5381
  for (let i = 0; i < str.length; i++) h = ((h << 5) + h + str.charCodeAt(i)) >>> 0
  return h
}

export function languageColor(lang) {
  const key = String(lang || '').toLowerCase()
  if (!key) return 'var(--text-dim)'
  if (key in LANGUAGE_SLOT) return catVar(LANGUAGE_SLOT[key])
  return catVar(hash(key))
}

// Subsystems get the next free hue the first time they are seen, so the handful
// of subsystems on one screen never collide; the assignment is then stable for
// the rest of the session across every view.
const subsystemSlots = new Map()
const usedSlots = new Set()

export function subsystemColor(name) {
  const key = String(name || '')
  if (!key) return 'var(--text-dim)'
  if (!subsystemSlots.has(key)) {
    let slot = hash(key) % CAT_COUNT
    for (let i = 0; i < CAT_COUNT && usedSlots.has(slot); i++) slot = (slot + 1) % CAT_COUNT
    if (usedSlots.size >= CAT_COUNT) usedSlots.clear()
    usedSlots.add(slot)
    subsystemSlots.set(key, slot)
  }
  return catVar(subsystemSlots.get(key))
}

export function resetSubsystemColors() {
  subsystemSlots.clear()
  usedSlots.clear()
}

// Roles that describe supporting material rather than the product itself.
export const SUPPORT_ROLES = new Set(['tests', 'docs', 'config', 'bench', 'examples', 'generated', 'vendor'])

export function isSupport(roles) {
  const list = Array.isArray(roles) ? roles : []
  if (!list.length) return false
  if (list.includes('source') || list.includes('entrypoint')) return false
  return list.some((r) => SUPPORT_ROLES.has(r))
}

/** CSS colour + mix strength for a node when colouring by role. */
export function roleStyle(roles) {
  const list = Array.isArray(roles) ? roles : []
  if (list.includes('entrypoint')) return { color: 'var(--ok)', mix: 46, label: 'entrypoint' }
  if (list.includes('source')) return { color: 'var(--accent)', mix: 40, label: 'source' }
  if (list.includes('tests')) return { color: 'var(--info)', mix: 26, label: 'tests' }
  if (list.includes('docs')) return { color: 'var(--warn)', mix: 20, label: 'docs' }
  if (list.includes('config')) return { color: 'var(--text-3)', mix: 20, label: 'config' }
  if (list.length) return { color: 'var(--text-3)', mix: 16, label: list[0] }
  return { color: 'var(--text-3)', mix: 14, label: 'other' }
}

export const ROLE_LEGEND = [
  { label: 'entrypoint', color: 'var(--ok)', mix: 46 },
  { label: 'source', color: 'var(--accent)', mix: 40 },
  { label: 'tests', color: 'var(--info)', mix: 26 },
  { label: 'docs', color: 'var(--warn)', mix: 20 },
  { label: 'config / bench / other', color: 'var(--text-3)', mix: 18 },
]

/** 0..1 heat to a mix percentage that stays readable on both themes. */
export function heatMix(t) {
  const v = Math.max(0, Math.min(1, Number(t) || 0))
  return Math.round(8 + v * 62)
}

/** Log-scaled 0..1 position of value within [0,max]; keeps one hub from flattening the rest. */
export function logScale(value, max) {
  const v = Math.max(0, Number(value) || 0)
  const m = Math.max(1, Number(max) || 1)
  return Math.min(1, Math.log1p(v) / Math.log1p(m))
}
