package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Regression tests for process step identity, repeat handling, partial call
// order disclosure and the --query evaluation cap.

func processDupFixture() map[string]string {
	return map[string]string{
		"go.mod": "module example.com/dup\n\ngo 1.25\n",
		"cmd/dup/main.go": `package main

import (
	"net/http"

	"example.com/dup/internal/api"
)

func main() {
	mux := http.NewServeMux()
	api.Routes(mux)
}
`,
		"internal/api/api.go": `package api

import "net/http"

func Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /x", x)
}

func x(w http.ResponseWriter, r *http.Request) {
	a()
	b()
	c()
}

func a() { leaf() }

func b() { leaf() }

func c() { leaf(); leaf() }

func leaf() {}
`,
	}
}

func stepKey(s ProcessStep) string {
	return fmt.Sprintf("%s:%d:%s", s.File, s.StartLine, s.FQN)
}

func TestProcessStepsAreUniqueByNode(t *testing.T) {
	svc, proj := processService(t, processDupFixture())
	rep, err := svc.Processes(proj, ProcessesOptions{Kinds: []string{"http_route"}})
	if err != nil {
		t.Fatal(err)
	}
	p := processByID(rep, "http_route:GET /x")
	if p == nil {
		t.Fatalf("no /x process in %+v", rep.Processes)
	}
	seen := map[string]bool{}
	for _, s := range p.Steps {
		if seen[stepKey(s)] {
			t.Fatalf("duplicate step %s in %v", stepKey(s), processFQNs(p))
		}
		seen[stepKey(s)] = true
	}
	want := "api.x,api.a,api.leaf,api.b,api.c"
	if got := strings.Join(processFQNs(p), ","); got != want {
		t.Fatalf("steps = %s, want %s (first appearance in call order)", got, want)
	}
}

