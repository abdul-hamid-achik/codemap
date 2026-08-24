package index

import (
	"path/filepath"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/extract/gosrc"
	"github.com/abdul-hamid-achik/codemap/internal/extract/lspsrc"
	"github.com/abdul-hamid-achik/codemap/internal/extract/tsscan"
	"github.com/abdul-hamid-achik/codemap/internal/extract/vuesrc"
)

// fileImportSpecs returns the file's import specifiers without talking to a
// language server. LSP ExtractFile is DidOpen+documentSymbol+DidClose; doing
// that again in the deferred import pass doubled TS/JS/Python/Vue index time
// and re-opened every unchanged file on incremental runs.
//
// ok is false when specs could not be recovered (parse failure). The caller
// must leave existing import edges alone in that case — empty specs with ok
// true mean "this file imports nothing" and replace the prior edges.
func fileImportSpecs(ft fileTask, src []byte) (specs []string, ok bool) {
	switch ft.lang {
	case "typescript", "javascript":
		return tsscan.Imports(src), true
	case "vue":
		return vuesrc.ImportSpecs(src), true
	case "python":
		return pythonImportSpecs(src), true
	case "go":
		specs, err := gosrc.ImportSpecs(ft.rel, src)
		return specs, err == nil
	}
	if _, isLSP := ft.ext.(*lspsrc.Extractor); isLSP {
		return nil, true
	}
	if ft.ext == nil {
		return nil, false
	}
	fr, err := ft.ext.ExtractFile(ft.rel, src)
	if err != nil || fr == nil {
		return nil, false
	}
	return fr.Imports, true
}

func pythonImportSpecs(src []byte) []string {
	seen := map[string]bool{}
	var out []string
	add := func(spec string) {
		spec = strings.TrimSpace(spec)
		if spec == "" || seen[spec] || !pyImportModule(spec) {
			return
		}
		seen[spec] = true
		out = append(out, spec)
	}
	for _, line := range strings.Split(string(src), "\n") {
		code, _, _ := strings.Cut(line, "#")
		code = strings.TrimSpace(code)
		if rest, ok := strings.CutPrefix(code, "import "); ok {
			for _, part := range strings.Split(rest, ",") {
				part = strings.TrimSpace(part)
				if i := strings.Index(part, " as "); i >= 0 {
					part = strings.TrimSpace(part[:i])
				}
				add(part)
			}
			continue
		}
		if rest, ok := strings.CutPrefix(code, "from "); ok {
			mod, after, found := strings.Cut(rest, " import")
			if !found {
				continue
			}
			mod = strings.TrimSpace(mod)
			add(mod)
			// `from . import helper` — the imported names are sibling modules.
			if pyOnlyDots(mod) {
				names := strings.Trim(strings.TrimSpace(after), "()")
				for _, part := range strings.Split(names, ",") {
					part = strings.TrimSpace(part)
					if i := strings.Index(part, " as "); i >= 0 {
						part = strings.TrimSpace(part[:i])
					}
					if pyImportModule(part) && !strings.Contains(part, ".") {
						add(mod + part)
					}
				}
			}
		}
	}
	return out
}

func pyOnlyDots(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r != '.' {
			return false
		}
	}
	return true
}

func pyImportModule(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '.':
		case r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z':
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}

func resolvePythonImport(fromRel, spec string, idx *importIndex) string {
	dots := 0
	for dots < len(spec) && spec[dots] == '.' {
		dots++
	}
	rest := strings.ReplaceAll(spec[dots:], ".", "/")
	var base string
	if dots == 0 {
		base = rest
	} else {
		dir := parentDir(filepath.ToSlash(fromRel))
		for i := 1; i < dots; i++ {
			dir = parentDir(dir)
		}
		base = joinSlash(dir, rest)
	}
	return lookupPyFile(idx, strings.Trim(base, "/"))
}

func lookupPyFile(idx *importIndex, base string) string {
	if idx == nil {
		return ""
	}
	candidates := []string{base + ".py", base + ".pyi", joinSlash(base, "__init__.py")}
	if base == "" {
		candidates = []string{"__init__.py"}
	}
	for _, cand := range candidates {
		if cand != "" && idx.relFiles[cand] {
			return cand
		}
	}
	return ""
}

func resolveGDScriptImport(fromRel, spec string, idx *importIndex) string {
	spec = strings.TrimPrefix(filepath.ToSlash(spec), "res://")
	spec = strings.TrimPrefix(spec, "/")
	if spec == "" || idx == nil {
		return ""
	}
	if idx.relFiles[spec] {
		return spec
	}
	if !strings.HasSuffix(spec, ".gd") && idx.relFiles[spec+".gd"] {
		return spec + ".gd"
	}
	fromDir := parentDir(filepath.ToSlash(fromRel))
	joined := normalizeSlashPath(joinSlash(fromDir, spec))
	if joined == "" {
		return ""
	}
	if idx.relFiles[joined] {
		return joined
	}
	if !strings.HasSuffix(joined, ".gd") && idx.relFiles[joined+".gd"] {
		return joined + ".gd"
	}
	return ""
}
