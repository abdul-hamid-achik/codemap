package app

import (
	"regexp"
	"testing"
)

func TestStripLineComment_StringAware(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		inBlock  bool
		want     string
		outBlock bool
	}{
		{
			name:     "double-slash inside string is kept",
			in:       `fetch("https://api.stripe.com", { auth: process.env.STRIPE_KEY })`,
			inBlock:  false,
			want:     `fetch("https://api.stripe.com", { auth: process.env.STRIPE_KEY })`,
			outBlock: false,
		},
		{
			name:     "double-slash outside string cuts at the //",
			in:       `const x = 1; // comment with KEY`,
			inBlock:  false,
			want:     `const x = 1; `,
			outBlock: false,
		},
		{
			name:     "hash cuts (Python/shell)",
			in:       `process.env.KEY  # comment`,
			inBlock:  false,
			want:     `process.env.KEY  `,
			outBlock: false,
		},
		{
			name:     "single-quoted string preserves // inside",
			in:       `var s = 'https://x'; // real comment`,
			inBlock:  false,
			want:     `var s = 'https://x'; `,
			outBlock: false,
		},
		{
			name:     "block comment opener continues to next line",
			in:       `process.env.KEY /* opens here`,
			inBlock:  false,
			want:     `process.env.KEY `,
			outBlock: true,
		},
		{
			name:     "block comment continuation is fully consumed",
			in:       `more comment text */ KEY inside the line`,
			inBlock:  true,
			want:     ` KEY inside the line`,
			outBlock: false,
		},
		{
			name:     "block comment unterminated stays inBlock",
			in:       `still inside a block comment`,
			inBlock:  true,
			want:     "",
			outBlock: true,
		},
		{
			name:     "single-line block comment is removed in place",
			in:       `KEY /* drop me */ real_code()`,
			inBlock:  false,
			want:     `KEY  real_code()`,
			outBlock: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, outBlock := stripLineComment(tc.in, tc.inBlock)
			if got != tc.want {
				t.Errorf("stripLineComment(%q, %v) = %q, want %q", tc.in, tc.inBlock, got, tc.want)
			}
			if outBlock != tc.outBlock {
				t.Errorf("stripLineComment(%q, %v) outBlock = %v, want %v", tc.in, tc.inBlock, outBlock, tc.outBlock)
			}
		})
	}
}

func TestScanGenericLiteral_FindsKeyInURLString(t *testing.T) {
	// P1-15 (B9) live repro: a JS/TS file with a URL containing
	// "https://" followed by a live STRIPE_KEY read. Pre-fix the line
	// was truncated at the // in the URL and the key was marked orphan.
	body := []byte("const url = \"https://api.stripe.com\";\nconst auth = process.env.STRIPE_KEY;\n")
	word := regexp.MustCompile(`\b` + regexp.QuoteMeta("STRIPE_KEY") + `\b`)
	sites := scanGenericLiteral("app.js", body, word)
	if len(sites) != 1 {
		t.Fatalf("scanGenericLiteral should find the live STRIPE_KEY despite the // in the URL string; got %d sites", len(sites))
	}
	if sites[0].Line != 2 {
		t.Errorf("site on line %d, want 2", sites[0].Line)
	}
}

func TestScanGenericLiteral_KeyInRealCommentIsNotCounted(t *testing.T) {
	body := []byte("// just a comment mentioning STRIPE_KEY\nconst x = 1;\n")
	word := regexp.MustCompile(`\\b` + regexp.QuoteMeta("STRIPE_KEY") + `\\b`)
	sites := scanGenericLiteral("app.js", body, word)
	if len(sites) != 0 {
		t.Errorf("STRIPE_KEY mentioned only in a comment must NOT count as a usage, got %d sites", len(sites))
	}
}
