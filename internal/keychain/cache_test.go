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

// The account-scoped half of the same rule, and the reason a service-keyed invalidate
// is enough for a cache keyed on service **and** account: `invalidate` drops every
// `service\x00account` entry by prefix, not just the bare service key. It
// over-invalidates across sibling accounts of one service, which is the safe direction.
//
// This is what every account-scoped item relies on — claude's per-config-dir
// credentials, codex's per-`CODEX_HOME` `Codex Auth`, agy's shared `gemini` service —
// and it was unpinned: removing `invalidate` entirely survived the whole `internal/cmd`
// suite, and the test above only covers the service-only key. A later "make invalidate
// precise" cleanup is exactly what this catches.
func TestReadCacheWriteInvalidatesASiblingAccount(t *testing.T) {
	cr := &countingRunner{payload: `{"claudeAiOauth":{"a":1}}`}
	runner.With(cr, func() {
		ctx := WithReadCache(context.Background())
		if _, _, err := ReadItemForAccount(ctx, "svc", "me"); err != nil {
			t.Fatal(err)
		}
		// Coalesced, so the write below is the only thing that can force a re-read.
		if _, _, err := ReadItemForAccount(ctx, "svc", "me"); err != nil {
			t.Fatal(err)
		}
		if got := cr.calls["find-generic-password"]; got != 1 {
			t.Fatalf("expected the second read to be coalesced, got %d", got)
		}
		// A write under a *different* account of the same service must still drop it:
		// the service is what changed, and the cache cannot know the two items apart.
		if err := WriteItem(ctx, "svc", "other", []byte(`{"claudeAiOauth":{"a":2}}`)); err != nil {
			t.Fatal(err)
		}
		if _, _, err := ReadItemForAccount(ctx, "svc", "me"); err != nil {
			t.Fatal(err)
		}
		if got := cr.calls["find-generic-password"]; got != 2 {
			t.Fatalf("a write under a sibling account left this account's entry cached: %d reads", got)
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
