package secret

import (
	"context"
	"sync"
)

// readCache coalesces repeated Get calls for the same key within one command.
// The switch path reads each target account's snapshot payload twice from
// kae's own secret store — once for the switch-time stale warning
// (accountFreshness) and again in applySnapshot. keychain.WithReadCache
// coalesces the *upstream* tool keychain, not this store, so without this cache
// the second read issues a second backend hit. A cache scoped to one command
// collapses them to a single read (docs/RELEASE.md §A).
//
// Reads populate the cache; writes (Set/Delete) invalidate the key so a later
// read reflects the new value. The cache lives in the context, so it is
// request-scoped with no process-global mutable state and is absent (behavior
// unchanged) unless a caller opted in with WithReadCache. Do not reuse a cached
// context across a child process run (kae run -s): the child may rotate a live
// credential the cache cannot observe.
//
// The mutex guards against a future concurrent caller sharing one cached
// context (today's only caller, the switch path, is single-goroutine).
type readCache struct {
	mu    sync.Mutex
	items map[string]cacheEntry
}

type cacheEntry struct {
	value []byte
	found bool
}

type cacheKey struct{}

// WithReadCache returns a context carrying a fresh secret read cache. Pass it
// down a single command's backend reads so repeated Gets of one key issue at
// most one backend hit. Wrap the backend with Cached for the cache to apply.
func WithReadCache(ctx context.Context) context.Context {
	return context.WithValue(ctx, cacheKey{}, &readCache{items: map[string]cacheEntry{}})
}

func cacheFrom(ctx context.Context) *readCache {
	c, _ := ctx.Value(cacheKey{}).(*readCache)
	return c
}

func (c *readCache) lookup(key string) (cacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.items[key]
	return e, ok
}

func (c *readCache) store(key string, e cacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[key] = e
}

func (c *readCache) invalidate(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.items, key)
}

// Cached wraps a Backend so its Get consults a context-scoped read cache when
// one is present (WithReadCache); Set and Delete invalidate the key. Without a
// cache in the context it delegates unchanged, so it is safe to apply
// unconditionally on a path that may or may not opt in.
//
// Note: the wrapper does not forward the Enumerator capability — doctor's
// orphan detection uses the raw resolved backend, not a Cached one.
func Cached(inner Backend) Backend {
	return cachingBackend{inner: inner}
}

type cachingBackend struct{ inner Backend }

func (c cachingBackend) Name() string { return c.inner.Name() }

func (c cachingBackend) Get(ctx context.Context, key string) ([]byte, bool, error) {
	cache := cacheFrom(ctx)
	if cache == nil {
		return c.inner.Get(ctx, key)
	}
	if e, ok := cache.lookup(key); ok {
		return e.value, e.found, nil
	}
	value, found, err := c.inner.Get(ctx, key)
	if err != nil {
		return value, found, err
	}
	cache.store(key, cacheEntry{value: value, found: found})
	return value, found, nil
}

func (c cachingBackend) Set(ctx context.Context, key string, value []byte) error {
	if cache := cacheFrom(ctx); cache != nil {
		cache.invalidate(key)
	}
	return c.inner.Set(ctx, key, value)
}

func (c cachingBackend) Delete(ctx context.Context, key string) error {
	if cache := cacheFrom(ctx); cache != nil {
		cache.invalidate(key)
	}
	return c.inner.Delete(ctx, key)
}
