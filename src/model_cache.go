// model_cache.go — TTL-based model list cache to avoid repeated upstream calls.
package yarouter

import (
	"log"
	"sync"
	"time"
)

const defaultModelCacheTTL = 10 * time.Minute

// ModelCache caches a ModelList result with a configurable TTL.
// Concurrent callers are coalesced: only one fetch runs at a time,
// and stale data is served while a background refresh is in progress.
type ModelCache struct {
	mu      sync.RWMutex
	data    *ModelList
	fetched time.Time
	ttl     time.Duration

	// fetchMu serialises upstream fetches so only one runs at a time.
	fetchMu sync.Mutex
}

// NewModelCache creates a cache with the given TTL.
// If ttl <= 0, defaultModelCacheTTL is used.
func NewModelCache(ttl time.Duration) *ModelCache {
	if ttl <= 0 {
		ttl = defaultModelCacheTTL
	}
	return &ModelCache{ttl: ttl}
}

// Get returns the cached model list if still fresh, or nil.
func (c *ModelCache) Get() *ModelList {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.data != nil && time.Since(c.fetched) < c.ttl {
		return c.data
	}
	return nil
}

// GetOrFetch returns cached data if fresh; otherwise calls fetchFn,
// caches the result, and returns it. Only one fetch runs at a time;
// concurrent callers wait for the same result.
func (c *ModelCache) GetOrFetch(fetchFn func() (*ModelList, error)) (*ModelList, error) {
	if cached := c.Get(); cached != nil {
		return cached, nil
	}

	c.fetchMu.Lock()
	defer c.fetchMu.Unlock()

	// Double-check after acquiring fetch lock — another goroutine
	// may have refreshed while we waited.
	if cached := c.Get(); cached != nil {
		return cached, nil
	}

	ml, err := fetchFn()
	if err != nil {
		return nil, err
	}
	c.Set(ml)
	return ml, nil
}

// Set stores a model list snapshot with the current timestamp.
func (c *ModelCache) Set(ml *ModelList) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data = ml
	c.fetched = time.Now()
	log.Printf("model cache: stored %d models (TTL %s)", len(ml.Data), c.ttl)
}

// Invalidate clears the cache, forcing the next Get to miss.
func (c *ModelCache) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data = nil
	c.fetched = time.Time{}
}

// LastKnownGood returns the most recent cached model list without initiating a
// fetch, together with the time it was stored and whether the entry is older
// than the cache TTL. ok is false when nothing has ever been cached. This backs
// umbrella-model availability reads, which must never trigger network I/O.
func (c *ModelCache) LastKnownGood() (list *ModelList, fetched time.Time, stale bool, ok bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.data == nil {
		return nil, time.Time{}, false, false
	}
	return cloneModelList(c.data), c.fetched, time.Since(c.fetched) >= c.ttl, true
}

// Age returns how long ago the cache was last populated.
// Returns -1 if the cache is empty.
func (c *ModelCache) Age() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.data == nil {
		return -1
	}
	return time.Since(c.fetched)
}

func cloneModelList(ml *ModelList) *ModelList {
	if ml == nil {
		return nil
	}
	return &ModelList{
		Object: ml.Object,
		Data:   append([]Model(nil), ml.Data...),
	}
}
