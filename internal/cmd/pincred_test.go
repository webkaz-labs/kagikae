package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/config"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/freshness"
	"github.com/webkaz-labs/kagikae/internal/paths"
	"github.com/webkaz-labs/kagikae/internal/secret"
)

// deadClaudeCred is a claude credential past every deadline: access token expired
// in 2020, refresh token in 2021. Named because six tests age a store's credential
// to exactly this state, and six copies of one payload is six chances for one to
// drift into meaning something else.
const deadClaudeCred = `{"claudeAiOauth":{"accessToken":"a","refreshToken":"r","expiresAt":1577836800000,"refreshTokenExpiresAt":1609459200000}}`

// refreshBackedClaudeCred is the oauth object of the realistic claude shape: a refresh
// token present, so the deadline kae judges is the login's own (`refreshTokenExpiresAt`)
// while the access token is long past, which is the normal state of a stored snapshot.
// in is measured from now to that login deadline; a negative value makes a spent one.
// Four tests built this by hand with the same template, for the reason deadClaudeCred
// already exists one line up.
func refreshBackedClaudeCred(now time.Time, in time.Duration) string {
	return fmt.Sprintf(
		`{"accessToken":"a","refreshToken":"r","expiresAt":1577836800000,"refreshTokenExpiresAt":%d}`,
		now.Add(in).UnixMilli(),
	)
}

// endOfLifeClaudeCred is the oauth object of a claude credential with **no refresh
// token**, so its access-token expiry is the whole deadline — cursor's shape, and the
// simplest fixture for exercising the lead-time band without a second timestamp in
// play. (A refresh-backed credential is judged on its login deadline the same way;
// TestRefreshBackedLoginDeadlineInTheBandWarns covers that side.) Six tests across
// five files build this with different tokens and horizons, which is one shape in six
// places — the same reason deadClaudeCred exists.
func endOfLifeClaudeCred(now time.Time, in time.Duration, token string) string {
	return fmt.Sprintf(`{"accessToken":%q,"refreshToken":"","expiresAt":%d}`,
		token, now.Add(in).UnixMilli())
}

// pinWithCapturedClaude captures a healthy claude/main, binds a temp directory to
// it in mode, and returns the bound directory plus the path of the credential copy
// that directory now owns. The tool refreshes *that* copy in place, which is why it
// can go stale while the account snapshot stays fine.
//
// The store path differs per mode — one directory per pin×tool for shared, a
// per-account one for isolated — which is the branch boundStoreDir picks, so the
// mode is a parameter rather than a second copy of this fixture.
func pinWithCapturedClaude(t *testing.T, app *App, mode string) (dir, storeDir, credFile string) {
	t.Helper()
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	// Deadline in 2096: healthy, so nothing is reported until a test ages it.
	seedClaudeOAuth(t, app,
		`{"accessToken":"a","refreshToken":"r","expiresAt":1577836800000,"refreshTokenExpiresAt":4000000000000}`)
	if code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "main") }); code != constants.ExitOK {
		t.Fatalf("capture claude/main: %s", out)
	}
	return pinAndResolveClaudeStore(t, app, mode)
}

// pinAndResolveClaudeStore binds a temp directory to claude/main and answers where that
// binding's credential actually is. Shared by the two fixtures above, which differ only in
// how the account was captured: the store path derivation is the thing dirCredFile's own
// doc says must not be kept by hand in two places.
//
// The path differs per mode — one directory per pin×tool for shared, a per-account one for
// isolated — which is the branch boundStoreDir picks, so the mode is a parameter rather
// than a second copy. Going through pinHereAs is what keeps a test's pin off the real
// keychain; that helper carries what calling runPin directly once cost.
func pinAndResolveClaudeStore(t *testing.T, app *App, mode string) (dir, storeDir, credFile string) {
	t.Helper()
	dir = pinHereAs(t, app, "main", mode)
	storeDir = app.Paths.SharedDir(paths.PinID(dir), constants.ToolClaude)
	if mode == modeIsolated {
		storeDir = app.Paths.IsolatedConfigDir(paths.PinID(dir), constants.ToolClaude, "main")
	}
	credFile = dirCredFile(app, constants.ToolClaude, "main", storeDir)
	if readFile(t, credFile) == "" {
		t.Fatalf("pin (%s) did not materialize a credential at %s", mode, credFile)
	}
	return dir, storeDir, credFile
}

// pinWithIdentifiedClaude is pinWithCapturedClaude for a test whose account has to be
// **attributable**, with a credential dated relative to `now` so the test can put a
// directory's copy ahead of the snapshot.
//
// The difference is one thing and it is not cosmetic: this captures through
// captureClaudeAt, whose seed carries the `/oauthAccount` object claude writes, so the
// snapshot records the identity-only artifact `sharedStoreAttribution` reads.
// pinWithCapturedClaude's account records none, so every harvest against it refuses with
// "no oauth_account identity is recorded for that account" — which makes that fixture the
// right one for testing a refusal and the wrong one for anything that must succeed.
// Measured 2026-08-16, by writing a harvest test on the wrong one and watching it stay
// green against both a broken kae and a fixed one.
func pinWithIdentifiedClaude(t *testing.T, app *App, mode string) (dir, storeDir, credFile string) {
	t.Helper()
	captureClaudeAt(t, app, "main", mainToken, app.Now().Add(time.Hour))
	dir, storeDir, credFile = pinAndResolveClaudeStore(t, app, mode)
	// The reader half of attribution: without a label in the store this fixture would be
	// evidence about nothing, exactly as bindClaudeHere's own control says.
	if readFile(t, filepath.Join(storeDir, ".claude.json")) == "" {
		t.Fatalf("pin (%s) left no identity cache in %s", mode, storeDir)
	}
	return dir, storeDir, credFile
}

