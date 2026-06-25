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
Released through **v0.14.0** (`brew install abdul-hamid-achik/tap/codemap`). Pure-Go, `CGO_ENABLED=0`,
5 cross-compiled targets. Three surfaces over one store: **CLI** (24 commands incl. `daemon`, `--json`), **MCP**
(`codemap serve`, 22 tools), **studio** TUI (Graph/Metrics/Impact/Search + `?` help + source & context
overlays). Languages: **Go** (go/parser + opt-in `--precise` go/types) and **TypeScript/JavaScript/Python**
(one typescript-language-server for TS+JS, pyright for Python; `--precise` = the unified exact pass —
go/types for Go, LSP `callHierarchy` for the rest). Semantic vectors via veclite + Ollama nomic-embed-text
(hybrid vector+BM25). **Annotation layer** (pin notes + opaque data to nodes & call paths, survives reindex,
surfaces on every surface). Flagship one-call **`context`** bundle (def + callers + callees + tests + blast
radius). Graph analytics: `impact` (cycle-safe blast radius + covering tests), `hotspots`, `orphans`, `path`.
Agent-trust honesty: index freshness/`stale`, ambiguous-name notes, name-inflation flags, call-graph-
unavailable `resolution` note. `doctor`, multi-project registry, incremental reindex with deleted-file pruning.

