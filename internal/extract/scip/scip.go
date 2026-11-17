package scip

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	pb "github.com/scip-code/scip/bindings/go/scip"
)

// Options controls one import run.
type Options struct {
	// ProjectRoot is the absolute path of the codemap project. The index's
	// metadata project_root (a file:// URI or plain path) must match it, and
	// every document's relative path must resolve inside it.
	ProjectRoot string
	// ChunkSize caps the records delivered to a single emit call (documents
	// and occurrences are streamed in chunks, never as one unbounded slice).
	// Defaults to DefaultChunkSize when zero.
	ChunkSize int
}

// DefaultChunkSize is the per-emit record budget.
const DefaultChunkSize = 1024

// Def is one imported definition (a SCIP symbol with a definition-role
// occurrence) mapped to a codemap symbol node.
type Def struct {
	Symbol    string // full SCIP symbol string — the stable cross-file key
	Name      string // display name
	Kind      string // codemap node kind (see kindMapping)
	File      string // project-relative path, '/'-separated
	StartLine int    // 1-based
	EndLine   int    // 1-based
	Language  string // lowercased document language ("java", …)
}

// Ref is one non-definition occurrence: file/line referencing Symbol. The
// importer attaches it to the enclosing definition in the same file.
type Ref struct {
	File   string
	Line   int
	Symbol string
}

// Impl is an implementation relationship (SCIP SymbolInformation.relationships
// with is_implementation) from one defined symbol to a target symbol.
type Impl struct {
	Symbol string
	Target string
}

// FileImport is a derivable file→file import: FromFile references a symbol
// defined in ToFile.
type FileImport struct {
	FromFile string
	ToFile   string
}

// BatchKind discriminates Batch phases: definitions arrive first (pass 1),
// relation edges second (pass 2), so the consumer can resolve edges against
// the def node ids it recorded during pass 1.
type BatchKind int

const (
	BatchDefs  BatchKind = 1
	BatchEdges BatchKind = 2
)

// Batch is one bounded chunk of imported records.
type Batch struct {
	Kind       BatchKind
	Defs       []Def
	Refs       []Ref
	Implements []Impl
	Imports    []FileImport
}

// Summary reports what the import produced (per-domain completeness).
type Summary struct {
	Producer  string // metadata tool_info.name — the "scip:<tool>" tag stem
	Version   string // metadata tool_info.version
	Documents int
	Defs      int
	Refs      int
	Impls     int
	Imports   int
}

// NormalizeProjectRoot strips an optional file:// scheme (and percent-encoding)
// from an index's metadata project_root, returning a clean absolute path.
func NormalizeProjectRoot(root string) string {
	if root == "" {
		return ""
	}
	if strings.HasPrefix(root, "file://") {
		rest := strings.TrimPrefix(root, "file://")
		if u, err := url.PathUnescape(rest); err == nil {
			root = u
		} else {
			root = rest
		}
		// Drop an optional host component (file:///path keeps an empty one).
		if i := strings.IndexByte(root, '/'); i > 0 {
			root = root[i:]
		}
	}
	return filepath.Clean(root)
}

// validate checks metadata and every document path BEFORE any record is
// emitted, so an invalid index never mutates the graph. Mixed-project input is
// rejected by the combination of a project-root match and per-path traversal
// checks (a document outside the root cannot pass both).
func validate(idx *pb.Index, opts Options) error {
	if idx.Metadata == nil {
		return fmt.Errorf("scip index has no metadata")
	}
	if idx.Metadata.ProjectRoot == "" {
		return fmt.Errorf("scip index metadata has no project_root")
	}
	got := NormalizeProjectRoot(idx.Metadata.ProjectRoot)
	want := filepath.Clean(opts.ProjectRoot)
	if got != want {
		return fmt.Errorf("scip index project_root %q does not match project %q (re-run the indexer against this project)", got, want)
	}
	seen := make(map[string]bool, len(idx.Documents))
	for _, doc := range idx.Documents {
		if doc == nil {
			continue
		}
		rel := doc.RelativePath
		if rel == "" {
			return fmt.Errorf("scip document with empty relative_path")
		}
		if strings.HasPrefix(rel, "/") {
			return fmt.Errorf("scip document path %q is absolute, want relative to the project root", rel)
		}
		if filepath.IsAbs(rel) {
			return fmt.Errorf("scip document path %q is absolute, want relative to the project root", rel)
		}
		clean := filepath.Clean(rel)
		if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || strings.HasPrefix(clean, "../") {
			return fmt.Errorf("scip document path %q escapes the project root", rel)
		}
		if clean == "." {
			return fmt.Errorf("scip document path %q is not a file", rel)
		}
		if seen[rel] {
			return fmt.Errorf("scip index has duplicate document path %q", rel)
		}
		seen[rel] = true
	}
	return nil
}

