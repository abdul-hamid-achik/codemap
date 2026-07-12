---
description: Bootstrap codemap in this project — install, index, and confirm the graph is ready
---

Set up codemap for this repository so its MCP tools return real answers. Run these
steps, adapting to what's already present (skip a step that's already done):

1. Check the tool is installed and healthy: `codemap doctor`. If `codemap` is not
   found, install it with `brew install abdul-hamid-achik/tap/codemap` (or
   `go install github.com/abdul-hamid-achik/codemap/cmd/codemap@latest`), then re-run.
2. Register this project: `codemap init`.
3. Build the graph: `codemap index --precise`. If the precise pass reports a missing
   language toolchain (no `go`, `typescript-language-server`, or `pyright-langserver`),
   fall back to `codemap index` so at least the name-based Go graph and symbols exist.
4. Confirm it worked: `codemap status --json` — check `indexed` is true and the node/edge
   counts are non-zero.
5. Suggest keeping it fresh: offer to run `codemap daemon start` (background watcher) or
   `codemap index --watch` so the graph re-indexes as files change.

Then tell the user codemap is ready and remind them the `using-codemap` skill explains
when to reach for the `codemap_*` tools.
