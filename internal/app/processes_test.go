package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/index"
)

// processFixture is a small net/http service: two routes whose handlers reach
// a service chain (signup → users.Register → validate/persist → store.Put) plus
// an unrelated helper, and a main that wires the mux.
var processFixture = map[string]string{
	"go.mod": "module example.com/pr\n\ngo 1.25\n",
	"cmd/pr/main.go": `package main

import (
	"net/http"

	"example.com/pr/internal/api"
)

func main() {
	mux := http.NewServeMux()
	api.Routes(mux)
}
`,
	"internal/api/api.go": `package api

import (
	"net/http"

	"example.com/pr/internal/users"
)

func Routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /signup", signup)
	mux.HandleFunc("GET /health", health)
}

func signup(w http.ResponseWriter, r *http.Request) {
	users.Register(r.FormValue("email"))
}

func health(w http.ResponseWriter, r *http.Request) {}
`,
	"internal/users/users.go": `package users

import "example.com/pr/internal/store"

// Register creates an account for a new email address.
func Register(email string) {
	validateEmail(email)
	persist(email)
}

func validateEmail(email string) {}

func persist(email string) { store.Put(email) }
`,
	"internal/store/store.go": `package store

func Put(key string) {}

func Unrelated() {}
`,
}

func processService(t *testing.T, files map[string]string) (*Service, string) {
	t.Helper()
	svc := openFeatureService(t)
	svc.s.Config.Vecgrep.Enabled = false
	proj := t.TempDir()
	writeFeatureTree(t, proj, files)
	if _, err := svc.Index(context.Background(), proj, index.Options{NoLSP: true}, false); err != nil {
		t.Fatal(err)
	}
	return svc, proj
}

func processByID(rep *ProcessesReport, id string) *Process {
	for i := range rep.Processes {
		if rep.Processes[i].ID == id {
			return &rep.Processes[i]
		}
	}
	return nil
}

func processFQNs(p *Process) []string {
	out := make([]string, 0, len(p.Steps))
	for _, s := range p.Steps {
		out = append(out, s.FQN)
	}
	return out
}

