/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Builds the codemap CLI that ships inside the packaged app. electron-builder
// copies dist-bin/<os>-<arch>/ into <resources>/bin (see "extraResources" in
// package.json), so a fresh install works before the user installs codemap.
//
//   node scripts/stage-binary.mjs --os mac --arch arm64,x64 [--version v0.69.0]
//
// --os/--arch use electron-builder's names (mac|linux|win, x64|arm64). The
// version defaults to `git describe`, matching `task build`.

import { execFileSync } from 'node:child_process'
import { mkdirSync, rmSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const here = path.dirname(fileURLToPath(import.meta.url))
const appRoot = path.resolve(here, '..')
const repoRoot = path.resolve(appRoot, '..')
const MODULE = 'github.com/abdul-hamid-achik/codemap'

const GOOS = { mac: 'darwin', linux: 'linux', win: 'windows' }
const GOARCH = { x64: 'amd64', arm64: 'arm64' }

function arg(name, fallback) {
  const i = process.argv.indexOf(`--${name}`)
  return i > 0 && process.argv[i + 1] ? process.argv[i + 1] : fallback
}

function git(...args) {
  try {
    return execFileSync('git', args, { cwd: repoRoot, encoding: 'utf8' }).trim()
  } catch {
    return ''
  }
}

const hostOS = { darwin: 'mac', linux: 'linux', win32: 'win' }[process.platform]
const os = arg('os', hostOS)
const arches = arg('arch', process.arch === 'arm64' ? 'arm64' : 'x64').split(',').filter(Boolean)
const version = arg('version', git('describe', '--tags', '--always', '--dirty') || 'dev')
const commit = git('rev-parse', '--short', 'HEAD') || 'none'
const date = new Date().toISOString().replace(/\.\d{3}Z$/, 'Z')

if (!GOOS[os]) {
  console.error(`unknown --os ${os} (want mac|linux|win)`)
  process.exit(2)
}

for (const arch of arches) {
  if (!GOARCH[arch]) {
    console.error(`unknown --arch ${arch} (want x64|arm64)`)
    process.exit(2)
  }
  const outDir = path.join(appRoot, 'dist-bin', `${os}-${arch}`)
  rmSync(outDir, { recursive: true, force: true })
  mkdirSync(outDir, { recursive: true })
  const out = path.join(outDir, os === 'win' ? 'codemap.exe' : 'codemap')
  const ldflags = [
    '-s -w',
    `-X ${MODULE}/internal/version.Version=${version}`,
    `-X ${MODULE}/internal/version.Commit=${commit}`,
    `-X ${MODULE}/internal/version.Date=${date}`,
  ].join(' ')
  // Embed only the tree-sitter grammars codemap parses (see Taskfile GOTAGS).
  const tags = 'grammar_subset,grammar_subset_typescript,grammar_subset_tsx,grammar_subset_javascript,grammar_subset_python'
  execFileSync('go', ['build', '-trimpath', '-tags', tags, '-ldflags', ldflags, '-o', out, './cmd/codemap'], {
    cwd: repoRoot,
    stdio: 'inherit',
    // Release binaries are pure Go (CGO_ENABLED=0), so every target
    // cross-compiles from any host.
    env: { ...process.env, CGO_ENABLED: '0', GOOS: GOOS[os], GOARCH: GOARCH[arch] },
  })
  console.log(`staged codemap ${version} → ${path.relative(appRoot, out)}`)
}
