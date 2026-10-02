package app

import (
	"strings"
	"unicode/utf8"
)

// Text helpers for codemap flow: comment/string masking so call-site scans do
// not match prose, a bounded call-site finder that recovers source order for
// callees (edges carry no call-site lines), and one-sentence doc extraction.

type flowSyntax struct {
	lineComments []string
	block        bool   // /* ... */
	quotes       string // single-line string delimiters
	backtick     bool   // multi-line raw strings (Go)
	triple       bool   // Python triple-quoted strings
}

func flowSyntaxFor(language string) flowSyntax {
	switch strings.ToLower(language) {
	case "python":
		return flowSyntax{lineComments: []string{"#"}, quotes: `"'`, triple: true}
	case "ruby", "yaml", "gdscript":
		return flowSyntax{lineComments: []string{"#"}, quotes: `"'`}
	case "lua":
		return flowSyntax{lineComments: []string{"--"}, quotes: `"'`}
	case "go":
		return flowSyntax{lineComments: []string{"//"}, block: true, quotes: `"'`, backtick: true}
	default: // javascript, typescript, java, c-family, …
		return flowSyntax{lineComments: []string{"//"}, block: true, quotes: `"'`}
	}
}

// flowMask returns src with comment and string-literal contents replaced by
// spaces (newlines and byte offsets preserved), so identifier scans only see
// code. It is deliberately simple — no regex literals or template strings — and
// errs towards leaving text visible.
func flowMask(src string, syn flowSyntax) string {
	b := []byte(src)
	n := len(b)
	blank := func(from, to int) {
		for k := from; k < to && k < n; k++ {
			if b[k] != '\n' {
				b[k] = ' '
			}
		}
	}
	i := 0
	for i < n {
		c := b[i]
		switch {
		case syn.block && c == '/' && i+1 < n && b[i+1] == '*':
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				blank(i, n)
				return string(b)
			}
			blank(i, i+2+end+2)
			i += 2 + end + 2
			continue
		case flowHasLineComment(src, i, syn):
			end := strings.IndexByte(src[i:], '\n')
			if end < 0 {
				blank(i, n)
				return string(b)
			}
			blank(i, i+end)
			i += end
			continue
		case syn.triple && (c == '"' || c == '\'') && i+2 < n && b[i+1] == c && b[i+2] == c:
			end := strings.Index(src[i+3:], src[i:i+3])
			if end < 0 {
				blank(i+3, n)
				return string(b)
			}
			blank(i+3, i+3+end)
			i += 3 + end + 3
			continue
		case syn.backtick && c == '`':
			end := strings.IndexByte(src[i+1:], '`')
			if end < 0 {
				blank(i+1, n)
				return string(b)
			}
			blank(i+1, i+1+end)
			i += 1 + end + 1
			continue
		case strings.IndexByte(syn.quotes, c) >= 0:
			j := i + 1
			for j < n && b[j] != c && b[j] != '\n' {
				if b[j] == '\\' && j+1 < n && b[j+1] != '\n' {
					j++
				}
				j++
			}
			blank(i+1, j)
			if j < n && b[j] == c {
				j++
			}
			i = j
			continue
		}
		i++
	}
	return string(b)
}

func flowHasLineComment(src string, i int, syn flowSyntax) bool {
	for _, lc := range syn.lineComments {
		if strings.HasPrefix(src[i:], lc) {
			return true
		}
	}
	return false
}

// flowBodyStart returns the offset just past the declaration header of masked,
// so the callee's own signature (which can contain its parent's name, e.g. a
// method calling a same-named method on another type) never counts as a call
// site. Brace languages skip to the first '{' outside parentheses/brackets;
// others skip the first line.
func flowBodyStart(masked, language string) int {
	switch strings.ToLower(language) {
	case "python", "ruby", "lua", "yaml", "gdscript":
		if nl := strings.IndexByte(masked, '\n'); nl >= 0 {
			return nl + 1
		}
		return 0
	}
	depth := 0
	for i := 0; i < len(masked); i++ {
		switch masked[i] {
		case '(', '[':
			depth++
		case ')', ']':
			if depth > 0 {
				depth--
			}
		case '{':
			if depth == 0 {
				return i + 1
			}
		}
	}
	return 0
}

func flowIdentByte(c byte) bool {
	return c == '_' || c == '$' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= 0x80
}

