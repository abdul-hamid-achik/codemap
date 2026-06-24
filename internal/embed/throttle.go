package embed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"

	"golang.org/x/sync/singleflight"
	"golang.org/x/time/rate"
)

// ThrottleConfig tunes a ThrottledProvider. Zero values get sane defaults.
type ThrottleConfig struct {
	RPS         float64 // token-bucket rate of inner calls for BACKGROUND (index) embeds; <=0 = no limit
	Burst       int     // token-bucket burst (background)
	MaxInFlight int     // max concurrent inner Embed calls (default 2)
	CacheSize   int     // max cached vectors by content hash (default 4096)
}

// ThrottledProvider wraps an embedding Provider to spare a local Ollama under a
// continuously-syncing daemon: it (1) DEDUPES by content hash — a given text is
// embedded once and cached, and concurrent identical requests are single-flighted;
// (2) RATE-LIMITS background (index) embeds with a token bucket and bounds the
// number of concurrent inner calls; and (3) offers a QueryEmbed lane for
// interactive search that skips the background rate limit so a reindex storm never
// stalls a user's query. It satisfies embed.Provider, so it drops in anywhere.
type ThrottledProvider struct {
	inner   Provider
	limiter *rate.Limiter // background lane; nil = unlimited
	sem     chan struct{} // max-in-flight
	group   singleflight.Group

	mu    sync.Mutex
	cache map[string][]float32
	order []string // FIFO eviction
	cap   int
}

// NewThrottled wraps inner with the given throttle policy.
func NewThrottled(inner Provider, cfg ThrottleConfig) *ThrottledProvider {
	if cfg.MaxInFlight < 1 {
		cfg.MaxInFlight = 2
	}
	if cfg.CacheSize <= 0 {
		cfg.CacheSize = 4096
	}
	var lim *rate.Limiter
	if cfg.RPS > 0 {
		burst := cfg.Burst
		if burst < 1 {
			burst = 1
		}
		lim = rate.NewLimiter(rate.Limit(cfg.RPS), burst)
	}
	return &ThrottledProvider{
		inner:   inner,
		limiter: lim,
		sem:     make(chan struct{}, cfg.MaxInFlight),
		cache:   make(map[string][]float32),
		cap:     cfg.CacheSize,
	}
}

// Embed embeds a batch on the BACKGROUND lane (rate-limited) — used by indexing.
func (t *ThrottledProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	return t.embed(ctx, texts, t.limiter)
}

// QueryEmbed embeds a batch on the interactive lane: it skips the background rate
// limit (still bounded by max-in-flight) so a search isn't delayed behind a
// reindex. Use it for query-time embeddings.
func (t *ThrottledProvider) QueryEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	return t.embed(ctx, texts, nil)
}

// Profile delegates to the wrapped provider (the throttle doesn't change the space).
func (t *ThrottledProvider) Profile() EmbeddingProfile { return t.inner.Profile() }

// Available forwards the inner provider's reachability probe (if it has one), so a
// caller can still detect an unreachable backend (e.g. Ollama down) and fall back
// to structure-only — the throttle wrapping must not hide that signal.
func (t *ThrottledProvider) Available(ctx context.Context) error {
	if a, ok := t.inner.(interface {
		Available(context.Context) error
	}); ok {
		return a.Available(ctx)
	}
	return nil
}

func (t *ThrottledProvider) embed(ctx context.Context, texts []string, lim *rate.Limiter) ([][]float32, error) {
	out := make([][]float32, len(texts))
	// Serve from cache; collect the unique misses (dedup within the batch too).
	rep := make(map[string]string)
	idxs := make(map[string][]int)
	var order []string
	for i, txt := range texts {
		h := hashText(txt)
		if v, ok := t.getCache(h); ok {
			out[i] = v
			continue
		}
		if _, seen := idxs[h]; !seen {
			order = append(order, h)
			rep[h] = txt
		}
		idxs[h] = append(idxs[h], i)
	}
	for _, h := range order {
		v, err, _ := t.group.Do(h, func() (any, error) {
			if cv, ok := t.getCache(h); ok { // another goroutine may have filled it
				return cv, nil
			}
			vec, eerr := t.embedOne(ctx, rep[h], lim)
			if eerr != nil {
				return nil, eerr
			}
			t.putCache(h, vec)
			return vec, nil
		})
		if err != nil {
			return nil, err
		}
		vec := v.([]float32)
		for _, i := range idxs[h] {
			out[i] = vec
		}
	}
	return out, nil
}

// embedOne sends one text through the rate limiter + max-in-flight gate to the
// inner provider.
func (t *ThrottledProvider) embedOne(ctx context.Context, txt string, lim *rate.Limiter) ([]float32, error) {
	if lim != nil {
		if err := lim.Wait(ctx); err != nil {
			return nil, err
		}
	}
	select {
	case t.sem <- struct{}{}:
		defer func() { <-t.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	vecs, err := t.inner.Embed(ctx, []string{txt})
	if err != nil {
		return nil, err
	}
	if len(vecs) != 1 {
		return nil, fmt.Errorf("throttle: inner provider returned %d vectors for 1 text", len(vecs))
	}
	return vecs[0], nil
}

func (t *ThrottledProvider) getCache(h string) ([]float32, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	v, ok := t.cache[h]
	return v, ok
}

func (t *ThrottledProvider) putCache(h string, v []float32) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, exists := t.cache[h]; exists {
		return
	}
	if t.cap > 0 && len(t.cache) >= t.cap {
		delete(t.cache, t.order[0]) // FIFO eviction
		t.order = t.order[1:]
	}
	t.cache[h] = v
	t.order = append(t.order, h)
}

func hashText(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
