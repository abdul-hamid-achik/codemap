/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

import { contextBridge, ipcRenderer } from 'electron'

const invoke = (channel, ...args) => ipcRenderer.invoke(channel, ...args)

const api = {
  info: () => invoke('app:info'),
  quit: () => invoke('app:quit'),

  settings: {
    get: () => invoke('settings:get'),
    all: () => invoke('settings:all'),
    set: (patch) => invoke('settings:set', patch),
    history: () => invoke('settings:history'),
    clearHistory: () => invoke('settings:clearHistory'),
  },

  binary: {
    resolve: (explicit) => invoke('binary:resolve', explicit),
    version: () => invoke('binary:version'),
  },

  run: (req) => invoke('codemap:run', req),
  cancel: (runIdOrKey) => invoke('codemap:cancel', runIdOrKey),
  active: () => invoke('codemap:active'),

  project: {
    pick: () => invoke('project:pick'),
    use: (p) => invoke('project:use', p),
    recent: () => invoke('project:recent'),
    tree: (cwd) => invoke('project:tree', { cwd }),
  },

  fs: {
    read: (cwd, p) => invoke('fs:read', { cwd, path: p }),
    write: (abs, text) => invoke('fs:write', { abs, text }),
  },

  save: (opts) => invoke('dialog:save', opts),
  reveal: (abs) => invoke('shell:reveal', abs),
  open: (target) => invoke('shell:open', target),
  git: (cwd) => invoke('git:info', { cwd }),
  gitDiff: (opts) => invoke('git:diff', opts),
  daemon: {
    launch: (cwd, args) => invoke('daemon:launch', { cwd, args }),
    kill: () => invoke('daemon:kill'),
  },
  mcp: {
    inspect: (opts) => invoke('mcp:inspect', opts),
  },
  theme: (t) => invoke('theme:set', t),

  on: (channel, cb) => {
    const allowed = ['codemap:stream', 'ui:goto', 'ui:palette', 'app:error']
    if (!allowed.includes(channel)) return () => {}
    const listener = (_e, payload) => cb(payload)
    ipcRenderer.on(channel, listener)
    return () => ipcRenderer.removeListener(channel, listener)
  },
}

contextBridge.exposeInMainWorld('studio', api)
