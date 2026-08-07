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
	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/config"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/freshness"
	"github.com/webkaz-labs/kagikae/internal/keychain"
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
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", credDir), `{"claudeAiOauth":{"accessToken":"stale"}}`)

	fake := &runnertest.Fake{Code: 0}
	runner.With(fake, func() {
		if err := app.writeDirCredential(context.Background(), testBackend(t, app),
			constants.ToolClaude, "main", credDir); err != nil {
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
// The cost is stated where it is paid (docs/RELEASE.md): the bind then overwrites a
// newer copy it declined to preserve, which is a destroyed login — loud, named, with
// the login that fixes it, and the same trade every refusal in this mechanism makes.
func TestWriteDirCredentialRefusesTwoIdentitiesThatAreNotAccountRecords(t *testing.T) {
	app := testApp(t, nil)
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

	credDir := t.TempDir()
	const refreshed = "sk-ant-oat01-MAIN-REFRESHED-dddd"
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", credDir), claudeOAuthPayload(refreshed, now.Add(8*time.Hour)))
	writeFile(t, filepath.Join(credDir, ".claude.json"), nonRecord)

	be := testBackend(t, app)
	// Positive first: the snapshot really does hold the non-record, or the refusal below
	// would be the "no identity is recorded" one and this test would prove nothing.
	acc, found, err := account.Load(app.Paths.AccountDir(constants.ToolClaude, "main"))
	if err != nil || !found || !acc.Artifacts["oauth_account"].Present {
		t.Fatalf("this test needs a recorded non-record identity: found=%v err=%v art=%+v",
			found, err, acc.Artifacts["oauth_account"])
	}

	_, stderr := captureStderr(t, func() int {
		if err := app.writeDirCredential(ctx, be, constants.ToolClaude, "main", credDir); err != nil {
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
func TestWriteDirCredentialHarvestsNewerLiveCredential(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	credDir := t.TempDir()
	const refreshed = "sk-ant-oat01-MAIN-REFRESHED-cccc"
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", credDir), claudeOAuthPayload(refreshed, now.Add(8*time.Hour)))
	writeFile(t, filepath.Join(credDir, ".claude.json"), claudeIdentityFile("main-uuid"))
	// A capture time that has moved on, so the recorded one is proof this snapshot
	// was rewritten rather than merely unchanged.
	later := now.Add(2 * time.Hour)
	app.Now = func() time.Time { return later }

	be := testBackend(t, app)
	_, stderr := captureStderr(t, func() int {
		if err := app.writeDirCredential(ctx, be, constants.ToolClaude, "main", credDir); err != nil {
			t.Fatalf("writeDirCredential: %v", err)
		}
		return 0
	})

	if got := readFile(t, dirCredFile(app, constants.ToolClaude, "main", credDir)); !strings.Contains(got, refreshed) {
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
	app := testApp(t, nil)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(8*time.Hour))
	credDir := t.TempDir()
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", credDir),
		claudeOAuthPayload("sk-ant-oat01-MAIN-OLD-dddd", now.Add(time.Hour)))
	writeFile(t, filepath.Join(credDir, ".claude.json"), claudeIdentityFile("main-uuid"))

	be := testBackend(t, app)
	_, stderr := captureStderr(t, func() int {
		if err := app.writeDirCredential(ctx, be, constants.ToolClaude, "main", credDir); err != nil {
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

// Attribution is the guard that makes the harvest safe, and the shared mechanism is
// why it is needed: its store is one directory per pin×tool, so re-binding it to
// another account finds the previous account's credential there — usually the
// *newer* one, since it is the one in daily use. Harvesting that would file account
// B's token under account A's name, after which nothing offline can tell.
func TestWriteDirCredentialRefusesToHarvestAnotherAccountsCredential(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	credDir := t.TempDir()
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", credDir), claudeOAuthPayload(sideToken, now.Add(8*time.Hour)))
	writeFile(t, filepath.Join(credDir, ".claude.json"), claudeIdentityFile("side-uuid"))

	be := testBackend(t, app)
	_, stderr := captureStderr(t, func() int {
		if err := app.writeDirCredential(ctx, be, constants.ToolClaude, "main", credDir); err != nil {
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
	if got := readFile(t, dirCredFile(app, constants.ToolClaude, "main", credDir)); !strings.Contains(got, mainToken) {
		t.Fatalf("the bind must still apply the bound account: %s", got)
	}
}

// **The first bind of a directory has no identity cache to attribute from, and the
// store it would overwrite belongs to the account, not to the directory.** Every test
// above seeds `.claude.json` first, which is why this went unnoticed: on a real first
// bind the config dir was created moments earlier, a shared bind links no `.claude.json`
// by design, and writeDirIdentity runs after the credential. So attribution refuses for
// missing evidence — and refusing used to mean overwriting anyway, which under single-use
// rotation logs out *every* directory bound to that account. Measured end to end
// 2026-08-08 (use claude in one worktree, bind a second, both dead up to 8h later) and
// silent afterwards: with the newer copy gone there is nothing left for doctor to compare.
func TestWriteDirCredentialKeepsANewerCopyItCannotAttribute(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	credDir := t.TempDir() // a fresh config dir: deliberately NO .claude.json
	const refreshed = "sk-ant-oat01-MAIN-REFRESHED-cccc"
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", credDir),
		claudeOAuthPayload(refreshed, now.Add(8*time.Hour)))

	be := testBackend(t, app)
	_, stderr := captureStderr(t, func() int {
		if err := app.writeDirCredential(ctx, be, constants.ToolClaude, "main", credDir); err != nil {
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
	// The bind is otherwise complete: only the credential write is skipped. The identity
	// cache still lands, because it is a label the directory needs like any other binding
	// — and because a version of this fix that skipped it left the directory permanently
	// unattributable, with `identity_drift` blind and no later bind able to harvest.
	if got := readFile(t, filepath.Join(credDir, ".claude.json")); !strings.Contains(got, "main-uuid") {
		t.Fatalf("keeping the credential must not skip the identity label: %s", got)
	}
}

// The other side of the same seam, and the reason the guard is keyed on *why* the
// harvest refused rather than on the refusal alone: positive evidence that the copy is
// somebody else's means this account's credential is elsewhere and fine, so the bind
// must still take effect. Keeping here would silently leave the directory running an
// account the user just bound away from.
func TestWriteDirCredentialStillReplacesAConflictingCopy(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	credDir := t.TempDir()
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", credDir),
		claudeOAuthPayload(sideToken, now.Add(8*time.Hour)))
	writeFile(t, filepath.Join(credDir, ".claude.json"), claudeIdentityFile("side-uuid"))

	be := testBackend(t, app)
	_, stderr := captureStderr(t, func() int {
		if err := app.writeDirCredential(ctx, be, constants.ToolClaude, "main", credDir); err != nil {
			t.Fatalf("writeDirCredential: %v", err)
		}
		return 0
	})

	if got := readFile(t, dirCredFile(app, constants.ToolClaude, "main", credDir)); !strings.Contains(got, mainToken) {
		t.Fatalf("a conflicting copy must still be replaced by the bound account: %s", got)
	}
	if strings.Contains(stderr, "kept it rather than replacing it") {
		t.Fatalf("positive evidence must not take the keep branch: %q", stderr)
	}
}

// The third refusal is deliberately NOT on the keep branch, and this pins that choice so
// nobody widens it by reading the comment as "kae keeps what it cannot judge". A payload
// kae can neither read nor date may be a working login in a shape kae has not been
// taught — but keeping it would make a corrupted account store unrepairable by `kae pin`,
// and manual deletion the only escape. The trade-off is docs/ROADMAP.md's to settle; the
// behaviour here is the one that shipped.
func TestWriteDirCredentialStillReplacesAnUnreadableCopy(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	credDir := t.TempDir()
	// Structurally valid for the artifact layer, but carrying no field kae can date.
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", credDir),
		`{"claudeAiOauth":{"accessTokenRenamedUpstream":"live-and-working"}}`)
	writeFile(t, filepath.Join(credDir, ".claude.json"), claudeIdentityFile("main-uuid"))

	be := testBackend(t, app)
	_, stderr := captureStderr(t, func() int {
		if err := app.writeDirCredential(ctx, be, constants.ToolClaude, "main", credDir); err != nil {
			t.Fatalf("writeDirCredential: %v", err)
		}
		return 0
	})

	if got := readFile(t, dirCredFile(app, constants.ToolClaude, "main", credDir)); !strings.Contains(got, mainToken) {
		t.Fatalf("the unreadable arm still applies the snapshot: %s", got)
	}
	if strings.Contains(stderr, "kept it rather than replacing it") {
		t.Fatalf("only the attribution refusal keeps the copy: %q", stderr)
	}
	if !strings.Contains(stderr, "this write replaces it") {
		t.Fatalf("replacing a copy kae cannot judge must be said out loud: %q", stderr)
	}
}

// A tombstone — what claude writes after a refresh it could not complete — is a
// fully-formed payload with both tokens blanked, so presence cannot stand in for
// "there is a login here". Harvesting one would overwrite a working snapshot with a
// dead credential, which no later kae command could undo.
func TestWriteDirCredentialDoesNotHarvestTombstone(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	credDir := t.TempDir()
	// Blank tokens with a deadline still in the future: only Revoked separates this
	// from a healthy copy.
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", credDir), fmt.Sprintf(
		`{"claudeAiOauth":{"accessToken":"","refreshToken":"","expiresAt":%d,"refreshTokenExpiresAt":%d}}`,
		now.Add(8*time.Hour).UnixMilli(), now.Add(27*24*time.Hour).UnixMilli(),
	))
	writeFile(t, filepath.Join(credDir, ".claude.json"), claudeIdentityFile("main-uuid"))

	be := testBackend(t, app)
	if err := app.writeDirCredential(ctx, be, constants.ToolClaude, "main", credDir); err != nil {
		t.Fatalf("writeDirCredential: %v", err)
	}
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); !strings.Contains(got, mainToken) {
		t.Fatalf("a tombstone was harvested over a usable snapshot: %s", got)
	}
}

// The harvest is claude-only, and stays that way until another tool's rotation is
// measured: without that measurement "the newest copy" is a guess, and a wrong
// guess destroys the working credential (docs/ROADMAP.md § Rotation is measured for
// claude only). codex's file store is copied into bound directories exactly like
// claude's, so it is the one that would break first if this gate were dropped.
func TestWriteDirCredentialDoesNotHarvestUnmeasuredTool(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	seedCodex(t, app, "codex-main-token")
	code, out := captureStdout(t, func() int {
		return runCapture(ctx, app, commonOpts{Format: formatText}, constants.ToolCodex, "main")
	})
	mustExit(t, constants.ExitOK, code, out)
	credDir := t.TempDir()
	// A JWT-dated codex credential far ahead of the snapshot's: it would win any
	// expiry comparison, and must still not be harvested.
	writeFile(t, filepath.Join(credDir, "auth.json"),
		`{"tokens":{"access_token":"`+jwtWithExp(app.Now().Add(9*time.Hour))+`"}}`)

	be := testBackend(t, app)
	if err := app.writeDirCredential(ctx, be, constants.ToolCodex, "main", credDir); err != nil {
		t.Fatalf("writeDirCredential: %v", err)
	}
	if got := snapshotPayload(t, app, be, constants.ToolCodex, "main"); !strings.Contains(got, "codex-main-token") {
		t.Fatalf("an unmeasured tool's live copy was harvested: %s", got)
	}
}

// The harvest inside writeDirCredential can only see the store it is writing, and
// the operations that hurt most bind the directory to a **different** store: a
// `-s` ↔ `-i` toggle and an isolated re-bind. Without a pin-level pass, the new
// store is built from the account snapshot while the copy the tool refreshed sits in
// the old one — so the directory the user just bound holds the credential rotation
// has already invalidated, with every offline check green and no message. Found by
// review (execution-type, 2026-08-04) after the first version of this fix shipped
// only the chokepoint half.
//
// This also pins the wiring the pass depends on: the superseded store here is the
// *shared* one, whose account is recorded nowhere but the binding being replaced, so
// it fails if `prev` is ever read after the fragment is rewritten.
func TestRunPinModeToggleHarvestsTheSupersededStoreFirst(t *testing.T) {
	app := overlayTestApp(t)
	chdirTemp(t)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))

	if code := runPin(ctx, app, opts, "main", modeShared); code != constants.ExitOK {
		t.Fatalf("pin --shared exit %d", code)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	pinID := paths.PinID(cwd)
	shared := app.Paths.SharedDir(pinID, constants.ToolClaude)
	// The tool refreshed the credential in the shared store, in place.
	const refreshed = "sk-ant-oat01-MAIN-REFRESHED-cccc"
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", shared), claudeOAuthPayload(refreshed, now.Add(8*time.Hour)))

	if code := runPin(ctx, app, opts, "main", modeIsolated); code != constants.ExitOK {
		t.Fatalf("pin --isolated exit %d", code)
	}

	be := testBackend(t, app)
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); !strings.Contains(got, refreshed) {
		t.Fatalf("the superseded store's newer credential was not harvested: %s", got)
	}
	isolated := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "main")
	if got := readFile(t, dirCredFile(app, constants.ToolClaude, "main", isolated)); !strings.Contains(got, refreshed) {
		t.Fatalf("the newly bound store holds a credential that can no longer refresh: %s", got)
	}
}

// refusalLines counts the stderr lines that report a credential kae did not harvest.
func refusalLines(stderr string) int {
	n := 0
	for _, line := range strings.Split(stderr, "\n") {
		if strings.Contains(line, "not harvesting") || strings.Contains(line, "could not preserve") {
			n++
		}
	}
	return n
}

// What a refusal *says*, which is a contract of its own: nine mutations to the
// reporting survived the suite before this test existed (execution-type review, round
// 2), including "print the login remedy for every refusal" — the one case where kae's
// own comment says a login would mint a chain invalidating what it just harvested.
//
// Three rules, all measured through `kae pin` rather than through the helpers:
// exactly one message per refused store (the store being written is looked at by both
// the pin-level pass and the write path, and one refusal read as two problems); the
// remedy appears only when the refusal is *missing evidence*; and a leftover store the
// command does not touch is not mentioned at all.
func TestRunPinReportsOneRefusalPerStoreWithTheRightRemedy(t *testing.T) {
	app := overlayTestApp(t)
	chdirTemp(t)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	captureClaudeAt(t, app, "side", sideToken, now.Add(time.Hour))

	if code := runPin(ctx, app, opts, "main", modeShared); code != constants.ExitOK {
		t.Fatalf("pin --shared exit %d", code)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	pinID := paths.PinID(cwd)
	shared := app.Paths.SharedDir(pinID, constants.ToolClaude)
	// A leftover isolated store from no binding this pin still has: its account cannot
	// be attributed from the replaced (shared) fragment, and the command does not touch
	// it either way.
	leftover := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "side")
	mkdirs(t, leftover)
	writeFile(t, filepath.Join(leftover, ".credentials.json"),
		claudeOAuthPayload("sk-ant-oat01-LEFTOVER-eeee", now.Add(9*time.Hour)))

	// Missing evidence: the store holds a newer copy and no identity cache to compare.
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", shared),
		claudeOAuthPayload("sk-ant-oat01-MAIN-REFRESHED-cccc", now.Add(8*time.Hour)))
	if err := os.Remove(filepath.Join(shared, ".claude.json")); err != nil {
		t.Fatal(err)
	}
	_, stderr := captureStderr(t, func() int { return runPin(ctx, app, opts, "main", modeShared) })

	// Counted per line, not per phrase: one message carries several of these markers, and
	// counting phrases made this assertion fail on correct output.
	if n := refusalLines(stderr); n != 1 {
		t.Fatalf("one refused store must produce one message, got %d:\n%s", n, stderr)
	}
	if !strings.Contains(stderr, "log in inside that directory") || !strings.Contains(stderr, cwd) {
		t.Fatalf("a missing-evidence refusal must name the bound directory to log in to:\n%s", stderr)
	}
	if strings.Contains(stderr, leftover) {
		t.Fatalf("a store this command does not touch must not be reported:\n%s", stderr)
	}
	// The consequence this message states has to be the one the write applies. Both halves
	// are asserted because each was unguarded and each failed on its own: the state, which
	// is how a re-pin came to destroy the copy with every test green, and the wording,
	// measured 2026-08-08 — leaving the old "and this bind replaces it" clause in place
	// while the write kept the copy survived the entire suite.
	if got := readFile(t, dirCredFile(app, constants.ToolClaude, "main", shared)); !strings.Contains(got, "sk-ant-oat01-MAIN-REFRESHED-cccc") {
		t.Fatalf("a copy kae could not attribute must be kept, not overwritten: %s", got)
	}
	if !strings.Contains(stderr, "kept it rather than replacing it") {
		t.Fatalf("the primary voice must state the consequence the write applied:\n%s", stderr)
	}
	if strings.Contains(stderr, "this bind replaces it") {
		t.Fatalf("the replace wording must not survive where the copy was kept:\n%s", stderr)
	}

	// Positive evidence: the copy belongs to another account. Same store, so still one
	// message — and no remedy, because this account's own credential is fine.
	writeFile(t, filepath.Join(shared, ".claude.json"), claudeIdentityFile("side-uuid"))
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", shared),
		claudeOAuthPayload(sideToken, now.Add(10*time.Hour)))
	_, stderr = captureStderr(t, func() int { return runPin(ctx, app, opts, "main", modeShared) })

	if n := refusalLines(stderr); n != 1 {
		t.Fatalf("one refused store must produce one message, got %d:\n%s", n, stderr)
	}
	if strings.Contains(stderr, "log in inside") {
		t.Fatalf("a copy that belongs to another account must not come with a login remedy:\n%s", stderr)
	}
}

// The harvest reads a store twice in one command by design — the pin-level pass
// classifies it, the chokepoint reads it again before writing — and on darwin each read
// is a `security` invocation, so a re-pin would double the keychain accesses (and the
// prompts) for every bound claude directory. `kae pin` therefore opts into the same
// per-command read cache the switch path uses, and this counts it the same way
// `TestSwitchCoalescesKeychainReads` does (efficiency lens, 2026-08-04).
func TestRunPinCoalescesTheHarvestKeychainReads(t *testing.T) {
	sim := &keychainSim{}
	runner.With(sim, func() {
		app := overlayTestApp(t)
		app.Env.GOOS = "darwin"
		chdirTemp(t)
		ctx := context.Background()
		opts := commonOpts{Format: formatText}
		captureClaudeFromKeychain(t, app, sim, "main", mainToken, app.Now().Add(time.Hour))
		if code := runPin(ctx, app, opts, "main", modeIsolated); code != constants.ExitOK {
			t.Fatalf("pin --isolated exit %d", code)
		}

		sim.readW = 0
		if code := runPin(ctx, app, opts, "main", modeIsolated); code != constants.ExitOK {
			t.Fatalf("re-pin exit %d", code)
		}

		if sim.readW != 1 {
			t.Fatalf("a re-pin must read the bound store's item once, got %d", sim.readW)
		}
	})
}

// The case a suppression keyed on the store's *kind* silenced completely: the pass ran
// and had nothing to say, so nobody said anything while a live login was overwritten.
// Reached without any exotic state — `kae unpin` keeps the store on purpose, so a re-pin
// has no previous binding to attribute it from (reading-type review, round 3).
func TestRunPinReportsARefusalThePinLevelPassCannotAttribute(t *testing.T) {
	app := overlayTestApp(t)
	chdirTemp(t)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	captureClaudeAt(t, app, "side", sideToken, now.Add(time.Hour))

	if code := runPin(ctx, app, opts, "main", modeShared); code != constants.ExitOK {
		t.Fatalf("pin --shared exit %d", code)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	shared := app.Paths.SharedDir(paths.PinID(cwd), constants.ToolClaude)
	// A bind that *does* report a refusal for this store first, so its record exists
	// before the one under test. That record is scoped to one bind and cleared when the
	// next pass starts; left to live as long as the App it silences the report below,
	// which is how it made a two-phase test pass for the wrong reason.
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", shared),
		claudeOAuthPayload("sk-ant-oat01-NO-IDENTITY-ffff", now.Add(6*time.Hour)))
	if err := os.Remove(filepath.Join(shared, ".claude.json")); err != nil {
		t.Fatal(err)
	}
	if _, stderr := captureStderr(t, func() int { return runPin(ctx, app, opts, "main", modeShared) }); refusalLines(stderr) != 1 {
		t.Fatalf("setup expected one reported refusal:\n%s", stderr)
	}

	// Someone else's newer copy in the store, and no binding left to attribute it from.
	writeFile(t, filepath.Join(shared, ".claude.json"), claudeIdentityFile("side-uuid"))
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", shared),
		claudeOAuthPayload(sideToken, now.Add(8*time.Hour)))
	if code := runUnpin(ctx, app, opts, false); code != constants.ExitOK {
		t.Fatalf("unpin exit %d", code)
	}

	_, stderr := captureStderr(t, func() int { return runPin(ctx, app, opts, "main", modeShared) })

	if n := refusalLines(stderr); n != 1 {
		t.Fatalf("overwriting a copy nobody could attribute must be reported exactly once, got %d:\n%s",
			n, stderr)
	}
	be := testBackend(t, app)
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); strings.Contains(got, sideToken) {
		t.Fatalf("an unattributable copy must not be filed under main: %s", got)
	}
}

// The same hole one store shape over. An **isolated** store is attributable from its
// own path, so the pass reaches its report switch and leaves by the "this operation
// does not touch it" arm rather than the unattributable gate the test above covers —
// and marking the store there would silence the write path exactly as the kind-based
// suppression did (execution-type review, round 3: that mutation survived the suite
// while a re-pin overwrote the only refreshable copy in silence).
func TestRunPinReportsARefusalForAnIsolatedStoreThePassSkips(t *testing.T) {
	app := overlayTestApp(t)
	chdirTemp(t)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	captureClaudeAt(t, app, "side", sideToken, now.Add(time.Hour))

	if code := runPin(ctx, app, opts, "main", modeIsolated); code != constants.ExitOK {
		t.Fatalf("pin --isolated exit %d", code)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	store := app.Paths.IsolatedConfigDir(paths.PinID(cwd), constants.ToolClaude, "main")
	writeFile(t, filepath.Join(store, ".claude.json"), claudeIdentityFile("side-uuid"))
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", store),
		claudeOAuthPayload(sideToken, now.Add(8*time.Hour)))
	// After unpin there is no binding to replace, so the pass skips this store rather
	// than reporting it — which is exactly when the write path has to speak.
	if code := runUnpin(ctx, app, opts, false); code != constants.ExitOK {
		t.Fatalf("unpin exit %d", code)
	}

	_, stderr := captureStderr(t, func() int { return runPin(ctx, app, opts, "main", modeIsolated) })

	if n := refusalLines(stderr); n != 1 {
		t.Fatalf("overwriting a copy the pass skipped must be reported exactly once, got %d:\n%s", n, stderr)
	}
}

// An **isolated** re-bind is the one case where the store the binding leaves is not the
// shared one, so the set of stores this operation moves off has to come from the
// replaced fragment's *own* mode. Forcing that lookup to shared mode survived the suite:
// the genuine refusal went unreported and a leftover shared store's was reported instead
// (execution-type review, round 3).
func TestRunRebindIsolatedReportsTheStoreItLeaves(t *testing.T) {
	app := overlayTestApp(t)
	chdirTemp(t)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	captureClaudeAt(t, app, "side", sideToken, now.Add(time.Hour))
	if code := runPin(ctx, app, opts, "main", modeIsolated); code != constants.ExitOK {
		t.Fatalf("pin --isolated exit %d", code)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	pinID := paths.PinID(cwd)
	leaving := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "main")
	// A newer copy with nothing to attribute it by, so the harvest refuses and the pass
	// has something to report about the store the re-bind moves off.
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", leaving),
		claudeOAuthPayload("sk-ant-oat01-MAIN-REFRESHED-cccc", now.Add(8*time.Hour)))
	if err := os.Remove(filepath.Join(leaving, ".claude.json")); err != nil {
		t.Fatal(err)
	}
	// A leftover shared store from no binding at all, which must not be what gets named.
	shared := app.Paths.SharedDir(pinID, constants.ToolClaude)
	mkdirs(t, shared)
	writeFile(t, filepath.Join(shared, ".credentials.json"),
		claudeOAuthPayload("sk-ant-oat01-LEFTOVER-eeee", now.Add(9*time.Hour)))

	_, stderr := captureStderr(t, func() int { return runRebind(ctx, app, opts, constants.ToolClaude, "side") })

	// The message names the **account** whose copy could not be kept and the bound
	// directory to log in to — not the store path, deliberately, since a store path is
	// not somewhere a login works. What the mutation removes is the message itself: with
	// `replaced` computed as if the previous binding were shared, the store being left is
	// no longer in it and nothing is reported.
	if !strings.Contains(stderr, "held for claude/main") ||
		!strings.Contains(stderr, "log in inside that directory") {
		t.Fatalf("the store the binding leaves must be reported, with the remedy:\n%s", stderr)
	}
	if strings.Contains(stderr, shared) || strings.Contains(stderr, "LEFTOVER") {
		t.Fatalf("a leftover store this re-bind does not touch must not be named:\n%s", stderr)
	}
	_ = leaving
}

// A snapshot that is itself a tombstone must lose to any usable live copy, whatever
// their deadlines say: a tombstone carries whatever `expiresAt` the tool left in it, so
// comparing timestamps alone would let a dead snapshot outrank a working credential and
// the bind would write the tombstone over it. Dropping the `!stored.Revoked` half of the
// cutoff survived the suite (execution-type review, round 3).
func TestWriteDirCredentialPrefersAUsableCopyOverATombstonedSnapshot(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	now := app.Now()
	// The snapshot is a tombstone dated *later* than the live copy.
	seedClaude(t, app, mainToken, "main-uuid")
	writeFile(t, filepath.Join(app.Env.Home, ".claude", ".credentials.json"), fmt.Sprintf(
		`{"claudeAiOauth":{"accessToken":"","refreshToken":"","expiresAt":%d}}`,
		now.Add(20*time.Hour).UnixMilli(),
	))
	code, out := captureStdout(t, func() int {
		return runCapture(ctx, app, commonOpts{Format: formatText}, constants.ToolClaude, "main")
	})
	mustExit(t, constants.ExitOK, code, out)

	credDir := t.TempDir()
	const alive = "sk-ant-oat01-MAIN-ALIVE-cccc"
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", credDir), claudeOAuthPayload(alive, now.Add(2*time.Hour)))
	writeFile(t, filepath.Join(credDir, ".claude.json"), claudeIdentityFile("main-uuid"))

	be := testBackend(t, app)
	if err := app.writeDirCredential(ctx, be, constants.ToolClaude, "main", credDir); err != nil {
		t.Fatalf("writeDirCredential: %v", err)
	}
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); !strings.Contains(got, alive) {
		t.Fatalf("a usable copy must beat a tombstoned snapshot whatever the dates say: %s", got)
	}
	if got := readFile(t, dirCredFile(app, constants.ToolClaude, "main", credDir)); !strings.Contains(got, alive) {
		t.Fatalf("the tombstone must not be written over a working login: %s", got)
	}
}

// Equal deadlines are **not** harvested, and two of the smoke block's cases rest on
// that: reusing one `expiresAt` on both sides is what made them prove nothing, which
// only holds as an argument while the comparison stays strict. Loosening it to
// `>=` survived the suite (execution-type review, round 3).
func TestWriteDirCredentialDoesNotHarvestAnEqualDeadline(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	deadline := app.Now().Add(time.Hour)
	captureClaudeAt(t, app, "main", mainToken, deadline)
	credDir := t.TempDir()
	const other = "sk-ant-oat01-SAME-DEADLINE-dddd"
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", credDir), claudeOAuthPayload(other, deadline))
	writeFile(t, filepath.Join(credDir, ".claude.json"), claudeIdentityFile("main-uuid"))

	be := testBackend(t, app)
	if err := app.writeDirCredential(ctx, be, constants.ToolClaude, "main", credDir); err != nil {
		t.Fatalf("writeDirCredential: %v", err)
	}
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); strings.Contains(got, other) {
		t.Fatalf("an equal deadline is not newer, so nothing may be harvested: %s", got)
	}
}

