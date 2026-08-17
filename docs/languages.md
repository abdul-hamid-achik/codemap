---
description: Current codemap language capabilities, required language servers, and the roadmap for adding new backends.
---

# Language support

codemap reports language support by **relation domain**, not with one vague
"supported" badge. Finding a function and proving who calls it are different
capabilities, and the JSON contracts keep that distinction visible.

## Current release

| Language | Symbols and definitions | Call graph | Requirement / limit |
|---|---|---|---|
| **Go** | Built in with the standard-library parser | Name-based by default; exact per-file coverage with `codemap index --precise` via in-process `go/types` | Go toolchain + a buildable module for the precise pass. One-off `callers --precise` / `callees --precise` uses `gopls`. |
| **TypeScript + JavaScript** | `documentSymbol` through one shared `typescript-language-server` process, including TSX/JSX and cross-language projects | Name-based candidate edges by default for JSX component usage (`.tsx`/`.jsx`), imports, and Next.js framework wiring; plain function calls need `--precise` (LSP `callHierarchy`), which supersedes the candidates per file | `node` + `typescript-language-server`. The name-based scan rides on LSP symbol extraction, so it also needs the server. |
| **Python** | `documentSymbol` through `pyright-langserver` | No name-based edges; `--precise` uses LSP `callHierarchy` | `node` + `pyright-langserver`. |
| **Ruby** | Built in with a pure-Go scanner: modules, classes, `def` (incl. `def self.x`, endless defs, `private def`); heredoc-, `=begin`-, and string-safe | Name-based calls plus `require`/`require_relative` imports; no precise pass yet | None — works offline like Go's name-based path. |
| **Lua** | Built in with a pure-Go scanner: `function M.foo()`/`M:foo()`/`local function` and function assignments; long-string- and comment-safe | Name-based calls plus `require` imports; no precise pass yet | None — works offline like Go's name-based path. |
| **GDScript** | Built in with a pure-Go scanner: `class_name`, inner classes, functions, signals, enums, variables, and constants; multiline-string- and comment-safe | Name-based calls plus `preload`/`load` imports; no precise pass yet | None — works offline like Go's name-based path. Godot Engine `.gd` files. |
| **Vue SFC** | `<script>` and `<script setup>` blocks are routed to the TypeScript/JavaScript server; source lines map back to the `.vue` file | Not available yet for calls; Vue emits symbols, `defines` edges, and import edges | `node` + `typescript-language-server`. Template and style blocks are not indexed. |
| **CSS / SCSS / Sass / Less** | Built in with a pure-Go scanner: one selector node per distinct class/id token per file, SCSS/Less nesting flattened via `&`-substitution, transparent at-rule frames (`@media`/`@supports`/`@layer`), interpolation-safe | `styles` edges from `class=`/`id=` in HTML and `className` in TSX/JSX (string literals, `cn()`/`clsx()` expressions, template statics) resolve to selector nodes; `@import`/`@use`/`@forward` become import edges (Sass partials and index files resolved) | None — pure Go, works offline. CSS-in-JS, CSS Modules member access, cascade/specificity, and `<style>` blocks are out of scope for v1. |
| **HTML** | Built in with a pure-Go scanner: `class=`/`id=` attribute references (template placeholders skipped); no symbols are emitted | `styles` reference edges into CSS selector nodes | None — pure Go, works offline. |

Install the optional language servers you need:

```bash
npm install --global typescript typescript-language-server
npm install --global pyright
go install golang.org/x/tools/gopls@latest
```

Run `codemap doctor` to see which servers are available. Missing LSP-language servers are
reported with install guidance; `--no-lsp` deliberately skips those backends. Semantic retrieval
is language-agnostic once source-bearing symbols are indexed, and Ollama remains optional.

### TS/JS name-based edges — what they cover and what they don't

The base (non-`--precise`) TS/JS graph carries three kinds of name-based evidence:

- **JSX component usage** (`.tsx`/`.jsx` only) — `<Foo/>` creates a candidate call edge from the
  enclosing component to `Foo`; member expressions (`<Foo.Bar/>`, `<motion.div/>`) resolve to the
  root binding. Lowercase intrinsics (`<div>`) never create edges; generics and comparisons are
  excluded, and comments and string/template-literal contents are sanitized first — commented-out
  JSX creates nothing.
- **Imports** — `import`/`export … from`/`require()`/dynamic `import()` become file→file edges,
  comment-safe. The resolver understands relative specifiers, `@/` and `~/` tsconfig-style
  aliases, and monorepo workspace packages via their `package.json` names
  (`exports`/`module`/`main`), with deterministic resolution when two directories declare the
  same name.
- **Next.js framework wiring** — App Router special files (`page`/`layout`/`route` HTTP verbs/
  `error`/metadata routes/…), `middleware`, and Pages Router modules get a reference from the
  file to their framework-invoked exports, so those symbols stop appearing as orphans.

These are *candidate* edges (weight 0.7 — the same over-match contract as Go's name-based
selector calls); a `--precise` pass supersedes them per file with exact `callHierarchy` edges.
Honest limits: a component passed only as a **prop** (`<Nav Link={AuthLink}/>`) is never
JSX-rendered by name and can still appear as an orphan; a **wrapped default export**
(`export default memo(Page)`) isn't framework-wired because the invoked identifier isn't
name-resolvable; and plain function calls (`foo()`) still have no name-based TS/JS edges at all —
`--precise` remains the only source for those.

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
3. **Planned SCIP import** — a future project-level import of an existing `index.scip`. SCIP is
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

Ruby and Lua shipped ahead of this wave as pure-Go name-based backends (symbols,
name-based calls, `require` imports — the same tier as Go's default path, without
a precise pass). Evaluate PHP, Dart, Swift and Elixir using the same ladder — and
a future *precise* tier for Ruby. Prefer an
existing precise SCIP producer for T2 and an LSP with advertised call hierarchy
for T3. Do not maintain language-specific forks of the graph/query layer.

### Wave 4 — containers and long tail

HTML/CSS/Sass shipped in v0.49.0 as pure-Go backends (selector nodes + `styles`
edges from markup and `className`). Svelte, Astro, Razor, shell, Terraform/HCL, SQL and YAML usually need
container-aware extraction or parser structure more than compiler call graphs.
Ship useful T1/T2 support with honest `unavailable` call coverage rather than
manufacturing name-based calls.

Tree-sitter is a planned optional structure fallback, not part of current builds. If shipped, it
will require a separate release/toolchain story and will not be presented as a source of
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
