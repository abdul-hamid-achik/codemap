// Package gdsrc extracts structural symbols and name-based references from
// GDScript source with a pure-Go line scanner (no CGO, no language server) —
// the same T1 fidelity as the Ruby/Lua backends: definition nodes, call
// references that the indexer resolves by name, and preload/load imports.
//
// The scanner is indentation-aware: class/func blocks open a frame; a frame
// closes on the matching dedented declaration or explicit end-of-file. That
// is exact for conventionally formatted GDScript and degrades to slightly-off
// EndLines on hand-crammed layouts — the accepted trade-off for a
// dependency-free backend, mirroring the Ruby/Lua approach.
package gdsrc

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/extract"
)

// Extractor is the pure-Go GDScript backend.
type Extractor struct{}

// New returns a GDScript extractor.
func New() *Extractor { return &Extractor{} }

// Language implements extract.Extractor.
func (*Extractor) Language() string { return "gdscript" }

var (
	// class_name Player extends Node2D
	classNameRe = regexp.MustCompile(`^class_name\s+([A-Z]\w*)`)
	// extends Node2D / extends "res://base.gd"
	extendsRe = regexp.MustCompile(`^extends\s+([A-Za-z]\w*|"[^"]+")`)
	// class InnerClass / class InnerClass extends Node
	innerClassRe = regexp.MustCompile(`^class\s+([A-Z]\w*)`)
	// func foo() / func _ready() -> void / func bar(x: int, y = 5) -> int
	funcRe = regexp.MustCompile(`^func\s+([A-Za-z_]\w*)`)
	// static func foo() / static var x
	staticRe = regexp.MustCompile(`^static\s+(func|var)\s+`)
	// var hp: int = 100 / var speed := 50.0 / @export var x
	varRe = regexp.MustCompile(`^(?:@\w+\s+)*var\s+([A-Za-z_]\w*)`)
	// const SPEED = 200 / const MAX_HP: int = 100
	constRe = regexp.MustCompile(`^const\s+([A-Z_]\w*)`)
	// signal died / signal hurt(old_hp, new_hp) / signal changed(value: int)
	signalRe = regexp.MustCompile(`^signal\s+([A-Za-z_]\w*)`)
	// enum State {IDLE, RUN} / enum {ONE = 1, TWO}
	enumRe = regexp.MustCompile(`^enum\s+([A-Z]\w*)?\s*\{`)

	// preload("res://player.gd") / load("res://scene.tscn")
	preloadRe = regexp.MustCompile(`\b(preload|load)\s*\(\s*"([^"]+)"\s*\)`)
	// name-based call references: foo() / obj.method() / $Node.call()
	// Matches: identifier followed by open paren, optionally preceded by . or $
	callRe = regexp.MustCompile(`([.$]?)\s*\b([A-Za-z_]\w*)\s*\(`)
	// Class.new() / Node2D.new()
	newRe = regexp.MustCompile(`\b([A-Z]\w*)\.new\b`)
)

// gdKeywords never resolve to project methods; excluding them keeps the
// name graph from linking `if`/`return`/`yield` sites.
var gdKeywords = map[string]bool{
	"if": true, "elif": true, "else": true, "for": true, "while": true,
	"match": true, "when": true, "break": true, "continue": true, "pass": true,
	"return": true, "class": true, "class_name": true, "extends": true,
	"func": true, "signal": true, "var": true, "const": true, "enum": true,
	"static": true, "export": true, "onready": true, "setget": true,
	"breakpoint": true, "preload": true, "load": true, "self": true, "super": true,
	"as": true, "in": true, "is": true, "and": true, "or": true, "not": true,
	"await": true, "yield": true, "assert": true, "void": true, "null": true,
	"true": true, "false": true, "PI": true, "TAU": true, "INF": true, "NAN": true,
}

type frame struct {
	kind      string // "class", "func", "enum"
	name      string
	fqn       string
	indent    int
	startLine int
	symIndex  int // index into result symbols, -1 for anonymous frames
}

