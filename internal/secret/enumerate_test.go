package secret

import (
	"context"
	"sort"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/runner"
	"github.com/webkaz-labs/kagikae/internal/testutil/runnertest"
)

func TestFileBackendKeys(t *testing.T) {
	be := fileBackend{dir: t.TempDir()}
	ctx := context.Background()
	want := []string{"backup/20260101/claude/claude_ai_oauth", "claude/main/claude_ai_oauth", "codex/side/auth"}
	for _, k := range want {
		if err := be.Set(ctx, k, []byte("payload")); err != nil {
			t.Fatal(err)
		}
	}
	keys, err := be.Keys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(keys)
	if len(keys) != len(want) {
		t.Fatalf("got %v, want %v", keys, want)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("got %v, want %v", keys, want)
		}
	}
}

func TestFileBackendKeysMissingDir(t *testing.T) {
	be := fileBackend{dir: t.TempDir() + "/never-created"}
	keys, err := be.Keys(context.Background())
	if err != nil || keys != nil {
		t.Fatalf("missing dir should yield no keys: %v %v", keys, err)
	}
}

func TestLibsecretKeysParsesAttributeLines(t *testing.T) {
	out := "[/org/.../1]\nlabel = kagikae/claude/main/claude_ai_oauth\n" +
		"secret = SECRETVALUE\nattribute.service = kagikae\nattribute.key = claude/main/claude_ai_oauth\n" +
		"\n[/org/.../2]\nattribute.key = codex/side/auth\n"
	fake := &runnertest.Fake{Stdout: out}
	runner.With(fake, func() {
		keys, err := libsecretBackend{}.Keys(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(keys) != 2 || keys[0] != "claude/main/claude_ai_oauth" || keys[1] != "codex/side/auth" {
			t.Fatalf("unexpected keys: %v", keys)
		}
		// The secret value line must never be mistaken for a key.
		for _, k := range keys {
			if k == "SECRETVALUE" {
				t.Fatal("secret value leaked into keys")
			}
		}
	})
}
