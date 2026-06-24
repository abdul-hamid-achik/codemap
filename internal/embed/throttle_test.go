package embed

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// countingProvider is a fake inner provider that counts how many texts it embeds
// and tracks peak concurrency, returning a deterministic vector per text.
type countingProvider struct {
	dims     int
	mu       sync.Mutex
	embedded int
	inFlight int32
	maxSeen  int32
}

func (c *countingProvider) Profile() EmbeddingProfile {
	return EmbeddingProfile{Provider: "fake", Model: "fake", Dimensions: c.dims, Distance: "cosine"}
}

func (c *countingProvider) Embed(_ context.Context, texts []string) ([][]float32, error) {
	n := atomic.AddInt32(&c.inFlight, 1)
	defer atomic.AddInt32(&c.inFlight, -1)
	for {
		old := atomic.LoadInt32(&c.maxSeen)
		if n <= old || atomic.CompareAndSwapInt32(&c.maxSeen, old, n) {
			break
		}
	}
	time.Sleep(5 * time.Millisecond) // simulate work so concurrency is observable
	c.mu.Lock()
	c.embedded += len(texts)
	c.mu.Unlock()
	out := make([][]float32, len(texts))
	for i, txt := range texts {
		out[i] = vecFor(txt, c.dims)
	}
	return out, nil
}

func vecFor(s string, dims int) []float32 {
	v := make([]float32, dims)
	for i := range v {
		if len(s) > 0 {
			v[i] = float32((int(s[i%len(s)]) + i) % 17)
		}
	}
	return v
}

func vecEqual(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestThrottledDedup(t *testing.T) {
	inner := &countingProvider{dims: 4}
	tp := NewThrottled(inner, ThrottleConfig{MaxInFlight: 2})
	ctx := context.Background()

	// The same text across two separate calls → the inner provider embeds it once.
	a, err := tp.Embed(ctx, []string{"hello"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := tp.Embed(ctx, []string{"hello"})
	if err != nil {
		t.Fatal(err)
	}
	if inner.embedded != 1 {
		t.Errorf("inner embedded %d texts, want 1 (cache dedup)", inner.embedded)
	}
	if !vecEqual(a[0], b[0]) || !vecEqual(a[0], vecFor("hello", 4)) {
		t.Errorf("dedup returned wrong or inconsistent vectors")
	}

	// A batch containing a duplicate → inner sees only the unique new texts.
	inner.embedded = 0
	out, err := tp.Embed(ctx, []string{"x", "y", "x", "z"})
	if err != nil {
		t.Fatal(err)
	}
	if inner.embedded != 3 {
		t.Errorf("batch with a duplicate: inner embedded %d, want 3 unique", inner.embedded)
	}
	if len(out) != 4 || !vecEqual(out[0], out[2]) {
		t.Errorf("duplicate text in a batch should map to the same vector")
	}
	if !vecEqual(out[1], vecFor("y", 4)) {
		t.Errorf("vector for 'y' is wrong")
	}
}

func TestThrottledMaxInFlight(t *testing.T) {
	inner := &countingProvider{dims: 4}
	tp := NewThrottled(inner, ThrottleConfig{MaxInFlight: 2})
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			if _, err := tp.Embed(ctx, []string{fmt.Sprintf("t%d", n)}); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if got := atomic.LoadInt32(&inner.maxSeen); got > 2 {
		t.Errorf("peak concurrent inner Embed calls = %d, want <= 2 (MaxInFlight)", got)
	}
	if inner.embedded != 12 {
		t.Errorf("inner embedded %d distinct texts, want 12", inner.embedded)
	}
}