// ExtractFile implements extract.Extractor.
func (*Extractor) ExtractFile(relPath string, src []byte) (*extract.FileResult, error) {
	res := &extract.FileResult{Path: relPath, Language: "gdscript"}
	lines := strings.Split(string(src), "\n")
	isTest := isTestPath(relPath)

	var stack []frame
	var pendingDoc []string
	var fileClass string // class_name declaration at file scope
	inMultilineString := false
	stringDelim := ""

	closeFrame := func(f frame, endLine int) {
		if f.symIndex >= 0 {
			res.Symbols[f.symIndex].EndLine = endLine
			res.Symbols[f.symIndex].Source = lineSlice(lines, f.startLine, endLine)
		}
	}
	containerFQN := func() string {
		for i := len(stack) - 1; i >= 0; i-- {
			if stack[i].fqn != "" {
				return stack[i].fqn
			}
		}
		return fileClass
	}
	insideFunc := func() *frame {
		for i := len(stack) - 1; i >= 0; i-- {
			if stack[i].kind == "func" {
				return &stack[i]
			}
		}
		return nil
	}

	for i, raw := range lines {
		lineNo := i + 1
		
		// Detect multiline strings: """ or '''
		if !inMultilineString {
			if strings.Contains(raw, `"""`) || strings.Contains(raw, "'''") {
				if strings.Count(raw, `"""`) == 1 {
					inMultilineString = true
					stringDelim = `"""`
				} else if strings.Count(raw, "'''") == 1 {
					inMultilineString = true
					stringDelim = "'''"
				}
			}
		} else {
			if strings.Contains(raw, stringDelim) {
				inMultilineString = false
				stringDelim = ""
			}
			continue
		}
		
		code := stripComment(raw)
		trimmed := strings.TrimSpace(code)
		if trimmed == "" {
			if strings.HasPrefix(strings.TrimSpace(raw), "#") {
				pendingDoc = append(pendingDoc, strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "#")))
			} else {
				pendingDoc = nil
			}
			continue
		}
		indent := indentOf(raw)

		doc := strings.Join(pendingDoc, " ")
		pendingDoc = nil

		// Close frames that have ended (dedented declaration)
		if isDecl(trimmed) {
			for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
				closeFrame(stack[len(stack)-1], lineNo-1)
				stack = stack[:len(stack)-1]
			}
		}

		// File-level class_name
		if len(stack) == 0 {
			if m := classNameRe.FindStringSubmatch(trimmed); m != nil {
				fileClass = m[1]
				res.Symbols = append(res.Symbols, extract.Symbol{
					Name: m[1], FQN: m[1], Kind: extract.KindClass, Language: "gdscript",
					StartLine: lineNo, EndLine: lineNo, Signature: trimmed, Docstring: doc,
				})
				continue
			}
			if m := extendsRe.FindStringSubmatch(trimmed); m != nil {
				// extends is an import-like relationship
				base := strings.Trim(m[1], `"`)
				if !strings.HasPrefix(base, "res://") {
					res.Imports = append(res.Imports, base)
				}
				continue
			}
		}

		// preload/load imports
		for _, m := range preloadRe.FindAllStringSubmatch(code, -1) {
			spec := m[2]
			// res:// paths are project-relative
			if strings.HasPrefix(spec, "res://") {
				spec = strings.TrimPrefix(spec, "res://")
			}
			res.Imports = append(res.Imports, spec)
		}

		switch {
		case innerClassRe.MatchString(trimmed):
			m := innerClassRe.FindStringSubmatch(trimmed)
			name := m[1]
			fqn := joinFQN(containerFQN(), name)
			res.Symbols = append(res.Symbols, extract.Symbol{
				Name: name, FQN: fqn, Kind: extract.KindClass, Language: "gdscript",
				StartLine: lineNo, EndLine: lineNo, Signature: trimmed, Docstring: doc,
			})
			stack = append(stack, frame{kind: "class", name: name, fqn: fqn, indent: indent, startLine: lineNo, symIndex: len(res.Symbols) - 1})
			continue

		case enumRe.MatchString(trimmed):
			m := enumRe.FindStringSubmatch(trimmed)
			name := m[1]
			if name == "" {
				name = "Enum" // anonymous enum
			}
			fqn := joinFQN(containerFQN(), name)
			res.Symbols = append(res.Symbols, extract.Symbol{
				Name: name, FQN: fqn, Kind: extract.KindType, Language: "gdscript",
				StartLine: lineNo, EndLine: lineNo, Signature: trimmed, Docstring: doc,
			})
			// Enums are single-line or end with }; don't track as a frame
			continue

		case funcRe.MatchString(trimmed):
			m := funcRe.FindStringSubmatch(trimmed)
			name := m[1]
			fqn := joinFQN(containerFQN(), name)
			kind := extract.KindFunction
			if len(stack) > 0 || containerFQN() != "" {
				kind = extract.KindMethod
			}
			if isTest {
				kind = extract.KindTest
			}
			res.Symbols = append(res.Symbols, extract.Symbol{
				Name: name, FQN: fqn, Kind: kind, Language: "gdscript",
				StartLine: lineNo, EndLine: lineNo, Signature: trimmed, Docstring: doc,
			})
			// Single-line func: func foo(): return 42
			if strings.Contains(trimmed, ":") && !strings.HasSuffix(trimmed, ":") {
				afterColon := strings.SplitN(trimmed, ":", 2)[1]
				if strings.TrimSpace(afterColon) != "" {
					res.Symbols[len(res.Symbols)-1].Source = raw
					collectRefs(res, fqn, afterColon, lineNo)
				} else {
					stack = append(stack, frame{kind: "func", name: name, fqn: fqn, indent: indent, startLine: lineNo, symIndex: len(res.Symbols) - 1})
				}
			} else {
				stack = append(stack, frame{kind: "func", name: name, fqn: fqn, indent: indent, startLine: lineNo, symIndex: len(res.Symbols) - 1})
			}
			continue

		case varRe.MatchString(trimmed):
			m := varRe.FindStringSubmatch(trimmed)
			name := m[1]
			fqn := joinFQN(containerFQN(), name)
			// Only record top-level vars and class members, not local variables
			if insideFunc() == nil {
				res.Symbols = append(res.Symbols, extract.Symbol{
					Name: name, FQN: fqn, Kind: extract.KindVariable, Language: "gdscript",
					StartLine: lineNo, EndLine: lineNo, Signature: trimmed, Docstring: doc,
				})
			}
			// Don't continue - collect refs from the initializer
			// Fall through to collectRefs

		case constRe.MatchString(trimmed):
			m := constRe.FindStringSubmatch(trimmed)
			name := m[1]
			fqn := joinFQN(containerFQN(), name)
			res.Symbols = append(res.Symbols, extract.Symbol{
				Name: name, FQN: fqn, Kind: extract.KindVariable, Language: "gdscript",
				StartLine: lineNo, EndLine: lineNo, Signature: trimmed, Docstring: doc,
			})
			// Fall through to collectRefs

		case signalRe.MatchString(trimmed):
			m := signalRe.FindStringSubmatch(trimmed)
			name := m[1]
			fqn := joinFQN(containerFQN(), name)
			res.Symbols = append(res.Symbols, extract.Symbol{
				Name: name, FQN: fqn, Kind: extract.KindVariable, Language: "gdscript",
				StartLine: lineNo, EndLine: lineNo, Signature: trimmed, Docstring: doc,
			})
			// Signals don't have initializers, continue
			continue
		}

		// Body line: attribute references to the innermost func, else the
		// enclosing class, else the file. Collect on all non-declaration lines.
		from := relPath
		if d := insideFunc(); d != nil {
			from = d.fqn
		} else if c := containerFQN(); c != "" {
			from = c
		}
		collectRefs(res, from, trimmed, lineNo)
	}
	for len(stack) > 0 {
		closeFrame(stack[len(stack)-1], len(lines))
		stack = stack[:len(stack)-1]
	}
	return res, nil
}