// A bound directory holds its own copy of the credential and the tool refreshes
// that copy in place, so it can die while every account snapshot kae has still
// looks fine. credential_stale reads snapshots, so nothing reported this at all —
// the first signal was the tool refusing to start in that directory.
func TestDoctorReportsStaleBoundDirectoryCredential(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	dir, _, credFile := pinWithCapturedClaude(t, app, modeShared)

	// The directory's copy died; the account snapshot is untouched and healthy.
	writeFile(t, credFile,
		deadClaudeCred)

	report := buildDoctor(ctx, app, "", false)
	if report.SchemaVersion != constants.SchemaVersion {
		t.Fatalf("schema_version changed: %d", report.SchemaVersion)
	}
	msg, ok := findCheck(report, constants.CheckCredentialStale)
	if !ok {
		t.Fatalf("expected a credential_stale check for the bound directory, got %+v", report.Checks)
	}
	// The remedy is `kae relogin` and not the tool's own login command: that one is
	// right only in a shell where the pin is active, and in any other it refreshes
	// the real home instead (pinLoginRemedy).
	for _, want := range []string{"bound to " + dir, "refresh token expired", "cd " + dir, "kae relogin claude"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q: %q", want, msg)
		}
	}
	// The remedy must be the login *inside* the directory, never a re-bind: the
	// account snapshot it would re-copy can be just as dead as this copy.
	if strings.Contains(msg, "kae pin") || strings.Contains(msg, "kae add") {
		t.Errorf("a bound-directory credential is not fixed by re-binding or re-capturing: %q", msg)
	}
	// Warn-level, so a dead bound credential never turns `kae doctor` into a
	// non-zero exit — which would break the mise enter hook.
	for _, c := range report.Checks {
		if c.Code == constants.CheckCredentialStale && c.Status != constants.StatusWarn {
			t.Fatalf("credential_stale must be warn-level, got %q", c.Status)
		}
	}
	// The healthy account snapshot must not be dragged in with it.
	for _, c := range report.Checks {
		if c.Code == constants.CheckCredentialStale && strings.Contains(c.Message, "snapshot \"main\"") {
			t.Errorf("the account snapshot is healthy and must not be reported: %q", c.Message)
		}
	}
}

// The lead-time half, in a bound directory: still working, deadline days away.
func TestDoctorReportsExpiringBoundDirectoryCredential(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	dir, _, credFile := pinWithCapturedClaude(t, app, modeShared)

	writeFile(t, credFile, `{"claudeAiOauth":`+endOfLifeClaudeCred(app.Now(), 5*24*time.Hour, "a")+`}`)

	report := buildDoctor(ctx, app, "", false)
	msg, ok := findCheck(report, constants.CheckCredentialExpiring)
	if !ok {
		t.Fatalf("expected a credential_expiring check for the bound directory, got %+v", report.Checks)
	}
	for _, want := range []string{"bound to " + dir, "5 day(s)", "cd " + dir} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q: %q", want, msg)
		}
	}
	if _, stale := findCheck(report, constants.CheckCredentialStale); stale {
		t.Error("a credential that still works must not also be reported stale")
	}
}

// A healthy bound credential says nothing.
func TestHealthyBoundDirectoryCredentialIsSilent(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	pinWithCapturedClaude(t, app, modeShared)

	if checks := app.pinCredentialChecks(ctx, app.boundDirStores()); len(checks) != 0 {
		t.Fatalf("a healthy bound credential must be silent, got %+v", checks)
	}
}

// `kae unpin` keeps the store on purpose so a re-pin restores its sessions — but
// nothing in that directory points at it any more. Reporting its credential would
// claim "bound to" about a directory that is not bound, and send the user to a
// login that lands somewhere the tool will no longer read. pinChecks skips an
// unpinned directory for the same reason.
//
// The credential is aged *after* unpinning here on purpose: with a healthy one it
// would say nothing about unpinning at all. What the test does **not** pin is the
// `!exists` branch of the fragment read — measured 2026-08-04: removing it changes
// nothing, because a directory with no fragment parses to an empty account map and
// boundStoreDir already answers "not bound" from that. What it does pin is the
// choice of source: reading the store *tree* instead of the fragment makes this fail
// (also measured). Keep the branch as the statement of intent it is, and do not
// expect a test to kill it. AGENTS.md carries the full list of which guards in that
// walk are killable and which converge.
func TestUnpinnedDirectoryCredentialIsNotReported(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	_, _, credFile := pinWithCapturedClaude(t, app, modeShared)

	code, out := captureStdout(t, func() int { return runUnpin(ctx, app, commonOpts{Format: formatText}, false) })
	mustExit(t, constants.ExitOK, code, out)
	writeFile(t, credFile,
		deadClaudeCred)

	if checks := app.pinCredentialChecks(ctx, app.boundDirStores()); len(checks) != 0 {
		t.Fatalf("an unpinned directory's kept store must stay silent even when stale, got %+v", checks)
	}
}