// `kae account rename` warns and tells the user to re-bind with `kae pin <tool> <new>`,
// which is **runRebind** — so that is the sweep the renamed account's newest copy meets,
// and its `purging=false` needs pinning separately from `runPin`'s (reading-type review,
// round 3: mutating this one to `true` passed the whole suite).
func TestRunRebindSweepKeepsALostAccountsCredential(t *testing.T) {
	sim := &keychainSim{}
	runner.With(sim, func() {
		app := overlayTestApp(t)
		app.Env.GOOS = "darwin"
		chdirTemp(t)
		ctx := context.Background()
		opts := commonOpts{Format: formatText}
		captureClaudeFromKeychain(t, app, sim, "main", mainToken, app.Now().Add(time.Hour))
		captureClaudeFromKeychain(t, app, sim, "side", sideToken, app.Now().Add(time.Hour))
		if code := runPin(ctx, app, opts, "main", modeIsolated); code != constants.ExitOK {
			t.Fatalf("pin --isolated exit %d", code)
		}
		// Pre-split: since the credential became the account's rather than the
		// directory's, a bind sweep does not consider it at all — it is kept the way an
		// account snapshot is kept, with nothing to say. The branch under test is the
		// one that still runs, for the per-directory item an older kae left behind.
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		makePreSplit(t, app, constants.ToolClaude, "main", cwd,
			app.Paths.IsolatedConfigDir(paths.PinID(cwd), constants.ToolClaude, "main"))
		if err := os.RemoveAll(app.Paths.AccountDir(constants.ToolClaude, "main")); err != nil {
			t.Fatal(err)
		}
		sim.payload = claudeOAuthPayload("sk-ant-oat01-MAIN-REFRESHED-cccc", app.Now().Add(8*time.Hour))
		sim.ops = nil

		_, stderr := captureStderr(t, func() int {
			return runRebind(ctx, app, opts, constants.ToolClaude, "side")
		})

		if strings.Contains(strings.Join(sim.ops, ","), "delete") {
			t.Fatalf("the re-bind kae itself recommends after a rename must not delete that copy: %v", sim.ops)
		}
		if !strings.Contains(stderr, "no account named claude/main exists any more") {
			t.Fatalf("keeping it must name the branch that kept it: %q", stderr)
		}
	})
}

