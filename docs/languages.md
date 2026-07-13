# Language support plan

codemap reports language support by **relation domain**, not with one vague
"supported" badge. Finding a function and proving who calls it are different
capabilities, and the JSON contracts keep that distinction visible.

## Support ladder

| Tier | What codemap can claim | Admission gate |
|---|---|---|
| **T0 · recognized** | Detect the language and report why files were skipped | Extension/filename fixtures; no graph claims |
| **T1 · symbols** | Files, functions, methods, types and `defines` edges | Stable ranges/FQNs on representative fixtures |
| **T2 · navigation** | Cross-file definitions, references/imports and, where available, implementation relationships | Project fixtures with confirmed/candidate provenance and explicit domain coverage |
| **T3 · calls** | Resolved caller/callee edges for files the backend actually analyzed | Per-file coverage persisted; empty results must distinguish “none” from “unavailable” |
| **T4 · release quality** | The language is documented as generally supported | Accuracy, incremental, failure, performance and mixed-language gates all pass |

A language can be T3 for calls while a separate relation remains partial. Query
responses continue to carry `call_graph`, reference coverage and dependency
confidence; one successful file never upgrades the whole project.

## Backend strategy

codemap keeps one normalized graph and admits evidence through three ports:

1. **Native parser** — cheap, offline structure and conservative name-based
   edges. Go remains the reference implementation.
2. **LSP** — a subprocess discovered on `PATH`, initialized once per project and
   shared by all language bindings it serves. `documentSymbol` supplies T1;
   advertised capabilities such as `callHierarchy` can supply T3 under
   `--precise`. Missing or failing servers degrade visibly instead of making the
   index fail wholesale.
3. **SCIP import** — a project-level import of an existing `index.scip`. SCIP is
   well suited to definitions, references and implementation relationships. It
   must not be relabeled as a call graph unless the producer supplies actual
   call-role evidence; otherwise calls remain `unresolved` and LSP/native
   backends own that domain.

Backends never share codemap's SQLite database. They return normalized records
with provenance, and the app/index layer owns validation, replacement and
coverage publication.

## Delivery waves

### Wave 1 — Rust pilot

Rust is the first T0 → T4 candidate. The pilot uses the official
[rust-analyzer](https://rust-analyzer.github.io/book/) binary and admits only
the LSP capabilities that the running version advertises.

- Register `rust-analyzer` as an optional LSP backend for `.rs` files.
- Admit T1 only after Cargo workspace, module, trait/impl, generic, macro-adjacent
  and test fixtures produce stable symbols and source ranges.
- Admit T3 only when the running server advertises `callHierarchy` and each
  fixture's exact cross-module calls pass. Files whose analysis fails stay
  `unresolved`.
- Add `codemap doctor` detection/install guidance, missing-server behavior,
  incremental reindex and mixed Go/Rust project tests.
- Compare LSP navigation with a Rust-produced SCIP index before choosing whether
  SCIP becomes the preferred T2 source for references/implementations.

### Wave 2 — SCIP importer and compiler families

Build `internal/extract/scip` as a project-level, versioned adapter. The
[SCIP protocol](https://github.com/sourcegraph/scip) is language-agnostic, and
its maintained indexer catalog already covers several languages in these waves;
codemap still validates every imported relation against its own gates.

- validate metadata, project root and relative paths before mutating the graph;
- stream documents/occurrences so large indexes are bounded;
- map stable symbols to codemap selectors and tag every edge with producer,
  version and provenance;
- publish completeness separately for definitions, references, imports and
  implementations;
- replace one producer generation atomically, with a rebuild path for contract
  changes;
- snapshot-test the adapter with the SCIP CLI and reject path traversal,
  malformed ranges and mixed-project input.

Once the importer is trustworthy, use it to accelerate Java/Kotlin/Scala,
C/C++/CUDA and C#/Visual Basic. Each language still advances independently;
the existence of an upstream indexer is not itself a codemap support claim.

### Wave 3 — additional semantic backends

Evaluate Ruby, PHP, Dart, Swift and Elixir using the same ladder. Prefer an
existing precise SCIP producer for T2 and an LSP with advertised call hierarchy
for T3. Do not maintain language-specific forks of the graph/query layer.

### Wave 4 — containers and long tail

Svelte, Astro, Razor, shell, Terraform/HCL, SQL, YAML and HTML/CSS usually need
container-aware extraction or parser structure more than compiler call graphs.
Ship useful T1/T2 support with honest `unavailable` call coverage rather than
manufacturing name-based calls.

Optional tree-sitter support remains build-tagged because it requires a
different release/toolchain story. It is a structure fallback, not a source of
compiler-precise relations.

## Required gates for every language

Before changing public docs from T0, a language needs:

- golden fixtures for symbols, nested ownership, imports/references and calls
  that the backend claims;
- a missing-tool and a malformed/incomplete-response test;
- incremental add/change/delete tests with stale coverage invalidation;
- mixed-language and same-name ambiguity tests;
- per-file coverage/provenance assertions, including successful leaf files with
  zero edges;
- bounded-time and cancellation tests for every external request;
- `doctor`, CLI JSON, MCP and studio status that agree on availability;
- an accuracy corpus and regression threshold before T4.

Semantic retrieval remains language-agnostic: once a definition has safe source
content, vecgrep/local veclite can search it regardless of which structural
backend produced the node.
