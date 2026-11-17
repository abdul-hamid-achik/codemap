package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/index"
)

// TestImpactBlastIncludesInheritance pins C1: the blast radius follows the
// inheritance family. Changing a base type reaches its declared subtype (via
// extends), and changing a base method reaches the overriding method (via
// overrides) — both name-provenance candidates until a precise pass.
func TestImpactBlastIncludesInheritance(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	write := func(rel, content string) {
		full := filepath.Join(proj, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("src/base.ts", "export class Base {\n  save(): void {}\n}\n")
	write("src/repo.ts", "import { Base } from \"./base\"\nexport class Repo extends Base {\n  save(): void { this.flush() }\n  flush(): void {}\n}\n")
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	svc := NewService(sess)
	if _, err := svc.Index(context.Background(), proj, index.Options{NoLSP: true}, false); err != nil {
		t.Fatal(err)
	}

	find := func(rep *ImpactReport, symbol string) (ImpactNode, bool) {
		for _, n := range rep.BlastRadius {
			if n.Symbol == symbol {
				return n, true
			}
		}
		return ImpactNode{}, false
	}

	// Changing the base type reaches the declared subtype, labeled via extends.
	rep, err := svc.Impact(proj, "Base", 2)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Found {
		t.Fatal("Base not found")
	}
	node, ok := find(rep, "Repo")
	if !ok {
		t.Fatalf("Repo subtype missing from blast radius: %+v", rep.BlastRadius)
	}
	if node.Via != "extends" || node.Depth != 1 {
		t.Errorf("Repo = depth %d via %q, want depth 1 via extends", node.Depth, node.Via)
	}
	if node.Confidence != ConfidenceCandidate {
		t.Errorf("declared extends reach = %q, want candidate", node.Confidence)
	}

	// Changing the base method reaches the overriding method via overrides. The
	// selector keeps the query on Base.save even though Repo.save shares the
	// short name.
	at, err := svc.SymbolAt(proj, "src/base.ts", 2)
	if err != nil {
		t.Fatal(err)
	}
	if at.Selector == nil {
		t.Fatalf("no symbol at src/base.ts:2: %+v", at)
	}
	mrep, err := svc.ImpactBySelector(proj, *at.Selector, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !mrep.Found {
		t.Fatal("Base.save not found via selector")
	}
	mnode, ok := find(mrep, "save")
	if !ok {
		t.Fatalf("overriding save missing from blast radius: %+v", mrep.BlastRadius)
	}
	if mnode.Via != "overrides" || mnode.Depth != 1 {
		t.Errorf("overriding save = depth %d via %q, want depth 1 via overrides", mnode.Depth, mnode.Via)
	}
	if mnode.Confidence != ConfidenceCandidate {
		t.Errorf("derived overrides reach = %q, want candidate", mnode.Confidence)
	}
}
