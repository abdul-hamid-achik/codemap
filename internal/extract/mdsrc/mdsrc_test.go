package mdsrc

import "testing"

func TestMarkdownSectionsAndRealLinks(t *testing.T) {
	source := "---\ntitle: Example\n---\n# Getting started\n[Code](../main.go)\n\n```go\n# fake\n[not a link](../fake.go)\nfunc Fake() {}\n```\n## Install\n[Back](#getting-started) [External](https://example.com)\n## Install\n[escape](../../outside.go)\n"
	r, err := New().ExtractFile("docs/guide.md", []byte(source))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Symbols) != 3 || len(r.References) != 2 {
		t.Fatalf("result=%+v", r)
	}
	if r.Symbols[0].StartLine != 4 || r.Symbols[1].FQN == r.Symbols[2].FQN {
		t.Fatalf("sections=%+v", r.Symbols)
	}
	if r.References[0].ToFQN != "main.go" || r.References[1].ToFQN != "docs/guide.md#section/getting-started" {
		t.Fatalf("refs=%+v", r.References)
	}
	plain, err := New().ExtractFile("README.md", []byte("No headings, still searchable."))
	if err != nil || len(plain.Symbols) != 1 {
		t.Fatal("missing prose fallback")
	}
}

func TestEmptyHeadingDoesNotPanic(t *testing.T) {
	if _, err := New().ExtractFile("empty.md", []byte("#\n\n##\n")); err != nil {
		t.Fatal(err)
	}
}
