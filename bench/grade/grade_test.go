package grade

import (
	"encoding/json"
	"testing"
)

func obj(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("bad json in test: %v", err)
	}
	return m
}

func TestExtractJSONBlock_LastFenceWins(t *testing.T) {
	final := "" +
		"Here is an illustrative shape:\n```json\n{\"callers\": [\"a.B\"]}\n```\n" +
		"After more work, the real answer:\n```json\n{\"callers\": [\"x.Y\", \"z.W\"]}\n```\n"
	m, err := ExtractJSONBlock(final)
	if err != nil {
		t.Fatal(err)
	}
	got := toStringSet(m["callers"])
	if !got["x.Y"] || !got["z.W"] || got["a.B"] {
		t.Fatalf("expected last block to win, got %v", sortedKeys(got))
	}
}

func TestExtractJSONBlock_MalformedTrailingFallsBack(t *testing.T) {
	final := "```json\n{\"count\": 5}\n```\nthen a broken one:\n```json\n{ not valid\n```\n"
	m, err := ExtractJSONBlock(final)
	if err != nil {
		t.Fatalf("expected fallback to earlier parseable block: %v", err)
	}
	if v, _ := toFloat(m["count"]); v != 5 {
		t.Fatalf("want count=5, got %v", m["count"])
	}
}

func TestExtractJSONBlock_NoLanguageTag(t *testing.T) {
	m, err := ExtractJSONBlock("prose\n```\n{\"alive\": true}\n```\n")
	if err != nil {
		t.Fatal(err)
	}
	if m["alive"] != true {
		t.Fatalf("want alive=true, got %v", m["alive"])
	}
}

func TestExtractJSONBlock_BareObjectFallback(t *testing.T) {
	m, err := ExtractJSONBlock("no fences here, just {\"count\": 42} at the end")
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := toFloat(m["count"]); v != 42 {
		t.Fatalf("want 42, got %v", m["count"])
	}
}

func TestExtractJSONBlock_None(t *testing.T) {
	if _, err := ExtractJSONBlock("no json at all"); err == nil {
		t.Fatal("expected error for missing json")
	}
}

func TestSetEqual_ExactMatch(t *testing.T) {
	got := obj(t, `{"callers":["a.B","c.D"]}`)
	want := obj(t, `{"callers":["c.D","a.B"]}`)
	r := setEqual("callers", got, want)
	if !r.Pass || r.Score != 1.0 {
		t.Fatalf("expected pass, got %+v", r)
	}
}

func TestSetEqual_PrecisionRecallOnMismatch(t *testing.T) {
	got := obj(t, `{"callers":["a.B","x.Y"]}`)  // one right, one wrong
	want := obj(t, `{"callers":["a.B","c.D"]}`) // missing c.D
	r := setEqual("callers", got, want)
	if r.Pass {
		t.Fatal("expected fail")
	}
	// precision = 1/2, recall = 1/2, F1 = 0.5
	if r.Score < 0.49 || r.Score > 0.51 {
		t.Fatalf("expected F1 ~0.5, got %v (%s)", r.Score, r.Detail)
	}
}

func TestNumeric_Tolerance(t *testing.T) {
	got := obj(t, `{"count":5}`)
	want := obj(t, `{"count":5}`)
	if r := numeric("count", got, want, 0); !r.Pass {
		t.Fatalf("exact numeric should pass: %+v", r)
	}
	got2 := obj(t, `{"count":6}`)
	if r := numeric("count", got2, want, 0); r.Pass {
		t.Fatal("off-by-one with tol=0 should fail")
	}
	if r := numeric("count", got2, want, 1); !r.Pass {
		t.Fatal("off-by-one with tol=1 should pass")
	}
}

func TestExact_MultiKey(t *testing.T) {
	got := obj(t, `{"file":"plumbing/hash.go","line":26,"extra":"ignored?"}`)
	want := obj(t, `{"file":"plumbing/hash.go","line":26}`)
	if r := exact(got, want); !r.Pass {
		t.Fatalf("expected pass (extra answer keys allowed): %+v", r)
	}
	bad := obj(t, `{"file":"plumbing/hash.go","line":27}`)
	if r := exact(bad, want); r.Pass {
		t.Fatal("wrong line should fail")
	}
}

