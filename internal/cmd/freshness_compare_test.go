package cmd

import (
	"context"
	"errors"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/testutil/secrettest"
)

// erroringBackend wraps a backend and fails every Get, to exercise the
// comparator's error return (callers choose the policy).
type erroringBackend struct{ *secrettest.MemBackend }

func (erroringBackend) Get(context.Context, string) ([]byte, bool, error) {
	return nil, false, errors.New("backend down")
}

func TestSnapshotArtifactDiffers(t *testing.T) {
	ctx := context.Background()
	be := secrettest.NewMem()
	const ref = "claude/main/claude_ai_oauth"
	_ = be.Set(ctx, ref, []byte("stored"))

	cases := []struct {
		name          string
		storedPresent bool
		live          artifact.Value
		wantDiffers   bool
	}{
		{"equal payload", true, artifact.Value{Present: true, Data: []byte("stored")}, false},
		{"different payload", true, artifact.Value{Present: true, Data: []byte("other")}, true},
		{"presence gained", false, artifact.Value{Present: true, Data: []byte("stored")}, true},
		{"presence lost", true, artifact.Value{Present: false}, true},
		{"both absent", false, artifact.Value{Present: false}, false},
		{"missing stored payload", true, artifact.Value{Present: true, Data: []byte("x")}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			key := ref
			if c.name == "missing stored payload" {
				key = "claude/main/absent"
			}
			differs, err := snapshotArtifactDiffers(ctx, be, key, c.storedPresent, c.live)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if differs != c.wantDiffers {
				t.Fatalf("differs = %v, want %v", differs, c.wantDiffers)
			}
		})
	}

	// A backend read error is returned (the caller decides the policy); it is
	// only reached when both sides are present.
	be2 := erroringBackend{secrettest.NewMem()}
	if _, err := snapshotArtifactDiffers(ctx, be2, ref, true, artifact.Value{Present: true, Data: []byte("x")}); err == nil {
		t.Fatal("expected the backend read error to propagate")
	}
}