// A command that refuses must not have written anything first. The mode check moved
// above the harvest for that reason, and it is observable: an unrecognized mode still
// lets an *isolated* store be attributed from its own path, so the pass would harvest
// and rewrite a snapshot before the refusal (reading-type review, round 3).
func TestRunRebindRefusesAnUnknownModeWithoutHarvesting(t *testing.T) {
	app := overlayTestApp(t)
	chdirTemp(t)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	captureClaudeAt(t, app, "side", sideToken, now.Add(time.Hour))
	if code := runPin(ctx, app, opts, "main", modeIsolated); code != constants.ExitOK {
		t.Fatalf("pin --isolated exit %d", code)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	store := app.Paths.IsolatedConfigDir(paths.PinID(cwd), constants.ToolClaude, "main")
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", store),
		claudeOAuthPayload("sk-ant-oat01-MAIN-REFRESHED-cccc", now.Add(8*time.Hour)))
	// A mode kae does not recognize, with everything else intact.
	fragment := readFile(t, fragmentRelPath)
	writeFile(t, fragmentRelPath, strings.Replace(fragment, "mode="+modeIsolated, "mode=overlay", 1))

	be := testBackend(t, app)
	before := snapshotPayload(t, app, be, constants.ToolClaude, "main")
	code := runRebind(ctx, app, opts, constants.ToolClaude, "side")

	if code == constants.ExitOK {
		t.Fatalf("an unrecognized mode must be refused, got exit %d", code)
	}
	if after := snapshotPayload(t, app, be, constants.ToolClaude, "main"); after != before {
		t.Fatalf("a refused command must not have harvested first:\nbefore %s\nafter  %s", before, after)
	}
}

