package index

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

// TestIndexTypeScriptSymbols proves the LSP backend indexes TypeScript into the
// same node/edge model Go uses. Server-gated: it self-skips where
// typescript-language-server isn't on PATH (e.g. CI), and runs locally.
func TestIndexTypeScriptSymbols(t *testing.T) {
	if _, err := exec.LookPath("typescript-language-server"); err != nil {
		t.Skip("typescript-language-server not on PATH")
	}
	dir := t.TempDir()
	writeFile(t, dir, "svc.ts", `export class UserService {
  getUser(id: string) { return id; }
}

export function makeService() { return new UserService(); }
`)
	g, _ := newStores(t)
	pid, _ := g.UpsertProject("ts", dir, "typescript")
	ix := New(g, nil, nil, config.DefaultConfig().Index)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := ix.IndexProject(ctx, pid, "ts", dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Nodes == 0 {
		t.Fatal("expected TypeScript nodes, got 0 (is typescript resolvable in this env?)")
	}

	kindOf := func(symbol, fqn string) string {
		nodes, err := g.FindNodesBySymbol(pid, symbol)
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range nodes {
			if n.FQN == fqn {
				return n.Kind
			}
		}
		return "(absent)"
	}
	if k := kindOf("UserService", "UserService"); k != graph.KindClass {
		t.Errorf("UserService kind = %q, want class", k)
	}
	if k := kindOf("getUser", "UserService.getUser"); k != graph.KindMethod {
		t.Errorf("getUser kind = %q, want method (nested FQN)", k)
	}
	if k := kindOf("makeService", "makeService"); k != graph.KindFunction {
		t.Errorf("makeService kind = %q, want function", k)
	}

	// A `defines` edge file -> class lands in Pass 1, so structure browsing works.
	nodes, _ := g.FindNodesBySymbol(pid, "UserService")
	if len(nodes) == 0 {
		t.Fatal("UserService node missing")
	}
	var in int
	_ = g.DB().QueryRow("SELECT COUNT(*) FROM edges WHERE target_id=? AND edge_type='defines'", nodes[0].ID).Scan(&in)
	if in == 0 {
		t.Error("expected a defines edge into the UserService node")
	}
}

// TestIndexTypeScriptCallEdges proves --precise adds exact TS call edges via
// callHierarchy, so callers/impact work for TypeScript. Server-gated.
func TestIndexTypeScriptCallEdges(t *testing.T) {
	if _, err := exec.LookPath("typescript-language-server"); err != nil {
		t.Skip("typescript-language-server not on PATH")
	}
	dir := t.TempDir()
	writeFile(t, dir, "callee.ts", "export function callee() { return 1; }\n")
	writeFile(t, dir, "caller.ts", "import { callee } from \"./callee\";\n\nexport function caller() { return callee(); }\n")
	g, _ := newStores(t)
	pid, _ := g.UpsertProject("ts", dir, "typescript")
	ix := New(g, nil, nil, config.DefaultConfig().Index)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := ix.IndexProject(ctx, pid, "ts", dir, Options{Precise: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.PreciseUpgraded == 0 {
		t.Fatal("expected precise TS call edges, got 0 (callHierarchy join failed?)")
	}
	callers, err := g.Callers(pid, "callee")
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, c := range callers {
		if c.Symbol == "caller" {
			found = true
		}
	}
	if !found {
		t.Errorf("callers of callee should include caller (precise TS edge), got %+v", callers)
	}
	// Without --precise, TS has no call edges (callHierarchy is the only source).
	g2, _ := newStores(t)
	pid2, _ := g2.UpsertProject("ts", dir, "typescript")
	ix2 := New(g2, nil, nil, config.DefaultConfig().Index)
	defer ix2.Close()
	if _, err := ix2.IndexProject(context.Background(), pid2, "ts", dir, Options{}); err != nil {
		t.Fatal(err)
	}
	if c, _ := g2.Callers(pid2, "callee"); len(c) != 0 {
		t.Errorf("name-based TS index should have no call edges, got %+v", c)
	}
}

// TestIndexTypeScriptDisabledByNoLSP confirms --no-lsp keeps TS unindexed
// regardless of installed servers (deterministic, never spawns a server).
func TestIndexTypeScriptDisabledByNoLSP(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "svc.ts", "export function f() {}\n")
	g, _ := newStores(t)
	pid, _ := g.UpsertProject("ts", dir, "typescript")
	ix := New(g, nil, nil, config.DefaultConfig().Index)
	res, err := ix.IndexProject(context.Background(), pid, "ts", dir, Options{NoLSP: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Nodes != 0 {
		t.Errorf("NoLSP should index no TS nodes, got %d", res.Nodes)
	}
	if res.Unsupported["typescript"] != 1 {
		t.Errorf("expected 1 unsupported typescript file under NoLSP, got %v", res.Unsupported)
	}
}
