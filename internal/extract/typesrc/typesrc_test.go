package typesrc

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/tools/go/packages"

	"github.com/abdul-hamid-achik/codemap/internal/extract/gosrc"
)

// TestLoadModeExcludesNeedDeps guards the 706MB footgun: NeedTypesInfo already
// resolves cross-package callees via export data, so NeedDeps must stay off.
func TestLoadModeExcludesNeedDeps(t *testing.T) {
	if LoadMode&packages.NeedDeps != 0 {
		t.Fatal("LoadMode must not include packages.NeedDeps (type-checks every dependency from source)")
	}
	if LoadMode&packages.NeedTypesInfo == 0 {
		t.Fatal("LoadMode must include packages.NeedTypesInfo (resolves callees)")
	}
}

const fixtureSrc = `package fix

import "fmt"

type T1 struct{}
type T2 struct{}
type T3 struct{}

func (T1) Close() error { return nil }
func (T2) Close() error { return nil }
func (T3) Close() error { return nil }

type Closer interface{ Close() error }

func UseT1() { var t T1; _ = t.Close() }

func UseIface(c Closer) { _ = c.Close() }

func UseStdlib() { fmt.Println("hi") }
`

func writeFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/fix\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fix.go"), []byte(fixtureSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func edgesFrom(res *Result, caller string) []PreciseEdge {
	var out []PreciseEdge
	for _, e := range res.Edges {
		if e.CallerFQN == caller {
			out = append(out, e)
		}
	}
	return out
}

func TestResolvePrecise(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	dir := writeFixture(t)
	res, err := Resolve(context.Background(), dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !res.Available {
		t.Fatal("Resolve should be Available for a buildable module")
	}
	if !res.CleanFiles["fix.go"] {
		t.Errorf("fix.go should be a clean file, got CleanFiles=%v", res.CleanFiles)
	}

	// The crux: name-based resolution would fan t.Close() out to all three
	// same-named Close methods; precise resolution gives exactly ONE edge, to the
	// concrete type's method.
	t1 := edgesFrom(res, "fix.UseT1")
	if len(t1) != 1 {
		t.Fatalf("UseT1 should have exactly 1 precise call edge, got %d: %+v", len(t1), t1)
	}
	if t1[0].CalleeFQN != "fix.T1.Close" {
		t.Errorf("UseT1 callee = %q, want fix.T1.Close", t1[0].CalleeFQN)
	}
	if t1[0].External || t1[0].Interface {
		t.Errorf("UseT1->T1.Close should be internal concrete, got external=%v iface=%v", t1[0].External, t1[0].Interface)
	}
	if t1[0].CalleeFile != "fix.go" {
		t.Errorf("callee file = %q, want fix.go", t1[0].CalleeFile)
	}

	// Interface dispatch resolves (statically) to the interface method.
	ifc := edgesFrom(res, "fix.UseIface")
	if len(ifc) != 1 || ifc[0].CalleeFQN != "fix.Closer.Close" || !ifc[0].Interface {
		t.Errorf("UseIface should resolve to fix.Closer.Close (interface), got %+v", ifc)
	}

	// Stdlib callee is flagged external (no codemap node to point at).
	std := edgesFrom(res, "fix.UseStdlib")
	if len(std) != 1 || std[0].CalleeFQN != "fmt.Println" || !std[0].External {
		t.Errorf("UseStdlib should resolve to external fmt.Println, got %+v", std)
	}

	// Position parity: the precise callee line must equal the StartLine gosrc
	// records for the same declaration — otherwise the indexer's (file,line) join
	// silently drops every precise edge.
	gres, err := gosrc.New().ExtractFile("fix.go", []byte(fixtureSrc))
	if err != nil {
		t.Fatal(err)
	}
	var wantLine int
	for _, s := range gres.Symbols {
		if s.FQN == "fix.T1.Close" {
			wantLine = s.StartLine
		}
	}
	if wantLine == 0 {
		t.Fatal("gosrc did not record fix.T1.Close")
	}
	if t1[0].CalleeLine != wantLine {
		t.Errorf("precise callee line = %d, but gosrc StartLine = %d (position join would miss)", t1[0].CalleeLine, wantLine)
	}
}

func TestResolveDegradesOnNonModule(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	// A directory with a .go file but no go.mod: packages.Load can't form a module.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.go"), []byte("package x\nfunc F() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Resolve(context.Background(), dir)
	// Either a load error or an empty/unavailable result is acceptable — the
	// contract is "never panic, let the caller keep name edges". It must NOT
	// return spurious edges.
	if err == nil && len(res.Edges) != 0 {
		t.Errorf("a non-module dir should yield no precise edges, got %+v", res.Edges)
	}
}
