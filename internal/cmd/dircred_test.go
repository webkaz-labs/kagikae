package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/config"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/paths"
	"github.com/webkaz-labs/kagikae/internal/runner"
	"github.com/webkaz-labs/kagikae/internal/secret"
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
			constants.ToolCodex, "main", credDir, false)
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
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", credDir), `{"claudeAiOauth":{"accessToken":"stale"}}`)

	fake := &runnertest.Fake{Code: 0}
	runner.With(fake, func() {
		if err := app.writeDirCredential(context.Background(), testBackend(t, app),
			constants.ToolClaude, "main", credDir, false); err != nil {
			t.Fatalf("writeDirCredential: %v", err)
		}
	})
	// Namespaced by the **credential** store, not by the config dir kae pointed the
	// tool's home at. Both are asserted: the item claude reads is the one named after
	// the credential variable, and writing to the config dir's name instead would put
	// the credential where nothing looks for it.
	args := strings.Join(fake.Args, " ")
	if !strings.Contains(args, sha8Of(app.credStoreDir(constants.ToolClaude, "main"))) {
		t.Fatalf("credential not written to the account's credential item: %v", fake.Args)
	}
	if strings.Contains(args, sha8Of(credDir)) {
		t.Fatalf("credential written to the config dir's item instead: %v", fake.Args)
	}
	if _, err := os.Stat(dirCredFile(app, constants.ToolClaude, "main", credDir)); !os.IsNotExist(err) {
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
		constants.ToolClaude, "main", credDir, false); err != nil {
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
			constants.ToolClaude, "main", credDir, false)
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
	if got := readFile(t, dirCredFile(app, constants.ToolClaude, "main", credDir)); !strings.Contains(got, mainToken) {
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
		constants.ToolClaude, "main", credDir, false); err != nil {
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
			constants.ToolClaude, "main", credDir, false)
		return 0
	})
	if werr != nil {
		t.Fatalf("a dangling shared link must not fail the bind: %v", werr)
	}
	if !strings.Contains(stderr, "identity cache") {
		t.Fatalf("the decline must be warned about: %q", stderr)
	}
	if got := readFile(t, dirCredFile(app, constants.ToolClaude, "main", credDir)); !strings.Contains(got, mainToken) {
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
			constants.ToolClaude, "main", credDir, false)
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
	if got := readFile(t, dirCredFile(app, constants.ToolClaude, "main", credDir)); !strings.Contains(got, mainToken) {
		t.Fatalf("credential not materialized for the bind: %s", got)
	}
}

// claudeOAuthPayload renders a claude credential whose access-token expiry is
// explicit, because that field is what orders two copies of one login: a refresh
// moves it forward, so the largest one is the copy that refreshed last and
// therefore the only one that can refresh again (docs/VALIDATION.md).
func claudeOAuthPayload(token string, expiresAt time.Time) string {
	return fmt.Sprintf(
		`{"claudeAiOauth":{"accessToken":%q,"refreshToken":"rt-%s","expiresAt":%d,`+
			`"refreshTokenExpiresAt":%d,"subscriptionType":"max"}}`,
		token, token, expiresAt.UnixMilli(), expiresAt.Add(27*24*time.Hour).UnixMilli(),
	)
}

// claudeIdentityFile renders the identity cache claude keeps beside a credential,
// with the email derived from the uuid — which is what most callers want, because
// they only need "this account" versus "a different account". Its keys match
// seedClaude's, so a store seeded with the same uuid attributes its credential to
// that account and a different uuid does not.
//
// One template for the payload, in boundIdentity: two fixtures for one shape drift,
// and this one had already lost `organizationUuid` — one of the three keys the
// comparison actually reads (claude's IdentityKeys) — so every test using it was
// comparing two payloads that agreed by omission on a third of the evidence.
func claudeIdentityFile(uuid string) string {
	return boundIdentity(uuid, uuid+"@example.com")
}

// captureClaudeAt captures an account whose credential carries a known expiry, so
// a test can put a bound directory's copy ahead of or behind the snapshot.
func captureClaudeAt(t *testing.T, app *App, accountName, token string, expiresAt time.Time) {
	t.Helper()
	seedClaude(t, app, token, accountName+"-uuid")
	writeFile(t, filepath.Join(app.Env.Home, ".claude", ".credentials.json"),
		claudeOAuthPayload(token, expiresAt))
	code, out := captureStdout(t, func() int {
		return runCapture(context.Background(), app, commonOpts{Format: formatText},
			constants.ToolClaude, accountName)
	})
	mustExit(t, constants.ExitOK, code, out)
}