// The sweep a *bind* runs must not delete a usable copy whose account is gone — the
// `purging` argument is what says so, and nothing pinned the **call sites** until this
// test: flipping `runPin`'s to `true` passed the whole suite while making `kae pin`
// destroy a renamed account's live credential (execution-type review, round 2).
func TestRunPinSweepKeepsALostAccountsCredential(t *testing.T) {
	sim := &keychainSim{}
	runner.With(sim, func() {
		app := overlayTestApp(t)
		app.Env.GOOS = "darwin"
		chdirTemp(t)
		ctx := context.Background()
		opts := commonOpts{Format: formatText}
		captureClaudeFromKeychain(t, app, sim, "main", mainToken, app.Now().Add(time.Hour))
		if code := runPin(ctx, app, opts, "main", modeIsolated); code != constants.ExitOK {
			t.Fatalf("pin --isolated exit %d", code)
		}
		// Pre-split, for the reason the sibling test above gives: a bind leaves an
		// account's own credential store alone, so only the per-directory item an older
		// kae left behind still reaches this branch.
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		makePreSplit(t, app, constants.ToolClaude, "main", cwd,
			app.Paths.IsolatedConfigDir(paths.PinID(cwd), constants.ToolClaude, "main"))
		// The account goes away (`kae account rm`, or a rename that moved it) while its
		// store still holds the copy the tool refreshed there.
		if err := os.RemoveAll(app.Paths.AccountDir(constants.ToolClaude, "main")); err != nil {
			t.Fatal(err)
		}
		captureClaudeFromKeychain(t, app, sim, "side", sideToken, app.Now().Add(time.Hour))
		app.Config.Profiles["main"] = config.Profile{Accounts: map[string]string{constants.ToolClaude: "side"}}
		sim.payload = claudeOAuthPayload("sk-ant-oat01-MAIN-REFRESHED-cccc", app.Now().Add(8*time.Hour))
		sim.ops = nil

		_, stderr := captureStderr(t, func() int { return runPin(ctx, app, opts, "main", modeIsolated) })

		if strings.Contains(strings.Join(sim.ops, ","), "delete") {
			t.Fatalf("a bind must not delete the credential of an account it cannot harvest into: %v", sim.ops)
		}
		// Branch-specific: "left in place" is shared by the unreadable-store, the
		// unattributable and the account-gone messages, so matching it alone would pass on
		// a run that took a different arm entirely.
		if !strings.Contains(stderr, "no account named claude/main exists any more") {
			t.Fatalf("keeping it must name the branch that kept it: %q", stderr)
		}
		if strings.Contains(stderr, "kae add --no-login") {
			t.Fatalf("an account removed on purpose must not be told to re-add itself: %q", stderr)
		}
	})
}

// The other half of "the binding moves to a different store": an isolated re-bind
// re-keys the store by account, so the copy the tool refreshed sits in the store of the
// account being left behind. Nothing tested the isolated path end-to-end (only the
// shared one), and the pass skipping isolated stores survived the suite.
func TestRunRebindIsolatedHarvestsThePreviousAccount(t *testing.T) {
	app := overlayTestApp(t)
	chdirTemp(t)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	captureClaudeAt(t, app, "side", sideToken, now.Add(time.Hour))

	if code := runPin(ctx, app, opts, "main", modeIsolated); code != constants.ExitOK {
		t.Fatalf("pin --isolated exit %d", code)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	old := app.Paths.IsolatedConfigDir(paths.PinID(cwd), constants.ToolClaude, "main")
	const refreshed = "sk-ant-oat01-MAIN-REFRESHED-cccc"
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", old), claudeOAuthPayload(refreshed, now.Add(8*time.Hour)))

	if code := runRebind(ctx, app, opts, constants.ToolClaude, "side"); code != constants.ExitOK {
		t.Fatalf("re-bind exit %d", code)
	}

	be := testBackend(t, app)
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); !strings.Contains(got, refreshed) {
		t.Fatalf("the account left behind lost its refreshed credential: %s", got)
	}
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "side"); strings.Contains(got, refreshed) {
		t.Fatalf("main's credential was filed under side: %s", got)
	}
}

