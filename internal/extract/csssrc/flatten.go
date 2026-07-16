package csssrc

import (
	"regexp"
	"strings"
)

// maxFlattenedSelectors caps the parent × child comma cartesian per rule.
// Beyond the cap we keep the first combinations — conservative truncation,
// never a failure (a pathological `.a,.b,…{&.x,&.y,…{}}` must not explode).
const maxFlattenedSelectors = 16

// flattenSelectors resolves a rule's own selector variants against its parent
// rule's flattened list: `&` splices the parent in place; otherwise the child
// is a descendant of the parent. Root-level rules pass through as-is.
func flattenSelectors(parents, children []string) []string {
	if len(children) > maxFlattenedSelectors {
		children = children[:maxFlattenedSelectors]
	}
	if len(parents) == 0 {
		return children
	}
	var out []string
	for _, p := range parents {
		for _, c := range children {
			var s string
			if strings.Contains(c, "&") {
				s = strings.ReplaceAll(c, "&", p)
			} else {
				s = p + " " + c
			}
			out = append(out, s)
			if len(out) >= maxFlattenedSelectors {
				return out
			}
		}
	}
	return out
}

// splitTopLevelCommas splits a selector prelude on commas outside `()`/`[]`
// (`:not(.a, .b)` is one variant), collapsing internal whitespace runs —
// preludes legally span lines.
func splitTopLevelCommas(s string) []string {
	var parts []string
	depth := 0
	start := 0
	flush := func(end int) {
		if p := strings.Join(strings.Fields(s[start:end]), " "); p != "" {
			parts = append(parts, p)
		}
	}
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(', '[':
			depth++
		case ')', ']':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				flush(i)
				start = i + 1
			}
		}
	}
	flush(len(s))
	return parts
}

// The Sass indented syntax: indentation replaces braces. A line opens a
// nesting level iff it is not a property declaration (`name: value` — colon
// followed by whitespace/end-of-line), not a `$var:` assignment, not an
// at-statement, and not a mixin shorthand (`=name` defines, `+name` includes).

// propertyLineRe: a property (or `$var`) name followed by `:` and whitespace
// or end of line. `a:hover` (colon followed by an ident char) stays a
// selector.
var propertyLineRe = regexp.MustCompile(`^-{0,2}[$\w-]+\s*:(\s|$)`)

// Indented-syntax at-rules that open opaque bodies (definitions/control flow —
// nothing inside is a static selector definition).
var opaqueIndentedAtRules = map[string]bool{
	"@mixin": true, "@function": true, "@keyframes": true, "@font-face": true,
	"@page": true, "@include": true, "@if": true, "@else": true, "@each": true,
	"@for": true, "@while": true, "@at-root": true,
}

type sassFrame struct {
	kind      frameKind
	indent    int
	selectors []string
	startLine int
	startIdx  int // 0-based index into the raw line slice
}

// scanIndentedRules scans .sass (indented syntax) source: an indentation
// stack replaces the brace stack, with the same flattening rules. EndLines
// are assigned from the last content line inside a block — imperfect on
// pathological layouts (the luasrc precedent: degrade, never drop).
func scanIndentedRules(src []byte) []Rule {
	code, _ := sanitizeCSS(src, true)
	lines := strings.Split(string(code), "\n")
	rawLines := strings.Split(string(src), "\n")

	var rules []Rule
	var stack []sassFrame
	lastContent := 0

	pop := func() {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if f.kind != ruleFrame {
			return
		}
		end := lastContent
		if end < f.startLine {
			end = f.startLine
		}
		rules = append(rules, Rule{
			Selectors: f.selectors,
			StartLine: f.startLine,
			EndLine:   end,
			Text:      strings.Join(rawLines[f.startIdx:end], "\n"),
		})
	}

	for i, line := range lines {
		lineNo := i + 1
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		indent := indentWidth(line)
		for len(stack) > 0 && indent <= stack[len(stack)-1].indent {
			pop()
		}
		lastContent = lineNo

		switch {
		case propertyLineRe.MatchString(trimmed):
			// property declaration (`color: red`) or `$var:` assignment — never
			// a selector. A trailing-colon property namespace (`font:`) nests
			// only more properties, which classify themselves; no frame needed.
		case strings.HasPrefix(trimmed, "@"):
			word := trimmed
			if j := strings.IndexAny(trimmed, " \t("); j >= 0 {
				word = trimmed[:j]
			}
			word = strings.ToLower(word)
			switch {
			case transparentAtRules[word]:
				stack = append(stack, sassFrame{kind: transparentFrame, indent: indent})
			case opaqueIndentedAtRules[word]:
				stack = append(stack, sassFrame{kind: opaqueFrame, indent: indent})
			}
			// @import/@use/@forward/@debug/…: statements, no block of interest
			// (imports are scanned separately over the whole source).
		case strings.HasPrefix(trimmed, "=") || strings.HasPrefix(trimmed, "+"):
			// `=mixin` definition / `+mixin` include shorthands: opaque bodies.
			stack = append(stack, sassFrame{kind: opaqueFrame, indent: indent})
		default:
			kind := ruleFrame
			for _, f := range stack {
				if f.kind == opaqueFrame {
					kind = opaqueFrame
					break
				}
			}
			f := sassFrame{kind: kind, indent: indent, startLine: lineNo, startIdx: i}
			if kind == ruleFrame {
				f.selectors = flattenSelectors(nearestSassRuleSelectors(stack), splitTopLevelCommas(trimmed))
			}
			stack = append(stack, f)
		}
	}
	for len(stack) > 0 {
		pop()
	}
	return rules
}

func nearestSassRuleSelectors(stack []sassFrame) []string {
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i].kind == ruleFrame {
			return stack[i].selectors
		}
	}
	return nil
}

// indentWidth counts leading whitespace bytes (a tab counts as one — files
// are expected to be internally consistent, per the Sass syntax itself).
func indentWidth(line string) int {
	for i := 0; i < len(line); i++ {
		if line[i] != ' ' && line[i] != '\t' {
			return i
		}
	}
	return len(line)
}
