package scip

import (
	"bytes"
	"strings"
	"testing"

	pb "github.com/scip-code/scip/bindings/go/scip"
)

// fixture builds a hermetic two-file Java-flavored index in memory and
// serializes it in the canonical length-delimited .scip format — no external
// indexer needed.
func fixture(t *testing.T, mutate func(*pb.Index)) []byte {
	t.Helper()
	class := "java com example lib Greeter"
	ctor := class + "#`constructor`()."
	greet := class + "#greet()."
	iface := "java com example lib Speaker"
	other := "java com example app Main"
	sym := func(kind pb.SymbolInformation_Kind, symbol, display string, rel ...*pb.Relationship) *pb.SymbolInformation {
		return &pb.SymbolInformation{Symbol: symbol, Kind: kind, DisplayName: display, Relationships: rel}
	}
	implRel := &pb.Relationship{Symbol: iface, IsImplementation: true}
	idx := &pb.Index{
		Metadata: &pb.Metadata{
			Version:              pb.ProtocolVersion_UnspecifiedProtocolVersion,
			ProjectRoot:          "file:///proj",
			ToolInfo:             &pb.ToolInfo{Name: "scip-test-indexer", Version: "9.9.9"},
			TextDocumentEncoding: pb.TextEncoding_UTF8,
		},
	}
	doc := func(lang, rel string, symbols []*pb.SymbolInformation, occs ...*pb.Occurrence) *pb.Document {
		return &pb.Document{Language: lang, RelativePath: rel, Symbols: symbols, Occurrences: occs}
	}
	def := func(symbol string, line int32) *pb.Occurrence {
		o := &pb.Occurrence{Symbol: symbol, SymbolRoles: int32(pb.SymbolRole_Definition)}
		o.SetSourceRange(pb.Range{Start: pb.Position{Line: line, Character: 1}, End: pb.Position{Line: line + 2, Character: 1}})
		return o
	}
	ref := func(symbol string, line int32) *pb.Occurrence {
		o := &pb.Occurrence{Symbol: symbol}
		o.SetSourceRange(pb.Range{Start: pb.Position{Line: line, Character: 5}, End: pb.Position{Line: line, Character: 9}})
		return o
	}
	idx.Documents = []*pb.Document{
		doc("Java", "src/main/java/com/example/lib/Greeter.java",
			[]*pb.SymbolInformation{
				sym(pb.SymbolInformation_Class, class, "Greeter"),
				sym(pb.SymbolInformation_Constructor, ctor, "Greeter"),
				sym(pb.SymbolInformation_Method, greet, "greet"),
				sym(pb.SymbolInformation_Interface, iface, "Speaker", implRel),
			},
			def(class, 3), def(ctor, 4), def(greet, 5), def(iface, 6), ref(greet, 7),
		),
		doc("Java", "src/main/java/com/example/app/Main.java",
			[]*pb.SymbolInformation{sym(pb.SymbolInformation_Class, other, "Main")},
			def(other, 10), ref(greet, 11), ref(iface, 12),
		),
	}
	if mutate != nil {
		mutate(idx)
	}
	var buf bytes.Buffer
	if err := WriteIndex(&buf, idx); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestReadIndexRoundTrip(t *testing.T) {
	data := fixture(t, nil)
	idx, err := ReadIndex(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if idx.Metadata.ToolInfo.Name != "scip-test-indexer" {
		t.Fatalf("tool name = %q", idx.Metadata.ToolInfo.Name)
	}
	if len(idx.Documents) != 2 || len(idx.Documents[0].Occurrences) != 5 {
		t.Fatalf("documents = %d", len(idx.Documents))
	}
}

func TestImportSnapshot(t *testing.T) {
	idx, err := ReadIndex(bytes.NewReader(fixture(t, nil)))
	if err != nil {
		t.Fatal(err)
	}
	// The adapter reuses batch buffers between emit calls: a consumer must
	// copy records synchronously (the index seam writes nodes/edges inside
	// emit for exactly this reason).
	var defs []Def
	var refs, impls, imports int
	sum, err := Import(idx, Options{ProjectRoot: "/proj", ChunkSize: 2}, func(b Batch) error {
		defs = append(defs, b.Defs...)
		refs += len(b.Refs)
		impls += len(b.Implements)
		imports += len(b.Imports)
		for _, n := range []int{len(b.Defs), len(b.Refs), len(b.Implements), len(b.Imports)} {
			if n > 2 {
				t.Errorf("chunk slice exceeds ChunkSize: %+v", b)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Producer != "scip-test-indexer" || sum.Version != "9.9.9" {
		t.Fatalf("summary producer/version = %q/%q", sum.Producer, sum.Version)
	}
	if sum.Defs != 5 || len(defs) != 5 {
		t.Fatalf("defs = %d", len(defs))
	}
	if refs != 3 { // greet@7 (same file), greet@11, iface@12
		t.Fatalf("refs = %d", refs)
	}
	if impls != 1 {
		t.Fatalf("implements = %d", impls)
	}
	if imports != 1 { // Main.java → Greeter.java (deduped cross-file refs)
		t.Fatalf("imports = %d", imports)
	}
	// Kind mapping snapshot.
	kinds := map[string]string{}
	for _, d := range defs {
		kinds[d.Symbol] = d.Kind
	}
	want := map[string]string{
		"java com example lib Greeter":                  "class",
		"java com example lib Greeter#`constructor`().": "method",
		"java com example lib Greeter#greet().":         "method",
		"java com example lib Speaker":                  "interface",
		"java com example app Main":                     "class",
	}
	for sym, kind := range want {
		if kinds[sym] != kind {
			t.Errorf("kind(%s) = %q, want %q", sym, kinds[sym], kind)
		}
	}
	for _, d := range defs {
		if d.Language != "java" {
			t.Errorf("def %s language = %q", d.Symbol, d.Language)
		}
		if !strings.HasPrefix(d.File, "src/") || strings.Contains(d.File, "..") {
			t.Errorf("def %s file = %q", d.Symbol, d.File)
		}
	}
}

func TestImportRejectsInvalidIndexes(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*pb.Index)
		want   string
	}{
		{"traversal", func(i *pb.Index) {
			i.Documents[1].RelativePath = "../outside/Main.java"
		}, "escapes the project root"},
		{"absolute path", func(i *pb.Index) {
			i.Documents[1].RelativePath = "/etc/Main.java"
		}, "absolute"},
		{"wrong project root", func(i *pb.Index) {
			i.Metadata.ProjectRoot = "file:///other"
		}, "does not match"},
		{"missing metadata", func(i *pb.Index) {
			i.Metadata = nil
		}, "no metadata"},
		{"duplicate document", func(i *pb.Index) {
			i.Documents = append(i.Documents, i.Documents[0])
		}, "duplicate"},
		{"malformed range", func(i *pb.Index) {
			occ := i.Documents[0].Occurrences[0]
			occ.SetSourceRange(pb.Range{Start: pb.Position{Line: 9}, End: pb.Position{Line: 2}})
		}, "malformed range"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			idx, err := ReadIndex(bytes.NewReader(fixture(t, tc.mutate)))
			if err != nil {
				t.Fatal(err)
			}
			emitted := false
			_, err = Import(idx, Options{ProjectRoot: "/proj"}, func(Batch) error {
				emitted = true
				return nil
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want substring %q", err, tc.want)
			}
			if emitted {
				t.Error("records emitted despite validation failure")
			}
		})
	}
}

func TestReadIndexRejectsTruncated(t *testing.T) {
	data := fixture(t, nil)
	if _, err := ReadIndex(bytes.NewReader(data[:len(data)/2])); err == nil {
		t.Fatal("truncated index parsed without error")
	}
}

func TestNeverEmitsCallEdges(t *testing.T) {
	idx, err := ReadIndex(bytes.NewReader(fixture(t, nil)))
	if err != nil {
		t.Fatal(err)
	}
	// The Batch type has no call-edge field at all — the assertion is
	// structural: importing can only produce defs/refs/impls/imports.
	_, err = Import(idx, Options{ProjectRoot: "/proj"}, func(Batch) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
}
