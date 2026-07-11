// Package vector wraps veclite for codemap's semantic layer: a single
// collection of code-node embeddings whose payload links back to graph node
// IDs, plus a stored embedding profile so a provider/model/dimension change is
// detected and forces a rebuild instead of corrupting the space.
package vector

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/abdul-hamid-achik/codemap/internal/embed"
	"github.com/abdul-hamid-achik/veclite"
)

// CollectionName is codemap's single veclite collection.
const CollectionName = "codemap"

const profileKey = "codemap.embedding_profile"

// ErrCollectionNotFound is returned by OpenExistingFromDB when the veclite
// database exists but has no codemap collection to maintain.
var ErrCollectionNotFound = errors.New("codemap vector collection not found")

// Payload keys stored alongside each embedding.
const (
	keyNodeID    = "node_id"
	keyProject   = "project"
	keyFile      = "file"
	keySymbol    = "symbol"
	keyFQN       = "fqn"
	keyKind      = "kind"
	keyLanguage  = "language"
	keyStartLine = "start_line"
	keyEndLine   = "end_line"
)

// NodeMeta is the metadata stored with each embedding, linking it to the graph.
type NodeMeta struct {
	NodeID    int64
	Project   string
	File      string
	Symbol    string
	FQN       string
	Kind      string
	Language  string
	StartLine int
	EndLine   int
}

// Hit is one semantic search result.
type Hit struct {
	NodeID  int64
	Score   float32
	Content string
	Meta    NodeMeta
}

// Store is a handle to the codemap vector collection.
type Store struct {
	db   *veclite.DB
	coll *veclite.Collection

	// ownsDB is true when this Store opened the DB itself (via Open) and
	// should close it on Close(). When created via OpenFromDB, the caller
	// owns the DB and Close() is a no-op for the DB.
	ownsDB bool
}

// OpenFromDB creates a Store from an already-opened veclite DB handle. This
// is used by the session layer (which manages the DB handle via the
// veclite/session package) so that collection creation is separated from
// lock management. The caller retains ownership of the DB handle and must
// close it after closing the Store.
func OpenFromDB(db *veclite.DB, profile embed.EmbeddingProfile) (*Store, error) {
	s := &Store{db: db, ownsDB: false}
	if err := s.ensureCollection(profile); err != nil {
		return nil, err
	}
	return s, nil
}

// OpenExistingFromDB opens the existing codemap collection without creating it
// or validating an embedding profile. It is intentionally maintenance-only:
// structure-only indexing needs to delete vectors for changed files even when
// embeddings are disabled or the configured model has changed. Search and
// insertion paths must continue to use OpenFromDB so incompatible spaces are
// rejected.
func OpenExistingFromDB(db *veclite.DB) (*Store, error) {
	if !db.HasCollection(CollectionName) {
		return nil, ErrCollectionNotFound
	}
	coll, err := db.GetCollection(CollectionName)
	if err != nil {
		return nil, err
	}
	return &Store{db: db, coll: coll, ownsDB: false}, nil
}

// DB returns the underlying veclite.DB handle (for Reload calls).
func (s *Store) DB() *veclite.DB { return s.db }

// Open opens (creating if needed) the vector store at path and ensures the
// codemap collection exists with an embedding space matching profile. If the
// collection already exists with an incompatible profile, it returns an
// *embed.IncompatibleError. Use ":memory:" for a non-persistent store.
func Open(path string, profile embed.EmbeddingProfile) (*Store, error) {
	return open(path, profile, false)
}

// (OpenReadOnly was removed: VectorsReadOnly in the session layer is the
// only read-only entry point now, via OpenFromDB. Pre-fix the package
// exposed two paths and callers always picked VectorsReadOnly.)

