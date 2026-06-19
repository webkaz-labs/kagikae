package secret

import (
	"bytes"
	"context"
	"testing"
)

// countBackend records Get/Set/Delete hits so the cache's coalescing and
// invalidation can be asserted at the inner-backend seam.
type countBackend struct {
	vals map[string][]byte
	gets map[string]int
}

func newCountBackend() *countBackend {
	return &countBackend{vals: map[string][]byte{}, gets: map[string]int{}}
}

func (b *countBackend) Name() string { return "count" }

func (b *countBackend) Get(_ context.Context, key string) ([]byte, bool, error) {
	b.gets[key]++
	v, ok := b.vals[key]
	return v, ok, nil
}

func (b *countBackend) Set(_ context.Context, key string, value []byte) error {
	b.vals[key] = append([]byte(nil), value...)
	return nil
}

func (b *countBackend) Delete(_ context.Context, key string) error {
	delete(b.vals, key)
	return nil
}

func TestCachedCoalescesRepeatedReads(t *testing.T) {
	inner := newCountBackend()
	_ = inner.Set(context.Background(), "k", []byte("v"))
	be := Cached(inner)

	ctx := WithReadCache(context.Background())
	for range 3 {
		v, found, err := be.Get(ctx, "k")
		if err != nil || !found || !bytes.Equal(v, []byte("v")) {
			t.Fatalf("unexpected read: %q found=%v err=%v", v, found, err)
		}
	}
	if inner.gets["k"] != 1 {
		t.Fatalf("expected 1 inner read with a cache, got %d", inner.gets["k"])
	}
}

func TestCachedWithoutCacheDelegates(t *testing.T) {
	inner := newCountBackend()
	_ = inner.Set(context.Background(), "k", []byte("v"))
	be := Cached(inner)

	// No WithReadCache: every read hits the inner backend (behavior unchanged).
	for range 3 {
		if _, _, err := be.Get(context.Background(), "k"); err != nil {
			t.Fatal(err)
		}
	}
	if inner.gets["k"] != 3 {
		t.Fatalf("expected 3 inner reads without a cache, got %d", inner.gets["k"])
	}
}

func TestCachedWriteInvalidates(t *testing.T) {
	inner := newCountBackend()
	_ = inner.Set(context.Background(), "k", []byte("v1"))
	be := Cached(inner)
	ctx := WithReadCache(context.Background())

	if v, _, _ := be.Get(ctx, "k"); !bytes.Equal(v, []byte("v1")) {
		t.Fatalf("first read = %q", v)
	}
	// A write invalidates the cached entry, so the next read reflects it.
	if err := be.Set(ctx, "k", []byte("v2")); err != nil {
		t.Fatal(err)
	}
	v, _, _ := be.Get(ctx, "k")
	if !bytes.Equal(v, []byte("v2")) {
		t.Fatalf("post-write read = %q, want v2", v)
	}
	if inner.gets["k"] != 2 {
		t.Fatalf("expected 2 inner reads across the invalidation, got %d", inner.gets["k"])
	}

	// Delete also invalidates.
	if err := be.Delete(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := be.Get(ctx, "k"); found {
		t.Fatal("expected not-found after delete")
	}
}