// collectRefs appends call/instantiation references found in one code line.
func collectRefs(res *extract.FileResult, from, code string, lineNo int) {
	seen := map[string]bool{}
	add := func(to string, kind string, qualified bool) {
		if to == "" || gdKeywords[to] || seen[kind+to] || to == from {
			return
		}
		seen[kind+to] = true
		res.References = append(res.References, extract.Reference{
			From: from, To: to, Kind: kind, Line: lineNo, Qualified: qualified,
		})
	}
	for _, m := range callRe.FindAllStringSubmatch(code, -1) {
		add(m[2], extract.RefCalls, m[1] != "")
	}
	for _, m := range newRe.FindAllStringSubmatch(code, -1) {
		add(m[1], extract.RefReferences, true)
	}
}

// stripComment removes a trailing # comment, respecting single/double quotes.
func stripComment(line string) string {
	var quote byte
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '#':
			return line[:i]
		}
	}
	return line
}

func indentOf(line string) int {
	n := 0
	for _, r := range line {
		switch r {
		case ' ':
			n++
		case '\t':
			n += 4 // GDScript convention: tab = 4 spaces
		default:
			return n
		}
	}
	return n
}

func isDecl(trimmed string) bool {
	return classNameRe.MatchString(trimmed) || innerClassRe.MatchString(trimmed) ||
		funcRe.MatchString(trimmed) || enumRe.MatchString(trimmed) ||
		staticRe.MatchString(trimmed)
}

func joinFQN(container, name string) string {
	if container == "" {
		return name
	}
	return container + "." + name
}

// isTestPath mirrors GDScript test conventions: *_test.gd, test_*.gd.
func isTestPath(relPath string) bool {
	base := strings.ToLower(filepath.Base(relPath))
	return strings.HasSuffix(base, "_test.gd") ||
		(strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".gd"))
}

func lineSlice(lines []string, start, end int) string {
	if start < 1 {
		start = 1
	}
	if end > len(lines) {
		end = len(lines)
	}
	if start > end {
		return ""
	}
	return strings.Join(lines[start-1:end], "\n")
}
