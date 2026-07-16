// Package luasrc extracts structural symbols and name-based references from
// Lua source with a pure-Go line scanner (no CGO, no language server) — the
// same T1 fidelity as the other name-based backends: definition nodes for
// functions (including the dominant module pattern of functions assigned to
// table fields: `function M.foo()`, `M.foo = function()`), call references
// resolved by name by the indexer, and require() imports.
//
// Block tracking counts Lua's `end`-terminated openers (function/if/do,
// with repeat/until as its own pair) per line, outside comments and strings.
// That closes each declaration at its real `end` for conventionally
// formatted code and degrades to slightly-off EndLines on pathological
// layouts — never dropping symbols.
package luasrc

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/extract"
)

// Extractor is the pure-Go Lua backend.
type Extractor struct{}

// New returns a Lua extractor.
func New() *Extractor { return &Extractor{} }

// Language implements extract.Extractor.
func (*Extractor) Language() string { return "lua" }

var (
	// function Name(...) / function M.foo(...) / function M:foo(...)
	funcDeclRe = regexp.MustCompile(`^(local\s+)?function\s+([A-Za-z_]\w*(?:[.:][A-Za-z_]\w*)*)\s*\(`)
	// M.foo = function(...) / local foo = function(...) / foo = function(...)
	funcAssignRe = regexp.MustCompile(`^(?:local\s+)?([A-Za-z_]\w*(?:\.[A-Za-z_]\w*)*)\s*=\s*function\s*\(`)
	requireRe    = regexp.MustCompile(`\brequire\s*\(?\s*['"]([^'"]+)['"]`)
	callRe       = regexp.MustCompile(`([.:]?)\s*\b([A-Za-z_]\w*)\s*[({"']`)
)

var luaKeywords = map[string]bool{
	"and": true, "break": true, "do": true, "else": true, "elseif": true,
	"end": true, "false": true, "for": true, "function": true, "goto": true,
	"if": true, "in": true, "local": true, "nil": true, "not": true, "or": true,
	"repeat": true, "return": true, "then": true, "true": true, "until": true,
	"while": true, "require": true, "pcall": true, "ipairs": true, "pairs": true,
	"type": true, "print": true, "error": true, "assert": true, "tostring": true,
	"tonumber": true, "select": true, "setmetatable": true, "rawget": true, "rawset": true,
}

type luaFrame struct {
	fqn       string
	depth     int // block depth BEFORE the declaration opened
	startLine int
	symIndex  int
}

// ExtractFile implements extract.Extractor.
func (*Extractor) ExtractFile(relPath string, src []byte) (*extract.FileResult, error) {
	res := &extract.FileResult{Path: relPath, Language: "lua"}
	lines := strings.Split(string(src), "\n")
	isTest := isTestPath(relPath)

	var stack []luaFrame
	depth := 0
	inBlockComment := false
	var pendingDoc []string

	closeTop := func(endLine int) {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if f.symIndex >= 0 {
			res.Symbols[f.symIndex].EndLine = endLine
			res.Symbols[f.symIndex].Source = strings.Join(lines[f.startLine-1:endLine], "\n")
		}
	}

	for i, raw := range lines {
		lineNo := i + 1
		rawTrim := strings.TrimSpace(raw)
		wasLineComment := !inBlockComment && strings.HasPrefix(rawTrim, "--") && !strings.HasPrefix(rawTrim, "--[[")
		code, noComment, stillIn := stripLua(raw, inBlockComment)
		inBlockComment = stillIn
		trimmed := strings.TrimSpace(code)
		if trimmed == "" {
			if wasLineComment {
				pendingDoc = append(pendingDoc, strings.TrimSpace(strings.TrimPrefix(rawTrim, "--")))
			} else {
				pendingDoc = nil
			}
			continue
		}

		doc := strings.Join(pendingDoc, " ")
		pendingDoc = nil

		// requires are matched on the comment-stripped noComment view: the
		// specifier lives inside a string (which `code` blanks), while a
		// require inside a trailing comment must not become an import.
		for _, m := range requireRe.FindAllStringSubmatch(noComment, -1) {
			res.Imports = append(res.Imports, m[1])
		}

		// Declaration on this line?
		declared := false
		var name string
		if m := funcDeclRe.FindStringSubmatch(trimmed); m != nil {
			name = m[2]
			declared = true
		} else if m := funcAssignRe.FindStringSubmatch(trimmed); m != nil {
			name = m[1]
			declared = true
		}
		if declared {
			isMethod := strings.ContainsAny(name, ":.")
			fqn := strings.ReplaceAll(name, ":", ".")
			kind := extract.KindFunction
			if strings.Contains(name, ":") {
				kind = extract.KindMethod
			}
			if isTest {
				kind = extract.KindTest
			}
			res.Symbols = append(res.Symbols, extract.Symbol{
				Name: lastSegment(fqn), FQN: fqn, Kind: kind, Language: "lua",
				StartLine: lineNo, EndLine: lineNo, Signature: firstLine(trimmed),
				Docstring: doc,
			})
			opens, closes := countBlocks(trimmed)
			if opens > closes { // multi-line body: track until its end
				stack = append(stack, luaFrame{fqn: fqn, depth: depth, startLine: lineNo, symIndex: len(res.Symbols) - 1})
			} else {
				res.Symbols[len(res.Symbols)-1].Source = raw
			}
			depth += opens - closes
			_ = isMethod
			// refs on the same line (a one-line body)
			collectLuaRefs(res, fqn, afterFunctionKeyword(trimmed), lineNo)
			continue
		}

		// Attribute references to the innermost tracked function, else the file.
		from := relPath
		if len(stack) > 0 {
			from = stack[len(stack)-1].fqn
		}
		collectLuaRefs(res, from, code, lineNo)

		opens, closes := countBlocks(trimmed)
		depth += opens
		for c := 0; c < closes; c++ {
			depth--
			for len(stack) > 0 && depth <= stack[len(stack)-1].depth {
				closeTop(lineNo)
			}
		}
	}
	for len(stack) > 0 {
		closeTop(len(lines))
	}
	return res, nil
}

