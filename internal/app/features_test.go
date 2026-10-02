package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/extract/featuresrc"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
	"github.com/abdul-hamid-achik/codemap/internal/index"
)

func writeFeatureTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, source := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func openFeatureService(t *testing.T) *Service {
	t.Helper()
	isolate(t)
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return NewService(sess)
}

var featureFixture = map[string]string{
	"go.mod": "module example.com/feat\n\ngo 1.25\n",
	"cmd/feat/main.go": `package main

import "github.com/spf13/cobra"

var rootCmd = &cobra.Command{Use: "feat", Short: "A feature demo"}

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run the pipeline",
	RunE:  runPipeline,
}

var cacheCmd = &cobra.Command{Use: "cache", Short: "Cache tools"}

var cacheDropCmd = &cobra.Command{
	Use:   "drop",
	Short: "Drop the cache",
	RunE:  func(cmd *cobra.Command, args []string) error { return nil },
}

func init() {
	cacheCmd.AddCommand(cacheDropCmd)
	rootCmd.AddCommand(runCmd, cacheCmd)
}

func main() { _ = rootCmd.Execute() }

// runPipeline executes the demo pipeline.
func runPipeline(cmd *cobra.Command, args []string) error {
	return work.Execute()
}
`,
	"cmd/feat/junk_test.go": `package main

import "github.com/spf13/cobra"

func TestJunk() {
	tests := []struct{ cmd *cobra.Command }{{&cobra.Command{Use: "junk", Short: "junk"}}}
	_ = tests
}
`,
	"internal/work/work.go": `package work

func Execute() error {
	helper()
	Close()
	return Step()
}

func helper() {}

func Step() error { return nil }

func Close() {}
`,
	"internal/work/work_test.go": `package work

import "testing"

func TestExecute(t *testing.T) { _ = Execute() }
`,
	"internal/other/other.go": `package other

func Close() {}
`,
	"internal/mcpsrv/server.go": `package mcpsrv

import sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

type Server struct{}

func (s *Server) register() {
	sdkmcp.AddTool(nil, &sdkmcp.Tool{Name: "feat_run", Description: "Run it over MCP"}, s.handleRun)
	sdkmcp.AddTool(nil, &sdkmcp.Tool{Name: "feat_status", Description: "Status over MCP"}, s.handleStatus)
}

func (s *Server) handleRun()    {}
func (s *Server) handleStatus() {}
`,
	"internal/api/api.go": `package api

import "net/http"

func Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /health", health)
	mux.HandleFunc("/ping", ping)
}

func health(w http.ResponseWriter, r *http.Request) {}
func ping(w http.ResponseWriter, r *http.Request)   {}
`,
}

func indexedFeatureService(t *testing.T) (*Service, string) {
	t.Helper()
	svc := openFeatureService(t)
	proj := t.TempDir()
	writeFeatureTree(t, proj, featureFixture)
	if _, err := svc.Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}
	return svc, proj
}

func featureByID(rep *FeaturesReport, id string) *Feature {
	for i := range rep.Features {
		if rep.Features[i].ID == id {
			return &rep.Features[i]
		}
	}
	return nil
}

