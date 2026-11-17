package lspsrc

// LangBinding maps a codemap language id to the LSP languageId sent in didOpen.
// One server can serve several (typescript-language-server handles both TS and
// JS); Lang must match what the extension detector assigns (see
// internal/extract.LanguageForPath) so the indexer routes those files here.
type LangBinding struct {
	Lang   string // codemap language id, e.g. "javascript"
	LangID string // LSP languageId in didOpen, e.g. "javascript"
}

// ServerSpec describes a language server codemap can drive to extract structure.
// One server process can back multiple languages (Langs) — the indexer spawns it
// once and registers an extractor per language, all sharing the connection.
type ServerSpec struct {
	Cmd   string        // server binary, resolved on PATH
	Args  []string      // server args, usually --stdio
	Langs []LangBinding // languages this one server handles
}

// DefaultServers are the language servers codemap auto-registers when the binary
// is on PATH and the project actually contains files of that language. A single
// typescript-language-server serves both TypeScript and JavaScript (.ts/.tsx and
// .js/.jsx/.mjs/.cjs); codemap spawns it once and routes each file with its own
// languageId. Python uses pyright-langserver (a separate process).
// Rust uses rust-analyzer when installed: it speaks LSP over stdio with no
// arguments and serves .rs files (codemap language "rust"). rust-analyzer
// answers fully only when the project (or an ancestor) is a Cargo workspace;
// outside one it degrades to empty/partial answers — codemap surfaces that as
// visible per-file degradation, never a hang (requests are bounded like the
// other servers).
var DefaultServers = []ServerSpec{
	{
		Cmd:  "typescript-language-server",
		Args: []string{"--stdio"},
		Langs: []LangBinding{
			{Lang: "typescript", LangID: "typescript"},
			{Lang: "javascript", LangID: "javascript"},
		},
	},
	{
		Cmd:   "pyright-langserver",
		Args:  []string{"--stdio"},
		Langs: []LangBinding{{Lang: "python", LangID: "python"}},
	},
	{
		Cmd:   "rust-analyzer",
		Langs: []LangBinding{{Lang: "rust", LangID: "rust"}},
	},
}
