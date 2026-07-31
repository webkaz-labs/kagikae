package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/freshness"
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

	// A stale plaintext copy the tool left in the directory: the keychain branch
	// removes it, and the identity must still be applied after that removal — the
	// restructure that made the identity unconditional runs it past this sweep.
	writeFile(t, filepath.Join(credDir, ".credentials.json"), `{"claudeAiOauth":{"accessToken":"stale"}}`)

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
	if _, err := os.Stat(filepath.Join(credDir, ".credentials.json")); !os.IsNotExist(err) {
		t.Fatalf("superseded plaintext copy not removed: %v", err)
	}
	if got := readFile(t, filepath.Join(credDir, ".claude.json")); !strings.Contains(got, "main-uuid") {
		t.Fatalf("identity not applied on the keychain path: %s", got)
	}
}

// A per-directory bind applies the identity cache as well as the credential, so
// the tool names the account it is actually authenticated as. Without this a bonded
// or isolated directory kept whichever account first ran there and `kae pin <tool>
// <account>` could not correct it — auth was right and the display was wrong.
func TestWriteDirCredentialAppliesIdentityCache(t *testing.T) {
	app := testApp(t, nil)
	captureClaude(t, app, "main", mainToken)
	credDir := t.TempDir()
	// The stale cache of whichever account ran here first, alongside a non-auth key
	// that must survive the patch.
	writeFile(t, filepath.Join(credDir, ".claude.json"),
		`{"oauthAccount":{"accountUuid":"side-uuid","emailAddress":"side@example.com"},"projects":{"/repo":{}}}`)

	if err := app.writeDirCredential(context.Background(), testBackend(t, app),
		constants.ToolClaude, "main", credDir); err != nil {
		t.Fatalf("writeDirCredential: %v", err)
	}
	got := readFile(t, filepath.Join(credDir, ".claude.json"))
	if !strings.Contains(got, "main-uuid") || strings.Contains(got, "side-uuid") {
		t.Fatalf("identity cache not switched to the bound account: %s", got)
	}
	if !strings.Contains(got, `"/repo"`) {
		t.Fatalf("only the identity pointer may move; mixed-state key lost: %s", got)
	}
}

// In bond mode the store links every entry of the real tool home into itself, so
// the identity target can be a link back *out* of the store. artifact.ApplyLive
// follows such a link deliberately — that sharing is what bond mode is for — which
// here would relabel the real home with this one directory's account. kae declines
// that single write and warns instead.
func TestWriteDirCredentialDeclinesIdentityThroughSharedLink(t *testing.T) {
	app := testApp(t, nil)
	captureClaude(t, app, "main", mainToken)
	credDir := t.TempDir()
	shared := filepath.Join(app.Env.Home, ".claude", ".claude.json")
	writeFile(t, shared, `{"oauthAccount":{"accountUuid":"side-uuid"}}`)
	if err := os.Symlink(shared, filepath.Join(credDir, ".claude.json")); err != nil {
		t.Fatal(err)
	}

	var werr error
	_, stderr := captureStderr(t, func() int {
		werr = app.writeDirCredential(context.Background(), testBackend(t, app),
			constants.ToolClaude, "main", credDir)
		return 0
	})
	if werr != nil {
		t.Fatalf("declining the identity write must not fail the bind: %v", werr)
	}
	if !strings.Contains(stderr, "identity cache") {
		t.Fatalf("declining to write it must be warned about: %q", stderr)
	}
	if got := readFile(t, shared); strings.Contains(got, "main-uuid") {
		t.Fatalf("the real home's identity cache was relabelled: %s", got)
	}
	// The credential half is unaffected: that store is private to the directory.
	if got := readFile(t, filepath.Join(credDir, ".credentials.json")); !strings.Contains(got, mainToken) {
		t.Fatalf("credential not materialized for the bind: %s", got)
	}
}

