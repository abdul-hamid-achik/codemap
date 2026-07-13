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
)

// Reference kinds (string values shared with internal/graph edge types).
const (
	RefCalls      = "calls"
	RefReferences = "references"
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
