package featuresrc

import (
	"strings"
	"testing"
)

func TestDetectJSExpressRoutes(t *testing.T) {
	src := `const express = require('express');
const app = express();
const router = express.Router();

// List all users
app.get('/api/users', listUsers);
router.post("/api/users", auth, createUser);
app.delete('/api/users/:id', async (req, res) => { res.send(); });
app.all('/any', handler);
api.get('/users');                       // http client call: no handler
api.get('/users', { params: { a: 1 } }); // http client call with options
// app.get('/commented', nope);
app.get(` + "`/tpl/:id`" + `,
  controller.show);
`
	regs := DetectPattern("src/server.js", []byte(src))
	want := map[string]string{
		"GET /api/users":        "listUsers",
		"POST /api/users":       "createUser",
		"DELETE /api/users/:id": "",
		"ANY /any":              "handler",
		"GET /tpl/:id":          "",
	}
	got := map[string]*Registration{}
	for i := range regs {
		got[regs[i].Label] = &regs[i]
	}
	if len(got) != len(want) {
		t.Fatalf("labels = %v, want %v", labels(regs, KindHTTPRoute), want)
	}
	for label, h := range want {
		r := got[label]
		if r == nil {
			t.Fatalf("missing %q", label)
		}
		if r.Kind != KindHTTPRoute || r.Detector != DetectorPattern || r.Confidence != Candidate || r.Framework != "express" {
			t.Errorf("%q meta = %+v", label, r)
		}
		if h == "" && r.Handler != nil {
			t.Errorf("%q: inline/multiline handler should be nil, got %+v", label, r.Handler)
		}
		if h != "" && (r.Handler == nil || r.Handler.Name != h) {
			t.Errorf("%q handler = %+v, want %s", label, r.Handler, h)
		}
	}
	if d := got["GET /api/users"].Description; d != "List all users" {
		t.Errorf("leading comment description = %q", d)
	}
	if got["GET /api/users"].Line != 6 {
		t.Errorf("line = %d", got["GET /api/users"].Line)
	}
}

func TestDetectJSCommanderAndYargs(t *testing.T) {
	src := `program
  .command('serve <dir>')
  .description('Serve a directory')
  .action(serveCmd);

program.command('build').description('Build it').action(() => {});

yargs.command('deploy [env]', 'Deploy the app', () => {}, deployHandler)

write(ui.command("chalupa balance --top-up 10|25|50"));
client.command('flushall');
`
	regs := DetectPattern("bin/cli.ts", []byte(src))
	serve := find(regs, KindCLICommand, "serve")
	if serve == nil || serve.Description != "Serve a directory" || serve.Handler == nil || serve.Handler.Name != "serveCmd" || serve.Framework != "commander" {
		t.Fatalf("serve = %+v", serve)
	}
	if b := find(regs, KindCLICommand, "build"); b == nil || b.Description != "Build it" || b.Handler != nil {
		t.Fatalf("build = %+v", b)
	}
	if d := find(regs, KindCLICommand, "deploy"); d == nil || d.Description != "Deploy the app" || d.Framework != "yargs" {
		t.Fatalf("deploy = %+v", d)
	}
	if got := labels(regs, KindCLICommand); len(got) != 3 {
		t.Fatalf("help text and unrelated .command() calls must not be features, got %v", got)
	}
}

func TestDetectJSIgnoresPlainCode(t *testing.T) {
	if regs := DetectPattern("a.js", []byte("const m = new Map(); m.get('x'); cache.get('/not-a-route');\n")); len(regs) != 0 {
		t.Fatalf("plain code produced %+v", regs)
	}
}

