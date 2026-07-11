# studio (TUI)

`codemap studio` opens a full-screen, interactive explorer of your code, built on Charm v2
(Bubble Tea / Lip Gloss / Bubbles).

```
 codemap studio          ● daemon main   codemap · 509 nodes · 1849 edges · 35 files
  1 Graph   2 Metrics   3 Impact   4 Search   5 Path
 Hubs (164)                           │ lspsrc.Extractor.Close
    57  lspsrc.Extractor.Close        │  Called by (57)
    56  app.Session.Close             │   ▸ main.runInit    cmd/codemap/main.go:186
    56  graph.Store.Close             │     main.runIndex   cmd/codemap/main.go:209
    26  app.NewService                │  Calls (9)
    19  app.Open                      │     app.Session.Close  internal/app/session.go:80
     ▼ 159 more                       │  ⟩ func runInit(cmd *cobra.Command, ...) error
 ↑/↓ hub · → walk · enter → impact · s source · p precise · ctrl+c quit · ? help
```

While an async operation is running (indexing, a semantic search, an impact analysis, a path lookup,
or a precise recompute), the footer shows an **animated spinner** beside the status — a spring-driven
frame loop that stops the instant the work completes, so an idle studio costs nothing.

## Tabs

- **Graph** — a call-graph explorer. The hubs (most-referenced symbols) are jump points on
  the left; the centered node's callers and callees are on the right. Press `→` to focus the
  right pane and **walk the graph** — `enter` re-centers on the selected caller/callee, so you
  can traverse "who calls this → who calls that → what does it call". `backspace` steps back
  along the path (the header shows the current depth), `←`/`h` returns focus to the hub list.
  Press `s` to read the selected symbol's **source code** in a scrollable overlay — navigate
  the graph and read the implementation without leaving studio. Press `m` to toggle a
  **neighborhood map**: the centered node drawn as a boxed focal point with its callers flowing
  down into it and its callees flowing out — a "you are here" diagram of the local call graph.
- **Metrics** — an overview dashboard: node/edge/file counts and bar charts by kind and
  language on the left, with a one-line `shape` **sparkline** of the kind distribution beneath
  them; on the right, the two ends of the call graph — the **top hubs**
  (most-referenced, load-bearing symbols) and the **dead-code candidates** (symbols with no
  callers). The bar charts **animate in** with spring physics (charmbracelet/harmonica) each
  time you land on the tab — they grow from zero with 1/8-cell partial blocks and settle with a
  touch of overshoot (the frame loop stops the instant they settle, so an idle studio costs
  nothing). Both lists are navigable with `↑`/`↓`: `enter` drills the selected symbol into
  Impact, `ctrl+s` reads its source, `ctrl+g` opens it in the Graph walker — so the overview is
  also a launchpad.
- **Impact** — type a symbol, see its callers, blast radius, and which tests cover it. The blast
  radius is a **depth heatmap**: each node's `[depth]` tag is colored from hot (a direct caller,
  most likely affected) to cool (a distant transitive dependent), so the shape of the impact reads
  at a glance. `ctrl+s` reads the selected blast node's source; `ctrl+g` opens it in the Graph walker.
- **Search** — search by meaning (semantic, when an embedded index exists), automatically
  falling back to fast name search otherwise; the header shows which mode is active. `ctrl+s`
  reads the selected hit's source; `ctrl+g` opens it in the Graph walker to explore its call
  neighborhood.
- **Path** — answer “how does this entrypoint reach that callee?” as an explicit task. Choose
  `FROM` and `TO` (a unique FQN disambiguates same-named definitions), then inspect the shortest
  directed call chain returned by the shared codemap graph. Entering Path from
  another tab pre-fills `FROM` with the current rich selection, including its FQN/file/line identity;
  type `TO` and press `enter`. The ordered result is navigable with `↑`/`↓` or `k`/`j`; `enter`
  opens the selected step in Graph, while `ctrl+s` reads its source and `ctrl+o` opens its context.
  Every answer shows `call_graph` and a confidence label. A resolved disconnection, a name-based
  result, an unresolved graph, and a missing endpoint remain visibly different; stale-index and
  `--precise` guidance are shown in place rather than turning “unknown” into “no path.”

