package csssrc

import (
	"fmt"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/extract"
)

func findSym(syms []extract.Symbol, name string) *extract.Symbol {
	for i := range syms {
		if syms[i].Name == name {
			return &syms[i]
		}
	}
	return nil
}

func symNames(syms []extract.Symbol) []string {
	out := make([]string, 0, len(syms))
	for _, s := range syms {
		out = append(out, s.Name)
	}
	return out
}

func TestExtractPlainCSS(t *testing.T) {
	src := `.btn {
  color: red;
}
.btn:hover {
  color: blue;
}
#hero {
  margin: 0;
}
div {
  padding: 0;
}
.card .btn {
  border: none;
}
`
	res, err := New("css").ExtractFile("styles/app.css", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if res.Language != "css" {
		t.Fatalf("language = %q", res.Language)
	}

	btn := findSym(res.Symbols, ".btn")
	if btn == nil {
		t.Fatalf("missing .btn; symbols = %v", symNames(res.Symbols))
	}
	if btn.Kind != extract.KindSelector {
		t.Errorf(".btn kind = %q, want selector", btn.Kind)
	}
	if btn.FQN != "styles/app.css#.btn" {
		t.Errorf(".btn FQN = %q", btn.FQN)
	}
	// First defining rule wins position and Signature.
	if btn.StartLine != 1 || btn.EndLine != 3 {
		t.Errorf(".btn span = %d-%d, want 1-3", btn.StartLine, btn.EndLine)
	}
	if btn.Signature != ".btn" {
		t.Errorf(".btn signature = %q", btn.Signature)
	}
	if !strings.Contains(btn.Source, "color: red") {
		t.Errorf(".btn source = %q", btn.Source)
	}
	// .btn is defined by three rules (.btn, .btn:hover, .card .btn) → one node,
	// docstring noting the two extra rules.
	count := 0
	for _, s := range res.Symbols {
		if s.Name == ".btn" {
			count++
		}
	}
	if count != 1 {
		t.Errorf(".btn nodes = %d, want 1 (deduped)", count)
	}
	if !strings.Contains(btn.Docstring, "2 more rule") {
		t.Errorf(".btn docstring = %q, want a 2-more-rules note", btn.Docstring)
	}

	if hero := findSym(res.Symbols, "#hero"); hero == nil || hero.Kind != extract.KindSelector {
		t.Errorf("#hero missing/wrong: %+v", hero)
	}
	if card := findSym(res.Symbols, ".card"); card == nil {
		t.Errorf("missing .card")
	}
	// Element selectors are never indexed.
	for _, s := range res.Symbols {
		if s.Name == "div" || strings.Contains(s.Name, "div") {
			t.Errorf("element selector indexed: %+v", s)
		}
	}
	if len(res.Symbols) != 3 {
		t.Errorf("symbols = %v, want [.btn #hero .card]", symNames(res.Symbols))
	}
}

func TestExtractSCSSNesting(t *testing.T) {
	src := `.card {
  &.active {
    color: red;
  }
  .title {
    font-weight: bold;
  }
  &:hover .icon {
    opacity: 1;
  }
}
`
	res, err := New("scss").ExtractFile("styles/card.scss", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	wantSig := map[string]string{
		".card":   ".card",
		".active": ".card.active",
		".title":  ".card .title",
		".icon":   ".card:hover .icon",
	}
	for name, sig := range wantSig {
		s := findSym(res.Symbols, name)
		if s == nil {
			t.Errorf("missing %s; symbols = %v", name, symNames(res.Symbols))
			continue
		}
		if s.Signature != sig {
			t.Errorf("%s signature = %q, want %q", name, s.Signature, sig)
		}
	}
	if len(res.Symbols) != 4 {
		t.Errorf("symbols = %v, want 4", symNames(res.Symbols))
	}
}

func TestCommaGroupsFlattenAndCap(t *testing.T) {
	src := `.a, .b {
  & .x, & .y {
    color: red;
  }
}
`
	rules := ScanRules([]byte(src), false)
	var inner *Rule
	for i := range rules {
		if rules[i].StartLine == 2 {
			inner = &rules[i]
		}
	}
	if inner == nil {
		t.Fatalf("missing inner rule: %+v", rules)
	}
	want := []string{".a .x", ".a .y", ".b .x", ".b .y"}
	if strings.Join(inner.Selectors, ", ") != strings.Join(want, ", ") {
		t.Errorf("flattened = %v, want %v", inner.Selectors, want)
	}

	// Pathological cartesian: 8 parents × 8 children = 64 → capped at 16.
	var b strings.Builder
	for i := 0; i < 8; i++ {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, ".p%d", i)
	}
	b.WriteString(" {\n")
	for i := 0; i < 8; i++ {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "& .c%d", i)
	}
	b.WriteString(" { color: red; }\n}\n")
	rules = ScanRules([]byte(b.String()), false)
	for _, r := range rules {
		if len(r.Selectors) > maxFlattenedSelectors {
			t.Errorf("rule has %d selectors, cap is %d", len(r.Selectors), maxFlattenedSelectors)
		}
	}
}

