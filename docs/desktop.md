---
title: Desktop app
description: Codemap Studio — an Electron workbench that wires every codemap feature into one reviewable interface.
---

# Codemap Studio (desktop)

Codemap Studio is an Electron workbench over the same store the CLI and MCP
server use. It does not reimplement anything: every panel spawns
`codemap … --json` against your project and renders the structured report, so
the CLI stays the single source of truth — including its exit-code taxonomy
and its `{ok:false,error,code,hint}` failure envelope.

```bash
cd desktop
npm install
npm start          # launch the app
npm run icons      # re-render build/icon.{icns,ico,png} from docs/public/mark.svg
npm test           # registry + renderer + view + graph + icon tests
npm run test:live  # every read-only feature against the real binary
npm run smoke      # boot the real app headlessly, walk every view, screenshot
```

The app icon is not a separate asset: `scripts/make-icons.mjs` composes the same
glyph the docs site ships (`docs/public/mark.svg`) onto the Studio dark tile and
rasterises it with Chromium into a full `.iconset`, an `.icns`, a PNG-in-`.ico`
and `icon.png`, so the Dock, the window and a packaged bundle all carry the
brand mark at every size.

## What is wired

The **Feature catalog** (sidebar → App → Feature catalog, or `⌘P`) lists every
capability the app integrates: the CLI command behind it, the MCP tool it maps
to, the flags the panel exposes, and whether it writes state. The catalog's
**Audit vs CLI** button parses `codemap --help` (and every nested subcommand's
help) from the running binary and fails loudly if any advertised command has no
panel, so the list cannot silently drift from the CLI.

Workspace views compose several features rather than mapping 1:1 onto a
command:

| View | What it answers |
| --- | --- |
| Dashboard | Is the index fresh, honest and complete — and what should I look at first? |
| Unified search | One box over four retrieval modes: name, indexed text, semantic, intent |
| Graph explorer | Typed relations from one exact source definition, drawn and expandable |
| Source browser | Indexed source with symbols overlaid; every position-aware feature from any line |
| Review desk | The real `git diff` beside codemap's diff-scoped impact, tests and risk band |
| Architecture map | Subsystems, directed bridges, hubs and entrypoints as a graph |
| MCP inspector | A live `codemap serve` handshake: profiles, tool schemas, direct tool calls |
| Raw command | Any argv at all — the guarantee that coverage is total |

## Honesty is part of the UI

codemap's confidence model is rendered, not hidden. Every report shows its
`call_graph` enum (`resolved` / `name` / `unresolved` / `none`), staleness is a
badge and a callout rather than a silent caveat, `analysis_complete:false` is
surfaced on review, and a risk level of `unknown` is drawn as *unknown*, never
as safe. Gates (`--fail-on-risk`, `--fail-on-untested`) are available on the
review and risk panels and report exit 6 as a verdict on an otherwise complete
report.

## How it talks to codemap

- **Binary resolution.** A GUI app does not inherit your shell PATH, so the app
  resolves a login-shell PATH once at startup and hands it to every child.
  Lookup order: Settings → `$CODEMAP_BIN` → `<repo>/bin/codemap` →
  `<repo>/codemap` → login-shell `command -v`.
- **One door.** `codemap:run` spawns the binary with your project as cwd,
  appends `--json`, streams long runs (`index`, `daemon start`) line by line,
  and maps exit codes 0–6 onto the UI.
- **MCP.** The inspector spawns `codemap serve --profile <p>` and speaks
  newline-delimited JSON-RPC 2.0 (initialize → initialized → tools/list →
  optional tools/call), then closes the server. Nothing stays resident.
- **State.** Settings, recent projects and run history live in
  `<userData>/codemap-studio.json`. The graph, vectors and annotations remain
  exactly where codemap keeps them.

## Testing

`desktop/test/` holds trimmed captures of real `codemap --json` reports
(`fixtures/`) plus a minimal DOM stub, so the renderer is exercised without a
window: every fixture must render through its feature panel, every declared
renderer must exist, and the highlighter must round-trip source exactly.
`test/integration.test.mjs` runs every read-only feature against the live
binary and accepts only honest answers (a report, or codemap's own structured
envelope). `test/smoke.mjs` boots the real Electron app headlessly, walks every
view, runs live features, performs the MCP handshake and writes screenshots to
`/tmp/codemap-studio-smoke/shots`.