// twoClaudeAccounts is overlayTestApp with a second profile and both accounts captured at
// one deadline — the state every test that moves a directory *between* accounts starts
// from. Extracted at the sixth copy; the deadline is returned because each of those tests
// then dates a store's copy relative to it.
func twoClaudeAccounts(t *testing.T) (*App, time.Time) {
	t.Helper()
	app := overlayTestApp(t)
	app.Config.Profiles["side"] = config.Profile{
		Accounts: map[string]string{constants.ToolClaude: "side"},
	}
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	captureClaudeAt(t, app, "side", sideToken, now.Add(time.Hour))
	return app, now
}

// bindClaudeHere binds a fresh temp cwd to profile's claude account in shared mode and
// returns the bound directory together with its store directory.
//
// The store directory is two things at once, which is what makes it the fixture the
// harvest tests need: it is the config dir a bind materializes into, *and* it is the
// config dir credStoreReaders returns for this binding. An identity cache seeded there
// is therefore evidence about the account's credential store — a genuine **reader** —
// while one in a bare t.TempDir() is evidence about nothing, because no binding points at
// it. Every one of these tests used a bare temp dir until 2026-08-08, which modelled a
// directory production never produces: all three callers of writeDirCredential run with a
// breadcrumb or a state.synced entry.
//
// It goes through kae's own bind rather than hand-writing the pin record, the fragment's
// [env] line and the mode-derived store path. Those three are the walk's entire input, and
// a fixture that writes them by hand is one edit away from the defect this package has
// measured before: the fake's value differs from the derived one, so the gate under test
// is never reached and the assertion holds for another reason.
func bindClaudeHere(t *testing.T, app *App, profile string) (dir, storeDir string) {
	t.Helper()
	accountName := app.Config.Profiles[profile].Accounts[constants.ToolClaude]
	if accountName == "" {
		t.Fatalf("profile %q binds no claude account, so this fixture would be evidence about nothing", profile)
	}
	dir = pinHereAs(t, app, profile, modeShared)
	storeDir = app.Paths.SharedDir(paths.PinID(dir), constants.ToolClaude)
	// Positive controls. The identity is what lets this reader speak at all: without one,
	// every "kae refused" assertion downstream would hold for missing evidence rather than
	// for the reason it names — the same trap pinIdentityApp guards against.
	if got := readFile(t, filepath.Join(storeDir, ".claude.json")); got == "" {
		t.Fatalf("the bind must leave an identity cache in %s for this directory to be evidence about anything", storeDir)
	}
	if got := readFile(t, dirCredFile(app, constants.ToolClaude, accountName, storeDir)); got == "" {
		t.Fatalf("the bind must materialize claude/%s's credential for this store to be read", accountName)
	}
	// A mark left here would suppress the report a later call in the same test owes.
	// Production builds an App per command and tests do not, which is how one bind's mark
	// came to silence the next one's message (measured 2026-08-04).
	if len(app.refusalReported) != 0 {
		t.Fatalf("this bind refused something, so later assertions would be suppressed: %v", app.refusalReported)
	}
	return dir, storeDir
}

// snapshotPayload reads what an account's snapshot currently holds for its
// credential — the side a harvest writes to.
func snapshotPayload(t *testing.T, app *App, be secret.Backend, tool, accountName string) string {
	t.Helper()
	return snapshotArtifact(t, app, be, tool, accountName, credentialArtifactName(tool))
}

// snapshotArtifact reads any one named artifact of a snapshot — the identity-only one
// beside the credential is the half a mis-attributed recapture relabels.
func snapshotArtifact(t *testing.T, app *App, be secret.Backend, tool, accountName, artifactName string) string {
	t.Helper()
	acc, found, err := account.Load(app.Paths.AccountDir(tool, accountName))
	if err != nil || !found {
		t.Fatalf("load snapshot %s/%s: found=%v err=%v", tool, accountName, found, err)
	}
	art := acc.Artifacts[artifactName]
	data, found, err := be.Get(context.Background(), art.SecretRef)
	if err != nil || !found {
		t.Fatalf("read snapshot payload %s: found=%v err=%v", art.SecretRef, found, err)
	}
	return string(data)
}