## ✅ DONE — Homebrew formula→cask migration (completed at v0.14.0, the first cask release)
`.goreleaser.yaml` publishes a **cask** (`Casks/codemap.rb`), not a formula (1c165ad). At the v0.14.0
release the tap was migrated: added `tap_migrations.json` = `{"codemap":"codemap"}` (auto-migrates existing
formula users to the cask on `brew upgrade`) and deleted the stale `Formula/codemap.rb`. `brew install
abdul-hamid-achik/tap/codemap` is unchanged. (Research: GoReleaser discussion #5563, Homebrew brew#20800.)

## Release history (condensed — full detail in the vault archive)
- **v0.14.0** — codemap⇄vecgrep ecosystem integration (related-files, symbol-at, semantic fallback +
  project-scoped memory recall via vecgrep, project_key/sibling status), batched-concurrent embedding
  (big win for network providers), studio alt+1–4 tab fix, and the **first Homebrew cask release**.
  Released 2026-06-25 (run 28144626270). Tap migrated formula→cask (tap_migrations.json + Formula removed).
- **v0.13.0** — daemon observability. Live daemon state in `codemap status` + `codemap_status` (a `daemon`
  object in `--json`); `daemon start --no-embed` (structure-only); studio header `● daemon` indicator;
  `codemap doctor` daemon-health check; full daemon docs; glyphrun flows (`daemon.yml`, `exclude_extra.yml`).
  Released 2026-06-24 (run 28135984585, brew tap bumped).
- **v0.12.0** — background daemon + config ergonomics. **Daemon (BD.8–13)**: `codemap daemon start/stop/status`
  watches the tree (fsnotify, debounce/coalesce), incrementally reindexes, throttles Ollama embeddings (token
  bucket + dedup + max-in-flight), and serves control RPCs over a unix socket; tuned via `DaemonConfig` +
  `CODEMAP_DAEMON_*`. **Configurable excludes**: `index.exclude_extra` (appended, no longer clobbers defaults)
  + path-aware globs (bare=any-depth, slash=root-anchored, `**/`=any-depth). **3-way config**: every knob
  reachable via config file < env < CLI flag. Released 2026-06-24 (run 28132071238, brew tap bumped).
- **v0.11.0** — the flagship + branch-aware index switching. **P0**: TS/JS/Python `callers`/`callees` resolve
  on demand (scoped per-symbol `callHierarchy` via `lspServerFor`, no `--precise` reindex) and `impact`
  reports honest test coverage (heuristic test-file scan, defeating #196's filtered-callback blind spot).
  **Branch-index (BD.1–7)**: a `git checkout` switches the code index — call graph + semantic vectors — by
  snapshotting the old branch into **fcheap** and restoring the new one (no reindex/re-embed; incremental
  fallback when stale), via `codemap branch-switch`/`branch-snapshot`/`branch-status`, an auto-installing
  `post-checkout` hook (`--install-hook`), and MCP (`codemap_branch_switch`/`_status`; 22 tools). New
  internal packages: `git`, `snapshot`, `branchstate`. Pure-Go, no new module deps.
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

### codemap ⇄ vecgrep — validated plan (2026-06-24, deep cross-repo investigation)
> Full plan: `~/projects/vecgrep/CODEMAP-INTEGRATION.md` (v2). **Key finding:** vecgrep already ships a
> codemap client that is **silently dead** — three field-shape/flag mismatches (impact `direct_callers`
> vs `callers`; hotspots `in_degree` vs `refs`; annotate positional vs `--symbol`) each fail-soft to
> heuristics with no trace. Channel = CLI `--json`, one hop, CLI-only (never MCP→MCP). **codemap-side work:**
- [x] **EI.6 / F1 (first slice)** — **codemap side DONE** (commit 1c54074): `codemap related-files <file>
  --json` + `codemap_related_files` MCP + `Service.RelatedFiles` (reuses graph Callers/Callees +
  `heuristicTestCoverage`); emits C1 with `indexed` + golden contract fixture/test
  (`testdata/contracts/related_files.json`). *vecgrep side (repoint client, delete dead structs): in
  progress via spawned agent on branch `codemap-integration-f1`.*
- [x] **EI.1 / F4 (keystone)** — **DONE** (commit 1c54074): `codemap symbol-at <file>:<line> --json` +
  `impact --at <file>:<line>` + `codemap_symbol_at` MCP + `Service.SymbolAt` + `graph.Store.NodeAtLine`
  (innermost enclosing by line range); resolution exact|enclosing|none; golden contract fixture/test.
- [x] **EI.4** (codemap side, commit 4f6c9a6): annotation `--source` help documents the recognized producer
  enum (note/vecgrep/fcheap/vidtrace/cairntrace/glyphrun/mongosh/postgres).
- [x] **G4 codemap side** (commit 4f6c9a6): `codemap status` / `codemap_status` report a `siblings` list
  (ecosystem tools that also index the project, name-based stat of `~/.<tool>/projects/<name>`).
- [x] **F3** (vecgrep, merged 58e0249): annotate resolves hit `file:line` via `codemap symbol-at` → pins the
  graph-resolved symbol positionally; skips on `resolution=none` (no garbage pins). Still TODO: the reindex/rename **rebind rule**.
- [x] **F2** (vecgrep, 58e0249): rerank uses `in_degree` + `shared_name` down-weighting only; dropped the dead
  blast-radius parse + fixed the misleading docs (option b — made honest).
- [x] **G4 vecgrep side** (58e0249): `vecgrep_status`/`vecgrep index` surface the codemap graph (`Graph: N
  nodes, M edges (stale: …)`) — fixed a THIRD silent skew the golden tests caught (`StatusResult` parsed
  `indexed`+`stale int` vs codemap's `registered`+`stale {changed,new,deleted}`). **Live-verified e2e.**

**Integration KEEP set COMPLETE & live on both mains** (codemap + vecgrep). The once-100%-dead integration
now works across 4 flows (related-files, symbol-at/annotate, status cross-read, structural rerank), each
guarded by golden contract tests in both CIs.

- [x] **G1 — semantic backfill** (codemap, commit 30b9211): measurement justified it (both indexed projects
  are `vectors=0`; graphite is structure-only-in-codemap AND in vecgrep). `Service.Semantic` `Mode="none"`
  → shells `vecgrep search --format json`, maps hits onto the graph (FQN/kind), `mode:"vecgrep"`. Config
  `vecgrep.enabled` (default true) + `CODEMAP_VECGREP_*`. CLI-only one hop, degrades gracefully. Live-verified.
- [x] **G2 — memory_recall** (Proposal E / EI.10) **COMPLETE & live**: `codemap_context` recalls
  project-scoped agent memories from vecgrep's global store. codemap is the authority for the scope key
  (`project_key`=RepoHash, 74e3baf); `Context` shells `vecgrep memory recall <sym> --tags codemap,<key>
  --format json` and attaches a transient `memories` list (codemap fc9b161/1232c20). vecgrep added the
  `memory recall/remember` CLI + a critical **exact tag-AND** fix — `veclite.Contains` is a substring match,
  so it caught that `codemap` matched `codemapper` and a key matched its superstrings (vecgrep cca5ced).
  **Live-verified incl. leak prevention**: 3 memories (correct key / different-project key / superstring key)
  → `context` returned ONLY the correctly-scoped one. Remaining (optional): also attach in `Impact`; docs.

**🎉 codemap ⇄ vecgrep integration COMPLETE** — 7 flows live on both mains (F1 related-files, F4 symbol-at,
F3 annotate, F2 rerank, G4 status cross-read, G1 semantic backfill, G2 memory recall), all golden-tested,
all live-verified, and **documented** (docs/ecosystem.md + cli/config/agent-guide, commit d305cac). codemap
FEEDS structure → vecgrep; codemap FETCHES meaning + memory ← vecgrep. CUT: G3, F5, EI.14. Optional
follow-ups: attach memories in Impact too (left in Context to keep the flagship hot-path exec-free); the
F3 annotation rebind rule on rename (annotations are name-keyed → survive reindex, orphan on rename).

_v0.14.0 shipped 2026-06-25._
- Deferred: G1 (semantic backfill into `Service.Semantic` Mode="none" — measure the empty-embedding case first),
  G2 (memory_recall into context/impact — needs the `['codemap',<project>]` tag governance). **Cut:** G3 (shared
  veclite read), F5, EI.14 (KnowledgeGraph), EI.15 (shelved with G3).

### Foundation (unblocks everything; build first)
- [ ] **EI.1** file:line → enclosing-symbol/FQN resolver as a first-class entry point: `codemap impact
  --at <file:line>` (CLI) + accept a position in the relevant MCP tool. The single join that wires every
  sibling's file:line results onto the graph (used by EI.7/EI.8/EI.11). *(= F4 above; vecgrep is its first consumer.)*
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
> Designed via investigate→design→critique workflow; full plan in `~/projects/tinyvault/CODEMAP-INTEGRATION.md`.
> **Slice 0 + Slice 1 SHIPPED** (codemap 0fc71ae): `tinyvault` added to the annotate `--source` enum; and
> **EI.12** below. Channel = CLI one-hop `--json`; key NAMES only cross (values never enter codemap).
- [x] **EI.12** — `codemap index --via-vault <project>` re-execs the index inside `tvault run -p <project>`
  so registry creds (GOPRIVATE/NPM_TOKEN/…) reach gopls/pyright/tsserver. **Hard-allowlisted to `tvault run`**
  (no `tvault get` reachable → no value leak); LookPath-guarded, degrades when tvault absent. Live-verified.
- [ ] **Scanner primitive (Slice 2, the only net-new cost)** — generalize `heuristicTestCoverage` into a
  literal-scan over all indexed files, **WITH string-context filtering** (the critique's BLOCKING must-fix:
  raw scan + SymbolAt can't tell a real `os.Getenv("K")` read from a comment/log mention inside a function →
  use go/scanner token-kind, or drop non-string hits). Prerequisite for EI.11 + EI.13.
- [ ] **EI.11 `codemap secret-impact` (Slice 3)** — scanner → `symbol-at` → `impact`. Default value-blind
  `keys[]`; `--via-vault`/`--prefix` fetch the inventory via value-free `tvault list/search --json`. Output
  `{key, used_by[], blast_radius, covering_tests, untested, unresolved[], precise, stale}` + value-leak test
  (emit file:line, never line content). Frame as "candidate usage + impact", not authoritative rotation gate.
  Keep `orphan_keys` labeled "no usages found, verify"; **CUT `unmanaged_keys` from v1**.
- [ ] **EI.13** — `codemap callees <entrypoint> --keys` → `required_keys[]` for `vault_seal`/`vault_export_env{keys}`
  (tinyvault side verified done). Rides the Slice 2 scanner (keys read by the transitive callees).

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
- [~] **BD.2** `internal/snapshot/snapshot.go` — `Export`/`Import` of a project's slice.
  - [x] **BD.2a (graph)** — Export → `nodes/edges/index_state/annotations.jsonl` + `snapshot.json`;
    **deterministic** (nodes sorted by content key; edges reference nodes by sorted POSITION not the volatile
    DB id; annotations sorted) so identical slices serialize byte-identically and fcheap can content-dedup.
    Import = WipeProject + bulk re-insert with index→new-id remapping, **gated on embeddingProfile match**,
    and **MERGES** annotations (adds missing, never deletes/duplicates). New graph helpers `ProjectEdges` +
    `ProjectIndexState` (+ `IndexEntry`). `TestRoundTrip` + `TestExportDeterministic` (swapped insertion
    order → identical bytes); task check green.
  - [x] **BD.2b (vectors)** — `vectors.jsonl` carries each embedding by node POSITION (remapped on import like
    edges) + raw vector/content/meta, so restore needs NO re-embed. New `vector.Store.IterByProject` →
    `[]VecRecord{Vector,Content,Meta}` (reads `veclite.Record.Vector/.Content/.Payload`). `Export`/`Import` gained
    a `vec *vector.Store` param (nil = graph-only); Import clears + re-inserts vectors with the new node ids.
    `TestVectorRoundTrip` (restored vector points at the new node id + is searchable); task check green.
    **BD.2 complete.**
- [x] **BD.3** `internal/snapshot/fcheap.go` — exec wrapper over the real fcheap CLI (verified v0.24.1):
  `FcheapSave(dir,tool,name,tags,sourceSHA)→stashID` (`save … --no-scan --json`, parse `.id`),
  `FcheapRestore(id,toDir)→verified` (`restore … --json`, check `.status=="restored"` + `.verified`),
  `FcheapList(tags)→[]StashInfo` (`list --json` + single server-side `--tag`, extra tags AND-matched
  client-side). `FcheapBinary`/`FcheapStashDir` overridable. `TestFcheapRoundTrip` (real save→list→restore,
  gated on `fcheap` on PATH); task check green.
- [x] **BD.4** `internal/branchstate/state.go` — per-project pointer file at `DataDir()/branches/<repoHash>.json`
  (`StatePath`): `State{repo_root/hash, project, default/active_branch, branches:{<b>:{stash_id, base_sha,
  embedding_profile, node/vector_count, last_switched_at}}}`. `Load` (missing→empty), `Save` (atomic
  temp+rename), `Lookup`, `Record` (stamps time), `Rebuild(ctx, repoHash)` from `FcheapList([codemap-index,
  repo:<hash>])` parsing `branch:<name>` tags (newest stash per branch). `TestStateRoundTrip` +
  `TestRebuildFromFcheap` (real fcheap, gated); task check green.
- [x] **BD.5** `internal/app/branchswitch.go` — the orchestration keystone. `BranchSnapshot(ctx, root, branch)`:
  `git.Inspect` → `snapshot.Export(g, vec, pid, name, tmp, profile, sha)` (vec/profile only if embedded) →
  `snapshot.FcheapSave` (tags codemap-index/repo:/branch:) → `branchstate.Record`+`Save`. `BranchSwitch(ctx,
  root, from, to)`: snapshot `from`, then restore `to`'s stash (`FcheapRestore`+`snapshot.Import`) when it exists,
  the profile matches (`profileCompatible`), and `git.IsAncestor(baseSHA, HEAD)` is fresh — else incremental
  reindex via `svc.Index`. Clean no-op on detached HEAD / non-git. New `git.IsAncestor` (`merge-base
  --is-ancestor`). **End-to-end test `TestBranchSwitchRestoresSnapshot`** (feature→main restores main's snapshot:
  FeatureOnly gone, MainOnly back, no reindex; git+fcheap-gated). task check green. **Branch-switch is functional.**
- [x] **BD.6** `cmd/codemap/main.go` — `branch-switch [--from --to --root --install-hook --json]` +
  `branch-snapshot [--branch --root --json]` wired to `svc.BranchSwitch`/`svc.BranchSnapshot` (default to the
  current git branch). `--install-hook` → `app.InstallPostCheckoutHook` writes an executable, idempotent,
  guarded `.git/hooks/post-checkout` (resolves the hooks dir via `git.HooksDir`; appends to an existing hook;
  fires only when `$3==1`, a branch checkout) that runs `codemap branch-switch --to <current>`. `BranchSwitch`
  defaults `from` to the pointer-file `ActiveBranch` (hook only knows `to`); snapshots key on the branch's tip
  sha (`git.BranchSHA`), not HEAD. Robustness: `CurrentBranch`/`IsDetached` now use `symbolic-ref` (works on an
  UNBORN branch). Tests `TestInstallPostCheckoutHook` + from-defaulting in `TestBranchSwitchRestoresSnapshot`;
  task check green.
- [x] **BD.7** `internal/mcp/server.go` — `codemap_branch_status{path}` → `svc.BranchStatus` and
  `codemap_branch_switch{path, to?, from?}` → `svc.BranchSwitch` (defaults `to` to the current branch). Registered
  (now **22 MCP tools**), presence test extended. task check green. **🎉 Branch-index BD.1–7 COMPLETE** — git
  checkout switches the index (graph+vectors) via snapshot→fcheap→restore, on CLI + auto-hook + MCP.

### Feature B — background daemon (incremental sync + Ollama throttle)
> One process owns the writable handle, watches the FS, serves all CLI/MCP/studio clients over a unix
> socket (fixes the multi-process veclite lock), and throttles Ollama so background re-embeds don't
> saturate it or starve interactive search. (codemap has NO watcher today and embeds inline per-file with
> no dedup; vecgrep already has a dormant `internal/index/watcher.go`.)
- [x] **BD.8** `internal/index/watcher.go` — a `Watcher` over **fsnotify** (added v1.10.1; codemap's first FS
  watch) with debounce/coalesce. `NewWatcher(root, WatchConfig{Debounce~500ms, Excluded}, onChange)` adds
  watches for the whole tree (fsnotify is non-recursive; skips excluded/dot dirs), filters events to
  recognized source (`extract.LanguageForPath != ""` + not excluded), coalesces a burst into
  create/write→toIndex + remove/rename→toRemove (rel paths), and fires `onChange(toIndex, toRemove)` on the
  quiet-tick. Handles new-dir creation (watch + queue moved-in files). `Run(ctx)`/`Close`. `TestWatcher`
  (create/modify/delete + excluded-dir-ignored; 3× no flake; race-clean); task check green. (Daemon wiring = BD.11.)
- [x] **BD.9** `Indexer.IndexFiles(ctx, projectID, name, root, rels, opts)` — the watcher's incremental
  reindex target: runs the existing `indexFile` over each named source file (hash-skip + re-extract + embed),
  prunes paths gone from disk (`DeleteNodesInFile`+`DeleteFileHash`+`vectors.DeleteByFile`, `FilesDeleted++`),
  then `resolveEdges` over the changed refs + `vectors.Sync`. `TestIndexFilesIncremental` (add file → symbol +
  edge resolve; delete file → pruned); task check green. (Inbound name-based edges from unchanged files into a
  changed file refresh on a full reindex — daemon can reconcile periodically.)
- [x] **BD.10** `internal/embed/throttle.go` — `ThrottledProvider` (embed.Provider decorator, the
  Ollama-sparing core): content-hash **dedup** (cache + `singleflight` for concurrent identical texts → a text
  embeds once across files/branches), token-bucket **rate limit** (`x/time/rate`) + **max-in-flight** semaphore
  on inner calls, and **two lanes** — `QueryEmbed` (interactive) skips the background rate limit so a reindex
  storm never stalls a search, while `Embed` (index) is throttled. `NewThrottled(inner, ThrottleConfig{RPS,
  Burst, MaxInFlight, CacheSize})`. New deps `x/time/rate`, `x/sync`. `TestThrottledDedup` +
  `TestThrottledMaxInFlight` (race-clean). (Daemon wraps Ollama in this; wiring = BD.11/13.)
- [x] **BD.11** `internal/daemon/daemon.go` — `Daemon.Start(ctx, root, cfg)`: opens the sole write Session
  (wrapping the embedder in `embed.NewThrottled` unless structure-only), one-time index, builds the Indexer +
  `index.NewWatcher` whose `onChange` calls `IndexFiles` (watch→debounce→incremental sync), binds
  `config.DaemonSocketPath()` (socket-dial liveness check refuses a 2nd daemon + clears a stale socket — no
  flock, so it cross-compiles for Windows), writes `daemon.json`, serves newline-JSON **control** RPCs
  (`daemon.status`/`reindex`/`shutdown`) + idle-timeout. `Stop`/`Wait`; teardown removes sock+json. New
  `config.DaemonSocketPath`/`DaemonStatePath`, `Indexer.Excluded`, throttle `Available` passthrough.
  `TestDaemonIndexesOnChange` (initial index + watch a new file → indexed + status + clean stop; 3× + race);
  task check green. (Full MCP-over-socket serving = BD.12.)
- [x] **BD.12** `cmd/codemap/daemon.go` — `codemap daemon start [path] / stop / status`. `start` runs the
  daemon in the foreground (background it with `&`; clean shutdown on Ctrl-C/SIGTERM via `d.Stop`); `stop`/
  `status` dial the control socket. Registered the `daemon` command group. **Dogfood-verified end-to-end**:
  started in the background, `status` → running, added a `.go` file → daemon auto-indexed it (`find` saw the new
  symbol live), `stop` → cleanly down. task check green. (Optional follow-up: a `serve` stdio↔socket MCP bridge;
  the daemon being the sole writer already lets clients query read-only without lock contention.)
- [x] **BD.13** `internal/config/config.go` — `DaemonConfig{DebounceMS, IdleTimeoutMin, EmbedRPS, EmbedMaxInFlight, EmbedCacheSize}` with built-in defaults + `CODEMAP_DAEMON_*` env overrides, sourced by `daemon start` (replaces the hardcoded 500ms placeholder). **Daemon epic (BD.8–13) complete.** (Deferred: `Autostart` + surfacing daemon state in `codemap_status`.)

**Follow-on config work (same session, user-requested):**
- [x] **Configurable exclude paths** — `index.exclude_extra` (config, APPENDED to defaults so adding a folder doesn't clobber `node_modules`/`vendor`; the yaml slice-merge replaces, which was the footgun) + `CODEMAP_EXCLUDE_EXTRA` env. Exclude globs are now **path-aware**: bare name = any segment/depth (`migrations`), slash = root-anchored prefix (`db/migrations`), `**/` = any depth (`**/testdata`). `matchExclude` threaded through the full walk, incremental reindex, and the watcher (now passes project-relative paths). Tests: `exclude_test.go`. (commit 390883e)
- [x] **Per-setting override flags** — every config-file/env knob now also has a CLI flag (precedence: file < env < **flag**). Embedding flags persistent (`--embed-provider/-model/--ollama-url/--embed-dimensions/-distance`); index flags on `index` (`--exclude`, `--exclude-extra`, `--max-file-bytes`); daemon flags on `daemon start` (`--debounce`, `--idle-timeout`, `--embed-rps`, `--embed-max-in-flight`, `--embed-cache-size`). `applyConfigFlags` overlays only `.Changed` flags; daemon gets an `Overrides` hook so flags reach its own indexer/embedder. (commit 6d6a054)

**Daemon observability (shipped in v0.13.0):**
- [x] **Daemon state in `status` / `codemap_status`** (deferred BD.13 tail) — `daemon.QueryStatus` dials the control socket; `StatusWithDaemon` (in the daemon pkg to avoid an app→daemon cycle) flattens into `--json` under `"daemon"`; human surface prints a `daemon: running — <project> (pid N, watching)` / `not running` line. Added `daemon start --no-embed` (structure-only daemon; fixes the initial-embed delaying socket bind + makes it testable). Test: `TestQueryStatus`. (commit 3a7fe8a)
- [x] **Studio daemon indicator** — green `● daemon <branch>` chip in the studio header while a daemon is watching (cheap periodic poll, re-armed each tick; live as it starts/stops). Test: `TestHeaderShowsDaemon`. (commit aa68875)
- [x] **`doctor` daemon-health check** — `codemap doctor` lists the daemon (running / hint to start). Socket-connect probe (no app→daemon cycle, doesn't reset idle). (commit 6ecfaef)
- [x] **Docs** — CLI "Background daemon" section + status/doctor/studio/MCP daemon notes. (commit ca8b9f5)
- [x] **glyphrun E2E flows** — `specs/exclude_extra.yml` (append + anchored slash + `**/` globs) and `specs/daemon.yml` (daemon lifecycle: start → socket running → status surfaces pid → stop → not running; deterministic only — auto-reindex stays in the unit test, fsnotify new-file detection flakes on macOS/kqueue at the E2E level). Both pure-Go, local-only. (commit 533e376)
- [ ] **DEFERRED — `serve` stdio↔socket MCP bridge** (BD.12 optional follow-up). Would let `codemap serve` forward `mcp.*` to a running daemon so multiple clients share ONE warm in-memory index. **Judged low-ROI:** lazy-open DB + SQLite WAL concurrent readers already make multi-client work (CLAUDE.md TD5); the warm-index win doesn't justify the daemon serving the full mcp.* surface + a bidirectional stdio↔socket proxy + lifecycle. Revisit only on a demonstrated need.

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