// The same rule one level down: re-binding a single tool leaves the previously
// bound tools' stores on disk, and those are no longer what the directory points
// at. Only what the fragment binds *now* may be reported, so the sweep reads the
// fragment rather than walking the store tree.
func TestUnboundToolsStoreInABoundDirectoryIsNotReported(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	dir, _, credFile := pinWithCapturedClaude(t, app, modeShared)

	// Re-bind this directory to codex alone; claude's store stays on disk.
	seedCodex(t, app, "codex-main")
	if code, out := captureStdout(t, func() int {
		return runCapture(ctx, app, commonOpts{Format: formatText}, "codex", "main")
	}); code != constants.ExitOK {
		t.Fatalf("capture codex/main: %s", out)
	}
	app.Config.Profiles = map[string]config.Profile{
		"main": {Accounts: map[string]string{constants.ToolCodex: "main"}},
	}
	if code, out := captureStdout(t, func() int {
		return runPin(ctx, app, commonOpts{Format: formatText}, "main", modeShared)
	}); code != constants.ExitOK {
		t.Fatalf("re-pin to codex only: %s", out)
	}

	// claude's leftover store dies; the directory no longer binds claude.
	writeFile(t, credFile,
		deadClaudeCred)

	for _, c := range app.pinCredentialChecks(ctx, app.boundDirStores()) {
		if c.Tool == constants.ToolClaude {
			t.Fatalf("%s no longer binds claude; its leftover store must not be reported: %q", dir, c.Message)
		}
	}
}

// A directory that no longer exists is pinChecks' orphaned-store finding. Naming
// it again here would report one problem twice, and the credential of a store
// nothing can reach is moot.
func TestDeletedBoundDirectoryCredentialIsNotReportedTwice(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	dir, _, credFile := pinWithCapturedClaude(t, app, modeShared)
	writeFile(t, credFile,
		deadClaudeCred)

	chdirTemp(t) // step out before removing it
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if checks := app.pinCredentialChecks(ctx, app.boundDirStores()); len(checks) != 0 {
		t.Fatalf("a deleted directory is pinChecks' finding only, got %+v", checks)
	}
	if _, ok := findCheck(buildDoctor(ctx, app, "", false), constants.CheckPinStale); !ok {
		t.Fatal("pinChecks must still report the orphaned store")
	}
}

// The bound-directory sweep is not per-tool, so `kae doctor <tool>` skips it —
// same rule as pinChecks and the companion checks, and doctor says so on stderr.
func TestFilteredDoctorSkipsBoundDirectoryCredentials(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	_, _, credFile := pinWithCapturedClaude(t, app, modeShared)
	writeFile(t, credFile,
		deadClaudeCred)

	for _, c := range buildDoctor(ctx, app, constants.ToolClaude, false).Checks {
		if strings.Contains(c.Message, "bound to ") {
			t.Fatalf("a filtered run must not include the bound-directory sweep: %q", c.Message)
		}
	}
}

// A new output path built from a live credential payload. AGENTS.md requires a
// redaction test for each one, and this is the only one that reads a *live* store
// rather than a snapshot, so it is where a raw payload is closest to the message.
func TestBoundDirectoryCredentialMessageNeverCarriesTheToken(t *testing.T) {
	const canary = "sk-ant-oat01-PIN-CANARY-iiii"
	app := overlayTestApp(t)
	ctx := context.Background()
	_, _, credFile := pinWithCapturedClaude(t, app, modeShared)

	for _, payload := range []string{
		// stale, expiring, and the tombstone, which is the branch that names no dates
		// and so has the least structure holding it back from echoing the payload.
		`{"claudeAiOauth":{"accessToken":"` + canary + `","refreshToken":"` + canary +
			`-r","expiresAt":1577836800000,"refreshTokenExpiresAt":1609459200000}}`,
		`{"claudeAiOauth":{"accessToken":"` + canary + `","refreshToken":"` + canary +
			`-r","expiresAt":1577836800000,"refreshTokenExpiresAt":` +
			strconv.FormatInt(app.Now().Add(2*24*time.Hour).UnixMilli(), 10) + `}}`,
		`{"claudeAiOauth":{"accessToken":"","refreshToken":"","expiresAt":0,"note":"` + canary + `"}}`,
	} {
		writeFile(t, credFile, payload)
		for _, format := range []string{formatText, formatJSON} {
			_, stdout, stderr := captureBoth(t, func() int {
				return runDoctor(ctx, app, commonOpts{Format: format}, "")
			})
			if strings.Contains(stdout+stderr, canary) {
				t.Fatalf("doctor (%s) leaked a bound credential value:\n%s\n%s", format, stdout, stderr)
			}
		}
	}
}

// claudeOAuthAccount is the /oauthAccount object every claude identity fixture in this
// package is built from: the live cache seedClaude writes, the store-side
// claudeIdentityFile, and boundIdentity below.
//
// One template, because these fixtures are compared **against each other** and the
// comparison is keyed on claude's IdentityKeys (accountUuid, emailAddress,
// organizationUuid). Two templates that both omitted `organizationUuid` agreed on it by
// omission and looked fine; the moment one of them gained the key the pair began
// reporting a conflict, which is how the coupling was found. A key added upstream now
// has one place to be added here.
func claudeOAuthAccount(uuid, email string) string {
	return fmt.Sprintf(`{"accountUuid":%q,"emailAddress":%q,"organizationUuid":"org-1"}`, uuid, email)
}

