package cmd

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/constants"
)

// captureStderr swaps os.Stderr for the duration of run and returns its exit
// code and captured stderr (mirrors captureStdout in cmd_test.go).
func captureStderr(t *testing.T, run func() int) (int, string) {
	t.Helper()
	old := os.Stderr
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = write
	code := run()
	if err := write.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stderr = old
	out, err := io.ReadAll(read)
	if err != nil {
		t.Fatal(err)
	}
	return code, string(out)
}

// TestVerbAliasesRoute verifies the single-letter verb aliases dispatch to the
// right command (not the unknown-command fallback, which also exits usage).
// Each case triggers an arg-validation error that fires before any config or
// HOME access, and whose message names the target command.
func TestVerbAliasesRoute(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		wantInErr string
	}{
		{"r->run", []string{"r"}, "kae run"},             // missing `-- <cmd>`
		{"d->doctor", []string{"d", "x", "y"}, "doctor"}, // too many positionals
		{"s->status", []string{"s", "extra"}, "status"},  // too many positionals
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, out := captureStderr(t, func() int { return Root(tc.args) })
			if code != constants.ExitUsage {
				t.Fatalf("want ExitUsage, got %d (%s)", code, out)
			}
			if !strings.Contains(out, tc.wantInErr) {
				t.Fatalf("want stderr containing %q, got %q", tc.wantInErr, out)
			}
			if strings.Contains(out, "unknown command") {
				t.Fatalf("alias %v fell through to the unknown-command path: %s", tc.args, out)
			}
		})
	}
}

// TestSwitchTombstoneKeepsSWasStatus confirms `s` is no longer the switch
// pointer (it routes to status now) while `switch` stays a removed-command
// pointer.
func TestSwitchTombstoneKeepsSWasStatus(t *testing.T) {
	code, out := captureStderr(t, func() int { return Root([]string{"switch"}) })
	if code != constants.ExitUsage || !strings.Contains(out, "kae use") {
		t.Fatalf("switch must still point at kae use, got %d (%s)", code, out)
	}
	// `s extra` hits the status usage error, not the switch pointer.
	_, sOut := captureStderr(t, func() int { return Root([]string{"s", "extra"}) })
	if strings.Contains(sOut, "removed") || strings.Contains(sOut, "kae use <profile>") {
		t.Fatalf("s must route to status, not the switch tombstone: %s", sOut)
	}
}
