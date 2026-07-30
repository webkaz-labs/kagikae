package cmd

import (
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
}