// The template has to carry every key the comparison reads, and that is asserted
// rather than assumed — because the defect one template was introduced to prevent was
// not two fixtures *diverging*, it was two fixtures **agreeing by omission**. Both had
// lost `organizationUuid`, so fourteen tests compared two thirds of the evidence and
// looked fine. Consolidating makes divergence impossible (a test catches it — measured)
// but makes the omission reachable in one edit instead of two, with nothing failing.
// This is the check for that: the fixture is resolved against the adapter's own
// IdentityKeys, so a key added upstream cannot be added to the spec and forgotten here.
func TestIdentityFixtureCarriesEveryIdentityKey(t *testing.T) {
	app := testApp(t, nil)
	specs, err := app.dirSpecs(context.Background(), constants.ToolClaude, bindDirs{Config: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	// Decoded through the same helper the production comparison uses, so this guard sees
	// the payload exactly as identityDiffers does. A substring search on the key name is
	// not enough: `"organizationUuid":null` or `""` keeps the name and still contributes
	// nothing, because identityDiffers compares the raw values a map lookup returns and
	// a present-but-empty key agrees as trivially on both sides as an absent one.
	obj, ok := freshness.DecodeObject([]byte(claudeOAuthAccount("main-uuid", "you@example.com")))
	if !ok {
		t.Fatalf("the fixture must decode as an account record: %s",
			claudeOAuthAccount("main-uuid", "you@example.com"))
	}
	seen := 0
	for _, sp := range specs {
		if !sp.IdentityOnly {
			continue
		}
		seen++
		for _, key := range sp.IdentityKeys {
			// Every identity key claude declares is a string today. A non-string one would
			// fail here rather than pass silently, which is the right direction: it needs a
			// deliberate look at what "carries evidence" means for that type.
			//
			// Known limit, if this is ever touched again: a whitespace-only placeholder
			// (`" "`) passes, and two fixtures sharing one would agree for free exactly as
			// `null` and `""` did. Left because it is not a shape a dropped field produces,
			// which is the mechanism this guard is about, and the only caller passes literals.
			if !freshness.NonEmptyString(obj[key]) {
				t.Errorf("claudeOAuthAccount does not carry a value for %q, one of claude's "+
					"IdentityKeys: every fixture built from it then agrees on that key for free — %s",
					key, claudeOAuthAccount("main-uuid", "you@example.com"))
			}
		}
	}
	// A guard that reached no spec checks nothing, which is how this package's sibling
	// keychain guard came to skip codex entirely.
	if seen == 0 {
		t.Fatal("this guard saw no IdentityOnly spec, so it would pass vacuously")
	}
}

// Several directories can bind one account, and the payload the identity check compares
// against is that **account's** recorded identity — one ref, however many directories.
// Reading it per directory is one `security` subprocess each on darwin. Same assertion,
// for the same reason, as TestSwitchReadsTargetSnapshotOnce on the switch path.
//
// The positive half is not decoration: without it a version that found nothing at all
// would satisfy the count.
func TestBoundDirectoryIdentityReadsOneAccountSnapshotOnce(t *testing.T) {
	app, _, firstIdentity := pinIdentityApp(t, modeShared)
	ctx := context.Background()

	second := pinHere(t, app, modeShared)
	secondIdentity := filepath.Join(
		app.Paths.SharedDir(paths.PinID(second), constants.ToolClaude), ".claude.json",
	)
	drifted := boundIdentity("side-uuid", "side@example.com")
	writeFile(t, firstIdentity, drifted)
	writeFile(t, secondIdentity, drifted)

	fileBE, err := secret.Resolve(secret.BackendFile, app.Env.GOOS, app.Paths.SecretsDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	counter := &countingBackend{Backend: fileBE, gets: map[string]int{}}
	checks := app.pinIdentityChecks(ctx, counter, app.boundDirStores())
	if len(checks) != 2 {
		t.Fatalf("both bound directories must be reported, or the count below means nothing: %+v", checks)
	}
	ref := account.SecretRef(constants.ToolClaude, "main", "oauth_account")
	if got := counter.gets[ref]; got != 1 {
		t.Fatalf("one account's recorded identity must be read once, not once per bound directory: %s read %d times",
			ref, got)
	}
}

// boundIdentity is the identity cache file a bound directory's store holds, with an
// email independent of the uuid — which the store-versus-snapshot comparisons need,
// since they turn on naming a *different* account.
func boundIdentity(uuid, email string) string {
	return `{"oauthAccount":` + claudeOAuthAccount(uuid, email) + `,"projects":{}}`
}

// pinIdentityApp captures claude/main from a login that has an /oauthAccount
// identity as well as a credential, binds a temp directory to it, and returns the
// bound directory plus the identity cache that directory now owns.
//
// The identity is what attributes a store to an account, so every bound-directory
// identity assertion needs one on each side — and the fixture asserts the bind
// actually wrote it. Without that, a test expecting silence would pass because
// dirIdentityConfirms was refusing for missing evidence rather than agreeing, which
// is the same "passes for another reason" trap as an empty grep counting zero.
func pinIdentityApp(t *testing.T, mode string) (app *App, dir, identityFile string) {
	t.Helper()
	app = overlayTestApp(t)
	// Written before the capture: the snapshot records whatever identity was live then.
	claudeJSON(t, app, boundIdentity("main-uuid", "you@example.com"))
	dir, storeDir, _ := pinWithCapturedClaude(t, app, mode)
	// The identity stays in the store dir in either mode. It is asked for separately
	// rather than derived from the credential's path: the credential moved to the
	// account's own store, so "the credential's neighbour" now names a different
	// directory in the mode where it used to be the same one.
	identityFile = filepath.Join(storeDir, ".claude.json")
	if got := readFile(t, identityFile); !strings.Contains(got, "main-uuid") {
		t.Fatalf("the bind must write the bound account's identity into %s, got %q", identityFile, got)
	}
	if checks := app.pinIdentityChecks(context.Background(), testBackend(t, app), app.boundDirStores()); len(checks) != 0 {
		t.Fatalf("a store whose identity matches its binding must be silent, got %+v", checks)
	}
	return app, dir, identityFile
}

// The bound-directory frame of identity_drift: the store's own identity cache names
// an account other than the one the directory binds. The global check cannot see
// this — it compares this shell's live state against state.Active, which is a
// different frame inside a kae-owned isolated home — so nothing reported it.
func TestDoctorReportsBoundDirectoryIdentityNamingAnotherAccount(t *testing.T) {
	app, dir, identityFile := pinIdentityApp(t, modeShared)
	ctx := context.Background()

	// Something logged in inside the directory as another account.
	writeFile(t, identityFile, boundIdentity("side-uuid", "side@example.com"))

	report := buildDoctor(ctx, app, "", false)
	msg, ok := findCheck(report, constants.CheckIdentityDrift)
	if !ok {
		t.Fatalf("expected identity_drift for the bound directory, got %+v", report.Checks)
	}
	// Bound-directory wording, not the global check's: the two share a code, and a
	// message from the wrong frame would otherwise satisfy this test.
	//
	// The remedy is `kae relogin` and not `kae pin`, which this asserted until 2026-08-08:
	// the credential store is the account's, so while a sibling directory still confirms
	// the account the readers disagree, the bind keeps the copy and writes no identity
	// label either — and the finding repeats unchanged after the remedy it gave.
	for _, want := range []string{"claude identity cache in " + dir, "claude/main", "cd " + dir + " && kae relogin claude"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q: %q", want, msg)
		}
	}
	// PII on both sides of the comparison, and neither may reach the report.
	for _, pii := range []string{"side@example.com", "side-uuid", "you@example.com", "main-uuid"} {
		if strings.Contains(msg, pii) {
			t.Errorf("identity payload %q must never reach doctor output: %q", pii, msg)
		}
	}
	// Warn-level, so a drifted label never turns `kae doctor` into a non-zero exit —
	// which would break the mise enter hook. And the row names its tool: a JSON
	// consumer filters on that field, and nothing else in this file asserts it.
	for _, c := range report.Checks {
		if c.Code != constants.CheckIdentityDrift {
			continue
		}
		if c.Status != constants.StatusWarn {
			t.Fatalf("identity_drift must be warn-level, got %q", c.Status)
		}
		if c.Tool != constants.ToolClaude {
			t.Fatalf("identity_drift must name its tool, got %q", c.Tool)
		}
	}
}

// Every drifted binding is reported, not just the first. The loop's whole purpose is
// that one `kae doctor` answers for every bound directory rather than the one you are
// standing in — and a `return` after the first finding survived the entire package
// until this test existed (measured 2026-08-05).
func TestDoctorReportsEveryDriftedBoundDirectory(t *testing.T) {
	app, first, firstIdentity := pinIdentityApp(t, modeShared)
	ctx := context.Background()

	second := pinHere(t, app, modeShared)
	secondIdentity := filepath.Join(
		app.Paths.SharedDir(paths.PinID(second), constants.ToolClaude), ".claude.json",
	)
	if got := readFile(t, secondIdentity); !strings.Contains(got, "main-uuid") {
		t.Fatalf("the second bind must label its own store too, got %q", got)
	}

	drifted := boundIdentity("side-uuid", "side@example.com")
	writeFile(t, firstIdentity, drifted)
	writeFile(t, secondIdentity, drifted)

	checks := app.pinIdentityChecks(ctx, testBackend(t, app), app.boundDirStores())
	named := map[string]bool{}
	for _, c := range checks {
		for _, dir := range []string{first, second} {
			if strings.Contains(c.Message, dir) {
				named[dir] = true
			}
		}
	}
	// Asserted as a **set**: pinnedDirs walks os.ReadDir over pin-id hashes, so the
	// order follows the hash of these two temp paths and flips between runs. Indexing
	// checks[0] would be a test that passes or fails by luck. The row count is asserted
	// alongside it, because a set of two names is also what a duplicated row produces.
	if len(named) != 2 || len(checks) != 2 {
		t.Fatalf("every drifted binding must be reported exactly once; named %v of %v in %+v",
			named, []string{first, second}, checks)
	}
}

// The isolated branch of `boundStoreDir`, on the identity half. A wrong branch reads
// a directory that does not exist and the check goes quietly silent, which is the
// failure that looks exactly like "no problem found" — the same argument
// TestDoctorReportsStaleCredentialInAnIsolatedBoundDirectory makes for the credential
// half. Taking the account from the store *tree* instead of the fragment
// (`storeAccount`) is equivalent in shared mode and answers "" here, so it disables
// the check for every isolated binding and survived the package until this test
// (measured 2026-08-05).
func TestIsolatedBoundDirectoryIdentityDriftIsReported(t *testing.T) {
	app, dir, identityFile := pinIdentityApp(t, modeIsolated)
	ctx := context.Background()

	writeFile(t, identityFile, boundIdentity("side-uuid", "side@example.com"))

	checks := app.pinIdentityChecks(ctx, testBackend(t, app), app.boundDirStores())
	if len(checks) != 1 || !strings.Contains(checks[0].Message, dir) {
		t.Fatalf("an isolated bound directory's drift must be reported too, got %+v", checks)
	}
}

// The account compared against is the one the **fragment** binds, not a constant and
// not the one that happens to be captured. Hard-coding `main` at the account load
// survived the package until this test (measured 2026-08-05), because every other
// fixture binds `main`.
func TestBoundDirectoryIdentityNamesTheAccountTheFragmentBinds(t *testing.T) {
	app, dir, identityFile := pinIdentityApp(t, modeShared)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	// A second account with its own identity, then re-bind this directory to it: the
	// re-bind rewrites the store with side's credential and side's identity.
	claudeJSON(t, app, boundIdentity("side-uuid", "side@example.com"))
	seedClaudeOAuth(t, app,
		`{"accessToken":"s","refreshToken":"r","expiresAt":1577836800000,"refreshTokenExpiresAt":4000000000000}`)
	if code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "side") }); code != constants.ExitOK {
		t.Fatalf("capture claude/side: %s", out)
	}
	if code, out := captureStdout(t, func() int { return runRebind(ctx, app, opts, "claude", "side") }); code != constants.ExitOK {
		t.Fatalf("re-bind to claude/side: %s", out)
	}
	if got := readFile(t, identityFile); !strings.Contains(got, "side-uuid") {
		t.Fatalf("the re-bind must relabel the store with side, got %q", got)
	}

	// The fragment and `state.Active` must **diverge**, or this test proves less than its
	// name: `kae add`/capture sets the active account as a side effect, so after capturing
	// side both sources name side, and a version reading global state instead of the
	// fragment would pass. Point the global selection back at main; the finding below must
	// still be about side.
	if err := app.saveActive(map[string]string{constants.ToolClaude: "main"}, ""); err != nil {
		t.Fatal(err)
	}
	st, err := app.loadState()
	if err != nil {
		t.Fatal(err)
	}
	if st.Active[constants.ToolClaude] != "main" {
		t.Fatalf("this test needs the global account to differ from the binding, got %q",
			st.Active[constants.ToolClaude])
	}

	// Now the store names main while the fragment binds side.
	writeFile(t, identityFile, boundIdentity("main-uuid", "you@example.com"))

	checks := app.pinIdentityChecks(ctx, testBackend(t, app), app.boundDirStores())
	if len(checks) != 1 {
		t.Fatalf("expected one finding for the side-bound directory, got %+v", checks)
	}
	for _, want := range []string{"claude/side", dir} {
		if !strings.Contains(checks[0].Message, want) {
			t.Errorf("message must name %q: %q", want, checks[0].Message)
		}
	}
	if strings.Contains(checks[0].Message, "claude/main") {
		t.Errorf("the binding is to side; main must not appear: %q", checks[0].Message)
	}
}