// flowSite is one call-shaped occurrence of a name inside a parent's body.
type flowSite struct {
	off       int
	call      bool   // call-shaped (Name( or <Name), not a bare word match
	qualified bool   // preceded by '.'
	qual      string // identifier directly before the '.', "" for an expression receiver
}

// flowCallSites finds call-shaped occurrences of name in masked (from start):
// name followed by optional whitespace and '(' with a left word boundary, plus
// '<Name' JSX usage. Occurrences are returned in source order.
func flowCallSites(masked string, start int, name string, jsx bool) []flowSite {
	var sites []flowSite
	if name == "" {
		return nil
	}
	from := start
	for from < len(masked) {
		idx := strings.Index(masked[from:], name)
		if idx < 0 {
			break
		}
		i := from + idx
		from = i + 1
		end := i + len(name)
		if i > 0 && flowIdentByte(masked[i-1]) {
			continue
		}
		if end < len(masked) && flowIdentByte(masked[end]) {
			continue
		}
		j := end
		for j < len(masked) && (masked[j] == ' ' || masked[j] == '\t' || masked[j] == '\n' || masked[j] == '\r') {
			j++
		}
		var prev byte
		if i > 0 {
			prev = masked[i-1]
		}
		isCall := j < len(masked) && masked[j] == '('
		isJSX := jsx && prev == '<'
		if !isCall && !isJSX {
			continue
		}
		site := flowSite{off: i, call: true}
		if prev == '.' {
			site.qualified = true
			k := i - 1
			e := k
			for k > 0 && flowIdentByte(masked[k-1]) {
				k--
			}
			site.qual = masked[k:e]
		}
		sites = append(sites, site)
	}
	return sites
}

// flowWordSites finds word-boundary occurrences of name (not necessarily
// calls): used for value references such as a handler passed as a callback.
func flowWordSites(masked string, start int, name string) []flowSite {
	var sites []flowSite
	if name == "" {
		return nil
	}
	from := start
	for from < len(masked) {
		idx := strings.Index(masked[from:], name)
		if idx < 0 {
			break
		}
		i := from + idx
		from = i + 1
		end := i + len(name)
		if i > 0 && flowIdentByte(masked[i-1]) {
			continue
		}
		if end < len(masked) && flowIdentByte(masked[end]) {
			continue
		}
		site := flowSite{off: i}
		if i > 0 && masked[i-1] == '.' {
			site.qualified = true
			k := i - 1
			e := k
			for k > 0 && flowIdentByte(masked[k-1]) {
				k--
			}
			site.qual = masked[k:e]
		}
		sites = append(sites, site)
	}
	return sites
}

// flowDoc returns the first sentence of a docstring, comment markers removed,
// whitespace collapsed, capped at max runes.
func flowDoc(doc string, max int) string {
	doc = strings.TrimSpace(doc)
	if doc == "" {
		return ""
	}
	var para []string
	for _, line := range strings.Split(doc, "\n") {
		l := strings.TrimSpace(line)
		for _, p := range []string{"/**", "/*", "///", "//", `"""`, "'''", "# ", "* "} {
			l = strings.TrimSpace(strings.TrimPrefix(l, p))
		}
		l = strings.TrimSpace(strings.TrimSuffix(l, "*/"))
		if l == "" {
			if len(para) > 0 {
				break
			}
			continue
		}
		para = append(para, l)
	}
	text := strings.Join(para, " ")
	if text == "" {
		return ""
	}
	if end := flowSentenceEnd(text); end > 0 {
		text = text[:end]
	}
	if utf8.RuneCountInString(text) > max {
		r := []rune(text)
		text = strings.TrimSpace(string(r[:max-1])) + "…"
	}
	return text
}

// flowSentenceEnd returns the byte offset just past the first sentence's
// terminator, or 0 when the text has no sentence break.
func flowSentenceEnd(text string) int {
	for i := 0; i < len(text); i++ {
		c := text[i]
		if c != '.' && c != '!' && c != '?' {
			continue
		}
		if i+1 < len(text) && text[i+1] != ' ' {
			continue
		}
		head := strings.ToLower(text[:i+1])
		if strings.HasSuffix(head, "e.g.") || strings.HasSuffix(head, "i.e.") || strings.HasSuffix(head, " vs.") || strings.HasSuffix(head, " etc.") {
			continue
		}
		return i + 1
	}
	return 0
}
