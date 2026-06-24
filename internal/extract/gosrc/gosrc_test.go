package gosrc

import (
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/extract"
)

const sample = `package sample

import (
	"fmt"
	"strings"
)

// Greeter greets people.
type Greeter struct {
	Name string
}

// Hello returns a greeting.
func (g Greeter) Hello() string {
	return fmt.Sprintf("hi %s", strings.ToUpper(g.Name))
}

// Top is a free function.
func Top() {
	g := Greeter{}
	_ = g.Hello()
	_ = len("x") // builtin, must be ignored
}
`

func findSym(syms []extract.Symbol, name string) *extract.Symbol {
	for i := range syms {
		if syms[i].Name == name {
			return &syms[i]
		}
	}
	return nil
}

func hasRef(refs []extract.Reference, from, to string) bool {
	for _, r := range refs {
		if r.From == from && r.To == to {
			return true
		}
	}
	return false
}

func TestExtractGo(t *testing.T) {
	res, err := New().ExtractFile("sample.go", []byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if res.Language != "go" {
		t.Errorf("language = %q, want go", res.Language)
	}

	// imports
	wantImports := map[string]bool{"fmt": false, "strings": false}
	for _, imp := range res.Imports {
		if _, ok := wantImports[imp]; ok {
			wantImports[imp] = true
		}
	}
	for imp, found := range wantImports {
		if !found {
			t.Errorf("missing import %q", imp)
		}
	}

	// type
	if g := findSym(res.Symbols, "Greeter"); g == nil {
		t.Error("missing Greeter symbol")
	} else {
		if g.Kind != extract.KindType {
			t.Errorf("Greeter kind = %q, want type", g.Kind)
		}
		if g.FQN != "sample.Greeter" {
			t.Errorf("Greeter FQN = %q", g.FQN)
		}
		if !strings.Contains(g.Docstring, "greets people") {
			t.Errorf("Greeter doc = %q", g.Docstring)
		}
	}

	// method
	if h := findSym(res.Symbols, "Hello"); h == nil {
		t.Error("missing Hello symbol")
	} else {
		if h.Kind != extract.KindMethod {
			t.Errorf("Hello kind = %q, want method", h.Kind)
		}
		if h.FQN != "sample.Greeter.Hello" {
			t.Errorf("Hello FQN = %q", h.FQN)
		}
		if !strings.Contains(h.Signature, "func (g Greeter) Hello() string") {
			t.Errorf("Hello signature = %q", h.Signature)
		}
		if !strings.Contains(h.Docstring, "returns a greeting") {
			t.Errorf("Hello doc = %q", h.Docstring)
		}
		if !strings.Contains(h.Source, "fmt.Sprintf") {
			t.Errorf("Hello source missing body: %q", h.Source)
		}
		if h.EndLine < h.StartLine {
			t.Errorf("Hello lines %d-%d invalid", h.StartLine, h.EndLine)
		}
	}

	// function
	if top := findSym(res.Symbols, "Top"); top == nil {
		t.Error("missing Top symbol")
	} else if top.Kind != extract.KindFunction {
		t.Errorf("Top kind = %q, want function", top.Kind)
	}

	// references (call edges)
	if !hasRef(res.References, "sample.Greeter.Hello", "Sprintf") {
		t.Error("missing Hello -> Sprintf call ref")
	}
	if !hasRef(res.References, "sample.Greeter.Hello", "ToUpper") {
		t.Error("missing Hello -> ToUpper call ref")
	}
	if !hasRef(res.References, "sample.Top", "Hello") {
		t.Error("missing Top -> Hello call ref")
	}
	if hasRef(res.References, "sample.Top", "len") {
		t.Error("builtin len should not be a call ref")
	}
}

func TestExtractTestFile(t *testing.T) {
	src := `package sample

import "testing"

func TestThing(t *testing.T) {}

func helper() {}
`
	res, err := New().ExtractFile("thing_test.go", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if tn := findSym(res.Symbols, "TestThing"); tn == nil || tn.Kind != extract.KindTest {
		t.Errorf("TestThing should be kind test, got %+v", tn)
	}
	if h := findSym(res.Symbols, "helper"); h == nil || h.Kind != extract.KindFunction {
		t.Errorf("helper should be kind function, got %+v", h)
	}
}

func TestExtractSyntaxError(t *testing.T) {
	if _, err := New().ExtractFile("bad.go", []byte("package x\nfunc (")); err == nil {
		t.Error("expected parse error for invalid Go")
	}
}

func TestPointerReceiver(t *testing.T) {
	src := `package p
type T struct{}
func (t *T) M() {}
`
	res, err := New().ExtractFile("p.go", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	m := findSym(res.Symbols, "M")
	if m == nil || m.FQN != "p.T.M" {
		t.Errorf("pointer-receiver method FQN = %v, want p.T.M", m)
	}
}

func hasKindRef(refs []extract.Reference, from, to, kind string) bool {
	for _, r := range refs {
		if r.From == from && r.To == to && r.Kind == kind {
			return true
		}
	}
	return false
}

// TestValueRefsForFunctionValues pins function-value reference extraction: a
// function used as a VALUE (a cobra `RunE: handler` table, a callback passed to
// register) becomes a `references` edge (not a `calls` edge), so such handlers
// aren't flagged as dead code while the call graph stays clean. Top-level decls
// are attributed to the file (relPath); body refs to the enclosing function.
func TestValueRefsForFunctionValues(t *testing.T) {
	const src = `package sample

func runInit() {}
func handler() {}
func handleX() {}
func register(f func()) {}

type Command struct{ RunE func() }
type Server struct{}

func (s *Server) handleReq() {}

var cmd = &Command{RunE: runInit}

func setup() {
	register(handler)
	register(handleX)
}

func (s *Server) wire() {
	register(s.handleReq) // method value (selector), e.g. mux.HandleFunc("/", s.handleReq)
}
`
	res, err := New().ExtractFile("cmd.go", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	// Top-level `RunE: runInit` → a references edge from the file to runInit.
	if !hasKindRef(res.References, "cmd.go", "runInit", extract.RefReferences) {
		t.Errorf("expected file->runInit references edge (top-level cobra-style handler), got %+v", res.References)
	}
	// Callbacks passed as args inside a body → references from the enclosing func.
	if !hasKindRef(res.References, "sample.setup", "handler", extract.RefReferences) {
		t.Error("expected setup->handler references edge (callback arg)")
	}
	if !hasKindRef(res.References, "sample.setup", "handleX", extract.RefReferences) {
		t.Error("expected setup->handleX references edge (callback arg)")
	}
	// A method value passed as an arg (s.handleReq) → references edge by method name.
	if !hasKindRef(res.References, "sample.Server.wire", "handleReq", extract.RefReferences) {
		t.Errorf("expected Server.wire->handleReq references edge (method value), got %+v", res.References)
	}
	// A function used only as a value is never a call target.
	if hasKindRef(res.References, "cmd.go", "runInit", extract.RefCalls) {
		t.Error("runInit is used as a value, not called — must not be a calls edge")
	}
	// Sanity: a real call (register(...)) is still a calls edge.
	if !hasKindRef(res.References, "sample.setup", "register", extract.RefCalls) {
		t.Error("register(...) should remain a calls edge")
	}
}