// A shared-mode re-bind to another account is the case the delete sweep gets right
// and the write path cannot see: that store is account-agnostic, so the credential
// in it belongs to the account being bound *away from* — and the binding being
// replaced is the only thing that says which. Without the pre-pass the re-bind
// overwrote it with the new account's snapshot and the previous account's login was
// gone from both the store and its snapshot (execution-type review, 2026-08-04).
func TestRunRebindSharedHarvestsThePreviousAccount(t *testing.T) {
	app := overlayTestApp(t)
	chdirTemp(t)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	captureClaudeAt(t, app, "side", sideToken, now.Add(time.Hour))

	if code := runPin(ctx, app, opts, "main", modeShared); code != constants.ExitOK {
		t.Fatalf("pin --shared exit %d", code)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	shared := app.Paths.SharedDir(paths.PinID(cwd), constants.ToolClaude)
	const refreshed = "sk-ant-oat01-MAIN-REFRESHED-cccc"
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", shared), claudeOAuthPayload(refreshed, now.Add(8*time.Hour)))

	if code := runRebind(ctx, app, opts, constants.ToolClaude, "side"); code != constants.ExitOK {
		t.Fatalf("re-bind exit %d", code)
	}

	be := testBackend(t, app)
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); !strings.Contains(got, refreshed) {
		t.Fatalf("main's refreshed credential was destroyed by re-binding to side: %s", got)
	}
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "side"); strings.Contains(got, refreshed) {
		t.Fatalf("main's credential was filed under side: %s", got)
	}
	if got := readFile(t, dirCredFile(app, constants.ToolClaude, "side", shared)); !strings.Contains(got, sideToken) {
		t.Fatalf("the re-bind must leave the store on the new account: %s", got)
	}
}

// The harvest stamps `captured_at` by re-reading the account rather than saving the
// copy it loaded, which is the seam rule `state.json` follows and for the same
// reason: the harvest holds only a per-directory lock. The case that makes the
// *missing* branch load-bearing — `account.Save` begins with MkdirAll, so saving a
// stale copy after a concurrent `kae account rm` would resurrect an `account.toml`
// naming payloads that are already being deleted (execution-type review, 2026-08-04).
func TestRecordHarvestTimeDoesNotResurrectARemovedAccount(t *testing.T) {
	app := testApp(t, nil)
	captureClaudeAt(t, app, "main", mainToken, app.Now().Add(time.Hour))
	dir := app.Paths.AccountDir(constants.ToolClaude, "main")
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}

	app.recordHarvestTime(constants.ToolClaude, "main")

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("an account removed under the harvest must stay removed: %v", err)
	}
}

// The harvest is claude-only by declaration, not by accident, and the declaration is
// what a new tool has to earn: without a measured rotation "the newest copy" is a
// guess, and a wrong guess destroys the working credential. Enumerated rather than
// spot-checked, so adding a tool to that predicate cannot pass unnoticed.
//
// The second half is why a tool needs more than the measurement: attribution reads
// an identity-only artifact, and a tool that declares none can never satisfy it — so
// the harvest would be dead code for it. codex is in that state today, which is why
// the end-to-end codex test below cannot prove this gate on its own.
func TestHarvestIsDeclaredForMeasuredToolsOnly(t *testing.T) {
	ctx := context.Background()
	app := testApp(t, nil)
	for _, tool := range constants.Tools {
		if got := rotatesSingleUse(tool); got != (tool == constants.ToolClaude) {
			t.Fatalf("rotatesSingleUse(%s) = %v; only claude's rotation is measured "+
				"(docs/VALIDATION.md, docs/ROADMAP.md)", tool, got)
		}
		if !rotatesSingleUse(tool) {
			continue
		}
		ad, err := adapter.ForTool(tool)
		if err != nil {
			t.Fatalf("adapter for %s: %v", tool, err)
		}
		specs, err := ad.Artifacts(ctx, app.Env)
		if err != nil {
			t.Fatalf("artifacts for %s: %v", tool, err)
		}
		identities := 0
		for _, sp := range specs {
			if sp.IdentityOnly {
				identities++
			}
		}
		if identities == 0 {
			t.Fatalf("%s harvests but declares no identity-only artifact, so dirIdentityConfirms "+
				"can never confirm and the harvest would never fire", tool)
		}
	}
}

// A payload that parses but carries no deadline cannot be ordered against anything,
// so it is never harvested — the guard the selection rule rests on. A mutation that
// dropped the zero check survived the whole suite (execution-type review).
//
// Two shapes, and they reach the classifier by different routes, which is why the second
// row exists: with `expiresAt` **absent** claude never sets `Known`, while a **non-numeric**
// one sets `Known` and parses to the zero time. The second used to be classified
// "nothing to lose" — see TestPruneDirCredentialsKeepsACopyItCannotDate for what that
// licensed — so an older comment here claiming the delete path "protected this state from
// the start" was true only of the first row.
func TestWriteDirCredentialDoesNotHarvestUndatedCredential(t *testing.T) {
	for _, tc := range []struct{ name, oauth string }{
		{"no expiresAt at all", `{"accessToken":"sk-ant-oat01-UNDATED-ffff","refreshToken":"r"}`},
		{"an expiresAt of the wrong type", `{"accessToken":"sk-ant-oat01-UNDATED-ffff","refreshToken":"r","expiresAt":"1814400000000"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := testApp(t, nil)
			ctx := context.Background()
			captureClaudeAt(t, app, "main", mainToken, app.Now().Add(time.Hour))
			credDir := t.TempDir()
			writeFile(t, dirCredFile(app, constants.ToolClaude, "main", credDir),
				`{"claudeAiOauth":`+tc.oauth+`}`)
			writeFile(t, filepath.Join(credDir, ".claude.json"), claudeIdentityFile("main-uuid"))

			be := testBackend(t, app)
			_, stderr := captureStderr(t, func() int {
				if err := app.writeDirCredential(ctx, be, constants.ToolClaude, "main", credDir); err != nil {
					t.Fatalf("writeDirCredential: %v", err)
				}
				return 0
			})
			if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); !strings.Contains(got, mainToken) {
				t.Fatalf("an undated credential must not be harvested: %s", got)
			}
			// And the overwrite is not silent. A payload kae cannot judge may still be a
			// login, and on this path it is the only early signal of an upstream format
			// change — `upstream_version` skips a version string it cannot parse.
			if !strings.Contains(stderr, "cannot read or date the copy already there") {
				t.Fatalf("overwriting a copy kae cannot judge must be reported: %q", stderr)
			}
		})
	}
}

// An identity cache that resolves *outside* the store labels the real home, not this
// directory (a pre-v0.16.0 bind linked it there), so it cannot attribute this
// store's credential — even when it happens to name the same account. Dropping that
// branch survived the whole suite (execution-type review).
func TestWriteDirCredentialDoesNotHarvestThroughSharedIdentity(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	credDir := t.TempDir()
	const refreshed = "sk-ant-oat01-MAIN-REFRESHED-cccc"
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", credDir), claudeOAuthPayload(refreshed, now.Add(8*time.Hour)))
	// The real home's cache, linked into the store, naming the account being bound.
	if err := os.Symlink(filepath.Join(app.Env.Home, ".claude.json"),
		filepath.Join(credDir, ".claude.json")); err != nil {
		t.Fatal(err)
	}

	be := testBackend(t, app)
	_, stderr := captureStderr(t, func() int {
		if err := app.writeDirCredential(ctx, be, constants.ToolClaude, "main", credDir); err != nil {
			t.Fatalf("writeDirCredential: %v", err)
		}
		return 0
	})
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); strings.Contains(got, refreshed) {
		t.Fatalf("a credential attributed by the real home's label was harvested: %s", got)
	}
	if !strings.Contains(stderr, "shared with the real tool home") {
		t.Fatalf("the reason must be the shared label, not something else: %q", stderr)
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
// removes. The stale store here is an isolated store for an account the directory no
// longer binds, holding the tombstone a failed refresh leaves behind — nothing to
// harvest, so the sweep is free to remove it, which keeps this test about the thing it
// is named for: *which* item the sweep addresses.
//
// The payload is the tombstone **as measured**: blank tokens *and* `expiresAt: 0`
// (docs/VALIDATION.md's claude row). It used to carry a future deadline, which is not a
// shape claude has been observed to produce and which the sweep now keeps rather than
// deletes — blank tokens with a live deadline are indistinguishable from an upstream
// token-key rename, and that is a working login. So this fixture is now the positive
// control for the one arm that licenses a delete, and the sibling below is its negative.
func TestPruneDirCredentialsRemovesSupersededItem(t *testing.T) {
	app, pinID := prunableApp(t)
	stale := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "side")
	bound := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "main")
	mkdirs(t, stale, bound)

	const tombstone = `{"claudeAiOauth":{"accessToken":"","refreshToken":"","expiresAt":0}}`
	fake := &runnertest.Fake{Stdout: tombstone, Code: 0}
	var lines []string
	runner.With(fake, func() {
		lines = app.pruneDirCredentials(context.Background(), testBackend(t, app), pinID, "", map[string]bool{bound: true}, fragmentInfo{}, false)
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

// The negative control for the arm above, in the two shapes that are **not** the measured
// tombstone. `Revoked` is derived from token fields that are empty *or absent*, so both of
// these read as revoked while carrying a live deadline — and an upstream rename of the
// token keys is exactly the second one. docs/VALIDATION.md justifies that wide reading by
// saying it makes every path *decline* to touch the copy; for this consumer it would have
// licensed the delete instead, which is the defect this pins.
func TestPruneDirCredentialsKeepsARevokedLookingCopyWithALiveDeadline(t *testing.T) {
	for _, tc := range []struct{ name, oauth string }{
		{"blank tokens, live deadline", `{"accessToken":"","refreshToken":"","expiresAt":%d}`},
		{"token keys renamed away", `{"tokenV2":"sk-ant-oat01-LIVE","expiresAt":%d}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, pinID := prunableApp(t)
			oauth := fmt.Sprintf(tc.oauth, app.Now().Add(time.Hour).UnixMilli())
			stale := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "side")
			bound := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "main")
			mkdirs(t, stale, bound)

			fake := &runnertest.Fake{Stdout: `{"claudeAiOauth":` + oauth + `}`, Code: 0}
			var lines []string
			var stderr string
			runner.With(fake, func() {
				_, stderr = captureStderr(t, func() int {
					lines = app.pruneDirCredentials(context.Background(), testBackend(t, app), pinID, "",
						map[string]bool{bound: true}, fragmentInfo{}, false)
					return 0
				})
			})
			if strings.Contains(strings.Join(fake.Args, " "), "delete-generic-password") {
				t.Fatalf("a copy kae cannot judge must be kept, not deleted: %v", fake.Args)
			}
			if len(lines) != 0 {
				t.Fatalf("nothing was removed, so nothing may be reported as removed: %v", lines)
			}
			if !strings.Contains(stderr, "cannot read or date the claude credential") {
				t.Fatalf("keeping it must say why: %q", stderr)
			}
		})
	}
}

