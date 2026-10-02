package app

import (
	"bytes"
	"encoding/json"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// Summary extraction for atlas. Every summary is real text lifted from the
// project (README paragraph, package doc, module docstring, leading comment);
// nothing is generated. Reads are bounded to the first atlasMaxReadBytes of a
// file and binary files are skipped.

const (
	atlasMaxReadBytes  = 16 << 10
	atlasSummaryMax    = 280
	atlasDocSentence   = 160
	atlasGoDocScanMax  = 400 // max .go files parsed per directory when looking for a package doc
	summaryPackageDoc  = "package doc"
	summaryDocGo       = "doc.go"
	summaryModuleDoc   = "module docstring"
	summaryFileComment = "file comment"
	summaryMarkdown    = "markdown"
	summaryIndexFile   = "index file"
	summaryManifest    = "manifest"
	summarySubdir      = "subdirectory"
)

var (
	licenseRe      = regexp.MustCompile(`(?i)copyright|licen[sc]e|spdx|all rights reserved|permission is hereby granted`)
	manifestDescRe = regexp.MustCompile(`(?m)^\s*description\s*=\s*"([^"\n]+)"`)
	packageDescRe  = regexp.MustCompile(`"description"\s*:\s*"((?:[^"\\]|\\.)*)"`)
	mdLinkRe       = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)
	mdRefLinkRe    = regexp.MustCompile(`\[([^\]]+)\]\[[^\]]*\]`)
	htmlTagRe      = regexp.MustCompile(`<[^>]*>`)
	mdEmphasisRe   = regexp.MustCompile("[`*]+")
	mdUnderscoreR  = regexp.MustCompile(`(^|\s)_+([^_\s][^_]*?)_+($|[\s.,;:!?])`)
	wsRe           = regexp.MustCompile(`\s+`)
	sentenceEndRe  = regexp.MustCompile(`[.!?]["')\]]?(\s|$)`)
	readmeNames    = []string{"README.md", "readme.md", "Readme.md", "README", "README.markdown", "README.mdx", "README.txt", "index.md"}
)

// summaryResult is a cleaned summary and where it came from.
type summaryResult struct {
	text   string
	source string
}

// summarizer extracts and caches summaries for one atlas request.
type summarizer struct {
	root   string
	cache  map[string]summaryResult
	errors []string
}

func newSummarizer(root string) *summarizer {
	return &summarizer{root: root, cache: map[string]summaryResult{}}
}

// readHead returns at most atlasMaxReadBytes of a project-relative file, or
// ok=false when it is missing, unreadable or binary.
func (s *summarizer) readHead(rel string) ([]byte, bool) {
	f, err := os.Open(filepath.Join(s.root, filepath.FromSlash(rel)))
	if err != nil {
		return nil, false
	}
	defer func() { _ = f.Close() }()
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
		return nil, false
	}
	buf, err := io.ReadAll(io.LimitReader(f, atlasMaxReadBytes))
	if err != nil {
		s.errors = append(s.errors, "summary read "+rel+": "+err.Error())
		return nil, false
	}
	if bytes.IndexByte(buf, 0) >= 0 {
		return nil, false
	}
	if !utf8.Valid(buf) {
		// A 16 KiB cut can split a rune; trim trailing partial bytes before judging.
		for i := 0; i < 3 && len(buf) > 0 && !utf8.Valid(buf); i++ {
			buf = buf[:len(buf)-1]
		}
		if !utf8.Valid(buf) {
			return nil, false
		}
	}
	return buf, true
}

// fileSummary returns the summary of one source/doc file (cached).
func (s *summarizer) fileSummary(rel, language string) summaryResult {
	if r, ok := s.cache[rel]; ok {
		return r
	}
	var r summaryResult
	if buf, ok := s.readHead(rel); ok {
		r = summarizeFileContent(rel, language, string(buf))
	}
	s.cache[rel] = r
	return r
}

