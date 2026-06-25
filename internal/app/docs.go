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
  2. find the entry point      # codemap_semantic "<intent>" OR codemap_find <name>
  3. orient on a symbol        # codemap_context <sym>  (def + callers + callees + tests, ONE call)
  4. go deeper                 # codemap_impact (blast radius) · codemap_source (full body)
                               # codemap_callers / codemap_callees (add precise:true on Go)
  5. trace flow                # codemap_path <from> <to>  (shortest call chain)
  6. survey                    # codemap_hotspots (hubs) · codemap_orphans (dead code)

Stay fresh: codemap_status returns a "stale" object (files changed/new/deleted
since the last index) — if any are non-zero, codemap_index before trusting query
results, which come from the indexed snapshot, not live files. (A registered-but-
never-indexed project reports indexed:false — codemap_index first.)

Every query takes an optional "path" (project dir; defaults to cwd) and returns
JSON. Results carry each symbol's signature and docstring, so you rarely open
files; use codemap_source when you need the implementation. codemap_context's
callers/callees/tests are capped (see *_total); drill with codemap_callers/impact
for the full lists.`},

	{"commands", `CLI commands (all query commands accept --json):
  init / index / status / projects   register, index (--reindex, --no-embed, --precise), stats, projects
  doctor                             check toolchains, language servers, embeddings (with install hints)
  callers / callees [--lsp]          who calls X / what X calls (--lsp = exact gopls resolution, Go)
  impact <sym> [--depth N]           definition, callers, transitive blast radius, covering tests
  path <from> <to>                   shortest call path between two symbols
  symbols <file>                     a file's outline (signatures)
  find <query>                       name search (offline, no embeddings)
  semantic <query>                   meaning-based search (needs an embedded index;
                                     on a structure-only project it returns mode
                                     "none" with a note — use find instead)
  source <sym>                       a symbol's source code
  hotspots / orphans [--top N]       hubs / dead-code candidates
  annotate <sym> | <from> <to>       pin a note/data to a symbol or call path
  annotations [sym] | [from] [to]    list annotations (--rm <id> to remove)
  serve                              run the MCP server (stdio)
  studio                             the interactive TUI

MCP tools mirror these as codemap_<name> (init, index, status, projects, semantic,
find, callers, callees, impact, path, symbols, source, context, hotspots, orphans,
annotate, annotations). codemap_context bundles a symbol's definition+callers+
callees+tests in one call. callers/callees accept precise:true. codemap_docs returns
this guide.`},

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
— codemap_impact, codemap_callers/callees (by-name and precise:true), codemap_source,
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
server's callHierarchy. It resolves every call to the one it invokes and replaces the
name-based call edges, so EVERY query (callers, callees, impact, hotspots, path) becomes
exact at once. (The LSP languages have NO name-based call edges, so --precise is what gives
TS/JS/Python a call graph at all — so without it, impact/callers/callees on a TS/JS/Python symbol
return a "resolution" note saying the call graph is unavailable, NOT a confidently-empty result or
untested:true; the callers/tests are unresolved, not absent.) The Go pass
needs the go toolchain + a buildable module; packages that don't type-check keep
name-based edges (per-package degrade), and no go/go.mod falls back wholesale with a
"note" — never worse than name-based, never a hard error. Opt-in: without --precise the
index is the fast name-based path. For a one-off exact Go answer without reindexing,
callers/callees also accept precise:true / --lsp (gopls). Interface dispatch is statically
undecidable, so a precise edge points at the interface method, not concrete implementors.`},

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