// recordedIdentity is account.toml's own Identity field — a different thing from the
// identity *artifact* above, and the one persistSnapshot builds from plan.Identity.
func recordedIdentity(t *testing.T, app *App, tool, accountName string) string {
	t.Helper()
	acc, found, err := account.Load(app.Paths.AccountDir(tool, accountName))
	if err != nil || !found {
		t.Fatalf("load snapshot %s/%s: found=%v err=%v", tool, accountName, found, err)
	}
	return acc.Identity
}

// Two identity payloads that are byte-identical and neither an account record agree
// about **nothing**, so they are not evidence that the store holds this account's
// credential. The reachable shape is `/oauthAccount` being `null`: capture records it,
// and `writeDirIdentity` then copies it into every bound store of that account — after
// which a shared store re-bound between two accounts that both recorded one would let
// the previous account's token be filed under the new name, undetectably, because the
// token is opaque.
//
// This is the case the decodability gate had to move **above** `identityDiffers` to
// reach: with the gate inside that branch, a byte-identical pair returned
// `identityDiffers == false` and fell through to the confirming path, so the harvest
// ran. Asserted on the harvest rather than on `doctor`, because `Conflicting` is false
// either way and the doctor half cannot tell the two implementations apart.
//
// The cost is stated where it is paid: the bind then overwrites a
// newer copy it declined to preserve, which is a destroyed login — loud, named, with
// the login that fixes it, and the same trade every refusal in this mechanism makes.
func TestWriteDirCredentialRefusesTwoIdentitiesThatAreNotAccountRecords(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	const nonRecord = `{"oauthAccount":null,"projects":{"/repo":{}}}`

	// Captured by hand rather than through captureClaudeAt, which seeds a well-formed
	// identity of its own: the whole point here is what the snapshot records when the
	// live cache is *not* an account record.
	writeFile(t, filepath.Join(app.Env.Home, ".claude", ".credentials.json"),
		claudeOAuthPayload(mainToken, now.Add(time.Hour)))
	writeFile(t, filepath.Join(app.Env.Home, ".claude.json"), nonRecord)
	code, out := captureStdout(t, func() int {
		return runCapture(ctx, app, commonOpts{Format: formatText}, constants.ToolClaude, "main")
	})
	mustExit(t, constants.ExitOK, code, out)

	_, storeDir := bindClaudeHere(t, app, "main")
	const refreshed = "sk-ant-oat01-MAIN-REFRESHED-dddd"
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", storeDir), claudeOAuthPayload(refreshed, now.Add(8*time.Hour)))
	// Written out even though the bind propagates the same non-record: the defect this
	// test reproduces needs the two sides to be **byte-identical**, and a fixture that
	// leaves that to another function's behaviour is one change away from testing the
	// weaker "they differ" case instead.
	writeFile(t, filepath.Join(storeDir, ".claude.json"), nonRecord)

	be := testBackend(t, app)
	// Positive first: the snapshot really does hold the non-record, or the refusal below
	// would be the "no identity is recorded" one and this test would prove nothing.
	acc, found, err := account.Load(app.Paths.AccountDir(constants.ToolClaude, "main"))
	if err != nil || !found || !acc.Artifacts["oauth_account"].Present {
		t.Fatalf("this test needs a recorded non-record identity: found=%v err=%v art=%+v",
			found, err, acc.Artifacts["oauth_account"])
	}

	_, stderr := captureStderr(t, func() int {
		if err := app.writeDirCredential(ctx, be, constants.ToolClaude, "main", storeDir, false); err != nil {
			t.Fatalf("writeDirCredential: %v", err)
		}
		return 0
	})

	if strings.Contains(stderr, "harvested") {
		t.Fatalf("two payloads that name no account must not confirm a harvest: %q", stderr)
	}
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); strings.Contains(got, refreshed) {
		t.Fatalf("the newer copy was harvested on the strength of agreeing about nothing: %s", got)
	}
	if !strings.Contains(stderr, "kae cannot read the identity records it would compare") {
		t.Fatalf("the refusal must name its own reason: %q", stderr)
	}
	if strings.Contains(stderr, refreshed) {
		t.Fatalf("a credential must never reach a message: %q", stderr)
	}
}