// dirSummary applies the directory priority order: README, Go package doc
// (doc.go first), Python __init__ docstring, JS/TS index leading comment.
// goFiles are the indexed .go files directly in the directory.
func (s *summarizer) dirSummary(dirRel string, goFiles []string) summaryResult {
	for _, name := range readmeNames {
		rel := path.Join(dirRel, name)
		if buf, ok := s.readHead(rel); ok {
			if t := markdownSummary(string(buf), true); t != "" {
				return summaryResult{text: t, source: name}
			}
		}
	}
	if len(goFiles) > 0 {
		if r := s.goPackageDoc(goFiles); r.text != "" {
			return r
		}
	}
	for _, name := range []string{"__init__.py"} {
		if buf, ok := s.readHead(path.Join(dirRel, name)); ok {
			if t := pythonDocstring(string(buf)); t != "" {
				return summaryResult{text: cleanSummary(t), source: summaryModuleDoc}
			}
		}
	}
	for _, name := range []string{"index.ts", "index.tsx", "index.js", "index.mjs"} {
		rel := path.Join(dirRel, name)
		if buf, ok := s.readHead(rel); ok {
			if t := leadingComment(string(buf), "//", "/*"); t != "" {
				return summaryResult{text: cleanSummary(t), source: summaryIndexFile}
			}
		}
	}
	return s.manifestDescription(dirRel)
}

// manifestDescription reads the package description a directory's own
// manifest declares: package.json "description", or the description key of
// pyproject.toml / Cargo.toml. It is the author's one-line summary of the
// package, so it is used as written.
func (s *summarizer) manifestDescription(dirRel string) summaryResult {
	if buf, ok := s.readHead(path.Join(dirRel, "package.json")); ok {
		var pkg struct {
			Description string `json:"description"`
		}
		if json.Unmarshal(buf, &pkg) != nil {
			// A manifest larger than the read head is truncated JSON; its
			// description key sits near the top in practice.
			if m := packageDescRe.FindSubmatch(buf); m != nil {
				_ = json.Unmarshal(append(append([]byte{'"'}, m[1]...), '"'), &pkg.Description)
			}
		}
		if t := cleanSummary(pkg.Description); t != "" {
			return summaryResult{text: t, source: "package.json"}
		}
	}
	for _, name := range []string{"pyproject.toml", "Cargo.toml"} {
		if buf, ok := s.readHead(path.Join(dirRel, name)); ok {
			if m := manifestDescRe.FindSubmatch(buf); m != nil {
				if t := cleanSummary(string(m[1])); t != "" {
					return summaryResult{text: t, source: name}
				}
			}
		}
	}
	return summaryResult{}
}

// goPackageDoc finds the package doc of a directory: doc.go first, otherwise
// the first non-test .go file with a "Package x" doc (any doc as a fallback).
func (s *summarizer) goPackageDoc(goFiles []string) summaryResult {
	files := append([]string(nil), goFiles...)
	sort.Slice(files, func(i, j int) bool {
		di, dj := path.Base(files[i]) == "doc.go", path.Base(files[j]) == "doc.go"
		if di != dj {
			return di
		}
		return files[i] < files[j]
	})
	var fallback summaryResult
	scanned := 0
	for _, rel := range files {
		if strings.HasSuffix(rel, "_test.go") {
			continue
		}
		if scanned++; scanned > atlasGoDocScanMax {
			break
		}
		buf, ok := s.readHead(rel)
		if !ok {
			continue
		}
		doc := goPackageDocText(buf)
		if doc == "" {
			continue
		}
		src := summaryPackageDoc
		if path.Base(rel) == "doc.go" {
			src = summaryDocGo
		}
		res := summaryResult{text: cleanSummary(doc), source: src}
		if strings.HasPrefix(doc, "Package ") || strings.HasPrefix(doc, "Command ") || src == summaryDocGo {
			return res
		}
		if fallback.text == "" {
			fallback = res
		}
	}
	return fallback
}

// goPackageDocText returns the package doc comment above the package clause,
// ignoring license headers.
func goPackageDocText(src []byte) string {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", src, parser.PackageClauseOnly|parser.ParseComments)
	if err != nil && f == nil {
		return ""
	}
	if f.Doc != nil {
		if t := firstParagraph(strings.TrimSpace(f.Doc.Text())); t != "" && !licenseRe.MatchString(t) {
			return t
		}
	}
	return ""
}

