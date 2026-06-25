package app

import (
	"go/scanner"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// literalSite is one place a secret key NAME appears in code — already filtered to
// usage context (a string literal or a non-comment code line), not a raw byte
// match. Confidence is "string" (a real string literal, from go/scanner — high) or
// "code" (a non-comment line in a non-Go file — medium, heuristic).
type literalSite struct {
	File       string
	Line       int
	Confidence string
}

// scanLiteralUsages finds where key appears as a USAGE (string literal / non-comment
// code) across the project's indexed files, returning project-relative file:line
// sites. This is the only net-new primitive of secret-impact, and the critique's
// blocking fix lives here: a raw scan can't tell os.Getenv("KEY") from a comment or
// log string mentioning KEY (both would inflate a "rotation blast radius"). So Go
// files are tokenized with go/scanner (only STRING tokens count — comments and bare
// identifiers are excluded by construction); other languages drop the comment
// portion of each line before matching. Word-boundary-anchored so STRIPE_KEY never
// matches STRIPE_KEY_BACKUP. NEVER returns line content — only positions.
func scanLiteralUsages(root string, files []string, key string) []literalSite {
	if key == "" {
		return nil
	}
	word := regexp.MustCompile(`\b` + regexp.QuoteMeta(key) + `\b`)
	var sites []literalSite
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil || !word.Match(data) {
			continue
		}
		if strings.HasSuffix(rel, ".go") {
			sites = append(sites, scanGoLiteral(rel, data, word)...)
		} else {
			sites = append(sites, scanGenericLiteral(rel, data, word)...)
		}
	}
	return sites
}

// scanGoLiteral returns the lines where key appears inside a Go STRING literal —
// exact, via go/scanner (mode 0 skips comments), so a key in a // comment or as an
// identifier is never counted.
func scanGoLiteral(rel string, data []byte, word *regexp.Regexp) []literalSite {
	var fset token.FileSet
	file := fset.AddFile(rel, fset.Base(), len(data))
	var s scanner.Scanner
	s.Init(file, data, nil /*no error handler*/, 0 /*skip comments*/)
	var out []literalSite
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		if tok == token.STRING {
			if unq, err := strconv.Unquote(lit); err == nil && word.MatchString(unq) {
				out = append(out, literalSite{File: rel, Line: fset.Position(pos).Line, Confidence: "string"})
			}
		}
	}
	return out
}

// scanGenericLiteral matches key in the non-comment portion of each line — a
// heuristic for languages with no stdlib tokenizer (Python/JS/TS): it drops the
// worst false-positive class (comment mentions) at near-zero cost. Catches both
// os.environ["KEY"] (string) and process.env.KEY (property access).
func scanGenericLiteral(rel string, data []byte, word *regexp.Regexp) []literalSite {
	var out []literalSite
	for i, raw := range strings.Split(string(data), "\n") {
		if word.MatchString(stripLineComment(raw)) {
			out = append(out, literalSite{File: rel, Line: i + 1, Confidence: "code"})
		}
	}
	return out
}

// stripLineComment removes a line/block-comment tail (best-effort: // # /*) so a
// key mentioned only in a comment isn't counted as a usage.
func stripLineComment(line string) string {
	for _, marker := range []string{"//", "#", "/*"} {
		if i := strings.Index(line, marker); i >= 0 {
			line = line[:i]
		}
	}
	return line
}
