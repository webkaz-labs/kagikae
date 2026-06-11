package cmd

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/constants"
)

// TestLoginUnchangedAuthRefused: a login flow that exits without touching
// the credential state must not be captured as a new account.
func TestLoginUnchangedAuthRefused(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatJSON}

	seedClaude(t, app, workToken, "work-uuid")
	withInteractive(t, func(context.Context, []string, string, ...string) (int, error) {
		return 0, nil // e.g. claude: "/login isn't available in this environment."
	})

	code, out := captureStdout(t, func() int { return runLogin(ctx, app, opts, "claude", "kaz", false) })
	mustExit(t, constants.ExitAuthUnchanged, code, out)
	if !strings.Contains(out, constants.CodeAuthUnchanged) {
		t.Fatalf("expected error_code %q in output: %s", constants.CodeAuthUnchanged, out)
	}
	if !strings.Contains(out, "kae capture claude kaz") {
		t.Fatalf("expected kae capture guidance in output: %s", out)
	}
	if _, found, err := account.Load(app.Paths.AccountDir("claude", "kaz")); err != nil || found {
		t.Fatalf("account must not be captured (found=%v err=%v)", found, err)
	}
}

// TestLoginUnchangedAuthWithRestore: --restore with an unchanged login flow
// still refuses and leaves the (untouched) live state alone.
func TestLoginUnchangedAuthWithRestore(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatJSON}
	credsPath := filepath.Join(app.Env.Home, ".claude", ".credentials.json")

	seedClaude(t, app, workToken, "work-uuid")
	withInteractive(t, func(context.Context, []string, string, ...string) (int, error) {
		return 0, nil
	})

	code, out := captureStdout(t, func() int { return runLogin(ctx, app, opts, "claude", "kaz", true) })
	mustExit(t, constants.ExitAuthUnchanged, code, out)
	if live := readFile(t, credsPath); !strings.Contains(live, workToken) {
		t.Fatalf("live state must stay on the previous login: %s", live)
	}
}

// TestLoginCapturesChangedAuth: a login flow that rewrites the credential is
// captured and becomes active.
func TestLoginCapturesChangedAuth(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	credsPath := filepath.Join(app.Env.Home, ".claude", ".credentials.json")

	seedClaude(t, app, workToken, "work-uuid")
	withInteractive(t, func(context.Context, []string, string, ...string) (int, error) {
		live := readFile(t, credsPath)
		writeFile(t, credsPath, strings.Replace(live, workToken, personalToken, 1))
		return 0, nil
	})

	code, out := captureStdout(t, func() int { return runLogin(ctx, app, opts, "claude", "kaz", false) })
	mustExit(t, constants.ExitOK, code, out)
	if !strings.Contains(out, "now active") {
		t.Fatalf("expected active confirmation: %s", out)
	}
	if _, found, err := account.Load(app.Paths.AccountDir("claude", "kaz")); err != nil || !found {
		t.Fatalf("account must be captured (found=%v err=%v)", found, err)
	}
}

// TestLoginRestorePutsPreviousLoginBack: --restore captures the new login
// and ends on the previous one.
func TestLoginRestorePutsPreviousLoginBack(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	credsPath := filepath.Join(app.Env.Home, ".claude", ".credentials.json")

	seedClaude(t, app, workToken, "work-uuid")
	withInteractive(t, func(context.Context, []string, string, ...string) (int, error) {
		live := readFile(t, credsPath)
		writeFile(t, credsPath, strings.Replace(live, workToken, personalToken, 1))
		return 0, nil
	})

	code, out := captureStdout(t, func() int { return runLogin(ctx, app, opts, "claude", "kaz", true) })
	mustExit(t, constants.ExitOK, code, out)
	if !strings.Contains(out, "restored the previous login") {
		t.Fatalf("expected restore confirmation: %s", out)
	}
	if live := readFile(t, credsPath); !strings.Contains(live, workToken) {
		t.Fatalf("previous login must be restored: %s", live)
	}
	if _, found, err := account.Load(app.Paths.AccountDir("claude", "kaz")); err != nil || !found {
		t.Fatalf("account must be captured (found=%v err=%v)", found, err)
	}
}
