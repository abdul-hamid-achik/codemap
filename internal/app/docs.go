package app

import "strings"

// This file holds the agent-facing guide to codemap, addressable by topic so a
// person or an LLM harness can learn the tool quickly via `codemap docs [topic]`
// (CLI) or the `codemap_docs` MCP tool. It is intentionally concise and
// action-oriented — "which command/tool for what" — rather than prose.

type docTopic struct{ name, body string }

var docTopics = []docTopic{
	{"overview", `codemap is local-first code intelligence: a structural code graph (who calls
what, types, tests, imports) fused with semantic vector search, queryable
offline. It precomputes structure once, then answers narrow questions in a few
calls instead of many file reads — for both people (CLI + the studio TUI) and
agents (a stdio MCP server).

Indexes Go (stdlib go/parser, full call graph), TypeScript + JavaScript (one
typescript-language-server, across the .ts<->.js boundary), and Python (pyright-
langserver) when the servers are installed: symbols + structure always, plus a
precise call graph under 'index --precise' so callers/impact/hotspots/path work
for them. Other languages are recognized and reported as skipped (more in
progress); --no-lsp disables the LSP backend. Semantic search is language-agnostic.

Data lives under XDG paths (or ~/.codemap): the graph DB, the veclite vector
store, and the project registry — so other tools can inspect the same store.`},

	{"workflow", `Index once, then query. The typical agent loop for understanding or fixing code:

  1. codemap index             # build the graph (+ embeddings if Ollama is up)
  2. where to start            # codemap_read_order  (entrypoints + hubs ranked — orient on a new repo)
  3. find the entry point      # codemap_semantic "<intent>" OR codemap_find <name>
  4. orient on a symbol        # codemap_context <sym>  (def + callers + callees + tests, ONE call)
                               # codemap_context_batch <s1> <s2> …  (several symbols at once + shared callers)
  5. go deeper                 # codemap_impact (blast radius) · codemap_source (full body)
                               # codemap_callers / codemap_callees (add precise:true on Go)
                               # codemap_references (callback/RunE/registration value wiring; not callers)
  6. before a risky change     # codemap_risk <sym>  (how careful?)
                               # codemap_dependencies <file>  (evidence only) · codemap_file_impact <file>  (evidence + blast/tests)
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
for the full lists. When a short name has several definitions, project a result's
existing {file,start_line,fqn,kind} fields into the "selector" input accepted by
source/context/callers/callees/references/impact/risk (and paired selectors for path). This
keeps every follow-up on one definition without persisting volatile database ids;
file+fqn+kind still resolves after declaration lines shift. When impact/context/
callers/callees/risk/source ARE ambiguous, their response already includes
candidates:[{selector,signature,file,start_line}] — the exact merged set — so
you don't need a separate find/symbols round-trip to build that selector.`},

	{"commands", `CLI commands (all query commands accept --json):
  init / index / status / projects   register, index (--reindex, --no-embed, --precise), stats, projects
  doctor                             check toolchains, language servers, embeddings (with install hints)
  callers / callees [--precise|--at] who calls X / what X calls (exact definition with --at file:line)
  references <sym> [--at file:line]  enclosing scopes that use X as a callback/value (not callers)
  impact <sym> [--depth N|--at]       definition, callers, transitive blast radius, covering tests
  review [--since R] [--staged]      diff-scoped: changed/deleted symbols, blast radius, tests to run, risk band
  read-order [query] [--top N]       where to start reading: entrypoints + load-bearing hubs, ranked
  dependencies <file>                bounded inbound evidence + confirmed/candidate totals + domain coverage
  file-impact <file>                 file impact: confidence-aware evidence + coverage + conservative delete verdict
  risk <sym> [--at file:line]        change-risk: unknown when graph coverage is missing; otherwise low/medium/high
  context <sym> [<sym>...] [--at]    one-call bundle; pass several symbols for a batch + shared callers
  path <from> <to>                   shortest call path between two symbols
  symbols <file>                     a file's outline (signatures)
  find <query>                       name search (offline, no embeddings)
  semantic <query>                   meaning-based search (needs an embedded index;
                                     on a structure-only project it returns mode
                                     "none" with a note — use find instead)
  source <sym> [--at file:line]      source for all same-named definitions, or one exact definition
  hotspots / orphans [--top N]       hubs / dead-code candidates
  coverage [--prefix P] [--lang L] [--uncovered] [--files] [--top N]
                                     per-file precise call-graph coverage: rollups by
                                     language/directory + bounded per-file detail
  annotate <sym> | <from> <to>       pin a note/data to a symbol or call path
  annotations [sym] | [from] [to]    list annotations (--rm <id> to remove)
  serve                              run the MCP server (stdio)
  studio                             the interactive TUI

MCP tools mirror these as codemap_<name> (init, index, status, doctor, semantic,
callers, callees, references, impact, file_impact, dependencies, review, secret_impact,
required_keys, risk, hotspots, orphans, coverage, read_order, path, related_files, symbols,
symbol_at, find, source, context, context_batch, projects, docs, annotate,
annotations, unannotate, branch_status, branch_switch, cache_save, cache_restore,
cache_list, cache_drop). MCP text payloads use compact JSON to save response tokens.

codemap_context bundles a symbol's definition+callers+callees+value references+tests in one graph-only call
(no implicit language-server spawn); context_batch budgets aggregate source bodies and both
surfaces report optional component failures in partial_errors;
codemap_review is the post-edit query (diff, including retained deleted definitions → impact + tests to run + one aggregate risk band);
callers/callees accept precise:true; references deliberately does not, because call precision
does not upgrade value-reference edges. Agent-facing source/context/callers/callees/
references/impact/risk accept selector:{file,start_line,fqn,kind}; path accepts from_selector
and to_selector. codemap_context_batch also accepts selectors:[{file,start_line,fqn,kind}]
unioned with symbols (same 25-cap; MCP-only, no CLI batch form yet). codemap_symbol_at accepts
positions:[{file,line}] as a batch alternative to file/line — a pasted multi-frame stack trace
resolves in one call. codemap_docs returns this guide.`},

	{"annotations", `Annotations are the harness's knowledge layer over the graph: pin notes and
external data (DB rows from mongosh/postgres, vidtrace/vecgrep findings, …) to a
symbol or a call path, then read them back alongside structure. They persist
across reindex.

  codemap_annotate {symbol|from+to, source, note, data}   attach
  codemap_annotations {symbol|from+to|none}               read (all / node / path)
  codemap_unannotate {id}                                 remove one (prune/correct the layer)
  CLI: codemap annotate <sym> --source postgres --data '{...}' --note "..."
       codemap annotate <from> <to> --note "entry path to fix"
       codemap annotations [<sym> | <from> <to>]   (--rm <id> to delete)

codemap_annotations returns a "dangling" list of ids whose target no longer
matches an indexed symbol (renamed/removed since) — prune those with
codemap_unannotate, or re-add against the new name.

'source' is a free label (note, mongosh, postgres, vidtrace, …); 'data' is stored
opaquely (often JSON). Annotations on a symbol surface INLINE in EVERY query result
— codemap_impact, codemap_callers/callees (by-name and precise:true), codemap_references, codemap_source,
and codemap_semantic/codemap_find — matched by name or resolved FQN, so once pinned
the knowledge shows up wherever you look at the symbol (and on every studio tab).
Typical use: codemap_impact a symbol, then codemap_annotate it with the DB rows /
repro findings that explain it, so the next step has the full picture in place.`},

	{"accuracy", `The graph is name-based by default: fast, offline, tolerant of broken code. Intra-
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
remain honestly "name" or "unresolved" rather than upgrading the whole project. (The LSP
languages have NO name-based call edges, so --precise is what gives
TS/JS/Python a call graph at all — so without it, impact/callers/callees on a TS/JS/Python symbol
return a "resolution" note saying the call graph is unavailable, NOT a confidently-empty result or
untested:true; the callers/tests are unresolved, not absent.) Every impact/callers/callees/review/
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
external consumers are incomplete; missing evidence therefore never means safe_to_delete.`},

	{"ecosystem", `codemap is one tool in a local, XDG-stored toolchain for analyzing and fixing
code. A harness can chain them:
  - vecgrep   — semantic code search across a repo by meaning.
  - codemap   — how the code is connected: callers, blast radius, paths, source.
  - vidtrace  — analyze a screen recording / repro video into structured findings.
  - fcheap    — save, search, and prune artifacts (findings, notes, intermediate data).
  - noted     — code notes.
Example bug-fix flow: vidtrace a repro → vecgrep the codebase for the relevant
area → codemap_impact / codemap_path to learn what a change would affect and
what tests cover it → plan the fix → fcheap to persist the artifacts. All share
local XDG storage, so findings can be cross-referenced.

codemap ⇄ vecgrep is wired directly (when the vecgrep binary is on PATH): if a
project has no codemap embeddings, codemap_semantic falls back to vecgrep's index
and maps hits onto the graph (mode:"vecgrep"); codemap_context surfaces vecgrep
agent-memories scoped to this project via status's project_key tag; and
codemap_status reports sibling indexes. It degrades silently when vecgrep is
absent. Disable with vecgrep.enabled=false.

codemap ⇄ tinyvault answers secret rotation impact: codemap_secret_impact takes
secret key NAMES (or --via-vault fetches them from tvault, value-free) and reports
which symbols read each key, the rotation blast radius, and covering tests
(untested=true warns of a key no test reaches). Key NAMES only cross the seam —
codemap never reads secret values. codemap index --via-vault <project> runs the
index inside 'tvault run' so language servers see private-registry creds.`},
}

// DocTopicNames returns the available `codemap docs` topics, in order.
func DocTopicNames() []string {
	names := make([]string, len(docTopics))
	for i, t := range docTopics {
		names[i] = t.name
	}
	return names
}

// Docs returns the agent guide. An empty topic returns the full guide; a known
// topic returns just that section; an unknown topic returns the topic list.
func Docs(topic string) string {
	topic = strings.ToLower(strings.TrimSpace(topic))
	if topic == "" {
		var b strings.Builder
		b.WriteString("codemap — agent guide (topics: " + strings.Join(DocTopicNames(), ", ") + ")\n\n")
		for _, t := range docTopics {
			b.WriteString("## " + t.name + "\n\n" + t.body + "\n\n")
		}
		return strings.TrimRight(b.String(), "\n") + "\n"
	}
	for _, t := range docTopics {
		if t.name == topic {
			return t.body + "\n"
		}
	}
	return "unknown docs topic " + topic + " — available: " + strings.Join(DocTopicNames(), ", ") + "\n"
}
