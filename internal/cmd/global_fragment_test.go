package cmd

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/state"
)

// TestUseIsolatedWritesGlobalFragment covers the kae use -i happy path: a full
// per-account private home is materialized with the captured credential, the
// global mise fragment is regenerated from state.synced, and the real home is
// never touched.
func TestUseIsolatedWritesGlobalFragment(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	captureClaude(t, app, "main", mainToken)
	// Diverge the real home from the captured account so "untouched" is testable.
	realCreds := app.Env.Home + "/.claude/.credentials.json"
	writeFile(t, realCreds, `{"claudeAiOauth":{"accessToken":"`+sideToken+`"}}`)

	code, out := captureStdout(t, func() int { return runUseIsolated(ctx, app, opts, "claude", "main") })
	mustExit(t, constants.ExitOK, code, out)

	// The global isolated home holds the main credential.
	home := app.Paths.GlobalIsolatedHomeDir(constants.ToolClaude, "main")
	if creds := readFile(t, home+"/.credentials.json"); !strings.Contains(creds, mainToken) {
		t.Fatalf("global isolated home must hold the main credential: %s", creds)
	}
	// The kae-owned global fragment points CLAUDE_CONFIG_DIR at that home.
	frag := readFile(t, app.Paths.MiseGlobalFragmentFile())
	if !strings.Contains(frag, `CLAUDE_CONFIG_DIR = "`+home+`"`) {
		t.Fatalf("global fragment must export CLAUDE_CONFIG_DIR: %s", frag)
	}
	// state.synced records the binding.
	st, err := state.Load(app.Paths.StateFile())
	if err != nil {
		t.Fatal(err)
	}
	if st.Synced["claude"] != "main" {
		t.Fatalf("state.synced must record claude->main, got %v", st.Synced)
	}
	// The real home is never modified.
	if creds := readFile(t, realCreds); !strings.Contains(creds, sideToken) {
		t.Fatalf("real home must stay untouched: %s", creds)
	}
	// The home dir is classified as the global-isolated (sync) mechanism.
	if kind := app.kaeManagedHomeKind(home); kind != constants.ModeSync {
		t.Fatalf("global isolated home must classify as sync, got %q", kind)
	}
}

// TestUseSharedTearsDownGlobalIsolation covers the documented teardown: a
// shared switch (kae use -s) patches the real home and drops the tool from
// state.synced, deleting the now-empty global fragment.
func TestUseSharedTearsDownGlobalIsolation(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	captureClaude(t, app, "main", mainToken)
	captureClaude(t, app, "side", sideToken)

	code, out := captureStdout(t, func() int { return runUseIsolated(ctx, app, opts, "claude", "main") })
	mustExit(t, constants.ExitOK, code, out)
	if _, err := os.Stat(app.Paths.MiseGlobalFragmentFile()); err != nil {
		t.Fatalf("global fragment must exist after use -i: %v", err)
	}

	// kae use -s switches the real home and tears down the global isolation.
	code, out = captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "main") })
	mustExit(t, constants.ExitOK, code, out)

	if _, err := os.Stat(app.Paths.MiseGlobalFragmentFile()); !os.IsNotExist(err) {
		t.Fatalf("global fragment must be deleted once synced is empty (err=%v)", err)
	}
	st, err := state.Load(app.Paths.StateFile())
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Synced) != 0 {
		t.Fatalf("synced must be empty after teardown, got %v", st.Synced)
	}
	if creds := readFile(t, app.Env.Home+"/.claude/.credentials.json"); !strings.Contains(creds, mainToken) {
		t.Fatalf("use -s must patch the real home: %s", creds)
	}
}

// TestUseIsolatedUnsupportedTool: a tool with no stable home-isolation env var
// (agy/opencode/cursor/copilot) exits 5, before any write.
func TestUseIsolatedUnsupportedTool(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	code, out := captureStdout(t, func() int { return runUseIsolated(ctx, app, opts, "agy", "main") })
	mustExit(t, constants.ExitUnsupported, code, out)
	if _, err := os.Stat(app.Paths.MiseGlobalFragmentFile()); !os.IsNotExist(err) {
		t.Fatalf("no fragment must be written for an unsupported tool (err=%v)", err)
	}
}

// TestUseIsolatedDryRun writes nothing.
func TestUseIsolatedDryRun(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText, DryRun: true}

	captureClaude(t, app, "main", mainToken)
	code, out := captureStdout(t, func() int { return runUseIsolated(ctx, app, opts, "claude", "main") })
	mustExit(t, constants.ExitOK, code, out)

	if _, err := os.Stat(app.Paths.MiseGlobalFragmentFile()); !os.IsNotExist(err) {
		t.Fatalf("dry-run must not write the fragment (err=%v)", err)
	}
	if _, err := os.Stat(app.Paths.GlobalIsolatedHomeDir(constants.ToolClaude, "main")); !os.IsNotExist(err) {
		t.Fatalf("dry-run must not create the isolated home (err=%v)", err)
	}
}
