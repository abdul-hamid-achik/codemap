package featuresrc

import (
	"strings"
	"testing"
)

func detectGo(t *testing.T, opts GoOptions, files map[string]string) []Registration {
	t.Helper()
	var srcs []SourceFile
	for p, s := range files {
		srcs = append(srcs, SourceFile{Path: p, Src: []byte(s)})
	}
	regs, errs := DetectGo(srcs, opts)
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	SortRegistrations(regs)
	return regs
}

func find(regs []Registration, kind, label string) *Registration {
	for i := range regs {
		if regs[i].Kind == kind && regs[i].Label == label {
			return &regs[i]
		}
	}
	return nil
}

func labels(regs []Registration, kind string) []string {
	var out []string
	for _, r := range regs {
		if r.Kind == kind {
			out = append(out, r.Label)
		}
	}
	return out
}

const cobraMain = `package main

import "github.com/spf13/cobra"

var rootCmd = &cobra.Command{
	Use:   "tool",
	Short: "A tool",
	RunE:  func(cmd *cobra.Command, args []string) error { return cmd.Help() },
}

func main() { _ = rootCmd.Execute() }
`

const cobraCache = `package main

import "github.com/spf13/cobra"

var cacheCmd = &cobra.Command{Use: "cache", Short: "Manage the cache"}

var cacheSaveCmd = &cobra.Command{
	Use:   "save <name>",
	Short: "Save the cache",
	Long:  "Long text that must not win over Short.",
	RunE:  jsonHandler(runCacheSave),
}

var cacheDropCmd = &cobra.Command{
	Use:   "drop",
	Short: "Drop the cache",
	RunE:  func(cmd *cobra.Command, args []string) error { return nil },
}

var hiddenCmd = &cobra.Command{Use: "secret", Short: "Hidden one", Hidden: true, Run: runSecret}

func init() {
	cacheCmd.AddCommand(cacheSaveCmd, cacheDropCmd)
	rootCmd.AddCommand(cacheCmd, hiddenCmd, newMapCmd())
}
`

const cobraMap = `package main

import "github.com/spf13/cobra"

var mapCmd = newMapCmd()

func newMapCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "map",
		Short: "Show the map",
		Long:  "Longer text.",
		RunE:  runMap,
	}
	return cmd
}
`

func TestDetectGoCobraTree(t *testing.T) {
	regs := detectGo(t, GoOptions{RootName: "tool"}, map[string]string{
		"cmd/tool/main.go":  cobraMain,
		"cmd/tool/cache.go": cobraCache,
		"cmd/tool/map.go":   cobraMap,
	})
	got := labels(regs, KindCLICommand)
	want := []string{"cache", "cache drop", "cache save", "map", "secret"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("cli labels = %v, want %v", got, want)
	}
	save := find(regs, KindCLICommand, "cache save")
	if save.Invocation != "tool cache save" || save.Description != "Save the cache" || save.DescSource != DescRegistration {
		t.Errorf("cache save = %+v", save)
	}
	if save.Handler == nil || save.Handler.Name != "runCacheSave" {
		t.Errorf("wrapper handler not unwrapped: %+v", save.Handler)
	}
	if save.Parent != "cache" || save.Framework != "cobra" || save.Detector != DetectorGoAST || save.Confidence != Confirmed {
		t.Errorf("cache save meta = %+v", save)
	}
	if drop := find(regs, KindCLICommand, "cache drop"); drop.Handler != nil {
		t.Errorf("inline func literal must leave handler nil, got %+v", drop.Handler)
	}
	if m := find(regs, KindCLICommand, "map"); m.Handler == nil || m.Handler.Name != "runMap" || m.Invocation != "tool map" {
		t.Errorf("constructor-bound command = %+v", m)
	}
	if s := find(regs, KindCLICommand, "secret"); !s.Hidden || s.Handler == nil || s.Handler.Name != "runSecret" {
		t.Errorf("hidden command = %+v", s)
	}
	if c := find(regs, KindCLICommand, "cache"); c.Parent != "" || c.Handler != nil {
		t.Errorf("group command = %+v", c)
	}
	if find(regs, KindCLICommand, "tool") != nil {
		t.Error("the root command must not be a feature")
	}
	prog := find(regs, KindProgram, "tool")
	if prog == nil || prog.Description != "A tool" || prog.DescSource != DescRegistration || prog.Handler == nil || prog.Handler.Name != "main" {
		t.Errorf("program = %+v", prog)
	}
}

