package app

import (
	"context"
	"sync"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/index"
)

func TestProcessesCacheHitsUntilFingerprintChanges(t *testing.T) {
	svc, proj := processService(t, processFixture)

	first, err := svc.Processes(proj, ProcessesOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if svc.procCache.hits != 0 {
		t.Fatalf("first call must load, hits=%d", svc.procCache.hits)
	}
	second, err := svc.Processes(proj, ProcessesOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if svc.procCache.hits != 1 {
		t.Fatalf("second identical call must hit the cache, hits=%d", svc.procCache.hits)
	}
	if second.ProcessesTotal != first.ProcessesTotal {
		t.Fatalf("cached run changed totals: %d != %d", second.ProcessesTotal, first.ProcessesTotal)
	}

	// explore shares the same engine path and must hit the cache too.
	if _, err := svc.Explore(context.Background(), proj, "Register", ExploreOptions{}); err != nil {
		t.Fatal(err)
	}
	if svc.procCache.hits != 2 {
		t.Fatalf("explore must reuse the cached graph, hits=%d", svc.procCache.hits)
	}

	// A reindex that rewrites a file changes the index_state fingerprint; the
	// next call must reload and see the new entrypoint.
	rewritten := `package api

import (
	"net/http"

	"example.com/pr/internal/users"
)

func Routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /signup", signup)
	mux.HandleFunc("GET /health", health)
	mux.HandleFunc("GET /stats", stats)
}

func signup(w http.ResponseWriter, r *http.Request) {
	users.Register(r.FormValue("email"))
}

func health(w http.ResponseWriter, r *http.Request) {}

func stats(w http.ResponseWriter, r *http.Request) {}
`
	writeFeatureTree(t, proj, map[string]string{"internal/api/api.go": rewritten})
	if _, err := svc.Index(context.Background(), proj, index.Options{NoLSP: true}, false); err != nil {
		t.Fatal(err)
	}
	after, err := svc.Processes(proj, ProcessesOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if svc.procCache.hits != 2 {
		t.Fatalf("reindex must invalidate the cache, hits=%d", svc.procCache.hits)
	}
	if processByID(after, "http_route:GET /stats") == nil {
		t.Fatalf("post-reindex run used a stale cache: %+v", after.Processes)
	}
}

func TestProcessesCacheConcurrentCalls(t *testing.T) {
	svc, proj := processService(t, processFixture)

	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := svc.Processes(proj, ProcessesOptions{}); err != nil {
				errs <- err
				return
			}
			_, err := svc.Explore(context.Background(), proj, "Register", ExploreOptions{})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if svc.procCache.hits < 1 {
		t.Fatalf("concurrent identical calls should mostly hit the cache, hits=%d", svc.procCache.hits)
	}
}
