package extract

import "testing"

// TestLanguageForPath pins every recognized extension to its stable language
// id. Some ids have structural backends and some are recognition-only T0; the
// mapping deliberately makes no support claim.
func TestLanguageForPath(t *testing.T) {
	cases := map[string]string{
		"pkg/a.go":            "go",
		"web/a.ts":            "typescript",
		"web/a.tsx":           "typescript",
		"web/a.mts":           "typescript",
		"web/a.cts":           "typescript",
		"web/a.js":            "javascript",
		"web/a.jsx":           "javascript",
		"web/a.mjs":           "javascript",
		"web/a.cjs":           "javascript",
		"src/a.py":            "python",
		"src/a.pyw":           "python",
		"src/a.pyi":           "python",
		"src/a.vue":           "vue",
		"src/a.lua":           "lua",
		"src/a.rb":            "ruby",
		"web/a.html":          "html",
		"web/a.htm":           "html",
		"web/a.css":           "css",
		"src/a.rs":            "rust",
		"src/A.java":          "java",
		"src/A.kt":            "kotlin",
		"build/a.kts":         "kotlin",
		"src/A.scala":         "scala",
		"src/a.c":             "c",
		"include/a.h":         "c",
		"src/a.cc":            "cpp",
		"src/a.cpp":           "cpp",
		"src/a.cxx":           "cpp",
		"src/a.c++":           "cpp",
		"include/a.hh":        "cpp",
		"include/a.hpp":       "cpp",
		"include/a.hxx":       "cpp",
		"include/a.ipp":       "cpp",
		"include/a.tpp":       "cpp",
		"src/a.cu":            "cuda",
		"include/a.cuh":       "cuda",
		"src/A.cs":            "csharp",
		"scripts/a.csx":       "csharp",
		"src/A.vb":            "visualbasic",
		"web/a.php":           "php",
		"web/a.phtml":         "php",
		"lib/a.dart":          "dart",
		"Sources/a.swift":     "swift",
		"lib/a.ex":            "elixir",
		"test/a.exs":          "elixir",
		"web/A.svelte":        "svelte",
		"web/A.astro":         "astro",
		"web/A.razor":         "razor",
		"web/A.cshtml":        "razor",
		"web/A.vbhtml":        "razor",
		"scripts/a.sh":        "shell",
		"scripts/a.bash":      "shell",
		"scripts/a.zsh":       "shell",
		"scripts/a.ksh":       "shell",
		"scripts/a.fish":      "shell",
		"infra/a.hcl":         "hcl",
		"infra/a.tf":          "terraform",
		"infra/a.tfvars":      "terraform",
		"infra/a.tf.json":     "terraform",
		"infra/a.tfvars.json": "terraform",
		"Makefile":            "shell",
		"Dockerfile.dev":      "shell",
		"Jenkinsfile":         "shell",
		"db/a.sql":            "sql",
		"config/a.yaml":       "yaml",
		"config/a.yml":        "yaml",
	}
	for path, want := range cases {
		if got := LanguageForPath(path); got != want {
			t.Errorf("LanguageForPath(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestLanguageForPathIsCaseInsensitive(t *testing.T) {
	cases := map[string]string{
		"SRC/MAIN.RS":     "rust",
		"SRC/MAIN.CPP":    "cpp",
		"WEB/APP.SVELTE":  "svelte",
		"WEB/VIEW.CSHTML": "razor",
		"CONFIG/APP.YAML": "yaml",
	}
	for path, want := range cases {
		if got := LanguageForPath(path); got != want {
			t.Errorf("LanguageForPath(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestLanguageForPathUnknown(t *testing.T) {
	for _, path := range []string{"README.md", "data.json", "Cargo.toml", "noext", ".gitignore"} {
		if got := LanguageForPath(path); got != "" {
			t.Errorf("LanguageForPath(%q) = %q, want unknown", path, got)
		}
	}
}