// goFileComment returns the best description of a Go file: the package doc, else
// the first non-license comment group before the package clause.
func goFileComment(src []byte) (string, string) {
	if t := goPackageDocText(src); t != "" {
		return t, summaryPackageDoc
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", src, parser.PackageClauseOnly|parser.ParseComments)
	if err != nil && f == nil {
		return "", ""
	}
	for _, cg := range f.Comments {
		if cg.Pos() > f.Package {
			break
		}
		t := firstParagraph(strings.TrimSpace(cg.Text()))
		if t == "" || licenseRe.MatchString(t) || strings.HasPrefix(t, "go:") {
			continue
		}
		return t, summaryFileComment
	}
	return "", ""
}

// summarizeFileContent dispatches on extension/language.
func summarizeFileContent(rel, language, content string) summaryResult {
	ext := strings.ToLower(path.Ext(rel))
	switch {
	case ext == ".go":
		if t, src := goFileComment([]byte(content)); t != "" {
			return summaryResult{text: cleanSummary(t), source: src}
		}
	case ext == ".py" || ext == ".pyi":
		if t := pythonDocstring(content); t != "" {
			return summaryResult{text: cleanSummary(t), source: summaryModuleDoc}
		}
		if t := leadingComment(content, "#", ""); t != "" {
			return summaryResult{text: cleanSummary(t), source: summaryFileComment}
		}
	case ext == ".md" || ext == ".markdown" || ext == ".mdx" || language == "markdown":
		if t := markdownSummary(content, false); t != "" {
			return summaryResult{text: t, source: summaryMarkdown}
		}
	case ext == ".yaml" || ext == ".yml":
		if t := leadingComment(content, "#", ""); t != "" {
			return summaryResult{text: cleanSummary(t), source: summaryFileComment}
		}
	case ext == ".lua":
		if t := leadingComment(content, "--", "--[["); t != "" {
			return summaryResult{text: cleanSummary(t), source: summaryFileComment}
		}
	case ext == ".rb" || ext == ".gd" || ext == ".sh" || ext == ".toml":
		if t := leadingComment(content, "#", ""); t != "" {
			return summaryResult{text: cleanSummary(t), source: summaryFileComment}
		}
	case ext == ".sql":
		if t := leadingComment(content, "--", "/*"); t != "" {
			return summaryResult{text: cleanSummary(t), source: summaryFileComment}
		}
	case ext == ".css" || ext == ".scss":
		if t := leadingComment(content, "", "/*"); t != "" {
			return summaryResult{text: cleanSummary(t), source: summaryFileComment}
		}
	case ext == ".html" || ext == ".htm" || ext == ".vue":
		if t := htmlLeadingComment(content); t != "" {
			return summaryResult{text: cleanSummary(t), source: summaryFileComment}
		}
	default: // ts, tsx, js, jsx, mjs, cjs, java, c, … : C-style comments
		if t := leadingComment(content, "//", "/*"); t != "" {
			return summaryResult{text: cleanSummary(t), source: summaryFileComment}
		}
	}
	return summaryResult{}
}

// leadingComment returns the first non-license comment block at the top of a
// file. line is the line-comment marker ("" for none), block the block-comment
// opener ("" for none; the closer is derived: "/*"→"*/", "--[["→"]]").
// Shebangs, encoding/magic comments, blank lines and license headers are skipped.
func leadingComment(content, line, block string) string {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	i := 0
	for i < len(lines) {
		raw := strings.TrimSpace(lines[i])
		switch {
		case raw == "" || strings.HasPrefix(raw, "#!") || isMagicComment(raw):
			i++
			continue
		}
		var text []string
		switch {
		case block != "" && strings.HasPrefix(raw, block):
			closer := blockCloser(block)
			j := i
			var body []string
			for ; j < len(lines); j++ {
				l := lines[j]
				if j == i {
					l = strings.TrimPrefix(strings.TrimSpace(l), block)
				}
				end := strings.Index(l, closer)
				if end >= 0 {
					body = append(body, l[:end])
					break
				}
				body = append(body, l)
				if j-i > 80 {
					break
				}
			}
			for _, l := range body {
				l = strings.TrimSpace(l)
				l = strings.TrimLeft(l, "*")
				body2 := strings.TrimSpace(l)
				text = append(text, body2)
			}
			i = j + 1
		case line != "" && strings.HasPrefix(raw, line):
			j := i
			for ; j < len(lines); j++ {
				l := strings.TrimSpace(lines[j])
				if !strings.HasPrefix(l, line) {
					break
				}
				l = strings.TrimPrefix(l, line)
				// "///" and "##" doc markers, then one optional space.
				l = strings.TrimLeft(l, string(line[len(line)-1:]))
				text = append(text, strings.TrimSpace(l))
			}
			i = j
		default:
			return ""
		}
		joined := strings.TrimSpace(strings.Join(text, "\n"))
		if joined == "" || licenseRe.MatchString(joined) {
			continue // license header or empty block: look at the next one
		}
		return firstParagraph(joined)
	}
	return ""
}

func blockCloser(opener string) string {
	switch opener {
	case "--[[":
		return "]]"
	case "<!--":
		return "-->"
	}
	return "*/"
}

func isMagicComment(l string) bool {
	low := strings.ToLower(l)
	return strings.HasPrefix(low, "# frozen_string_literal") ||
		strings.HasPrefix(low, "# -*-") ||
		strings.HasPrefix(low, "# encoding:") ||
		strings.HasPrefix(low, "# coding") ||
		strings.HasPrefix(low, "# vim:") ||
		strings.HasPrefix(low, "# eslint") ||
		strings.HasPrefix(low, "// @ts-") ||
		strings.HasPrefix(low, "/* eslint") ||
		strings.HasPrefix(low, "'use strict'") || strings.HasPrefix(low, "\"use strict\"") ||
		strings.HasPrefix(low, "// eslint") || strings.HasPrefix(low, "// prettier") ||
		strings.HasPrefix(low, "# yaml-language-server") || strings.HasPrefix(low, "# syntax=")
}

func htmlLeadingComment(content string) string {
	t := strings.TrimSpace(content)
	t = strings.TrimPrefix(t, "<!DOCTYPE html>")
	t = strings.TrimPrefix(strings.TrimSpace(t), "<!doctype html>")
	t = strings.TrimSpace(t)
	if !strings.HasPrefix(t, "<!--") {
		return ""
	}
	end := strings.Index(t, "-->")
	if end < 0 {
		return ""
	}
	body := strings.TrimSpace(t[4:end])
	if licenseRe.MatchString(body) {
		return ""
	}
	return firstParagraph(body)
}

// pythonDocstring returns the module docstring (first statement), if any.
func pythonDocstring(content string) string {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	i := 0
	for i < len(lines) {
		raw := strings.TrimSpace(lines[i])
		if raw == "" || strings.HasPrefix(raw, "#") {
			i++
			continue
		}
		break
	}
	if i >= len(lines) {
		return ""
	}
	rest := strings.Join(lines[i:], "\n")
	rest = strings.TrimLeft(rest, " \t")
	// Optional string prefixes (r, u, b).
	rest = strings.TrimLeft(rest, "rRuUbB")
	var q string
	switch {
	case strings.HasPrefix(rest, `"""`):
		q = `"""`
	case strings.HasPrefix(rest, `'''`):
		q = `'''`
	default:
		return ""
	}
	rest = rest[3:]
	end := strings.Index(rest, q)
	if end < 0 {
		return ""
	}
	body := strings.TrimSpace(rest[:end])
	if body == "" || licenseRe.MatchString(body) {
		return ""
	}
	return firstParagraph(body)
}

// firstParagraph returns the first blank-line-delimited paragraph.
func firstParagraph(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	for _, p := range strings.Split(s, "\n\n") {
		if strings.TrimSpace(p) != "" {
			return strings.TrimSpace(p)
		}
	}
	return ""
}

// markdownSummary extracts the first prose paragraph (after the H1), skipping
// front matter, badges, images, HTML, tables, lists and code fences. When
// there is no prose paragraph, a file (not a README) falls back to its H1.
func markdownSummary(content string, readme bool) string {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	i := 0
	// YAML front matter.
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "---" {
		for j := 1; j < len(lines) && j < 60; j++ {
			if strings.TrimSpace(lines[j]) == "---" {
				i = j + 1
				break
			}
		}
	}
	h1 := ""
	var para []string
	inFence := false
	inComment := false
	flush := func() string {
		if len(para) == 0 {
			return ""
		}
		text := cleanSummary(strings.Join(para, " "))
		para = nil
		return text
	}
	for ; i < len(lines); i++ {
		raw := lines[i]
		l := strings.TrimSpace(raw)
		if inComment {
			if strings.Contains(l, "-->") {
				inComment = false
			}
			continue
		}
		if strings.HasPrefix(l, "<!--") {
			if !strings.Contains(l, "-->") {
				inComment = true
			}
			continue
		}
		if strings.HasPrefix(l, "```") || strings.HasPrefix(l, "~~~") {
			if t := flush(); t != "" && !isNoiseParagraph(t) {
				return t
			}
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if l == "" {
			if t := flush(); t != "" && !isNoiseParagraph(t) {
				return t
			}
			continue
		}
		if strings.HasPrefix(l, "#") {
			if t := flush(); t != "" && !isNoiseParagraph(t) {
				return t
			}
			if h1 == "" && strings.HasPrefix(l, "# ") {
				h1 = cleanSummary(strings.TrimLeft(l, "# "))
			}
			continue
		}
		if isMarkdownNonProse(l) {
			if t := flush(); t != "" && !isNoiseParagraph(t) {
				return t
			}
			continue
		}
		l = strings.TrimSpace(strings.TrimPrefix(l, ">"))
		if l == "" {
			continue
		}
		para = append(para, l)
	}
	if t := flush(); t != "" && !isNoiseParagraph(t) {
		return t
	}
	if !readme {
		return h1
	}
	return h1
}

// isMarkdownNonProse reports lines that are never part of a prose paragraph.
func isMarkdownNonProse(l string) bool {
	switch {
	case strings.HasPrefix(l, "![") || strings.HasPrefix(l, "[!["):
		return true // images and badges
	case strings.HasPrefix(l, "|"):
		return true // tables
	case strings.HasPrefix(l, "- ") || strings.HasPrefix(l, "* ") || strings.HasPrefix(l, "+ "):
		return true // lists
	case len(l) > 2 && l[0] >= '0' && l[0] <= '9' && (strings.HasPrefix(l[1:], ". ") || strings.HasPrefix(l[1:], ") ")):
		return true
	case strings.Trim(l, "-*_= ") == "":
		return true // horizontal rules / setext underlines
	case strings.HasPrefix(l, "<") && strings.TrimSpace(htmlTagRe.ReplaceAllString(l, "")) == "":
		return true // pure HTML (<p align=center><img …>)
	case strings.HasPrefix(l, "[") && strings.Contains(l, "]:") && !strings.Contains(l, " "):
		return true
	}
	return false
}

// isNoiseParagraph rejects paragraphs that survived cleaning but carry no prose:
// too short, or navigation rows made only of link labels separated by bars.
func isNoiseParagraph(t string) bool {
	if utf8.RuneCountInString(t) < 4 {
		return true
	}
	if !strings.ContainsAny(t, " \t") && !strings.ContainsAny(t, ".!?") {
		return true
	}
	words := strings.Fields(t)
	bars := 0
	for _, w := range words {
		if w == "|" || w == "·" || w == "•" || w == "-" || w == "—" {
			bars++
		}
	}
	return bars > 0 && bars*3 >= len(words)-bars
}

// cleanSummary turns a comment/markdown fragment into a short plain-text summary:
// markdown syntax, HTML tags and link targets are stripped, whitespace is
// collapsed and the result is cut at a sentence/word boundary to atlasSummaryMax.
func cleanSummary(s string) string {
	s = mdLinkRe.ReplaceAllStringFunc(s, func(m string) string {
		if strings.HasPrefix(m, "!") {
			return "" // image: drop entirely
		}
		return mdLinkRe.ReplaceAllString(m, "$1")
	})
	s = mdRefLinkRe.ReplaceAllString(s, "$1")
	s = htmlTagRe.ReplaceAllString(s, "")
	s = mdEmphasisRe.ReplaceAllString(s, "")
	s = mdUnderscoreR.ReplaceAllString(s, "$1$2$3")
	s = strings.TrimLeft(strings.TrimSpace(s), "#> ")
	s = wsRe.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)
	return truncateSentence(s, atlasSummaryMax)
}

// truncateSentence caps s at max runes, preferring a sentence boundary, then a
// word boundary (with an ellipsis).
func truncateSentence(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	cut := string(r[:max])
	best := -1
	for _, loc := range sentenceEndRe.FindAllStringIndex(cut, -1) {
		best = loc[0] + 1
	}
	if best >= 60 {
		return strings.TrimSpace(cut[:best])
	}
	if i := strings.LastIndexByte(cut, ' '); i > max/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,;:-") + "…"
}

// firstSentence returns the first sentence of a docstring, capped at max runes.
func firstSentence(doc string, max int) string {
	doc = wsRe.ReplaceAllString(strings.TrimSpace(doc), " ")
	if doc == "" {
		return ""
	}
	if loc := sentenceEndRe.FindStringIndex(doc); loc != nil {
		doc = strings.TrimSpace(doc[:loc[0]+1])
	}
	return truncateSentence(doc, max)
}
