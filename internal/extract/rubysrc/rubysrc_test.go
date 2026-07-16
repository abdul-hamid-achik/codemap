package rubysrc

import (
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/extract"
)

const sample = `require "json"
require_relative "helpers/format"

# Billing wraps invoice math.
module Billing
  # Invoice totals line items.
  class Invoice
    def initialize(items)
      @items = items
      normalize!(items)
    end

    # total sums the line items.
    def total
      @items.sum { |i| i.price }
      format_cents(subtotal)
    end

    def subtotal = @items.sum(&:price)

    def self.build(items)
      Invoice.new(items)
    end

    private

    def normalize!(items)
      items.compact
    end
  end

  def self.format_cents(cents)
    "%.2f" % (cents / 100.0)
  end
end
`

func findSym(syms []extract.Symbol, fqn string) *extract.Symbol {
	for i := range syms {
		if syms[i].FQN == fqn {
			return &syms[i]
		}
	}
	return nil
}

func hasRef(refs []extract.Reference, from, to, kind string) bool {
	for _, r := range refs {
		if r.From == from && r.To == to && r.Kind == kind {
			return true
		}
	}
	return false
}

func TestExtractRuby(t *testing.T) {
	res, err := New().ExtractFile("lib/billing.rb", []byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if res.Language != "ruby" {
		t.Fatalf("language = %q", res.Language)
	}

	// imports
	imp := map[string]bool{}
	for _, s := range res.Imports {
		imp[s] = true
	}
	if !imp["json"] || !imp["./helpers/format"] {
		t.Errorf("imports = %v, want json and ./helpers/format", res.Imports)
	}

	// containers
	mod := findSym(res.Symbols, "Billing")
	if mod == nil || mod.Kind != extract.KindModule {
		t.Fatalf("Billing module missing/wrong: %+v", mod)
	}
	if mod.Docstring == "" {
		t.Errorf("Billing docstring missing")
	}
	cls := findSym(res.Symbols, "Billing.Invoice")
	if cls == nil || cls.Kind != extract.KindClass {
		t.Fatalf("Billing.Invoice class missing/wrong: %+v", cls)
	}

	// instance method, class method, endless method
	for _, fqn := range []string{"Billing.Invoice.initialize", "Billing.Invoice.total", "Billing.Invoice.subtotal", "Billing.Invoice.build", "Billing.format_cents"} {
		s := findSym(res.Symbols, fqn)
		if s == nil {
			t.Fatalf("missing symbol %s (have %v)", fqn, symNames(res.Symbols))
		}
		if s.Kind != extract.KindMethod {
			t.Errorf("%s kind = %q, want method", fqn, s.Kind)
		}
	}

	// end-balanced ranges: total spans its body
	total := findSym(res.Symbols, "Billing.Invoice.total")
	if total.StartLine >= total.EndLine {
		t.Errorf("total range = %d..%d, want a multi-line span", total.StartLine, total.EndLine)
	}
	if cls.EndLine <= total.EndLine {
		t.Errorf("class end %d should be after method end %d", cls.EndLine, total.EndLine)
	}

	// name-based call references
	if !hasRef(res.References, "Billing.Invoice.initialize", "normalize!", extract.RefCalls) {
		t.Errorf("missing call initialize → normalize! (refs=%v)", res.References)
	}
	if !hasRef(res.References, "Billing.Invoice.total", "format_cents", extract.RefCalls) {
		t.Errorf("missing call total → format_cents")
	}
	// Foo.new instantiation is a value reference to the class.
	if !hasRef(res.References, "Billing.Invoice.build", "Invoice", extract.RefReferences) {
		t.Errorf("missing instantiation reference build → Invoice")
	}
	// keywords never become callees
	for _, r := range res.References {
		if rubyKeywords[r.To] {
			t.Errorf("keyword leaked as reference: %+v", r)
		}
	}
}

func TestRubyTestKind(t *testing.T) {
	src := "class InvoiceTest\n  def test_total\n    assert_equal(1, 1)\n  end\nend\n"
	res, err := New().ExtractFile("test/invoice_test.rb", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	s := findSym(res.Symbols, "InvoiceTest.test_total")
	if s == nil || s.Kind != extract.KindTest {
		t.Errorf("test file def kind = %+v, want test", s)
	}
}

func TestRubySetterNotEndless(t *testing.T) {
	src := "class C\n  def name=(v)\n    @name = v\n    audit(v)\n  end\nend\n"
	res, err := New().ExtractFile("c.rb", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	s := findSym(res.Symbols, "C.name=")
	if s == nil {
		t.Fatalf("setter missing: %v", symNames(res.Symbols))
	}
	if s.EndLine <= s.StartLine {
		t.Errorf("setter treated as endless; range %d..%d", s.StartLine, s.EndLine)
	}
	if !hasRef(res.References, "C.name=", "audit", extract.RefCalls) {
		t.Errorf("setter body call missing: %v", res.References)
	}
}

func TestRubyCommentsAndStrings(t *testing.T) {
	src := "def greet\n  msg = \"call fake_call(1) # not a comment\"\n  real_call(msg) # trailing comment(ignored)\nend\n"
	res, err := New().ExtractFile("g.rb", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if !hasRef(res.References, "greet", "real_call", extract.RefCalls) {
		t.Errorf("missing real_call ref: %v", res.References)
	}
	for _, r := range res.References {
		if r.To == "ignored" {
			t.Errorf("comment content leaked into refs: %+v", r)
		}
	}
}

func symNames(syms []extract.Symbol) []string {
	out := make([]string, len(syms))
	for i, s := range syms {
		out[i] = s.FQN
	}
	return out
}

// TestRubyHeredocContentIgnored pins heredoc handling: body lines are content,
// not code — a def/end/call inside one must not shape symbols, frames, or
// references, even when the content sits at column 0 (where the old
// indent-based end matching would close the enclosing frames early).
func TestRubyHeredocContentIgnored(t *testing.T) {
	src := `class Report
  def render
    body = <<~SQL
      SELECT phantom_call(1)
      end
    SQL
    plain = <<TXT
def phantom
end
helper_in_heredoc(2)
TXT
    format(body)
  end
end
`
	res, err := New().ExtractFile("h.rb", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	names := symNames(res.Symbols)
	if len(res.Symbols) != 2 {
		t.Fatalf("symbols = %v, want [Report Report.render]", names)
	}
	render := res.Symbols[1]
	if render.FQN != "Report.render" || render.StartLine != 2 || render.EndLine != 13 {
		t.Errorf("render span = %s %d-%d, want Report.render 2-13", render.FQN, render.StartLine, render.EndLine)
	}
	if !hasRef(res.References, "Report.render", "format", extract.RefCalls) {
		t.Errorf("missing format call: %v", res.References)
	}
	for _, dead := range []string{"phantom_call", "helper_in_heredoc"} {
		for _, r := range res.References {
			if r.To == dead {
				t.Errorf("heredoc content leaked into refs: %+v", r)
			}
		}
	}
}

// TestRubyQuotedHeredocAndMultiple: quoted terminators (<<~'EOS') and two
// heredocs opened on one line both consume their bodies in order.
func TestRubyQuotedHeredocAndMultiple(t *testing.T) {
	src := `def pair
  compare(<<~A, <<~'B')
    left_phantom(1)
  A
    right_phantom(2)
  B
  done()
end
`
	res, err := New().ExtractFile("q.rb", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if !hasRef(res.References, "pair", "compare", extract.RefCalls) || !hasRef(res.References, "pair", "done", extract.RefCalls) {
		t.Errorf("missing real calls: %v", res.References)
	}
	for _, r := range res.References {
		if r.To == "left_phantom" || r.To == "right_phantom" {
			t.Errorf("heredoc content leaked: %+v", r)
		}
	}
	if res.Symbols[0].EndLine != 8 {
		t.Errorf("pair EndLine = %d, want 8", res.Symbols[0].EndLine)
	}
}

// TestRubyEqBeginBlockIgnored: =begin/=end embedded documentation is comment
// content; a def inside it is not a symbol.
func TestRubyEqBeginBlockIgnored(t *testing.T) {
	src := "=begin\ndef phantom\n  fake_call(1)\nend\n=end\ndef real\n  live_call(2)\nend\n"
	res, err := New().ExtractFile("e.rb", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Symbols) != 1 || res.Symbols[0].FQN != "real" {
		t.Fatalf("symbols = %v, want [real]", symNames(res.Symbols))
	}
	if !hasRef(res.References, "real", "live_call", extract.RefCalls) {
		t.Errorf("missing live_call: %v", res.References)
	}
	for _, r := range res.References {
		if r.To == "fake_call" {
			t.Errorf("commented-out code leaked: %+v", r)
		}
	}
}

// TestRubyModifierDef: `private def x` declares x (idiomatic Rails); its body
// references attribute to the method, not the enclosing class.
func TestRubyModifierDef(t *testing.T) {
	src := `class K
  private def hidden
    secret_call(1)
  end
  module_function def helper
    other_call(2)
  end
end
`
	res, err := New().ExtractFile("m.rb", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	names := symNames(res.Symbols)
	want := map[string]bool{"K": true, "K.hidden": true, "K.helper": true}
	for n := range want {
		found := false
		for _, got := range names {
			if got == n {
				found = true
			}
		}
		if !found {
			t.Errorf("missing symbol %s in %v", n, names)
		}
	}
	if !hasRef(res.References, "K.hidden", "secret_call", extract.RefCalls) {
		t.Errorf("secret_call not attributed to K.hidden: %v", res.References)
	}
}

// TestRubyStringContentsNotCalls: parens inside plain string data are not
// calls, while #{...} interpolation bodies are code and keep theirs.
func TestRubyStringContentsNotCalls(t *testing.T) {
	src := "def greet(user)\n  msg = \"Hello #{format_name(user)} fake(1)\"\n  log('literal foo(2)')\nend\n"
	res, err := New().ExtractFile("s.rb", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if !hasRef(res.References, "greet", "format_name", extract.RefCalls) {
		t.Errorf("interpolated call lost: %v", res.References)
	}
	if !hasRef(res.References, "greet", "log", extract.RefCalls) {
		t.Errorf("missing log call: %v", res.References)
	}
	for _, r := range res.References {
		if r.To == "fake" || r.To == "foo" {
			t.Errorf("string content leaked into refs: %+v", r)
		}
	}
}
