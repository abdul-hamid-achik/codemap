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

Indexes Go (stdlib go/parser, full call graph) and TypeScript (via
typescript-language-server when installed: symbols + structure always, plus a
precise call graph under 'index --precise' so callers/impact/hotspots/path work
for TS). Other languages are recognized and reported as skipped (more in
progress); --no-lsp disables the LSP backend. Semantic search is language-agnostic.

Data lives under XDG paths (or ~/.codemap): the graph DB, the veclite vector
store, and the project registry — so other tools can inspect the same store.`},

	{"workflow", `Index once, then query. The typical agent loop for understanding or fixing code:

  1. codemap index             # build the graph (+ embeddings if Ollama is up)
  2. find the entry point      # codemap_semantic "<intent>" OR codemap_find <name>
  3. understand it             # codemap_impact <sym>  (callers + blast radius + tests)
                               # codemap_callers / codemap_callees (add precise:true on Go)
  4. read it                   # codemap_source <sym>  (the body, no file open)
  5. trace flow                # codemap_path <from> <to>  (shortest call chain)
  6. survey                    # codemap_hotspots (hubs) · codemap_orphans (dead code)

Every query takes an optional "path" (project dir; defaults to cwd) and returns
JSON. Results carry each symbol's signature and docstring, so you rarely open
files; use codemap_source when you need the implementation.`},

	{"commands", `CLI commands (all query commands accept --json):
  init / index / status / projects   register, index (--reindex, --no-embed, --precise), stats, projects
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
find, callers, callees, impact, path, symbols, source, hotspots, orphans, annotate,
annotations). callers/callees accept precise:true. codemap_docs returns this guide.`},

	{"annotations", `Annotations are the harness's knowledge layer over the graph: pin notes and
external data (DB rows from mongosh/postgres, vidtrace/vecgrep findings, …) to a
symbol or a call path, then read them back alongside structure. They persist
across reindex.

  codemap_annotate {symbol|from+to, source, note, data}   attach
  codemap_annotations {symbol|from+to|none}               read (all / node / path)
  CLI: codemap annotate <sym> --source postgres --data '{...}' --note "..."
       codemap annotate <from> <to> --note "entry path to fix"
       codemap annotations [<sym> | <from> <to>]   (--rm <id> to delete)

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
  - hotspots marks name-collision inflation; orphans can't see interface/reflection callers (candidates).
THE GRAPH-WIDE FIX: re-index with 'codemap index --precise' (CLI) or codemap_index
precise:true (MCP) — the unified exact-resolution pass. For Go it's a pure-Go go/types
pass; for TypeScript it drives typescript-language-server callHierarchy. It resolves
every call to the one it invokes and replaces the name-based call edges, so EVERY query
(callers, callees, impact, hotspots, path) becomes exact at once. (TypeScript has NO
name-based call edges, so --precise is what gives TS a call graph at all.) The Go pass
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
local XDG storage, so findings can be cross-referenced.`},
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
