# Ecosystem — codemap ⇄ vecgrep

codemap composes with [vecgrep](https://github.com/abdul-hamid-achik/vecgrep), a sibling local-first
semantic code-search tool. They share an embedding space (Ollama `nomic-embed-text`) and address symbols
by `(relative_path, start_line)`, so each can use the other as an **optional accelerator that degrades to
its own local capability** — never a hard dependency.

**The boundary:** codemap owns *structure* (the resolved call/type/test graph + a durable, symbol-pinned
annotation layer); vecgrep owns *meaning* (chunk-level semantic search) and a *cross-project agent-memory*
store. They talk over each other's CLI (`--json`), one hop deep — neither calls the other's MCP server.

## Enabling it

Nothing to configure in the common case: if the `vecgrep` binary is on your `$PATH`, codemap uses it
automatically. To turn it off or point at a specific binary:

```yaml
vecgrep:
  enabled: true     # default
  bin: /usr/local/bin/vecgrep
```

or `CODEMAP_VECGREP_ENABLED=false` / `CODEMAP_VECGREP_BIN=…`. Every integrated call degrades gracefully
when vecgrep is absent, disabled, or hasn't indexed the project.

## What flows where

**codemap fetches *meaning* from vecgrep:**

- **Semantic search with no local embeddings.** If a project is structure-only in codemap (indexed with
  `--no-embed`, or before Ollama was available) but embedded in vecgrep, `codemap semantic` delegates to
  `vecgrep search` and maps each hit back onto the graph — so you get meaning-ranked results carrying
  codemap's FQN/kind/signature, with no codemap embedding pass. The result is marked `mode: "vecgrep"`.
- **Agent memory in context.** `codemap context <symbol>` surfaces relevant notes recalled from vecgrep's
  global agent-memory store, scoped to this project (see *project_key* below). They appear as a transient
  `memories` list — distinct from codemap's own durable annotations.

**codemap feeds *structure* to vecgrep** (implemented on the vecgrep side): `vecgrep_related_files` uses
codemap's real call/test graph instead of import-text heuristics; `vecgrep` re-ranks its semantic hits by
codemap's structural hub score; it pins search relevance back as codemap annotations (`source: vecgrep`);
and `vecgrep_status` reports the codemap graph alongside its own index.

## project_key — leak-free memory scoping

vecgrep's agent memory is a single *global* store. To recall only *this* project's notes without leaking
across projects, memories are tagged `['codemap', <project_key>]`, where `<project_key>` is a stable,
collision-resistant id codemap derives (`git`-rooted `RepoHash`). **codemap is the single authority for the
key** — it's reported by `codemap status --json` (`project_key`) and the `codemap_status` MCP tool. A tool
or agent writing a codemap-scoped memory uses that exact value rather than re-deriving it, and recall
matches on the *exact* tag set, so a wrong or absent key returns nothing (it fails closed).

```bash
# write a codemap-scoped memory (the key comes from codemap, not re-derived)
KEY=$(codemap status --json | jq -r .project_key)
vecgrep memory remember "ValidateToken is a hot path; refactor pending" --tags "codemap,$KEY" --importance 0.8

# …and it surfaces, scoped to this project, on:
codemap context ValidateToken
```
