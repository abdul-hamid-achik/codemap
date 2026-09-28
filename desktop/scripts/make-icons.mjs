/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// App-icon generator. Reuses the brand glyph the docs site already ships
// (docs/public/mark.svg) and composes it onto the same dark tile the Studio UI
// uses, then renders every size a desktop bundle needs with a real Chromium
// rasteriser (no image dependencies): .iconset → .icns via iconutil, a
// PNG-in-ICO container for Windows, and icon.png for Linux/electron-builder.
//
// Run with: npm run icons   (i.e. `electron scripts/make-icons.mjs`)

import { app, BrowserWindow } from 'electron'
import { readFileSync, writeFileSync, mkdirSync, copyFileSync, existsSync, rmSync } from 'node:fs'
import { execFileSync } from 'node:child_process'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const here = path.dirname(fileURLToPath(import.meta.url))
const appRoot = path.resolve(here, '..')
const repo = path.resolve(appRoot, '..')
const outDir = path.join(appRoot, 'build')
const iconsetDir = path.join(outDir, 'icon.iconset')
mkdirSync(iconsetDir, { recursive: true })

// The docs mark is the single source of truth for the glyph.
const MARK = path.join(repo, 'docs', 'public', 'mark.svg')
const svg = readFileSync(MARK, 'utf8')
  .replace(/^<\?xml[^>]*\?>\s*/, '')
  .replace(/<svg([^>]*)\swidth="[^"]*"\sheight="[^"]*"/, '<svg$1 width="100%" height="100%"')

const TILE_HTML = `<!doctype html>
<html>
  <head>
    <style>
      html, body { margin: 0; background: transparent; overflow: hidden; }
      #tile {
        position: fixed;
        inset: 0;
        border-radius: 22.5%;
        background:
          radial-gradient(120% 92% at 78% -12%, rgba(102, 217, 239, 0.22), transparent 62%),
          radial-gradient(92% 80% at 4% 110%, rgba(180, 142, 240, 0.16), transparent 58%),
          linear-gradient(160deg, #131a22, #0a0c0f 72%);
        box-shadow: inset 0 0 0 1px rgba(255, 255, 255, 0.07);
        display: grid;
        place-items: center;
      }
      #tile svg { width: 58%; height: 58%; display: block; }
    </style>
  </head>
  <body>
    <div id="tile">${svg}</div>
  </body>
</html>`

const SIZES = [16, 24, 32, 48, 64, 128, 256, 512, 1024]

const ICONSET = {
  'icon_16x16.png': 16,
  'icon_16x16@2x.png': 32,
  'icon_32x32.png': 32,
  'icon_32x32@2x.png': 64,
  'icon_128x128.png': 128,
  'icon_128x128@2x.png': 256,
  'icon_256x256.png': 256,
  'icon_256x256@2x.png': 512,
  'icon_512x512.png': 512,
  'icon_512x512@2x.png': 1024,
}

const ICO_SIZES = [16, 24, 32, 48, 64, 128, 256]

const wait = (ms) => new Promise((r) => setTimeout(r, ms))

/** PNG-in-ICO container (Vista+): each entry is a complete PNG image. */
function writeIco(file, entries) {
  const n = entries.length
  const header = Buffer.alloc(6)
  header.writeUInt16LE(0, 0) // reserved
  header.writeUInt16LE(1, 2) // type: icon
  header.writeUInt16LE(n, 4)
  const dir = Buffer.alloc(16 * n)
  let offset = 6 + 16 * n
  entries.forEach((e, i) => {
    const dim = e.size >= 256 ? 0 : e.size
    dir[i * 16 + 0] = dim
    dir[i * 16 + 1] = dim
    dir[i * 16 + 2] = 0 // palette
    dir[i * 16 + 3] = 0 // reserved
    dir.writeUInt16LE(1, i * 16 + 4) // planes
    dir.writeUInt16LE(32, i * 16 + 6) // bpp
    dir.writeUInt32LE(e.buf.length, i * 16 + 8)
    dir.writeUInt32LE(offset, i * 16 + 12)
    offset += e.buf.length
  })
  writeFileSync(file, Buffer.concat([header, dir, ...entries.map((e) => e.buf)]))
}

app.whenReady().then(async () => {
  const htmlPath = path.join(outDir, '.icon-tile.html')
  writeFileSync(htmlPath, TILE_HTML)

  const win = new BrowserWindow({
    show: false,
    width: 1024,
    height: 1024,
    transparent: true,
    webPreferences: { offscreen: true, contextIsolation: true, nodeIntegration: false },
  })
  await win.loadFile(htmlPath)

  const rendered = new Map()
  for (const size of SIZES) {
    win.setSize(size, size)
    // let the offscreen compositor produce a frame at the new size
    await win.webContents.executeJavaScript('new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(() => r(1))))')
    await wait(60)
    const image = await win.webContents.capturePage()
    const buf = image.toPNG()
    rendered.set(size, buf)
    writeFileSync(path.join(outDir, `icon-${size}.png`), buf)
  }
  win.destroy()

  for (const [name, size] of Object.entries(ICONSET)) {
    copyFileSync(path.join(outDir, `icon-${size}.png`), path.join(iconsetDir, name))
  }

  const made = []
  if (process.platform === 'darwin' && existsSync('/usr/bin/iconutil')) {
    execFileSync('/usr/bin/iconutil', ['-c', 'icns', '-o', path.join(outDir, 'icon.icns'), iconsetDir], { stdio: 'pipe' })
    made.push('icon.icns')
  }
  writeIco(
    path.join(outDir, 'icon.ico'),
    ICO_SIZES.map((s) => ({ size: s, buf: rendered.get(s) })),
  )
  made.push('icon.ico')
  copyFileSync(path.join(outDir, 'icon-512.png'), path.join(outDir, 'icon.png'))
  made.push('icon.png')

  // Only icon.{icns,ico,png} are artifacts; everything else is regenerable
  // intermediate state and must not reach the repository.
  for (const size of SIZES) rmSync(path.join(outDir, `icon-${size}.png`), { force: true })
  rmSync(htmlPath, { force: true })
  rmSync(iconsetDir, { recursive: true, force: true })

  process.stdout.write(`ICONS_OK ${made.join(', ')} in ${outDir}\n`)
  app.exit(0)
})

app.on('window-all-closed', () => app.exit(1))
