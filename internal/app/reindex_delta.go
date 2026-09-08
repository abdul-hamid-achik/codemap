package app

import (
	"encoding/hex"
	"sort"

	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

// reindex delta capture: after every successful index run, codemap records the
// file-level drift of that run anchored by the structural fingerprints on
// either side of it. A peer that certified the previous fingerprint (vecgrep's
// ingestion receipt) may then re-ingest exactly the delta files through the
// filtered export instead of re-reading the whole stream — records for every
// other file are identical between the two exports, because identical node
// metadata plus identical content hash means an identical record.

// structuralFingerprintState is the source-free fingerprint of the index at one
// point in time, with the file hashes from the same SQLite snapshot.
type structuralFingerprintState struct {
	Fingerprint string
	FileHashes  map[string]string
	Records     int
}

// captureStructuralFingerprintState computes the fingerprint exactly the way
// the structural manifest does (same walker, same seed, same order), so the
// anchors a peer certified from a manifest match the anchors recorded here.
func captureStructuralFingerprintState(g *graph.Store, pid int64, projectKey string) (*structuralFingerprintState, error) {
	h := newStructuralIndexFingerprint(projectKey)
	st := &structuralFingerprintState{FileHashes: map[string]string{}}
	snapshot, err := g.WalkProjectStructuralIndexSnapshot(pid, func(n graph.Node) error {
		writeStructuralFingerprintNode(h, n)
		st.Records++
		return nil
	})
	if err != nil {
		return nil, err
	}
	st.Fingerprint = hex.EncodeToString(h.Sum(nil))
	st.FileHashes = snapshot.FileHashes
	return st, nil
}

// diffFileHashes returns changed (indexed both sides, hash moved), new (indexed
// only now), and deleted (indexed before, gone now) file lists, all as
// canonical structural paths in sorted order.
func diffFileHashes(before, after map[string]string) (changed, newFiles, deleted []string) {
	for rel, prev := range before {
		cur, ok := after[rel]
		switch {
		case !ok:
			deleted = append(deleted, graph.CanonicalStructuralPath(rel))
		case cur != prev:
			changed = append(changed, graph.CanonicalStructuralPath(rel))
		}
	}
	for rel := range after {
		if _, ok := before[rel]; !ok {
			newFiles = append(newFiles, graph.CanonicalStructuralPath(rel))
		}
	}
	sort.Strings(changed)
	sort.Strings(newFiles)
	sort.Strings(deleted)
	return changed, newFiles, deleted
}

// recordReindexDelta persists the run's attestation after a successful index.
// It records a delta only when one is honestly attestable: the index had
// records before (an empty index was never certified to a peer) and at least
// one file actually moved. Every other outcome clears any previous
// attestation, because it would anchor a from_fingerprint no peer can hold.
func recordReindexDelta(g *graph.Store, pid int64, before, after *structuralFingerprintState) error {
	if before.Records == 0 || before.Fingerprint == after.Fingerprint {
		return g.ClearReindexDelta(pid)
	}
	changed, newFiles, deleted := diffFileHashes(before.FileHashes, after.FileHashes)
	if len(changed)+len(newFiles)+len(deleted) == 0 {
		// The fingerprint moved without a file-level cause (e.g. a codemap
		// upgrade changed record metadata for unchanged files). A file delta
		// would be an empty claim of "nothing else moved" — refuse to attest.
		return g.ClearReindexDelta(pid)
	}
	return g.UpsertReindexDelta(pid, graph.ReindexDelta{
		FromFingerprint: before.Fingerprint,
		ToFingerprint:   after.Fingerprint,
		ChangedFiles:    changed,
		NewFiles:        newFiles,
		DeletedFiles:    deleted,
	})
}
