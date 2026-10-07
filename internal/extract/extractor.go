// Package extract turns source files into structural symbols and references
// that the indexer stores as graph nodes and edges. Backends — go/parser (pure
// Go), a headless LSP client, and (optionally) tree-sitter — implement
// Extractor. The go/parser and LSP backends are pure-Go so release binaries
// stay CGO_ENABLED=0.
package extract

import "context"

// Symbol kinds (string values shared with internal/graph node kinds).
const (
	KindFile     = "file"
	KindFunction = "function"
	KindMethod   = "method"
	KindType     = "type"
	KindClass    = "class"    // class-based languages (TS/Python); Go has none
	KindModule   = "module"   // namespace/module (TS) or package
	KindVariable = "variable" // top-level var/const that isn't callable
	KindTest     = "test"
	KindSelector = "selector" // CSS class/id selector (.btn, #hero)
	KindTable    = "table"
	KindView     = "view"
	KindQuery    = "query"
	KindKey      = "key"
	KindSection  = "section"
)

// Reference kinds (string values shared with internal/graph edge types).
const (
	RefCalls      = "calls"
	RefReferences = "references"
	// RefStyles links a styled thing (a JSX className, an HTML class attribute)
	// to the CSS selector node that defines the styling. Kept distinct from
	// RefReferences so call-graph consumers (impact, orphans, callers) stay
	// unpolluted by styling relationships.
	RefStyles    = "styles"
	RefReads     = "reads"
	RefWrites    = "writes"
	RefDocuments = "documents"
	// Declared inheritance: a class or interface naming its base (extends) or a
	// class naming an interface it implements. Never part of the call graph.
	RefExtends    = "extends"
	RefImplements = "implements"
)

// Symbol is a code entity discovered in a file.
type Symbol struct {
	Name      string
	FQN       string // fully qualified name, e.g. "pkg.Type.Method"
	Kind      string
	Language  string
	StartLine int
	EndLine   int
	Signature string
	Docstring string
	Source    string // raw source text (for embedding + source_hash)
}

// Reference is a relationship from an enclosing symbol to a named target. The
// target is by name; resolving it to a concrete node (by FQN/symbol match, or
// precisely via LSP) is the indexer's job.
type Reference struct {
	From string // FQN of the enclosing symbol
	To   string // referenced name (e.g. the callee)
	Kind string
	Line int
	// Qualified is true for selector calls (x.Foo(), pkg.Foo()) which may cross
	// packages. False for bare-identifier calls (Foo()), which — in Go — always
	// resolve within the same package, so the indexer can resolve them precisely.
	Qualified bool
	// ToFQN is an explicit project-local target (including file paths). It
	// never falls back to name matching. ToKinds restricts name candidates so
	// SQL tables cannot accidentally resolve to same-named Go functions.
	ToFQN   string
	ToKinds []string

	// FromFile scopes From to one file: From names a symbol FQN that is only
	// unique inside its own file (TS/JS documentSymbol FQNs carry no file
	// prefix, so "App" or "GET" collide across a project). When From equals
	// FromFile the source is the file node itself (module-level code).
	FromFile string
	// FromLine / ToLine, when non-zero, are the declaration start lines of the
	// scoped source / target symbol. They pick the exact definition when the
	// same FQN is declared more than once in a file (test callbacks, overloads,
	// repeated nested names); without them the first declaration is used.
	FromLine, ToLine int
	// ToFile scopes the target to one file: the indexer looks To up as an FQN
	// inside ToFile only and never falls back to project-wide name matching.
	// tsscan sets it for same-file calls; for imported bindings it sets
	// ImportSpec and the indexer fills ToFile from the resolved specifier.
	ToFile string
	// ImportSpec is the module specifier the callee binding was imported from
	// ("./x", "@/lib/y", "@scope/pkg"). The indexer resolves it with the
	// project's import resolver (relative paths, @/ ~/ aliases, workspace
	// packages) into ToFile; a specifier that resolves to no project file
	// (an npm dependency, node:fs) drops the reference.
	ImportSpec string
	// DefaultExport marks an ImportSpec target as the imported module's default
	// export: To is then the member path under it ("" for the export itself,
	// "get" for api.get()), and the indexer prefixes the export's own name.
	DefaultExport bool

	// SourceFile is set by the indexer, never by extractors: the project-relative
	// file this reference was extracted from. It disambiguates an unscoped From
	// FQN declared in several files — every Go `main.main`, two packages both
	// named `data` — which would otherwise collapse onto one node.
	SourceFile string
}

// FileResult is everything extracted from one file.
type FileResult struct {
	Path       string
	Language   string
	Imports    []string
	Symbols    []Symbol
	References []Reference
}

// Extractor extracts structure from a single file's source.
type Extractor interface {
	// Language is the codemap language id this backend handles ("go", ...).
	Language() string
	// ExtractFile parses src (the contents of relPath) into a FileResult.
	ExtractFile(relPath string, src []byte) (*FileResult, error)
}

// CallEdge is one resolved call between declarations, located by root-relative
// file + 1-based line (matching each node's StartLine). FromFQN is retained as
// descriptive evidence, but identity is positional: FQNs from several files may
// legitimately collide in languages whose documentSymbol response omits a
// package/module prefix. External callees (a dependency / lib outside the
// project) have no graph node. Produced by a CallResolver (e.g. the LSP
// backend's callHierarchy).
type CallEdge struct {
	FromFQN  string
	FromFile string
	FromLine int
	ToFile   string
	ToLine   int
	External bool
}

// CallResolver is an optional extractor capability: resolve a file's outgoing
// calls precisely (the LSP backend does this via callHierarchy). The indexer runs
// it under --precise to add exact call edges for languages with no cheap
// name-based call extraction (e.g. TypeScript).
type CallResolver interface {
	CallEdges(ctx context.Context, relPath string) ([]CallEdge, error)
}
