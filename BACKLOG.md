# codemap — Backlog

> Forward-looking status + priorities for codemap. **Design source of truth: `AGENTS.md` / `SPEC.md`.**
> Full per-iteration build-loop history (every `#NNN` / `COMMIT+PUSH`, v0.1→v0.9.1) is archived
> verbatim in the Obsidian vault: `~/notes/projects/codemap/iteration-log/2026-06-24-backlog-full-archive.md`
> (repo root `.md` is limited to README/AGENTS/CLAUDE/BACKLOG/SPEC, so the history lives in the vault).
> **Going forward:** append iteration narrative to the vault, keep this file to durable status + priorities.

## Status legend
`[ ]` todo · `[~]` in progress · `[x]` done · `[!]` blocked/needs decision

---

## Current state — what's shipped
Released through **v0.10.0** (`brew install abdul-hamid-achik/tap/codemap`). Pure-Go, `CGO_ENABLED=0`,
5 cross-compiled targets. Three surfaces over one store: **CLI** (23 commands, `--json`), **MCP**
(`codemap serve`, 20 tools), **studio** TUI (Graph/Metrics/Impact/Search + `?` help + source & context
overlays). Languages: **Go** (go/parser + opt-in `--precise` go/types) and **TypeScript/JavaScript/Python**
(one typescript-language-server for TS+JS, pyright for Python; `--precise` = the unified exact pass —
go/types for Go, LSP `callHierarchy` for the rest). Semantic vectors via veclite + Ollama nomic-embed-text
(hybrid vector+BM25). **Annotation layer** (pin notes + opaque data to nodes & call paths, survives reindex,
surfaces on every surface). Flagship one-call **`context`** bundle (def + callers + callees + tests + blast
radius). Graph analytics: `impact` (cycle-safe blast radius + covering tests), `hotspots`, `orphans`, `path`.
Agent-trust honesty: index freshness/`stale`, ambiguous-name notes, name-inflation flags, call-graph-
unavailable `resolution` note. `doctor`, multi-project registry, incremental reindex with deleted-file pruning.

## Release history (condensed — full detail in the vault archive)
- **v0.10.0** — honesty + dogfood fidelity. TS/JS/Python `impact` no longer returns a confidently-empty
  `[]`+`untested` without a call graph (a `resolution` note instead, FIX.md §1); studio **global back/forward
  nav history** that restores the bar you came from (§2); studio source overlay **syntax highlighting**
  (chroma/v2, pure-Go) + real file-line gutter (§3). Indexing fidelity: Go package-level **var/const** now
  indexed (finding D); **scanned-but-skipped files** (parse-error/oversized) no longer show false staleness
  (finding E); **generated code** skipped by `*_gen.go` + the `// Code generated … DO NOT EDIT.` header
  (finding B). New dep: `chroma/v2`.
