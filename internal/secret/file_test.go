package secret

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/patch"
)

func TestFileBackendMutationSyncFailures(t *testing.T) {
	ctx := context.Background()
	b := fileBackend{dir: filepath.Join(t.TempDir(), "new", "secrets")}
	key := "preservation/id/payload"
	failure := errors.New("sync failed")
	b.syncDir = func(string) error { return failure }
	if err := b.Set(ctx, key, []byte("fixture")); !errors.Is(err, failure) {
		t.Fatalf("Set error = %v", err)
	}
	if value, ok, err := b.Get(ctx, key); err != nil || !ok || string(value) != "fixture" {
		t.Fatalf("renamed payload unavailable: ok=%v err=%v", ok, err)
	}
	b.syncDir = patch.SyncDir
	if err := b.Set(ctx, key, []byte("fixture")); err != nil {
		t.Fatal(err)
	}
	b.syncDir = func(string) error { return failure }
	if err := b.Delete(ctx, key); !errors.Is(err, failure) {
		t.Fatalf("Delete error = %v", err)
	}
	if _, ok, err := b.Get(ctx, key); err != nil || ok {
		t.Fatalf("unlinked payload present: ok=%v err=%v", ok, err)
	}
	if err := b.Delete(ctx, key); !errors.Is(err, failure) {
		t.Fatalf("retry bypassed failed unlink sync: %v", err)
	}
	b.syncDir = nil
	if err := b.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
}
