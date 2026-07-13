package lspsrc

import (
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/extract"
	"github.com/abdul-hamid-achik/codemap/internal/lsp"
)

func TestAppendFlatSymbolUsesContainerNameForFQN(t *testing.T) {
	s := lsp.DocumentSymbol{
		Name:          "run",
		Kind:          lsp.SymbolMethod,
		ContainerName: "Service",
		Range: lsp.Range{
			Start: lsp.Position{Line: 1},
			End:   lsp.Position{Line: 2},
		},
	}
	res := &extract.FileResult{Path: "service.ts", Language: "typescript"}
	appendSymbols(res, []string{"class Service {", "run() {}", "}"}, "typescript", "", false, res.Path, s)

	if len(res.Symbols) != 1 {
		t.Fatalf("symbols = %+v, want one", res.Symbols)
	}
	if got := res.Symbols[0].FQN; got != "Service.run" {
		t.Fatalf("flat symbol FQN = %q, want Service.run", got)
	}
	if got := symbolFQN("", s); got != "Service.run" {
		t.Fatalf("call-edge FQN = %q, want Service.run", got)
	}
}

func TestHierarchicalParentWinsOverFlatContainer(t *testing.T) {
	s := lsp.DocumentSymbol{Name: "run", ContainerName: "stale-container"}
	if got := symbolFQN("ActualService", s); got != "ActualService.run" {
		t.Fatalf("hierarchical FQN = %q, want ActualService.run", got)
	}
}