// A payload kae cannot read as an account record names **no** account, so it is
// missing evidence like an absent one — not proof that the store belongs to somebody
// else. `identityDiffers` falls back to a byte comparison when either side is not a
// JSON object, which is right for the drift comparison itself (two payloads it cannot
// read must not be called equal) and wrong for attribution: before this was split,
// each of these reported "names an account other than claude/main" (measured
// 2026-08-05).
func TestBoundDirectoryIdentityKaeCannotReadIsSilent(t *testing.T) {
	app, _, identityFile := pinIdentityApp(t, modeShared)
	ctx := context.Background()

	// Each is well-formed JSON that ReadLive returns happily, and none is an object —
	// the minimum anomaly that passes the layers above and reaches the comparison.
	for _, payload := range []string{
		`{"oauthAccount":null}`,
		`{"oauthAccount":"main-uuid"}`,
		`{"oauthAccount":7}`,
		`{"oauthAccount":["main-uuid"]}`,
	} {
		writeFile(t, identityFile, payload)
		if checks := app.pinIdentityChecks(ctx, testBackend(t, app), app.boundDirStores()); len(checks) != 0 {
			t.Errorf("%s: a payload naming no account must not be reported as naming another: %+v",
				payload, checks)
		}
	}
}

// The same rule on the **recorded** side, which is the half nothing else covers: the
// gate is symmetric, and dropping the stored condition survived every other test
// (measured 2026-08-05). Silence is right for a reason worth stating — a store that
// properly names another account does not prove it is not this one's when this
// account's own recorded label cannot be read; what is broken there is kae's label,
// not the binding.
//
// It also pins the refusal's reason **string**, which reaches stderr on every harvest
// path. A refusal that reports the wrong reason sends the user to diagnose the wrong
// thing, and here the wrong reason is the same false claim this gate removed from
// doctor: "belongs to an account other than" about a payload that names none.
func TestSnapshotIdentityThatIsNotARecordIsMissingEvidence(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	// Reachable with no hostile input: capture while the live cache holds a payload that
	// is well-formed JSON but not an account record.
	claudeJSON(t, app, `{"oauthAccount":null,"projects":{}}`)
	_, storeDir, credFile := pinWithCapturedClaude(t, app, modeShared)
	identityFile := filepath.Join(storeDir, ".claude.json")

	writeFile(t, identityFile, boundIdentity("side-uuid", "side@example.com"))
	// Newer than the snapshot, so the harvest gets past the expiresAt comparison and
	// reaches attribution at all — otherwise there is no refusal to report.
	writeFile(t, credFile,
		`{"claudeAiOauth":{"accessToken":"NEWER","refreshToken":"r","expiresAt":4100000000000,"refreshTokenExpiresAt":4100000000000}}`)

	if checks := app.pinIdentityChecks(ctx, testBackend(t, app), app.boundDirStores()); len(checks) != 0 {
		t.Fatalf("an unreadable recorded identity proves nothing, got %+v", checks)
	}

	_, _, stderr := captureBoth(t, func() int {
		return runPin(ctx, app, commonOpts{Format: formatText}, "main", modeShared)
	})
	if !strings.Contains(stderr, "kae cannot read the identity records it would compare") {
		t.Fatalf("the refusal must name its own reason: %q", stderr)
	}
	if strings.Contains(stderr, "belongs to an account other than") {
		t.Errorf("a payload naming no account must not be reported as naming another: %q", stderr)
	}
}