// A usable copy whose account no longer exists is the case where "delete it" and
// "keep it" are both defensible, so it turns on what the user asked for. `kae unpin
// --purge` asked for these credentials to go: keeping one would strand a live token
// no kae command can address. A `kae pin` sweep asked to *bind* something, and
// deleting there destroys a login nobody named — which `kae account rename` reaches
// through kae's own re-bind remedy, making the renamed account's newest copy the
// casualty (reading-type review, round 2).
func TestPruneDirCredentialsDeletesALostAccountsCopyOnlyWhenPurging(t *testing.T) {
	for _, purging := range []bool{false, true} {
		t.Run(map[bool]string{false: "housekeeping", true: "purge"}[purging], func(t *testing.T) {
			sim := &keychainSim{}
			runner.With(sim, func() {
				app := testApp(t, map[string]string{"USER": "me"})
				app.Env.GOOS = "darwin"
				// Captured, then removed, so the item exists under the account claude's own
				// rule derives while the snapshot is gone — what `rm` and `rename` both leave.
				captureClaudeFromKeychain(t, app, sim, "side", sideToken, app.Now().Add(time.Hour))
				if err := os.RemoveAll(app.Paths.AccountDir(constants.ToolClaude, "side")); err != nil {
					t.Fatal(err)
				}
				pinID := paths.PinID(t.TempDir())
				stale := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "side")
				mkdirs(t, stale)
				sim.payload = claudeOAuthPayload("sk-ant-oat01-REFRESHED-cccc", app.Now().Add(8*time.Hour))
				sim.ops = nil

				var lines []string
				_, stderr := captureStderr(t, func() int {
					lines = app.pruneDirCredentials(context.Background(), testBackend(t, app),
						pinID, "", nil, fragmentInfo{}, purging)
					return 0
				})

				deleted := strings.Contains(strings.Join(sim.ops, ","), "delete")
				if deleted != purging {
					t.Fatalf("purging=%v: deleted=%v (ops %v)", purging, deleted, sim.ops)
				}
				if len(lines) != map[bool]int{false: 0, true: 1}[purging] {
					t.Fatalf("purging=%v: reported %v", purging, lines)
				}
				// Asserted per direction, because accepting either message in both made the
				// test pass on a run that fell into the wrong branch entirely.
				want := "left in place"
				if purging {
					want = "deleted without being kept anywhere"
				}
				if !strings.Contains(stderr, want) {
					t.Fatalf("purging=%v: stderr must say %q: %q", purging, want, stderr)
				}
				if !purging && !strings.Contains(stderr, "kae unpin --purge") {
					t.Fatalf("housekeeping must name the command that would remove it: %q", stderr)
				}
			})
		})
	}
}

// The fourth arm of the delete gate, and the one nothing covered: the account exists but
// kae could not read its credential snapshot. A later run may still harvest this copy, so
// the item stays — flipping it to "delete" survived the whole suite (execution-type
// review, round 3).
func TestPruneDirCredentialsKeepsWhenTheSnapshotPayloadIsUnreadable(t *testing.T) {
	sim := &keychainSim{}
	runner.With(sim, func() {
		app := testApp(t, map[string]string{"USER": "me"})
		app.Env.GOOS = "darwin"
		ctx := context.Background()
		captureClaudeFromKeychain(t, app, sim, "side", sideToken, app.Now().Add(time.Hour))
		// The snapshot still declares its payload; the payload itself is gone from the
		// backend, which is the state `doctor secret_missing` reports.
		be := testBackend(t, app)
		acc, found, err := account.Load(app.Paths.AccountDir(constants.ToolClaude, "side"))
		if err != nil || !found {
			t.Fatalf("load side: found=%v err=%v", found, err)
		}
		if err := be.Delete(ctx, acc.Artifacts[credentialArtifactName(constants.ToolClaude)].SecretRef); err != nil {
			t.Fatal(err)
		}
		pinID := paths.PinID(t.TempDir())
		stale := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "side")
		mkdirs(t, stale)
		sim.payload = claudeOAuthPayload("sk-ant-oat01-REFRESHED-cccc", app.Now().Add(8*time.Hour))
		sim.ops = nil

		var lines []string
		_, stderr := captureStderr(t, func() int {
			lines = app.pruneDirCredentials(ctx, be, pinID, "", nil, fragmentInfo{}, true)
			return 0
		})

		if strings.Contains(strings.Join(sim.ops, ","), "delete") {
			t.Fatalf("a copy a later run could still harvest must not be deleted: %v", sim.ops)
		}
		if len(lines) != 0 {
			t.Fatalf("nothing was removed, so nothing may be reported: %v", lines)
		}
		if !strings.Contains(stderr, "could not read snapshot") {
			t.Fatalf("the branch that kept it must be the one that says so: %q", stderr)
		}
	})
}

// The opposite: a store kae cannot *read* is kept. An unrecognized payload is what
// an upstream format change looks like from here, and it cannot be told apart from a
// working login — so the sweep must not treat "I could not parse it" as "there is
// nothing in it". The pre-change code deleted unconditionally, and the fixtures that
// hid this were payloads no real store holds.
// The double (keychainSim, not runnertest.Fake) is load-bearing: the sweep probes the
// item's *attributes* before deciding, and a flat fake answers that probe with the
// same body it answers the payload read with. A first version of this test used one
// and passed because the probe read "absent" — proving nothing about the branch it
// names. Only a double that tells `-w` from an attributes read reaches the gate.
func TestPruneDirCredentialsKeepsUnreadableCredential(t *testing.T) {
	sim := &keychainSim{}
	runner.With(sim, func() {
		app := testApp(t, map[string]string{"USER": "me"})
		app.Env.GOOS = "darwin"
		// Captured first, so the item exists under the account claude's own rule derives
		// rather than one this test made up: the sweep probes attributes with that
		// account, and a hand-set one answers "absent" and skips the gate entirely.
		captureClaudeFromKeychain(t, app, sim, "side", sideToken, app.Now().Add(time.Hour))
		pinID := paths.PinID(t.TempDir())
		stale := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "side")
		mkdirs(t, stale)
		// Then the payload becomes one kae cannot judge: it is claude's shape, so the
		// artifact layer's structure guard passes it through, but it carries no deadline,
		// so nothing can order it against the snapshot. A payload the structure guard
		// *rejects* lands in the same state by a different route (the read errors), which
		// is why this fixture is the one that reaches the branch — measured, because the
		// rejected-shape fixture passed this test without ever getting there.
		sim.payload = `{"claudeAiOauth":{"accessToken":"a","refreshToken":"r"}}`
		sim.ops = nil

		var lines []string
		_, stderr := captureStderr(t, func() int {
			lines = app.pruneDirCredentials(context.Background(), testBackend(t, app), pinID, "", nil, fragmentInfo{}, false)
			return 0
		})

		if strings.Contains(strings.Join(sim.ops, ","), "delete") {
			t.Fatalf("a credential kae cannot read must not be deleted: %v", sim.ops)
		}
		if len(lines) != 0 {
			t.Fatalf("nothing was removed, so nothing may be reported: %v", lines)
		}
		if !strings.Contains(stderr, "cannot read") {
			t.Fatalf("keeping it must be explained: %q", stderr)
		}
	})
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
		lines = app.pruneDirCredentials(context.Background(), testBackend(t, app), pinID, "", map[string]bool{claudeStore: true}, fragmentInfo{}, false)
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
		app.pruneDirCredentials(context.Background(), testBackend(t, app), pinID, constants.ToolCodex, nil, fragmentInfo{}, false)
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
		lines = app.pruneDirCredentials(context.Background(), testBackend(t, app), pinID, "", nil, fragmentInfo{}, false)
	})

	if len(lines) != 0 {
		t.Fatalf("an absent item must not be reported as removed: %v", lines)
	}
	if args := strings.Join(fake.Args, " "); strings.Contains(args, "delete-generic-password") {
		t.Fatalf("nothing to delete, yet a delete ran: %v", fake.Args)
	}
}

