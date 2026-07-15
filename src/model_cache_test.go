// model_cache_test.go — tests for the TTL-based model cache.
package yarouter

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestModelCache_GetEmpty(t *testing.T) {
	c := NewModelCache(time.Minute)
	if got := c.Get(); got != nil {
		t.Errorf("empty cache returned non-nil: %v", got)
	}
	if age := c.Age(); age != -1 {
		t.Errorf("empty cache age = %v, want -1", age)
	}
}

func TestModelCache_SetAndGet(t *testing.T) {
	c := NewModelCache(time.Minute)
	ml := &ModelList{Object: "list", Data: []Model{{ID: "m1"}}}
	c.Set(ml)

	got := c.Get()
	if got == nil || len(got.Data) != 1 || got.Data[0].ID != "m1" {
		t.Fatalf("cache miss after Set: %v", got)
	}
	if age := c.Age(); age < 0 || age > time.Second {
		t.Errorf("unexpected age: %v", age)
	}
}

func TestModelCache_TTLExpiry(t *testing.T) {
	c := NewModelCache(10 * time.Millisecond)
	ml := &ModelList{Object: "list", Data: []Model{{ID: "m1"}}}
	c.Set(ml)

	time.Sleep(20 * time.Millisecond)
	if got := c.Get(); got != nil {
		t.Errorf("cache should have expired, got: %v", got)
	}
}

func TestModelCache_Invalidate(t *testing.T) {
	c := NewModelCache(time.Minute)
	c.Set(&ModelList{Object: "list", Data: []Model{{ID: "m1"}}})
	c.Invalidate()
	if got := c.Get(); got != nil {
		t.Errorf("invalidated cache returned non-nil: %v", got)
	}
}

func TestModelCache_GetOrFetch(t *testing.T) {
	c := NewModelCache(time.Minute)
	var calls atomic.Int32
	ml := &ModelList{Object: "list", Data: []Model{{ID: "fetched"}}}

	fetcher := func() (*ModelList, error) {
		calls.Add(1)
		return ml, nil
	}

	// First call should invoke fetcher.
	got, err := c.GetOrFetch(fetcher)
	if err != nil || got == nil || got.Data[0].ID != "fetched" {
		t.Fatalf("first GetOrFetch: err=%v, got=%v", err, got)
	}
	if calls.Load() != 1 {
		t.Errorf("fetcher called %d times, want 1", calls.Load())
	}

	// Second call should use cache.
	got2, err := c.GetOrFetch(fetcher)
	if err != nil || got2 == nil || got2.Data[0].ID != "fetched" {
		t.Fatalf("second GetOrFetch: err=%v, got=%v", err, got2)
	}
	if calls.Load() != 1 {
		t.Errorf("fetcher called %d times on cache hit, want 1", calls.Load())
	}
}

func TestModelCache_GetOrFetch_Error(t *testing.T) {
	c := NewModelCache(time.Minute)
	errFetch := errors.New("network error")
	_, err := c.GetOrFetch(func() (*ModelList, error) {
		return nil, errFetch
	})
	if !errors.Is(err, errFetch) {
		t.Errorf("GetOrFetch error = %v, want %v", err, errFetch)
	}
	// Cache should still be empty after error.
	if got := c.Get(); got != nil {
		t.Errorf("cache should be empty after error, got: %v", got)
	}
}

func TestModelCache_GetOrFetch_Concurrent(t *testing.T) {
	c := NewModelCache(time.Minute)
	var calls atomic.Int32
	ml := &ModelList{Object: "list", Data: []Model{{ID: "concurrent"}}}

	fetcher := func() (*ModelList, error) {
		calls.Add(1)
		time.Sleep(50 * time.Millisecond)
		return ml, nil
	}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := c.GetOrFetch(fetcher)
			if err != nil || got == nil || got.Data[0].ID != "concurrent" {
				t.Errorf("concurrent GetOrFetch: err=%v, got=%v", err, got)
			}
		}()
	}
	wg.Wait()

	// Only 1 fetch should have happened (others waited or got cache hit).
	if n := calls.Load(); n != 1 {
		t.Errorf("concurrent fetcher called %d times, want 1", n)
	}
}

func TestModelCache_DefaultTTL(t *testing.T) {
	c := NewModelCache(0)
	if c.ttl != defaultModelCacheTTL {
		t.Errorf("default TTL = %v, want %v", c.ttl, defaultModelCacheTTL)
	}
}