// A pre-v0.16.0 shared bind linked the identity cache back out to the real tool home,
// so the payload read there is the *real home's* — it says nothing about whose
// credential the store holds, and it disagrees with the bound account whenever the
// global account differs, which is the ordinary case. Treating that as proof would
// give every such directory a false finding.
func TestBoundDirectoryIdentitySharedWithTheRealHomeIsSilent(t *testing.T) {
	app, _, identityFile := pinIdentityApp(t, modeShared)
	ctx := context.Background()

	// The real home names another account, and the store's cache is a link to it.
	realHome := filepath.Join(app.Env.Home, ".claude.json")
	writeFile(t, realHome, boundIdentity("side-uuid", "side@example.com"))
	if err := os.Remove(identityFile); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realHome, identityFile); err != nil {
		t.Fatal(err)
	}
	// Positive first: the link resolves and holds the conflicting payload, so silence
	// below is the escape guard rather than an unreadable file.
	if got := readFile(t, identityFile); !strings.Contains(got, "side-uuid") {
		t.Fatalf("the link must resolve to the real home's payload, got %q", got)
	}

	if checks := app.pinIdentityChecks(ctx, testBackend(t, app), app.boundDirStores()); len(checks) != 0 {
		t.Fatalf("an identity shared with the real home proves nothing, got %+v", checks)
	}
}

