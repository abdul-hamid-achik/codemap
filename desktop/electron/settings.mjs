/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Small JSON settings store under Electron's userData dir. Everything the app
// remembers (binary path, project list, appearance, run history) lives here so
// the renderer stays stateless between reloads.

import { readFileSync, writeFileSync, mkdirSync, existsSync } from 'node:fs'
import path from 'node:path'

const DEFAULTS = {
  binaryPath: '',
  projectPath: '',
  recentProjects: [],
  timeoutMs: 180000,
  indexTimeoutMs: 1800000,
  theme: 'dark',
  density: 'comfortable',
  fontSize: 13,
  autoRefresh: true,
  autoSearch: true,
  graphPhysics: true,
  extraArgs: '',
  recentSearches: [],
  recentRaw: [],
  history: [],
  pinned: [],
}

export class Settings {
  constructor(dir) {
    this.dir = dir
    this.file = path.join(dir, 'codemap-studio.json')
    this.data = { ...DEFAULTS }
    this.load()
  }

  load() {
    try {
      if (existsSync(this.file)) {
        const raw = JSON.parse(readFileSync(this.file, 'utf8'))
        this.data = { ...DEFAULTS, ...raw }
      }
    } catch {
      // a corrupt settings file must never stop the app from booting
      this.data = { ...DEFAULTS }
    }
    return this.data
  }

  save() {
    try {
      if (!existsSync(this.dir)) mkdirSync(this.dir, { recursive: true })
      writeFileSync(this.file, JSON.stringify(this.data, null, 2))
      return true
    } catch {
      return false
    }
  }

  get() {
    // history is exposed separately; keep the hot path small
    const { history, ...rest } = this.data
    return { ...rest, historyCount: history.length }
  }

  all() {
    return this.data
  }

  update(patch) {
    const known = Object.keys(DEFAULTS)
    for (const [k, v] of Object.entries(patch || {})) {
      if (!known.includes(k)) continue
      if (v === undefined || v === null) continue
      this.data[k] = v
    }
    this.save()
    return this.get()
  }

  rememberProject(p) {
    if (!p) return this.data.recentProjects
    const list = [p, ...this.data.recentProjects.filter((x) => x !== p)].slice(0, 12)
    this.data.recentProjects = list
    this.data.projectPath = p
    this.save()
    return list
  }

  addHistory(entry) {
    this.data.history.unshift({ at: Date.now(), ...entry })
    if (this.data.history.length > 300) this.data.history.length = 300
    // persist at most every 5 entries so a long session does not thrash the disk
    if (this.data.history.length % 5 === 0) this.save()
    return this.data.history.slice(0, 60)
  }

  history() {
    return this.data.history
  }

  clearHistory() {
    this.data.history = []
    this.save()
    return []
  }

  flush() {
    this.save()
  }
}