// captureClaudeFromKeychain captures an account on the darwin driver, where the
// live credential comes from the keychain double rather than a file, with a known
// expiry so a later copy can be put ahead of it.
func captureClaudeFromKeychain(t *testing.T, app *App, sim *keychainSim, accountName, token string, expiresAt time.Time) {
	t.Helper()
	seedClaude(t, app, token, accountName+"-uuid")
	// account "" makes the double match whatever account claude's own rule derives for
	// the environment under test — hardcoding one made a capture fail with auth_missing
	// wherever that rule falls back (no USER in the env), which reads as a broken test
	// rather than a wrong fixture.
	sim.present, sim.account = true, ""
	sim.payload = claudeOAuthPayload(token, expiresAt)
	code, out := captureStdout(t, func() int {
		return runCapture(context.Background(), app, commonOpts{Format: formatText},
			constants.ToolClaude, accountName)
	})
	mustExit(t, constants.ExitOK, code, out)
}

// Deleting a per-directory keychain item is unrecoverable — a re-pin rebuilds a
// store's credential from the account snapshot, so a copy that was never harvested
// into one is simply gone — and that item can hold the newest copy of the account's
// credential, the only one that can still refresh. So the sweep harvests before it
// deletes. An isolated store names its own account: kae composed the path from it.
func TestPruneDirCredentialsHarvestsBeforeDeleting(t *testing.T) {
	sim := &keychainSim{}
	runner.With(sim, func() {
		app := testApp(t, map[string]string{"USER": "me"})
		app.Env.GOOS = "darwin"
		ctx := context.Background()
		now := app.Now()
		captureClaudeFromKeychain(t, app, sim, "main", mainToken, now.Add(time.Hour))

		pinID := paths.PinID(t.TempDir())
		stale := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "main")
		mkdirs(t, stale)
		writeFile(t, filepath.Join(stale, ".claude.json"), claudeIdentityFile("main-uuid"))
		// The store as the tool left it: refreshed in place, ahead of the snapshot.
		const refreshed = "sk-ant-oat01-MAIN-REFRESHED-cccc"
		sim.payload = claudeOAuthPayload(refreshed, now.Add(8*time.Hour))

		be := testBackend(t, app)
		var lines []string
		_, stderr := captureStderr(t, func() int {
			lines = app.pruneDirCredentials(ctx, be, pinID, "", nil, fragmentInfo{}, false)
			return 0
		})

		if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); !strings.Contains(got, refreshed) {
			t.Fatalf("the item was deleted without harvesting what it held: %s", got)
		}
		if !strings.Contains(stderr, "harvested") {
			t.Fatalf("the harvest must be reported: %q", stderr)
		}
		if !strings.Contains(strings.Join(sim.ops, ","), "delete") {
			t.Fatalf("a harvested item must still be swept: %v", sim.ops)
		}
		if len(lines) != 1 || !strings.Contains(lines[0], stale) {
			t.Fatalf("the removal must be reported: %v", lines)
		}
	})
}

// A live copy kae cannot **date** is not one it has nothing to lose by deleting. This is
// the fourth consumer of the same predicate the recapture guards split on, and the only
// one where folding "nothing to lose" together with "kae cannot tell" licenses a
// **delete**: readLiveCredential classified `Known && !Revoked &&` undated as
// `liveNothing`, so harvestBeforeDelete removed the item without harvesting it and
// without a word, reporting it as a *superseded* credential.
//
// The fixture is TestPruneDirCredentialsHarvestsBeforeDeleting's, with one difference: the
// payload's `expiresAt` is a string. That is what an upstream type change produces, and
// what a comment here used to call unobservable.
func TestPruneDirCredentialsKeepsACopyItCannotDate(t *testing.T) {
	sim := &keychainSim{}
	runner.With(sim, func() {
		app := testApp(t, map[string]string{"USER": "me"})
		app.Env.GOOS = "darwin"
		ctx := context.Background()
		now := app.Now()
		captureClaudeFromKeychain(t, app, sim, "main", mainToken, now.Add(time.Hour))

		pinID := paths.PinID(t.TempDir())
		stale := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "main")
		mkdirs(t, stale)
		writeFile(t, filepath.Join(stale, ".claude.json"), claudeIdentityFile("main-uuid"))
		const live = "sk-ant-oat01-MAIN-UNDATED-dddd"
		sim.payload = `{"claudeAiOauth":{"accessToken":"` + live +
			`","refreshToken":"r","expiresAt":"1814400000000"}}`

		be := testBackend(t, app)
		var lines []string
		_, stderr := captureStderr(t, func() int {
			lines = app.pruneDirCredentials(ctx, be, pinID, "", nil, fragmentInfo{}, false)
			return 0
		})

		if strings.Contains(strings.Join(sim.ops, ","), "delete") {
			t.Fatalf("a live copy kae cannot date must be kept, not deleted: %v", sim.ops)
		}
		if len(lines) != 0 {
			t.Fatalf("nothing was removed, so nothing may be reported as removed: %v", lines)
		}
		// Kept for a reason the user can act on — this is the only offline signal of an
		// upstream format change on this path.
		if !strings.Contains(stderr, "cannot read or date the claude credential") {
			t.Fatalf("keeping it must say why: %q", stderr)
		}
		// And the snapshot is untouched: kae could not order the two, so it may not adopt
		// the copy either.
		if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); !strings.Contains(got, mainToken) {
			t.Fatalf("a copy kae cannot date must not be adopted either: %s", got)
		}
	})
}

// The delete half of the decodability gate, which is the outcome that matters most:
// an item that would previously have been harvested and swept is now **kept**.
//
// Two identity payloads that are byte-identical and neither an account record used to
// confirm the store (identityDiffers returned false, so nothing refused), which let the
// sweep harvest and then delete. They agree about nothing, so the attribution is not
// there, and an item kae cannot attribute is left in place — a leftover secret rather
// than a login destroyed by a cleanup, the rule this whole sweep is built on.
//
// Written because the review round that would have measured this seam did not finish:
// the write path's version of the same change is
// TestWriteDirCredentialRefusesTwoIdentitiesThatAreNotAccountRecords, and "an item that
// used to be deleted is now kept" is the other half, in the direction that cannot be
// undone.
func TestPruneDirCredentialsKeepsAnItemItCannotAttributeToARecord(t *testing.T) {
	sim := &keychainSim{}
	runner.With(sim, func() {
		app := testApp(t, map[string]string{"USER": "me"})
		app.Env.GOOS = "darwin"
		ctx := context.Background()
		now := app.Now()
		const nonRecord = `{"oauthAccount":null,"projects":{"/repo":{}}}`
		// Captured with a live identity cache that is well-formed JSON and not an account
		// record, so that is what the snapshot records. Written after the seed inside
		// captureClaudeFromKeychain would overwrite it, so the capture is done by hand.
		seedClaude(t, app, mainToken, "main-uuid")
		writeFile(t, filepath.Join(app.Env.Home, ".claude.json"), nonRecord)
		sim.present, sim.account = true, ""
		sim.payload = claudeOAuthPayload(mainToken, now.Add(time.Hour))
		code, out := captureStdout(t, func() int {
			return runCapture(ctx, app, commonOpts{Format: formatText}, constants.ToolClaude, "main")
		})
		mustExit(t, constants.ExitOK, code, out)

		pinID := paths.PinID(t.TempDir())
		stale := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "main")
		mkdirs(t, stale)
		writeFile(t, filepath.Join(stale, ".claude.json"), nonRecord)
		// Newer than the snapshot, so the sweep reaches attribution at all.
		const refreshed = "sk-ant-oat01-MAIN-REFRESHED-eeee"
		sim.payload = claudeOAuthPayload(refreshed, now.Add(8*time.Hour))
		sim.ops = nil

		be := testBackend(t, app)
		var lines []string
		_, stderr := captureStderr(t, func() int {
			lines = app.pruneDirCredentials(ctx, be, pinID, "", nil, fragmentInfo{}, false)
			return 0
		})

		if strings.Contains(strings.Join(sim.ops, ","), "delete") {
			t.Fatalf("an item kae cannot attribute must not be deleted: %v", sim.ops)
		}
		if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); strings.Contains(got, refreshed) {
			t.Fatalf("it must not be harvested either, on two sides agreeing about nothing: %s", got)
		}
		if len(lines) != 0 {
			t.Fatalf("nothing was removed, so nothing may be reported: %v", lines)
		}
		if !strings.Contains(stderr, "kae cannot read the identity records it would compare") {
			t.Fatalf("keeping it must name the reason: %q", stderr)
		}
		if strings.Contains(stderr, refreshed) {
			t.Fatalf("a credential must never reach a message: %q", stderr)
		}
	})
}

