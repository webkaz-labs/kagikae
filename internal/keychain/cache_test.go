package keychain

import (
	"context"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/runner"
)

// countingRunner records how many subprocesses were launched, keyed by the
// first argument (the security subcommand), so the cache's coalescing can be
// asserted on the read-vs-write counts.
type countingRunner struct {
	payload string
	calls   map[string]int
}

func (c *countingRunner) Run(_ context.Context, _ string, args ...string) (string, string, int) {
	if c.calls == nil {
		c.calls = map[string]int{}
	}
	if len(args) > 0 {
		c.calls[args[0]]++
	}
	return c.payload, "", 0
}

func (c *countingRunner) RunInput(ctx context.Context, _ string, name string, args ...string) (string, string, int) {
	return c.Run(ctx, name, args...)
}

func TestReadCacheCoalescesReads(t *testing.T) {
	cr := &countingRunner{payload: `{"claudeAiOauth":{"a":1}}`}
	runner.With(cr, func() {
		ctx := WithReadCache(context.Background())
		for i := range 3 {
			if _, found, err := ReadItem(ctx, "Claude Code-credentials"); err != nil || !found {
				t.Fatalf("ReadItem %d: found=%v err=%v", i, found, err)
			}
		}
		if got := cr.calls["find-generic-password"]; got != 1 {
			t.Fatalf("expected 1 coalesced read, got %d", got)
		}
	})
}

func TestReadCacheWriteInvalidates(t *testing.T) {
	cr := &countingRunner{payload: `{"claudeAiOauth":{"a":1}}`}
	runner.With(cr, func() {
		ctx := WithReadCache(context.Background())
		if _, _, err := ReadItem(ctx, "svc"); err != nil {
			t.Fatal(err)
		}
		// A write must drop the cached read so the next read re-issues.
		if err := WriteItem(ctx, "svc", "user", []byte(`{"claudeAiOauth":{"a":2}}`)); err != nil {
			t.Fatal(err)
		}
		if _, _, err := ReadItem(ctx, "svc"); err != nil {
			t.Fatal(err)
		}
		if got := cr.calls["find-generic-password"]; got != 2 {
			t.Fatalf("expected 2 reads across the invalidating write, got %d", got)
		}
	})
}

func TestReadCacheAbsentWithoutOptIn(t *testing.T) {
	cr := &countingRunner{payload: `{"claudeAiOauth":{"a":1}}`}
	runner.With(cr, func() {
		ctx := context.Background() // no WithReadCache
		for range 2 {
			if _, _, err := ReadItem(ctx, "svc"); err != nil {
				t.Fatal(err)
			}
		}
		if got := cr.calls["find-generic-password"]; got != 2 {
			t.Fatalf("expected 2 uncached reads, got %d", got)
		}
	})
}
