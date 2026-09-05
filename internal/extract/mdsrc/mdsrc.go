// Package mdsrc extracts CommonMark sections and local links. Code fences are
// documentation examples, never executable definitions or call edges.
package mdsrc

import (
	"bytes"
	"net/url"
	"path"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/extract"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

type Extractor struct{}

func New() *Extractor               { return &Extractor{} }
func (*Extractor) Language() string { return "markdown" }

func (*Extractor) ExtractFile(file string, src []byte) (*extract.FileResult, error) {
	res := &extract.FileResult{Path: file, Language: "markdown"}
	parseSource := bytes.Clone(src)
	// Front matter belongs to the document metadata, not a Setext heading.
	if bytes.HasPrefix(parseSource, []byte("---\n")) || bytes.HasPrefix(parseSource, []byte("---\r\n")) {
		pos := bytes.IndexByte(parseSource, '\n') + 1
		for pos < len(parseSource) {
			end := bytes.IndexByte(parseSource[pos:], '\n')
			if end < 0 {
				end = len(parseSource) - pos
			}
			line := strings.TrimSpace(string(parseSource[pos : pos+end]))
			if line == "---" || line == "..." {
				for i := 0; i < pos+end; i++ {
					if parseSource[i] != '\n' && parseSource[i] != '\r' {
						parseSource[i] = ' '
					}
				}
				break
			}
			pos += end + 1
		}
	}
	md := goldmark.New(goldmark.WithParserOptions(parser.WithAutoHeadingID()))
	doc := md.Parser().Parse(text.NewReader(parseSource))
	lines := strings.Split(string(src), "\n")
	type section struct{ index, level int }
	var stack []section
	closeSection := func(index, end int) {
		s := &res.Symbols[index]
		if end < s.StartLine {
			end = s.StartLine
		}
		s.EndLine = end
		s.Source = strings.Join(lines[s.StartLine-1:end], "\n")
	}
	err := ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if h, ok := n.(*ast.Heading); ok {
			// CommonMark accepts an empty '#' heading without a source segment.
			if h.Lines().Len() == 0 {
				return ast.WalkContinue, nil
			}
			line := 1 + bytes.Count(src[:h.Lines().At(0).Start], []byte("\n"))
			for len(stack) > 0 && stack[len(stack)-1].level >= h.Level {
				s := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				closeSection(s.index, line-1)
			}
			var title strings.Builder
			_ = ast.Walk(h, func(c ast.Node, enter bool) (ast.WalkStatus, error) {
				if enter {
					switch t := c.(type) {
					case *ast.Text:
						title.Write(t.Value(src))
					case *ast.String:
						title.Write(t.Value)
					}
				}
				return ast.WalkContinue, nil
			})
			id, _ := h.AttributeString("id")
			anchor, _ := id.([]byte)
			res.Symbols = append(res.Symbols, extract.Symbol{Name: title.String(), FQN: file + "#section/" + string(anchor), Kind: extract.KindSection, Language: "markdown", StartLine: line, Signature: title.String()})
			stack = append(stack, section{len(res.Symbols) - 1, h.Level})
		}
		if link, ok := n.(*ast.Link); ok {
			u, err := url.Parse(string(link.Destination))
			if err != nil || u.IsAbs() || u.Host != "" || u.Opaque != "" {
				return ast.WalkContinue, nil
			}
			target := file
			if u.Path != "" {
				if strings.HasPrefix(u.Path, "/") {
					target = path.Clean(strings.TrimPrefix(u.Path, "/"))
				} else {
					target = path.Clean(path.Join(path.Dir(file), u.Path))
				}
			}
			if target == ".." || strings.HasPrefix(target, "../") || strings.ContainsAny(target, "\\\x00") {
				return ast.WalkContinue, nil
			}
			if u.Fragment != "" && (target == file || strings.HasSuffix(target, ".md") || strings.HasSuffix(target, ".markdown")) {
				target += "#section/" + u.Fragment
			}
			from, line := file, 1
			if len(stack) > 0 {
				s := res.Symbols[stack[len(stack)-1].index]
				from, line = s.FQN, s.StartLine
			}
			res.References = append(res.References, extract.Reference{From: from, ToFQN: target, Kind: extract.RefDocuments, Line: line})
		}
		return ast.WalkContinue, nil
	})
	if err != nil {
		return nil, err
	}
	for _, s := range stack {
		closeSection(s.index, len(lines))
	}
	if len(res.Symbols) == 0 {
		res.Symbols = append(res.Symbols, extract.Symbol{Name: path.Base(file), FQN: file + "#section/document", Kind: extract.KindSection, Language: "markdown", StartLine: 1, EndLine: len(lines), Signature: path.Base(file), Source: string(src)})
	}
	return res, nil
}
