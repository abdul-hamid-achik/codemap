package app

import (
	"sync"
	"testing"
)

// TestSessionConcurrentLazyOpen pins P1-14 (B37): Session's lazy Graph()/
// Vectors() opens used to be an unsynchronized check-then-set on plain struct
// fields, so concurrent callers (the TUI's tea.Batch command fan-out from
// Init, or concurrent MCP tool dispatch on one Session) could double-open a
// store or observe a half-initialized field. Hammer Graph(), Vectors(),
// VectorsReadOnly(), VectorsForMaintenance(), and Close() from many
// goroutines simultaneously; `go test -race` must report no data race, and
// every successful open must return a non-nil, usable store.
func TestSessionConcurrentLazyOpen(t *testing.T) {
	isolate(t)

	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	sess.SetEmbedder(fakeEmbedder{dims: 4})
	defer sess.Close()

	const goroutines = 24
	var wg sync.WaitGroup
	wg.Add(goroutines * 4)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			g, err := sess.Graph()
			if err != nil {
				// Close racing the open can legitimately fail an open; only a
				// nil store on a nil error would indicate a broken invariant.
				return
			}
			if g == nil {
				t.Error("Graph() returned nil store with nil error")
			}
		}()
		go func() {
			defer wg.Done()
			v, err := sess.Vectors()
			if err != nil {
				return
			}
			if v == nil {
				t.Error("Vectors() returned nil store with nil error")
			}
		}()
		go func() {
			defer wg.Done()
			v, err := sess.VectorsReadOnly()
			if err != nil {
				return
			}
			if v == nil {
				t.Error("VectorsReadOnly() returned nil store with nil error")
			}
		}()
		go func() {
			defer wg.Done()
			// VectorsForMaintenance legitimately returns (nil, nil) when no
			// vector DB file exists yet, so only errors are worth failing on.
			if _, err := sess.VectorsForMaintenance(); err != nil {
				t.Errorf("VectorsForMaintenance() error: %v", err)
			}
		}()
	}

	// Race a handful of concurrent Close calls against the opens above: Close
	// must never itself race (double-close is idempotent per the nil checks
	// under vecMu/graphMu) and every store obtained above stays valid to have
	// been returned even if Close tears it down moments later.
	var closeWG sync.WaitGroup
	closeWG.Add(4)
	for i := 0; i < 4; i++ {
		go func() {
			defer closeWG.Done()
			if err := sess.Close(); err != nil {
				t.Errorf("Close() error: %v", err)
			}
		}()
	}

	wg.Wait()
	closeWG.Wait()

	// The Session must still be usable (lazy-open semantics preserved) after
	// every store was closed out from under concurrent openers.
	if err := sess.Close(); err != nil {
		t.Fatalf("Close after concurrent hammering: %v", err)
	}
	g, err := sess.Graph()
	if err != nil {
		t.Fatalf("Graph() after concurrent hammering: %v", err)
	}
	if g == nil {
		t.Fatal("Graph() returned nil store after concurrent hammering")
	}
}
