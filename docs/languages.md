---
description: Current codemap language capabilities, the optional language servers behind --precise, and the roadmap for adding new backends.
---

# Language support

codemap reports language support by **relation domain**, not with one vague
"supported" badge. Finding a function and proving who calls it are different
capabilities, and the JSON contracts keep that distinction visible.

## Current release

| Language | Symbols and definitions | Relationships | Requirement / limit |
|---|---|---|---|
| **Go** | Built in with the standard-library parser | Name-based by default; exact per-file coverage with `codemap index --precise` via in-process `go/types` | Go toolchain + buildable modules for the precise pass (a repo without a root `go.mod` loads each module under it). One-off `callers --precise` / `callees --precise` uses `gopls`. |
| **TypeScript + JavaScript** | Built in with a pure-Go tree-sitter parser (TS, TSX, JS/JSX, `.mjs`/`.cjs`), producing the same symbols `typescript-language-server`'s `documentSymbol` would | Name-based candidate edges by default for JSX component usage (`.tsx`/`.jsx`), imports, Next.js framework wiring, same-file calls, and calls through imported bindings; arbitrary `obj.method()` calls are not linked, so the graph is partial until `--precise` (LSP `callHierarchy`), which supersedes the candidates per file | None to index. `--precise` needs `node` + `typescript-language-server`. |
| **Python** | Built in with a pure-Go tree-sitter parser, producing the same symbols `pyright-langserver` would | Name-based candidates for same-file calls, `self`/`cls` methods, and imported bindings, plus file→file import edges; `--precise` (LSP `callHierarchy`) supersedes them per file | None to index. `--precise` needs `node` + `pyright-langserver`. |
| **Ruby** | Built in with a pure-Go scanner: modules, classes, `def` (incl. `def self.x`, endless defs, `private def`); heredoc-, `=begin`-, and string-safe | Name-based calls plus `require`/`require_relative` imports; no precise pass yet | None — works offline like Go's name-based path. |
| **Lua** | Built in with a pure-Go scanner: `function M.foo()`/`M:foo()`/`local function` and function assignments; long-string- and comment-safe | Name-based calls plus `require` imports; no precise pass yet | None — works offline like Go's name-based path. |
| **GDScript** | Built in with a pure-Go scanner: `class_name`, inner classes, functions, signals, enums, variables, and constants; comment-safe | Name-based calls plus `preload`/`load` imports; no precise pass yet | None — works offline like Go's name-based path. Godot Engine `.gd` files. |
| **Vue SFC** | `<script>` and `<script setup>` blocks are parsed by the TypeScript/JavaScript backend; source lines map back to the `.vue` file | Not available yet for calls; Vue emits symbols, `defines` edges, and import edges | None. Template and style blocks are not indexed. |
| **CSS / SCSS / Sass / Less** | Built in with a pure-Go scanner: one selector node per distinct class/id token per file, SCSS/Less nesting flattened via `&`-substitution, transparent at-rule frames (`@media`/`@supports`/`@layer`), interpolation-safe | `styles` edges from `class=`/`id=` in HTML and `className` in TSX/JSX (string literals, `cn()`/`clsx()` expressions, template statics) resolve to selector nodes; `@import`/`@use`/`@forward` become import edges (Sass partials and index files resolved) | None — pure Go, works offline. CSS-in-JS, CSS Modules member access, and cascade/specificity remain outside coverage. |
| **HTML** | Static class/id references and selectors in embedded `<style>` blocks | `styles` references plus local stylesheet/script import edges | Offline. Template expressions, external URLs, and script bodies are not analyzed as HTML. |
| **SQL / sqlc** | Tables, views, named queries, and anonymous statements | Candidate `reads`/`writes` edges; configured sqlc Go methods link to queries with `depends_on` | Offline lexical extraction. No dynamic SQL, live schema, or column lineage. |
| **YAML** | Mapping keys with escaped, stable key paths | Explicit Task, Compose, and GitHub Actions dependencies | Offline. Aliases and templates are not evaluated. |
| **Markdown** | Headings and sections; one document node for heading-free files | Local `documents` links into indexed files and Markdown headings | Offline CommonMark. Fenced examples stay documentation; no MDX execution or external fetching. |

Language servers are optional: install them for the exact `--precise` call graph (and
`gopls` for one-off Go `callers`/`callees --precise`):

```bash
npm install --global typescript typescript-language-server
npm install --global pyright
go install golang.org/x/tools/gopls@latest
```

Run `codemap doctor` to see which servers are available. A server missing when `--precise` needs
it is reported with install guidance (`capability: "precise"` in `tooling.issues`); the
language's symbols are indexed either way. `--no-lsp` never spawns a server. Semantic retrieval
is language-agnostic once source-bearing symbols are indexed, and Ollama remains optional.

On large monorepos `typescript-language-server`'s tsserver can run out of memory — typically when
a root `tsconfig.json` with no `include` puts every file in one project. codemap raises tsserver's
heap ceiling to 8 GB (`maxTsServerMemory`; a ceiling, not a reservation), and if the backend still
dies it restarts the server (up to 8 times per run), retries the file it was answering, and notes
the restart in the `--precise` summary. Long runs of identical per-file errors are grouped by
cause in the CLI output; `--json` keeps the full list.

