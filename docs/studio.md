# studio (TUI)

`codemap studio` opens a full-screen, interactive explorer of your code, built on Charm v2
(Bubble Tea / Lip Gloss / Bubbles).

```
 codemap studio                        myproject · 411 nodes · 1414 edges · 35 files
  1 Graph   2 Metrics   3 Impact   4 Search
 Hubs                          │ graph.Store.Close
    45  graph.Store.Close      │  Called by (8)
    45  app.Session.Close      │    indexFile   internal/index/indexer.go:182
    21  embed.…Error           │    Index       internal/app/service.go:81
                               │  Calls (0)
```

## Tabs

- **Graph** — a call-graph explorer. The hubs (most-referenced symbols) are jump points on
  the left; the centered node's callers and callees are on the right. Press `→` to focus the
  right pane and **walk the graph** — `enter` re-centers on the selected caller/callee, so you
  can traverse "who calls this → who calls that → what does it call". `backspace` steps back
  along the path (the header shows the current depth), `←`/`h` returns focus to the hub list.
- **Metrics** — node/edge/file counts and bar charts by kind and language, plus the top hubs.
- **Impact** — type a symbol, see its callers, blast radius, and which tests cover it.
- **Search** — search by meaning (semantic, when an embedded index exists), automatically
  falling back to fast name search otherwise; the header shows which mode is active.

## Keys

| Key | Action |
|---|---|
| `1`–`4` / `tab` / `shift+tab` | Switch tabs |
| `↑` / `↓` | Select a hub/ref (Graph), a result (Search), or a blast-radius node (Impact) |
| `→` / `←` (`l` / `h`) | (Graph) move focus between the hub list and the callers/callees pane |
| `enter` | (Graph, hub pane) drill the hub into Impact · (Graph, refs pane) re-center on the selected caller/callee · (Search/Impact) drill the selection · or run the query after editing the text |
| `backspace` | (Graph) step back to the previous centered node while walking |
| `p` | (Graph) recompute the centered node's callers/callees precisely via gopls (Go) |
| `ctrl+r` | Reindex the project (structure-only) and refresh, without leaving studio |
| `ctrl+c` | Quit (`q` also quits on Graph and Metrics) |

Symbols are shown with their fully-qualified names so same-named methods (e.g.
`graph.Store.Close` vs `app.Session.Close`) are easy to tell apart.
