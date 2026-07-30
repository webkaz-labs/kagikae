package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/keychain"
	"github.com/webkaz-labs/kagikae/internal/paths"
	"github.com/webkaz-labs/kagikae/internal/runner"
	"github.com/webkaz-labs/kagikae/internal/testutil/runnertest"
)

// seedKeyringCodex configures codex's keyring store inside a bound directory, the
// shape whose credential kae will not bind per directory.
func seedKeyringCodex(t *testing.T, dir string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, "config.toml"), "cli_auth_credentials_store = \"keyring\"\n")
}

// TestWriteDirCredentialRefusesGlobalKeychainStore is the guard on the most
// destructive thing this code could do: writing a keychain item for a bound
// directory when the adapter has not declared that the item moves with the
// isolation variable. codex's `Codex Auth` item is shared by every codex home
// (scoped by an account kae would have to derive for the bond dir), so writing it
// here touches a store the bound directory does not own.
func TestWriteDirCredentialRefusesGlobalKeychainStore(t *testing.T) {
	app := testApp(t, nil)
	app.Env.GOOS = "darwin"
	credDir := t.TempDir()
	seedKeyringCodex(t, credDir)

	fake := &runnertest.Fake{Code: 0}
	var err error
	runner.With(fake, func() {
		err = app.writeDirCredential(context.Background(), testBackend(t, app),
			constants.ToolCodex, "main", credDir)
	})

	if !errors.Is(err, errGlobalCredentialStore) {
		t.Fatalf("a global credential store must be refused, got %v", err)
	}
	// Refused before anything ran: no read, no write, and above all no delete of
	// the global item.
	if fake.Name != "" {
		t.Fatalf("refusal must not touch the keychain, ran %q %v", fake.Name, fake.Args)
	}
}

// The per-directory case is the opposite: claude's item is namespaced by the
// config dir, so it is written.
func TestWriteDirCredentialWritesDirScopedKeychainStore(t *testing.T) {
	app := testApp(t, nil)
	app.Env.GOOS = "darwin"
	payload := `{"claudeAiOauth":{"accessToken":"` + mainToken + `","subscriptionType":"max"}}`
	runner.With(&runnertest.Fake{Stdout: payload, Code: 0}, func() {
		captureClaude(t, app, "main", mainToken)
	})
	credDir := t.TempDir()

	fake := &runnertest.Fake{Code: 0}
	runner.With(fake, func() {
		if err := app.writeDirCredential(context.Background(), testBackend(t, app),
			constants.ToolClaude, "main", credDir); err != nil {
			t.Fatalf("writeDirCredential: %v", err)
		}
	})
	if !strings.Contains(strings.Join(fake.Args, " "), sha8Of(credDir)) {
		t.Fatalf("credential not written to the per-directory item: %v", fake.Args)
	}
}

// A whole-profile bind must not fail over one tool whose credential store cannot
// be scoped to a directory: the others still bind, and that tool's settings and
// sessions are still isolated. Only the credential is shared, and the warning
// says so.
func TestPrepareBondWarnsOnGlobalStoreAndKeepsBinding(t *testing.T) {
	app := testApp(t, nil)
	app.Env.GOOS = "darwin"
	cwd := t.TempDir()
	pinID := paths.PinID(cwd)
	// codex's real home carries the keyring setting; prepareBond symlinks
	// config.toml into the bond dir before the credential step, which is how the
	// bound directory ends up resolving the global store.
	seedKeyringCodex(t, filepath.Join(app.Env.Home, ".codex"))

	// The whole-profile path is prepareIsolationDirs; prepareBond itself reports
	// the limitation and the policy of tolerating it lives one level up.
	ctx := context.Background()
	be := testBackend(t, app)
	entries := app.bondIsolationEntries([]runTarget{{Tool: constants.ToolCodex, Account: "main"}}, pinID)
	bondDir := app.Paths.SharedDir(pinID, constants.ToolCodex)

	fake := &runnertest.Fake{Code: 0}
	var err error
	runner.With(fake, func() {
		err = app.prepareIsolationDirs(modeShared, entries, func(tool, account string) (string, error) {
			return app.prepareBond(ctx, be, tool, account, pinID)
		})
	})
	if err != nil {
		t.Fatalf("a global credential store must warn, not fail the bind: %v", err)
	}
	if fake.Name != "" {
		t.Fatalf("the global keychain item must be left alone, ran %q %v", fake.Name, fake.Args)
	}
	// The bond dir is still built, so the tool's non-auth state is isolated.
	if _, statErr := os.Lstat(filepath.Join(bondDir, "config.toml")); statErr != nil {
		t.Fatalf("bond dir must still be materialized: %v", statErr)
	}
	// And no credential file was left as a consolation prize: codex reads the
	// keyring, so a file here would be a plaintext secret nothing reads.
	if _, statErr := os.Stat(filepath.Join(bondDir, "auth.json")); !os.IsNotExist(statErr) {
		t.Error("no credential file may be written for a tool that reads a global keyring")
	}
}

