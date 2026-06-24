package lspsrc

// ServerSpec describes a language server codemap can drive to extract structure.
// Lang must match the codemap language id the extension detector assigns (see
// internal/extract.LanguageForPath), so the indexer routes those files here.
type ServerSpec struct {
	Lang   string   // codemap language id, e.g. "typescript"
	LangID string   // LSP languageId sent in didOpen, e.g. "typescript"
	Cmd    string   // server binary, resolved on PATH
	Args   []string // server args, usually --stdio
}

// DefaultServers are the language servers codemap auto-registers when the binary
// is on PATH and the project actually contains files of that language. TypeScript
// first; JavaScript and Python are one-row additions in later slices.
var DefaultServers = []ServerSpec{
	{Lang: "typescript", LangID: "typescript", Cmd: "typescript-language-server", Args: []string{"--stdio"}},
}