// A store directory that is gone leaves the binding out of the walk entirely.
// Asserted on the walk rather than through either check's silence: on darwin claude's
// per-directory keychain **item** outlives the deleted directory, so without this gate
// the credential half would read that item and report `credential_stale` for a store
// that no longer exists — and tests run with GOOS "linux", where both halves decline
// for the unrelated reason that there is nothing to read.
func TestBoundStoreThatIsGoneLeavesTheWalk(t *testing.T) {
	app, _, identityFile := pinIdentityApp(t, modeShared)

	if len(app.boundDirStores()) != 1 {
		t.Fatalf("the fixture must produce exactly one bound store, got %+v", app.boundDirStores())
	}
	if err := os.RemoveAll(filepath.Dir(identityFile)); err != nil {
		t.Fatal(err)
	}
	if stores := app.boundDirStores(); len(stores) != 0 {
		t.Fatalf("a store directory that is gone must leave the walk, got %+v", stores)
	}
}

// Missing evidence is not drift. A bound directory legitimately has no identity
// cache until its tool runs there, and one bound before v0.16.0 never had one
// written — warning on that would fire on healthy directories, which is how the
// v0.15.0/v0.15.1 freshness warnings became wallpaper.
func TestBoundDirectoryWithNoIdentityCacheIsSilent(t *testing.T) {
	app, _, identityFile := pinIdentityApp(t, modeShared)

	if err := os.Remove(identityFile); err != nil {
		t.Fatal(err)
	}
	if checks := app.pinIdentityChecks(context.Background(), testBackend(t, app), app.boundDirStores()); len(checks) != 0 {
		t.Fatalf("a store with no identity cache proves nothing and must stay silent, got %+v", checks)
	}
}