// The shared mechanism's store records no account in its path — one directory
// serves every account this pin ever bound there — so the only thing that can name
// the account behind its credential is the binding being replaced. That is why the
// sweep is handed the previous fragment, and why the callers read it before
// overwriting or removing it.
func TestPruneDirCredentialsHarvestsSharedStoreFromPreviousBinding(t *testing.T) {
	sim := &keychainSim{}
	runner.With(sim, func() {
		app := testApp(t, map[string]string{"USER": "me"})
		app.Env.GOOS = "darwin"
		ctx := context.Background()
		now := app.Now()
		captureClaudeFromKeychain(t, app, sim, "main", mainToken, now.Add(time.Hour))

		pinID := paths.PinID(t.TempDir())
		shared := app.Paths.SharedDir(pinID, constants.ToolClaude)
		mkdirs(t, shared)
		writeFile(t, filepath.Join(shared, ".claude.json"), claudeIdentityFile("main-uuid"))
		const refreshed = "sk-ant-oat01-MAIN-REFRESHED-cccc"
		sim.payload = claudeOAuthPayload(refreshed, now.Add(8*time.Hour))

		be := testBackend(t, app)
		prev := fragmentInfo{Mode: modeShared, Accounts: map[string]string{constants.ToolClaude: "main"}}
		lines := app.pruneDirCredentials(ctx, be, pinID, "", nil, prev, false)

		if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); !strings.Contains(got, refreshed) {
			t.Fatalf("the shared store's credential was not harvested: %s", got)
		}
		if len(lines) != 1 {
			t.Fatalf("the removal must be reported: %v", lines)
		}
	})
}

// And when nothing can name the account — no account in the path and no readable
// binding to ask — the item stays. A leftover secret is a smaller fault than a
// cleanup that destroys the last copy of a login, and kae must not adopt a payload
// it cannot attribute into some other account's snapshot to avoid that.
func TestPruneDirCredentialsKeepsUnattributableCredential(t *testing.T) {
	sim := &keychainSim{}
	runner.With(sim, func() {
		app := testApp(t, map[string]string{"USER": "me"})
		app.Env.GOOS = "darwin"
		ctx := context.Background()
		now := app.Now()
		captureClaudeFromKeychain(t, app, sim, "main", mainToken, now.Add(time.Hour))

		pinID := paths.PinID(t.TempDir())
		shared := app.Paths.SharedDir(pinID, constants.ToolClaude)
		mkdirs(t, shared)
		writeFile(t, filepath.Join(shared, ".claude.json"), claudeIdentityFile("main-uuid"))
		sim.payload = claudeOAuthPayload("sk-ant-oat01-UNKNOWN-eeee", now.Add(8*time.Hour))
		sim.ops = nil

		be := testBackend(t, app)
		var lines []string
		_, stderr := captureStderr(t, func() int {
			// A previous binding kae could not read: the zero fragment attributes nothing.
			lines = app.pruneDirCredentials(ctx, be, pinID, "", nil, fragmentInfo{}, false)
			return 0
		})

		if strings.Contains(strings.Join(sim.ops, ","), "delete") {
			t.Fatalf("an unattributable credential must not be deleted: %v", sim.ops)
		}
		if len(lines) != 0 {
			t.Fatalf("nothing was removed, so nothing may be reported: %v", lines)
		}
		if !strings.Contains(stderr, "cannot tell which account") {
			t.Fatalf("keeping it must be explained: %q", stderr)
		}
		if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); strings.Contains(got, "UNKNOWN") {
			t.Fatalf("an unattributable payload was adopted into a snapshot: %s", got)
		}
	})
}

// The mode gate on where a shared store's account comes from. A shared store can be
// left over from a binding *older* than the one being replaced — kae keeps a store
// so a re-pin restores its sessions — and taking the account from a fragment that
// was in isolated mode would attribute it to whichever account that binding named.
// Filing one account's token under another's is undetectable afterwards, so the
// answer here is "kae cannot tell", not a guess. Dropping the gate survived the
// whole suite (execution-type review, 2026-08-04).
func TestPruneDirCredentialsWillNotAttributeASharedStoreFromAnIsolatedBinding(t *testing.T) {
	sim := &keychainSim{}
	runner.With(sim, func() {
		app := testApp(t, map[string]string{"USER": "me"})
		app.Env.GOOS = "darwin"
		ctx := context.Background()
		now := app.Now()
		captureClaudeFromKeychain(t, app, sim, "main", mainToken, now.Add(time.Hour))

		pinID := paths.PinID(t.TempDir())
		shared := app.Paths.SharedDir(pinID, constants.ToolClaude)
		mkdirs(t, shared)
		writeFile(t, filepath.Join(shared, ".claude.json"), claudeIdentityFile("main-uuid"))
		sim.payload = claudeOAuthPayload("sk-ant-oat01-LEFTOVER-eeee", now.Add(8*time.Hour))
		sim.ops = nil

		be := testBackend(t, app)
		// The binding being replaced was isolated, so it says nothing about who owns a
		// leftover *shared* store.
		prev := fragmentInfo{Mode: modeIsolated, Accounts: map[string]string{constants.ToolClaude: "main"}}
		var lines []string
		_, stderr := captureStderr(t, func() int {
			lines = app.pruneDirCredentials(ctx, be, pinID, "", nil, prev, false)
			return 0
		})

		if strings.Contains(strings.Join(sim.ops, ","), "delete") {
			t.Fatalf("an unattributable credential must not be deleted: %v", sim.ops)
		}
		if len(lines) != 0 {
			t.Fatalf("nothing was removed, so nothing may be reported: %v", lines)
		}
		if !strings.Contains(stderr, "cannot tell which account") {
			t.Fatalf("the refusal must name its reason: %q", stderr)
		}
		if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); strings.Contains(got, "LEFTOVER") {
			t.Fatalf("a leftover store's credential was filed under main: %s", got)
		}
	})
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

// `--purge` is the way out for a copy kae cannot judge, and a bind's sweep still is not.
// Keeping it during housekeeping is right — it may be a working login in a shape kae has
// not been taught — but keeping it under `--purge` stranded a secret **nothing kae offers
// can remove**: a per-directory item is addressable only from the string kae hashes its
// service name from, and this sweep is the only path to it. Both arms say what they do.
func TestPurgeIsTheWayOutForACredentialKaeCannotJudge(t *testing.T) {
	for _, tc := range []struct {
		name        string
		purging     bool
		wantDeleted bool
		wantSays    string
	}{
		{"a bind's sweep keeps it and names the way out", false, false, "kae unpin --purge in that directory removes it"},
		{"--purge takes it and says what it destroys", true, true, "nor tell which account it belonged to"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, pinID := prunableApp(t)
			stale := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "side")
			bound := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "main")
			mkdirs(t, stale, bound)

			// Unreadable at all: not JSON kae's parser recognizes as a credential.
			fake := &runnertest.Fake{Stdout: `{"somethingElse":true}`, Code: 0}
			var lines []string
			var stderr string
			runner.With(fake, func() {
				_, stderr = captureStderr(t, func() int {
					lines = app.pruneDirCredentials(context.Background(), testBackend(t, app), pinID, "",
						map[string]bool{bound: true}, fragmentInfo{}, tc.purging)
					return 0
				})
			})
			deleted := strings.Contains(strings.Join(fake.Args, " "), "delete-generic-password")
			if deleted != tc.wantDeleted {
				t.Fatalf("deleted=%v want %v (args %v, stderr %q)", deleted, tc.wantDeleted, fake.Args, stderr)
			}
			if !strings.Contains(stderr, tc.wantSays) {
				t.Errorf("want stderr to contain %q: %q", tc.wantSays, stderr)
			}
			if tc.wantDeleted && (len(lines) != 1 || !strings.Contains(lines[0], stale)) {
				t.Errorf("a purge that removed it must report it: %v", lines)
			}
			if !tc.wantDeleted && len(lines) != 0 {
				t.Errorf("nothing was removed, so nothing may be reported: %v", lines)
			}
		})
	}
}
