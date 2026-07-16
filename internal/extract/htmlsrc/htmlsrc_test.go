package htmlsrc

import (
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/extract"
)

func refTargets(refs []extract.Reference) map[string]extract.Reference {
	out := map[string]extract.Reference{}
	for _, r := range refs {
		out[r.To] = r
	}
	return out
}

func TestExtractHTMLClassRefs(t *testing.T) {
	src := `<!doctype html>
<html>
  <body>
    <div class="btn card-title">x</div>
    <div class='btn'>dedupe</div>
    <section id="hero"></section>
    <!-- <div class="ghost"> -->
    <span class="{{cls}}">template</span>
    <div data-id="row-7" data-class="phantom">not styling attrs</div>
  </body>
</html>
`
	res, err := New().ExtractFile("site/index.html", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if res.Language != "html" {
		t.Fatalf("language = %q", res.Language)
	}
	if len(res.Symbols) != 0 {
		t.Fatalf("html must emit no symbols, got %d", len(res.Symbols))
	}

	byTo := refTargets(res.References)
	for _, want := range []string{".btn", ".card-title", "#hero"} {
		r, ok := byTo[want]
		if !ok {
			t.Errorf("missing ref %s; refs = %v", want, res.References)
			continue
		}
		if r.From != "site/index.html" {
			t.Errorf("%s From = %q, want file path", want, r.From)
		}
		if r.Kind != extract.RefStyles {
			t.Errorf("%s Kind = %q, want styles", want, r.Kind)
		}
		if !r.Qualified {
			t.Errorf("%s must be Qualified (candidate cross-file)", want)
		}
	}
	if btn := byTo[".btn"]; btn.Line != 4 {
		t.Errorf(".btn line = %d, want 4 (first occurrence)", btn.Line)
	}
	// Commented-out markup, template placeholders, and hyphenated data-*
	// attributes (data-id/data-class) never become references; duplicates
	// collapse.
	for _, dead := range []string{"#row-7", ".phantom"} {
		if _, ok := byTo[dead]; ok {
			t.Errorf("data-* attribute produced a ref: %s", dead)
		}
	}
	if len(res.References) != 3 {
		t.Errorf("refs = %v, want exactly [.btn .card-title #hero]", res.References)
	}
}
