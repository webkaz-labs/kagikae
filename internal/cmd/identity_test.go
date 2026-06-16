package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/constants"
)

func TestSanitizeAccountName(t *testing.T) {
	cases := []struct{ raw, want string }{
		{"alice@example.com", "alice"},
		{"Work.User_1@corp.example", "Work.User_1"},
		{"  spaced@example.com  ", "spaced"},
		{"handle-only", "handle-only"},
		{"weird/name:with*chars", "weirdnamewithchars"},
		{"plus+tag@example.com", "plustag"},
		{"@only-domain.com", ""},
		{"!!!", ""},
		{strings.Repeat("a", 80) + "@x", strings.Repeat("a", 64)},
	}
	for _, c := range cases {
		if got := sanitizeAccountName(c.raw); got != c.want {
			t.Errorf("sanitizeAccountName(%q) = %q, want %q", c.raw, got, c.want)
		}
	}
}

// kae add --no-login <tool> (no name) captures under the sanitized detected
// identity (docs/RELEASE.md §B). seedClaude writes oauthAccount.emailAddress =
// "<uuid>@example.com", so the detected name is the local part.
func TestAddAutoDetectClaudeNoLogin(t *testing.T) {
	app := testApp(t, nil)
	seedClaude(t, app, workToken, "alice")
	ctx := context.Background()

	code, out := captureStdout(t, func() int {
		return runCapture(ctx, app, commonOpts{Format: formatText}, "claude", "")
	})
	mustExit(t, constants.ExitOK, code, out)
	if _, found, err := account.Load(app.Paths.AccountDir(constants.ToolClaude, "alice")); err != nil || !found {
		t.Fatalf("auto-detected account claude/alice not captured (found=%v err=%v)", found, err)
	}
}

// An explicit account name always wins over auto-detection.
func TestAddExplicitNameWins(t *testing.T) {
	app := testApp(t, nil)
	seedClaude(t, app, workToken, "alice")
	ctx := context.Background()

	code, out := captureStdout(t, func() int {
		return runCapture(ctx, app, commonOpts{Format: formatText}, "claude", "chosen")
	})
	mustExit(t, constants.ExitOK, code, out)
	if _, found, _ := account.Load(app.Paths.AccountDir(constants.ToolClaude, "chosen")); !found {
		t.Fatal("explicit account claude/chosen not captured")
	}
	if _, found, _ := account.Load(app.Paths.AccountDir(constants.ToolClaude, "alice")); found {
		t.Fatal("auto-detected name must not be used when an explicit name is given")
	}
}

// A tool with no Identity capability (agy) errors naming the explicit form,
// never a silent fallback.
func TestAddAutoDetectUnsupportedTool(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()

	code, out := captureStdout(t, func() int {
		return runCapture(ctx, app, commonOpts{Format: formatJSON}, constants.ToolAgy, "")
	})
	mustExit(t, constants.ExitUsage, code, out)
	if !strings.Contains(out, "kae add agy <account>") {
		t.Fatalf("expected explicit-form guidance for agy: %s", out)
	}
}

// A detectable tool that is logged out (no identity file) errors naming the
// explicit form.
func TestAddAutoDetectNotLoggedIn(t *testing.T) {
	app := testApp(t, nil) // no seedClaude: no ~/.claude.json
	ctx := context.Background()

	code, out := captureStdout(t, func() int {
		return runCapture(ctx, app, commonOpts{Format: formatJSON}, "claude", "")
	})
	mustExit(t, constants.ExitUsage, code, out)
	if !strings.Contains(out, "kae add claude <account>") {
		t.Fatalf("expected explicit-form guidance in output: %s", out)
	}
}

// The login flow path auto-detects the account name from the post-login
// identity (resolved only after the flow exits).
func TestLoginAutoDetectsAccount(t *testing.T) {
	app := testApp(t, nil)
	seedClaude(t, app, workToken, "olduser") // pre-login identity
	withInteractive(t, func(context.Context, []string, string, ...string) (int, error) {
		// Post-login: a new credential and a new identity become live.
		seedClaude(t, app, personalToken, "newuser")
		return 0, nil
	})
	ctx := context.Background()

	code, out := captureStdout(t, func() int {
		return runLogin(ctx, app, commonOpts{Format: formatText}, "claude", "", false)
	})
	mustExit(t, constants.ExitOK, code, out)
	if _, found, err := account.Load(app.Paths.AccountDir(constants.ToolClaude, "newuser")); err != nil || !found {
		t.Fatalf("login auto-detected account claude/newuser not captured (found=%v err=%v)", found, err)
	}
}
