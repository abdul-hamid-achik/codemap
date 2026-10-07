package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/index"
)

func TestContextHierarchyAndOrphans(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	for name, body := range map[string]string{
		"repo.ts": "export interface Repo {\n  save(): void;\n}\n",
		"base.ts": "export class Base {\n  save() {}\n}\n",
		"user.ts": "import { Base } from \"./base\";\nimport type { Repo } from \"./repo\";\n\nexport class UserRepo extends Base implements Repo {\n  save() {}\n  unused() {}\n}\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	sess.Config.Vecgrep.Enabled = false
	svc := NewService(sess)
	if _, err := svc.Index(context.Background(), root, index.Options{NoLSP: true}, false); err != nil {
		t.Fatal(err)
	}

	rep, err := svc.Context(root, "UserRepo", 2, true)
	if err != nil {
		t.Fatal(err)
	}
	h := rep.Hierarchy
	if h == nil || len(h.Extends) != 1 || h.Extends[0].FQN != "Base" || len(h.Implements) != 1 || h.Implements[0].FQN != "Repo" {
		t.Fatalf("UserRepo hierarchy = %+v, want extends Base, implements Repo", h)
	}
	base, err := svc.Context(root, "Base", 2, true)
	if err != nil {
		t.Fatal(err)
	}
	if base.Hierarchy == nil || len(base.Hierarchy.Subtypes) != 1 || base.Hierarchy.Subtypes[0].FQN != "UserRepo" {
		t.Fatalf("Base hierarchy = %+v, want subtype UserRepo", base.Hierarchy)
	}
	save, err := svc.Context(root, "UserRepo.save", 2, true)
	if err != nil {
		t.Fatal(err)
	}
	if save.Hierarchy == nil || len(save.Hierarchy.Overrides) != 2 {
		t.Fatalf("UserRepo.save hierarchy = %+v, want overrides of Base.save and Repo.save", save.Hierarchy)
	}

	orphans, err := svc.Orphans(root, 50)
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, o := range orphans.Orphans {
		listed[o.FQN] = true
	}
	if listed["UserRepo.save"] {
		t.Error("an overriding method is reached through its base and must not be an orphan")
	}
	if !listed["UserRepo.unused"] {
		t.Errorf("UserRepo.unused overrides nothing and should stay a candidate orphan: %v", listed)
	}
}
