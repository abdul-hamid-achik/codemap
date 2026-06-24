# studio (TUI)

`codemap studio` opens a full-screen, interactive explorer of your code, built on Charm v2
(Bubble Tea / Lip Gloss / Bubbles).

```
 codemap studio                       codemap · 509 nodes · 1849 edges · 35 files
  1 Graph   2 Metrics   3 Impact   4 Search
 Hubs                                 │ lspsrc.Extractor.Close
    57  lspsrc.Extractor.Close        │  Called by (57)
    56  app.Session.Close             │   ▸ main.runInit    cmd/codemap/main.go:186
    56  graph.Store.Close             │     main.runIndex   cmd/codemap/main.go:209
    26  app.NewService                │  Calls (9)
    19  app.Open                      │     app.Session.Close  internal/app/session.go:80
                                      │  ⟩ func runInit(cmd *cobra.Command, ...) error
 ↑/↓ hub · → walk callers/calls · enter → impact · p precise · ctrl+c quit
```

## Tabs

- **Graph** — a call-graph explorer. The hubs (most-referenced symbols) are jump points on
  the left; the centered node's callers and callees are on the right. Press `→` to focus the
  right pane and **walk the graph** — `enter` re-centers on the selected caller/callee, so you
  can traverse "who calls this → who calls that → what does it call". `backspace` steps back
  along the path (the header shows the current depth), `←`/`h` returns focus to the hub list.
  Press `s` to read the selected symbol's **source code** in a scrollable overlay — navigate
  the graph and read the implementation without leaving studio.
- **Metrics** — an overview dashboard: node/edge/file counts and bar charts by kind and
  language on the left; on the right, the two ends of the call graph — the **top hubs**
  (most-referenced, load-bearing symbols) and the **dead-code candidates** (symbols with no
  callers). Both lists are navigable with `↑`/`↓`: `enter` drills the selected symbol into
  Impact, `ctrl+s` reads its source, `ctrl+g` opens it in the Graph walker — so the overview is
  also a launchpad.
- **Impact** — type a symbol, see its callers, blast radius, and which tests cover it. `ctrl+s`
  reads the selected blast node's source; `ctrl+g` opens it in the Graph walker.
- **Search** — search by meaning (semantic, when an embedded index exists), automatically
  falling back to fast name search otherwise; the header shows which mode is active. `ctrl+s`
  reads the selected hit's source; `ctrl+g` opens it in the Graph walker to explore its call
  neighborhood.

## Keys

| Key | Action |
|---|---|
| `?` | Toggle a full-screen keybinding help overlay (works on any tab) |
| `1`–`4` / `tab` / `shift+tab` | Switch tabs |
| `↑` / `↓` (`k` / `j`) | Select a hub/ref (Graph), a hub/dead-code row (Metrics), a result (Search), or a blast-radius node (Impact); `k`/`j` work on Graph & Metrics (on Search/Impact those keys type into the query box) |
| `pgup` / `pgdn` | Jump a page through any of those lists (plus `home`/`end` on Graph and Metrics) |
| `→` / `←` (`l` / `h`) | (Graph) move focus between the hub list and the callers/callees pane |
| `enter` | (Graph, hub pane) drill the hub into Impact · (Graph, refs pane) re-center on the selected caller/callee · (Search/Impact) drill the selection · or run the query after editing the text |
| `backspace` | (Graph) step back to the previous centered node while walking |
| `s` (Graph) / `ctrl+s` (any tab) | view the selected symbol's **source code** in a scrollable overlay (`↑`/`↓` or `k`/`j`, `pgup`/`pgdn`, `g`/`G`; `esc`/`q` to close). `ctrl+s` works on Impact/Search too, where the text input would otherwise capture a plain `s` |
| `ctrl+g` (any tab) | **open the selection in the Graph walker** — re-centers the Graph on the selected hit/blast node/row and switches to it, focused on the callers/calls pane, so any symbol becomes a place to start walking the call graph (not just the hubs) |
| `p` | (Graph) recompute the centered node's callers/callees precisely via gopls (Go) |
| `ctrl+r` | Reindex the project and refresh, without leaving studio — keeps the project's precision (re-runs `--precise` when it already has a precise call graph, so the graph isn't dropped) |
| `ctrl+c` | Quit (`q` also quits on Graph and Metrics) |

Symbols are shown with their fully-qualified names so same-named methods (e.g.
`graph.Store.Close` vs `app.Session.Close`) are easy to tell apart. In the Graph, Impact, and
Search panes the selected symbol's **signature** and the first line of its **docstring** are
previewed at the bottom (`⟩ func …`), so you can tell what a symbol is and what it does without
opening the file.
