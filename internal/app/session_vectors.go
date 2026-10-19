package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/vector"
	vlsession "github.com/abdul-hamid-achik/veclite/session"
)

// projectVectorState mirrors the legacy vector handle trio for one
// project's own store file. All fields are guarded by Session.vecMu.
type projectVectorState struct {
	session     *vlsession.Session
	vectors     *vector.Store
	vectorsRO   *vector.Store
	maintenance *vector.Store
}

func (st *projectVectorState) closeSession() error {
	if st.session == nil {
		return nil
	}
	err := st.session.Close()
	st.session = nil
	return err
}

func (st *projectVectorState) close() error {
	var err error
	if st.vectors != nil {
		err = errors.Join(err, st.vectors.Close())
		st.vectors = nil
	}
	if st.vectorsRO != nil {
		err = errors.Join(err, st.vectorsRO.Close())
		st.vectorsRO = nil
	}
	if st.maintenance != nil {
		err = errors.Join(err, st.maintenance.Close())
		st.maintenance = nil
	}
	return errors.Join(err, st.closeSession())
}

// validVectorScope accepts the stable keys RepoHash produces (hex) plus
// migration suffixes; anything path-like is rejected so a scope can never
// escape the vectors directory.
func validVectorScope(scope string) bool {
	if scope == "" || len(scope) > 128 || scope == "." || scope == ".." {
		return false
	}
	for _, r := range scope {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_'
		if !ok {
			return false
		}
	}
	return !strings.Contains(scope, "..")
}

// projectState returns (creating if needed) the handle state for scope and
// its lazily-built veclite session. Callers must hold vecMu.
func (s *Session) projectState(scope string) (*projectVectorState, error) {
	if !validVectorScope(scope) {
		return nil, fmt.Errorf("invalid vector scope %q", scope)
	}
	if s.projectVectors == nil {
		s.projectVectors = map[string]*projectVectorState{}
	}
	st := s.projectVectors[scope]
	if st == nil {
		st = &projectVectorState{}
		s.projectVectors[scope] = st
	}
	if st.session == nil {
		path := config.VecliteProjectPath(scope)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		st.session = vlsession.New(vlsession.Config{
			Path:       path,
			Dimensions: s.Embedder().Profile().Dimensions,
		})
	}
	return st, nil
}

// VectorsForProject opens the per-project vector store for scope,
// profile-validated like Vectors. Unlike the legacy store, this loads only
// the scoped project's records.
func (s *Session) VectorsForProject(scope string) (*vector.Store, error) {
	s.vecMu.Lock()
	defer s.vecMu.Unlock()
	st, err := s.projectState(scope)
	if err != nil {
		return nil, err
	}
	if st.vectors != nil {
		return st.vectors, nil
	}
	if st.vectorsRO != nil {
		_ = st.vectorsRO.Close()
		st.vectorsRO = nil
	}
	db, err := st.session.ReadWrite()
	if err != nil {
		return nil, err
	}
	v, err := vector.OpenFromDB(db, s.Embedder().Profile())
	if err != nil {
		return nil, errors.Join(err, st.closeSession())
	}
	st.vectors = v
	return st.vectors, nil
}

// VectorsReadOnlyForProject is VectorsForProject's shared-lock read handle.
func (s *Session) VectorsReadOnlyForProject(scope string) (*vector.Store, error) {
	s.vecMu.Lock()
	defer s.vecMu.Unlock()
	st, err := s.projectState(scope)
	if err != nil {
		return nil, err
	}
	if st.vectors != nil {
		return st.vectors, nil
	}
	if st.vectorsRO == nil {
		db, err := st.session.ReadOnly()
		if err != nil {
			return nil, err
		}
		v, err := vector.OpenFromDB(db, s.Embedder().Profile())
		if err != nil {
			return nil, err
		}
		st.vectorsRO = v
	}
	return st.vectorsRO, nil
}

// VectorsForProjectMaintenance opens the per-project store for cleanup
// only (profile guard bypassed, never creates the file). Returns nil when
// the project's store does not exist.
func (s *Session) VectorsForProjectMaintenance(scope string) (*vector.Store, error) {
	s.vecMu.Lock()
	defer s.vecMu.Unlock()
	if !validVectorScope(scope) {
		return nil, fmt.Errorf("invalid vector scope %q", scope)
	}
	if !fileExists(config.VecliteProjectPath(scope)) {
		return nil, nil
	}
	st, err := s.projectState(scope)
	if err != nil {
		return nil, err
	}
	if st.vectors != nil {
		return st.vectors, nil
	}
	if st.maintenance != nil {
		return st.maintenance, nil
	}
	if st.vectorsRO != nil {
		_ = st.vectorsRO.Close()
		st.vectorsRO = nil
	}
	db, err := st.session.ReadWrite()
	if err != nil {
		return nil, err
	}
	v, err := vector.OpenExistingFromDB(db)
	if errors.Is(err, vector.ErrCollectionNotFound) {
		return nil, st.closeSession()
	}
	if err != nil {
		return nil, errors.Join(err, st.closeSession())
	}
	st.maintenance = v
	return st.maintenance, nil
}

// ReleaseProjectVectors closes the scoped profile-validated writer.
func (s *Session) ReleaseProjectVectors(scope string) error {
	s.vecMu.Lock()
	defer s.vecMu.Unlock()
	st := s.projectVectors[scope]
	if st == nil || st.vectors == nil {
		return nil
	}
	var err error
	err = errors.Join(err, st.vectors.Close())
	st.vectors = nil
	if st.vectorsRO != nil {
		err = errors.Join(err, st.vectorsRO.Close())
		st.vectorsRO = nil
	}
	return errors.Join(err, st.closeSession())
}

// ReleaseProjectMaintenanceVectors closes the scoped maintenance handle.
func (s *Session) ReleaseProjectMaintenanceVectors(scope string) error {
	s.vecMu.Lock()
	defer s.vecMu.Unlock()
	st := s.projectVectors[scope]
	if st == nil || st.maintenance == nil {
		return nil
	}
	var err error
	err = errors.Join(err, st.maintenance.Close())
	st.maintenance = nil
	if st.vectors != nil {
		return err
	}
	if st.vectorsRO != nil {
		err = errors.Join(err, st.vectorsRO.Close())
		st.vectorsRO = nil
	}
	return errors.Join(err, st.closeSession())
}