- **v0.9.1** — real-repo dogfood fixes: drop anonymous-callback symbol noise (#196), accept qualified
  `pkg.Type.Method` names in callers/impact/etc. (#197).
- **v0.9.0** — flagship one-call `context`; index-freshness/staleness for agent trust; honesty pass
  (typo-vs-no-callers `found`, external-vs-unresolved wording, no-HTML-escape JSON); default-exclude
  hardening (`dist-*`/`build-*`/`coverage`); onboarding progress bar; full 20-tool docs audit.
- **v0.8.0** — large-TS symbol recovery 52%→~96% (#139); capped human output (`--json` stays complete);
  studio width-robust at ≤80 cols; path-annotation surfacing; tree-sitter evaluated & **deferred** (#148).
- **v0.7.0** — JavaScript + Python + JSX/TSX call graphs; `codemap doctor`; hybrid semantic search;
  per-request LSP timeouts; incremental prune of deleted files; trustworthy `orphans` (value-wired handlers).
- **v0.6.0** — TypeScript first-class (LSP backend, `--precise` callHierarchy); `references` edge for
  value-wired handlers; `codemap_unannotate`; studio `ctrl+g`.
- **v0.5.0** — precise call resolution via in-process go/types (`--precise` Pass 3, `edges.provenance`).
- **v0.2.0** — annotation layer surfaces on every surface; documented per-agent MCP registration.
- **v0.1.0** — first public release: CLI · MCP (17 tools) · studio · graph+vectors+LSP · annotations.

## Product vision — agent harness on top of the toolchain
codemap (with vecgrep/vidtrace/fcheap/noted, all XDG-stored) powers an agent harness that analyzes & fixes
codebases. codemap's role: **structural understanding + the annotation layer**, as the structural-
intelligence hub that both *feeds* ground-truth to and *fetches* meaning/runtime/secrets from its siblings.
Ecosystem flow: **vidtrace** (repro) → **vecgrep** (semantic) → **codemap** (structure/impact) → **fcheap**
(persist artifacts), with findings pinned back onto the graph as durable annotations. See the Ecosystem
integration epic below; per-sibling design plans live at each sibling repo's ROOT (CODEMAP-INTEGRATION.md).

---

## Open priorities (forward-looking)
Ordered by leverage (from a verified state-review + adversarial critic, 2026-06-24).

- [~] **P0 — §1 ask-2: real TS/JS/Python call graph on demand.** `impact`/`callers`/tests are EMPTY by
  default for the LSP languages (no call graph without a whole-project `index --precise`). §1 made this
  *honest* (a `resolution` note instead of false `[]`), not *resolved*. The flagship for the PRIMARY (agent)
  audience. Slices:
  - [x] **Slice 1** — generalized `preciseRelations`/`PreciseCallers`/`PreciseCallees` (service.go) from
    gopls-only to any LSP language via `lspServerFor` (reuses `lspsrc.DefaultServers`: gopls/tsserver/pyright,
    `.tsx`→typescriptreact). Per-symbol `callHierarchy` now resolves on demand with NO `--precise` reindex.
    `TestPreciseCallersTypeScript` (passes locally, structure-only index); task check green.
  - [x] **Slice 2** — `Callers`/`Callees` auto-upgrade: when `callGraphUnavailable` set the "unresolved" note
    for an LSP-language symbol, `autoUpgradeRelation` drives a scoped on-demand `callHierarchy` and returns
    real results (clearing the note); no-op for Go/precise indexes (no latency); keeps the honest note if the
    server is absent. `TestCallersAutoUpgradesTypeScript`; task check green. (Deferred: **`Impact`** auto-upgrade
    — its blast radius is transitive, so on-demand needs recursive callHierarchy; left note-only for now, a
    follow-up could at least fill `direct_callers`. An agent can use `callers` now, which resolves.)
  - [x] **Slice 3** — heuristic test coverage: when the call graph finds NO tests for a symbol, `Impact` scans
    test files (`isTestFilePath`: `_test.go`/`.test|.spec.{ts,tsx,js,…}`/`test_*.py`) for a word-boundary
    reference to the symbol name (`heuristicTestCoverage`), adds them as `Heuristic:true` `ImpactNode`s, and
    sets `Untested=false` + a note. Defeats #196's filtered-callback blind spot (a covered TS symbol read
    untested because its call lived in a filtered anonymous `it(()=>…)` callback) and works with no call graph.
    `TestImpactHeuristicTestCoverage` (pure-Go); task check green. **P0 core complete** (callers/callees resolve
    on demand; untested is honest). Remaining optional: `Impact` direct_callers/blast on-demand (transitive,
    deferred).
- [ ] **P1 — cost envelope.** Measure index time, `--precise` wall-clock, DB size, query latency on ≥1 real
  medium repo (1k+ symbols) per language. The whole pitch is "cheaper than file reads," and `--precise`
  (mandatory for TS/JS/Py graphs) is the unmeasured expensive path; also de-risks the 0.2 tree-sitter call.
- [ ] **P2 — MCP-transport + failure-path test coverage.** glyphrun exercises only 1 of 20 tools over the
  real stdio transport; every flow asserts exit 0. Invoke the flagship tools (impact/context/semantic/
  callers/orphans) over MCP; add failure-path flows (missing symbol, malformed `codemap.yaml`); **bind
  studio snapshots to goldens** (today only pane-presence is gated; the sole formal outcome is `clean_exit`).
- [ ] **P3 — dogfood fidelity fixes** (all verified real):
  - [x] **D** — Go package-level `var`/`const` now indexed as `KindVariable` (gosrc `valueSymbols`, top-level
    only; blank `_` and function-local var/const stay out). `version.Version`, sentinel errors, const blocks
    are findable. Test `TestExtractVarConst`; `task check` green; dogfood-verified via `find`. (No fixture
    shift — fileA/fileB and flow snapshots had no top-level var/const.)
  - [x] **E** — scanned-but-skipped files (parse error AND oversized) now record their hash in
    `index_state` (in `indexFile`), so staleness no longer reports them as perpetually "new" and a
    re-index clears the false drift. The parse error is still surfaced once. Test
    `TestStalenessTracksParseErrorFile`; `task check` green.
  - [x] **B** — generated code skipped two ways: `*_gen.go` added to default excludes (config.go, caught by
    both the indexer + staleness walks) AND a header heuristic `isGenerated` (canonical
    `// Code generated … DO NOT EDIT.` before the package clause) in `indexFile` — robust across
    sqlc/protobuf/stringer regardless of filename; skipped files surface in `Result.Generated` + record their
    hash (no false staleness). Tests `TestIndexSkipsGeneratedCode`/`TestIsGenerated`; `task check` green.
- [ ] **P4 — studio for humans** (capped — human surface on an agent-first project; do the cheap wins,
  defer the rest until P0 lands): mouse support (`run.go:14` passes zero options — no `WithMouseCellMotion`);
  drive `ctrl+r` from empty/cold states instead of telling the user to open a shell (`view.go:399-408`).
  Second wave: a **Path tab** (`svc.Path` has zero TUI surface), in-TUI annotate (svc.AnnotateNode exists,
  rendered but read-only), jump-to-`$EDITOR` + yank `file:line`, blast-radius-as-tree, scrollbars, fix the
  per-tab `q`/digit key traps.
- [ ] **P5 — Ecosystem integration** (the EI.* epic below).
- [ ] **Unbuilt query/tooling debt:** E2.3 unified extractor (LSP-over-go/parser merge + FQN dedupe);
  E3.2 `codemap_semantic_callers`; E3.3 `codemap_refactor_plan`; E2.5 remainder (`references`/`symbols`/
  `dependencies` MCP tools); E3.1 TODO (`impact` semantically-similar via `vector.Similar`); E5.1 high-
  coverage unit sweep.
- [ ] **Process:** cut **0.10.0** (FIX.md §1/§2/§3 are on `main`; needs explicit auth). Move/delete the
  **untracked** `FIX.md` (it's fully addressed and outside the root-`.md` convention).

---

## 🎯 Epic — Ecosystem integration (codemap as the structural-intelligence hub)

> codemap both FEEDS structural ground-truth to siblings and FETCHES meaning/runtime/secrets back.
> Shared rails: veclite (Ollama nomic-embed-text 768-dim cosine), the XDG project registry,
> newline-delimited MCP stdio, and the reindex-durable annotation layer (kind:node|path, opaque
> data). Each task degrades gracefully when the sibling is off `$PATH`. Supersedes the old E7 stub.
> Per-sibling design plans live at each repo's ROOT — CODEMAP-INTEGRATION.md (vecgrep, veclite, tinyvault, file.cheap,
> glyphrun, cairntrace).

### Foundation (unblocks everything; build first)
- [ ] **EI.1** file:line → enclosing-symbol/FQN resolver as a first-class entry point: `codemap impact
  --at <file:line>` (CLI) + accept a position in the relevant MCP tool. The single join that wires every
  sibling's file:line results onto the graph (used by EI.7/EI.8/EI.11).
- [ ] **EI.2** `codemap impact --files a,b,c` aggregation: per-file symbols + aggregate blast_radius +
  covering tests (feeds fcheap diff-lift EI.9 and affected-specs EI.5).
- [ ] **EI.3** Cross-read sibling registries: codemap_projects/codemap_status report `{name, abs_path,
  has_graph, has_chunks (vecgrep ~/.vecgrep/config.yaml), has_specs+last_suite (newest .glyphrun/runs/*/
  run.json), has_secrets (tvault by-name)}`; emit "also registered in <sibling>" hints. Pure filesystem reads.
- [ ] **EI.4** Document the annotation `source` enum for the new producers (vecgrep, fcheap, glyphrun,
  glyphrun-flaky/-repair, cairntrace, tinyvault, tinyvault-audit) in docs.go + `annotate --source` help;
  verify data stays opaque (no schema change).

### Feed structure to siblings (codemap → tool)
- [ ] **EI.5** `codemap affected-specs --since <ref>`: git diff → codemap_impact → blast radius → intersect
  spec↔symbol links → minimal spec-path list for `glyph run` / `cairn run`. Blast-radius-driven spec selection.
- [ ] **EI.6** Serve codemap_impact reverse-call file-set + covering tests in the shape `vecgrep_related_files`
  needs, so vecgrep delegates imports/imported_by/tests to the real graph (vecgrep implements the client).

### Fetch meaning + runtime + secrets back (tool → codemap)
- [ ] **EI.7** Ingest glyphrun + cairntrace run results as behavioral-coverage annotations: map
  target.cmd/spec entrypoint → symbol (codemap_find), write `codemap_annotate{source:glyphrun|cairntrace,
  note:'spec X passed/failed N/M', data:{specName,contractHash,runId,status,outcomes,durationMs}}`;
  contractHash invalidates stale green badges. A `codemap behavior-link` adapter reading run.json.
- [ ] **EI.8** Ingest fcheap/vidtrace/cairntrace evidence: after EI.1 resolves a stash/run to a symbol, pin
  `codemap_annotate{source:vidtrace|fcheap, data:{stash_id,bundle_type,frame,ocr_snippet,content_hash,runId}}`
  — pointer + summary only; heavy artifacts stay in fcheap.
- [ ] **EI.9** Structural re-rank served to `fcheap connect` / `cairn investigate`: codemap_semantic+find on
  failing-outcome text/URLs, re-rank by codemap_hotspots + codemap_callers depth; return
  `{symbol,blast_radius_size,covering_tests,callers}`.
- [ ] **EI.10** codemap_context/impact optionally call vecgrep `memory_recall(tags:['codemap'])` and attach
  `Memory{Content,Importance,Tags}` as "related notes" beside annotations (optional vecgrep memory client).

### Secrets (tinyvault; strictly value-free — only key names cross the seam)
- [ ] **EI.11** `codemap_secret_impact`: accept keys[] (or call `vault_list_secrets_by_prefix`), find
  os.Getenv/process.env/os.environ usages, resolve symbol, run codemap_impact → `{key,defined_at,
  used_by_symbols,blast_radius_count,covering_tests}`; pin as annotation (source:tinyvault). Rotation blast radius.
- [ ] **EI.12** `codemap index --via-vault <project>`: thin wrapper over `vault_run_with_secrets(command=
  [codemap,index,--precise])` so registry creds (GOPRIVATE/NPM_TOKEN/PIP_INDEX_URL) reach spawned
  gopls/pyright/tsserver. Docs + wrapper; tinyvault unchanged. (Realizes the original LSP-creds motivation.)
- [ ] **EI.13** Least-privilege seal scope: codemap_callees from a service entrypoint → required_keys, emitted
  for `vault_seal_for_recipients`/`vault_export_env --keys` (needs a tinyvault explicit-keys filter add).

### Shared substrate (veclite; deepest — after the wins above)
- [ ] **EI.14** Push call graph into veclite KnowledgeGraph (AddEntity/AddRelationship) + a named `structure`
  space (blast_radius/in_degree/is_orphan) with FuseRRF, so semantic hits re-rank by centrality with no graph
  round-trip. Reuses stored embeddings.
- [ ] **EI.15** Agree the ecosystem `EmbeddingProfile` key (provider:model:dims:distance:chunker) with
  veclite/vecgrep/fcheap; `Compatible()`-gate a read-only cross-read of vecgrep's chunks collection so
  codemap_semantic reuses finer function/block chunks (join by line-range overlap) — no second Ollama pass.
- [ ] **EI.16** Annotations as searchable veclite agent-memory: annotate also `InsertTextDocument(node_id,
  fqn,source)`; recall via HybridSearch → node_ids (exact-target-only today). Survives reindex.

### Discovery + deferred
- [ ] **EI.17** Index glyphrun/cairntrace spec intent+outcome text into codemap's veclite (kind:spec);
  codemap_semantic returns specs + code together; uncovered symbols (codemap_orphans) emit `glyph spec
  scaffold`/`cairn spec scaffold` stubs seeded from codemap_context.
- [ ] **EI.18** (from old E7) MCP registration snippets (Claude Code, Hermes, vecai, local-agent); hunk
  `--agent-context` sidecar from codemap_impact; noted integration (notes about cycles/dead code).

**Sequencing:** EI.1+EI.2+EI.4 (foundation) → EI.3+EI.6 (registry + first feed) → EI.7→EI.5 (behavioral loop)
→ EI.8+EI.9 (evidence pinning + re-rank) → EI.12→EI.11→EI.13 (secrets) → EI.10+EI.16+EI.14 (memory/substrate)
→ EI.15+EI.17 (deep substrate + discovery). EI.18 is cross-cutting, anytime.

---

## 🎯 Epic — branch-aware index + background daemon (codemap + vecgrep) — GREENLIT 2026-06-24

> Build in **codemap first**, then the user spawns agents for the vecgrep + fcheap halves (plans live in
> those repos' ROOT; full cross-project spec in the vault). **gpeek dropped** (Swift macOS GUI git client).
> Trigger = a plain git **`post-checkout` hook**. See [[branch-index-and-daemon-direction]] memory + the
> vault spec `~/notes/projects/codemap/branch-index-and-daemon-spec.md`.

### Feature A — per-branch index switching (via fcheap)
> On checkout, snapshot the leaving branch's index to fcheap (keyed by repo+branch+base-sha) and restore
> the entering branch's snapshot — else incremental reindex. codemap CANNOT copy a file (single shared
> graph.db + codemap.veclite sliced by project), so it **serializes its project slice** and restores via
> WipeProject + bulk-insert; vecgrep just file-copies its per-branch veclite.
- [x] **BD.1** `internal/git/branch.go` — `CurrentBranch`/`HeadSHA`/`RepoRoot`/`IsDetached`/`SanitizeBranch`
  (always-hash for collision-free path segments)/`RepoHash` (sha1[:12] of the symlink-resolved root) +
  `Inspect`→`Status`, all via `git` shell-out (`exec.CommandContext`, no CGO). Read-only `codemap branch-status
  [path] [--json]` (Service `BranchStatus` + CLI) reports branch/sha/detached/repoHash/key. Tests
  `TestInspect`/`TestSanitizeBranch`/`TestRepoHashStable` (init a temp repo, commit, detached-HEAD); task check
  green; dogfood-verified on codemap itself.
- [ ] **BD.2** `internal/snapshot/snapshot.go` — `Export(graph, vectors, projectID, project, dir, profile, baseSHA)` → `nodes/edges/index_state/annotations/vectors.jsonl` + `snapshot.json` (**deterministic ordering** so fcheap dedups identical slices). `Import` = WipeProject + DeleteByProject then bulk re-insert, **gated on embeddingProfile match**. New graph helpers `ProjectEdges`/`ProjectIndexState`; new `vector.Store.IterByProject` (mirror DeleteByProject). ⚠ **Merge (don't blow away) annotations** on import.
- [ ] **BD.3** `internal/snapshot/fcheap.go` — exec wrapper: `Save(dir,tool,name,tags,sourceSHA)->stashID` (parse `--json`), `Restore(id,toDir)` (check `Verified`), `List(tags)`. fcheap binary path from config/PATH.
- [ ] **BD.4** `internal/branchstate/state.go` — pointer file `RegistryDir()/branches/<repoHash>.json` (atomic temp+rename): branch→{stash_id, base_sha, profile, counts}. `Rebuild` from `fcheap list --tag`.
- [ ] **BD.5** `internal/app/branchswitch.go` — `BranchSnapshot`/`BranchSwitch`/`BranchStatus` Service methods; orchestrate snapshot-old → restore-or-reindex-new; base-sha staleness via `git merge-base --is-ancestor`; reuse `Service.Index` profile gate; detect daemon lock.
- [ ] **BD.6** `cmd/codemap/main.go` — `branch-switch [--from --to --root --force-reindex --install-hook --json]`, `branch-snapshot`, `branch-status`. `--install-hook` writes `.git/hooks/post-checkout` (worktree/`core.hooksPath`-aware; check the hook `flag` arg so it skips `git checkout -- file`).
- [ ] **BD.7** `internal/mcp/server.go` — `codemap_branch_switch` / `codemap_branch_status` tools.

### Feature B — background daemon (incremental sync + Ollama throttle)
> One process owns the writable handle, watches the FS, serves all CLI/MCP/studio clients over a unix
> socket (fixes the multi-process veclite lock), and throttles Ollama so background re-embeds don't
> saturate it or starve interactive search. (codemap has NO watcher today and embeds inline per-file with
> no dedup; vecgrep already has a dormant `internal/index/watcher.go`.)
- [ ] **BD.8** Port a watcher into `internal/index/watcher.go` (fsnotify + debounce/coalesce; filter via `IndexConfig.Exclude` + `extract.LanguageForPath`; codemap doesn't yet import fsnotify). Wire the delete path: removed/renamed → `graph.DeleteNodesInFile` + `DeleteFileHash` + `vectors.DeleteByFile` (reuse `pruneDeleted`).
- [ ] **BD.9** `Indexer.IndexFiles(ctx, projectID, name, root, rels, opts)` — run the existing `indexFile` loop over only changed rels + `resolveEdges` (the SHA256 hash check already makes it incremental). The watcher's reindex target.
- [ ] **BD.10** `internal/embed/throttle.go` — `ThrottledProvider` (embed.Provider decorator): content-hash **dedup** (codemap has none today), coalescing queue + bounded worker pool (`EmbedWorkers`, default 2), token-bucket rate (`x/time/rate`, `EmbedRPS`) + max-in-flight, **two priority lanes** (query > background), backpressure. Replaces inline `ix.embedder.Embed` under the daemon.
- [ ] **BD.11** `internal/daemon/` — `Daemon{session, watcher, throttle, mcpHandler, listener}`: open the sole write Session, write `daemon.{sock,json,lock}` under `config.DataDir()`, accept-loop on the unix socket routing `mcp.*` frames (newline-JSON, **never Content-Length**) + `daemon.*` control RPCs; idle-timeout + SIGTERM-clean teardown.
- [ ] **BD.12** `cmd/codemap/main.go` — `daemon start|stop|status`. Make `serve` a stdio↔socket **bridge** when a daemon is live; make query commands + `codemap_*` tools prefer the socket, else `VectorsReadOnly` fallback. Keep LSP Content-Length strictly inside `internal/lsp`.
- [ ] **BD.13** `internal/config/config.go` — `DaemonConfig{Autostart, IdleTimeout(30m), EmbedWorkers(2), EmbedRPS, EmbedMaxInFlight, Debounce}` + `CODEMAP_DAEMON_*` overrides. Surface daemon state in `status` / `codemap_status`.

**Compose:** the daemon performs the branch switch (`daemon.switchBranch` RPC: re-point watcher, reopen slice) and re-embeds only the diff via the throttle; `branch-switch` delegates to a running daemon instead of opening a second writer.
**Sequencing:** BD.1 (detection, read-only status) → BD.8+BD.9+BD.10 (watcher + IndexFiles + throttle — the daemon's guts) → BD.11+BD.12+BD.13 (daemon process + serve + config) → BD.2–BD.7 (branch snapshot/switch, the heavier serialize/restore) → `--install-hook`. Do vecgrep's simpler file-copy version in parallel (its agent) to de-risk the model.

---

## 🎯 Epic — multi-language support (LSP backend) — **SHIPPED** (v0.6.0–v0.7.0)
> Design record (iteration log archived). User greenlit TS/JS, Python, Docker, HTML, CSS, Vue. Chosen
> approach (registry-plus-structure): extend `internal/extract/lspsrc` into a generic LSP-driven extractor
> fed by a tiny server registry (lang, langID, binary, args), LookPath-guarded, registered next to gosrc in
> IndexProject. The Go path is byte-for-byte untouched; queries are backend-blind (generic
> extract.Symbol/Reference) so callers/impact/hotspots/path/semantic work with ZERO query changes. Pure-Go
> (spawned subprocess, like gopls; no tree-sitter/CGO). Honest scoping: TS/JS+Python are call-graph-capable;
> Docker/HTML/CSS/Vue are STRUCTURE-ONLY (Pass-2b, never built — see Deferred). **Vue via Volar = dead end**
> for the generic documentSymbol/callHierarchy driver (#133); realistic path is parsing the SFC `<script>`.

## 🎯 Epic — precise call resolution (go/types) — **SHIPPED** (v0.5.0)
> Design record. Name-based over-matching is eliminated by resolving calls with pure-Go `go/types`. Opt-in
> `--precise` Pass 3 using in-process `golang.org/x/tools/go/packages`+`go/types` (NOT gopls) in
> `internal/extract/typesrc/`. Name-based Pass 2 stays the fast default. "Precise supersedes name" made
> deterministic by an explicit `edges.provenance` column; supersede = delete-then-insert per clean source
> node, same edge_type ⇒ zero query changes. Degrades per-package (type-check error keeps that package's
> name edges) and globally (no `go` toolchain ⇒ no-op).

---

## Deferred by design
- **tree-sitter backend** (TD1/R2) — needs CGO, breaks the `CGO_ENABLED=0` pure-Go release model; stays
  behind the `treesitter` build tag, out of release binaries. Target for **0.2** (round out language
  coverage + large-repo speed), ideally via a pure-Go WASM (wazero + tree-sitter.wasm) path, never default
  CGO. Q-CGO (below) still open.
- **Vue / SFC support** — Volar dead-end (#133); realistic path is parsing the SFC `<script>` into the TS
  pipeline (couples to the deferred markup/tree-sitter layer). `.vue`/`.html`/`.css` mapped only to report "planned".
- **Structure-only markup layer (Pass-2b)** — Docker/HTML/CSS/Vue as reference/import edges by path; sequenced last, unbuilt.
- **Named vector spaces** (TD2) — docstring/signature/comment spaces (veclite supports them) → 0.2+. One space today.
- **Domain-entity / LogicLens nodes** (TD3) — needs LLM enrichment at index time → Phase 5.
- **Daemon mode** — lazy-open DB (TD5) is the v1 multi-process answer; a shared daemon was not built.
- **Large-TS speed** — useSyntaxServer/initializationOptions experiment gave no speedup, reverted (#140);
  real fix is tree-sitter or tsserver-open pacing (a dedicated future effort).
- **Repo-local DATA storage** (store the index in `.codemap/`) — deferred data-model change; `--local`/
  `.codemap` only affects CONFIG discovery today.
- **ntcharts v2 real charts** for Metrics (E4.2) — hand-rolled ASCII bars today to avoid the replace-directive risk (R1).

---

## Resolved product decisions (user, 2026-06-23)
- [x] **D1. v0.1 scope = EVERYTHING** — MVP + LSP + studio TUI all ship in 0.1 (Epics 1–6). Epic 7 (deep ecosystem) may trail.
- [x] **D2. studio TUI = tabbed** — `[1] Graph` · `[2] Metrics` · `[3] Impact` · `[4] Search`.
- [x] **D3. config = XDG + `~/.codemap` fallback** — `$XDG_CONFIG_HOME/codemap/config.yaml`,
  `$XDG_DATA_HOME/codemap/`, `$XDG_CACHE_HOME/codemap/`; `CODEMAP_*` overrides; honor `~/.codemap/`. **Config = YAML.**

## Tech decisions (reversible — flag if you disagree)
- [x] **TD1. v0.1 extraction = pure-Go (LSP + stdlib `go/parser`), `CGO_ENABLED=0`.** tree-sitter needs CGO
  (breaks clean cross-compile) → OPTIONAL backend behind the `treesitter` build tag, out of release binaries,
  targeted for 0.2. ⚠️ **Biggest deviation from SPEC's dual-backend MVP.**
- [x] **TD2. One vector space in v0.1** (code text). Named spaces (docstring/signature) → 0.2+ (veclite ≥0.17).
- [x] **TD3. No domain-entity/LogicLens nodes in v0.1** (needs LLM enrichment) → Phase 5.
- [x] **TD4. Registry = `$XDG_DATA_HOME/codemap/projects/`** (+ `~/.codemap` fallback), `init --local` escape hatch. Separate from vecgrep's.
- [x] **TD5. Lazy DB open** on first query (v1 multi-process answer).
- [x] **TD6. LSP client** — superseded: shipped a hand-rolled Content-Length JSON-RPC client (internal/lsp), no new deps (see E2.1).
- [x] **TD7. MCP stdio = newline-delimited JSON-RPC** (go-sdk StdioTransport). Never let LSP's Content-Length framing leak into MCP. (Hard-won from `glyph`.)
- [x] **TD8. CLI emits `--json`** for agents (three-surface pattern from noted).

## Verified dependency set (pinned in go.mod)
- go `1.25.x` · module `github.com/abdul-hamid-achik/codemap`
- `github.com/modelcontextprotocol/go-sdk` **v1.6.1** · `github.com/abdul-hamid-achik/veclite` **≥ v0.17.0** (in-tree 0.19.0)
- `modernc.org/sqlite` **v1.53.0** (pure-Go) · `github.com/spf13/cobra` **v1.10.2**
- `go.lsp.dev/protocol` **v1.0.0** + `go.lsp.dev/jsonrpc2` **v1.0.0** (note: E2.1 hand-rolled the client) · `gopkg.in/yaml.v3` v3.0.1
- **TUI (Charm v2, vanity paths!):** `charm.land/bubbletea/v2` **v2.0.7** · `charm.land/lipgloss/v2` **v2.0.4** ·
  `charm.land/bubbles/v2` **v2.1.0** · `charm.land/glamour/v2` **v2.0.1**
- **Syntax highlighting (added FIX.md §3):** `github.com/alecthomas/chroma/v2` (pure-Go)
- **Optional/0.2:** `github.com/tree-sitter/go-tree-sitter` (CGO); SCIP import bindings. Embeddings: POST `http://localhost:11434/api/embed`.
- **Build/CI:** goreleaser **v2.16.0**; GitHub Actions checkout@v6 + setup-go@v6 + goreleaser-action@v6; Task `v3.x`.
  Secrets: `GITHUB_TOKEN` (auto) + `HOMEBREW_TAP_TOKEN` (PAT → homebrew-tap, from `~/.config/secrets/env`).

## Active risks
- **R1.** ntcharts v2 `replace`s bubbletea to a fork (ignored by consumers) → may not build against v2.0.7. Build early; mirror the replace or pin.
- **R2.** tree-sitter = CGO → breaks static cross-compile. Mitigated by TD1 (optional/0.2).
- **R3.** `modernc.org/sqlite` high release cadence → pin exact; integration-test before bumps.
- **R4.** LSP servers vary → client tolerates unknown/optional fields; lock server versions in CI.
- **R5.** Ollama is a runtime dep → detect `nomic-embed-text` at startup; clear error if missing.

## Open questions
- [!] **Q-CGO** — keep v0.1+ release binaries pure-Go with tree-sitter deferred to 0.2 (per TD1), or pull tree-sitter into release binaries (accept CGO + zig-cc matrix)?

---

## Original epic checklist — remaining open work
Epics 0–6 are complete and shipped through v0.9.1 (full per-item detail in the vault archive). Remaining:
- [ ] **E2.3** unified extractor (merge LSP precedence over go/parser, dedupe by FQN) — Go + LSP paths run side-by-side today.
- [~] **E2.5** MCP query tools — callees/impact/path/hotspots/orphans done; `references`/`symbols`/`dependencies` still TODO.
- [ ] **E3.2** `codemap_semantic_callers` (semantic → graph expansion) · [ ] **E3.3** `codemap_refactor_plan`.
- [ ] **E3.1 follow-up** — `impact` add semantically-similar (needs `vector.Similar`).
- [ ] **E4.2** ntcharts real charts for Metrics (validate R1 first) · **E4.3** Graph node-link/Sugiyama canvas (future).
- [ ] **E5.1** high-coverage unit sweep (graph traversal/cycles, extract, search, config).
- [ ] **E7 → Ecosystem epic** (above) supersedes the old E7.1–E7.4 stubs.