func TestDetectPythonRoutes(t *testing.T) {
	src := `from fastapi import FastAPI
app = FastAPI()

@app.get("/items/{item_id}")
async def read_item(item_id: int):
    """Fetch one item."""
    return {}

@app.route("/legacy", methods=["GET", "POST"])
@login_required
def legacy():
    return ""

@router.api_route('/any')
def anything(): pass

@pytest.mark.get("/nope")
def test_nope(): pass
`
	regs := DetectPattern("app/main.py", []byte(src))
	r := find(regs, KindHTTPRoute, "GET /items/{item_id}")
	if r == nil || r.Handler == nil || r.Handler.Name != "read_item" || r.Description != "Fetch one item." || r.DescSource != DescDocstring || r.Confidence != Candidate {
		t.Fatalf("read_item = %+v", r)
	}
	for _, label := range []string{"GET /legacy", "POST /legacy"} {
		l := find(regs, KindHTTPRoute, label)
		if l == nil || l.Handler == nil || l.Handler.Name != "legacy" {
			t.Fatalf("%s = %+v (have %v)", label, l, labels(regs, KindHTTPRoute))
		}
	}
	if a := find(regs, KindHTTPRoute, "ANY /any"); a == nil || a.Handler.Name != "anything" {
		t.Fatalf("any = %+v", a)
	}
	if len(labels(regs, KindHTTPRoute)) != 4 {
		t.Fatalf("labels = %v", labels(regs, KindHTTPRoute))
	}
}

func TestDetectPythonCLIAndMain(t *testing.T) {
	src := `"""Reports tool."""
import click

@click.group()
def cli(): pass

@cli.command()
def sync_all():
    """Sync everything."""

@cli.command("purge")
def purge_cmd():
    pass

if __name__ == "__main__":
    cli()
`
	regs := DetectPattern("tools/report.py", []byte(src))
	if c := find(regs, KindCLICommand, "sync-all"); c == nil || c.Description != "Sync everything." || c.Handler.Name != "sync_all" {
		t.Fatalf("sync-all = %+v", c)
	}
	if c := find(regs, KindCLICommand, "purge"); c == nil || c.Handler.Name != "purge_cmd" {
		t.Fatalf("purge = %+v", c)
	}
	p := find(regs, KindProgram, "report")
	if p == nil || p.Handler == nil || p.Handler.Name != "cli" || p.Description != "Reports tool." {
		t.Fatalf("program = %+v", p)
	}
	main := DetectPattern("pkg/__main__.py", []byte("if __name__ == '__main__':\n    main()\n"))
	if len(main) != 1 || main[0].Label != "pkg" {
		t.Fatalf("__main__.py = %+v", main)
	}
}

func TestDetectPythonDjango(t *testing.T) {
	src := `from django.urls import path, include
urlpatterns = [
    path('articles/', views.article_list),
    path("detail/<int:pk>/", views.DetailView.as_view()),
    path('api/', include('api.urls')),
]
`
	regs := DetectPattern("blog/urls.py", []byte(src))
	if len(regs) != 2 {
		t.Fatalf("regs = %+v", regs)
	}
	if r := find(regs, KindHTTPRoute, "ANY /articles/"); r == nil || r.Handler.Name != "article_list" {
		t.Fatalf("articles = %+v", r)
	}
	if r := find(regs, KindHTTPRoute, "ANY /detail/<int:pk>/"); r == nil || r.Handler.Name != "DetailView" {
		t.Fatalf("detail = %+v", r)
	}
	if got := DetectPattern("blog/helpers.py", []byte(src)); len(got) != 0 {
		t.Fatalf("path() outside urls.py must be ignored: %+v", got)
	}
}