func TestFeaturesInventoryOnIndexedProject(t *testing.T) {
	svc, proj := indexedFeatureService(t)
	rep, err := svc.Features(proj, FeaturesOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.SchemaVersion != 1 || !rep.Indexed || rep.Project == "" {
		t.Fatalf("identity = %+v", rep)
	}
	if rep.FeaturesTotal != len(rep.Features) || rep.Truncated {
		t.Fatalf("totals = %d/%d truncated=%v", len(rep.Features), rep.FeaturesTotal, rep.Truncated)
	}
	wantKinds := map[string]int{"program": 1, "cli_command": 3, "rpc_tool": 2, "http_route": 2}
	if !reflect.DeepEqual(rep.ByKind, wantKinds) {
		t.Fatalf("by_kind = %v, want %v (features: %v)", rep.ByKind, wantKinds, featureIDs(rep))
	}
	if !reflect.DeepEqual(rep.Frameworks, []string{"cobra", "go", "go-http", "mcp-go-sdk"}) {
		t.Errorf("frameworks = %v", rep.Frameworks)
	}
	if featureByID(rep, "cli_command:junk") != nil {
		t.Error("a Command literal inside a _test.go file leaked into the inventory")
	}
	if rep.CallGraph != CallGraphName {
		t.Errorf("call_graph = %q", rep.CallGraph)
	}
	if rep.Stale {
		t.Error("freshly indexed project reported stale")
	}
	if rep.Notes == nil || rep.PartialErrors == nil || rep.Frameworks == nil {
		t.Error("empty slices must serialize as [] not null")
	}

	// Sort order: program, cli_command, rpc_tool, http_route; then label.
	var kinds []string
	for _, f := range rep.Features {
		kinds = append(kinds, f.Kind)
	}
	if got := strings.Join(kinds, ","); got != "program,cli_command,cli_command,cli_command,rpc_tool,rpc_tool,http_route,http_route" {
		t.Errorf("kind order = %s", got)
	}

	prog := featureByID(rep, "program:feat")
	if prog == nil || prog.Description != "A feature demo" || prog.Handler == nil || prog.Handler.Symbol != "main" || prog.Handler.File != "cmd/feat/main.go" {
		t.Fatalf("program = %+v", prog)
	}

	run := featureByID(rep, "cli_command:run")
	if run == nil {
		t.Fatalf("run missing: %v", featureIDs(rep))
	}
	if run.Invocation != "feat run" || run.Description != "Run the pipeline" || run.DescriptionSource != "registration" ||
		run.Confidence != "confirmed" || run.Detector != "go_ast" || run.Surface != "CLI" {
		t.Errorf("run meta = %+v", run)
	}
	if run.Handler == nil || run.Handler.Symbol != "runPipeline" || run.Handler.Selector == nil || run.Handler.Selector.File != "cmd/feat/main.go" ||
		run.Registration.File != "cmd/feat/main.go" || run.Registration.Line != 7 {
		t.Errorf("run handler/registration = %+v / %+v", run.Handler, run.Registration)
	}
	drop := featureByID(rep, "cli_command:cache drop")
	if drop == nil || drop.Parent != "cli_command:cache" || drop.Handler != nil || drop.Footprint != nil {
		t.Errorf("cache drop = %+v", drop)
	}

	tool := featureByID(rep, "rpc_tool:feat_run")
	if tool == nil || tool.Handler == nil || tool.Handler.FQN != "mcpsrv.Server.handleRun" || tool.Description != "Run it over MCP" {
		t.Fatalf("feat_run = %+v", tool)
	}
	health := featureByID(rep, "http_route:GET /health")
	if health == nil || health.Handler == nil || health.Handler.Symbol != "health" || health.Confidence != "confirmed" {
		t.Fatalf("GET /health = %+v", health)
	}
	if ping := featureByID(rep, "http_route:ANY /ping"); ping == nil || ping.Handler == nil || ping.Handler.Symbol != "ping" {
		t.Fatalf("ANY /ping = %+v", ping)
	}

	// Footprint: runPipeline -> work.Execute -> helper, Step (and Close, which
	// has two definitions: the same-directory one wins, other.Close is not
	// followed).
	fp := run.Footprint
	if fp == nil {
		t.Fatal("run has no footprint")
	}
	if fp.Symbols < 4 || fp.Depth != DefaultFootprintDepth || fp.Truncated {
		t.Errorf("footprint = %+v", fp)
	}
	if fp.Tests < 1 {
		t.Errorf("expected TestExecute to cover the footprint: %+v", fp)
	}
	if len(fp.Subsystems) == 0 || fp.Subsystems[0].Symbols < 1 {
		t.Errorf("footprint subsystems = %+v", fp.Subsystems)
	}
	for _, s := range fp.Subsystems {
		if s.Name == "internal/other" {
			t.Errorf("ambiguous Close leaked into other package: %+v", fp.Subsystems)
		}
	}
}

func featureIDs(rep *FeaturesReport) []string {
	var ids []string
	for _, f := range rep.Features {
		ids = append(ids, f.ID)
	}
	return ids
}

func TestFeaturesFiltersBoundsAndDeterminism(t *testing.T) {
	svc, proj := indexedFeatureService(t)

	cli, err := svc.Features(proj, FeaturesOptions{Kinds: []string{"cli_command, program"}})
	if err != nil {
		t.Fatal(err)
	}
	if cli.FeaturesTotal != 4 || cli.ByKind["rpc_tool"] != 0 {
		t.Errorf("kind filter total = %d by_kind=%v", cli.FeaturesTotal, cli.ByKind)
	}

	q, err := svc.Features(proj, FeaturesOptions{Query: "RUN IT OVER"})
	if err != nil {
		t.Fatal(err)
	}
	if q.FeaturesTotal != 1 || q.Features[0].Label != "feat_run" {
		t.Errorf("query filter = %v", featureIDs(q))
	}
	byHandler, _ := svc.Features(proj, FeaturesOptions{Query: "mcpsrv.server.handlestatus"})
	if byHandler.FeaturesTotal != 1 || byHandler.Features[0].Label != "feat_status" {
		t.Errorf("handler fqn query = %v", featureIDs(byHandler))
	}

	top, err := svc.Features(proj, FeaturesOptions{Top: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(top.Features) != 3 || top.FeaturesTotal != 8 || !top.Truncated {
		t.Errorf("top bound: %d/%d truncated=%v", len(top.Features), top.FeaturesTotal, top.Truncated)
	}
	if sum := top.ByKind["program"] + top.ByKind["cli_command"] + top.ByKind["rpc_tool"] + top.ByKind["http_route"]; sum != 8 {
		t.Errorf("by_kind must describe the full filtered set, got %v", top.ByKind)
	}

	nf, err := svc.Features(proj, FeaturesOptions{NoFootprint: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range nf.Features {
		if f.Footprint != nil {
			t.Errorf("%s has a footprint under NoFootprint", f.ID)
		}
	}

	shallow, _ := svc.Features(proj, FeaturesOptions{Depth: 1})
	deep, _ := svc.Features(proj, FeaturesOptions{Depth: 3})
	s1 := featureByID(shallow, "cli_command:run").Footprint
	s3 := featureByID(deep, "cli_command:run").Footprint
	if s1.Symbols >= s3.Symbols || s1.Depth != 1 {
		t.Errorf("depth 1 footprint (%d) should be smaller than depth 3 (%d)", s1.Symbols, s3.Symbols)
	}

	a, _ := json.Marshal(mustFeatures(t, svc, proj))
	b, _ := json.Marshal(mustFeatures(t, svc, proj))
	if string(a) != string(b) {
		t.Error("two identical requests produced different JSON")
	}
}

func mustFeatures(t *testing.T, svc *Service, proj string) *FeaturesReport {
	t.Helper()
	rep, err := svc.Features(proj, FeaturesOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func TestFeaturesUnindexedAndInvalidOptions(t *testing.T) {
	svc := openFeatureService(t)
	rep, err := svc.Features(t.TempDir(), FeaturesOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Indexed || rep.SchemaVersion != 1 || rep.Features == nil || rep.ByKind == nil || rep.CallGraph != CallGraphNone {
		t.Fatalf("unindexed report = %+v", rep)
	}
	for _, opts := range []FeaturesOptions{
		{Top: -1}, {Top: MaxFeaturesTop + 1}, {Depth: -1}, {Depth: MaxFootprintDepth + 1}, {Kinds: []string{"nonsense"}},
	} {
		if _, err := svc.Features(t.TempDir(), opts); err == nil {
			t.Errorf("Features(%+v) should fail", opts)
		}
	}
}

func TestFeaturesJSAndPythonOnSeededGraph(t *testing.T) {
	svc := openFeatureService(t)
	proj := t.TempDir()
	writeFeatureTree(t, proj, map[string]string{
		"package.json":                `{"name":"web","dependencies":{"next":"15"},"bin":{"webctl":"./bin/webctl.js"}}`,
		"app/page.tsx":                "export default function Home() { return null }\n",
		"app/(shop)/cart/page.tsx":    "export default function Cart() { return null }\n",
		"app/api/users/route.ts":      "export async function GET() {}\nexport async function POST() {}\n",
		"app/layout.tsx":              "export default function Layout() {}\n",
		"app/page.test.tsx":           "export default function Test() {}\n",
		"server/index.js":             "app.get('/status', getStatus);\nfunction getStatus() {}\n",
		"other/package.json":          `{"name":"plain"}`,
		"other/app/page.tsx":          "export default function NotNext() {}\n",
		"svc/main.py":                 "from flask import Flask\napp = Flask(__name__)\n\n@app.route('/hello')\ndef hello():\n    \"\"\"Say hello.\"\"\"\n    return 'hi'\n",
		"node_modules/x/app/page.tsx": "export default function Dep() {}\n",
	})
	g, err := svc.s.Graph()
	if err != nil {
		t.Fatal(err)
	}
	pid, err := g.UpsertProject("seeded", proj, "typescript")
	if err != nil {
		t.Fatal(err)
	}
	add := func(n graph.Node) {
		n.ProjectID = pid
		if _, err := g.AddNode(&n); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{
		"app/page.tsx", "app/(shop)/cart/page.tsx", "app/api/users/route.ts", "app/layout.tsx", "app/page.test.tsx",
		"server/index.js", "other/app/page.tsx", "svc/main.py", "node_modules/x/app/page.tsx",
	} {
		lang := "typescript"
		if strings.HasSuffix(p, ".js") {
			lang = "javascript"
		} else if strings.HasSuffix(p, ".py") {
			lang = "python"
		}
		add(graph.Node{FilePath: p, Symbol: p, FQN: p, Kind: graph.KindFile, Language: lang})
	}
	add(graph.Node{FilePath: "app/page.tsx", Symbol: "Home", FQN: "Home", Kind: graph.KindFunction, Language: "typescript", StartLine: 1, EndLine: 1})
	add(graph.Node{FilePath: "app/(shop)/cart/page.tsx", Symbol: "Cart", FQN: "Cart", Kind: graph.KindFunction, Language: "typescript", StartLine: 1, EndLine: 1})
	add(graph.Node{FilePath: "app/api/users/route.ts", Symbol: "GET", FQN: "GET", Kind: graph.KindFunction, Language: "typescript", StartLine: 1, EndLine: 1})
	add(graph.Node{FilePath: "app/api/users/route.ts", Symbol: "POST", FQN: "POST", Kind: graph.KindFunction, Language: "typescript", StartLine: 2, EndLine: 2})
	add(graph.Node{FilePath: "server/index.js", Symbol: "getStatus", FQN: "getStatus", Kind: graph.KindFunction, Language: "javascript", StartLine: 2, EndLine: 2})
	add(graph.Node{FilePath: "svc/main.py", Symbol: "hello", FQN: "hello", Kind: graph.KindFunction, Language: "python", StartLine: 5, EndLine: 7, Docstring: "Say hello."})

	rep, err := svc.Features(proj, FeaturesOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"program:webctl",
		"http_route:ANY /hello",
		"http_route:GET /status",
		"api_route:GET /api/users",
		"api_route:POST /api/users",
		"page:/",
		"page:/cart",
	}
	if got := featureIDs(rep); !reflect.DeepEqual(got, want) {
		t.Fatalf("features = %v\nwant     %v", got, want)
	}
	home := featureByID(rep, "page:/")
	if home.Confidence != "confirmed" || home.Detector != "path" || home.Handler == nil || home.Handler.Symbol != "Home" || home.Surface != "UI" {
		t.Errorf("home = %+v", home)
	}
	if get := featureByID(rep, "api_route:GET /api/users"); get.Handler == nil || get.Handler.StartLine != 1 {
		t.Errorf("route GET handler = %+v", get.Handler)
	}
	st := featureByID(rep, "http_route:GET /status")
	if st.Confidence != "candidate" || st.Detector != "pattern" || st.Handler == nil || st.Handler.Symbol != "getStatus" {
		t.Errorf("express route = %+v", st)
	}
	hello := featureByID(rep, "http_route:ANY /hello")
	if hello.Description != "Say hello." || hello.DescriptionSource != "docstring" || hello.Handler == nil {
		t.Errorf("flask route = %+v", hello)
	}
	if bin := featureByID(rep, "program:webctl"); bin.Registration.File != "package.json" || bin.Handler != nil {
		t.Errorf("bin program = %+v", bin)
	}
	// TS/JS/Python without --precise have no call edges: honest confidence.
	if rep.CallGraph != CallGraphUnresolved || !strings.Contains(rep.Resolution, "precise") {
		t.Errorf("call_graph=%q resolution=%q", rep.CallGraph, rep.Resolution)
	}
}

func TestFeatureScanHandlerResolutionNeverGuessesAcrossPackages(t *testing.T) {
	nodes := []graph.Node{
		{ID: 1, FilePath: "a/x.go", Symbol: "run", FQN: "a.run", Kind: graph.KindFunction, Language: "go"},
		{ID: 2, FilePath: "b/y.go", Symbol: "run", FQN: "b.run", Kind: graph.KindFunction, Language: "go"},
		{ID: 3, FilePath: "a/x.go", Symbol: "solo", FQN: "a.solo", Kind: graph.KindFunction, Language: "go"},
		{ID: 4, FilePath: "c/z.go", Symbol: "dup", FQN: "c.dup", Kind: graph.KindFunction, Language: "go"},
		{ID: 5, FilePath: "c/w.go", Symbol: "dup", FQN: "c.dup", Kind: graph.KindFunction, Language: "go"},
		{ID: 6, FilePath: "a/x_test.go", Symbol: "ghost", FQN: "a.ghost", Kind: graph.KindFunction, Language: "go"},
		{ID: 7, FilePath: "m/s.go", Symbol: "handle", FQN: "m.Server.handle", Kind: graph.KindMethod, Language: "go"},
		{ID: 8, FilePath: "m/s.go", Symbol: "handle", FQN: "m.Client.handle", Kind: graph.KindMethod, Language: "go"},
	}
	s := newFeatureScan(t.TempDir(), "p", nodes)
	res := func(file, name string) *graph.Node {
		return s.resolveHandler(regFor(file, name, "", ""))
	}
	sameFile := regFor("b/y.go", "run", "", "")
	sameFile.Handler.SameFileOnly = true
	if n := s.resolveHandler(sameFile); n == nil || n.ID != 2 {
		t.Errorf("same-file-only reference resolves in its own file: %+v", n)
	}
	sameFile.File = "z/z.go"
	if n := s.resolveHandler(sameFile); n != nil {
		t.Errorf("same-file-only reference must not leak to other files: %+v", n)
	}
	if n := res("a/x.go", "run"); n == nil || n.ID != 1 {
		t.Errorf("same-file tier: %+v", n)
	}
	if n := res("a/other.go", "run"); n == nil || n.ID != 1 {
		t.Errorf("same-dir tier: %+v", n)
	}
	if n := res("z/z.go", "run"); n != nil {
		t.Errorf("two project-wide candidates must stay unresolved, got %+v", n)
	}
	if n := res("z/z.go", "solo"); n == nil || n.ID != 3 {
		t.Errorf("project-unique tier: %+v", n)
	}
	if n := res("c/z.go", "dup"); n == nil || n.ID != 4 {
		t.Errorf("same-file beats same-dir: %+v", n)
	}
	if n := res("z/z.go", "ghost"); n != nil {
		t.Errorf("test-file symbols are never handlers: %+v", n)
	}
	if n := s.resolveHandler(regFor("m/s.go", "handle", "Client", "")); n == nil || n.ID != 8 {
		t.Errorf("receiver-qualified method: %+v", n)
	}
	if n := res("m/s.go", "handle"); n != nil {
		t.Errorf("ambiguous methods without receiver must stay unresolved: %+v", n)
	}
	if s.ambiguous == 0 || len(s.ambiguousEx) == 0 {
		t.Error("ambiguity should be counted for the summary note")
	}
}

func regFor(file, name, recv, qualifier string) featuresrc.Registration {
	return featuresrc.Registration{
		Kind: featuresrc.KindCLICommand, Label: name, File: file,
		Handler: &featuresrc.HandlerRef{Name: name, Recv: recv, Qualifier: qualifier},
	}
}
