package index

import (
	"context"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

// TestIndexRubyAndLua proves the pure-Go Ruby/Lua backends are registered by
// default and produce the full T1 shape end-to-end: definition nodes,
// name-resolved call edges, and file→file imports edges — with no language
// server involved.
func TestIndexRubyAndLua(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "lib/billing.rb", `require_relative "money"

module Billing
  class Invoice
    def total
      to_cents(100)
    end
  end
end
`)
	writeFile(t, dir, "lib/money.rb", `def to_cents(amount)
  amount * 100
end
`)
	writeFile(t, dir, "lua/app/init.lua", `local util = require("app.util")

local M = {}

function M.run()
  return util.shout("go")
end

return M
`)
	writeFile(t, dir, "lua/app/util.lua", `local M = {}

function M.shout(s)
  return s .. "!"
end

return M
`)

	g, _ := newStores(t)
	pid, err := g.UpsertProject("ruby-lua", dir, "mixed")
	if err != nil {
		t.Fatal(err)
	}
	ix := New(g, nil, nil, config.DefaultConfig().Index)
	res, err := ix.IndexProject(context.Background(), pid, "ruby-lua", dir, Options{NoLSP: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Languages["ruby"] != 2 || res.Languages["lua"] != 2 {
		t.Fatalf("Languages = %v, want ruby:2 lua:2", res.Languages)
	}
	if res.Unsupported["ruby"] != 0 || res.Unsupported["lua"] != 0 {
		t.Errorf("ruby/lua must not be unsupported: %v", res.Unsupported)
	}

	nodes, err := g.ProjectNodes(pid)
	if err != nil {
		t.Fatal(err)
	}
	byFQN := map[string]graph.Node{}
	for _, n := range nodes {
		byFQN[n.FQN] = n
	}
	for _, fqn := range []string{"Billing.Invoice.total", "to_cents", "M.run", "M.shout"} {
		if _, ok := byFQN[fqn]; !ok {
			t.Errorf("missing node %s", fqn)
		}
	}

	allEdges, err := g.ProjectEdges(pid)
	if err != nil {
		t.Fatal(err)
	}
	hasEdge := func(fromID, toID int64, kind string) bool {
		for _, e := range allEdges {
			if e.SourceID == fromID && e.TargetID == toID && e.EdgeType == kind {
				return true
			}
		}
		return false
	}
	edgeExists := func(fromFQN, toFQN, kind string) bool {
		return hasEdge(byFQN[fromFQN].ID, byFQN[toFQN].ID, kind)
	}
	if !edgeExists("Billing.Invoice.total", "to_cents", graph.EdgeCalls) {
		t.Errorf("missing ruby call edge total → to_cents")
	}
	if !edgeExists("M.run", "M.shout", graph.EdgeCalls) {
		t.Errorf("missing lua call edge M.run → M.shout")
	}

	fileNode := func(path string) graph.Node {
		for _, n := range nodes {
			if n.Kind == graph.KindFile && n.FilePath == path {
				return n
			}
		}
		t.Fatalf("missing file node %s", path)
		return graph.Node{}
	}
	importEdge := func(fromPath, toPath string) bool {
		return hasEdge(fileNode(fromPath).ID, fileNode(toPath).ID, graph.EdgeImports)
	}
	if !importEdge("lib/billing.rb", "lib/money.rb") {
		t.Errorf("missing ruby require_relative import edge")
	}
	if !importEdge("lua/app/init.lua", "lua/app/util.lua") {
		t.Errorf("missing lua require import edge")
	}
}
