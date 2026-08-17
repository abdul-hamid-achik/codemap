package extract

import (
	"path/filepath"
	"strings"
)

// languageByExtension is the recognition catalog, not a support registry.
// Presence here means codemap can identify and report the file as unsupported;
// it does not imply an extractor, language server, call graph, or any confidence
// tier beyond T0. A language becomes indexable only when the indexer explicitly
// registers a backend for its id.
var languageByExtension = map[string]string{
	// Current structural backends.
	".go":  "go",
	".ts":  "typescript",
	".tsx": "typescript",
	".mts": "typescript",
	".cts": "typescript",
	".js":  "javascript",
	".jsx": "javascript",
	".mjs": "javascript",
	".cjs": "javascript",
	".py":  "python",
	".pyw": "python",
	".pyi": "python",
	".vue": "vue",

	// Existing recognized-only languages and markup.
	".lua":  "lua",
	".rb":   "ruby",
	".gd":   "gdscript",
	".html": "html",
	".htm":  "html",
	".css":  "css",
	".scss": "scss",
	".sass": "sass",
	".less": "less",

	// Wave 1: compiler/LSP/SCIP candidates. Recognition remains T0 only.
	".rs":    "rust",
	".java":  "java",
	".kt":    "kotlin",
	".kts":   "kotlin",
	".scala": "scala",
	".c":     "c",
	".h":     "c",
	".cc":    "cpp",
	".cpp":   "cpp",
	".cxx":   "cpp",
	".c++":   "cpp",
	".hh":    "cpp",
	".hpp":   "cpp",
	".hxx":   "cpp",
	".ipp":   "cpp",
	".tpp":   "cpp",
	".cu":    "cuda",
	".cuh":   "cuda",
	".cs":    "csharp",
	".csx":   "csharp",
	".vb":    "visualbasic",

	// Wave 2/3: additional SCIP/LSP candidates.
	".php":   "php",
	".phtml": "php",
	".dart":  "dart",
	".swift": "swift",
	".ex":    "elixir",
	".exs":   "elixir",

	// Wave 4: containers and long-tail structural formats.
	".svelte": "svelte",
	".astro":  "astro",
	".razor":  "razor",
	".cshtml": "razor",
	".vbhtml": "razor",
	".sh":     "shell",
	".bash":   "shell",
	".zsh":    "shell",
	".ksh":    "shell",
	".fish":   "shell",
	".hcl":    "hcl",
	".tf":     "terraform",
	".tfvars": "terraform",
	".sql":    "sql",
	".yaml":   "yaml",
	".yml":    "yaml",
}

// LanguageForPath maps a file path to a stable codemap language id, or "" if
// unknown. Matching is case-insensitive. Recognition alone never registers a
// backend; the indexer reports a recognized file with no extractor as skipped
// and unsupported.
func LanguageForPath(path string) string {
	lower := strings.ToLower(filepath.ToSlash(path))
	if strings.HasSuffix(lower, ".tfvars.json") || strings.HasSuffix(lower, ".tf.json") {
		return "terraform"
	}
	if language := languageByExtension[strings.ToLower(filepath.Ext(lower))]; language != "" {
		return language
	}
	base := strings.ToLower(filepath.Base(lower))
	switch {
	case base == "makefile" || base == "gnumakefile":
		return "shell"
	case base == "dockerfile" || strings.HasPrefix(base, "dockerfile."):
		return "shell"
	case base == "jenkinsfile":
		return "shell"
	default:
		return ""
	}
}