func TestDetectGoIgnoresLookalikes(t *testing.T) {
	regs := detectGo(t, GoOptions{}, map[string]string{"x/x.go": `package x

import (
	"os/exec"
	"example.com/other"
)

type Command struct{ Use string }

func f() {
	_ = exec.Command("ls")
	_ = Command{Use: "local"}
	_ = other.Command{Use: "foreign", Short: "x"}
}
`})
	if len(regs) != 0 {
		t.Fatalf("lookalikes produced features: %+v", regs)
	}
}

func TestDetectGoUrfave(t *testing.T) {
	regs := detectGo(t, GoOptions{}, map[string]string{"cmd/app/main.go": `package main

import "github.com/urfave/cli/v2"

func main() {
	app := &cli.App{
		Name: "app",
		Commands: []*cli.Command{
			{Name: "serve", Usage: "Serve things", Action: serve},
			{
				Name:  "db",
				Usage: "Database tools",
				Subcommands: []*cli.Command{
					{Name: "migrate", Usage: "Run migrations", Action: migrate},
				},
			},
		},
	}
	_ = app
}
`})
	serve := find(regs, KindCLICommand, "serve")
	if serve == nil || serve.Description != "Serve things" || serve.Handler == nil || serve.Handler.Name != "serve" || serve.Invocation != "app serve" {
		t.Fatalf("serve = %+v", serve)
	}
	mig := find(regs, KindCLICommand, "db migrate")
	if mig == nil || mig.Parent != "db" || mig.Framework != "urfave-cli" {
		t.Fatalf("db migrate = %+v (all: %v)", mig, labels(regs, KindCLICommand))
	}
}

const mcpServer = `package server

import (
	"fmt"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mark3labs/mcp-go/mcp"
)

const toolMap = "demo_map"

type Server struct{ srv *sdkmcp.Server }

func (s *Server) register() {
	if s.include("demo_map") {
		sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
			Name:        toolMap,
			Description: "Show the " + "map",
		}, s.handleMap)
	}
	sdkmcp.AddTool(s.srv, &sdkmcp.Tool{
		Name:        "demo_keys",
		Description: fmt.Sprintf("Keys bounded to %d names", 10),
	}, handleKeys)
	s.srv2.AddTool(mcp.NewTool("legacy_tool", mcp.WithDescription("Legacy description")), s.handleLegacy)
}

func (s *Server) handleMap()    {}
func handleKeys()               {}
func (s *Server) handleLegacy() {}
`

func TestDetectGoMCPTools(t *testing.T) {
	regs := detectGo(t, GoOptions{}, map[string]string{"internal/server/s.go": mcpServer})
	m := find(regs, KindRPCTool, "demo_map")
	if m == nil || m.Description != "Show the map" || m.Framework != "mcp-go-sdk" || m.Handler == nil || m.Handler.Name != "handleMap" || m.Handler.Recv != "Server" {
		t.Fatalf("demo_map = %+v", m)
	}
	if k := find(regs, KindRPCTool, "demo_keys"); k == nil || k.Description != "Keys bounded to … names" || k.Handler == nil || k.Handler.Name != "handleKeys" {
		t.Fatalf("demo_keys = %+v", k)
	}
	l := find(regs, KindRPCTool, "legacy_tool")
	if l == nil || l.Framework != "mcp-go" || l.Description != "Legacy description" || l.Handler == nil || l.Handler.Name != "handleLegacy" {
		t.Fatalf("legacy_tool = %+v", l)
	}
	if m.Line == 0 {
		t.Error("registration line missing")
	}
}

func TestDetectGoHTTPRoutes(t *testing.T) {
	regs := detectGo(t, GoOptions{}, map[string]string{"internal/api/routes.go": `package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/gin-gonic/gin"
)

type client struct{}

func (c *client) fetch(h http.Handler) {
	_ = c.Get("/no-handler")
}

func (a *API) routes(mux *http.ServeMux, r chi.Router, g *gin.Engine) {
	mux.HandleFunc("/health", a.health)
	mux.HandleFunc("POST /v1/items", http.HandlerFunc(a.create))
	r.Route("/api", func(r chi.Router) {
		r.Get("/users", a.listUsers)
		r.Route("/admin", func(r chi.Router) {
			r.Delete("/users/{id}", a.deleteUser)
		})
	})
	v1 := g.Group("/v1")
	v1.GET("/ping", ping)
	g.GET("/inline", func(c *gin.Context) {})
	r.Method("PUT", "/thing", a.putThing)
}
`})
	cases := map[string]string{
		"ANY /health":                  "health",
		"POST /v1/items":               "create",
		"GET /api/users":               "listUsers",
		"DELETE /api/admin/users/{id}": "deleteUser",
		"GET /v1/ping":                 "ping",
		"PUT /thing":                   "putThing",
	}
	for label, handler := range cases {
		r := find(regs, KindHTTPRoute, label)
		if r == nil {
			t.Errorf("route %q missing; have %v", label, labels(regs, KindHTTPRoute))
			continue
		}
		if r.Handler == nil || r.Handler.Name != handler {
			t.Errorf("route %q handler = %+v, want %s", label, r.Handler, handler)
		}
		if !r.ConfirmedIfHandler {
			t.Errorf("route %q should only be confirmed with a resolved handler", label)
		}
	}
	if r := find(regs, KindHTTPRoute, "GET /inline"); r == nil || r.Handler != nil {
		t.Errorf("inline route = %+v", r)
	}
	if find(regs, KindHTTPRoute, "GET /no-handler") != nil {
		t.Error("an HTTP client call without a handler is not a route")
	}
}

