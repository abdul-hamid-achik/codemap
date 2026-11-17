package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/abdul-hamid-achik/codemap/internal/app"
	"github.com/abdul-hamid-achik/codemap/internal/index"
)

// connectTestServer serves srv over an in-memory transport and returns a
// connected client session (the go-sdk client negotiates the latest protocol
// revision, i.e. one that defines structuredContent).
func connectTestServer(t *testing.T, srv *Server) *sdkmcp.ClientSession {
	t.Helper()
	clientT, serverT := sdkmcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = srv.serve(ctx, serverT) }()
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func listedResources(t *testing.T, cs *sdkmcp.ClientSession) map[string]*sdkmcp.Resource {
	t.Helper()
	lr, err := cs.ListResources(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]*sdkmcp.Resource{}
	for _, r := range lr.Resources {
		got[r.URI] = r
	}
	return got
}

// TestMCPResourcesExposeDocsTopics pins the resource catalog: one static
// codemap://docs/<topic> resource per internal/app docs.go topic (read through
// app.Docs so the catalog cannot drift from the guide) plus codemap://status.
// Resource content for a docs topic must be exactly the topic's guide text.
func TestMCPResourcesExposeDocsTopics(t *testing.T) {
	sess := newProfileTestSession(t)
	cs := connectTestServer(t, NewServer(sess))

	resources := listedResources(t, cs)
	topics := app.DocTopicNames()
	if len(topics) == 0 {
		t.Fatal("app.DocTopicNames is empty — the resource catalog has nothing to expose")
	}
	for _, topic := range topics {
		r, ok := resources["codemap://docs/"+topic]
		if !ok {
			t.Errorf("missing resource codemap://docs/%s (have %v)", topic, resources)
			continue
		}
		if r.Name != "codemap-docs-"+topic || r.MIMEType != "text/markdown" {
			t.Errorf("%s resource name/mime = %q/%q", topic, r.Name, r.MIMEType)
		}
		if r.Description == "" {
			t.Errorf("codemap://docs/%s has no description", topic)
		}
	}
	sr, ok := resources["codemap://status"]
	if !ok {
		t.Fatalf("missing codemap://status resource (have %v)", resources)
	}
	if sr.MIMEType != "application/json" {
		t.Errorf("codemap://status mime = %q, want application/json", sr.MIMEType)
	}

	// Reading a topic returns the playbook/docs topic content verbatim.
	rr, err := cs.ReadResource(context.Background(), &sdkmcp.ReadResourceParams{URI: "codemap://docs/workflow"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rr.Contents) != 1 {
		t.Fatalf("docs/workflow read returned %d contents, want 1", len(rr.Contents))
	}
	if got := rr.Contents[0].Text; got != app.Docs("workflow") {
		t.Errorf("docs/workflow resource content drifted from app.Docs: %d bytes vs %d", len(got), len(app.Docs("workflow")))
	}
	if !strings.Contains(rr.Contents[0].Text, "codemap_index") {
		t.Error("workflow topic resource should teach codemap tools")
	}

	// codemap://status returns the same JSON payload shape as codemap_status.
	str, err := cs.ReadResource(context.Background(), &sdkmcp.ReadResourceParams{URI: "codemap://status"})
	if err != nil {
		t.Fatal(err)
	}
	var statusPayload map[string]any
	if err := json.Unmarshal([]byte(str.Contents[0].Text), &statusPayload); err != nil {
		t.Fatalf("codemap://status is not JSON: %v (%s)", err, str.Contents[0].Text)
	}
	if _, ok := statusPayload["registered"]; !ok {
		t.Errorf("codemap://status payload lacks 'registered': %s", str.Contents[0].Text)
	}

	// Unknown URIs must be resource-not-found errors, not silent content.
	if _, err := cs.ReadResource(context.Background(), &sdkmcp.ReadResourceParams{URI: "codemap://docs/bogus"}); err == nil {
		t.Error("reading codemap://docs/bogus should fail")
	}
}

// TestMCPResourcesPromptsInEveryProfile pins that resources and prompts ride
// along under the lean profiles too — they add no tool-schema tokens, so the
// agent profile's measured 28-tool contract is untouched while clients still
// get the full resource catalog and prompt set.
func TestMCPResourcesPromptsInEveryProfile(t *testing.T) {
	for _, profile := range []string{ProfileFull, ProfileCore, ProfileAgent} {
		sess := newProfileTestSession(t)
		sess.Config.MCP.Profile = profile
		cs := connectTestServer(t, NewServer(sess))

		resources := listedResources(t, cs)
		for _, topic := range app.DocTopicNames() {
			if _, ok := resources["codemap://docs/"+topic]; !ok {
				t.Errorf("profile %s: missing resource codemap://docs/%s", profile, topic)
			}
		}
		if _, ok := resources["codemap://status"]; !ok {
			t.Errorf("profile %s: missing codemap://status resource", profile)
		}

		lp, err := cs.ListPrompts(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(lp.Prompts) != 3 {
			t.Errorf("profile %s: prompts/list returned %d prompts, want 3", profile, len(lp.Prompts))
		}
	}
}

// TestMCPPromptsRenderCodemapToolInstructions renders every prompt template
// and checks the instructions name existing codemap tools — and only tools the
// lean profiles register, since prompts are available in every profile.
func TestMCPPromptsRenderCodemapToolInstructions(t *testing.T) {
	sess := newProfileTestSession(t)
	cs := connectTestServer(t, NewServer(sess))

	lp, err := cs.ListPrompts(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]*sdkmcp.Prompt{}
	for _, p := range lp.Prompts {
		names[p.Name] = p
	}
	for _, want := range []string{"review_diff", "triage_impact", "orient_task"} {
		if _, ok := names[want]; !ok {
			t.Errorf("missing prompt %q (have %v)", want, names)
		}
	}
	if p := names["triage_impact"]; p == nil || len(p.Arguments) != 1 || !p.Arguments[0].Required {
		t.Errorf("triage_impact must declare one required 'symbol' argument, got %+v", p)
	}

	ctx := context.Background()
	renders := map[string]string{}
	for name, args := range map[string]map[string]string{
		"review_diff":   {"since": "main"},
		"triage_impact": {"symbol": "graph.Store.Close"},
		"orient_task":   {"task": "add retry with backoff to uploads"},
	} {
		gp, err := cs.GetPrompt(ctx, &sdkmcp.GetPromptParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(gp.Messages) != 1 || gp.Messages[0].Role != "user" {
			t.Fatalf("%s: want one user message, got %+v", name, gp.Messages)
		}
		tc, ok := gp.Messages[0].Content.(*sdkmcp.TextContent)
		if !ok {
			t.Fatalf("%s: prompt content is %T, want TextContent", name, gp.Messages[0].Content)
		}
		renders[name] = tc.Text
	}
	if !strings.Contains(renders["review_diff"], `since="main"`) || !strings.Contains(renders["review_diff"], "codemap_review") {
		t.Errorf("review_diff render must interpolate since and name codemap_review:\n%s", renders["review_diff"])
	}
	if !strings.Contains(renders["triage_impact"], "graph.Store.Close") || !strings.Contains(renders["triage_impact"], "codemap_impact") {
		t.Errorf("triage_impact render must interpolate symbol and name codemap_impact:\n%s", renders["triage_impact"])
	}
	if !strings.Contains(renders["orient_task"], "add retry with backoff to uploads") || !strings.Contains(renders["orient_task"], "codemap_read_order") {
		t.Errorf("orient_task render must interpolate task and name codemap_read_order:\n%s", renders["orient_task"])
	}

	// Prompts are served under every profile, so they may only teach tools the
	// leanest profile (agent) registers — the same invariant the instructions
	// test pins for the server instructions text.
	toolRe := regexp.MustCompile(`codemap_[a-z][a-z_]*`)
	for name, text := range renders {
		for _, tok := range toolRe.FindAllString(text, -1) {
			if !agentTools[tok] {
				t.Errorf("%s teaches %q, which the agent profile does not register", name, tok)
			}
		}
		if len(toolRe.FindAllString(text, -1)) == 0 {
			t.Errorf("%s render names no codemap tool", name)
		}
	}

	// A required argument must be enforced, not silently rendered empty.
	if _, err := cs.GetPrompt(ctx, &sdkmcp.GetPromptParams{Name: "triage_impact"}); err == nil {
		t.Error("triage_impact without 'symbol' must fail")
	}
}

// TestTrimStructuredToolTextBounded runs the text-trim middleware directly for
// the branches an integration client cannot reach (no live session): a client
// whose protocol revision predates structuredContent keeps today's full-JSON
// text and loses the (to it meaningless) structured field, and results without
// structured content or with errors are never trimmed.
func TestTrimStructuredToolTextBounded(t *testing.T) {
	small := json.RawMessage(`{"ok":true,"n":1}`)
	big := json.RawMessage(`{"data":"` + strings.Repeat("x", 3*maxToolTextBytes) + `"}`)
	mkNext := func(res sdkmcp.Result) sdkmcp.MethodHandler {
		return func(context.Context, string, sdkmcp.Request) (sdkmcp.Result, error) { return res, nil }
	}
	s := &Server{}
	// A CallToolRequest built by hand has no session: the conservative read is
	// "client predates structuredContent".
	callReq := &sdkmcp.CallToolRequest{}
	ctx := context.Background()

	res := &sdkmcp.CallToolResult{
		Content:           []sdkmcp.Content{&sdkmcp.TextContent{Text: string(big)}},
		StructuredContent: big,
	}
	if _, err := s.trimStructuredToolText(mkNext(res))(ctx, "tools/call", callReq); err != nil {
		t.Fatal(err)
	}
	if res.StructuredContent != nil {
		t.Error("pre-structuredContent client should not receive the structured field")
	}
	if got := textOf(res); got != string(big) {
		t.Errorf("pre-structuredContent client must keep the full text mirror (got %d bytes, want %d)", len(got), len(big))
	}

	// Error results are never trimmed, structured or not.
	errRes := &sdkmcp.CallToolResult{
		Content:           []sdkmcp.Content{&sdkmcp.TextContent{Text: "Error: boom"}},
		IsError:           true,
		StructuredContent: small,
	}
	if _, err := s.trimStructuredToolText(mkNext(errRes))(ctx, "tools/call", callReq); err != nil {
		t.Fatal(err)
	}
	if textOf(errRes) != "Error: boom" || errRes.StructuredContent == nil {
		t.Error("error results must pass through untouched")
	}

	// Non-tools/call methods pass through untouched.
	other := &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: string(big)}}, StructuredContent: big}
	if _, err := s.trimStructuredToolText(mkNext(other))(ctx, "prompts/get", callReq); err != nil {
		t.Fatal(err)
	}
	if other.StructuredContent == nil || textOf(other) != string(big) {
		t.Error("non tools/call results must pass through untouched")
	}

	// The bounded pointer itself: compact JSON, bounded, honest about size.
	ptr := toolTextPointer(big)
	if len(ptr) >= maxToolTextBytes {
		t.Errorf("pointer text is %d bytes, must stay under %d", len(ptr), maxToolTextBytes)
	}
	var shape struct {
		Structured bool   `json:"structured"`
		FullBytes  int    `json:"full_bytes"`
		Preview    string `json:"preview"`
		Hint       string `json:"hint"`
	}
	if err := json.Unmarshal([]byte(ptr), &shape); err != nil {
		t.Fatalf("pointer text is not JSON: %v (%s)", err, ptr)
	}
	if !shape.Structured || shape.FullBytes != len(big) || shape.Hint == "" {
		t.Errorf("pointer text is dishonest: %+v", shape)
	}
	if !strings.HasPrefix(string(big), strings.TrimSuffix(shape.Preview, "…")) {
		t.Error("preview must be the payload's head")
	}
}