func TestContainsPath_SubsequenceOfEdges(t *testing.T) {
	truth := obj(t, `{"edges":[["A","B"],["B","C"],["A","D"]]}`)
	if r := containsPath("path", obj(t, `{"path":["A","B","C"]}`), truth); !r.Pass {
		t.Fatalf("valid path should pass: %+v", r)
	}
	if r := containsPath("path", obj(t, `{"path":["A","C"]}`), truth); r.Pass {
		t.Fatal("A->C is not an edge; should fail")
	}
	if r := containsPath("path", obj(t, `{"path":["A"]}`), truth); r.Pass {
		t.Fatal("single-node path should fail")
	}
}

// TestContainsPath_ChecksEndpoints pins the fix for containsPath never
// validating the answer's path against truth["from"]/truth["to"]: before the
// fix, any single truth edge (or short walk) anywhere in the edge set graded
// PASS even when it didn't start/end at the symbols the task actually asked
// about (bench/tasks/truth/07_call_path.json carries "from"/"to" alongside
// "edges").
func TestContainsPath_ChecksEndpoints(t *testing.T) {
	truth := obj(t, `{"edges":[["A","B"],["B","C"],["A","D"],["D","E"]],"from":"A","to":"C"}`)

	if r := containsPath("path", obj(t, `{"path":["A","B","C"]}`), truth); !r.Pass {
		t.Fatalf("A->B->C connects the required endpoints and should pass: %+v", r)
	}
	// A valid truth edge, but it doesn't start at "from" — must fail even
	// though every consecutive pair is a real edge.
	if r := containsPath("path", obj(t, `{"path":["A","D"]}`), truth); r.Pass {
		t.Fatal("A->D is a real edge but doesn't reach \"to\":\"C\" — should fail")
	}
	if r := containsPath("path", obj(t, `{"path":["D","E"]}`), truth); r.Pass {
		t.Fatal("D->E is a real edge but doesn't start at \"from\":\"A\" — should fail")
	}
}

// TestContainsPath_NoEndpointsInTruthSkipsCheck keeps the pre-fix behavior
// for fixtures that don't carry "from"/"to" — the endpoint check is
// skippable, not required, so older/synthetic truth files without those
// keys still grade purely on edge membership.
func TestContainsPath_NoEndpointsInTruthSkipsCheck(t *testing.T) {
	truth := obj(t, `{"edges":[["A","B"],["B","C"]]}`)
	if r := containsPath("path", obj(t, `{"path":["A","B"]}`), truth); !r.Pass {
		t.Fatalf("truth without from/to should not enforce endpoints: %+v", r)
	}
}

// TestContainsPath_NonStringEntryFails pins that a malformed (non-string)
// path entry now fails the grade instead of being silently dropped, which
// previously could turn an invalid answer into a shorter — sometimes
// accidentally valid — path.
func TestContainsPath_NonStringEntryFails(t *testing.T) {
	truth := obj(t, `{"edges":[["A","B"],["B","C"]],"from":"A","to":"C"}`)
	if r := containsPath("path", obj(t, `{"path":["A",42,"C"]}`), truth); r.Pass {
		t.Fatalf("a non-string path entry must fail the grade, not be silently dropped: %+v", r)
	}
}

func TestGradeDispatch_UnknownGrader(t *testing.T) {
	if _, err := Grade("bogus", "k", nil, nil, 0); err == nil {
		t.Fatal("expected error for unknown grader")
	}
	for _, k := range []string{"set_equal", "exact", "numeric", "contains_path"} {
		if !KnownGrader(k) {
			t.Fatalf("%s should be a known grader", k)
		}
	}
	if KnownGrader("nope") {
		t.Fatal("nope should not be known")
	}
}