func TestDetectGoGRPC(t *testing.T) {
	regs := detectGo(t, GoOptions{}, map[string]string{"cmd/srv/main.go": `package main

import "google.golang.org/grpc"

func main() {
	s := grpc.NewServer()
	pb.RegisterGreeterServer(s, &greeter{})
	pb.RegisterBillingServer(s, newBilling())
}
`})
	g := find(regs, KindRPCTool, "Greeter")
	if g == nil || g.Handler == nil || g.Handler.Name != "greeter" || !g.Handler.IsType || g.Framework != "grpc" {
		t.Fatalf("Greeter = %+v", g)
	}
	if b := find(regs, KindRPCTool, "Billing"); b == nil || b.Handler == nil || b.Handler.Name != "billing" {
		t.Fatalf("Billing = %+v", b)
	}
}

func TestDetectGoPrograms(t *testing.T) {
	regs := detectGo(t, GoOptions{RootName: "myproj"}, map[string]string{
		"main.go": "// Command myproj does a thing. More detail.\npackage main\n\nfunc main() {}\n",
	})
	p := find(regs, KindProgram, "myproj")
	if p == nil || p.Description != "Does a thing." || p.DescSource != DescDocstring {
		t.Fatalf("root program = %+v", p)
	}
	regs = detectGo(t, GoOptions{}, map[string]string{"cmd/bench/main.go": "package main\n\nfunc main() {}\nfunc other() {}\n"})
	if p := find(regs, KindProgram, "bench"); p == nil || p.Detector != DetectorGoAST || p.Line != 3 {
		t.Fatalf("dir program = %+v", p)
	}
	regs = detectGo(t, GoOptions{}, map[string]string{"lib/lib.go": "package lib\n\nfunc main() {}\n"})
	if len(regs) != 0 {
		t.Fatalf("main outside package main is not a program: %+v", regs)
	}
}

func TestDetectGoParseErrorIsReportedNotFatal(t *testing.T) {
	regs, errs := DetectGo([]SourceFile{
		{Path: "a/bad.go", Src: []byte("package a\nfunc (")},
		{Path: "a/main.go", Src: []byte("package main\nfunc main() {}\n")},
	}, GoOptions{})
	if len(errs) != 1 || !strings.Contains(errs[0], "a/bad.go") {
		t.Fatalf("errs = %v", errs)
	}
	if len(regs) != 1 {
		t.Fatalf("regs = %+v", regs)
	}
}

func TestDetectGoCommandCycleIsSafe(t *testing.T) {
	regs := detectGo(t, GoOptions{}, map[string]string{"c/c.go": `package main

import "github.com/spf13/cobra"

var a = &cobra.Command{Use: "a", Short: "A", Run: runA}
var b = &cobra.Command{Use: "b", Short: "B", Run: runB}

func init() {
	a.AddCommand(b)
	b.AddCommand(a)
}
`})
	if len(regs) == 0 {
		t.Fatal("cycle should still produce features")
	}
}

func TestDetectGoDeterministic(t *testing.T) {
	files := map[string]string{"cmd/tool/main.go": cobraMain, "cmd/tool/cache.go": cobraCache, "cmd/tool/map.go": cobraMap}
	a := detectGo(t, GoOptions{}, files)
	for i := 0; i < 5; i++ {
		b := detectGo(t, GoOptions{}, files)
		if len(a) != len(b) {
			t.Fatalf("run %d: len %d != %d", i, len(b), len(a))
		}
		for j := range a {
			if a[j].Kind != b[j].Kind || a[j].Label != b[j].Label || a[j].Line != b[j].Line {
				t.Fatalf("run %d: nondeterministic at %d: %+v vs %+v", i, j, a[j], b[j])
			}
		}
	}
}
