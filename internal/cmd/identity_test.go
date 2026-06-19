package cmd

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/constants"
)

func TestSanitizeAccountName(t *testing.T) {
	cases := []struct{ raw, want string }{
		{"you@example.com", "you"},
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
	seedClaude(t, app, mainToken, "main")
	ctx := context.Background()

	code, out := captureStdout(t, func() int {
		return runCapture(ctx, app, commonOpts{Format: formatText}, "claude", "")
	})
	mustExit(t, constants.ExitOK, code, out)
	if _, found, err := account.Load(app.Paths.AccountDir(constants.ToolClaude, "main")); err != nil || !found {
		t.Fatalf("auto-detected account claude/main not captured (found=%v err=%v)", found, err)
	}
}

// An explicit account name always wins over auto-detection.
func TestAddExplicitNameWins(t *testing.T) {
	app := testApp(t, nil)
	seedClaude(t, app, mainToken, "main")
	ctx := context.Background()

	code, out := captureStdout(t, func() int {
		return runCapture(ctx, app, commonOpts{Format: formatText}, "claude", "chosen")
	})
	mustExit(t, constants.ExitOK, code, out)
	if _, found, _ := account.Load(app.Paths.AccountDir(constants.ToolClaude, "chosen")); !found {
		t.Fatal("explicit account claude/chosen not captured")
	}
	if _, found, _ := account.Load(app.Paths.AccountDir(constants.ToolClaude, "main")); found {
		t.Fatal("auto-detected name must not be used when an explicit name is given")
	}
}

// A detection failure (here agy with no ~/.gemini/google_accounts.json in the
// temp HOME) errors naming the explicit form, never a silent fallback.
func TestAddAutoDetectFailureNamesExplicitForm(t *testing.T) {
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

// agy auto-detects the account name from the active Google account when one is
// recorded (v0.8.7): on the Linux file driver, seeding google_accounts.json plus
// a credential file lets `kae add --no-login agy` (no name) capture under the
// sanitized email local part.
func TestAddAutoDetectAgyFromGoogleAccounts(t *testing.T) {
	app := testApp(t, nil) // testApp Env.GOOS = linux → agy file driver
	ctx := context.Background()
	writeFile(t, filepath.Join(app.Env.Home, ".gemini", "google_accounts.json"),
		`{"active":"you@example.com","old":[]}`)
	writeFile(t, filepath.Join(app.Env.Home, ".gemini", "antigravity-cli", "credentials.enc"), "opaque-token")

	code, out := captureStdout(t, func() int {
		return runCapture(ctx, app, commonOpts{Format: formatText}, constants.ToolAgy, "")
	})
	mustExit(t, constants.ExitOK, code, out)
	acc, found, _ := account.Load(app.Paths.AccountDir(constants.ToolAgy, "you"))
	if !found {
		t.Fatalf("agy auto-detected account 'you' not captured: %s", out)
	}
	if acc.Identity != "you@example.com" {
		t.Fatalf("agy identity = %q, want you@example.com", acc.Identity)
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

// §D: auto-detect capture records the raw detected identity (the full email)
// in the snapshot, separate from the sanitized account name.
func TestAddRecordsIdentityAutoDetect(t *testing.T) {
	app := testApp(t, nil)
	seedClaude(t, app, mainToken, "main")
	ctx := context.Background()

	code, out := captureStdout(t, func() int {
		return runCapture(ctx, app, commonOpts{Format: formatText}, "claude", "")
	})
	mustExit(t, constants.ExitOK, code, out)
	acc, found, err := account.Load(app.Paths.AccountDir(constants.ToolClaude, "main"))
	if err != nil || !found {
		t.Fatalf("claude/main not captured (found=%v err=%v)", found, err)
	}
	if acc.Identity != "main@example.com" {
		t.Fatalf("identity = %q, want main@example.com", acc.Identity)
	}
}

// §D: an explicit name still records the best-effort detected identity.
func TestAddRecordsIdentityWithExplicitName(t *testing.T) {
	app := testApp(t, nil)
	seedClaude(t, app, mainToken, "main")
	ctx := context.Background()

	code, out := captureStdout(t, func() int {
		return runCapture(ctx, app, commonOpts{Format: formatText}, "claude", "chosen")
	})
	mustExit(t, constants.ExitOK, code, out)
	acc, _, _ := account.Load(app.Paths.AccountDir(constants.ToolClaude, "chosen"))
	if acc.Identity != "main@example.com" {
		t.Fatalf("identity = %q, want main@example.com (best-effort under an explicit name)", acc.Identity)
	}
}

// §D: an explicit name with no readable identity (credential present but no
// ~/.claude.json) captures with an empty identity, never erroring.
func TestAddExplicitNameDetectionFailureLeavesIdentityEmpty(t *testing.T) {
	app := testApp(t, nil)
	seedClaudeOAuth(t, app, `{"accessToken":"x","subscriptionType":"max"}`) // credential, no .claude.json
	ctx := context.Background()

	code, out := captureStdout(t, func() int {
		return runCapture(ctx, app, commonOpts{Format: formatText}, "claude", "chosen")
	})
	mustExit(t, constants.ExitOK, code, out)
	acc, found, _ := account.Load(app.Paths.AccountDir(constants.ToolClaude, "chosen"))
	if !found || acc.Identity != "" {
		t.Fatalf("found=%v identity=%q, want captured with empty identity", found, acc.Identity)
	}
}

// §D: switch-away recapture refreshes the credential without blanking the
// stored identity.
func TestRecapturePreservesIdentity(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	seedClaude(t, app, mainToken, "main")
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "") })
	seedClaude(t, app, sideToken, "side")
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "") })

	captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "main") })
	seedClaude(t, app, "sk-ant-oat01-REFRESHED", "main") // in-tool rotation
	captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "side") })

	acc, _, _ := account.Load(app.Paths.AccountDir(constants.ToolClaude, "main"))
	if acc.Identity != "main@example.com" {
		t.Fatalf("recapture blanked the identity: %q", acc.Identity)
	}
}