// The destructive branch, which needs to be intentional rather than incidental: a
// snapshot with no recorded identity applies as **absent**, so the bound
// directory's live cache is *removed* rather than left. That is the same choice the
// global switch makes — the tool refetches from the credential it can now see,
// while a kept cache is a label for an account that is no longer there — and every
// snapshot captured before kae tracked identities is in this state.
func TestWriteDirCredentialRemovesIdentityWithoutSnapshot(t *testing.T) {
	app := testApp(t, nil)
	// A credential but no identity: the mixed-state file does not exist at capture,
	// so the identity artifact is captured absent.
	seedClaudeOAuth(t, app, `{"accessToken":"`+mainToken+`"}`)
	code, out := captureStdout(t, func() int {
		return runCapture(context.Background(), app, commonOpts{Format: formatText}, "claude", "main")
	})
	mustExit(t, constants.ExitOK, code, out)
	credDir := t.TempDir()
	writeFile(t, filepath.Join(credDir, ".claude.json"),
		`{"oauthAccount":{"accountUuid":"side-uuid"},"projects":{"/repo":{}}}`)

	if err := app.writeDirCredential(context.Background(), testBackend(t, app),
		constants.ToolClaude, "main", credDir); err != nil {
		t.Fatalf("writeDirCredential: %v", err)
	}
	got := readFile(t, filepath.Join(credDir, ".claude.json"))
	if strings.Contains(got, "oauthAccount") {
		t.Fatalf("a snapshot with no identity must clear the live cache, not keep it: %s", got)
	}
	if !strings.Contains(got, `"/repo"`) {
		t.Fatalf("only the identity pointer may be removed; mixed-state key lost: %s", got)
	}
}

// A target that is a symlink whose destination is gone reports "not exist" exactly
// as an absent file does, and the two need opposite answers: the dangling link
// still leaves the store. Resolving its parent instead classified it as inside, and
// artifact.ApplyLive then refused it — turning a case kae can decline into a failed
// bind. The bind must survive, with the same decline warning as a live link.
func TestWriteDirCredentialDeclinesIdentityThroughDanglingLink(t *testing.T) {
	app := testApp(t, nil)
	captureClaude(t, app, "main", mainToken)
	credDir := t.TempDir()
	if err := os.Symlink(filepath.Join(app.Env.Home, ".claude", "gone.json"),
		filepath.Join(credDir, ".claude.json")); err != nil {
		t.Fatal(err)
	}

	var werr error
	_, stderr := captureStderr(t, func() int {
		werr = app.writeDirCredential(context.Background(), testBackend(t, app),
			constants.ToolClaude, "main", credDir)
		return 0
	})
	if werr != nil {
		t.Fatalf("a dangling shared link must not fail the bind: %v", werr)
	}
	if !strings.Contains(stderr, "identity cache") {
		t.Fatalf("the decline must be warned about: %q", stderr)
	}
	if got := readFile(t, filepath.Join(credDir, ".credentials.json")); !strings.Contains(got, mainToken) {
		t.Fatalf("credential not materialized for the bind: %s", got)
	}
}