## Keys

| Key | Action |
|---|---|
| `?` | Toggle a full-screen keybinding help overlay (works on any tab) |
| `1`–`5` / `tab` / `shift+tab` | Switch tabs. On **Search**/**Impact**/**Path** a bare digit types into the focused input — use `alt`+`1`–`5` to switch tabs while a text input is focused |
| `↑` / `↓` (`k` / `j`) | Select a hub/ref (Graph), a hub/dead-code row (Metrics), a result (Search), a blast-radius node (Impact), or a returned Path step. While editing Path, `↑`/`↓` moves between `FROM` and `TO`; after a result, both arrows and `k`/`j` inspect its nodes |
| `pgup` / `pgdn` | Jump a page through any of those lists (plus `home`/`end` on Graph and Metrics) |
| `→` / `←` (`l` / `h`) | (Graph) move focus between the hub list and the callers/callees pane |
| `enter` | (Graph, hub pane) drill the hub into Impact · (Graph, refs pane) re-center on the selected caller/callee · (Search/Impact) drill the selection or run edited text · (Path) advance `FROM` → `TO`, run the lookup, or open a selected result step in Graph |
| `f` / `t` / `r` | (Path result) edit `FROM`, edit `TO`, or rerun the current endpoint pair |
| `backspace` | (Graph) step back to the previous centered node while walking |
| `s` (Graph) / `ctrl+s` (any tab) | view the selected symbol's **source code** in a scrollable overlay (`↑`/`↓` or `k`/`j`, `pgup`/`pgdn`, `g`/`G`; `esc`/`q` to close). `ctrl+s` works on Impact/Search too, where the text input would otherwise capture a plain `s` |
| `o` (Graph/Metrics) / `ctrl+o` (any tab) | **orient** — open the context card for the selected symbol in a scrollable overlay: its definition, callers, callees, covering tests, blast-radius count, and pinned annotations in one view (the same bundle `codemap context` / `codemap_context` returns), so you get the whole picture without hopping between tabs |
| `ctrl+g` (any tab) | **open the selection in the Graph walker** — re-centers the Graph on the selected hit/blast node/Path step/row and switches to it, focused on the callers/calls pane, so any symbol becomes a place to start walking the call graph (not just the hubs) |
| `p` | (Graph) recompute the centered node's callers/callees precisely via gopls (Go) |
| `ctrl+r` | Reindex the project and refresh, without leaving studio — keeps the project's precision (re-runs `--precise` when it already has a precise call graph, so the graph isn't dropped). The header shows `⚠ stale C/N/D` (changed/new/deleted) when your files have drifted from the index since it was built, so you know when to press it |
| `alt+←` / `alt+→` (any tab) | **Global back / forward** — browser-style history across tabs and drills. Drilling a search hit into Impact, opening it in the Graph walker, etc. records where you came from; `alt+←` returns to the *exact* prior view **with the text you'd typed still in the bar** and the prior selection highlighted. `alt+→` re-walks forward; a new search/drill clears the forward stack. (This is a layer above the Graph's own `⌫` walk.) |
| `esc` | Step back one global level (when no overlay/help is open); also closes the help/source/context overlay |
| `ctrl+c` | Quit (`q` also quits on Graph and Metrics) |

When a [background daemon](/cli#background-daemon) is keeping the index fresh, the header shows a
live green `● daemon <branch>` indicator (polled every few seconds) so you can tell at a glance
that edits are being re-indexed automatically.

Symbols are shown with their fully-qualified names so same-named methods (e.g.
`graph.Store.Close` vs `app.Session.Close`) are easy to tell apart. In the Graph, Impact, and
Search panes—and for the selected step in Path—the symbol's **signature** and the first line of its **docstring** are
previewed at the bottom (`⟩ func …`), so you can tell what a symbol is and what it does without
opening the file.
