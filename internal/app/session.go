// Package app is codemap's shared service layer. The CLI, MCP server, and TUI
// all go through it; none of them contain business logic. A Session lazily
// opens the graph and vector stores (so a process that never queries — e.g. an
// idle MCP server — never takes the DB lock), and Service implements the
// operations on top of it.
package app

import (
	"errors"
	"os"

	"github.com/abdul-hamid-achik/codemap/internal/config"
	"github.com/abdul-hamid-achik/codemap/internal/embed"
	"github.com/abdul-hamid-achik/codemap/internal/graph"
	"github.com/abdul-hamid-achik/codemap/internal/vector"
	vlsession "github.com/abdul-hamid-achik/veclite/session"
)

// Session holds resolved configuration and lazily-opened stores.
type Session struct {
	Config *config.Config

	graph     *graph.Store
	vectors   *vector.Store
	vectorsRO *vector.Store
	embedder  embed.Provider // optional override (tests)

	// vecSession manages the lazy dual-handle DB access (RO with shared
	// flock, RW with exclusive flock). Created lazily on first vector open.
	vecSession *vlsession.Session
}

// Open loads configuration (honoring the --config path / CODEMAP_CONFIG) and
// ensures the XDG data directories exist. Stores are NOT opened here; they open
// on first use.
func Open(configPath string) (*Session, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}
	if err := config.EnsureDirs(); err != nil {
		return nil, err
	}
	return &Session{Config: cfg}, nil
}

// Graph opens (once) and returns the graph store. A failure to open the graph
// DB is wrapped in a CodedError so the CLI/MCP can map it to a stable machine
// code: an existing-but-broken DB is index_corrupt (back up + reindex); a
// missing DB file (first run / wiped data dir) is index_missing.
func (s *Session) Graph() (*graph.Store, error) {
	if s.graph == nil {
		dbPath := config.DBPath()
		existed := fileExists(dbPath)
		g, err := graph.Open(dbPath)
		if err != nil {
			if existed {
				return nil, coded("index_corrupt",
					"the graph DB exists but won't open — back it up, then run: codemap index --reindex", err)
			}
			return nil, coded("index_missing", "run: codemap index", err)
		}
		s.graph = g
	}
	return s.graph, nil
}

// Embedder returns the embedding provider: an override set via SetEmbedder if
// present, else the configured Ollama provider.
func (s *Session) Embedder() embed.Provider {
	if s.embedder != nil {
		return s.embedder
	}
	e := s.Config.Embedding
	return embed.NewOllama(e.OllamaURL, e.Model, e.Dimensions, e.Distance)
}

// SetEmbedder overrides the embedding provider (used by tests and alternate
// providers).
func (s *Session) SetEmbedder(p embed.Provider) { s.embedder = p }

// vecSess returns the veclite session, creating it lazily.
func (s *Session) vecSess() *vlsession.Session {
	if s.vecSession == nil {
		s.vecSession = vlsession.New(vlsession.Config{
			Path:       config.VeclitePath(),
			Dimensions: s.Embedder().Profile().Dimensions,
		})
	}
	return s.vecSession
}

// Vectors opens (once) and returns the vector store, guarded by the configured
// embedding profile. Uses the veclite/session package for exclusive-lock
// management (closes any cached RO handle first).
func (s *Session) Vectors() (*vector.Store, error) {
	if s.vectors != nil {
		return s.vectors, nil
	}
	// ReadWrite closes vecSession's cached RO database before acquiring the
	// exclusive handle. Drop the Store wrapper too: it otherwise retains a
	// pointer to the closed database and may be returned by VectorsReadOnly.
	if s.vectorsRO != nil {
		_ = s.vectorsRO.Close()
		s.vectorsRO = nil
	}
	db, err := s.vecSess().ReadWrite()
	if err != nil {
		return nil, err
	}
	v, err := vector.OpenFromDB(db, s.Embedder().Profile())
	if err != nil {
		return nil, errors.Join(err, s.closeVectorSession())
	}
	s.vectors = v
	return s.vectors, nil
}

// ReleaseVectors closes the profile-validated writer acquired by Vectors and
// releases its exclusive flock. Long-lived processes must call this after each
// index operation so subsequent reads reopen a fresh shared handle and unrelated
// processes are not excluded after the write has completed.
func (s *Session) ReleaseVectors() error {
	if s.vectors == nil {
		return nil
	}
	var err error
	err = errors.Join(err, s.vectors.Close())
	s.vectors = nil
	if s.vectorsRO != nil {
		err = errors.Join(err, s.vectorsRO.Close())
		s.vectorsRO = nil
	}
	if s.vecSession != nil {
		err = errors.Join(err, s.closeVectorSession())
	}
	return err
}

// closeVectorSession releases all veclite DB handles and resets the manager so
// the next vector operation reopens lazily.
func (s *Session) closeVectorSession() error {
	if s.vecSession == nil {
		return nil
	}
	err := s.vecSession.Close()
	s.vecSession = nil
	return err
}

// VectorsReadOnly opens (once) the vector store in read-only mode with a shared
// flock. Use for search-only paths so they don't block a concurrent index.
// If the RW store is already open in this process, it is returned directly
// (flock is per-file-description, so a second open in the same process would
// conflict with the existing RW handle).
func (s *Session) VectorsReadOnly() (*vector.Store, error) {
	if s.vectors != nil {
		return s.vectors, nil
	}
	if s.vectorsRO == nil {
		db, err := s.vecSess().ReadOnly()
		if err != nil {
			return nil, err
		}
		v, err := vector.OpenFromDB(db, s.Embedder().Profile())
		if err != nil {
			return nil, err
		}
		s.vectorsRO = v
	}
	return s.vectorsRO, nil
}

// Close closes any stores that were opened.
func (s *Session) Close() error {
	var err error
	if s.vectors != nil {
		err = errors.Join(err, s.vectors.Close())
		s.vectors = nil
	}
	if s.vectorsRO != nil {
		err = errors.Join(err, s.vectorsRO.Close())
		s.vectorsRO = nil
	}
	if s.vecSession != nil {
		err = errors.Join(err, s.vecSession.Close())
		s.vecSession = nil
	}
	if s.graph != nil {
		err = errors.Join(err, s.graph.Close())
		s.graph = nil
	}
	return err
}

// fileExists reports whether path names an existing file (follows symlinks).
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
