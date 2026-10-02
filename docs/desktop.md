---
title: Desktop app
description: Codemap Studio — a desktop app that shows a codebase as a map, lists what it can do, traces how each feature works, and runs every codemap command.
---

# Codemap Studio (desktop)

Codemap Studio is an Electron app over the same store the CLI and MCP server
use. It has two halves:

- **Learn** turns an indexed repository into something you can look at: a map
  of its directories, the list of things the software can do, and the call tree
  behind each one.
- **Workspace** runs every codemap command as a panel: search, a graph
  explorer, a source browser, the review desk, and an MCP inspector.

It does not reimplement anything: every view spawns `codemap … --json` against
your project and renders the structured report, so the CLI stays the single
source of truth — including its exit-code taxonomy and its
`{ok:false,error,code,hint}` failure envelope.

## Install

Every [release](https://github.com/abdul-hamid-achik/codemap/releases) since
v0.69.0 attaches Codemap Studio next to the CLI archives:

| Platform | File |
| --- | --- |
| macOS (Apple silicon) | `Codemap-Studio-<version>-mac-arm64.dmg` |
| macOS (Intel) | `Codemap-Studio-<version>-mac-x64.dmg` |
| Linux (x64) | `Codemap-Studio-<version>-linux-<arch>.AppImage` (or `.tar.gz`) |
| Windows | `Codemap-Studio-<version>-win-x64.exe` |

The app ships with the `codemap` CLI of the same release, so it works before you
install anything else. If you already have `codemap` on your `PATH`, the app uses
yours — that binary built your existing indexes — unless it is older than the
bundled one. Settings and `$CODEMAP_BIN` still override both.

The macOS build is not notarized yet, so macOS asks once: right-click the app
→ **Open** (or System Settings → Privacy & Security → **Open Anyway**). The
Windows installer is unsigned, so SmartScreen may ask you to confirm.

### From source

You need Go, Node.js, and a checkout of the repository:

```bash
cd desktop
npm install
npm start          # launch the app against <repo>/bin/codemap or your PATH
npm run dist:mac   # build .dmg/.zip for arm64 and x64 into desktop/release/
```

`npm run dist:linux` and `npm run dist:win` build the other platforms; each
first compiles the matching `codemap` into the package.

## Run it

Open a project with the project chip in the title bar. If it is not indexed
yet, the app offers to run `codemap index` for you. An indexed project opens on
**Overview**.

## Learn a repository

The Learn section is the visual version of the [Learn a codebase](/learn)
walkthrough. Everything in it comes from three commands — `atlas`, `features`
and `flow` — so an agent sees the same answers over MCP.

### Overview

![Overview: the README summary, size, languages, and a six-step learning path](/desktop/overview.jpg)

The landing page for a project. It shows the README's first paragraph, how big the
repository is, its language mix, and whether the index is fresh. Below that is a
six-step **learning path**:

1. the top-level directories and what each one is for;
2. what the software can do, by surface;
3. where execution starts;
4. the core types and functions, with their docs;
5. three features worth tracing end to end;
6. your notes (annotations).

Progress is remembered per project.

### Atlas

![Atlas: the internal directory as a treemap, with the detail panel open](/desktop/atlas.jpg)

The repository as a zoomable treemap. Tile size follows symbols, lines or files.
Tile colour follows language, role (source vs. tests, docs, config), coupling
(inbound edges) or test share.

Click a directory to zoom in; press Esc to go back out. The detail panel shows:

- the directory's description, taken from its README, package doc, manifest or
  leading comment — never generated;
- its size and coupling;
- its key symbols;
- the directories it talks to most.

From there you can trace a flow, list the features that live in it, or copy a
markdown summary for an agent.

### Features

![Features: CLI commands grouped with their sub-commands, each with its footprint](/desktop/features.jpg)

Everything the software can do: CLI commands, MCP/RPC tools, HTTP routes,
pages, and programs. Each card shows the description from the registration
itself (a cobra `Short`, an MCP tool description) and the footprint — how much
code the feature touches and how many tests cover it specifically. Click a card
to see how the feature works.

### Flow

![Flow: the call tree of the review command beside the selected step's source](/desktop/flow.jpg)

The call tree behind one feature, in the order the code makes the calls. Each
step carries:

- its subsystem colour and its one-line doc;
- a confidence badge;
- `+N alt` when a name-based graph matched several definitions and the app
  shows the most plausible one;
- `ambiguous` when no single definition fits.

The route strip at the top summarises which subsystems the feature passes
through. Select a step to read its source, re-root the flow there, or open its
context and impact. The **Diagram** tab draws the same tree left to right.
**Copy as brief** produces a numbered outline you can paste into an agent chat
or a note.

## Workspace

The rest of the sidebar runs codemap's commands. The **Feature catalog**
(`⌘P`) lists every capability the app integrates: the CLI command behind it,
the MCP tool it maps to, the flags the panel exposes, and whether it writes
state. Its **Audit vs CLI** button parses `codemap --help` (and every nested
subcommand's help) from the running binary. It fails loudly if any advertised
command has no panel, so the list cannot silently drift from the CLI.

| View | What it answers |
| --- | --- |
| Health | Is the index fresh, honest and complete — and what should I look at first? |
| Unified search | One box over four retrieval modes: name, indexed text, semantic, intent |
| Graph explorer | Typed relations from one exact source definition, drawn and expandable |
| Source browser | Indexed source with symbols overlaid; every position-aware feature from any line |
| Review desk | The real `git diff` beside codemap's diff-scoped impact, tests and risk band |
| Architecture map | Subsystems and the directed bridges between them, as a graph |
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
  Lookup order: Settings → `$CODEMAP_BIN` → (from a checkout) `<repo>/bin/codemap` →
  `<repo>/codemap` → login-shell `command -v` → the `codemap` bundled in the
  installed app. A PATH binary older than the bundled one yields to it, since
  the app calls commands an older CLI lacks.
- **One door.** `codemap:run` spawns the binary with your project as cwd,
  appends `--json`, streams long runs (`index`, `daemon start`) line by line,
  and maps exit codes 0–6 onto the UI.
- **MCP.** The inspector spawns `codemap serve --profile <p>` and speaks
  newline-delimited JSON-RPC 2.0 (initialize → initialized → tools/list →
  optional tools/call), then closes the server. Nothing stays resident.
- **State.** Settings, recent projects and run history live in
  `<userData>/codemap-studio.json`. The graph, vectors and annotations remain
  exactly where codemap keeps them.