func TestAtRulesInterpolationAndComments(t *testing.T) {
	src := `@media (min-width: 600px) {
  .wide {
    display: block;
  }
}
@keyframes spin {
  from { transform: none; }
  to { transform: rotate(1turn); }
}
@mixin frame {
  .inside-mixin { color: red; }
}
.mod-#{$variant} {
  color: blue;
}
/* .ghost { color: red; } */
// .ghost2 { color: red; }
`
	res, err := New("scss").ExtractFile("styles/at.scss", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if wide := findSym(res.Symbols, ".wide"); wide == nil {
		t.Errorf("rules inside @media must emit; symbols = %v", symNames(res.Symbols))
	}
	for _, bad := range []string{".inside-mixin", ".ghost", ".ghost2", ".mod-", "from", "to"} {
		if s := findSym(res.Symbols, bad); s != nil {
			t.Errorf("must not index %q: %+v", bad, s)
		}
	}
	if len(res.Symbols) != 1 {
		t.Errorf("symbols = %v, want only .wide", symNames(res.Symbols))
	}
}

func TestExtractSassIndented(t *testing.T) {
	src := `// comment .ghost
.toolbar
  color: red

  .btn
    font-weight: bold

  &.compact
    padding: 0

a:hover
  text-decoration: underline
`
	res, err := New("sass").ExtractFile("styles/app.sass", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	wantSig := map[string]string{
		".toolbar": ".toolbar",
		".btn":     ".toolbar .btn",
		".compact": ".toolbar.compact",
	}
	for name, sig := range wantSig {
		s := findSym(res.Symbols, name)
		if s == nil {
			t.Errorf("missing %s; symbols = %v", name, symNames(res.Symbols))
			continue
		}
		if s.Signature != sig {
			t.Errorf("%s signature = %q, want %q", name, s.Signature, sig)
		}
	}
	// Property lines (`color: red`) and comments never become selectors;
	// a:hover is element/pseudo-only → no token.
	if len(res.Symbols) != 3 {
		t.Errorf("symbols = %v, want 3", symNames(res.Symbols))
	}
	toolbar := findSym(res.Symbols, ".toolbar")
	if toolbar != nil && (toolbar.StartLine != 2 || toolbar.EndLine < 9) {
		t.Errorf(".toolbar span = %d-%d, want 2-9", toolbar.StartLine, toolbar.EndLine)
	}
}

func TestScanImports(t *testing.T) {
	src := `@import "./base.css";
@use "sass:math";
@use "./vars" as v;
@forward "./mixins";
@import url(https://fonts.example/css);
@import "./one.scss", "./two.scss";
/* @import "./commented.css"; */
.x { color: red; }
`
	res, err := New("scss").ExtractFile("styles/imp.scss", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"./base.css", "./vars", "./mixins", "./one.scss", "./two.scss"}
	if strings.Join(res.Imports, "|") != strings.Join(want, "|") {
		t.Errorf("imports = %v, want %v", res.Imports, want)
	}
}

func TestExtractLess(t *testing.T) {
	src := `// line comment .ghost
.badge {
  color: red;
}
.@{prefix}-dynamic {
  color: blue;
}
.alias {
  &:extend(.badge);
}
`
	res, err := New("less").ExtractFile("styles/app.less", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if badge := findSym(res.Symbols, ".badge"); badge == nil {
		t.Errorf("missing .badge; symbols = %v", symNames(res.Symbols))
	}
	if alias := findSym(res.Symbols, ".alias"); alias == nil {
		t.Errorf("missing .alias (&:extend must be tolerated)")
	}
	for _, s := range res.Symbols {
		if strings.Contains(s.Name, "dynamic") || strings.Contains(s.Name, "ghost") {
			t.Errorf("must not index dynamic/commented selector: %+v", s)
		}
	}
}