// The defect this whole harvest exists for: the tool refreshes the copy *inside* a
// bound directory, in place, and claude's refresh token is single-use — so writing
// the account snapshot over that copy does not regress the directory to an older
// login, it logs it out, hours later, with every offline check green
// (docs/VALIDATION.md). The bind must take the newer copy into the snapshot first
// and then write *that*.
//
// The evidence that the copy is this account's comes from the directories that read the
// store, so the fixture binds one: the identity cache the bind leaves in that directory
// is what confirms the harvest. Its opposite number is
// TestWriteDirCredentialKeepsANewerCopyItCannotAttribute, which is the same store with no
// reader at all — the pair is what separates "the reader gate" from "the harvest".
func TestWriteDirCredentialHarvestsNewerLiveCredential(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	_, storeDir := bindClaudeHere(t, app, "main")
	// The tool refreshed the account's copy in place, in the store every directory bound
	// to claude/main reads.
	const refreshed = "sk-ant-oat01-MAIN-REFRESHED-cccc"
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", storeDir), claudeOAuthPayload(refreshed, now.Add(8*time.Hour)))
	// A capture time that has moved on, so the recorded one is proof this snapshot
	// was rewritten rather than merely unchanged.
	later := now.Add(2 * time.Hour)
	app.Now = func() time.Time { return later }

	be := testBackend(t, app)
	_, stderr := captureStderr(t, func() int {
		if err := app.writeDirCredential(ctx, be, constants.ToolClaude, "main", storeDir, false); err != nil {
			t.Fatalf("writeDirCredential: %v", err)
		}
		return 0
	})

	if got := readFile(t, dirCredFile(app, constants.ToolClaude, "main", storeDir)); !strings.Contains(got, refreshed) {
		t.Fatalf("the bind overwrote the newer live credential: %s", got)
	}
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); !strings.Contains(got, refreshed) {
		t.Fatalf("the newer credential was not harvested into the snapshot: %s", got)
	}
	if !strings.Contains(stderr, "harvested") {
		t.Fatalf("a harvest must be reported: %q", stderr)
	}
	if strings.Contains(stderr, refreshed) {
		t.Fatalf("a credential must never reach a message: %q", stderr)
	}
	acc, _, err := account.Load(app.Paths.AccountDir(constants.ToolClaude, "main"))
	if err != nil || !acc.CapturedAt.Equal(later) {
		t.Fatalf("captured_at must follow the harvested payload: %v (err %v)", acc.CapturedAt, err)
	}
}

// The other direction, which must stay cheap and silent: the snapshot is the newer
// copy, so the bind writes it as it always did. Without this the harvest would be
// free to run backwards and overwrite a good snapshot from a directory nobody has
// opened in weeks.
func TestWriteDirCredentialKeepsSnapshotWhenLiveIsOlder(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(8*time.Hour))
	_, credDir := bindClaudeHere(t, app, "main")
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", credDir),
		claudeOAuthPayload("sk-ant-oat01-MAIN-OLD-dddd", now.Add(time.Hour)))

	be := testBackend(t, app)
	_, stderr := captureStderr(t, func() int {
		if err := app.writeDirCredential(ctx, be, constants.ToolClaude, "main", credDir, false); err != nil {
			t.Fatalf("writeDirCredential: %v", err)
		}
		return 0
	})

	if got := readFile(t, dirCredFile(app, constants.ToolClaude, "main", credDir)); !strings.Contains(got, mainToken) {
		t.Fatalf("the snapshot must be applied when it is the newer copy: %s", got)
	}
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); !strings.Contains(got, mainToken) {
		t.Fatalf("the older live copy must not reach the snapshot: %s", got)
	}
	if strings.Contains(stderr, "harvested") {
		t.Fatalf("nothing was harvested, so nothing may be reported: %q", stderr)
	}
}

