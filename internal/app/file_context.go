package app

// FileContextReport is the one-call "orient me on this file" bundle: the file's
// symbol outline, its file-level impact (dependents, blast radius, covering
// tests, delete verdict, breaking-change), and the files structurally tied to
// it. It composes codemap_symbols + codemap_file_impact + codemap_related_files
// so orienting on a file is one call instead of three.
type FileContextReport struct {
	Project      string            `json:"project"`
	File         string            `json:"file"`
	Indexed      bool              `json:"indexed"`
	Found        bool              `json:"found"`
	CallGraph    string            `json:"call_graph"`
	Symbols      []SymbolRef       `json:"symbols"`       // the file's outline (what it defines)
	Impact       *FileImpactReport `json:"impact"`        // dependents + blast radius + covering tests + delete verdict + breaking_change
	RelatedFiles []RelatedFile     `json:"related_files"` // files structurally tied to this one (co-change candidates)
	Next         []NextAction      `json:"next,omitempty"`
}

// FileContext orients on a file in one call. FileImpact does the heavy lifting
// (and resolves the normalized project-relative path); the symbol outline and
// related files are added best-effort so a failure there never loses the impact
// analysis — the bundle degrades to the impact it could compute rather than
// erroring wholesale.
func (svc *Service) FileContext(cwd, file string, depth int) (*FileContextReport, error) {
	impact, err := svc.FileImpact(cwd, file, depth)
	if err != nil {
		return nil, err
	}
	rep := &FileContextReport{
		Project:      impact.Project,
		File:         impact.File,
		Indexed:      impact.Indexed,
		Found:        impact.Found,
		CallGraph:    impact.CallGraph,
		Symbols:      []SymbolRef{},
		Impact:       impact,
		RelatedFiles: []RelatedFile{},
		Next:         impact.Next,
	}
	if syms, serr := svc.Symbols(cwd, impact.File); serr == nil && syms != nil {
		rep.Symbols = syms.Symbols
	}
	if impact.Indexed {
		if rel, rerr := svc.RelatedFiles(cwd, impact.File); rerr == nil && rel != nil {
			rep.RelatedFiles = rel.Related
		}
	}
	return rep, nil
}
