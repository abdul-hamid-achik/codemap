---
name: using-codemap
description: When answering structural code questions (callers, blast radius, tests covering a change, where to start reading), use the codemap_* tools instead of grep/Read; run codemap_review after edits.
---

For structural code questions — who calls X, what breaks if I change Y, where do I
start reading, what tests cover this — reach for codemap before grep or opening
files: if answering would mean reading more than 2 files, call codemap_context (or
codemap_context_batch) first. codemap is a precomputed code graph fused with
semantic search, exposed as codemap_context, codemap_impact and friends over MCP
(and as codemap … --json on the CLI).

Map the loop to the tools:
  - orient on a repo    -> codemap_read_order (entrypoints + hubs, ranked)
  - locate              -> codemap_semantic (by meaning) · codemap_find (by name) · codemap_grep (exact text: a literal, error string, route, env var) · codemap_explore (fuzzy intent → oriented structure)
  - resolve a file:line -> codemap_symbol_at (a stack trace / diff hunk / grep hit → a selector; batch positions:[…] for a whole trace)
  - orient on a file    -> codemap_file_context (symbol outline + file impact + related files in one call)
  - understand          -> codemap_context, codemap_impact, codemap_source
  - before a risky edit -> codemap_risk, codemap_file_impact, codemap_dependencies, codemap_related_files
  - after every edit    -> codemap_review (it names the tests to run and folds one risk band)
  - calibrate trust     -> codemap_coverage (per-file precise call-graph coverage)

Trust the honesty signals on every result: stale (reindex first), call_graph
(resolved/name/unresolved/none), resolution, and the *_total caps. When a name is
ambiguous the response already carries candidates:[…] and accepts a selector, so
pick one definition from those instead of a second lookup. Prefer
codemap_index --precise for exact call edges — it is also the only call graph for
TypeScript, JavaScript and Python.

## The workflow

Index once, then query. The typical agent loop for understanding or fixing code:

  1. codemap index             # build the graph (+ embeddings if Ollama is up)
  2. where to start            # codemap_read_order  (entrypoints + hubs ranked — orient on a new repo)
  3. find the entry point      # codemap_semantic "<intent>" OR codemap_find <name>
                               # OR codemap_grep "<exact text>" (string literal, error message, route, env-var)
                               # codemap_explore "<intent>" (fuzzy goal → bounded context neighborhoods, source-light)
                               # got a file:line? codemap_symbol_at <file>:<line> (batch positions:[…] resolves a whole trace)
  4. orient on a symbol        # codemap_context <sym>  (def + callers + callees + tests + test_commands, ONE call)
                               # codemap_context_batch <s1> <s2> …  (several symbols at once + shared callers)
  5. go deeper                 # codemap_impact (blast radius + runnable test_commands) · codemap_source (full body)
                               # codemap_callers / codemap_callees (add precise:true on Go)
                               # codemap_references (callback/RunE/registration value wiring; not callers)
  6. before a risky change     # codemap_risk <sym>  (how careful?)
                               # codemap_file_context <file>  (ONE call: symbol outline + file impact + related files)
                               # codemap_dependencies <file>  (evidence only) · codemap_file_impact <file>  (evidence + blast/tests)
                               # codemap_related_files <file>  (the other files structurally tied to this one)
  7. trace flow                # codemap_path <from> <to>  (shortest call chain)
  8. AFTER you edit            # codemap_review  (your diff → changed symbols, blast radius, the TESTS TO RUN)
  9. survey                    # codemap_hotspots (hubs) · codemap_orphans (dead code)

Stay fresh: codemap_status returns a "stale" object (files changed/new/deleted
since the last index) — normally codemap_index before trusting snapshot-based
results. Deletion review is the exception: codemap_review uses retained old
definitions when available, emits deletion_analysis, and orders selected tests
before the reindex that prunes them. (A registered-but-never-indexed project
reports indexed:false — codemap_index first.)

Every query takes an optional "path" (project dir; defaults to cwd) and returns
JSON. Results carry each symbol's signature and docstring, so you rarely open
files; use codemap_source when you need the implementation. codemap_context's
callers/callees/tests are capped (see *_total); drill with codemap_callers/impact
for the full lists. If a hub symbol's context/source response feels heavy, pass
brief:true (source/context/context_batch) to drop source bodies (signature+doc
stay) — each dropped definition sets source_omitted:true; re-call codemap_source
without brief for the one body you actually need. When a short name has several definitions, project a result's
existing {file,start_line,fqn,kind} fields into the "selector" input accepted by
source/context/callers/callees/references/impact/risk (and paired selectors for path). This
keeps every follow-up on one definition without persisting volatile database ids;
file+fqn+kind still resolves after declaration lines shift. When impact/context/
callers/callees/risk/source ARE ambiguous, their response already includes
candidates:[{selector,signature,file,start_line}] — the exact merged set — so
you don't need a separate find/symbols round-trip to build that selector.

