package app

import (
	"regexp"
	"strings"
)

// queryShape is the result of classifyQuery: which fusion profile a query
// should use.
type queryShape string

const (
	shapeIdentifier      queryShape = "identifier"
	shapeNaturalLanguage queryShape = "natural_language"
)

// dottedIdentRe matches a dotted/scoped identifier segment such as
// "graph.Store.NodeAtLine" (no surrounding whitespace around the dots).
var dottedIdentRe = regexp.MustCompile(`\w+(?:\.\w+)+`)

// snakeCaseRe matches an underscore-joined token such as "parse_selector".
var snakeCaseRe = regexp.MustCompile(`\w+_\w+`)

// camelHumpRe matches a lower-to-upper case transition such as the "eS" in
// "NodeStore" — the hallmark of camelCase/PascalCase identifiers.
var camelHumpRe = regexp.MustCompile(`[a-z0-9][A-Z]`)

// stopwords is the fixed set consulted by rule 5 (case-insensitive).
var stopwords = map[string]bool{
	"a": true, "an": true, "the": true, "is": true, "are": true, "was": true, "were": true,
	"do": true, "does": true, "did": true, "can": true, "could": true, "should": true, "would": true,
	"will": true, "where": true, "what": true, "when": true, "how": true, "why": true, "who": true,
	"which": true, "with": true, "for": true, "to": true, "in": true, "on": true, "at": true,
	"of": true, "and": true, "or": true, "not": true, "this": true, "that": true, "these": true,
	"those": true, "we": true, "i": true, "you": true, "it": true, "its": true, "be": true, "been": true,
}

// classifyQuery is a cheap, deterministic heuristic — no model calls — that
// decides whether a semantic-search query looks like a code identifier (name
// lookup, should lean BM25/exact-match) or a natural-language question
// (meaning lookup, should lean vector). Ambiguous short queries (<=2 words,
// no stopwords, e.g. "node store") are pinned to identifier: in a code-search
// context a short non-stopword phrase is far more often a name/type lookup
// than a genuine question, and misclassifying only shifts weight (both
// channels stay > 0), it never zeroes one out.
//
// Rules are checked in order against the trimmed query:
//  1. Strip one trailing '.', '?', or '!' (sentence punctuation).
//  2. A dotted/scoped identifier anywhere ("graph.Store.NodeAtLine") or a
//     literal "::" substring ("graph::Store") -> identifier.
//  3. A snake_case token anywhere ("parse_selector") -> identifier.
//  4. A camelCase hump anywhere ("NodeAtLine") -> identifier.
//  5. <=2 whitespace-separated words, none a stopword -> identifier.
//  6. Otherwise -> natural_language.
func classifyQuery(query string) queryShape {
	q := strings.TrimSpace(query)
	if q != "" {
		last := q[len(q)-1]
		if last == '.' || last == '?' || last == '!' {
			q = q[:len(q)-1]
		}
	}

	if dottedIdentRe.MatchString(q) || strings.Contains(q, "::") {
		return shapeIdentifier
	}
	if snakeCaseRe.MatchString(q) {
		return shapeIdentifier
	}
	if camelHumpRe.MatchString(q) {
		return shapeIdentifier
	}

	words := strings.Fields(q)
	if len(words) <= 2 && len(words) > 0 {
		hasStopword := false
		for _, w := range words {
			if stopwords[strings.ToLower(w)] {
				hasStopword = true
				break
			}
		}
		if !hasStopword {
			return shapeIdentifier
		}
	}

	return shapeNaturalLanguage
}