// An identity write that fails for any other reason also must not fail the bind —
// the credential is already correct and an identity is a label. A malformed
// mixed-state file left behind by the tool is the reachable case. The warning that
// replaces the error must not carry the identity payload, which is PII.
func TestWriteDirCredentialIdentityFailureWarnsWithoutLeaking(t *testing.T) {
	app := testApp(t, nil)
	captureClaude(t, app, "main", mainToken)
	credDir := t.TempDir()
	writeFile(t, filepath.Join(credDir, ".claude.json"), `{"oauthAccount":`) // truncated

	var werr error
	_, stderr := captureStderr(t, func() int {
		werr = app.writeDirCredential(context.Background(), testBackend(t, app),
			constants.ToolClaude, "main", credDir)
		return 0
	})
	if werr != nil {
		t.Fatalf("an unwritable identity must not fail the bind: %v", werr)
	}
	if !strings.Contains(stderr, "identity cache") {
		t.Fatalf("the failure must be warned about: %q", stderr)
	}
	if strings.Contains(stderr, "main-uuid") || strings.Contains(stderr, "@example.com") {
		t.Fatalf("the identity payload must never reach a warning: %q", stderr)
	}
	if got := readFile(t, filepath.Join(credDir, ".credentials.json")); !strings.Contains(got, mainToken) {
		t.Fatalf("credential not materialized for the bind: %s", got)
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

// prunableApp is the shared setup of the sweep tests: a temp-HOME app on the
// keychain driver (darwin — a file store has nothing invisible to sweep) plus the
// pin id of a directory that is not the test's cwd, since the sweep takes the id
// from its caller.
func prunableApp(t *testing.T) (*App, string) {
	t.Helper()
	app := testApp(t, nil)
	app.Env.GOOS = "darwin"
	return app, paths.PinID(t.TempDir())
}

func mkdirs(t *testing.T, dirs ...string) {
	t.Helper()
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
}

// The teardown mirrors the write gate: a per-directory keychain item exists only
// where the adapter declares the item bindable, so that is exactly what a sweep
// removes. The stale store here is an isolated store for an account the directory
// no longer binds.
func TestPruneDirCredentialsRemovesSupersededItem(t *testing.T) {
	app, pinID := prunableApp(t)
	stale := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "side")
	bound := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "main")
	mkdirs(t, stale, bound)

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
	app, pinID := prunableApp(t)
	claudeStore := app.Paths.SharedDir(pinID, constants.ToolClaude)
	codexStore := app.Paths.SharedDir(pinID, constants.ToolCodex)
	mkdirs(t, claudeStore, codexStore)
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
	app, pinID := prunableApp(t)
	claudeStore := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "side")
	codexStore := app.Paths.IsolatedConfigDir(pinID, constants.ToolCodex, "side")
	mkdirs(t, claudeStore, codexStore)

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
	app, pinID := prunableApp(t)
	stale := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "side")
	mkdirs(t, stale)

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

// The read side of the same gate, and the reason it has to be there. The doctor
// sweep resolves a bound directory's credential to judge its freshness; for a tool
// whose keychain item does *not* move with its isolation variable, the item it
// would read is the **global** login. Reporting that as this directory's credential
// blames a healthy global login on a directory it has nothing to do with — and a
// stale one on every bound directory at once.
//
// Asserted through the subprocess seam rather than through the absence of a check,
// because on a non-darwin host every keychain read fails anyway and a missing check
// would prove nothing.
func TestDirCredentialFreshnessRefusesGlobalKeychainStore(t *testing.T) {
	app := testApp(t, nil)
	app.Env.GOOS = "darwin"
	credDir := t.TempDir()
	seedKeyringCodex(t, credDir)

	fake := &runnertest.Fake{Code: 0}
	var ok bool
	runner.With(fake, func() {
		_, ok = app.dirCredentialFreshness(context.Background(),
			dirStore{Tool: constants.ToolCodex, Dir: credDir})
	})
	if ok {
		t.Fatal("a store kae cannot bind per directory must not be judged")
	}
	if fake.Name != "" {
		t.Fatalf("the refusal must happen before the keychain is touched, ran %q %v", fake.Name, fake.Args)
	}
}

// The permitted counterpart: claude's item is namespaced by the config dir, so the
// sweep reads the item that directory owns — proven by the per-directory hash in
// the service name, the same assertion the write side makes.
func TestDirCredentialFreshnessReadsTheDirScopedItem(t *testing.T) {
	app := testApp(t, nil)
	app.Env.GOOS = "darwin"
	credDir := t.TempDir()

	fake := &runnertest.Fake{
		Stdout: `{"claudeAiOauth":{"accessToken":"a","refreshToken":"","expiresAt":1577836800000}}`,
		Code:   0,
	}
	var info freshness.Info
	var ok bool
	runner.With(fake, func() {
		info, ok = app.dirCredentialFreshness(context.Background(),
			dirStore{Tool: constants.ToolClaude, Dir: credDir})
	})
	if !ok || !info.Known {
		t.Fatalf("a dir-scoped store must be judged, got %+v (ok=%v)", info, ok)
	}
	if !strings.Contains(strings.Join(fake.Args, " "), sha8Of(credDir)) {
		t.Fatalf("freshness read the wrong item (not this directory's): %v", fake.Args)
	}
	// And the payload it read is the one that decided the verdict.
	if !needsRelogin(info, app.Now()) {
		t.Fatalf("an expiry of 2020 with no refresh token must read as needing a re-login: %+v", info)
	}
}