func TestDetectNextPath(t *testing.T) {
	cases := []struct {
		rel, src string
		kind     string
		labels   []string
		handler  string
	}{
		{"app/page.tsx", "export default function Home() { return null }", KindPage, []string{"/"}, "Home"},
		{"src/app/(marketing)/pricing/page.tsx", "export default async function Pricing() {}", KindPage, []string{"/pricing"}, "Pricing"},
		{"app/dashboard/[id]/page.jsx", "const P = () => null;\nexport default P;\n", KindPage, []string{"/dashboard/[id]"}, "P"},
		{"app/blog/page.tsx", "export default memo(Blog)", KindPage, []string{"/blog"}, "Blog"},
		{"app/api/users/route.ts", "export async function GET() {}\nexport const POST = async () => {}\n", KindAPIRoute, []string{"GET /api/users", "POST /api/users"}, ""},
		{"pages/index.tsx", "export default function Index() {}", KindPage, []string{"/"}, "Index"},
		{"pages/blog/index.js", "export default function Blog() {}", KindPage, []string{"/blog"}, "Blog"},
		{"pages/about.tsx", "export default function About() {}", KindPage, []string{"/about"}, "About"},
		{"pages/api/health.ts", "export default function handler() {}", KindAPIRoute, []string{"ANY /api/health"}, "handler"},
	}
	for _, c := range cases {
		regs := DetectNextPath(c.rel, []byte(c.src))
		if got := labels(regs, c.kind); strings.Join(got, "|") != strings.Join(c.labels, "|") {
			t.Errorf("%s: labels = %v, want %v", c.rel, got, c.labels)
			continue
		}
		for _, r := range regs {
			if r.Detector != DetectorPath || r.Confidence != Confirmed || r.Framework != "nextjs" {
				t.Errorf("%s: meta = %+v", c.rel, r)
			}
			if c.handler != "" && (r.Handler == nil || r.Handler.Name != c.handler) {
				t.Errorf("%s: handler = %+v, want %s", c.rel, r.Handler, c.handler)
			}
		}
	}
	for _, rel := range []string{
		"app/layout.tsx", "app/loading.tsx", "app/globals.css", "pages/_app.tsx", "pages/_document.tsx",
		"app/components/Button.tsx", "lib/page.tsx", "app/page.d.ts",
	} {
		if regs := DetectNextPath(rel, []byte("export default function X() {}")); len(regs) != 0 {
			t.Errorf("%s must not be a feature: %+v", rel, regs)
		}
	}
	verbs := DetectNextPath("app/api/x/route.ts", []byte("export async function GET() {}\nexport async function GET() {}\n"))
	if len(verbs) != 1 {
		t.Errorf("duplicate verbs not deduped: %+v", verbs)
	}
	if one := DetectNextPath("app/api/y/route.ts", []byte("export default {}")); len(one) != 1 || one[0].Confidence != Candidate {
		t.Errorf("verbless route.ts = %+v", one)
	}
}

func TestDetectPackageBin(t *testing.T) {
	regs := DetectPackageBin("package.json", []byte(`{
  "name": "@scope/tool",
  "description": "Does tool things",
  "bin": "./cli.js"
}`))
	if len(regs) != 1 || regs[0].Label != "tool" || regs[0].Description != "Does tool things" || regs[0].Line != 4 {
		t.Fatalf("string bin = %+v", regs)
	}
	regs = DetectPackageBin("pkg/package.json", []byte(`{"name":"x","bin":{"b":"./b.js","a":"./a.js"}}`))
	if len(regs) != 2 || regs[0].Label != "a" || regs[1].Label != "b" {
		t.Fatalf("object bin = %+v", regs)
	}
	if regs := DetectPackageBin("package.json", []byte(`{"name":"x"}`)); len(regs) != 0 {
		t.Fatalf("no bin: %+v", regs)
	}
	if regs := DetectPackageBin("package.json", []byte(`not json`)); len(regs) != 0 {
		t.Fatalf("invalid json: %+v", regs)
	}
}

func TestIsIgnoredPath(t *testing.T) {
	for p, want := range map[string]bool{
		"a/testdata/x.go": true, "node_modules/x/a.js": true, "a/fixtures/s.go": true, "vendor/x/y.go": true,
		"cmd/main.go": false, "app/page.tsx": false,
	} {
		if got := IsIgnoredPath(p); got != want {
			t.Errorf("IsIgnoredPath(%q) = %v, want %v", p, got, want)
		}
	}
}
