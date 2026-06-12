package cmd

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/constants"
)

// newLoginTestApp seeds a claude work login and replaces the interactive
// login flow with flow (nil = a flow that exits without touching auth,
// e.g. claude: "/login isn't available in this environment.").
func newLoginTestApp(t *testing.T, flow func(credsPath string)) (*App, string) {
	t.Helper()
	app := testApp(t, nil)
	credsPath := filepath.Join(app.Env.Home, ".claude", ".credentials.json")
	seedClaude(t, app, workToken, "work-uuid")
	withInteractive(t, func(context.Context, []string, string, ...string) (int, error) {
		if flow != nil {
			flow(credsPath)
		}
		return 0, nil
	})
	return app, credsPath
}

// swapToPersonalToken simulates a login flow that switches to another
// account by rewriting the live credential.
func swapToPersonalToken(t *testing.T) func(string) {
	return func(credsPath string) {
		live := readFile(t, credsPath)
		writeFile(t, credsPath, strings.Replace(live, workToken, personalToken, 1))
	}
}

// TestLoginUnchangedAuthRefused: a login flow that exits without touching
// the credential state must not be captured as a new account.
func TestLoginUnchangedAuthRefused(t *testing.T) {
	app, _ := newLoginTestApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatJSON}

	code, out := captureStdout(t, func() int { return runLogin(ctx, app, opts, "claude", "work", false) })
	mustExit(t, constants.ExitAuthUnchanged, code, out)
	if !strings.Contains(out, constants.CodeAuthUnchanged) {
		t.Fatalf("expected error_code %q in output: %s", constants.CodeAuthUnchanged, out)
	}
	if !strings.Contains(out, "kae add --no-login claude work") {
		t.Fatalf("expected kae add --no-login guidance in output: %s", out)
	}
	if _, found, err := account.Load(app.Paths.AccountDir("claude", "work")); err != nil || found {
		t.Fatalf("account must not be captured (found=%v err=%v)", found, err)
	}
}

// TestLoginUnchangedAuthWithRestore: --restore with an unchanged login flow
// still refuses and leaves the (untouched) live state alone.
func TestLoginUnchangedAuthWithRestore(t *testing.T) {
	app, credsPath := newLoginTestApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatJSON}

	code, out := captureStdout(t, func() int { return runLogin(ctx, app, opts, "claude", "work", true) })
	mustExit(t, constants.ExitAuthUnchanged, code, out)
	if live := readFile(t, credsPath); !strings.Contains(live, workToken) {
		t.Fatalf("live state must stay on the previous login: %s", live)
	}
}

// TestLoginCapturesChangedAuth: a login flow that rewrites the credential is
// captured and becomes active.
func TestLoginCapturesChangedAuth(t *testing.T) {
	app, _ := newLoginTestApp(t, swapToPersonalToken(t))
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	code, out := captureStdout(t, func() int { return runLogin(ctx, app, opts, "claude", "work", false) })
	mustExit(t, constants.ExitOK, code, out)
	if !strings.Contains(out, "now active") {
		t.Fatalf("expected active confirmation: %s", out)
	}
	if _, found, err := account.Load(app.Paths.AccountDir("claude", "work")); err != nil || !found {
		t.Fatalf("account must be captured (found=%v err=%v)", found, err)
	}
}

// TestLoginRestorePutsPreviousLoginBack: --restore captures the new login
// and ends on the previous one.
func TestLoginRestorePutsPreviousLoginBack(t *testing.T) {
	app, credsPath := newLoginTestApp(t, swapToPersonalToken(t))
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	code, out := captureStdout(t, func() int { return runLogin(ctx, app, opts, "claude", "work", true) })
	mustExit(t, constants.ExitOK, code, out)
	if !strings.Contains(out, "restored the previous login") {
		t.Fatalf("expected restore confirmation: %s", out)
	}
	if live := readFile(t, credsPath); !strings.Contains(live, workToken) {
		t.Fatalf("previous login must be restored: %s", live)
	}
	if _, found, err := account.Load(app.Paths.AccountDir("claude", "work")); err != nil || !found {
		t.Fatalf("account must be captured (found=%v err=%v)", found, err)
	}
}
