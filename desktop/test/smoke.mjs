/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Launcher for the Electron smoke run: spawns the real app under Electron,
// waits for the driver's SMOKE_RESULT line, and exits non-zero on failure.

import { spawn } from 'node:child_process'
import { existsSync, readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const here = path.dirname(fileURLToPath(import.meta.url))
const appRoot = path.resolve(here, '..')
const electron = path.join(appRoot, 'node_modules', '.bin', process.platform === 'win32' ? 'electron.cmd' : 'electron')
const outDir = process.env.SMOKE_OUT || '/tmp/codemap-studio-smoke'

if (!existsSync(electron)) {
  console.error(`electron not installed — run \`npm install\` inside ${appRoot}`)
  process.exit(2)
}

const timeoutMs = Number(process.env.SMOKE_TIMEOUT || 300000)
const child = spawn(electron, [path.join(here, 'electron-smoke.mjs')], {
  cwd: appRoot,
  env: { ...process.env, SMOKE_OUT: outDir, ELECTRON_ENABLE_LOGGING: '0' },
  stdio: ['ignore', 'pipe', 'pipe'],
})

let stdout = ''
let stderr = ''
let done = false

const finish = (code, why) => {
  if (done) return
  done = true
  clearTimeout(timer)
  const resultLine = stdout.split('\n').find((l) => l.startsWith('SMOKE_RESULT'))
  let summary = null
  if (resultLine) {
    try {
      summary = JSON.parse(resultLine.slice('SMOKE_RESULT'.length))
    } catch {
      /* keep raw */
    }
  }
  if (summary) {
    console.log(`smoke ${summary.ok ? 'PASSED' : 'FAILED'} — ${summary.errorCount} error(s), ${summary.shots} screenshot(s) in ${outDir}/shots`)
    for (const e of summary.errors || []) console.log(`  ✗ ${e}`)
  } else {
    console.log(`smoke produced no result (${why})`)
    console.log(stdout.slice(-4000))
  }
  if (!summary?.ok && stderr.trim()) console.error(`--- electron stderr ---\n${stderr.slice(-4000)}`)
  try {
    const full = JSON.parse(readFileSync(path.join(outDir, 'results.json'), 'utf8'))
    console.log(`views: ${full.views?.length || 0} · features: ${full.features?.length || 0} · graph nodes: ${full.graph?.nodes ?? '-'} · mcp tools seen: ${full.mcp?.tools ?? '-'}`)
  } catch {
    /* results.json only exists when the driver completed */
  }
  child.kill('SIGKILL')
  process.exit(code)
}

child.stdout.on('data', (d) => {
  stdout += d.toString()
  process.stdout.write(d)
  if (stdout.includes('SMOKE_RESULT')) finish(/"ok":true/.test(stdout.slice(stdout.indexOf('SMOKE_RESULT'))) ? 0 : 1, 'result')
})
child.stderr.on('data', (d) => {
  stderr += d.toString()
})
child.on('close', (code) => finish(code === 0 && stdout.includes('"ok":true') ? 0 : 1, `exited ${code}`))

const timer = setTimeout(() => finish(1, `timed out after ${timeoutMs}ms`), timeoutMs)
