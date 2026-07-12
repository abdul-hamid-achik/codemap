// Package grade implements deterministic, API-key-free grading of an agent's
// final answer against frozen ground truth. Every grader is pure Go and
// unit-tested (grade_test.go) without touching the network.
//
// The agent is instructed to end its answer with a single fenced ```json block.
// ExtractJSONBlock pulls the last such block out of the raw final text; the
// task-specific grader then compares it to the truth JSON.
package grade

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
)

// Result is the outcome of grading one answer.
type Result struct {
	Pass   bool
	Score  float64 // 1.0 pass; for set_equal, F1 on mismatch
	Detail string
}

// fencedJSON matches ```json ... ``` (case-insensitive language tag). We take
// the LAST match so trailing summary blocks win over illustrative ones earlier
// in the transcript.
var fencedJSON = regexp.MustCompile("(?is)```(?:json)?\\s*\\n(.*?)```")

// ExtractJSONBlock returns the last fenced json block in s that parses as a JSON
// object. It tolerates prose around the block, multiple fences, and a malformed
// trailing block (it falls back to the last block that actually parses).
func ExtractJSONBlock(s string) (map[string]any, error) {
	matches := fencedJSON.FindAllStringSubmatch(s, -1)
	if len(matches) == 0 {
		// Last resort: try to find a bare {...} object at the end of the text.
		if obj, ok := lastBareObject(s); ok {
			return obj, nil
		}
		return nil, fmt.Errorf("no fenced json block found")
	}
	// Walk from the last block backwards; return the last one that parses.
	var lastErr error
	for i := len(matches) - 1; i >= 0; i-- {
		body := strings.TrimSpace(matches[i][1])
		var m map[string]any
		if err := json.Unmarshal([]byte(body), &m); err != nil {
			lastErr = err
			continue
		}
		return m, nil
	}
	return nil, fmt.Errorf("no parseable json object in %d fenced block(s): %w", len(matches), lastErr)
}

// lastBareObject scans for the last top-level {...} and tries to parse it.
func lastBareObject(s string) (map[string]any, bool) {
	end := strings.LastIndex(s, "}")
	if end < 0 {
		return nil, false
	}
	depth := 0
	for i := end; i >= 0; i-- {
		switch s[i] {
		case '}':
			depth++
		case '{':
			depth--
			if depth == 0 {
				var m map[string]any
				if json.Unmarshal([]byte(s[i:end+1]), &m) == nil {
					return m, true
				}
				return nil, false
			}
		}
	}
	return nil, false
}

// Grade dispatches to the named grader. answer is the extracted agent JSON;
// truth is the frozen ground-truth JSON; key is the field to compare;
// tolerance is used by the numeric grader.
func Grade(kind, key string, answer, truth map[string]any, tolerance float64) (Result, error) {
	switch kind {
	case "set_equal":
		return setEqual(key, answer, truth), nil
	case "exact":
		return exact(answer, truth), nil
	case "numeric":
		return numeric(key, answer, truth, tolerance), nil
	case "contains_path":
		return containsPath(key, answer, truth), nil
	default:
		return Result{}, fmt.Errorf("unknown grader %q", kind)
	}
}

// KnownGrader reports whether kind is a recognised grader (used by the task
// integrity test).
func KnownGrader(kind string) bool {
	switch kind {
	case "set_equal", "exact", "numeric", "contains_path":
		return true
	}
	return false
}

