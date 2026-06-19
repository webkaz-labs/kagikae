package cmd

import (
	"context"
	"sync"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/secret"
)

// countingBackend wraps a real backend and counts Get calls per key, so a test
// can assert how many times a single switch reads a given snapshot from kae's
// own secret store (the backend seam).
type countingBackend struct {
	secret.Backend
	mu   sync.Mutex
	gets map[string]int
}

func (c *countingBackend) Get(ctx context.Context, key string) ([]byte, bool, error) {
	c.mu.Lock()
	c.gets[key]++
	c.mu.Unlock()
	return c.Backend.Get(ctx, key)
}

func (c *countingBackend) reset() {
	c.mu.Lock()
	c.gets = map[string]int{}
	c.mu.Unlock()
}

// §A acceptance: a single kae use reads each target snapshot payload from the
// secret backend once (the stale-warning read and the applySnapshot read share
// the context-scoped secret.WithReadCache).
func TestSwitchReadsTargetSnapshotOnce(t *testing.T) {
	app := testApp(t, nil)
	fileBE, err := secret.Resolve(secret.BackendFile, app.Env.GOOS, app.Paths.SecretsDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	counter := &countingBackend{Backend: fileBE, gets: map[string]int{}}
	app.backendForTest = counter
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	seedClaude(t, app, mainToken, "main-uuid")
	if code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "main") }); code != constants.ExitOK {
		t.Fatalf("capture main: %s", out)
	}
	seedClaude(t, app, sideToken, "side-uuid")
	if code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "side") }); code != constants.ExitOK {
		t.Fatalf("capture side: %s", out)
	}
	if code, out := captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "main") }); code != constants.ExitOK {
		t.Fatalf("switch to main: %s", out)
	}

	target := account.SecretRef(constants.ToolClaude, "side", "claude_ai_oauth")
	counter.reset()
	if code, out := captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "side") }); code != constants.ExitOK {
		t.Fatalf("switch to side: %s", out)
	}
	if got := counter.gets[target]; got != 1 {
		t.Fatalf("expected 1 backend read of the target snapshot %s, got %d", target, got)
	}
}