## Accuracy and honesty signals

The graph is name-based by default: fast, offline, tolerant of broken code. Intra-
package calls resolve precisely (Go), but a cross-package method call like
x.Foo() links to EVERY method named Foo (resolving the receiver's type needs a
type-checker). codemap flags this rather than hiding it:
  - callers/callees/impact note when a name resolves to multiple definitions.
  - hotspots marks name-collision inflation; orphans follows functions wired by value
    (handlers like cobra RunE / mux.HandleFunc) but can't see interface/reflection callers (candidates).
THE GRAPH-WIDE FIX: re-index with 'codemap index --precise' (CLI) or codemap_index
precise:true (MCP) — the unified exact-resolution pass. For Go it's a pure-Go go/types
pass; for the LSP languages (TypeScript, JavaScript, Python) it drives the language
server's callHierarchy. Successful precise coverage is recorded per file; a query is
"resolved" only when every matched definition file completed the pass. Partial failures
remain honestly "name" or "unresolved" rather than upgrading the whole project. (TS/JS have
name-based candidate edges for JSX component usage, imports, and framework wiring at the
base level — but plain function calls still come only from --precise, so impact/callers/callees
on a non-JSX TS/JS/Python symbol return a "resolution" note saying the call graph is
unavailable, NOT a confidently-empty result or untested:true; the callers/tests are
unresolved, not absent. Ruby and Lua carry name-based call edges from their built-in
backends and classify as "name".) Every impact/callers/callees/review/
context/hotspots/orphans/path report also carries a stable machine enum — "call_graph": "resolved|name|unresolved|none" —
so a consumer can switch on confidence (resolved→high, name→medium, unresolved/none→low) instead of
parsing the free-form resolution sentence. The Go pass
needs the go toolchain + a buildable module; packages that don't type-check keep
name-based edges (per-package degrade), and no go/go.mod falls back wholesale with a
"note" — never worse than name-based, never a hard error. Opt-in: without --precise the
index is the fast name-based path. For a one-off exact Go answer without reindexing,
callers/callees also accept precise:true / --precise for one-off language-server resolution. Interface dispatch is statically
undecidable, so a precise edge points at the interface method, not concrete implementors.

Precise indexing makes call EDGES exact; a bare query name can still intentionally
match several exact definitions. Use the source selector {file,start_line,fqn,kind}
from find/symbols/context results — or from a prior ambiguous call's own candidates
field, which is the same merged-set surface — no extra lookup needed — to choose one.
Selectors are stable across reindex and ordinary line shifts when file+FQN+kind remain,
but moves/renames can invalidate them and return found:false rather than silently
selecting a different node. Raw graph node ids are never part of the public contract.

codemap_references is a separate inbound value-wiring query for callbacks, Cobra
RunE handlers, and registrations. It follows only 'references' edges, never
'calls', and returns bounded enclosing function/method/file scopes with true
totals. Go coverage is partial and other language backends do not yet persist
general value references; an empty result is therefore not proof of no wiring.
The stored source location is the enclosing declaration (or file scope), not the
exact expression line. 'index --precise' resolves call edges and does not upgrade
reference confidence; use the report's own coverage/stale/confidence fields.

codemap_coverage exposes the per-file signal behind that enum directly: which files have
a persisted precise-resolution row, its resolver and timestamp, and whether that file has
drifted on disk since (independent of codemap_status's project-wide drift count). Query it
once per package/directory to calibrate trust locally instead of assuming the whole
project's single worst-file call_graph.

File dependency evidence is deliberately broader and more conservative than the call graph:
file-impact groups inbound calls, Go function-value references, and imports by dependent file,
with per-domain complete/partial/unavailable coverage and bounded source→target samples. Every
relationship is confirmed or candidate with a reason. Only fresh confirmed file-scoped evidence
can prove delete_verdict=unsafe; qualified name fan-out, stale snapshots, and package-scoped Go
imports remain unknown for an exact file. General type/value uses, runtime wiring/reflection, and
external consumers are incomplete; missing evidence therefore never means safe_to_delete.