A language server answers one call-hierarchy request at a time. `index.precise_servers` (or
`CODEMAP_PRECISE_SERVERS`) lets the `--precise` pass run several server processes in parallel; each
takes whole projects — files grouped by their nearest `tsconfig.json`, `jsconfig.json`,
`package.json`, `pyproject.toml`, or `pyrightconfig.json` — from a shared queue. It is off by default:
on a large single project two or three processes cut the pass by roughly a quarter, but on a monorepo
of many projects that share dependencies each process reloads them, and the gain disappears.

### How TypeScript, JavaScript, and Python are parsed

The built-in backend parses with [gotreesitter](https://github.com/odvcencio/gotreesitter), a
pure-Go tree-sitter runtime (no CGO), and reproduces the symbol trees the language servers
return — TypeScript's navigation tree and pyright's symbol scopes, quirks included (an arrow
function assigned to a `const` is a variable; `self.x = …` in a method declares a class
member). Indexing therefore needs no server and is several times faster on TypeScript-heavy
repositories (about 8× end to end on a 1,100-file one), and graphs built either way are
interchangeable: `--precise` joins the server's
`callHierarchy` answers to the same symbol positions. Files over 1 MB (usually minified bundles)
are skipped, and a file with syntax errors keeps the symbols of the recovered parse.

To use the servers for symbols as well (the behavior before this backend), set
`index.structural_backend: lsp` (or `CODEMAP_STRUCTURAL_BACKEND=lsp`).

### Declared inheritance

Classes and interfaces carry their declared bases as graph edges: `extends` (`class A extends B`,
`interface I extends J`, Python `class A(B)`) and `implements` (`class A implements I`), resolved
through the same-file and import bindings the call candidates use — so a base from a package
(`React.Component`) yields no edge. Each method of the subtype that shares a name with a method of
a direct base or implemented interface gets an `overrides` edge (constructors excluded). For Go,
`codemap index --precise` adds exact `implements` edges from `go/types` satisfiability (every module
type against every module interface with at least one method) and the matching method-level
`overrides`. `codemap context` reports them under `hierarchy` (`extends`, `implements`, `subtypes`,
`overrides`, `overridden_by`), `traverse --edge-types extends,implements,overrides` walks them, and
`orphans` no longer lists a method that overrides or implements a base method (it is reached through
the base). The declared edges are name-provenance candidates; only the Go `--precise` ones are exact.

### TS/JS name-based edges — what they cover and what they don't

The base (non-`--precise`) TS/JS graph carries four kinds of name-based evidence:

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
- **Calls** — high-precision candidate call edges for same-file calls (`f()`, `new C()`, `await f()`,
  `this.m()`) and for calls through imported bindings: named, default, and namespace imports and
  `require`, resolved through relative specifiers, `@/`/`~/` aliases, and workspace packages. A call
  to an unresolvable receiver, such as `obj.method()` on an arbitrary object, is not linked.

These are *candidate* edges (the same over-match contract as Go's name-based
selector calls); a `--precise` pass supersedes them per file with exact `callHierarchy` edges.
Honest limits: a component passed only as a **prop** (`<Nav Link={AuthLink}/>`) is never
JSX-rendered by name and can still appear as an orphan; a **wrapped default export**
(`export default memo(Page)`) isn't framework-wired because the invoked identifier isn't
name-resolvable; and calls that are neither same-file nor through an imported binding
(`obj.method()`, dynamic dispatch, callbacks resolved at runtime) have no name-based edges, so
`call_graph` stays `unresolved` (a partial graph) for uncovered TS/JS and `--precise` remains the
only source for complete calls. Python's base graph carries the analogous candidates — calls to
same-file definitions (scope-aware: parameters, locals, lambda parameters and comprehension
targets shadow), `self.m()` / `cls.m()` to the enclosing class, `C.m()` on a same-file class,
and calls through `from m import f` / `import pkg.mod as m` bindings (relative, absolute, and
`src/`-layout imports) — plus file→file import edges.

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

HTML/CSS/Sass, SQL/sqlc, YAML, and Markdown have offline structural backends.
See [Data, configuration, and documentation](/data-and-docs) for examples and
limits. SQL, YAML, and Markdown report `call_graph: none`; use their typed
dependencies instead.

Svelte, Astro, Razor, shell, and Terraform/HCL usually need
container-aware extraction or parser structure more than compiler call graphs.
Ship useful T1/T2 support with honest `unavailable` call coverage rather than
manufacturing name-based calls.

Tree-sitter ships as a pure-Go runtime (no CGO) and already backs TypeScript, JavaScript, and
Python symbols. It is the natural structure source for the languages above too, but each needs
an authoritative reference to match (a compiler or language server) and the gates below; it is
never presented as a source of compiler-precise relations.

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
- `doctor`, CLI JSON and MCP status that agree on availability;
- an accuracy corpus and regression threshold before T4.

Semantic retrieval remains language-agnostic: once a definition has safe source
content, vecgrep/local veclite can search it regardless of which structural
backend produced the node.
