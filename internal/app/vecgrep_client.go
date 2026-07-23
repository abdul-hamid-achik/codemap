package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/abdul-hamid-achik/codemap/internal/config"
)

const (
	// The CLI adapter is an optional one-hop boundary, so it must not be able to
	// hold a long-lived MCP request open forever. A caller's earlier deadline
	// still wins through context derivation.
	vecgrepCommandTimeout = 10 * time.Second
	// vecgrep search/recall responses are small, bounded result sets. Four MiB
	// leaves ample contract headroom while preventing an unexpected CLI from
	// making codemap buffer arbitrary stdout.
	vecgrepMaxOutputBytes = 4 << 20
	// Bound Wait when a terminated process leaves a descendant holding its
	// stdout pipe open.
	vecgrepCommandWaitDelay = 250 * time.Millisecond
)

// vecgrepHit is the subset of vecgrep's `search --format json` result (the C4
// contract) that codemap consumes — keyed for the (relative_path, start_line)
// join back onto the graph.
type vecgrepHit struct {
	RelativePath string  `json:"relative_path"`
	SymbolName   string  `json:"symbol_name"`
	StartLine    int     `json:"start_line"`
	EndLine      int     `json:"end_line"`
	Language     string  `json:"language"`
	Score        float32 `json:"score"`
}

// vecgrepSearch shells the sibling vecgrep tool for semantic search, parsing its
// `search <query> --format json` array (run from cwd, so vecgrep resolves the same
// project). It returns nil — never an error — when vecgrep is disabled, off $PATH,
// or yields nothing usable, so the caller degrades to its own behavior. CLI-only,
// one hop (no vecgrep import); mirrors how vecgrep shells codemap.
func vecgrepSearch(ctx context.Context, cfg config.VecgrepConfig, cwd, query string, topK int) []vecgrepHit {
	hits, _, err := vecgrepSearchCommand(ctx, cfg, cwd, query, topK)
	if err != nil {
		return nil
	}
	return hits
}

// vecgrepSearchCommand is the observable form used when the user explicitly
// selects semantic.backend=vecgrep. available distinguishes an optional missing
// adapter from a real zero-hit search; execution/contract errors are preserved
// instead of silently switching semantic owners.
func vecgrepSearchCommand(ctx context.Context, cfg config.VecgrepConfig, cwd, query string, topK int) ([]vecgrepHit, bool, error) {
	if !cfg.Enabled {
		return nil, false, nil
	}
	// Option-injection guard (P0-03): a query that starts with "-" is parsed as
	// a flag by argv-parsing tools even past a `--` separator. The query is
	// agent-influenced (sibling MCP `codemap_semantic` hands it through), so
	// refuse it rather than risk invoking vecgrep with attacker-controlled
	// flags. Vector stores are not security-critical so failing closed here is
	// the right tradeoff (caller degrades to local semantic search).
	if query == "" || query[0] == '-' {
		return nil, true, fmt.Errorf("vecgrep semantic query must be non-empty and must not start with '-'")
	}
	bin := cfg.Bin
	if bin == "" {
		bin = "vecgrep"
	}
	if _, err := exec.LookPath(bin); err != nil {
		return nil, false, nil
	}
	out, err := runVecgrepJSON(ctx, bin, cwd,
		"search", query, "--format", "json", "--limit", strconv.Itoa(topK))
	if err != nil {
		return nil, true, fmt.Errorf("vecgrep semantic search: %w", err)
	}
	var hits []vecgrepHit
	if err := json.Unmarshal(out, &hits); err != nil {
		return nil, true, fmt.Errorf("parse vecgrep semantic output: %w", err)
	}
	return hits, true, nil
}

// vecgrepMemoryRecall shells vecgrep's global agent-memory store for memories
// matching query AND carrying every tag (the scope tags ['codemap', <project_key>]
// keep recall to this project — no cross-project leakage, per the G2 governance).
// Disabled, absent, or invalid-query states are optional-capability misses and
// return (nil,nil). Once a vecgrep process is actually launched, execution and
// JSON failures are returned so Context can expose them in partial_errors.
func vecgrepMemoryRecall(ctx context.Context, cfg config.VecgrepConfig, cwd, query string, tags []string, topK int) ([]MemoryNote, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	// Option-injection guard (P0-03): reject empty or leading-dash queries before
	// they reach vecgrep. Tag values are already constrained by the caller.
	if query == "" || query[0] == '-' {
		return nil, nil
	}
	bin := cfg.Bin
	if bin == "" {
		bin = "vecgrep"
	}
	if _, err := exec.LookPath(bin); err != nil {
		return nil, nil
	}
	out, err := runVecgrepJSON(ctx, bin, cwd, "memory", "recall", query,
		"--tags", strings.Join(tags, ","), "--min-importance", "0.3",
		"--limit", strconv.Itoa(topK), "--format", "json")
	if err != nil {
		return nil, fmt.Errorf("vecgrep memory recall: %w", err)
	}
	var notes []MemoryNote
	if err := json.Unmarshal(out, &notes); err != nil {
		return nil, fmt.Errorf("parse vecgrep memory recall output: %w", err)
	}
	return notes, nil
}