func open(path string, profile embed.EmbeddingProfile, readOnly bool) (*Store, error) {
	opts := []veclite.Option{}
	if readOnly {
		opts = append(opts, veclite.WithReadOnly(true), veclite.WithSharedRead(true))
	}
	db, err := veclite.Open(path, opts...)
	if err != nil {
		return nil, err
	}
	s := &Store{db: db, ownsDB: true}
	if err := s.ensureCollection(profile); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) ensureCollection(want embed.EmbeddingProfile) error {
	if s.db.HasCollection(CollectionName) {
		// Collection exists — get it (works for both RW and RO).
		coll, err := s.db.GetCollection(CollectionName)
		if err != nil {
			return err
		}
		s.coll = coll
		if have, ok, err := s.storedProfile(); err != nil {
			return err
		} else if ok {
			if err := embed.CheckCompatible(have, want); err != nil {
				return err
			}
		} else {
			// O57: no stored profile means the guard is effectively disabled.
			// Cross-check the live collection dimension and, when writable,
			// self-heal by stamping the current profile so future opens are guarded.
			if want.Dimensions > 0 && coll.Dimension() != want.Dimensions {
				return fmt.Errorf("vector collection dimension %d does not match requested dimensions %d", coll.Dimension(), want.Dimensions)
			}
			// Self-heal by stamping the current profile so future opens are guarded.
			// On a read-only DB, setStoredProfile will return veclite.ErrReadOnly;
			// that is expected and not fatal for the guard check.
			if err := s.setStoredProfile(want); err != nil && !errors.Is(err, veclite.ErrReadOnly) {
				return err
			}
		}
		return nil
	}

	coll, err := s.db.CreateCollection(CollectionName,
		veclite.WithDimension(want.Dimensions),
		distanceOption(want.Distance),
		// Enabling text indexing (non-empty fields) also indexes Record.Content
		// for BM25/HybridSearch. We index symbol + fqn so keyword search matches
		// names too.
		veclite.WithTextIndex(keySymbol, keyFQN),
	)
	if err != nil {
		return err
	}
	s.coll = coll
	return s.setStoredProfile(want)
}

func distanceOption(name string) veclite.CollectionOption {
	switch name {
	case "dot":
		return veclite.WithDistanceType(veclite.DistanceDot)
	case "euclidean":
		return veclite.WithDistanceType(veclite.DistanceEuclidean)
	default:
		return veclite.WithDistanceType(veclite.DistanceCosine)
	}
}

func (s *Store) storedProfile() (embed.EmbeddingProfile, bool, error) {
	raw, ok := s.db.Metadata()[profileKey]
	if !ok {
		return embed.EmbeddingProfile{}, false, nil
	}
	str, ok := raw.(string)
	if !ok {
		return embed.EmbeddingProfile{}, false, nil
	}
	var p embed.EmbeddingProfile
	if err := json.Unmarshal([]byte(str), &p); err != nil {
		return embed.EmbeddingProfile{}, false, err
	}
	return p, true, nil
}

func (s *Store) setStoredProfile(p embed.EmbeddingProfile) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return s.db.SetMetadataValue(profileKey, string(b))
}

// Insert stores an embedding with its content and graph metadata.
func (s *Store) Insert(vector []float32, content string, meta NodeMeta) (uint64, error) {
	return s.coll.InsertDocument(vector, content, meta.toPayload())
}

// DeleteByFile removes all embeddings for a file in a project (used on
// incremental reindex). Returns the number of records deleted.
func (s *Store) DeleteByFile(project, file string) (int, error) {
	return s.coll.DeleteWhere(veclite.And(veclite.Equal(keyProject, project), veclite.Equal(keyFile, file)))
}

// DeleteByProject removes all embeddings for a project (used by a full
// reindex). Returns the number of records deleted.
func (s *Store) DeleteByProject(project string) (int, error) {
	return s.coll.DeleteWhere(veclite.Equal(keyProject, project))
}

// VecRecord is one stored embedding with everything needed to re-insert it
// elsewhere without re-embedding: the raw vector, its searchable content, and the
// graph metadata (incl. the node id it belongs to).
type VecRecord struct {
	Vector  []float32
	Content string
	Meta    NodeMeta
}

