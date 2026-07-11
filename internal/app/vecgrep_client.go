package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/config"
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
	if !cfg.Enabled {
		return nil
	}
	// Option-injection guard (P0-03): a query that starts with "-" is parsed as
	// a flag by argv-parsing tools even past a `--` separator. The query is
	// agent-influenced (sibling MCP `codemap_semantic` hands it through), so
	// refuse it rather than risk invoking vecgrep with attacker-controlled
	// flags. Vector stores are not security-critical so failing closed here is
	// the right tradeoff (caller degrades to local semantic search).
	if query == "" || query[0] == '-' {
		return nil
	}
	bin := cfg.Bin
	if bin == "" {
		bin = "vecgrep"
	}
	if _, err := exec.LookPath(bin); err != nil {
		return nil
	}
	cmd := exec.CommandContext(ctx, bin, "search", query, "--format", "json", "--limit", strconv.Itoa(topK))
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var hits []vecgrepHit
	if err := json.Unmarshal(out, &hits); err != nil {
		return nil
	}
	return hits
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
	cmd := exec.CommandContext(ctx, bin, "memory", "recall", query,
		"--tags", strings.Join(tags, ","), "--min-importance", "0.3",
		"--limit", strconv.Itoa(topK), "--format", "json")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("vecgrep memory recall: %w", err)
	}
	var notes []MemoryNote
	if err := json.Unmarshal(out, &notes); err != nil {
		return nil, fmt.Errorf("parse vecgrep memory recall output: %w", err)
	}
	return notes, nil
}

// semanticViaVecgrep answers a semantic query through vecgrep when codemap has no
// local embeddings, mapping each chunk hit back onto a graph node by its
// (relative_path, start_line) so the result carries codemap's FQN/kind/signature.
// Hits with no enclosing node are kept (vecgrep's raw symbol_name + position), so
// recall isn't lost. Returns nil when vecgrep can't help.
func (svc *Service) semanticViaVecgrep(ctx context.Context, cwd string, pid int64, query string, topK int) []SemanticHit {
	raw := vecgrepSearch(ctx, svc.s.Config.Vecgrep, cwd, query, topK)
	if len(raw) == 0 {
		return nil
	}
	g, err := svc.s.Graph()
	if err != nil {
		return nil
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
		}
		if hit.Symbol == "" {
			continue // a chunk with no enclosing symbol and no name — drop the noise
		}
		hits = append(hits, hit)
	}
	return hits
}
