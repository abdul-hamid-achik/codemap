package index

import (
	"reflect"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/extract"
)

func TestPythonImportSpecs(t *testing.T) {
	src := []byte(`
import os
import pkg.mod as m, sibling
from . import helper, other as o
from .local import x
from ..pkg import y
# import skipped
`)
	got := pythonImportSpecs(src)
	want := []string{"os", "pkg.mod", "sibling", ".", ".helper", ".other", ".local", "..pkg"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("pythonImportSpecs = %v, want %v", got, want)
	}
}

func TestResolvePythonImport(t *testing.T) {
	idx := &importIndex{relFiles: map[string]bool{
		"pkg/__init__.py":   true,
		"pkg/a.py":          true,
		"pkg/helper.py":     true,
		"pkg/local.py":      true,
		"other/__init__.py": true,
		"other/mod.py":      true,
	}}
	cases := []struct {
		from, spec, want string
	}{
		{"pkg/a.py", "pkg.local", "pkg/local.py"},
		{"pkg/a.py", ".helper", "pkg/helper.py"},
		{"pkg/a.py", ".", "pkg/__init__.py"},
		{"pkg/a.py", "..other.mod", "other/mod.py"},
		{"pkg/a.py", "os", ""},
	}
	for _, tc := range cases {
		if got := resolvePythonImport(tc.from, tc.spec, idx); got != tc.want {
			t.Errorf("resolvePythonImport(%q, %q) = %q, want %q", tc.from, tc.spec, got, tc.want)
		}
	}
}

func TestResolveGDScriptImport(t *testing.T) {
	idx := &importIndex{relFiles: map[string]bool{
		"scripts/helper.gd": true,
		"main.gd":           true,
		"enemy.gd":          true,
	}}
	cases := []struct {
		from, spec, want string
	}{
		{"main.gd", "scripts/helper.gd", "scripts/helper.gd"},
		{"main.gd", "res://scripts/helper.gd", "scripts/helper.gd"},
		{"main.gd", "./enemy", "enemy.gd"},
		{"main.gd", "missing.gd", ""},
	}
	for _, tc := range cases {
		if got := resolveGDScriptImport(tc.from, tc.spec, idx); got != tc.want {
			t.Errorf("resolveGDScriptImport(%q, %q) = %q, want %q", tc.from, tc.spec, got, tc.want)
		}
	}
}

func TestFileImportSpecsSkipsLSPExtractor(t *testing.T) {
	ft := fileTask{rel: "a.ts", lang: "typescript", ext: panicExtractor{lang: "typescript"}}
	src := []byte("import { x } from './b'\nexport const y = x\n")
	specs, ok := fileImportSpecs(ft, src)
	if !ok {
		t.Fatal("fileImportSpecs returned ok=false")
	}
	if len(specs) != 1 || specs[0] != "./b" {
		t.Errorf("specs = %v, want ['./b'] (must not call ExtractFile)", specs)
	}
}

type panicExtractor struct{ lang string }

func (p panicExtractor) Language() string { return p.lang }

func (p panicExtractor) ExtractFile(string, []byte) (*extract.FileResult, error) {
	panic("ExtractFile must not be called for LSP languages in the import pass")
}
