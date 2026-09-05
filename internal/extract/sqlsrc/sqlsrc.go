// Package sqlsrc extracts SQL declarations and query/table dependencies offline.
// It is a lexical, dialect-conservative backend, not a database schema evaluator.
package sqlsrc

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/abdul-hamid-achik/codemap/internal/extract"
)

type Extractor struct{}

func New() *Extractor               { return &Extractor{} }
func (*Extractor) Language() string { return "sql" }

type token struct {
	text             string
	start, end, line int
	ident            bool
	quoted           bool
	annotation       bool
}

func (t token) is(s string) bool { return !t.quoted && strings.EqualFold(t.text, s) }

var annotation = regexp.MustCompile(`(?m)^[\t ]*--[\t ]*name:[\t ]*([A-Za-z_][A-Za-z_0-9]*)[\t ]+:[a-z]+[\t ]*\r?$`)

func (*Extractor) ExtractFile(file string, src []byte) (*extract.FileResult, error) {
	file = filepath.ToSlash(file)
	res := &extract.FileResult{Path: file, Language: "sql"}
	tokens, err := lex(src)
	if err != nil {
		return nil, err
	}
	occurrences := map[string]int{}
	for len(tokens) > 0 {
		var named *token
		if tokens[0].annotation {
			t := tokens[0]
			named = &t
			tokens = tokens[1:]
		}
		end := 0
		for end < len(tokens) && !tokens[end].is(";") {
			end++
		}
		stmt := tokens[:end]
		consumed := end
		statementEnd := 0
		if end < len(tokens) {
			statementEnd = tokens[end].end
			consumed++
		}
		tokens = tokens[consumed:]
		if len(stmt) == 0 {
			continue
		}
		first, last := stmt[0], stmt[len(stmt)-1]
		kind, name := declaration(stmt)
		start, startLine := first.start, first.line
		if named != nil {
			name = named.text
			kind = extract.KindQuery
			start = named.start
			startLine = named.line
		}
		stop := last.end
		if statementEnd > 0 {
			stop = statementEnd
		}
		if kind == "" {
			if !first.is("select") && !first.is("with") && !first.is("insert") && !first.is("update") && !first.is("delete") && !first.is("alter") && !first.is("drop") {
				continue
			}
			kind = extract.KindQuery
			// Anonymous statement identity survives line shifts. Content changes
			// deliberately make a new definition; sqlc names remain stable.
			name = fmt.Sprintf("%s_%x", strings.ToLower(first.text), sha256.Sum256(src[first.start:last.end]))[:20]
		}
		key := kind + "/" + name
		occurrences[key]++
		fqn := file + "#sql/" + key
		if occurrences[key] > 1 {
			fqn += fmt.Sprintf("/%d", occurrences[key])
		}
		res.Symbols = append(res.Symbols, extract.Symbol{
			Name: name, FQN: fqn, Kind: kind, Language: "sql", StartLine: startLine, EndLine: 1 + strings.Count(string(src[:last.end]), "\n"),
			Signature: strings.TrimSpace(strings.SplitN(string(src[first.start:last.end]), "\n", 2)[0]), Source: string(src[start:stop]),
		})
		res.References = append(res.References, relations(stmt, fqn)...)
	}
	return res, nil
}

func declaration(ts []token) (string, string) {
	if len(ts) < 3 || !ts[0].is("create") {
		return "", ""
	}
	for i := 1; i < len(ts)-1 && i < 6; i++ {
		kind := ""
		if ts[i].is("table") {
			kind = extract.KindTable
		}
		if ts[i].is("view") {
			kind = extract.KindView
		}
		if kind == "" {
			continue
		}
		j := i + 1
		if j+2 < len(ts) && ts[j].is("if") && ts[j+1].is("not") && ts[j+2].is("exists") {
			j += 3
		}
		name, _ := identifier(ts, j)
		if name == "" {
			return "", ""
		}
		return kind, name
	}
	return "", ""
}

func identifier(ts []token, i int) (string, int) {
	if i >= len(ts) || !ts[i].ident {
		return "", i
	}
	name := ts[i].text
	i++
	for i+1 < len(ts) && ts[i].text == "." && ts[i+1].ident {
		name += "." + ts[i+1].text
		i += 2
	}
	return name, i
}