// Import validates the index and streams its records to emit in two bounded
// passes (definitions, then edges). emit is called once per chunk; nothing is
// emitted unless the whole index passes validation.
func Import(idx *pb.Index, opts Options, emit func(Batch) error) (Summary, error) {
	sum := Summary{}
	if idx.Metadata != nil && idx.Metadata.ToolInfo != nil {
		sum.Producer = idx.Metadata.ToolInfo.Name
		sum.Version = idx.Metadata.ToolInfo.Version
	}
	if sum.Producer == "" {
		sum.Producer = "unknown"
	}
	if err := validate(idx, opts); err != nil {
		return sum, err
	}
	chunk := opts.ChunkSize
	if chunk <= 0 {
		chunk = DefaultChunkSize
	}

	// defFile maps every defined symbol to its defining file — the resolution
	// working set for references and cross-file imports.
	defFile := make(map[string]string)
	sum.Documents = len(idx.Documents)

	// Pass 1: definitions.
	b := Batch{Kind: BatchDefs, Defs: make([]Def, 0, chunk)}
	flush := func() error {
		if len(b.Defs) == 0 {
			return nil
		}
		if err := emit(b); err != nil {
			return err
		}
		b.Defs = b.Defs[:0]
		return nil
	}
	for _, doc := range idx.Documents {
		lang := strings.ToLower(doc.Language)
		kinds, names := symbolTable(doc)
		for _, occ := range doc.Occurrences {
			if occ == nil || !pb.SymbolRole_Definition.Matches(occ) || isLocal(occ.Symbol) {
				continue
			}
			r, ok := occ.SourceRange()
			if !ok || r.Start.Line < 0 || r.End.Line < r.Start.Line {
				return sum, fmt.Errorf("scip occurrence of %s in %s has a malformed range", occ.Symbol, doc.RelativePath)
			}
			if _, dup := defFile[occ.Symbol]; dup {
				continue // one node per symbol; first definition wins
			}
			defFile[occ.Symbol] = doc.RelativePath
			b.Defs = append(b.Defs, Def{
				Symbol:    occ.Symbol,
				Name:      names[occ.Symbol],
				Kind:      kindOf(kinds[occ.Symbol], occ.Symbol),
				File:      doc.RelativePath,
				StartLine: int(r.Start.Line),
				EndLine:   int(r.End.Line),
				Language:  lang,
			})
			sum.Defs++
			if len(b.Defs) >= chunk {
				if err := flush(); err != nil {
					return sum, err
				}
			}
		}
	}
	if err := flush(); err != nil {
		return sum, err
	}

	// Pass 2: relation edges — references (never calls), implementations,
	// and derivable cross-file imports.
	b2 := Batch{Kind: BatchEdges}
	b2.Refs = make([]Ref, 0, chunk)
	seenImport := make(map[FileImport]bool)
	flush2 := func() error {
		if len(b2.Refs) == 0 && len(b2.Implements) == 0 && len(b2.Imports) == 0 {
			return nil
		}
		if err := emit(b2); err != nil {
			return err
		}
		b2.Refs = b2.Refs[:0]
		b2.Implements = b2.Implements[:0]
		b2.Imports = b2.Imports[:0]
		return nil
	}
	maybeFlush := func() error {
		if len(b2.Refs) >= chunk || len(b2.Implements) >= chunk || len(b2.Imports) >= chunk {
			return flush2()
		}
		return nil
	}
	for _, doc := range idx.Documents {
		// Relationships live on the document's symbol table (SCIP has no
		// implementation occurrence role): implements edges are derived here.
		for _, sym := range doc.Symbols {
			for _, rel := range sym.Relationships {
				if rel == nil || !rel.IsImplementation {
					continue
				}
				if _, defined := defFile[sym.Symbol]; !defined {
					continue
				}
				b2.Implements = append(b2.Implements, Impl{Symbol: sym.Symbol, Target: rel.Symbol})
				sum.Impls++
				if err := maybeFlush(); err != nil {
					return sum, err
				}
			}
		}
		// References, attributed to the enclosing definition in this file.
		for _, occ := range doc.Occurrences {
			if occ == nil || pb.SymbolRole_Definition.Matches(occ) || isLocal(occ.Symbol) {
				continue
			}
			r, ok := occ.SourceRange()
			if !ok || r.Start.Line < 0 {
				return sum, fmt.Errorf("scip occurrence of %s in %s has a malformed range", occ.Symbol, doc.RelativePath)
			}
			line := int(r.Start.Line)
			b2.Refs = append(b2.Refs, Ref{File: doc.RelativePath, Line: line, Symbol: occ.Symbol})
			sum.Refs++
			// Derivable import: this file references a symbol defined in
			// another document of the same project.
			if to, defined := defFile[occ.Symbol]; defined && to != doc.RelativePath {
				fi := FileImport{FromFile: doc.RelativePath, ToFile: to}
				if !seenImport[fi] {
					seenImport[fi] = true
					b2.Imports = append(b2.Imports, fi)
					sum.Imports++
				}
			}
			if err := maybeFlush(); err != nil {
				return sum, err
			}
		}
	}
	if err := flush2(); err != nil {
		return sum, err
	}
	return sum, nil
}

