package tui

import (
	"path/filepath"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// highlightSource turns a symbol's source body into one ANSI-colored string per input
// line, picked by the file's language. Returning per-line (not one blob) keeps the
// result a []string that drops straight into the overlay's existing scroll/window
// machinery. ok=false when the language can't be lexed — the caller then falls back to
// plain rendering, so an unknown/unsupported language never errors, just isn't colored.
func highlightSource(file, src string) ([]string, bool) {
	lexer := lexers.Match(filepath.Base(file))
	if lexer == nil {
		lexer = lexers.Analyse(src)
	}
	if lexer == nil {
		return nil, false
	}
	lexer = chroma.Coalesce(lexer)

	style := styles.Get("monokai")
	if style == nil {
		style = styles.Fallback
	}
	formatter := formatters.Get("terminal256")
	if formatter == nil {
		return nil, false
	}

	iter, err := lexer.Tokenise(nil, src)
	if err != nil {
		return nil, false
	}
	lines := chroma.SplitTokensIntoLines(iter.Tokens())
	out := make([]string, 0, len(lines))
	for _, lineToks := range lines {
		var b strings.Builder
		if err := formatter.Format(&b, style, chroma.Literator(lineToks...)); err != nil {
			return nil, false
		}
		out = append(out, strings.TrimRight(b.String(), "\n"))
	}
	return out, true
}
