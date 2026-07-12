package app

import "fmt"

// FilePosition is one file:line input to a batch symbol_at resolution — the
// MCP/CLI-facing counterpart of Service.SymbolAt's two positional args.
type FilePosition struct {
	File string `json:"file" jsonschema:"project-relative file path"`
	Line int    `json:"line" jsonschema:"1-based line number to resolve to its enclosing symbol"`
}

const symbolAtBatchMax = 25

// SymbolAtBatchReport resolves several file:line positions to their
// enclosing symbols in one call — a pasted multi-frame stack trace or a
// diff's changed-line list resolves in one round-trip instead of one per
// frame. Each Results[i] self-reports its own miss via Resolution:"none";
// there is no separate NotFound list.
type SymbolAtBatchReport struct {
	Project   string            `json:"project"`
	Indexed   bool              `json:"indexed"`
	Requested int               `json:"requested"`
	Results   []*SymbolAtReport `json:"results"`
	Note      string            `json:"note,omitempty"`
}

// SymbolAtBatch resolves each position with the existing single-position
// Service.SymbolAt — thin fan-out, no new graph queries beyond what N
// individual calls would already do.
func (svc *Service) SymbolAtBatch(cwd string, positions []FilePosition) (*SymbolAtBatchReport, error) {
	_, name, found, err := svc.project(cwd)
	if err != nil {
		return nil, err
	}
	rep := &SymbolAtBatchReport{Project: name, Indexed: found, Requested: len(positions), Results: []*SymbolAtReport{}}
	if !found {
		return rep, nil
	}
	if len(positions) > symbolAtBatchMax {
		rep.Note = fmt.Sprintf("requested %d positions — resolved the first %d", len(positions), symbolAtBatchMax)
		positions = positions[:symbolAtBatchMax]
	}
	for _, pos := range positions {
		r, err := svc.SymbolAt(cwd, pos.File, pos.Line)
		if err != nil {
			return nil, err
		}
		rep.Results = append(rep.Results, r)
	}
	return rep, nil
}
