package index

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pb "github.com/scip-code/scip/bindings/go/scip"

	"github.com/abdul-hamid-achik/codemap/internal/config"
	scipx "github.com/abdul-hamid-achik/codemap/internal/extract/scip"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

// scipFixture writes a hermetic two-file .scip index for the given project
// root (metadata project_root must match the codemap project root exactly)
// and returns its path. Symbols: Greeter (class, implements Speaker), Speaker
// (interface), greet (method) in Greeter.java; Main (class) in Main.java
// referencing greet and one external symbol.
func scipFixture(t *testing.T, root string, mutate func(*pb.Index)) string {
	t.Helper()
	class := "java com example lib Greeter"
	iface := "java com example lib Speaker"
	greet := class + "#greet()."
	main := "java com example app Main"
	ext := "java util Objects"
	def := func(symbol string, line int32) *pb.Occurrence {
		o := &pb.Occurrence{Symbol: symbol, SymbolRoles: int32(pb.SymbolRole_Definition)}
		o.SetSourceRange(pb.Range{Start: pb.Position{Line: line}, End: pb.Position{Line: line + 2}})
		return o
	}
	ref := func(symbol string, line int32) *pb.Occurrence {
		o := &pb.Occurrence{Symbol: symbol}
		o.SetSourceRange(pb.Range{Start: pb.Position{Line: line}, End: pb.Position{Line: line}})
		return o
	}
	idx := &pb.Index{
		Metadata: &pb.Metadata{
			ProjectRoot:          "file://" + filepath.ToSlash(root),
			TextDocumentEncoding: pb.TextEncoding_UTF8,
			ToolInfo:             &pb.ToolInfo{Name: "scip-java", Version: "1.2.3"},
		},
		Documents: []*pb.Document{
			{
				Language: "Java", RelativePath: "src/lib/Greeter.java",
				Symbols: []*pb.SymbolInformation{
					{Symbol: class, Kind: pb.SymbolInformation_Class, DisplayName: "Greeter",
						Relationships: []*pb.Relationship{{Symbol: iface, IsImplementation: true}}},
					{Symbol: iface, Kind: pb.SymbolInformation_Interface, DisplayName: "Speaker"},
					{Symbol: greet, Kind: pb.SymbolInformation_Method, DisplayName: "greet"},
				},
				Occurrences: []*pb.Occurrence{def(class, 3), def(iface, 4), def(greet, 5)},
			},
			{
				Language: "Java", RelativePath: "src/app/Main.java",
				Symbols: []*pb.SymbolInformation{
					{Symbol: main, Kind: pb.SymbolInformation_Class, DisplayName: "Main"},
				},
				Occurrences: []*pb.Occurrence{def(main, 2), ref(greet, 3), ref(ext, 4)},
			},
		},
	}
	if mutate != nil {
		mutate(idx)
	}
	var buf bytes.Buffer
	if err := scipx.WriteIndex(&buf, idx); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "index.scip")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestScipImportSeam(t *testing.T) {
	dir := t.TempDir()
	for _, rel := range []string{"src/lib/Greeter.java", "src/app/Main.java"} {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("// fixture\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	g, err := graph.Open(filepath.Join(t.TempDir(), "g.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	pid, _ := g.UpsertProject("scipapp", dir, "java")
	ix := New(g, nil, nil, config.DefaultConfig().Index)

	scipPath := scipFixture(t, dir, nil)
	res, err := ix.IndexProject(context.Background(), pid, "scipapp", dir, Options{NoLSP: true, ScipPath: scipPath})
	if err != nil {
		t.Fatal(err)
	}
	if res.Scip == nil {
		t.Fatal("res.Scip not published")
	}
	r := res.Scip
	if r.Producer != "scip-java" || r.Version != "1.2.3" {
		t.Fatalf("producer/version = %q/%q", r.Producer, r.Version)
	}
	if r.Definitions != 4 || r.References != 1 || r.Implements != 1 || r.Imports != 1 {
		t.Fatalf("report = %+v", r)
	}
	if r.UnresolvedReferences != 1 {
		t.Fatalf("unresolved = %d, want 1 (the java.util.Objects ref)", r.UnresolvedReferences)
	}
	if r.CallEdges != 0 {
		t.Fatalf("call_edges = %d, an import must never claim calls", r.CallEdges)
	}

	// Node surface: producer/version stamped in source_hash, kinds mapped.
	nodes, err := g.ProjectNodes(pid)
	if err != nil {
		t.Fatal(err)
	}
	var greeter, speaker, greetNode bool
	for _, n := range nodes {
		if !strings.HasPrefix(n.SourceHash, "scip:scip-java@1.2.3") {
			t.Errorf("node %s source_hash = %q, want scip producer tag", n.Symbol, n.SourceHash)
		}
		switch n.FQN {
		case "java com example lib Greeter":
			greeter = n.Kind == "class"
		case "java com example lib Speaker":
			speaker = n.Kind == "interface"
		case "java com example lib Greeter#greet().":
			greetNode = n.Kind == "method"
		}
	}
	if !greeter || !speaker || !greetNode {
		t.Fatalf("kind mapping incomplete: greeter=%v speaker=%v greet=%v", greeter, speaker, greetNode)
	}

	// Edge surface: producer-tagged provenance, weight 0.7, no calls.
	edges, err := g.ProjectEdges(pid)
	if err != nil {
		t.Fatal(err)
	}
	byType := map[string]int{}
	for _, e := range edges {
		byType[e.EdgeType]++
		if e.Provenance != "scip:scip-java" {
			t.Errorf("edge %d provenance = %q", e.ID, e.Provenance)
		}
		if e.Weight != 0.7 {
			t.Errorf("edge %d weight = %v, want 0.7", e.ID, e.Weight)
		}
	}
	if byType[graph.EdgeReferences] != 1 || byType[graph.EdgeImplements] != 1 || byType[graph.EdgeImports] != 1 {
		t.Fatalf("edge types = %v", byType)
	}
	if n, _ := g.CountEdgesByType(pid, graph.EdgeCalls); n != 0 {
		t.Fatalf("calls edges = %d, want 0 (SCIP is not call-role evidence)", n)
	}

	// Re-import: atomic per-producer replacement, not duplication.
	_, err = ix.IndexProject(context.Background(), pid, "scipapp", dir, Options{NoLSP: true, ScipPath: scipPath})
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := g.CountEdgesByType(pid, graph.EdgeReferences); n != 1 {
		t.Fatalf("references after re-import = %d, want 1 (generation replaced)", n)
	}
	nodes2, _ := g.ProjectNodes(pid)
	if len(nodes2) != len(nodes) {
		t.Fatalf("nodes after re-import = %d, want %d", len(nodes2), len(nodes))
	}

	// New generation with an extra definition replaces the old one.
	scipPath2 := scipFixture(t, dir, func(i *pb.Index) {
		extra := "java com example lib Extra"
		i.Documents[0].Symbols = append(i.Documents[0].Symbols,
			&pb.SymbolInformation{Symbol: extra, Kind: pb.SymbolInformation_Struct, DisplayName: "Extra"})
		i.Documents[0].Occurrences = append(i.Documents[0].Occurrences,
			func() *pb.Occurrence {
				o := &pb.Occurrence{Symbol: extra, SymbolRoles: int32(pb.SymbolRole_Definition)}
				o.SetSourceRange(pb.Range{Start: pb.Position{Line: 9}, End: pb.Position{Line: 10}})
				return o
			}())
	})
	if _, err := ix.IndexProject(context.Background(), pid, "scipapp", dir, Options{NoLSP: true, ScipPath: scipPath2}); err != nil {
		t.Fatal(err)
	}
	nodes3, _ := g.ProjectNodes(pid)
	var hasExtra, hasMain bool
	for _, n := range nodes3 {
		if n.FQN == "java com example lib Extra" {
			hasExtra = true
		}
		if n.FQN == "java com example app Main" {
			hasMain = true
		}
	}
	if !hasExtra || !hasMain {
		t.Fatalf("replacement generation wrong: extra=%v main=%v", hasExtra, hasMain)
	}
}

func TestScipImportInvalidFileLeavesGraphUntouched(t *testing.T) {
	dir := t.TempDir()
	g, err := graph.Open(filepath.Join(t.TempDir(), "g.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	pid, _ := g.UpsertProject("scipbad", dir, "java")
	ix := New(g, nil, nil, config.DefaultConfig().Index)

	// Seed one valid generation, remember the counts, then feed a traversal
	// fixture: the error must be actionable and the graph must not change.
	good := scipFixture(t, dir, nil)
	if _, err := ix.IndexProject(context.Background(), pid, "scipbad", dir, Options{NoLSP: true, ScipPath: good}); err != nil {
		t.Fatal(err)
	}
	before, _ := g.ProjectNodes(pid)
	beforeEdges, _ := g.ProjectEdges(pid)

	bad := scipFixture(t, dir, func(i *pb.Index) {
		i.Documents[1].RelativePath = "../escape/Main.java"
	})
	if _, err := ix.IndexProject(context.Background(), pid, "scipbad", dir, Options{NoLSP: true, ScipPath: bad}); err == nil {
		t.Fatal("traversal fixture accepted")
	} else if !strings.Contains(err.Error(), "escapes the project root") {
		t.Fatalf("err = %v, want traversal message", err)
	}
	after, _ := g.ProjectNodes(pid)
	afterEdges, _ := g.ProjectEdges(pid)
	if len(after) != len(before) || len(afterEdges) != len(beforeEdges) {
		t.Fatalf("graph mutated by invalid import: %d/%d -> %d/%d",
			len(before), len(beforeEdges), len(after), len(afterEdges))
	}

	// A missing file is an actionable error, not a panic.
	if _, err := ix.IndexProject(context.Background(), pid, "scipbad", dir, Options{NoLSP: true, ScipPath: filepath.Join(dir, "nope.scip")}); err == nil || !strings.Contains(err.Error(), "read scip index") {
		t.Fatalf("missing-file err = %v", err)
	}
}