func TestProcessesBuildEntrypointFlows(t *testing.T) {
	svc, proj := processService(t, processFixture)
	rep, err := svc.Processes(proj, ProcessesOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.SchemaVersion != ProcessesSchemaVersion || !rep.Indexed || rep.Depth != DefaultProcessDepth || rep.MaxSteps != DefaultProcessSteps {
		t.Fatalf("report identity/bounds = %+v", rep)
	}

	signup := processByID(rep, "http_route:POST /signup")
	if signup == nil {
		var ids []string
		for _, p := range rep.Processes {
			ids = append(ids, p.ID)
		}
		t.Fatalf("no signup process among %v", ids)
	}
	if signup.Kind != "http_route" || signup.Entry == nil || signup.Entry.File != "internal/api/api.go" {
		t.Fatalf("signup identity = %+v", signup)
	}
	got := processFQNs(signup)
	want := []string{"api.signup", "users.Register", "users.validateEmail", "users.persist", "store.Put"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("signup steps = %v, want %v (call order)", got, want)
	}
	for i, s := range signup.Steps {
		if s.File == "" || s.StartLine == 0 || s.Kind == "" {
			t.Fatalf("step %d lacks location/kind: %+v", i, s)
		}
	}
	if signup.Steps[0].Depth != 0 || signup.Steps[1].Depth != 1 || signup.Steps[4].Depth != 3 {
		t.Fatalf("depths = %+v", signup.Steps)
	}
	if len(signup.Files) != 3 || signup.Files[0] != "internal/api/api.go" {
		t.Fatalf("files = %v", signup.Files)
	}
	if signup.Truncated || signup.CallGraph == "" || signup.StepsTotal != 5 {
		t.Fatalf("signup bounds/call_graph = truncated:%v call_graph:%q total:%d", signup.Truncated, signup.CallGraph, signup.StepsTotal)
	}

	health := processByID(rep, "http_route:GET /health")
	if health == nil || len(health.Steps) != 1 || health.Steps[0].FQN != "api.health" {
		t.Fatalf("health process = %+v", health)
	}
	if processByID(rep, "program:pr") == nil {
		t.Fatalf("program entry missing from %+v", rep.ProcessesTotal)
	}
	if rep.ProcessesTotal != len(rep.Processes) || rep.Truncated {
		t.Fatalf("totals = %d/%d truncated=%v", len(rep.Processes), rep.ProcessesTotal, rep.Truncated)
	}

	// deterministic, JSON-serializable
	again, err := svc.Processes(proj, ProcessesOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(rep.Processes)
	b, _ := json.Marshal(again.Processes)
	if string(a) != string(b) {
		t.Fatal("processes are not deterministic across calls")
	}
}

func TestProcessesFiltersAndBounds(t *testing.T) {
	svc, proj := processService(t, processFixture)

	routes, err := svc.Processes(proj, ProcessesOptions{Kinds: []string{"http_route"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(routes.Processes) != 2 {
		t.Fatalf("kind filter returned %d processes", len(routes.Processes))
	}
	for _, p := range routes.Processes {
		if p.Kind != "http_route" {
			t.Fatalf("kind filter leaked %s", p.ID)
		}
	}

	// the lexical floor's tokenization: stopwords drop, so only "register"
	// and "email" carry the query; the signup chain mentions both.
	q, err := svc.Processes(proj, ProcessesOptions{Query: "how does the email register work"})
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Processes) != 1 || q.Processes[0].ID != "http_route:POST /signup" || q.ProcessesTotal != 1 {
		t.Fatalf("query matched %d processes: %+v", len(q.Processes), q.Processes)
	}
	none, err := svc.Processes(proj, ProcessesOptions{Query: "kubernetes"})
	if err != nil || len(none.Processes) != 0 || none.ProcessesTotal != 0 {
		t.Fatalf("unmatched query = %+v, %v", none, err)
	}

	top, err := svc.Processes(proj, ProcessesOptions{Top: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(top.Processes) != 1 || !top.Truncated || top.ProcessesTotal < 3 {
		t.Fatalf("top bound = %d processes, truncated=%v, total=%d", len(top.Processes), top.Truncated, top.ProcessesTotal)
	}

	short, err := svc.Processes(proj, ProcessesOptions{Kinds: []string{"http_route"}, Query: "signup", MaxSteps: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(short.Processes) != 1 || len(short.Processes[0].Steps) > 2 || !short.Processes[0].Truncated {
		t.Fatalf("max_steps bound = %+v", short.Processes)
	}
	shallow, err := svc.Processes(proj, ProcessesOptions{Kinds: []string{"http_route"}, Query: "signup", Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range shallow.Processes[0].Steps {
		if s.Depth > 1 {
			t.Fatalf("depth bound exceeded: %+v", s)
		}
	}
	if !shallow.Processes[0].Truncated {
		t.Fatal("a depth-cut process must say it is truncated")
	}
}

func TestProcessesInvalidOptionsAndUnindexed(t *testing.T) {
	svc, proj := processService(t, processFixture)
	for _, opts := range []ProcessesOptions{
		{Top: MaxProcessesTop + 1},
		{Top: -1},
		{Depth: MaxFlowDepth + 1},
		{MaxSteps: MaxProcessSteps + 1},
		{Kinds: []string{"bogus"}},
	} {
		if _, err := svc.Processes(proj, opts); err == nil {
			t.Fatalf("options %+v were accepted", opts)
		}
	}
	empty := openFeatureService(t)
	rep, err := empty.Processes(t.TempDir(), ProcessesOptions{})
	if err != nil || rep.Indexed || len(rep.Processes) != 0 {
		t.Fatalf("unindexed project = %+v, %v", rep, err)
	}
}

func TestProcessesWithoutFeatures(t *testing.T) {
	svc, proj := processService(t, map[string]string{
		"go.mod":         "module example.com/lib\n\ngo 1.25\n",
		"lib/lib.go":     "package lib\n\nfunc Helper() { inner() }\n\nfunc inner() {}\n",
		"lib/lib_two.go": "package lib\n\nfunc Other() {}\n",
	})
	rep, err := svc.Processes(proj, ProcessesOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Indexed || len(rep.Processes) != 0 || rep.ProcessesTotal != 0 || rep.Truncated {
		t.Fatalf("library project processes = %+v", rep)
	}
	b, _ := json.Marshal(rep)
	if !strings.Contains(string(b), `"processes":[]`) {
		t.Fatalf("empty list must serialize as []: %s", b)
	}
}

func TestExploreGroupsSeedsByProcess(t *testing.T) {
	svc, proj := processService(t, processFixture)
	rep, err := svc.Explore(context.Background(), proj, "Register", ExploreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.PartialErrors) != 0 {
		t.Fatalf("partial errors: %+v", rep.PartialErrors)
	}
	if len(rep.Processes) == 0 {
		t.Fatalf("explore returned no processes for seeds %+v", rep.Seeds)
	}
	var signup *ExploreProcess
	for i := range rep.Processes {
		if rep.Processes[i].ID == "http_route:POST /signup" {
			signup = &rep.Processes[i]
		}
	}
	if signup == nil {
		t.Fatalf("signup route missing from %+v", rep.Processes)
	}
	if len(signup.MatchedSeeds) != 1 || signup.MatchedSeeds[0] != "users.Register" {
		t.Fatalf("matched seeds = %v", signup.MatchedSeeds)
	}
	var chain []string
	for _, s := range signup.Steps {
		chain = append(chain, s.FQN)
	}
	// the route → handler → service chain to the seed, not the whole tree
	if strings.Join(chain, ",") != "api.signup,users.Register" {
		t.Fatalf("chain = %v", chain)
	}
	if signup.Entry == nil || signup.CallGraph == "" || len(rep.Processes) > DefaultExploreProcesses {
		t.Fatalf("process shape/bound = %+v (n=%d)", signup, len(rep.Processes))
	}

	// a seed reachable from no entrypoint contributes nothing
	lone, err := svc.Explore(context.Background(), proj, "Unrelated", ExploreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(lone.Seeds) == 0 || len(lone.Processes) != 0 || len(lone.PartialErrors) != 0 {
		t.Fatalf("unreachable seed = processes:%d partial:%+v", len(lone.Processes), lone.PartialErrors)
	}

	// the serialized report always carries the field, and task-context embeds it verbatim
	b, _ := json.Marshal(lone)
	if !strings.Contains(string(b), `"processes":[]`) {
		t.Fatalf("explore JSON lacks the processes field: %s", b)
	}
	tc, err := svc.TaskContext(context.Background(), proj, "Register", TaskContextOptions{Mode: TaskModeUnderstand})
	if err != nil {
		t.Fatal(err)
	}
	tb, _ := json.Marshal(tc)
	if tc.Explore == nil || !strings.Contains(string(tb), `"processes":[{"id":"http_route:POST /signup"`) {
		t.Fatalf("task-context explore does not carry processes: %s", tb)
	}
}

func TestExploreProcessBoundsAndOptOut(t *testing.T) {
	svc, proj := processService(t, processFixture)

	one, err := svc.Explore(context.Background(), proj, "Register persist", ExploreOptions{Processes: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(one.Processes) != 1 {
		t.Fatalf("processes bound: got %d", len(one.Processes))
	}
	off, err := svc.Explore(context.Background(), proj, "Register", ExploreOptions{Processes: -1})
	if err != nil {
		t.Fatal(err)
	}
	if len(off.Processes) != 0 {
		t.Fatalf("opt-out still returned %d processes", len(off.Processes))
	}
	if _, err := normalizeExploreOptions(ExploreOptions{Processes: MaxExploreProcesses + 1}); err == nil {
		t.Fatal("processes above the maximum were accepted")
	}
}

func TestExploreWithoutFeaturesHasNoProcessesAndNoError(t *testing.T) {
	svc, proj := processService(t, map[string]string{
		"go.mod":     "module example.com/lib\n\ngo 1.25\n",
		"lib/lib.go": "package lib\n\nfunc Helper() { inner() }\n\nfunc inner() {}\n",
	})
	rep, err := svc.Explore(context.Background(), proj, "Helper", ExploreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Seeds) == 0 || rep.Processes == nil || len(rep.Processes) != 0 || len(rep.PartialErrors) != 0 {
		t.Fatalf("no-features explore = seeds:%d processes:%v partial:%+v", len(rep.Seeds), rep.Processes, rep.PartialErrors)
	}
}

func TestSelectExploreStepsKeepsEntryAndSeedsWhenCapped(t *testing.T) {
	root := &FlowStep{Symbol: "entry"}
	var steps []*FlowStep
	steps = append(steps, root)
	onPath := map[*FlowStep]bool{root: true}
	for i := 0; i < 12; i++ {
		s := &FlowStep{Symbol: string(rune('a' + i))}
		steps = append(steps, s)
		onPath[s] = true
	}
	seed := steps[len(steps)-1]
	got := selectExploreSteps(steps, onPath, []*FlowStep{seed}, root, 8)
	if len(got) != 8 {
		t.Fatalf("kept %d steps, want 8", len(got))
	}
	if got[0] != root || got[len(got)-1] != seed {
		t.Fatalf("entry and seed must survive the cap: first=%s last=%s", got[0].Symbol, got[len(got)-1].Symbol)
	}
	for i := 1; i < len(got); i++ {
		if indexOfStep(steps, got[i]) <= indexOfStep(steps, got[i-1]) {
			t.Fatal("capped steps are not in call order")
		}
	}
	if all := selectExploreSteps(steps[:4], onPath, nil, root, 8); len(all) != 4 {
		t.Fatalf("under the cap nothing is dropped, got %d", len(all))
	}
}

func indexOfStep(steps []*FlowStep, s *FlowStep) int {
	for i, c := range steps {
		if c == s {
			return i
		}
	}
	return -1
}
