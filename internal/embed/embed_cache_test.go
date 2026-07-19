package embed

import (
	"context"
	"fmt"
	"testing"
)

// These tests close coverage gap C5: the daemon's `daemon.embed_cache_size`
// setting is wired (cmd/codemap/daemon.go → daemon.Config.Throttle →
// embed.NewThrottled) into ThrottledProvider.cap, but the runtime cap/eviction
// behavior was previously untested (only config validation was covered in
// internal/config). The cache itself lives here in package embed — the daemon
// has no cache of its own, it just wraps the embedder — so this is the seam
// that pins the behavior.
//
// Eviction policy found in the code (throttle.go putCache): FIFO by insertion
// order. When len(cache) >= cap, the OLDEST inserted key (t.order[0]) is
// deleted. Neither a cache hit (getCache) nor a re-put of an existing key
// refreshes recency, so this is deliberately FIFO, NOT LRU.

// cacheLen returns the current number of cached vectors, reading under the
// provider's mutex so the assertion is race-safe.
func cacheLen(tp *ThrottledProvider) int {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	return len(tp.cache)
}

// TestThrottledCacheFIFOEvictionOrder drives putCache/getCache directly to pin
// the exact eviction order: once the cap is reached, each new insert evicts the
// oldest insert (FIFO), and evicted entries stay gone.
func TestThrottledCacheFIFOEvictionOrder(t *testing.T) {
	inner := &countingProvider{dims: 4}
	tp := NewThrottled(inner, ThrottleConfig{CacheSize: 3})

	v := func(k string) []float32 { return vecFor(k, 4) }

	// Fill exactly to capacity in insertion order k0, k1, k2.
	tp.putCache("k0", v("k0"))
	tp.putCache("k1", v("k1"))
	tp.putCache("k2", v("k2"))
	if got := cacheLen(tp); got != 3 {
		t.Fatalf("cache len after filling to cap = %d, want 3", got)
	}

	// Insert k3 → must evict the oldest insert (k0), holding the cap at 3.
	tp.putCache("k3", v("k3"))
	if got := cacheLen(tp); got != 3 {
		t.Fatalf("cache len after eviction = %d, want 3 (cap must hold)", got)
	}
	if _, ok := tp.getCache("k0"); ok {
		t.Errorf("k0 still cached; FIFO must evict the oldest insert (k0) first")
	}
	for _, k := range []string{"k1", "k2", "k3"} {
		if _, ok := tp.getCache(k); !ok {
			t.Errorf("%s evicted unexpectedly; survivors should be k1,k2,k3", k)
		}
	}

	// Insert k4 → now k1 is the oldest and must be evicted.
	tp.putCache("k4", v("k4"))
	if got := cacheLen(tp); got != 3 {
		t.Fatalf("cache len after second eviction = %d, want 3", got)
	}
	if _, ok := tp.getCache("k1"); ok {
		t.Errorf("k1 still cached; FIFO must evict k1 next")
	}
	if _, ok := tp.getCache("k0"); ok {
		t.Errorf("k0 reappeared; evicted entries must stay gone")
	}
	for _, k := range []string{"k2", "k3", "k4"} {
		if _, ok := tp.getCache(k); !ok {
			t.Errorf("%s evicted unexpectedly; survivors should be k2,k3,k4", k)
		}
	}
}

// TestThrottledCacheEvictionIsFIFONotLRU pins that touching an entry — either a
// cache hit (getCache) or a re-put of an existing key — does NOT refresh its
// recency. Under a true LRU either touch would protect k0; under the implemented
// FIFO policy k0 is still the oldest insert and is evicted next.
func TestThrottledCacheEvictionIsFIFONotLRU(t *testing.T) {
	inner := &countingProvider{dims: 4}
	tp := NewThrottled(inner, ThrottleConfig{CacheSize: 3})

	v := func(k string) []float32 { return vecFor(k, 4) }

	tp.putCache("k0", v("k0"))
	tp.putCache("k1", v("k1"))
	tp.putCache("k2", v("k2"))

	// Touch k0 both ways: a read hit and a re-put. putCache short-circuits on an
	// existing key (no reorder); getCache only reads. Neither moves k0 off the
	// front of the FIFO order.
	if _, ok := tp.getCache("k0"); !ok {
		t.Fatalf("k0 missing before eviction assertion")
	}
	tp.putCache("k0", v("k0")) // existing key → no-op, must NOT refresh order

	// Insert k3: FIFO still evicts k0 (oldest insert), proving touches don't reorder.
	tp.putCache("k3", v("k3"))
	if got := cacheLen(tp); got != 3 {
		t.Fatalf("cache len = %d, want 3", got)
	}
	if _, ok := tp.getCache("k0"); ok {
		t.Errorf("k0 survived after being touched; eviction is FIFO (insertion order) — a touch must NOT refresh recency like LRU")
	}
	for _, k := range []string{"k1", "k2", "k3"} {
		if _, ok := tp.getCache(k); !ok {
			t.Errorf("%s evicted unexpectedly; survivors should be k1,k2,k3", k)
		}
	}
}

// TestThrottledCacheCapEnforcedViaEmbed verifies the cap through the public
// Embed path (the route the daemon actually exercises): the cache never exceeds
// the configured size, FIFO retains the newest inserts, an evicted text becomes
// a cache miss again (re-embedded by the inner provider), and a retained text
// stays a cache hit.
func TestThrottledCacheCapEnforcedViaEmbed(t *testing.T) {
	inner := &countingProvider{dims: 4}
	const capSize = 2
	tp := NewThrottled(inner, ThrottleConfig{CacheSize: capSize, MaxInFlight: 2})
	ctx := context.Background()

	// Embed 5 distinct texts one batch at a time so each flows through putCache.
	for i := 0; i < 5; i++ {
		if _, err := tp.Embed(ctx, []string{fmt.Sprintf("text-%d", i)}); err != nil {
			t.Fatalf("embed text-%d: %v", i, err)
		}
		if n := cacheLen(tp); n > capSize {
			t.Fatalf("cache size %d exceeded cap %d after embedding text-%d", n, capSize, i)
		}
	}

	// FIFO keeps the two newest inserts (text-3, text-4); older ones were evicted.
	if n := cacheLen(tp); n != capSize {
		t.Fatalf("final cache size = %d, want %d", n, capSize)
	}
	if _, ok := tp.getCache(hashText("text-3")); !ok {
		t.Errorf("text-3 (recent) should still be cached")
	}
	if _, ok := tp.getCache(hashText("text-4")); !ok {
		t.Errorf("text-4 (most recent) should still be cached")
	}
	if _, ok := tp.getCache(hashText("text-0")); ok {
		t.Errorf("text-0 (oldest) should have been evicted")
	}

	// Re-embedding an evicted text must hit the inner provider again — proof the
	// entry was truly removed, not merely hidden behind the cap.
	before := inner.embedded
	if _, err := tp.Embed(ctx, []string{"text-0"}); err != nil {
		t.Fatalf("re-embed text-0: %v", err)
	}
	if got := inner.embedded - before; got != 1 {
		t.Errorf("re-embedding evicted text-0 embedded %d new texts, want 1 (cache miss after eviction)", got)
	}

	// A still-cached text must NOT re-embed (cache hit).
	before = inner.embedded
	if _, err := tp.Embed(ctx, []string{"text-4"}); err != nil {
		t.Fatalf("re-embed text-4: %v", err)
	}
	if got := inner.embedded - before; got != 0 {
		t.Errorf("re-embedding cached text-4 embedded %d new texts, want 0 (cache hit)", got)
	}
}
