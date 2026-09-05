// Package htmlsrc indexes static HTML styling and local script/stylesheet
// dependencies. The tokenizer keeps attributes inside comments and scripts out
// of the graph; embedded CSS reuses the stylesheet extractor with original lines.
package htmlsrc

import (
	"bytes"
	"io"
	"net/url"
	"path"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/extract"
	"github.com/abdul-hamid-achik/codemap/internal/extract/csssrc"
	"golang.org/x/net/html"
)

type Extractor struct{}

func New() *Extractor               { return &Extractor{} }
func (*Extractor) Language() string { return "html" }

var templateMarkers = []string{"{{", "{%", "<%", "${", "}}", "%}", "%>"}

func isTemplateToken(s string) bool {
	for _, m := range templateMarkers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

func (*Extractor) ExtractFile(file string, src []byte) (*extract.FileResult, error) {
	res := &extract.FileResult{Path: file, Language: "html"}
	z := html.NewTokenizer(bytes.NewReader(src))
	line := 1
	style := false
	seen := map[string]bool{}
	symbols := map[string]bool{}
	for {
		kind := z.Next()
		raw := z.Raw()
		startLine := line
		line += bytes.Count(raw, []byte("\n"))
		switch kind {
		case html.ErrorToken:
			if z.Err() != io.EOF {
				return nil, z.Err()
			}
			return res, nil
		case html.StartTagToken, html.SelfClosingTagToken:
			token := z.Token()
			attrs := map[string]string{}
			for _, a := range token.Attr {
				attrs[a.Key] = a.Val
			}
			if token.Data == "style" {
				style = attrs["type"] == "" || attrs["type"] == "text/css"
			}
			for _, key := range []string{"class", "id"} {
				prefix := "."
				if key == "id" {
					prefix = "#"
				}
				for _, name := range strings.Fields(attrs[key]) {
					if isTemplateToken(name) || seen[prefix+name] {
						continue
					}
					seen[prefix+name] = true
					res.References = append(res.References, extract.Reference{From: file, To: prefix + name, Kind: extract.RefStyles, Line: startLine, Qualified: true})
				}
			}
			spec := ""
			if token.Data == "script" {
				spec = attrs["src"]
			}
			if token.Data == "link" {
				for _, rel := range strings.Fields(strings.ToLower(attrs["rel"])) {
					if rel == "stylesheet" {
						spec = attrs["href"]
					}
				}
			}
			if spec != "" && !isTemplateToken(spec) {
				u, err := url.Parse(spec)
				if err == nil && !u.IsAbs() && u.Host == "" && u.Opaque == "" && u.Path != "" && !strings.ContainsAny(u.Path, "\\\x00") {
					// Imports use a source-relative spec; root-relative paths are expressed
					// relative to this file without escaping the indexed project.
					p := u.Path
					if strings.HasPrefix(p, "/") {
						p = strings.Repeat("../", strings.Count(path.Dir(file), "/")+1) + strings.TrimPrefix(p, "/")
						if path.Dir(file) == "." {
							p = "./" + strings.TrimPrefix(u.Path, "/")
						}
					} else if !strings.HasPrefix(p, ".") {
						p = "./" + p
					}
					res.Imports = append(res.Imports, p)
				}
			}
		case html.EndTagToken:
			if z.Token().Data == "style" {
				style = false
			}
		case html.TextToken:
			if !style {
				continue
			}
			embedded, err := csssrc.New("css").ExtractFile(file, raw)
			if err != nil {
				return nil, err
			}
			for _, s := range embedded.Symbols {
				if symbols[s.FQN] {
					continue
				}
				symbols[s.FQN] = true
				s.StartLine += startLine - 1
				s.EndLine += startLine - 1
				s.Language = "html"
				res.Symbols = append(res.Symbols, s)
			}
			res.Imports = append(res.Imports, embedded.Imports...)
		}
	}
}