// IterByProject returns every embedding stored for a project — its raw vector,
// content, and metadata — so a snapshot can carry the vectors and restore them
// without a second embedding pass. Order is unspecified.
func (s *Store) IterByProject(project string) ([]VecRecord, error) {
	recs, err := s.coll.Find(veclite.Equal(keyProject, project))
	if err != nil {
		return nil, err
	}
	out := make([]VecRecord, 0, len(recs))
	for _, r := range recs {
		out = append(out, VecRecord{Vector: r.Vector, Content: r.Content, Meta: metaFromPayload(r.Payload)})
	}
	return out, nil
}

// CountByProject returns how many embeddings a project has (0 = no semantic
// index, so semantic search won't return results until `index` runs with an
// embedding provider).
func (s *Store) CountByProject(project string) (int, error) {
	recs, err := s.coll.Find(veclite.Equal(keyProject, project))
	if err != nil {
		return 0, err
	}
	return len(recs), nil
}

// Search returns the topK nearest embeddings to query. If project is non-empty
// results are restricted to that project.
func (s *Store) Search(query []float32, topK int, project string) ([]Hit, error) {
	res, err := s.coll.Search(query, searchOpts(topK, project)...)
	if err != nil {
		return nil, err
	}
	return toHits(res), nil
}

// HybridSearch fuses vector similarity (query) with BM25 keyword search (text).
func (s *Store) HybridSearch(query []float32, text string, topK int, project string) ([]Hit, error) {
	res, err := s.coll.HybridSearch(query, text, searchOpts(topK, project)...)
	if err != nil {
		return nil, err
	}
	return toHits(res), nil
}

func searchOpts(topK int, project string) []veclite.SearchOption {
	opts := []veclite.SearchOption{veclite.TopK(topK), veclite.WithContent(true)}
	if project != "" {
		opts = append(opts, veclite.WithFilter(veclite.Equal(keyProject, project)))
	}
	return opts
}

// Count returns the number of stored embeddings.
func (s *Store) Count() int { return s.coll.Count() }

// Sync flushes pending writes to disk.
func (s *Store) Sync() error { return s.db.Sync() }

// Close syncs and closes the store. If the Store was created via OpenFromDB,
// the DB handle is NOT closed (the caller/session owns it).
func (s *Store) Close() error {
	if s.ownsDB {
		return s.db.Close()
	}
	return nil
}

func toHits(res []veclite.Result) []Hit {
	hits := make([]Hit, 0, len(res))
	for _, r := range res {
		meta := metaFromPayload(r.Record.Payload)
		hits = append(hits, Hit{NodeID: meta.NodeID, Score: r.Score, Content: r.Record.Content, Meta: meta})
	}
	return hits
}

func (m NodeMeta) toPayload() map[string]any {
	return map[string]any{
		keyNodeID:    m.NodeID,
		keyProject:   m.Project,
		keyFile:      m.File,
		keySymbol:    m.Symbol,
		keyFQN:       m.FQN,
		keyKind:      m.Kind,
		keyLanguage:  m.Language,
		keyStartLine: m.StartLine,
		keyEndLine:   m.EndLine,
	}
}

func metaFromPayload(p map[string]any) NodeMeta {
	return NodeMeta{
		NodeID:    toInt64(p[keyNodeID]),
		Project:   toStr(p[keyProject]),
		File:      toStr(p[keyFile]),
		Symbol:    toStr(p[keySymbol]),
		FQN:       toStr(p[keyFQN]),
		Kind:      toStr(p[keyKind]),
		Language:  toStr(p[keyLanguage]),
		StartLine: int(toInt64(p[keyStartLine])),
		EndLine:   int(toInt64(p[keyEndLine])),
	}
}

func toStr(v any) string { s, _ := v.(string); return s }

func toInt64(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case uint64:
		return int64(n)
	case float64:
		return int64(n)
	case float32:
		return int64(n)
	}
	return 0
}
