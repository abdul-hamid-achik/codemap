package lspsrc

import (
	"testing"
)

// TestRustAnalyzerSpecRegistered pins the rust-analyzer server spec: stdio LSP
// with no CLI arguments, serving the codemap "rust" language (rust → rust).
func TestRustAnalyzerSpecRegistered(t *testing.T) {
	var spec *ServerSpec
	for i := range DefaultServers {
		if DefaultServers[i].Cmd == "rust-analyzer" {
			spec = &DefaultServers[i]
		}
	}
	if spec == nil {
		t.Fatal("rust-analyzer missing from DefaultServers")
	}
	if len(spec.Args) != 0 {
		t.Errorf("rust-analyzer speaks stdio LSP with no args, got %v", spec.Args)
	}
	if len(spec.Langs) != 1 || spec.Langs[0].Lang != "rust" || spec.Langs[0].LangID != "rust" {
		t.Errorf("rust-analyzer langs = %v, want [{rust rust}]", spec.Langs)
	}
}

// TestHasDeclarationsRust keeps the empty-answer retry heuristic honest for
// Rust sources: a file that declares anything must be retried when the server
// answers documentSymbol empty while still parsing.
func TestHasDeclarationsRust(t *testing.T) {
	positive := []string{
		"pub fn main() {}",
		"struct User { name: String }",
		"impl User {}",
		"trait Shape {}",
		"mod models;",
		"enum Color { Red }",
	}
	for _, src := range positive {
		if !hasDeclarations([]byte(src)) {
			t.Errorf("hasDeclarations(%q) = false, want true", src)
		}
	}
	if hasDeclarations([]byte("// just a comment\nuse std::io;\n")) {
		// `use` alone declares nothing; "struct " with trailing space avoids
		// false matches. A use-only file may legitimately be empty.
		t.Error("hasDeclarations on a use-only file = true, want false (no declaration keyword)")
	}
}
