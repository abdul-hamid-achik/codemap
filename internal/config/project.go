package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrNoProject is returned when no project root can be located.
var ErrNoProject = errors.New("not in a codemap project (no codemap.yaml or .codemap directory found); run 'codemap init'")

// projectMarkers mark a directory as a codemap project root, checked in order.
var projectMarkers = []string{
	"codemap.yaml",
	"codemap.yml",
	filepath.Join(".config", "codemap.yaml"),
	".codemap",
}

// FindProjectRoot walks up from start (or the current directory when start is
// empty) looking for a project marker. It returns ErrNoProject at the
// filesystem root if none is found.
func FindProjectRoot(start string) (string, error) {
	if start == "" {
		var err error
		start, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	dir := start
	for {
		for _, m := range projectMarkers {
			path := filepath.Join(dir, m)
			if _, err := os.Stat(path); err == nil {
				return dir, nil
			} else if !isOptionalPathAbsent(err) {
				return "", fmt.Errorf("inspect project marker %s: %w", path, err)
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", ErrNoProject
		}
		dir = parent
	}
}

// (IsNoProject was removed: callers (and the one test) check the
// sentinel with errors.Is(ErrNoProject) directly. P3-01.)

// DeriveProjectName returns a stable, human-readable project name for dir.
func DeriveProjectName(dir string) string {
	base := filepath.Base(filepath.Clean(dir))
	if base == "." || base == string(filepath.Separator) || base == "" {
		return "root"
	}
	return base
}
