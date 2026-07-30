package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/config"
	"github.com/webkaz-labs/kagikae/internal/constants"
)

func TestClaudeDriverGetenvPrecedence(t *testing.T) {
	cfg := config.Default()
	cfg.Tools[constants.ToolClaude] = config.Tool{Driver: constants.DriverValueFile}

	// Config value fills in when the env var is unset.
	get := claudeDriverGetenv(func(string) string { return "" }, cfg)
	if got := get(constants.EnvKaeClaudeDriver); got != constants.DriverValueFile {
		t.Fatalf("config fallback not applied: %q", got)
	}

	// The real env var always wins over config.
	get = claudeDriverGetenv(func(key string) string {
		if key == constants.EnvKaeClaudeDriver {
			return "explicit"
		}
		return ""
	}, cfg)
	if got := get(constants.EnvKaeClaudeDriver); got != "explicit" {
		t.Fatalf("env var did not take precedence: %q", got)
	}

	// Without the config option, Getenv is untouched.
	get = claudeDriverGetenv(func(string) string { return "passthrough" }, config.Default())
	if got := get("ANY_KEY"); got != "passthrough" {
		t.Fatalf("passthrough broken: %q", got)
	}
}

// TestNonToolLockNamesDoNotCollideWithTools pins the one thing that makes the
// shared lock directory safe: the config and state locks are named in the same
// namespace as the per-tool locks, so a tool id equal to either would make an
// unrelated critical section share that tool's lock — visible only as a
// spurious lock_busy, or as two writers to state.json that both think they hold
// it.
func TestNonToolLockNamesDoNotCollideWithTools(t *testing.T) {
	for _, name := range []string{lockNameConfig, lockNameState} {
		if constants.IsTool(name) {
			t.Errorf("lock name %q is also a tool id; give it a name no tool can take", name)
		}
	}
	// The pin locks are named pin-<pin-id> in the same directory, so a tool id
	// with that prefix would let a bound directory and a tool share a lock.
	for _, tool := range constants.Tools {
		if strings.HasPrefix(tool, "pin-") {
			t.Errorf("tool id %q collides with the pin-<pin-id> lock namespace", tool)
		}
	}
}

// TestStateWritesGoThroughTheSeam pins the convention App.mutateState exists to
// carry. state.Save stays exported (tests fixture state.json with it, and
// unexporting it would ripple through every read-only status path), so nothing
// but this stops a later change from writing state.json directly and silently
// reintroducing the lost update between concurrent tool switches — a normal
// exported call that compiles, passes review, and fails only under concurrency.
func TestStateWritesGoThroughTheSeam(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") || name == "app.go" {
			continue // app.go holds the seam; tests fixture state directly
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "state.Save(") {
			t.Errorf("%s writes state.json directly; go through App.mutateState so the write "+
				"re-reads under the state lock", name)
		}
	}
}