// The teardown mirrors the write gate: a per-directory keychain item exists only
// where the adapter declares the item bindable, so that is exactly what a sweep
// removes. The stale store here is an isolated store for an account the directory
// no longer binds.
func TestPruneDirCredentialsRemovesSupersededItem(t *testing.T) {
	app := testApp(t, nil)
	app.Env.GOOS = "darwin"
	pinID := paths.PinID(t.TempDir())
	stale := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "side")
	bound := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "main")
	for _, dir := range []string{stale, bound} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	fake := &runnertest.Fake{Code: 0}
	var lines []string
	runner.With(fake, func() {
		lines = app.pruneDirCredentials(context.Background(), pinID, "", map[string]bool{bound: true})
	})

	args := strings.Join(fake.Args, " ")
	if !strings.Contains(args, "delete-generic-password") || !strings.Contains(args, sha8Of(stale)) {
		t.Fatalf("the superseded item was not deleted: %q %v", fake.Name, fake.Args)
	}
	if strings.Contains(args, sha8Of(bound)) {
		t.Fatalf("the bound store's item must survive: %v", fake.Args)
	}
	if len(lines) != 1 || !strings.Contains(lines[0], stale) {
		t.Fatalf("the removal must be reported: %v", lines)
	}
}

// Two stores a sweep must never touch: one the binding still points at, and one
// whose credential is not a bindable keychain item at all. codex's keyring item is
// the second case — kae never wrote it for this directory, so removing it would
// delete a login kae does not own (the same asymmetry writeDirCredential refuses).
func TestPruneDirCredentialsSkipsBoundAndUnbindableStores(t *testing.T) {
	app := testApp(t, nil)
	app.Env.GOOS = "darwin"
	pinID := paths.PinID(t.TempDir())
	claudeStore := app.Paths.SharedDir(pinID, constants.ToolClaude)
	codexStore := app.Paths.SharedDir(pinID, constants.ToolCodex)
	for _, dir := range []string{claudeStore, codexStore} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	seedKeyringCodex(t, codexStore)

	fake := &runnertest.Fake{Code: 0}
	var lines []string
	runner.With(fake, func() {
		lines = app.pruneDirCredentials(context.Background(), pinID, "", map[string]bool{claudeStore: true})
	})

	if fake.Name != "" {
		t.Fatalf("nothing was superseded, yet the keychain was touched: %q %v", fake.Name, fake.Args)
	}
	if len(lines) != 0 {
		t.Fatalf("nothing to report: %v", lines)
	}
}

// onlyTool is what keeps a single-tool re-bind from sweeping a sibling tool's
// store, which the same fragment still binds.
func TestPruneDirCredentialsHonorsToolFilter(t *testing.T) {
	app := testApp(t, nil)
	app.Env.GOOS = "darwin"
	pinID := paths.PinID(t.TempDir())
	claudeStore := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "side")
	codexStore := app.Paths.IsolatedConfigDir(pinID, constants.ToolCodex, "side")
	for _, dir := range []string{claudeStore, codexStore} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	fake := &runnertest.Fake{Code: 0}
	runner.With(fake, func() {
		app.pruneDirCredentials(context.Background(), pinID, constants.ToolCodex, nil)
	})
	if strings.Contains(strings.Join(fake.Args, " "), sha8Of(claudeStore)) {
		t.Fatalf("a sibling tool's store must be out of scope: %v", fake.Args)
	}
}

// The delete primitive treats "no such item" as success, so the sweep probes
// first: a store that never had an item must not be announced as cleaned up, and
// nothing should be deleted for it either.
func TestPruneDirCredentialsReportsNothingWhenNoItemExists(t *testing.T) {
	app := testApp(t, nil)
	app.Env.GOOS = "darwin"
	pinID := paths.PinID(t.TempDir())
	stale := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "side")
	if err := os.MkdirAll(stale, 0o700); err != nil {
		t.Fatal(err)
	}

	fake := &runnertest.Fake{Stderr: "security: " + keychain.NotFoundMarker, Code: 44}
	var lines []string
	runner.With(fake, func() {
		lines = app.pruneDirCredentials(context.Background(), pinID, "", nil)
	})

	if len(lines) != 0 {
		t.Fatalf("an absent item must not be reported as removed: %v", lines)
	}
	if args := strings.Join(fake.Args, " "); strings.Contains(args, "delete-generic-password") {
		t.Fatalf("nothing to delete, yet a delete ran: %v", fake.Args)
	}
}