// The same gate every "bound to <dir>" report needs, inherited from boundDirStores:
// `kae unpin` keeps the store on purpose so a re-pin restores its sessions, but
// nothing in that directory points at it any more. A finding would name a directory
// that is not bound and a re-bind remedy for a binding that no longer exists.
//
// The identity is made to conflict *after* unpinning: with an agreeing one this test
// would say nothing about unpinning. **Isolated** mode on purpose — in shared mode a
// walk of the store tree cannot name the account either (storeAccount answers only
// from a shared-mode fragment), so the account load would decline first and this
// would pass without the binding ever being consulted. An isolated store's path
// carries its account, so the fragment is the only thing left that says the
// directory is no longer bound (both measured, 2026-08-04).
func TestUnpinnedDirectoryIdentityIsNotReported(t *testing.T) {
	app, _, identityFile := pinIdentityApp(t, modeIsolated)
	ctx := context.Background()

	code, out := captureStdout(t, func() int { return runUnpin(ctx, app, commonOpts{Format: formatText}, false) })
	mustExit(t, constants.ExitOK, code, out)
	writeFile(t, identityFile, boundIdentity("side-uuid", "side@example.com"))

	if checks := app.pinIdentityChecks(ctx, testBackend(t, app), app.boundDirStores()); len(checks) != 0 {
		t.Fatalf("an unpinned directory's kept store must stay silent, got %+v", checks)
	}
}

// A new output path built from a live identity payload, which is PII. AGENTS.md
// requires a redaction test for each one; this is the only path that reads an
// identity out of a per-directory store.
func TestBoundDirectoryIdentityMessageNeverCarriesTheIdentity(t *testing.T) {
	const canary = "canary-uuid-do-not-print"
	app, _, identityFile := pinIdentityApp(t, modeShared)
	ctx := context.Background()

	writeFile(t, identityFile, boundIdentity(canary, canary+"@example.com"))

	for _, format := range []string{formatText, formatJSON} {
		_, stdout, stderr := captureBoth(t, func() int {
			return runDoctor(ctx, app, commonOpts{Format: format}, "")
		})
		// Positive first: without the finding this loop would prove nothing. Keyed on
		// the message, not the check code — the text format prints the message alone.
		if !strings.Contains(stdout+stderr, "names an account other than claude/main") {
			t.Fatalf("doctor (%s) did not report the drift this test redacts:\n%s\n%s", format, stdout, stderr)
		}
		if strings.Contains(stdout+stderr, canary) {
			t.Fatalf("doctor (%s) leaked a bound identity value:\n%s\n%s", format, stdout, stderr)
		}
	}
}

// Unfiltered with the rest of the pin family: a binding is a property of the
// directory, and `kae doctor <tool>` already announces that it skipped them.
func TestFilteredDoctorSkipsBoundDirectoryIdentity(t *testing.T) {
	app, dir, identityFile := pinIdentityApp(t, modeShared)
	ctx := context.Background()
	writeFile(t, identityFile, boundIdentity("side-uuid", "side@example.com"))

	for _, c := range buildDoctor(ctx, app, constants.ToolClaude, false).Checks {
		if strings.Contains(c.Message, dir) {
			t.Fatalf("a filtered run must not include bound-directory findings: %q", c.Message)
		}
	}
}

// The two halves sit behind different gates on purpose, and the difference is load
// bearing: the credential half needs no secret backend and an unavailable backend is
// exactly when a user is diagnosing and least wants a check to vanish, while the
// identity half has to read the account's recorded identity to compare against.
func TestBoundDirectoryCredentialSurvivesAnUnavailableBackendButIdentityIsSkipped(t *testing.T) {
	app, dir, identityFile := pinIdentityApp(t, modeShared)
	ctx := context.Background()
	writeFile(t, identityFile, boundIdentity("side-uuid", "side@example.com"))
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main",
		app.Paths.SharedDir(paths.PinID(dir), constants.ToolClaude)), deadClaudeCred)

	// The keychain backend is unavailable on linux, which is how doctor's other
	// backend-free checks are pinned (TestActiveOrphan...).
	app.Config.Security.SecretBackend = secret.BackendKeychain
	if _, err := app.secretBackend(); err == nil {
		t.Fatal("this test needs an unavailable backend to be meaningful")
	}

	report := buildDoctor(ctx, app, "", false)
	if _, ok := findCheck(report, constants.CheckCredentialStale); !ok {
		t.Fatalf("the bound credential half must survive an unavailable backend, got %+v", report.Checks)
	}
	if msg, ok := findCheck(report, constants.CheckIdentityDrift); ok {
		t.Fatalf("the identity half needs the backend and must be skipped, got %q", msg)
	}
}

// boundStoreDir branches on the fragment's mode, and a two-branch switch with one
// branch untested is the shape of a guard that checks nothing. Isolated mode puts
// the store at a per-account path instead of the shared one, so a wrong branch
// reads a directory that does not exist and the check goes quietly silent — the
// failure that looks exactly like "no problem found".
func TestDoctorReportsStaleCredentialInAnIsolatedBoundDirectory(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	dir, _, credFile := pinWithCapturedClaude(t, app, modeIsolated)

	writeFile(t, credFile,
		deadClaudeCred)

	msg, ok := findCheck(buildDoctor(ctx, app, "", false), constants.CheckCredentialStale)
	if !ok {
		t.Fatal("an isolated bound directory's dead credential must be reported too")
	}
	if !strings.Contains(msg, "bound to "+dir) {
		t.Fatalf("message must name the bound directory: %q", msg)
	}
}