// Attribution is the guard that makes the harvest safe, because a store can hold a
// credential that is not the account's at all. The reachable shape is a **login as
// somebody else inside a bound directory**: the directory binds claude/main, the user runs
// `/login` there as side, and the account's store now holds side's credential — usually
// the newer one, since it is the one in daily use — while the directory's identity cache
// says side too. Harvesting that would file side's token under main's name, after which
// nothing offline can tell: the token is opaque, so live, snapshot and doctor all agree on
// a label that is simply wrong.
//
// Note which state this is *not*, since it used to be: a re-bind of this directory from
// one account to another. That one is now the case the model deliberately declines to
// judge (TestRunPinRebindBetweenAccountsPreservesTheTargetsLiveCredential) — the directory
// being re-bound is not yet a reader of the new account's store, so its stale label says
// nothing about it.
func TestWriteDirCredentialRefusesToHarvestAnotherAccountsCredential(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	_, storeDir := bindClaudeHere(t, app, "main")
	// Logged in as side inside the bound directory: the store's copy and the reader's
	// label both name side.
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", storeDir), claudeOAuthPayload(sideToken, now.Add(8*time.Hour)))
	writeFile(t, filepath.Join(storeDir, ".claude.json"), claudeIdentityFile("side-uuid"))

	be := testBackend(t, app)
	_, stderr := captureStderr(t, func() int {
		if err := app.writeDirCredential(ctx, be, constants.ToolClaude, "main", storeDir, false); err != nil {
			t.Fatalf("writeDirCredential: %v", err)
		}
		return 0
	})

	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); strings.Contains(got, sideToken) {
		t.Fatalf("another account's token was filed under this one: %s", got)
	}
	if !strings.Contains(stderr, "not harvesting") {
		t.Fatalf("declining to harvest must be said out loud: %q", stderr)
	}
	// The bind still does its job: the directory ends up on the account it names.
	if got := readFile(t, dirCredFile(app, constants.ToolClaude, "main", storeDir)); !strings.Contains(got, mainToken) {
		t.Fatalf("the bind must still apply the bound account: %s", got)
	}
}

// **The first bind of a directory has no evidence to attribute from, and the store it
// would overwrite belongs to the account, not to the directory.** writeDirCredential's
// comment carries why attribution refuses there; what this test adds is the reason it went
// unnoticed — every test above binds a directory first, so none of them is a first bind.
// Measured end to end 2026-08-08: use claude in one worktree, bind a second, and both are
// dead up to 8h later with nothing left for doctor to compare.
//
// Deliberately **not** bound, which is what makes it the opposite number of
// TestWriteDirCredentialHarvestsNewerLiveCredential: no directory reads this account's
// credential store yet, so nothing can say whose login the copy is. Missing evidence here
// is the absence of a *reader*, not the absence of a file in this directory — seeding a
// `.claude.json` beside the store would change nothing, because a directory no binding
// points at is evidence about nothing.
func TestWriteDirCredentialKeepsANewerCopyItCannotAttribute(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	credDir := t.TempDir() // an unbound config dir: nothing reads the account's store
	const refreshed = "sk-ant-oat01-MAIN-REFRESHED-cccc"
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", credDir),
		claudeOAuthPayload(refreshed, now.Add(8*time.Hour)))

	be := testBackend(t, app)
	_, stderr := captureStderr(t, func() int {
		if err := app.writeDirCredential(ctx, be, constants.ToolClaude, "main", credDir, false); err != nil {
			t.Fatalf("writeDirCredential: %v", err)
		}
		return 0
	})

	if got := readFile(t, dirCredFile(app, constants.ToolClaude, "main", credDir)); !strings.Contains(got, refreshed) {
		t.Fatalf("the only copy that can still refresh was destroyed: %s", got)
	}
	// Kept is not harvested: the copy stays where it is and is *not* filed under this
	// account, because the reason kae kept it is that it could not tell whose it is.
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); strings.Contains(got, refreshed) {
		t.Fatalf("an unattributable copy must not be filed under this account: %s", got)
	}
	if !strings.Contains(stderr, "kept it rather than replacing it") {
		t.Fatalf("keeping the copy must be said out loud: %q", stderr)
	}
	if strings.Contains(stderr, "this write replaces it") {
		t.Fatalf("the overwrite wording must not survive here: %q", stderr)
	}
	// No remedy at this site by design: it holds a store path, not the bound directory a
	// login would have to happen in. The pin-level pass carries the remedy, and when it
	// speaks this message is suppressed — asserted by the pin-level tests.
	if strings.Contains(stderr, "kae relogin") || strings.Contains(stderr, "kae add --no-login") {
		t.Fatalf("the chokepoint must not name a remedy for a store path: %q", stderr)
	}
	if strings.Contains(stderr, refreshed) {
		t.Fatalf("a credential must never reach a message: %q", stderr)
	}
	// No label either, and this is the load-bearing half. kae's own label is exactly the
	// evidence the next bind's attribution reads, so writing it here let `kae pin` again
	// confirm against a cache kae had planted and harvest the copy this bind refused —
	// measured 2026-08-08, filing another account's token under this one's name. Absence is
	// the honest record; the next cache here is the tool's own.
	if _, err := os.Stat(filepath.Join(credDir, ".claude.json")); !os.IsNotExist(err) {
		t.Fatalf("kae must not plant the label it would later read as attribution (err %v)", err)
	}
}
