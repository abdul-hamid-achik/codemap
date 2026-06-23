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

- **Graph** — a call-graph explorer: the hubs (most-referenced symbols) on the left; the
  selected hub's callers and callees on the right.
- **Metrics** — node/edge/file counts and bar charts by kind and language, plus the top hubs.
- **Impact** — type a symbol, see its callers, blast radius, and which tests cover it.
- **Search** — semantic search by meaning, ranked by similarity.

## Keys

| Key | Action |
|---|---|
| `1`–`4` / `tab` / `shift+tab` | Switch tabs |
| `↑` / `↓` | Select a hub (Graph) or scroll results (Search / Impact) |
| `enter` | Graph: drill into the selected hub's impact · Search/Impact: run the query |
| `ctrl+c` | Quit (`q` also quits on Graph and Metrics) |

Symbols are shown with their fully-qualified names so same-named methods (e.g.
`graph.Store.Close` vs `app.Session.Close`) are easy to tell apart.
