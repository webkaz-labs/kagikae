package keychain

import (
	"context"
	"strings"
	"sync"
)

// readCache coalesces repeated `security find-generic-password` reads of the
// same service within one command. The recapture decision added in v0.8.1
// (docs/RELEASE.md §A/§C) reads the active account's live credential to compare
// it against the snapshot; without coalescing that read would multiply the
// `security` invocations (and the auth prompts) a single switch already makes
// (Detect, backup, recapture all read the same account-agnostic service). A
// cache scoped to one command collapses them to a single read.
//
// Reads populate the cache; writes (WriteItem/DeleteItem) invalidate the
// service entry so a later read reflects the new value. The cache lives in the
// context, so it is request-scoped with no process-global mutable state and is
// absent (behavior unchanged) unless a caller opted in with WithReadCache.
//
// Today's only caller (the switch path) is single-goroutine, but the mutex
// guards against a future concurrent caller (e.g. running per-tool Detect in
// parallel) sharing one cached context.
type readCache struct {
	mu       sync.Mutex
	items    map[string]itemEntry
	accounts map[string]acctEntry
}

type itemEntry struct {
	payload []byte
	found   bool
}

type acctEntry struct {
	account string
	found   bool
}

type cacheKey struct{}

// WithReadCache returns a context carrying a fresh read cache. Pass it down a
// single command's keychain reads so repeated reads of one service issue at
// most one `security` subprocess. Do not reuse a cached context across a child
// process run (e.g. kae run -s): the child may rotate the live credential
// behind kae's back, which the cache cannot observe.
func WithReadCache(ctx context.Context) context.Context {
	return context.WithValue(ctx, cacheKey{}, &readCache{
		items:    map[string]itemEntry{},
		accounts: map[string]acctEntry{},
	})
}

func cacheFrom(ctx context.Context) *readCache {
	c, _ := ctx.Value(cacheKey{}).(*readCache)
	return c
}

func (c *readCache) lookupItem(key string) (itemEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.items[key]
	return e, ok
}

func (c *readCache) storeItem(key string, e itemEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[key] = e
}

func (c *readCache) lookupAccount(service string) (acctEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.accounts[service]
	return e, ok
}

func (c *readCache) storeAccount(service string, e acctEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.accounts[service] = e
}

// invalidate drops both cached reads for a service after a write, so the next
// read observes the new value. It clears the service-only entry and every
// account-scoped entry of that service (keys are service or service\x00account;
// see itemKey), so a write through any match form is reflected.
func (c *readCache) invalidate(service string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.items {
		if k == service || strings.HasPrefix(k, service+"\x00") {
			delete(c.items, k)
		}
	}
	delete(c.accounts, service)
}
