package cmd

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/adapter/claude"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/paths"
	"github.com/webkaz-labs/kagikae/internal/runner"
	"github.com/webkaz-labs/kagikae/internal/secret"
	"github.com/webkaz-labs/kagikae/internal/testutil/runnertest"
)

// setupBondHome seeds the non-credential half of a realistic claude real home.
// The credential comes from captureClaudeAccount, because a bond materializes
// the bound account's snapshot rather than whatever is live.
func setupBondHome(t *testing.T, app *App) {
	t.Helper()
	home := filepath.Join(app.Env.Home, ".claude")
	writeFile(t, filepath.Join(home, "settings.json"), `{"theme":"dark"}`)
	writeFile(t, filepath.Join(home, "CLAUDE.md"), "# project\n")
}

// captureClaudeAccount seeds a live claude login and captures it under name, so
// the temp-HOME store holds a real snapshot for the per-directory materializers
// to read.
func captureClaudeAccount(t *testing.T, app *App, name, token string) {
	t.Helper()
	seedClaude(t, app, token, name+"-uuid")
	code, out := captureStdout(t, func() int {
		return runCapture(context.Background(), app, commonOpts{Format: formatText}, "claude", name)
	})
	mustExit(t, constants.ExitOK, code, out)
}

// sha8Of mirrors the per-config-dir suffix claude derives. The formula itself is
// pinned against an externally computed hash in the adapter's tests; here it only
// has to agree with the service name the write actually targeted.
func sha8Of(dir string) string {
	sum := sha256.Sum256([]byte(dir))
	return fmt.Sprintf("%x", sum[:4])
}

func testBackend(t *testing.T, app *App) secret.Backend {
	t.Helper()
	be, err := app.secretBackend()
	if err != nil {
		t.Fatal(err)
	}
	return be
}