// runVecgrepJSON executes the CLI boundary with production limits. Keeping the
// subprocess policy in one place ensures semantic search and memory recall have
// the same cancellation and output-bounding behavior.
func runVecgrepJSON(ctx context.Context, bin, cwd string, args ...string) ([]byte, error) {
	return runVecgrepJSONWithLimits(ctx, bin, cwd, vecgrepCommandTimeout, vecgrepMaxOutputBytes, args...)
}

// runVecgrepJSONWithLimits is split out so the process-boundary behavior can be
// tested quickly with hermetic helper executables rather than sleeping for the
// production timeout.
func runVecgrepJSONWithLimits(ctx context.Context, bin, cwd string, timeout time.Duration, maxOutput int, args ...string) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	stdout := &cappedCommandOutput{limit: maxOutput, cancel: cancel}
	cmd := exec.CommandContext(runCtx, bin, args...)
	cmd.Dir = cwd
	cmd.Stdout = stdout
	cmd.WaitDelay = vecgrepCommandWaitDelay
	err := cmd.Run()
	if stdout.exceeded() {
		return nil, fmt.Errorf("stdout exceeds %d bytes", maxOutput)
	}
	if err != nil {
		// CommandContext often reports "signal: killed" after cancellation.
		// Preserve the causal context error so callers can use errors.Is.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if runErr := runCtx.Err(); runErr != nil {
			return nil, runErr
		}
		return nil, err
	}
	return stdout.bytes(), nil
}

// cappedCommandOutput retains only the first limit bytes and cancels the child
// as soon as it observes more. Write reports the full input length because the
// excess is intentionally discarded while CommandContext tears the process
// down; returning a short write could leave a producer blocked on its pipe.
type cappedCommandOutput struct {
	mu       sync.Mutex
	data     []byte
	limit    int
	overflow bool
	cancel   context.CancelFunc
}

func (w *cappedCommandOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	remaining := w.limit - len(w.data)
	if remaining > len(p) {
		remaining = len(p)
	}
	if remaining > 0 {
		w.data = append(w.data, p[:remaining]...)
	}
	overflowed := len(p) > remaining
	if overflowed {
		w.overflow = true
	}
	w.mu.Unlock()
	if overflowed {
		w.cancel()
	}
	return len(p), nil
}

func (w *cappedCommandOutput) exceeded() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.overflow
}

func (w *cappedCommandOutput) bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]byte(nil), w.data...)
}

// semanticViaVecgrep answers a semantic query through vecgrep when codemap has no
// local embeddings, mapping each chunk hit back onto a graph node by its
// (relative_path, start_line) so the result carries codemap's FQN/kind/signature.
// Hits with no enclosing node are kept (vecgrep's raw symbol_name + position), so
// recall isn't lost. Returns nil when vecgrep can't help.
func (svc *Service) semanticViaVecgrep(ctx context.Context, cwd string, pid int64, query string, topK int) []SemanticHit {
	hits, _ := svc.semanticViaVecgrepStrict(ctx, cwd, pid, query, topK)
	return hits
}

func (svc *Service) semanticViaVecgrepStrict(ctx context.Context, cwd string, pid int64, query string, topK int) ([]SemanticHit, error) {
	raw, available, err := vecgrepSearchCommand(ctx, svc.s.Config.Vecgrep, cwd, query, topK)
	if err != nil {
		return nil, err
	}
	if !available {
		return nil, fmt.Errorf("vecgrep semantic backend is unavailable; install vecgrep or set semantic.backend to fallback or local")
	}
	if len(raw) == 0 {
		return []SemanticHit{}, nil
	}
	g, err := svc.s.Graph()
	if err != nil {
		return nil, err
	}
	hits := make([]SemanticHit, 0, len(raw))
	for _, h := range raw {
		hit := SemanticHit{
			Symbol: h.SymbolName, File: h.RelativePath,
			StartLine: h.StartLine, EndLine: h.EndLine, Score: h.Score,
		}
		if n, ok, nerr := g.NodeAtLine(pid, h.RelativePath, h.StartLine); nerr == nil && ok {
			hit.Symbol, hit.FQN, hit.Kind = n.Symbol, n.FQN, n.Kind
			hit.StartLine, hit.EndLine = n.StartLine, n.EndLine
			hit.Signature, hit.Doc = n.Signature, n.Docstring
			hit.Selector = selectorForNode(n)
		}
		if hit.Symbol == "" {
			continue // a chunk with no enclosing symbol and no name — drop the noise
		}
		hits = append(hits, hit)
	}
	enrichHitAnnotations(g, pid, hits)
	return hits, nil
}