// toStringSet normalises a JSON value (expected []any of strings) to a set of
// trimmed strings.
func toStringSet(v any) map[string]bool {
	set := map[string]bool{}
	arr, ok := v.([]any)
	if !ok {
		return set
	}
	for _, e := range arr {
		if s, ok := e.(string); ok {
			s = strings.TrimSpace(s)
			if s != "" {
				set[s] = true
			}
		}
	}
	return set
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func setEqual(key string, answer, truth map[string]any) Result {
	got := toStringSet(answer[key])
	want := toStringSet(truth[key])
	if len(want) == 0 {
		return Result{Pass: len(got) == 0, Score: boolScore(len(got) == 0), Detail: "empty truth set"}
	}
	var tp, fp int
	for g := range got {
		if want[g] {
			tp++
		} else {
			fp++
		}
	}
	fn := 0
	var missing []string
	for w := range want {
		if !got[w] {
			fn++
			missing = append(missing, w)
		}
	}
	var extra []string
	for g := range got {
		if !want[g] {
			extra = append(extra, g)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	pass := fp == 0 && fn == 0
	precision := ratio(tp, tp+fp)
	recall := ratio(tp, tp+fn)
	f1 := 0.0
	if precision+recall > 0 {
		f1 = 2 * precision * recall / (precision + recall)
	}
	detail := fmt.Sprintf("precision=%.2f recall=%.2f", precision, recall)
	if !pass {
		if len(missing) > 0 {
			detail += " missing=" + strings.Join(missing, ",")
		}
		if len(extra) > 0 {
			detail += " extra=" + strings.Join(extra, ",")
		}
	}
	score := 1.0
	if !pass {
		score = f1
	}
	return Result{Pass: pass, Score: score, Detail: detail}
}

func exact(answer, truth map[string]any) Result {
	// Every key in truth must be present and deep-equal in answer.
	for k, want := range truth {
		got, ok := answer[k]
		if !ok {
			return Result{Pass: false, Detail: fmt.Sprintf("missing key %q", k)}
		}
		if !jsonEqual(got, want) {
			return Result{Pass: false, Detail: fmt.Sprintf("%s: got %v want %v", k, got, want)}
		}
	}
	return Result{Pass: true, Score: 1.0, Detail: "all keys match"}
}

func numeric(key string, answer, truth map[string]any, tol float64) Result {
	got, gok := toFloat(answer[key])
	want, wok := toFloat(truth[key])
	if !gok || !wok {
		return Result{Pass: false, Detail: fmt.Sprintf("non-numeric value for %q", key)}
	}
	diff := math.Abs(got - want)
	pass := diff <= tol
	return Result{Pass: pass, Score: boolScore(pass), Detail: fmt.Sprintf("got=%.0f want=%.0f tol=%.0f", got, want, tol)}
}

// containsPath verifies the answer's ordered path is a valid walk over the
// ground-truth edge set: every consecutive (a, b) pair must be a truth edge.
// truth["edges"] is [[from,to], ...]; answer[key] is [n0, n1, ...].
func containsPath(key string, answer, truth map[string]any) Result {
	pathAny, ok := answer[key].([]any)
	if !ok || len(pathAny) < 2 {
		return Result{Pass: false, Detail: "answer path missing or too short"}
	}
	var path []string
	for _, e := range pathAny {
		if s, ok := e.(string); ok {
			path = append(path, strings.TrimSpace(s))
		}
	}
	edges := map[string]bool{}
	edgesAny, _ := truth["edges"].([]any)
	for _, e := range edgesAny {
		pair, ok := e.([]any)
		if !ok || len(pair) != 2 {
			continue
		}
		a, _ := pair[0].(string)
		b, _ := pair[1].(string)
		edges[strings.TrimSpace(a)+"->"+strings.TrimSpace(b)] = true
	}
	for i := 0; i+1 < len(path); i++ {
		if !edges[path[i]+"->"+path[i+1]] {
			return Result{Pass: false, Detail: fmt.Sprintf("edge %s->%s not in ground truth", path[i], path[i+1])}
		}
	}
	return Result{Pass: true, Score: 1.0, Detail: fmt.Sprintf("valid %d-hop path", len(path)-1)}
}

func ratio(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
}

func boolScore(b bool) float64 {
	if b {
		return 1.0
	}
	return 0.0
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

// jsonEqual compares two decoded JSON values, tolerating int/float and
// numeric-string mismatches for scalars.
func jsonEqual(a, b any) bool {
	if af, aok := toFloat(a); aok {
		if bf, bok := toFloat(b); bok {
			return af == bf
		}
	}
	as := fmt.Sprintf("%v", a)
	bs := fmt.Sprintf("%v", b)
	return as == bs
}
