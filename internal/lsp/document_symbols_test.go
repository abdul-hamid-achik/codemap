package lsp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodeDocumentSymbolsHierarchical(t *testing.T) {
	raw := json.RawMessage(`[
  {
    "name": "Service",
    "kind": 5,
    "range": {"start":{"line":1,"character":0},"end":{"line":5,"character":1}},
    "selectionRange": {"start":{"line":1,"character":6},"end":{"line":1,"character":13}},
    "children": [{
      "name": "run",
      "detail": "(): void",
      "kind": 6,
      "range": {"start":{"line":2,"character":2},"end":{"line":4,"character":3}},
      "selectionRange": {"start":{"line":2,"character":2},"end":{"line":2,"character":5}}
    }]
  }
]`)

	syms, err := decodeDocumentSymbols(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(syms) != 1 || len(syms[0].Children) != 1 {
		t.Fatalf("symbols = %+v, want one parent with one child", syms)
	}
	child := syms[0].Children[0]
	if syms[0].Name != "Service" || child.Name != "run" || child.Detail != "(): void" {
		t.Fatalf("hierarchical normalization lost fields: %+v", syms)
	}
}

func TestDecodeDocumentSymbolsFlatPreservesContainer(t *testing.T) {
	raw := json.RawMessage(`[
  {
    "name": "run",
    "kind": 6,
    "location": {
      "uri": "file:///project/service.ts",
      "range": {"start":{"line":7,"character":2},"end":{"line":7,"character":5}}
    },
    "containerName": "Service"
  }
]`)

	syms, err := decodeDocumentSymbols(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(syms) != 1 {
		t.Fatalf("symbols = %+v, want one", syms)
	}
	if got := syms[0]; got.Name != "run" || got.ContainerName != "Service" || got.Range.Start.Line != 7 || got.SelectionRange != got.Range {
		t.Fatalf("flat normalization = %+v", got)
	}
}

func TestDecodeDocumentSymbolsRejectsMalformedResponses(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "mixed variants",
			raw: `[
  {"name":"a","kind":12,"range":{"start":{},"end":{}},"selectionRange":{"start":{},"end":{}}},
  {"name":"b","kind":12,"location":{"uri":"file:///b.ts","range":{"start":{},"end":{}}}}
]`,
			want: "mixed documentSymbol and symbolInformation",
		},
		{
			name: "hierarchical missing selection",
			raw:  `[{"name":"a","kind":12,"range":{"start":{},"end":{}}}]`,
			want: "missing range or selectionRange",
		},
		{
			name: "flat missing location range",
			raw:  `[{"name":"a","kind":12,"location":{"uri":"file:///a.ts"}}]`,
			want: "missing location uri or range",
		},
		{
			name: "backwards range",
			raw: `[{"name":"a","kind":12,
  "range":{"start":{"line":3},"end":{"line":2}},
  "selectionRange":{"start":{"line":3},"end":{"line":3}}
}]`,
			want: "range ends before it starts",
		},
		{
			name: "unknown shape",
			raw:  `[{"name":"a","kind":12}]`,
			want: "missing range or location",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decodeDocumentSymbols(json.RawMessage(tc.raw))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want substring %q", err, tc.want)
			}
		})
	}
}