func relations(ts []token, from string) []extract.Reference {
	var refs []extract.Reference
	ctes, seen := map[string]bool{}, map[string]bool{}
	// CTE aliases are query-local, not schema tables. Treat alias-looking
	// declarations conservatively, including recursive and column-list CTEs.
	if ts[0].is("with") {
		for i := 1; i < len(ts)-1; i++ {
			if !ts[i].ident {
				continue
			}
			j := i + 1
			if ts[j].text == "(" {
				depth := 1
				j++
				for j < len(ts) && depth > 0 {
					if ts[j].text == "(" {
						depth++
					}
					if ts[j].text == ")" {
						depth--
					}
					j++
				}
			}
			if j < len(ts) && ts[j].is("as") {
				ctes[ts[i].text] = true
			}
		}
	}
	for i, t := range ts {
		kind, j := "", i+1
		switch {
		case t.is("from"), t.is("join"), t.is("references"):
			kind = extract.RefReads
			if t.is("from") && i > 0 && ts[i-1].is("delete") {
				kind = extract.RefWrites
			}
		case t.is("into"):
			if i > 0 && (ts[i-1].is("insert") || ts[i-1].is("replace")) {
				kind = extract.RefWrites
			}
		case t.is("update"):
			// ON CONFLICT DO UPDATE does not introduce a table.
			if i == 0 || !ts[i-1].is("do") {
				kind = extract.RefWrites
			}
		case t.is("table"):
			if i > 0 && (ts[i-1].is("alter") || ts[i-1].is("drop")) {
				kind = extract.RefWrites
			}
		}
		if kind == "" {
			continue
		}
		if j < len(ts) && ts[j].is("only") {
			j++
		}
		if j+1 < len(ts) && ts[j].is("if") && ts[j+1].is("exists") {
			j += 2
		}
		name, after := identifier(ts, j)
		if name == "" || ctes[name] || (after < len(ts) && ts[after].text == "(" && !t.is("references") && !t.is("into")) {
			continue
		}
		key := kind + "/" + name
		if seen[key] {
			continue
		}
		seen[key] = true
		refs = append(refs, extract.Reference{From: from, To: name, Kind: kind, Line: t.line, Qualified: true, ToKinds: []string{extract.KindTable, extract.KindView}})
	}
	return refs
}

// lex hides comments/literals before recognizing keywords; semicolons inside
// strings or PostgreSQL dollar bodies never split a statement.
func lex(src []byte) ([]token, error) {
	var out []token
	line := 1
	for i := 0; i < len(src); {
		c := src[i]
		if c == '\n' {
			line++
			i++
			continue
		}
		if c == ' ' || c == '\t' || c == '\r' {
			i++
			continue
		}
		if i+1 < len(src) && string(src[i:i+2]) == "--" {
			start := i
			for i < len(src) && src[i] != '\n' {
				i++
			}
			if m := annotation.FindSubmatch(src[start:i]); len(m) > 1 {
				out = append(out, token{text: string(m[1]), start: start, end: i, line: line, annotation: true})
			}
			continue
		}
		if i+1 < len(src) && string(src[i:i+2]) == "/*" {
			depth := 1
			i += 2
			for i < len(src) && depth > 0 {
				if i+1 < len(src) && string(src[i:i+2]) == "/*" {
					depth++
					i += 2
				} else if i+1 < len(src) && string(src[i:i+2]) == "*/" {
					depth--
					i += 2
				} else {
					if src[i] == '\n' {
						line++
					}
					i++
				}
			}
			if depth > 0 {
				return nil, fmt.Errorf("unterminated SQL comment")
			}
			continue
		}
		start, ln := i, line
		if c == '\'' || c == '"' || c == '`' || c == '[' {
			close := c
			if c == '[' {
				close = ']'
			}
			i++
			var text strings.Builder
			closed := false
			for i < len(src) {
				if src[i] == close {
					if i+1 < len(src) && src[i+1] == close {
						text.WriteByte(close)
						i += 2
						continue
					}
					i++
					closed = true
					break
				}
				if src[i] == '\n' {
					line++
				}
				text.WriteByte(src[i])
				i++
			}
			if !closed {
				return nil, fmt.Errorf("unterminated SQL quoted token at line %d", ln)
			}
			out = append(out, token{text: text.String(), start: start, end: i, line: ln, ident: c != '\'', quoted: true})
			continue
		}
		if c == '$' {
			j := i + 1
			for j < len(src) && (src[j] == '_' || src[j] >= 'a' && src[j] <= 'z' || src[j] >= 'A' && src[j] <= 'Z' || src[j] >= '0' && src[j] <= '9') {
				j++
			}
			if j < len(src) && src[j] == '$' {
				delim := string(src[i : j+1])
				stop := strings.Index(string(src[j+1:]), delim)
				if stop < 0 {
					return nil, fmt.Errorf("unterminated SQL dollar body at line %d", ln)
				}
				i = j + 1 + stop + len(delim)
				line += strings.Count(string(src[start:i]), "\n")
				out = append(out, token{start: start, end: i, line: ln, quoted: true})
				continue
			}
		}
		if unicode.IsLetter(rune(c)) || c == '_' || c >= 128 {
			i++
			for i < len(src) && (src[i] == '_' || src[i] == '$' || src[i] >= 128 || src[i] >= 'a' && src[i] <= 'z' || src[i] >= 'A' && src[i] <= 'Z' || src[i] >= '0' && src[i] <= '9') {
				i++
			}
			out = append(out, token{text: strings.ToLower(string(src[start:i])), start: start, end: i, line: ln, ident: true})
			continue
		}
		i++
		out = append(out, token{text: string(c), start: start, end: i, line: ln})
	}
	return out, nil
}