func symbolTable(doc *pb.Document) (kinds map[string]pb.SymbolInformation_Kind, names map[string]string) {
	kinds = make(map[string]pb.SymbolInformation_Kind, len(doc.Symbols))
	names = make(map[string]string, len(doc.Symbols))
	for _, s := range doc.Symbols {
		if s == nil || s.Symbol == "" {
			continue
		}
		kinds[s.Symbol] = s.Kind
		if s.DisplayName != "" {
			names[s.Symbol] = s.DisplayName
		} else {
			names[s.Symbol] = lastName(s.Symbol)
		}
	}
	return kinds, names
}

func isLocal(symbol string) bool {
	return strings.HasPrefix(symbol, "local ")
}

// lastName extracts the display name from the symbol's last descriptor.
func lastName(symbol string) string {
	parts := strings.Fields(symbol)
	if len(parts) == 0 {
		return symbol
	}
	last := parts[len(parts)-1]
	// descriptor forms: "Name", "Name()", "Name#method()", "Name#."…
	if i := strings.LastIndexAny(last, "#."); i >= 0 {
		last = last[i+1:]
	}
	return strings.TrimSuffix(last, "()")
}

// kindOf maps a SCIP SymbolInformation kind to a codemap node kind. The
// descriptor suffix is the fallback when the indexer left the kind
// unspecified. The table:
//
//	scip Class/SingletonClass/Mixin/Extension/Concept/Contract → class
//	scip Interface/Trait/Protocol/TypeClass                     → interface
//	scip Struct/Enum                                            → struct
//	scip Method family (incl. getter/setter/constructor)        → method
//	scip Function/Macro/Predicate/Tactic/Theorem                → function
//	scip Type/TypeAlias/AssociatedType/TypeParameter            → type
//	scip Namespace/Module/Package/Library                       → module
//	scip Field/Property/Variable/Constant/EnumMember/Event/…    → variable
func kindOf(k pb.SymbolInformation_Kind, symbol string) string {
	switch k {
	case pb.SymbolInformation_Class, pb.SymbolInformation_SingletonClass,
		pb.SymbolInformation_Mixin, pb.SymbolInformation_Extension,
		pb.SymbolInformation_Concept, pb.SymbolInformation_Contract:
		return "class"
	case pb.SymbolInformation_Interface, pb.SymbolInformation_Trait,
		pb.SymbolInformation_Protocol, pb.SymbolInformation_TypeClass:
		return "interface"
	case pb.SymbolInformation_Struct, pb.SymbolInformation_Enum:
		return "struct"
	case pb.SymbolInformation_Method, pb.SymbolInformation_AbstractMethod,
		pb.SymbolInformation_StaticMethod, pb.SymbolInformation_Getter,
		pb.SymbolInformation_Setter, pb.SymbolInformation_Constructor,
		pb.SymbolInformation_ProtocolMethod, pb.SymbolInformation_TraitMethod,
		pb.SymbolInformation_MethodAlias, pb.SymbolInformation_MethodSpecification,
		pb.SymbolInformation_PureVirtualMethod, pb.SymbolInformation_SingletonMethod,
		pb.SymbolInformation_Accessor, pb.SymbolInformation_Subscript,
		pb.SymbolInformation_MethodReceiver:
		return "method"
	case pb.SymbolInformation_Function, pb.SymbolInformation_Macro,
		pb.SymbolInformation_Predicate, pb.SymbolInformation_Tactic,
		pb.SymbolInformation_Theorem, pb.SymbolInformation_Quasiquoter:
		return "function"
	case pb.SymbolInformation_Type, pb.SymbolInformation_TypeAlias,
		pb.SymbolInformation_AssociatedType, pb.SymbolInformation_TypeParameter,
		pb.SymbolInformation_TypeFamily, pb.SymbolInformation_Signature:
		return "type"
	case pb.SymbolInformation_Namespace, pb.SymbolInformation_Module,
		pb.SymbolInformation_Library, pb.SymbolInformation_Lang:
		return "module"
	case pb.SymbolInformation_Field, pb.SymbolInformation_Property,
		pb.SymbolInformation_Variable, pb.SymbolInformation_Constant,
		pb.SymbolInformation_EnumMember, pb.SymbolInformation_Event,
		pb.SymbolInformation_Attribute, pb.SymbolInformation_StaticField,
		pb.SymbolInformation_StaticProperty, pb.SymbolInformation_StaticVariable,
		pb.SymbolInformation_StaticDataMember, pb.SymbolInformation_StaticEvent,
		pb.SymbolInformation_ParameterLabel:
		return "variable"
	}
	// Fallback: derive from the last descriptor's suffix shape.
	sym, err := pb.ParseSymbol(symbol)
	if err != nil || len(sym.Descriptors) == 0 {
		return "type"
	}
	d := sym.Descriptors[len(sym.Descriptors)-1]
	switch d.Suffix {
	case pb.Descriptor_Namespace:
		return "module"
	case pb.Descriptor_Method:
		return "method"
	case pb.Descriptor_Macro:
		return "function"
	case pb.Descriptor_TypeParameter, pb.Descriptor_Parameter, pb.Descriptor_Local:
		return "variable"
	default:
		return "type"
	}
}

// ProducerOf returns metadata tool_info.name ("" when absent) — the producer
// tag stem for "scip:<tool>".
func ProducerOf(idx *pb.Index) string {
	if idx == nil || idx.Metadata == nil || idx.Metadata.ToolInfo == nil {
		return ""
	}
	return idx.Metadata.ToolInfo.Name
}

// VersionOf returns metadata tool_info.version ("" when absent).
func VersionOf(idx *pb.Index) string {
	if idx == nil || idx.Metadata == nil || idx.Metadata.ToolInfo == nil {
		return ""
	}
	return idx.Metadata.ToolInfo.Version
}
