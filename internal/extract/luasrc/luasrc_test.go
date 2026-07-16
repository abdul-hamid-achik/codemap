package luasrc

import (
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/extract"
)

const sample = `local util = require("app.util")
local json = require "vendor.json"

local M = {}

-- greet builds a greeting.
function M.greet(name)
  local upper = util.upcase(name)
  if upper == "" then
    return fallback()
  end
  return "hi " .. upper
end

-- Session is a method-style module.
function M:reset()
  self.count = 0
  M.greet("world")
end

M.helper = function(x)
  return json.encode(x)
end

local function fallback()
  return "anon"
end

return M
`

func findSym(syms []extract.Symbol, fqn string) *extract.Symbol {
	for i := range syms {
		if syms[i].FQN == fqn {
			return &syms[i]
		}
	}
	return nil
}

func hasRef(refs []extract.Reference, from, to string) bool {
	for _, r := range refs {
		if r.From == from && r.To == to {
			return true
		}
	}
	return false
}

func TestExtractLua(t *testing.T) {
	res, err := New().ExtractFile("lua/app/init.lua", []byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if res.Language != "lua" {
		t.Fatalf("language = %q", res.Language)
	}

	// imports (both require forms)
	imp := map[string]bool{}
	for _, s := range res.Imports {
		imp[s] = true
	}
	if !imp["app.util"] || !imp["vendor.json"] {
		t.Errorf("imports = %v, want app.util and vendor.json", res.Imports)
	}

	// module-field function, method-style, assignment form, local function
	greet := findSym(res.Symbols, "M.greet")
	if greet == nil || greet.Kind != extract.KindFunction {
		t.Fatalf("M.greet missing/wrong: %+v", greet)
	}
	if greet.Docstring == "" {
		t.Errorf("M.greet docstring missing")
	}
	if greet.StartLine >= greet.EndLine {
		t.Errorf("M.greet range = %d..%d, want multi-line (end-balanced through the if block)", greet.StartLine, greet.EndLine)
	}
	reset := findSym(res.Symbols, "M.reset")
	if reset == nil || reset.Kind != extract.KindMethod {
		t.Fatalf("M:reset missing or not a method: %+v", reset)
	}
	if s := findSym(res.Symbols, "M.helper"); s == nil {
		t.Fatalf("M.helper = function() form missing: %+v", symNames(res.Symbols))
	}
	if s := findSym(res.Symbols, "fallback"); s == nil {
		t.Fatalf("local function fallback missing")
	}

	// call references with correct attribution
	if !hasRef(res.References, "M.greet", "upcase") {
		t.Errorf("missing M.greet → upcase (refs=%v)", res.References)
	}
	if !hasRef(res.References, "M.greet", "fallback") {
		t.Errorf("missing M.greet → fallback")
	}
	if !hasRef(res.References, "M.reset", "greet") {
		t.Errorf("missing M.reset → greet")
	}
	if !hasRef(res.References, "M.helper", "encode") {
		t.Errorf("missing M.helper → encode")
	}
	for _, r := range res.References {
		if luaKeywords[r.To] {
			t.Errorf("keyword leaked as reference: %+v", r)
		}
	}
}

func TestLuaCommentsAndStrings(t *testing.T) {
	src := "--[[ block comment fake_one() ]]\nfunction go()\n  -- fake_two()\n  local s = \"fake_three()\"\n  real()\nend\n"
	res, err := New().ExtractFile("x.lua", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if !hasRef(res.References, "go", "real") {
		t.Errorf("missing go → real: %v", res.References)
	}
	for _, bad := range []string{"fake_one", "fake_two", "fake_three"} {
		if hasRef(res.References, "go", bad) || hasRef(res.References, "x.lua", bad) {
			t.Errorf("comment/string content leaked: %s", bad)
		}
	}
}

func TestLuaTestKind(t *testing.T) {
	res, err := New().ExtractFile("spec/util_spec.lua", []byte("function describe_case()\n  return 1\nend\n"))
	if err != nil {
		t.Fatal(err)
	}
	if s := findSym(res.Symbols, "describe_case"); s == nil || s.Kind != extract.KindTest {
		t.Errorf("spec file function kind = %+v, want test", s)
	}
}

func symNames(syms []extract.Symbol) []string {
	out := make([]string, len(syms))
	for i, s := range syms {
		out[i] = s.FQN
	}
	return out
}

// TestLuaMultilineLongString pins long-string state across lines: [[ ... ]]
// content is data — calls inside it are not references, an `end` inside it
// must not close the block tracking, and code after the closer indexes
// normally.
func TestLuaMultilineLongString(t *testing.T) {
	src := `local M = {}

local usage = [[
codemap index --precise
if something then end
phantom_call(x)
end
]]

function M.run()
  real_call(1)
end

return M
`
	res, err := New().ExtractFile("ls.lua", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Symbols) != 1 || res.Symbols[0].FQN != "M.run" {
		t.Fatalf("symbols = %v, want [M.run]", res.Symbols)
	}
	if res.Symbols[0].StartLine != 10 || res.Symbols[0].EndLine != 12 {
		t.Errorf("M.run span = %d-%d, want 10-12", res.Symbols[0].StartLine, res.Symbols[0].EndLine)
	}
	if !hasRef(res.References, "M.run", "real_call") {
		t.Errorf("missing M.run → real_call: %v", res.References)
	}
	for _, r := range res.References {
		if r.To == "phantom_call" {
			t.Errorf("long-string content leaked into refs: %+v", r)
		}
	}
}

// TestLuaRequireNotInComments: a require inside a trailing or block comment is
// not an import; real requires (paren and paren-less) still are.
func TestLuaRequireNotInComments(t *testing.T) {
	src := `local a = 1 -- require "phantom.one"
--[[ require "phantom.two" ]]
local real = require("app.real")
local terse = require "app.terse"
`
	res, err := New().ExtractFile("rq.lua", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"app.real", "app.terse"}
	if len(res.Imports) != len(want) || res.Imports[0] != want[0] || res.Imports[1] != want[1] {
		t.Errorf("imports = %v, want %v", res.Imports, want)
	}
}
