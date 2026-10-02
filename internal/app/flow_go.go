package app

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

// Go-specific call-site evidence for codemap flow: which package qualifiers a
// file imports (so viper.New() is recognized as an external call and
// gosrc.New() as internal/extract/gosrc) and which targets one call site's
// syntax can reach.

var flowImportLine = regexp.MustCompile(`^\s*(?:import\s+)?(?:([A-Za-z_][A-Za-z0-9_]*|\.)\s+)?"([^"]+)"`)

// flowImportInfo maps the package qualifiers a Go file can use to their import
// paths. known is false when the file could not be read or declares no imports.
type flowImportInfo struct {
	byName map[string]string
	known  bool
}

func (b *flowBuilder) goImports(file string) flowImportInfo {
	if ii, ok := b.imports[file]; ok {
		return ii
	}
	ii := flowImportInfo{byName: map[string]string{}}
	lines := b.fileLines(file)
	inBlock := false
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		switch {
		case inBlock && strings.HasPrefix(trim, ")"):
			inBlock = false
			continue
		case !inBlock && strings.HasPrefix(trim, "import ("):
			inBlock = true
			continue
		case !inBlock && strings.HasPrefix(trim, "import "):
		case inBlock:
		case strings.HasPrefix(trim, "func ") || strings.HasPrefix(trim, "type ") || strings.HasPrefix(trim, "var ") || strings.HasPrefix(trim, "const "):
			b.imports[file] = ii
			return ii
		default:
			continue
		}
		m := flowImportLine.FindStringSubmatch(trim)
		if m == nil {
			continue
		}
		name, path := m[1], m[2]
		if name == "_" || name == "." {
			continue
		}
		if name == "" {
			name = flowImportName(path)
		}
		ii.byName[name] = path
		ii.known = true
	}
	b.imports[file] = ii
	return ii
}

// flowImportName guesses the package name of an import path: its last element,
// skipping a /vN major-version suffix and trimming a ".vN" or "-go" style tail.
func flowImportName(path string) string {
	parts := strings.Split(path, "/")
	name := parts[len(parts)-1]
	if len(parts) > 1 && len(name) >= 2 && name[0] == 'v' && strings.Trim(name[1:], "0123456789") == "" {
		name = parts[len(parts)-2]
	}
	if dot := strings.LastIndexByte(name, '.'); dot > 0 {
		name = name[:dot]
	}
	return strings.ReplaceAll(strings.TrimSuffix(name, "-go"), "-", "_")
}

// reaches reports whether `qual.X(...)` can denote the package-level target t.
func (ii flowImportInfo) reaches(qual string, t graph.Node) bool {
	path, imported := ii.byName[qual]
	if !imported {
		return !ii.known && flowPackageOf(t.FQN) == qual
	}
	dir := filepath.ToSlash(filepath.Dir(t.FilePath))
	if dir == "." {
		return flowPackageOf(t.FQN) == qual
	}
	return path == dir || strings.HasSuffix(path, "/"+dir)
}

// flowGoSiteCands returns the targets one Go call site can syntactically reach:
// a bare Name() is a function of the parent's own package; pkg.Name() is that
// imported package's function; recv.Name() on the parent's receiver is a method
// of its type; x.Name() on any other expression is a method.
func flowGoSiteCands(targets []graph.Node, s flowSite, recvName, recvType, parentDir string, ii flowImportInfo) []graph.Node {
	var out []graph.Node
	methods := func() []graph.Node {
		var ms []graph.Node
		for _, t := range targets {
			if t.Kind == graph.KindMethod {
				ms = append(ms, t)
			}
		}
		return ms
	}
	switch {
	case !s.qualified:
		for _, t := range targets {
			if t.Kind != graph.KindMethod && filepath.Dir(t.FilePath) == parentDir {
				out = append(out, t)
			}
		}
	case s.qual != "" && s.qual == recvName && recvType != "":
		for _, t := range targets {
			if t.Kind == graph.KindMethod && strings.HasPrefix(t.FQN, recvType+".") {
				out = append(out, t)
			}
		}
		if len(out) == 0 { // promoted from an embedded type
			out = methods()
		}
	default:
		if _, isPkg := ii.byName[s.qual]; isPkg && s.qual != "" {
			for _, t := range targets {
				if t.Kind != graph.KindMethod && ii.reaches(s.qual, t) {
					out = append(out, t)
				}
			}
			return out // an imported package: methods cannot be reached through it
		}
		if s.qual != "" && !ii.known {
			for _, t := range targets {
				if t.Kind != graph.KindMethod && ii.reaches(s.qual, t) {
					out = append(out, t)
				}
			}
			if len(out) > 0 {
				return out
			}
		}
		out = methods()
		if len(out) == 0 && !ii.known { // imports unreadable: assume an aliased package
			for _, t := range targets {
				if t.Kind != graph.KindMethod {
					out = append(out, t)
				}
			}
		}
	}
	return out
}
