package index

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/extract"
	"github.com/abdul-hamid-achik/codemap/internal/extract/csssrc"
	"github.com/abdul-hamid-achik/codemap/internal/extract/gdsrc"
	"github.com/abdul-hamid-achik/codemap/internal/extract/gosrc"
	"github.com/abdul-hamid-achik/codemap/internal/extract/htmlsrc"
	"github.com/abdul-hamid-achik/codemap/internal/extract/lspsrc"
	"github.com/abdul-hamid-achik/codemap/internal/extract/luasrc"
	"github.com/abdul-hamid-achik/codemap/internal/extract/rubysrc"
	"github.com/abdul-hamid-achik/codemap/internal/extract/vuesrc"
)

// inflightExtractor counts overlapping ExtractFile calls so a test can prove
// cheap backends share the extract-concurrency pool instead of a limit-1
// language-server queue.
type inflightExtractor struct {
	extract.Extractor
	cur, max *int64
}

func (e *inflightExtractor) ExtractFile(rel string, src []byte) (*extract.FileResult, error) {
	n := atomic.AddInt64(e.cur, 1)
	defer atomic.AddInt64(e.cur, -1)
	for {
		old := atomic.LoadInt64(e.max)
		if n <= old || atomic.CompareAndSwapInt64(e.max, old, n) {
			break
		}
	}
	time.Sleep(30 * time.Millisecond)
	return e.Extractor.ExtractFile(rel, src)
}

func TestCheapNonLSPExtractsInParallel(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "package app\n\nfunc Main() {}\n")
	writeFile(t, dir, "a.rb", "def alpha\n  1\nend\n")
	writeFile(t, dir, "b.rb", "def beta\n  2\nend\n")
	writeFile(t, dir, "a.lua", "function alpha()\n  return 1\nend\n")
	writeFile(t, dir, "b.lua", "function beta()\n  return 2\nend\n")
	writeFile(t, dir, "app.css", ".btn { color: red; }\n")
	writeFile(t, dir, "index.html", `<div class="btn">x</div>`+"\n")
	writeFile(t, dir, "player.gd", "extends Node\nfunc _ready():\n\tpass\n")

	g, _ := newStores(t)
	pid, err := g.UpsertProject("mixed", dir, "mixed")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig().Index
	cfg.ExtractConcurrency = 4
	ix := New(g, nil, nil, cfg)

	var cur, max int64
	wrap := func(e extract.Extractor) extract.Extractor {
		return &inflightExtractor{Extractor: e, cur: &cur, max: &max}
	}
	// Wrap only non-Go cheap backends. If they still sit on the serial LSP
	// queue, max in-flight stays 1 even with ExtractConcurrency=4.
	ix.Register(wrap(rubysrc.New()))
	ix.Register(wrap(luasrc.New()))
	ix.Register(wrap(csssrc.New("css")))
	ix.Register(wrap(htmlsrc.New()))
	ix.Register(wrap(gdsrc.New()))

	res, err := ix.IndexProject(context.Background(), pid, "mixed", dir, Options{NoLSP: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.FilesIndexed < 7 {
		t.Fatalf("FilesIndexed = %d, want at least 7 cheap files", res.FilesIndexed)
	}
	if got := atomic.LoadInt64(&max); got < 2 {
		t.Fatalf("max in-flight cheap ExtractFile = %d, want >= 2 (Ruby/Lua/CSS/HTML/GDScript must share extract-concurrency, not a limit-1 LSP queue)", got)
	}
}

func TestUsesLanguageServerByExtractorNotGoOnly(t *testing.T) {
	cheap := []fileTask{
		{lang: "go", ext: gosrc.New()},
		{lang: "ruby", ext: rubysrc.New()},
		{lang: "lua", ext: luasrc.New()},
		{lang: "css", ext: csssrc.New("css")},
		{lang: "html", ext: htmlsrc.New()},
		{lang: "gdscript", ext: gdsrc.New()},
	}
	for _, ft := range cheap {
		if usesLanguageServer(ft) {
			t.Errorf("%s classified as language-server extract, want cheap pool", ft.lang)
		}
	}
	lsp := []fileTask{
		{lang: "typescript"},
		{lang: "javascript"},
		{lang: "python"},
		{lang: "vue"},
		{lang: "typescript", ext: &lspsrc.Extractor{}},
		{lang: "vue", ext: vuesrc.New(nil, nil)},
	}
	for _, ft := range lsp {
		if !usesLanguageServer(ft) {
			t.Errorf("%s classified as cheap extract, want language-server serial queue", ft.lang)
		}
	}
}
