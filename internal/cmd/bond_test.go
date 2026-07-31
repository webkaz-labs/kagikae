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
// The credential comes from captureClaude, because a bond materializes
// the bound account's snapshot rather than whatever is live.
func setupBondHome(t *testing.T, app *App) {
	t.Helper()
	home := filepath.Join(app.Env.Home, ".claude")
	writeFile(t, filepath.Join(home, "settings.json"), `{"theme":"dark"}`)
	writeFile(t, filepath.Join(home, "CLAUDE.md"), "# project\n")
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
	captureClaude(t, app, "main", mainToken)
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

// With a user-set CLAUDE_CONFIG_DIR, claude keeps its mixed-state file *inside*
// that directory, so it is an entry of the real home and used to be symlinked into
// every bond dir — which made the per-directory identity write land on the real
// home and therefore be declined, permanently, for exactly that class of user. It
// is denylisted now, so the bond dir gets a private copy and the identity lands.
func TestPrepareBondKeepsIdentityPrivateUnderUserConfigDir(t *testing.T) {
	app := testApp(t, nil)
	custom := filepath.Join(app.Env.Home, "custom-claude")
	app.Env.Getenv = func(key string) string {
		if key == "CLAUDE_CONFIG_DIR" {
			return custom
		}
		return ""
	}
	// claude's whole home is the user-set config dir, mixed-state file included.
	writeFile(t, filepath.Join(custom, "settings.json"), `{"theme":"dark"}`)
	writeFile(t, filepath.Join(custom, ".credentials.json"),
		`{"claudeAiOauth":{"accessToken":"`+mainToken+`","subscriptionType":"max"}}`)
	writeFile(t, filepath.Join(custom, ".claude.json"),
		`{"oauthAccount":{"accountUuid":"main-uuid","emailAddress":"main-uuid@example.com"},"projects":{"/repo":{}}}`)
	code, out := captureStdout(t, func() int {
		return runCapture(context.Background(), app, commonOpts{Format: formatText}, "claude", "main")
	})
	mustExit(t, constants.ExitOK, code, out)
	// Another account is live in the real home now, so "the bond dir names main"
	// and "the real home still names side" are distinguishable.
	writeFile(t, filepath.Join(custom, ".claude.json"),
		`{"oauthAccount":{"accountUuid":"side-uuid"},"projects":{"/repo":{}}}`)

	var stderr string
	var bondDir string
	_, stderr = captureStderr(t, func() int {
		dir, err := app.prepareBond(context.Background(), testBackend(t, app),
			constants.ToolClaude, "main", paths.PinID(t.TempDir()))
		if err != nil {
			t.Errorf("prepareBond: %v", err)
			return 1
		}
		bondDir = dir
		return 0
	})
	if bondDir == "" {
		t.FailNow()
	}
	if strings.Contains(stderr, "identity cache") {
		t.Fatalf("the identity must not be declined once the file is private: %q", stderr)
	}
	info, err := os.Lstat(filepath.Join(bondDir, ".claude.json"))
	if err != nil {
		t.Fatalf("bond dir has no identity file: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("the identity file must be private, not a link back to the real home")
	}
	if got := readFile(t, filepath.Join(bondDir, ".claude.json")); !strings.Contains(got, "main-uuid") {
		t.Fatalf("bound account's identity not applied: %s", got)
	}
	// The real home is untouched — the whole point of keeping the copy private.
	if got := readFile(t, filepath.Join(custom, ".claude.json")); !strings.Contains(got, "side-uuid") {
		t.Fatalf("real home's identity was relabelled: %s", got)
	}
	// Everything else still shares.
	if info, err := os.Lstat(filepath.Join(bondDir, "settings.json")); err != nil ||
		info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("non-denied entries must still be shared: %v", err)
	}
}

// A denylist that only governs new bond dirs is not a denylist: an existing
// directory keeps sharing what it was told to stop sharing. prepareBond retracts a
// link for a denied entry — and only a *link*, since a real file by that name is a
// private override, usually kae's own per-directory copy. Reached by
// shared_denylist_extra gaining an entry as much as by an upgrade, so this was a
// gap before .claude.json joined the list.
func TestPrepareBondRetractsLinkForDeniedEntry(t *testing.T) {
	app := testApp(t, nil)
	captureClaude(t, app, "main", mainToken)
	setupBondHome(t, app)
	pinID := paths.PinID(t.TempDir())
	bondDir := app.Paths.SharedDir(pinID, constants.ToolClaude)
	if err := os.MkdirAll(bondDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// The state an older kae left: the now-denied entry linked into the bond dir.
	realIdentity := filepath.Join(app.Env.Home, ".claude", ".claude.json")
	writeFile(t, realIdentity, `{"oauthAccount":{"accountUuid":"side-uuid"}}`)
	if err := os.Symlink(realIdentity, filepath.Join(bondDir, ".claude.json")); err != nil {
		t.Fatal(err)
	}
	// A real file for another denied entry must survive: it is a private override.
	writeFile(t, filepath.Join(bondDir, ".credentials.json"), `{"claudeAiOauth":{"accessToken":"private"}}`)

	if _, err := app.prepareBond(context.Background(), testBackend(t, app),
		constants.ToolClaude, "main", pinID); err != nil {
		t.Fatalf("prepareBond: %v", err)
	}

	info, err := os.Lstat(filepath.Join(bondDir, ".claude.json"))
	if err != nil {
		t.Fatalf("identity file missing after retraction: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("the stale link to the real home was not retracted")
	}
	if got := readFile(t, realIdentity); !strings.Contains(got, "side-uuid") {
		t.Fatalf("real home written through the stale link: %s", got)
	}
	if got := readFile(t, filepath.Join(bondDir, ".credentials.json")); !strings.Contains(got, mainToken) {
		t.Fatalf("kae's own per-directory credential must be refreshed, not retracted: %s", got)
	}
}

// TestPrepareBondCredentialComesFromSnapshotNotLiveHome pins the second defect
// fixed alongside the macOS pin gap: the materializers copied the *live* store,
// so binding an account that was not currently active seeded the directory with
// whichever credential happened to be live.
func TestPrepareBondCredentialComesFromSnapshotNotLiveHome(t *testing.T) {
	app := testApp(t, nil)
	captureClaude(t, app, "main", mainToken)
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
	captureClaude(t, app, "main", mainToken)
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
		captureClaude(t, app, "main", mainToken)
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
		captureClaude(t, app, "main", mainToken)
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

// TestSharedRebindRepairsAWipedBondDir pins the asymmetry that used to exist
// between the two mechanisms: the isolated re-bind ran preparePinConfig and
// rebuilt its store, while the shared one wrote only the credential and left a
// bond dir whose symlinks to the real home — settings, sessions — had been
// deleted exactly as broken as it found it.
func TestSharedRebindRepairsAWipedBondDir(t *testing.T) {
	app := overlayTestApp(t)
	seedClaude(t, app, mainToken, "main-uuid")
	code, out := captureStdout(t, func() int {
		return runCapture(context.Background(), app, commonOpts{Format: formatText}, constants.ToolClaude, "main")
	})
	mustExit(t, constants.ExitOK, code, out)

	chdirTemp(t)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	code, out = captureStdout(t, func() int {
		return runPin(context.Background(), app, commonOpts{Format: formatText}, "main", modeShared)
	})
	mustExit(t, constants.ExitOK, code, out)

	bond := app.Paths.SharedDir(paths.PinID(cwd), constants.ToolClaude)
	linked := filepath.Join(bond, "settings.json")
	if _, err := os.Lstat(linked); err != nil {
		t.Fatalf("premise: the bond dir must link the real home's settings: %v", err)
	}
	if err := os.RemoveAll(bond); err != nil {
		t.Fatal(err)
	}

	code, out = captureStdout(t, func() int {
		return runRebind(context.Background(), app, commonOpts{Format: formatText}, constants.ToolClaude, "main")
	})
	mustExit(t, constants.ExitOK, code, out)
	if _, err := os.Lstat(linked); err != nil {
		t.Fatalf("a re-bind must rebuild the wiped bond dir's links: %v", err)
	}
}
