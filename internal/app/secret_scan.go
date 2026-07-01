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
//
// P1-15 (B9): stripLineComment is now quote- and block-comment-aware, so a
// key inside a string after a `//` (e.g. a URL like "https://api.stripe.com")
// is no longer truncated. inBlock carries across lines so an interior
// /* ... */ block spanning multiple lines is fully consumed.
func scanGenericLiteral(rel string, data []byte, word *regexp.Regexp) []literalSite {
	var out []literalSite
	inBlock := false
	for i, raw := range strings.Split(string(data), "\n") {
		stripped, stillInBlock := stripLineComment(raw, inBlock)
		inBlock = stillInBlock
		if word.MatchString(stripped) {
			out = append(out, literalSite{File: rel, Line: i + 1, Confidence: "code"})
		}
	}
	return out
}

// stripLineComment removes a line/block-comment tail (best-effort: // # /*) so a
// key mentioned only in a comment isn't counted as a usage. P1-15: was
// first-occurrence cut with no string-awareness, so a JS/TS line
// `fetch("https://api.stripe.com", { auth: process.env.STRIPE_KEY })`
// was truncated at the `//` inside `https://` and the live key was
// marked orphan. The new version tracks in-string state (", ', `) so
// only the `//` / `#` / `/*` OUTSIDE strings terminates the line. The
// inBlock flag carries across lines within a file so interior
// `/* ... */` lines are skipped entirely. Returns the stripped line
// and the new inBlock state for the next line.
func stripLineComment(line string, inBlock bool) (string, bool) {
	if inBlock {
		// Entire line is inside an open block comment unless `*/` closes it.
		if i := strings.Index(line, "*/"); i >= 0 {
			return stripLineComment(line[i+2:], false)
		}
		return "", true
	}
	var (
		inS      byte // 0 when not in a string; otherwise the quote byte
		esc      bool
		cut      = -1
		outBlock bool
	)
	for i := 0; i < len(line); i++ {
		c := line[i]
		if inS != 0 {
			if esc {
				esc = false
				continue
			}
			if c == '\\' {
				esc = true
				continue
			}
			if c == inS {
				inS = 0
			}
			continue
		}
		switch c {
		case '"', '\'', '`':
			inS = c
		case '/':
			if i+1 < len(line) && line[i+1] == '/' {
				cut = i
				goto done
			}
			if i+1 < len(line) && line[i+1] == '*' {
				// Block comment: if */ closes on this line, drop the
				// commented-out chunk and recurse; otherwise cut at /*
				// and signal inBlock for the next line.
				if j := strings.Index(line[i+2:], "*/"); j >= 0 {
					tail := line[i+2+j+2:]
					rest, _ := stripLineComment(tail, false)
					return line[:i] + rest, false
				}
				cut = i
				outBlock = true
				goto done
			}
		case '#':
			cut = i
			goto done
		}
	}
done:
	if cut >= 0 {
		return line[:cut], outBlock
	}
	return line, false
}
