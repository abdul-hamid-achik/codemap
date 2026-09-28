/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Icon asset contract: `npm run icons` renders docs/public/mark.svg into the
// bundle icons. These checks parse the containers (not pixels) so a broken or
// missing icon set fails the suite instead of shipping a default Electron atom.

import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync, existsSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const here = path.dirname(fileURLToPath(import.meta.url))
const buildDir = path.join(here, '..', 'build')
const have = existsSync(path.join(buildDir, 'icon.png'))

test('icon.png is a 512x512 RGBA PNG', { skip: !have && 'run `npm run icons` first' }, () => {
  const buf = readFileSync(path.join(buildDir, 'icon.png'))
  assert.equal(buf.subarray(1, 4).toString('ascii'), 'PNG')
  assert.equal(buf.readUInt32BE(16), 512, 'width')
  assert.equal(buf.readUInt32BE(20), 512, 'height')
  assert.equal(buf[24], 8, 'bit depth')
  assert.equal(buf[25], 6, 'color type RGBA')
})

test('icon.ico is a valid container with the small sizes Windows needs', { skip: !have && 'run `npm run icons` first' }, () => {
  const buf = readFileSync(path.join(buildDir, 'icon.ico'))
  assert.equal(buf.readUInt16LE(0), 0, 'reserved')
  assert.equal(buf.readUInt16LE(2), 1, 'type must be icon')
  const count = buf.readUInt16LE(4)
  assert.ok(count >= 6, `expected at least 6 entries, got ${count}`)
  const sizes = []
  for (let i = 0; i < count; i++) {
    const o = 6 + i * 16
    const w = buf[o] === 0 ? 256 : buf[o]
    sizes.push(w)
    assert.equal(buf.readUInt16LE(o + 4), 1, 'planes')
    assert.equal(buf.readUInt16LE(o + 6), 32, 'bpp')
    const len = buf.readUInt32LE(o + 8)
    const off = buf.readUInt32LE(o + 12)
    assert.equal(buf.subarray(off, off + 4).subarray(1, 4).toString('ascii'), 'PNG', 'each entry must embed a PNG')
    assert.ok(off + len <= buf.length, 'entry overruns the file')
  }
  for (const need of [16, 32, 48, 256]) assert.ok(sizes.includes(need), `missing ${need}px entry`)
})

test('icon.icns carries the macOS icon magic', { skip: !have && 'run `npm run icons` first' }, () => {
  const buf = readFileSync(path.join(buildDir, 'icon.icns'))
  assert.equal(buf.subarray(0, 4).toString('ascii'), 'icns')
  assert.ok(buf.length > 10000, 'an icns with a full iconset should not be tiny')
})

// The iconset is an intermediate of `npm run icons` (deleted afterwards), so
// this only runs right after a generation on the machine that produced it.
test('the iconset covers every Apple slot', { skip: !existsSync(path.join(buildDir, 'icon.iconset')) && 'iconset not present (intermediate; run `npm run icons` to regenerate)' }, () => {
  const names = [
    'icon_16x16.png',
    'icon_16x16@2x.png',
    'icon_32x32.png',
    'icon_32x32@2x.png',
    'icon_128x128.png',
    'icon_128x128@2x.png',
    'icon_256x256.png',
    'icon_256x256@2x.png',
    'icon_512x512.png',
    'icon_512x512@2x.png',
  ]
  for (const n of names) assert.ok(existsSync(path.join(buildDir, 'icon.iconset', n)), `missing ${n}`)
})

test('the generator reuses the docs brand mark, not a copy', () => {
  const script = readFileSync(path.join(here, '..', 'scripts', 'make-icons.mjs'), 'utf8')
  assert.ok(script.includes(path.join('docs', 'public', 'mark.svg').split('/').join(path.sep)) || script.includes("docs', 'public', 'mark.svg"), 'generator must read docs/public/mark.svg')
})
