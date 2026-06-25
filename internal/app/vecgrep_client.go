package app

import (
	"context"
	"encoding/json"
	"os/exec"
	"strconv"

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
