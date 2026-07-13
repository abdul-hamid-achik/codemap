package drivers

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"

	"github.com/abdul-hamid-achik/codemap/bench/suite"
)

// SmokeDriver is an OFFLINE driver that never calls the API. It fabricates a
// plausible final answer from the frozen ground truth and synthetic per-arm
// metrics so the full orchestrate → grade → report pipeline can be exercised
// with `--smoke` at zero cost. It is NOT a benchmark: its numbers are invented
// (baseline arms are hard-coded to look heavier than codemap arms) purely to
// prove the plumbing. Never publish smoke numbers.
type SmokeDriver struct {
	byPrompt map[string]smokeTask
}

type smokeTask struct {
	task  suite.Task
	truth map[string]any
}

func (d SmokeDriver) Name() string { return "smoke" }

// NewSmokeDriver indexes tasks by prompt and preloads their truth so Run can
// echo a passing answer. truthPath resolves a task's Truth field to a file.
func NewSmokeDriver(tasks []suite.Task, truthPath func(t suite.Task) string) (SmokeDriver, error) {
	m := map[string]smokeTask{}
	for _, t := range tasks {
		truth, err := suite.LoadTruth(truthPath(t))
		if err != nil {
			return SmokeDriver{}, fmt.Errorf("smoke: load truth for %s: %w", t.ID, err)
		}
		m[t.Prompt] = smokeTask{task: t, truth: truth}
	}
	return SmokeDriver{byPrompt: m}, nil
}

func (d SmokeDriver) Run(_ context.Context, prompt string, arm Arm, _ string) (Metrics, error) {
	st, ok := d.byPrompt[prompt]
	if !ok {
		return Metrics{}, fmt.Errorf("smoke: no task matches prompt")
	}
	answer := d.answerFor(st)
	body, _ := json.Marshal(answer)
	final := fmt.Sprintf("Here is my analysis.\n\n```json\n%s\n```\n", string(body))

	// Synthetic metrics: baseline heavier than codemap, with deterministic
	// per-(task,arm,rep) jitter so aggregated σ is non-zero.
	j := jitter(st.task.ID + arm.Name)
	m := Metrics{FinalAnswer: final, OK: true}
	if arm.Name == "codemap" {
		m.ToolCalls = 3 + j%3
		m.InputTokens = 10000 + (j%2000)*3
		m.OutputTokens = 1500 + (j % 400)
		m.CacheReadTokens = 4000 + (j % 1000)
		m.WallClockMs = int64(11000 + (j % 3000))
		m.CostUSD = 0.04 + float64(j%20)/1000.0
	} else {
		m.ToolCalls = 12 + j%7
		m.InputTokens = 34000 + (j%9000)*2
		m.OutputTokens = 2500 + (j % 800)
		m.CacheReadTokens = 6000 + (j % 1500)
		m.WallClockMs = int64(38000 + (j % 12000))
		m.CostUSD = 0.11 + float64(j%50)/1000.0
	}
	return m, nil
}

// answerFor builds a passing answer from the truth per grader kind.
func (d SmokeDriver) answerFor(st smokeTask) map[string]any {
	t := st.task
	switch t.Grader {
	case "exact":
		return st.truth
	case "contains_path":
		out := map[string]any{}
		edges, _ := st.truth["edges"].([]any)
		from, hasFrom := st.truth["from"].(string)
		to, hasTo := st.truth["to"].(string)
		// contains_path now also checks the answer's endpoints against
		// truth["from"]/truth["to"] (when present) — walk the truth edges to
		// build a REAL path between them instead of echoing an arbitrary
		// edge, which no longer reliably satisfies the grader.
		if hasFrom && hasTo {
			if path := bfsPath(edges, from, to); path != nil {
				out[t.AnswerKey] = path
				return out
			}
		}
		// Fallback for a contains_path task whose truth carries no from/to
		// (or where from/to are unreachable over the recorded edges): echo
		// the first edge, matching what the grader checks in that case.
		if len(edges) > 0 {
			if first, ok := edges[0].([]any); ok && len(first) == 2 {
				out[t.AnswerKey] = []any{first[0], first[1]}
			}
		}
		return out
	default: // set_equal, numeric
		return map[string]any{t.AnswerKey: st.truth[t.AnswerKey]}
	}
}

// bfsPath finds a shortest directed path from "from" to "to" over edges
// (each element [a,b] as loaded from truth["edges"]), returning it as
// []any{from, ..., to} in the shape a JSON answer path uses, or nil if "to"
// is unreachable from "from".
func bfsPath(edges []any, from, to string) []any {
	adj := map[string][]string{}
	for _, e := range edges {
		pair, ok := e.([]any)
		if !ok || len(pair) != 2 {
			continue
		}
		a, _ := pair[0].(string)
		b, _ := pair[1].(string)
		if a == "" || b == "" {
			continue
		}
		adj[a] = append(adj[a], b)
	}
	type node struct {
		name string
		prev *node
	}
	if from == to {
		return []any{from}
	}
	visited := map[string]bool{from: true}
	queue := []*node{{name: from}}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, next := range adj[cur.name] {
			if visited[next] {
				continue
			}
			n := &node{name: next, prev: cur}
			if next == to {
				var rev []string
				for p := n; p != nil; p = p.prev {
					rev = append(rev, p.name)
				}
				out := make([]any, len(rev))
				for i, s := range rev {
					out[len(rev)-1-i] = s
				}
				return out
			}
			visited[next] = true
			queue = append(queue, n)
		}
	}
	return nil
}

func jitter(s string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return int(h.Sum32() % 100)
}