func TestExploreProcessMatchedSeedsAreUnique(t *testing.T) {
	svc, proj := processService(t, processDupFixture())
	rep, err := svc.Explore(context.Background(), proj, "leaf", ExploreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, p := range rep.Processes {
		seen := map[string]bool{}
		for _, m := range p.MatchedSeeds {
			if seen[m] {
				t.Fatalf("process %s lists matched seed %q twice: %v", p.ID, m, p.MatchedSeeds)
			}
			seen[m] = true
		}
		stepSeen := map[string]bool{}
		for _, s := range p.Steps {
			if stepSeen[stepKey(s)] {
				t.Fatalf("process %s repeats step %s", p.ID, stepKey(s))
			}
			stepSeen[stepKey(s)] = true
		}
		if p.ID == "http_route:GET /x" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the /x process for seed leaf: %+v", rep.Processes)
	}
}

func TestProcessKeepsRepeatWhoseOriginalWasTrimmed(t *testing.T) {
	files := map[string]string{
		"go.mod": "module example.com/rep\n\ngo 1.25\n",
		"cmd/rep/main.go": `package main

import (
	"net/http"

	"example.com/rep/internal/api"
)

func main() {
	mux := http.NewServeMux()
	api.Routes(mux)
}
`,
		"internal/api/api.go": `package api

import "net/http"

func Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /x", x)
}

func x(w http.ResponseWriter, r *http.Request) {
	a()
	c()
}

func a() { c() }

func c() { d() }

func d() {}
`,
	}
	svc, proj := processService(t, files)
	// max 3 steps: x, then its children a and c are admitted; the original
	// expansion of c (under a) is trimmed, so the repeat under x is all that
	// is left of c and must still be listed.
	rep, err := svc.Processes(proj, ProcessesOptions{Kinds: []string{"http_route"}, MaxSteps: 3})
	if err != nil {
		t.Fatal(err)
	}
	p := processByID(rep, "http_route:GET /x")
	if p == nil {
		t.Fatalf("no /x process")
	}
	if got := strings.Join(processFQNs(p), ","); got != "api.x,api.a,api.c" {
		t.Fatalf("steps = %s: a direct callee of the entry vanished", got)
	}
}

func processPartialFixture() map[string]string {
	files := map[string]string{}
	for k, v := range processFixture {
		files[k] = v
	}
	files["internal/api/api.go"] = strings.Replace(files["internal/api/api.go"],
		`mux.HandleFunc("GET /health", health)`,
		"mux.HandleFunc(\"GET /health\", health)\n\tmux.HandleFunc(\"POST /again\", again)", 1) +
		"\nfunc again(w http.ResponseWriter, r *http.Request) {\n\tusers.Register(r.FormValue(\"email\"))\n}\n"
	return files
}

func TestProcessesSurfaceUnreadableSourcePerProcess(t *testing.T) {
	svc, proj := processService(t, processPartialFixture())
	clean, err := svc.Processes(proj, ProcessesOptions{Kinds: []string{"http_route"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range clean.Processes {
		if len(p.PartialErrors) != 0 {
			t.Fatalf("readable source must not report partial errors: %s %v", p.ID, p.PartialErrors)
		}
	}
	if err := os.Remove(filepath.Join(proj, "internal/users/users.go")); err != nil {
		t.Fatal(err)
	}
	rep, err := svc.Processes(proj, ProcessesOptions{Kinds: []string{"http_route"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"http_route:POST /signup", "http_route:POST /again"} {
		p := processByID(rep, id)
		if p == nil {
			t.Fatalf("missing %s", id)
		}
		// both processes read the same unreadable file: the second one hits the
		// builder's file cache and must still be told.
		if len(p.PartialErrors) == 0 || !strings.Contains(p.PartialErrors[0], "internal/users/users.go") {
			t.Fatalf("%s lacks the unreadable-source disclosure: %v", id, p.PartialErrors)
		}
	}
	if h := processByID(rep, "http_route:GET /health"); h == nil || len(h.PartialErrors) != 0 {
		t.Fatalf("health never touches the file but reports %+v", h)
	}
	joined := strings.Join(rep.Notes, "\n")
	if !strings.Contains(joined, "name order") {
		t.Fatalf("report notes must say call order is partial: %v", rep.Notes)
	}

	ex, err := svc.Explore(context.Background(), proj, "Register", ExploreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range ex.Processes {
		if p.ID == "http_route:POST /signup" && len(p.PartialErrors) == 0 {
			t.Fatalf("explore process must disclose unreadable source: %+v", p)
		}
	}
}

func processManyRoutesFixture(n int) map[string]string {
	var routes, handlers strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&routes, "\tmux.HandleFunc(\"GET /r%03d\", handler%03d)\n", i, i)
		fmt.Fprintf(&handlers, "func handler%03d(w http.ResponseWriter, r *http.Request) {}\n\n", i)
	}
	return map[string]string{
		"go.mod": "module example.com/many\n\ngo 1.25\n",
		"api/api.go": "package api\n\nimport \"net/http\"\n\nfunc Routes(mux *http.ServeMux) {\n" +
			routes.String() + "}\n\n" + handlers.String(),
	}
}

func TestProcessesQueryReportsWhenEntrypointsWereNotEvaluated(t *testing.T) {
	total := processEvalCap + 5
	svc, proj := processService(t, processManyRoutesFixture(total))
	// handler304 is the last route: it sits beyond the evaluation cap.
	rep, err := svc.Processes(proj, ProcessesOptions{Kinds: []string{"http_route"}, Query: "handler304", Top: 10})
	if err != nil {
		t.Fatal(err)
	}
	if rep.EntrypointsTotal != total || rep.Evaluated != processEvalCap {
		t.Fatalf("entrypoints_total=%d evaluated=%d, want %d/%d", rep.EntrypointsTotal, rep.Evaluated, total, processEvalCap)
	}
	if !rep.Truncated {
		t.Fatal("a --query that could only inspect the first entrypoints must say truncated:true")
	}
	if rep.ProcessesTotal > rep.Evaluated {
		t.Fatalf("processes_total %d counts more than was evaluated (%d)", rep.ProcessesTotal, rep.Evaluated)
	}

	all, err := svc.Processes(proj, ProcessesOptions{Kinds: []string{"http_route"}, Top: 5})
	if err != nil {
		t.Fatal(err)
	}
	if all.EntrypointsTotal != total || all.Evaluated != 5 || !all.Truncated {
		t.Fatalf("no-query report = entrypoints_total:%d evaluated:%d truncated:%v", all.EntrypointsTotal, all.Evaluated, all.Truncated)
	}
}