// bigMapProject writes an N-package Go module whose packages call the next
// package in a chain, so codemap_map's default-capped report (50 subsystems /
// 100 bridges) is guaranteed to exceed the 4 KiB text bound.
func bigMapProject(t *testing.T, packages int) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module big.example/map\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := range packages {
		dir := filepath.Join(root, "pkg", fmt.Sprintf("p%02d", i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		fmt.Fprintf(&b, "package p%02d\n\n", i)
		if i+1 < packages {
			fmt.Fprintf(&b, "import \"big.example/map/pkg/p%02d\"\n\n", i+1)
			fmt.Fprintf(&b, "// A%02d bridges to the next package.\nfunc A%02d() { p%02d.A%02d() }\n\n", i, i, i+1, i+1)
		} else {
			fmt.Fprintf(&b, "// A%02d is the chain terminator.\nfunc A%02d() {}\n\n", i, i)
		}
		fmt.Fprintf(&b, "func B%02d() { A%02d() }\n", i, i)
		if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// TestCodemapMapPayloadNotDoubled measures codemap_map's wire payload over a
// real tools/call round-trip. Before the text trim, the result carried the map
// JSON twice (text mirror + structuredContent); now a structuredContent-capable
// client gets the full JSON once plus a bounded text pointer.
func TestCodemapMapPayloadNotDoubled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("CODEMAP_DATA", filepath.Join(home, "data"))
	t.Setenv("CODEMAP_CONFIG", "")
	t.Setenv("XDG_DATA_HOME", "")

	proj := bigMapProject(t, 24)
	sess, err := app.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if _, err := app.NewService(sess).Index(context.Background(), proj, index.Options{}, false); err != nil {
		t.Fatal(err)
	}
	cs := connectTestServer(t, NewServer(sess))

	res, err := cs.CallTool(context.Background(), &sdkmcp.CallToolParams{
		Name:      "codemap_map",
		Arguments: map[string]any{"path": proj},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("codemap_map error: %s", textOf(res))
	}
	if res.StructuredContent == nil {
		t.Fatal("codemap_map result has no structuredContent — the SDK always mirrors the typed output there")
	}
	structuredJSON, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if len(structuredJSON) <= maxToolTextBytes {
		t.Fatalf("fixture too small to exercise the trim: structured payload is %d bytes (need > %d); grow bigMapProject", len(structuredJSON), maxToolTextBytes)
	}

	text := textOf(res)
	if len(text) >= maxToolTextBytes {
		t.Errorf("text mirror is %d bytes, must stay under %d when structuredContent is present:\n%.400s", len(text), maxToolTextBytes, text)
	}
	if !strings.Contains(text, `"structured":true`) {
		t.Errorf("bounded text should be a pointer to structuredContent, got: %.400s", text)
	}
	// The structured payload stays complete: spot-check map contract fields.
	if !strings.Contains(string(structuredJSON), `"subsystems"`) || !strings.Contains(string(structuredJSON), `"bridges"`) {
		t.Error("structured payload is missing map sections")
	}

	before := 2 * len(structuredJSON) // old behavior: the same JSON twice
	after := len(structuredJSON) + len(text)
	t.Logf("codemap_map payload on %d-package fixture: structured=%d bytes, text=%d bytes; wire before trim=%d, after=%d (saved %.0f%%)",
		24, len(structuredJSON), len(text), before, after, 100*float64(before-after)/float64(before))
}
