package index

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

// TestIndexCSSAndHTML proves the pure-Go stylesheet/HTML backends are
// registered by default and produce the full shape end-to-end with no
// language server: selector definition nodes (SCSS nesting flattened),
// className→selector styles edges resolved by the existing name-resolution
// pass, stylesheet import edges, and incremental re-linking when a selector
// is renamed (the HTML file holding edges into the stylesheet is re-extracted
// via inbound expansion).
func TestIndexCSSAndHTML(t *testing.T) {
	dir := t.TempDir()
	appSCSS := `@use "./vars";

.toolbar {
  color: vars.$fg;

  .btn {
    font-weight: bold;
  }
}
`
	writeFile(t, dir, "styles/app.scss", appSCSS)
	writeFile(t, dir, "styles/_vars.scss", `$fg: #333;
`)
	writeFile(t, dir, "site/index.html", `<html>
  <body>
    <div class="btn toolbar missing-class">x</div>
  </body>
</html>
`)

	g, _ := newStores(t)
	pid, err := g.UpsertProject("css-html", dir, "mixed")
	if err != nil {
		t.Fatal(err)
	}
	ix := New(g, nil, nil, config.DefaultConfig().Index)
	res, err := ix.IndexProject(context.Background(), pid, "css-html", dir, Options{NoLSP: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Languages["scss"] != 2 || res.Languages["html"] != 1 {
		t.Fatalf("Languages = %v, want scss:2 html:1", res.Languages)
	}
	if res.Unsupported["scss"] != 0 || res.Unsupported["html"] != 0 || res.Unsupported["css"] != 0 {
		t.Errorf("scss/html must not be unsupported: %v", res.Unsupported)
	}

	loadNodes := func() (map[string]graph.Node, []graph.Node) {
		nodes, err := g.ProjectNodes(pid)
		if err != nil {
			t.Fatal(err)
		}
		byFQN := map[string]graph.Node{}
		for _, n := range nodes {
			byFQN[n.FQN] = n
		}
		return byFQN, nodes
	}
	byFQN, nodes := loadNodes()

	for _, fqn := range []string{"styles/app.scss#.btn", "styles/app.scss#.toolbar"} {
		n, ok := byFQN[fqn]
		if !ok {
			t.Fatalf("missing selector node %s", fqn)
		}
		if n.Kind != graph.KindSelector {
			t.Errorf("%s kind = %q, want selector", fqn, n.Kind)
		}
	}
	// The nested .btn rule flattens under .toolbar.
	if sym := byFQN["styles/app.scss#.btn"].Symbol; sym != ".btn" {
		t.Errorf(".btn Symbol = %q, want .btn", sym)
	}

	fileNode := func(nodes []graph.Node, path string) graph.Node {
		for _, n := range nodes {
			if n.Kind == graph.KindFile && n.FilePath == path {
				return n
			}
		}
		t.Fatalf("missing file node %s", path)
		return graph.Node{}
	}
	loadEdges := func() []graph.Edge {
		edges, err := g.ProjectEdges(pid)
		if err != nil {
			t.Fatal(err)
		}
		return edges
	}
	hasEdge := func(edges []graph.Edge, fromID, toID int64, kind string) *graph.Edge {
		for i := range edges {
			if edges[i].SourceID == fromID && edges[i].TargetID == toID && edges[i].EdgeType == kind {
				return &edges[i]
			}
		}
		return nil
	}

	edges := loadEdges()
	html := fileNode(nodes, "site/index.html")

	// class="btn" / class="toolbar" → styles edges at candidate weight.
	for _, target := range []string{"styles/app.scss#.btn", "styles/app.scss#.toolbar"} {
		e := hasEdge(edges, html.ID, byFQN[target].ID, graph.EdgeStyles)
		if e == nil {
			t.Fatalf("missing styles edge html → %s", target)
		}
		if e.Weight != graph.WeightTreeSitter {
			t.Errorf("styles edge weight = %v, want %v (candidate)", e.Weight, graph.WeightTreeSitter)
		}
	}
	// class="missing-class" resolves to nothing → no edge, no noise.
	for _, e := range edges {
		if e.SourceID == html.ID && e.EdgeType == graph.EdgeStyles {
			tgt := func() *graph.Node {
				for i := range nodes {
					if nodes[i].ID == e.TargetID {
						return &nodes[i]
					}
				}
				return nil
			}()
			if tgt != nil && tgt.Symbol == ".missing-class" {
				t.Errorf("unresolvable class produced an edge: %+v", e)
			}
		}
	}

	// @use "./vars" → file→file import edge onto the Sass partial.
	vars := fileNode(nodes, "styles/_vars.scss")
	app := fileNode(nodes, "styles/app.scss")
	if hasEdge(edges, app.ID, vars.ID, graph.EdgeImports) == nil {
		t.Errorf("missing import edge app.scss → _vars.scss")
	}

	// Rename .btn → .button and reindex just the stylesheet: the stale
	// styles edge must disappear (inbound expansion re-extracts the HTML),
	// while html → .toolbar survives re-linking.
	writeFile(t, dir, "styles/app.scss", `@use "./vars";

.toolbar {
  color: vars.$fg;

  .button {
    font-weight: bold;
  }
}
`)
	if _, err := ix.IndexFiles(context.Background(), pid, "css-html", dir, []string{"styles/app.scss"}, Options{NoLSP: true}); err != nil {
		t.Fatal(err)
	}

	byFQN, nodes = loadNodes()
	edges = loadEdges()
	if _, ok := byFQN["styles/app.scss#.btn"]; ok {
		t.Errorf("stale .btn selector node survived the rename")
	}
	if _, ok := byFQN["styles/app.scss#.button"]; !ok {
		t.Errorf("renamed .button selector node missing")
	}
	html = fileNode(nodes, "site/index.html")
	for _, e := range edges {
		if e.SourceID == html.ID && e.EdgeType == graph.EdgeStyles && e.TargetID == byFQN["styles/app.scss#.button"].ID {
			t.Errorf("html references class btn, not button — no edge expected: %+v", e)
		}
	}
	if hasEdge(edges, html.ID, byFQN["styles/app.scss#.toolbar"].ID, graph.EdgeStyles) == nil {
		t.Errorf("html → .toolbar styles edge lost after incremental reindex")
	}
}

// TestIndexTSXClassNameToCSS proves the tsscan className scan produces
// styles edges from a TSX component to the CSS selector nodes end-to-end.
// Server-gated like the other TSX coverage: tsscan only runs when the
// TypeScript language server extracted the file.
func TestIndexTSXClassNameToCSS(t *testing.T) {
	if _, err := exec.LookPath("typescript-language-server"); err != nil {
		t.Skip("typescript-language-server not on PATH")
	}
	dir := t.TempDir()
	writeFile(t, dir, "styles/app.css", `.btn {
  color: red;
}
`)
	writeFile(t, dir, "src/Page.tsx", `export function Page() {
  return <div className="btn missing-class">x</div>;
}
`)
	g, _ := newStores(t)
	pid, err := g.UpsertProject("tsx-css", dir, "mixed")
	if err != nil {
		t.Fatal(err)
	}
	ix := New(g, nil, nil, config.DefaultConfig().Index)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := ix.IndexProject(ctx, pid, "tsx-css", dir, Options{}); err != nil {
		t.Fatal(err)
	}

	nodes, err := g.ProjectNodes(pid)
	if err != nil {
		t.Fatal(err)
	}
	var pageID, btnID int64
	for _, n := range nodes {
		switch {
		case n.FQN == "Page" && n.FilePath == "src/Page.tsx":
			pageID = n.ID
		case n.FQN == "styles/app.css#.btn":
			btnID = n.ID
		}
	}
	if pageID == 0 || btnID == 0 {
		t.Fatalf("missing Page or .btn node (page=%d, btn=%d)", pageID, btnID)
	}
	edges, err := g.ProjectEdges(pid)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range edges {
		if e.SourceID == pageID && e.TargetID == btnID && e.EdgeType == graph.EdgeStyles {
			found = true
			if e.Weight != graph.WeightTreeSitter {
				t.Errorf("styles edge weight = %v, want candidate %v", e.Weight, graph.WeightTreeSitter)
			}
		}
	}
	if !found {
		t.Errorf("missing styles edge Page → .btn")
	}
}