func TestPrepareBondSymlinksNonDenylist(t *testing.T) {
	app := testApp(t, nil)
	captureClaudeAccount(t, app, "main", mainToken)
	setupBondHome(t, app)
	cwd := t.TempDir()
	pinID := paths.PinID(cwd)

	bondDir, err := app.prepareBond(context.Background(), testBackend(t, app), constants.ToolClaude, "main", pinID)
	if err != nil {
		t.Fatalf("prepareBond: %v", err)
	}

	// settings.json and CLAUDE.md must be symlinks pointing into the real home.
	for _, item := range []string{"settings.json", "CLAUDE.md"} {
		dst := filepath.Join(bondDir, item)
		info, err := os.Lstat(dst)
		if err != nil {
			t.Fatalf("%s missing in bond dir: %v", item, err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			t.Errorf("%s must be a symlink in bond dir", item)
		}
		target, _ := os.Readlink(dst)
		want := filepath.Join(app.Env.Home, ".claude", item)
		if target != want {
			t.Errorf("%s symlink points to %q, want %q", item, target, want)
		}
	}
}

// TestPrepareBondCredentialComesFromSnapshotNotLiveHome pins the second defect
// fixed alongside the macOS pin gap: the materializers copied the *live* store,
// so binding an account that was not currently active seeded the directory with
// whichever credential happened to be live.
func TestPrepareBondCredentialComesFromSnapshotNotLiveHome(t *testing.T) {
	app := testApp(t, nil)
	captureClaudeAccount(t, app, "main", mainToken)
	// Another account is live now. A bond for main must still materialize main's
	// credential, not this one.
	seedClaude(t, app, sideToken, "side-uuid")
	setupBondHome(t, app)
	cwd := t.TempDir()
	pinID := paths.PinID(cwd)

	bondDir, err := app.prepareBond(context.Background(), testBackend(t, app), constants.ToolClaude, "main", pinID)
	if err != nil {
		t.Fatalf("prepareBond: %v", err)
	}

	dst := filepath.Join(bondDir, ".credentials.json")
	info, err := os.Lstat(dst)
	if err != nil {
		t.Fatalf(".credentials.json missing in bond dir: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Error(".credentials.json must be a private copy, not a symlink")
	}
	got := readFile(t, dst)
	if !strings.Contains(got, mainToken) {
		t.Errorf("bond dir must hold the bound account's snapshot, got %q", got)
	}
	if strings.Contains(got, sideToken) {
		t.Errorf("bond dir holds the live account's credential instead of the bound one: %q", got)
	}
}

func TestPrepareBondIdempotent(t *testing.T) {
	app := testApp(t, nil)
	captureClaudeAccount(t, app, "main", mainToken)
	setupBondHome(t, app)
	be := testBackend(t, app)
	cwd := t.TempDir()
	pinID := paths.PinID(cwd)

	// First run.
	if _, err := app.prepareBond(context.Background(), be, constants.ToolClaude, "main", pinID); err != nil {
		t.Fatalf("first prepareBond: %v", err)
	}
	// Second run must succeed without error.
	bondDir, err := app.prepareBond(context.Background(), be, constants.ToolClaude, "main", pinID)
	if err != nil {
		t.Fatalf("second prepareBond (idempotent): %v", err)
	}

	// Verify symlinks still correct after second run.
	dst := filepath.Join(bondDir, "settings.json")
	info, _ := os.Lstat(dst)
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("settings.json must remain a symlink after re-bond")
	}
}

// An account with no captured credential used to be skipped silently, leaving a
// bond that reported success and had no credential in it. The snapshot is now
// the source, so its absence is reported instead.
func TestPrepareBondFailsWhenAccountNotCaptured(t *testing.T) {
	app := testApp(t, nil)
	setupBondHome(t, app)
	cwd := t.TempDir()
	pinID := paths.PinID(cwd)

	_, err := app.prepareBond(context.Background(), testBackend(t, app), constants.ToolClaude, "main", pinID)
	if err == nil {
		t.Fatal("binding an uncaptured account must fail, not silently produce a credential-less bond")
	}
	if code := exitOf(err); code != constants.ExitNotFound {
		t.Fatalf("exit code = %d, want %d (%v)", code, constants.ExitNotFound, err)
	}
}

// TestPrepareBondDarwinWritesPerDirKeychainItem is the macOS pin gap's
// regression test. claude namespaces its keychain service by the config dir and
// reads the keychain before the file, so the credential for a bound directory
// belongs in *that directory's* item; writing the file instead is what let a
// pinned directory keep running the previous account.
func TestPrepareBondDarwinWritesPerDirKeychainItem(t *testing.T) {
	app := testApp(t, nil)
	app.Env.GOOS = "darwin"
	setupBondHome(t, app)
	payload := `{"claudeAiOauth":{"accessToken":"` + mainToken + `","subscriptionType":"max"}}`

	// Capture under the keychain driver so the snapshot holds the verbatim
	// keychain payload the apply path writes back.
	runner.With(&runnertest.Fake{Stdout: payload, Code: 0}, func() {
		captureClaudeAccount(t, app, "main", mainToken)
	})

	cwd := t.TempDir()
	pinID := paths.PinID(cwd)
	bondDir := app.Paths.SharedDir(pinID, constants.ToolClaude)
	// A plaintext copy left behind by an earlier kae version must not survive:
	// nothing reads it once the keychain item exists, and claude only deletes it
	// itself when it finds no item.
	writeFile(t, filepath.Join(bondDir, ".credentials.json"), payload)

	fake := &runnertest.Fake{Code: 0}
	runner.With(fake, func() {
		if _, err := app.prepareBond(context.Background(), testBackend(t, app),
			constants.ToolClaude, "main", pinID); err != nil {
			t.Fatalf("prepareBond: %v", err)
		}
	})

	wantService := claude.KeychainService + "-" + sha8Of(bondDir)
	if fake.Name != "security" {
		t.Fatalf("keychain write did not go through the security CLI: %q", fake.Name)
	}
	args := strings.Join(fake.Args, " ")
	if !strings.Contains(args, "add-generic-password") || !strings.Contains(args, wantService) {
		t.Fatalf("credential was not written to the per-directory item %q: %v", wantService, fake.Args)
	}
	if _, err := os.Stat(filepath.Join(bondDir, ".credentials.json")); !os.IsNotExist(err) {
		t.Error("the superseded plaintext copy must be removed once the keychain item holds the credential")
	}
}

// A keychain write that fails must surface. Falling back to the plaintext file
// would report success and rebuild the original defect: a credential file in a
// directory whose tool reads the keychain first.
func TestPrepareBondDarwinKeychainWriteFailureIsNotDowngraded(t *testing.T) {
	app := testApp(t, nil)
	app.Env.GOOS = "darwin"
	setupBondHome(t, app)
	payload := `{"claudeAiOauth":{"accessToken":"` + mainToken + `","subscriptionType":"max"}}`
	runner.With(&runnertest.Fake{Stdout: payload, Code: 0}, func() {
		captureClaudeAccount(t, app, "main", mainToken)
	})

	cwd := t.TempDir()
	pinID := paths.PinID(cwd)
	bondDir := app.Paths.SharedDir(pinID, constants.ToolClaude)

	runner.With(&runnertest.Fake{Stderr: "keychain is locked", Code: 1}, func() {
		if _, err := app.prepareBond(context.Background(), testBackend(t, app),
			constants.ToolClaude, "main", pinID); err == nil {
			t.Fatal("a failed keychain write must be reported, not downgraded to a file write")
		}
	})
	if _, err := os.Stat(filepath.Join(bondDir, ".credentials.json")); !os.IsNotExist(err) {
		t.Error("a failed keychain write must not leave a plaintext credential behind")
	}
}