// afterFunctionKeyword returns the tail of a declaration line past the
// opening parenthesis, so the declared function itself isn't collected as a
// callee of its own body.
func afterFunctionKeyword(trimmed string) string {
	if i := strings.IndexByte(trimmed, '('); i >= 0 {
		return trimmed[i+1:]
	}
	return ""
}

// collectLuaRefs appends call references found in one code line: bare calls
// (`helper(x)`), table-field calls (`M.helper(x)` — qualified), and method
// calls (`obj:save()` — qualified). Lua also permits string/table single
// arguments without parens (`require "x"`, `f{1}`, `f"lit"`), which callRe's
// terminator class covers.
func collectLuaRefs(res *extract.FileResult, from, code string, lineNo int) {
	seen := map[string]bool{}
	for _, m := range callRe.FindAllStringSubmatch(code, -1) {
		name := m[2]
		if name == "" || luaKeywords[name] || seen[name] || name == from {
			continue
		}
		seen[name] = true
		res.References = append(res.References, extract.Reference{
			From: from, To: name, Kind: extract.RefCalls, Line: lineNo,
			Qualified: m[1] != "",
		})
	}
}

// countBlocks counts `end`-consuming openers and `end`/`until` closers in a
// comment/string-stripped line. Openers: function, if (elseif does not nest),
// do (for/while headers end in `do`, so counting `do` alone avoids
// double-counting them), repeat.
func countBlocks(code string) (opens, closes int) {
	for _, tok := range tokenizeWords(code) {
		switch tok {
		case "function", "if", "do", "repeat":
			opens++
		case "end", "until":
			closes++
		}
	}
	return opens, closes
}

var wordRe = regexp.MustCompile(`[A-Za-z_]\w*`)

func tokenizeWords(code string) []string {
	return wordRe.FindAllString(code, -1)
}

// stripLua removes comments and string contents from a line, returning the
// structural code view plus a noComment view (comments removed, string
// contents KEPT — require() specifiers live inside strings). inBlock carries
// multi-line [[...]] state, which covers BOTH --[[ ]] long comments and
// [[ ]] long strings: they share the `]]` closer, and content of either must
// never produce symbols, references, or block counts.
func stripLua(line string, inBlock bool) (code, noComment string, stillIn bool) {
	var b, nc strings.Builder
	i := 0
	for i < len(line) {
		if inBlock {
			if j := strings.Index(line[i:], "]]"); j >= 0 {
				i += j + 2
				inBlock = false
				continue
			}
			return b.String(), nc.String(), true
		}
		c := line[i]
		switch {
		case c == '-' && i+1 < len(line) && line[i+1] == '-':
			if strings.HasPrefix(line[i+2:], "[[") {
				inBlock = true
				i += 4
				continue
			}
			return b.String(), nc.String(), false // line comment
		case c == '\'' || c == '"':
			quote := c
			b.WriteByte('"') // keep a delimiter so `f"lit"` still reads as a call
			nc.WriteByte(quote)
			i++
			for i < len(line) {
				if line[i] == '\\' {
					if i+1 < len(line) {
						nc.WriteString(line[i : i+2])
					}
					i += 2
					continue
				}
				if line[i] == quote {
					i++
					break
				}
				nc.WriteByte(line[i])
				i++
			}
			b.WriteByte('"')
			nc.WriteByte(quote)
		case c == '[' && i+1 < len(line) && line[i+1] == '[':
			// long string: blank to its close, or carry the in-[[ ]] state so
			// following lines are content, not code.
			if j := strings.Index(line[i+2:], "]]"); j >= 0 {
				i += j + 4
				b.WriteString(`""`)
				nc.WriteString(`""`)
			} else {
				return b.String(), nc.String(), true
			}
		default:
			b.WriteByte(c)
			nc.WriteByte(c)
			i++
		}
	}
	return b.String(), nc.String(), inBlock
}

func lastSegment(name string) string {
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		return name[i+1:]
	}
	return name
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

// isTestPath mirrors Lua/busted conventions: *_spec.lua, *_test.lua, test_*.lua.
func isTestPath(relPath string) bool {
	base := strings.ToLower(filepath.Base(relPath))
	return strings.HasSuffix(base, "_spec.lua") || strings.HasSuffix(base, "_test.lua") ||
		(strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".lua"))
}