// The login flow path auto-detects the account name from the post-login
// identity (resolved only after the flow exits).
func TestLoginAutoDetectsAccount(t *testing.T) {
	app := testApp(t, nil)
	seedClaude(t, app, mainToken, "olduser") // pre-login identity
	withInteractive(t, func(context.Context, []string, string, ...string) (int, error) {
		// Post-login: a new credential and a new identity become live.
		seedClaude(t, app, sideToken, "newuser")
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

// --identity records the value even when on-disk detection fails (the agy /
// current-Antigravity case, modeled here with claude credentials but no
// .claude.json so Identity() errors).
func TestAddIdentityOverrideRecordsValue(t *testing.T) {
	app := testApp(t, nil)
	seedClaudeOAuth(t, app, `{"accessToken":"x","subscriptionType":"max"}`)
	ctx := context.Background()
	code, out := captureStdout(t, func() int {
		return runCapture(ctx, app, commonOpts{Format: formatText, IdentityOverride: "you@example.com"}, "claude", "chosen")
	})
	mustExit(t, constants.ExitOK, code, out)
	acc, found, _ := account.Load(app.Paths.AccountDir(constants.ToolClaude, "chosen"))
	if !found || acc.Identity != "you@example.com" {
		t.Fatalf("found=%v identity=%q, want you@example.com", found, acc.Identity)
	}
}

// With no explicit name, --identity supplies both the recorded identity and the
// derived account name (its sanitized local part).
func TestResolveAccountDerivesNameFromIdentityOverride(t *testing.T) {
	app := testApp(t, nil)
	name, identity, err := app.resolveAccount(context.Background(), constants.ToolClaude, "", "Side.User@example.com")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if identity != "Side.User@example.com" {
		t.Fatalf("identity=%q, want Side.User@example.com", identity)
	}
	if name != "Side.User" {
		t.Fatalf("name=%q, want Side.User", name)
	}
}
