package app

import "testing"

// TestClassifyQueryAmbiguousShortPhrases pins the F7 design decision: an
// ambiguous <=2-word query with no stopword (e.g. "node store") is pinned to
// shapeIdentifier, not shapeNaturalLanguage. This is a deliberate choice (a
// short non-stopword phrase in a code-search context is far more often a
// name/type lookup than a genuine question) — if this rule ever changes,
// update this comment too.
func TestClassifyQueryAmbiguousShortPhrases(t *testing.T) {
	for _, q := range []string{"node store", "rate limiter"} {
		if got := classifyQuery(q); got != shapeIdentifier {
			t.Errorf("classifyQuery(%q) = %q, want %q (pinned ambiguous-short-phrase default)", q, got, shapeIdentifier)
		}
	}
}

func TestClassifyQuery(t *testing.T) {
	identifierCases := []string{
		"ParseSelector",          // bare CamelCase word
		"parse_selector",         // snake_case
		"graph.Store.NodeAtLine", // dotted FQN
		"graph::Store",           // scoped identifier
		"NodeAtLine",             // single word, camelCase hump
	}
	for _, q := range identifierCases {
		if got := classifyQuery(q); got != shapeIdentifier {
			t.Errorf("classifyQuery(%q) = %q, want %q", q, got, shapeIdentifier)
		}
	}

	naturalLanguageCases := []string{
		"where do we retry on rate limit", // question, stopwords
		"how does auth work",
		"find the function that validates a jwt", // multi-word with stopwords
	}
	for _, q := range naturalLanguageCases {
		if got := classifyQuery(q); got != shapeNaturalLanguage {
			t.Errorf("classifyQuery(%q) = %q, want %q", q, got, shapeNaturalLanguage)
		}
	}
}

// TestClassifyQuerySingleStopword verifies that a lone stopword ("the") does
// NOT classify as identifier: rule 5 requires that NO word be a stopword, and
// a single stopword word still counts as containing one, so this falls
// through every rule to natural_language. This is the one place a single
// word does not classify as identifier — cover it explicitly so it doesn't
// silently flip later.
func TestClassifyQuerySingleStopword(t *testing.T) {
	if got := classifyQuery("the"); got != shapeNaturalLanguage {
		t.Errorf("classifyQuery(%q) = %q, want %q (lone stopword)", "the", got, shapeNaturalLanguage)
	}
}

// TestClassifyQueryTrailingPunctuation verifies the trailing sentence
// punctuation strip (rule 1) and that the dotted-identifier rule wins over
// the word-count/stopword rule even when the query is phrased as a question.
func TestClassifyQueryTrailingPunctuation(t *testing.T) {
	// "what is graph.Store?" contains a dotted token, so it classifies as
	// identifier even though it reads as a question — the dotted-token rule
	// (checked earlier) wins per the documented rule order.
	if got := classifyQuery("what is graph.Store?"); got != shapeIdentifier {
		t.Errorf("classifyQuery(%q) = %q, want %q (dotted token beats question phrasing)", "what is graph.Store?", got, shapeIdentifier)
	}
	// "foo.bar." should have only its single trailing '.' stripped, not its
	// meaningful internal dot — still identifier-shaped via the dotted rule.
	if got := classifyQuery("foo.bar."); got != shapeIdentifier {
		t.Errorf("classifyQuery(%q) = %q, want %q (trailing period stripped, internal dot kept)", "foo.bar.", got, shapeIdentifier)
	}
}

// TestClassifyQueryEmpty verifies the empty-string edge case: zero words
// means the <=2-word ambiguous-identifier rule never fires (it requires at
// least one word), so an empty query falls through to natural_language.
func TestClassifyQueryEmpty(t *testing.T) {
	if got := classifyQuery(""); got != shapeNaturalLanguage {
		t.Errorf("classifyQuery(%q) = %q, want %q", "", got, shapeNaturalLanguage)
	}
}
