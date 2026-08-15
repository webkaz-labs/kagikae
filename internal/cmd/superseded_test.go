package cmd

import (
	"context"
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
)

// boundStoreForClaudeMain binds a fresh temp directory to the captured claude/main
// and returns it with the store kae gave it. Every store starts holding the bytes
// the bind wrote, so an ordering between two of them only exists once a test states
// one — which is the point: two copies with the same deadline are not ordered, and
// this check must stay silent about them.
//
// The caller captures claude/main first (captureClaudeAt), because the deadline the
// snapshot carries is one of the three copies in play.
func boundStoreForClaudeMain(t *testing.T, app *App) (dir, storeDir, credFile string) {
	t.Helper()
	dir = pinHere(t, app, modeShared)
	storeDir = app.Paths.SharedDir(paths.PinID(dir), constants.ToolClaude)
	credFile = dirCredFile(app, constants.ToolClaude, "main", storeDir)
	if readFile(t, credFile) == "" {
		t.Fatalf("pin did not materialize a credential at %s", credFile)
	}
	// Attribution is required in both directions, so a store that cannot name its
	// account is invisible to this check. Assert the bind wrote the identity rather
	// than assuming it: without this the tests below would pass by never reaching
	// the comparison at all.
	if got := readFile(t, filepath.Join(storeDir, ".claude.json")); !strings.Contains(got, "main-uuid") {
		t.Fatalf("the bind must leave an identity cache naming the account: %q", got)
	}
	return dir, storeDir, credFile
}

// twoBoundCopiesOfClaudeMain binds two directories to claude/main and leaves them
// holding *different* copies of that account's credential, which is the state this
// whole check is about.
//
// Since the credential split, that state has exactly one cause: one of the two
// directories predates the split and still keeps its credential in its own store,
// while the other reads the account's shared one. Two directories bound by a current
// kae share a single copy, so nothing can overtake anything — which is the feature,
// and the reason this fixture has to build the mixed state deliberately instead of
// pinning twice.
//
// It returns each directory with the file that copy lives in; the caller writes the
// payloads, because the deadlines are what each test is arranging.
func twoBoundCopiesOfClaudeMain(t *testing.T, app *App) (behind, ahead boundCopy) {
	t.Helper()
	behind.Dir, behind.StoreDir, _ = boundStoreForClaudeMain(t, app)
	makePreSplit(t, app, constants.ToolClaude, "main", behind.Dir, behind.StoreDir)
	behind.CredFile = filepath.Join(behind.StoreDir, ".credentials.json")
	ahead.Dir, ahead.StoreDir, ahead.CredFile = boundStoreForClaudeMain(t, app)
	return behind, ahead
}

// boundCopy is one bound directory and the two locations that stopped being the
// same one: the store, which holds the sessions and the identity cache, and the
// file the credential is actually in.
type boundCopy struct{ Dir, StoreDir, CredFile string }

// findChecks is findCheck for a code that is legitimately emitted more than once —
// one per bound directory here — so a test can assert *which* subjects it named.
func findChecks(report *doctorReport, code string) []string {
	msgs := []string{}
	for _, c := range report.Checks {
		if c.Code == code {
			msgs = append(msgs, c.Message)
		}
	}
	return msgs
}

// The failure with no other signal at all: two worktrees bound to one account each
// hold their own copy, the tool refreshes one of them in place, and claude's refresh
// token rotates single-use — so the other copy is dead from that moment. Every
// freshness surface still reports it `ok`, because they judge by
// `refreshTokenExpiresAt` and an invalidation does not move that field. "I used
// claude in the other worktree and this one logged out hours later" had no visible
// cause until this check (docs/ROADMAP.md § Every credential copy).
func TestDoctorReportsABoundCopyOvertakenByAnotherDirectory(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	// Three distinct deadlines, never two equal ones: an ordering test whose sides
	// can tie is a test that can pass without the comparison ever running.
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	behind, ahead := twoBoundCopiesOfClaudeMain(t, app)
	writeFile(t, behind.CredFile, claudeOAuthPayload("sk-ant-oat01-BEHIND-aaaa", now.Add(4*time.Hour)))
	writeFile(t, ahead.CredFile, claudeOAuthPayload("sk-ant-oat01-AHEAD-bbbb", now.Add(8*time.Hour)))

	msgs := findChecks(buildDoctor(ctx, app, "", false), constants.CheckCredentialSuperseded)
	if len(msgs) != 1 {
		t.Fatalf("exactly the overtaken directory is reported, got %d: %v", len(msgs), msgs)
	}
	// "bound to <dir>" with the separator, not a bare path: the two temp directories
	// are siblings, and a substring match on a path is how a prefix passes for a
	// match somewhere else.
	if !strings.Contains(msgs[0], "bound to "+behind.Dir) {
		t.Errorf("the overtaken directory must be named: %q", msgs[0])
	}
	// Where the newer copy is, because that is the cause the user cannot otherwise
	// see, and the remedy has to be run in the *other* directory.
	if !strings.Contains(msgs[0], "the store bound to "+ahead.Dir) {
		t.Errorf("the message must say where the newer copy is: %q", msgs[0])
	}
	if !strings.Contains(msgs[0], "cd "+behind.Dir+" && kae relogin claude") {
		t.Errorf("the remedy is a relogin in the overtaken directory: %q", msgs[0])
	}
	for _, secret := range []string{"BEHIND-aaaa", "AHEAD-bbbb"} {
		if strings.Contains(msgs[0], secret) {
			t.Fatalf("a credential must never reach a message: %q", msgs[0])
		}
	}
}

// The ordinary configuration — one pin plus the account snapshot — is two copies of
// one credential, and it is healthy. A check that warned about it would be wallpaper
// within a day, which is the mistake v0.15.0/v0.15.1 shipped in both directions.
//
// The positive control is in this test on purpose: the silence above is only
// evidence if the same fixture *can* speak, and five quiet-path tests once passed
// against a warning function replaced by an immediate return.
func TestDoctorIsSilentWhileTheBoundCopyIsTheNewest(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	dir, _, credFile := boundStoreForClaudeMain(t, app)
	writeFile(t, credFile, claudeOAuthPayload("sk-ant-oat01-FRESH-cccc", now.Add(8*time.Hour)))

	if msgs := findChecks(buildDoctor(ctx, app, "", false), constants.CheckCredentialSuperseded); len(msgs) != 0 {
		t.Fatalf("a bound copy newer than the snapshot is the healthy case: %v", msgs)
	}
	// Positive control: put the snapshot ahead of the store and the same fixture
	// reports it. This also covers the snapshot as the *winner*, which needs no
	// attribution of its own — it is the account's own record.
	writeFile(t, credFile, claudeOAuthPayload("sk-ant-oat01-STALEISH-dddd", now.Add(30*time.Minute)))
	msgs := findChecks(buildDoctor(ctx, app, "", false), constants.CheckCredentialSuperseded)
	if len(msgs) != 1 || !strings.Contains(msgs[0], "bound to "+dir) {
		t.Fatalf("a bound copy the snapshot overtook must be reported: %v", msgs)
	}
	if !strings.Contains(msgs[0], "snapshot claude/main") {
		t.Errorf("the message must say the newer copy is in the snapshot: %q", msgs[0])
	}
	// And the remedy differs from the other branch's, because where the newer copy is
	// decides what fixes it: a re-bind materializes the snapshot into the store with
	// no browser round-trip, which is only true when the snapshot is the newer one.
	if !strings.Contains(msgs[0], "cd "+dir+" && kae pin claude main") {
		t.Errorf("a snapshot-side winner is fixed by a re-bind, not a login: %q", msgs[0])
	}
	if strings.Contains(msgs[0], "relogin") {
		t.Errorf("no login is needed when the snapshot already holds the newer copy: %q", msgs[0])
	}
}

// Ordering never establishes *whose* login two copies are. A shared store is bound
// by one directory but written by whichever account was bound before, so without
// attribution this check reports one account's copy as having been overtaken by
// another's — and the token is opaque, so nothing afterwards can tell.
func TestDoctorSaysNothingAboutAStoreItCannotAttribute(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(8*time.Hour))
	dir, storeDir, credFile := boundStoreForClaudeMain(t, app)
	writeFile(t, credFile, claudeOAuthPayload("sk-ant-oat01-BEHIND-eeee", now.Add(time.Hour)))
	identity := filepath.Join(storeDir, ".claude.json")
	if err := os.Remove(identity); err != nil {
		t.Fatal(err)
	}

	if msgs := findChecks(buildDoctor(ctx, app, "", false), constants.CheckCredentialSuperseded); len(msgs) != 0 {
		t.Fatalf("a copy kae cannot attribute must not be reported: %v", msgs)
	}
	// Positive control, so the silence above is the attribution gate and not a
	// fixture that never reaches the comparison.
	writeFile(t, identity, claudeIdentityFile("main-uuid"))
	msgs := findChecks(buildDoctor(ctx, app, "", false), constants.CheckCredentialSuperseded)
	if len(msgs) != 1 || !strings.Contains(msgs[0], "bound to "+dir) {
		t.Fatalf("with the identity back the same copy is reported: %v", msgs)
	}
}

// A store kae cannot place in the ordering is one it cannot judge, and this check
// requires the *loser* to be `orderable` — which is stricter than what `supersedes`
// asks of its b side, on purpose. supersedes lets an un-orderable b lose to anything
// because its callers are asking "may I overwrite this?", where a copy with no
// comparable deadline is nothing to lose. Taking that subset here would report every
// undated store as overtaken by anything, which is the "subset of a predicate"
// defect this repo has already shipped once.
//
// The payload is the shape an upstream type change actually produces: `expiresAt`
// present but not a number, so claude reads it as Known, un-Revoked and undated at
// once, while its refresh deadline keeps every other surface quiet about it.
func TestDoctorSaysNothingAboutAnUndatedBoundCopy(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(8*time.Hour))
	dir, _, credFile := boundStoreForClaudeMain(t, app)
	undated := fmt.Sprintf(
		`{"claudeAiOauth":{"accessToken":"a","refreshToken":"r","expiresAt":"not-a-number",`+
			`"refreshTokenExpiresAt":%d}}`, now.Add(27*24*time.Hour).UnixMilli(),
	)
	writeFile(t, credFile, undated)

	report := buildDoctor(ctx, app, "", false)
	if msgs := findChecks(report, constants.CheckCredentialSuperseded); len(msgs) != 0 {
		t.Fatalf("a copy kae cannot order must not be reported as overtaken: %v", msgs)
	}
	// And nothing else claims it either, which is what makes the silence honest
	// rather than a gap the other check would have covered.
	if msgs := findChecks(report, constants.CheckCredentialStale); len(msgs) != 0 {
		t.Fatalf("the undated fixture must not be reported as stale either: %v", msgs)
	}
	// Positive control: the same store with a real deadline behind the snapshot is
	// reported, so the silence above is the orderable gate.
	writeFile(t, credFile, claudeOAuthPayload("sk-ant-oat01-DATED-ffff", now.Add(time.Hour)))
	msgs := findChecks(buildDoctor(ctx, app, "", false), constants.CheckCredentialSuperseded)
	if len(msgs) != 1 || !strings.Contains(msgs[0], "bound to "+dir) {
		t.Fatalf("a dated copy behind the snapshot is reported: %v", msgs)
	}
}

// A bind copies the snapshot into the store, so the two carry the *same* deadline
// until something refreshes one of them. supersedes is a strict comparison and this
// check inherits that: equal is not overtaken, or every freshly pinned directory
// would report itself.
func TestDoctorDoesNotReportAnEqualDeadlineAsOvertaken(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(4*time.Hour))
	dir, _, credFile := boundStoreForClaudeMain(t, app)

	if msgs := findChecks(buildDoctor(ctx, app, "", false), constants.CheckCredentialSuperseded); len(msgs) != 0 {
		t.Fatalf("a directory pinned moments ago must report nothing: %v", msgs)
	}
	// Positive control: one second behind is behind. The gap between the two halves
	// of this test is the whole assertion — a comparison that treated equal as
	// overtaken would fail the first half, and one that never ran would fail here.
	writeFile(t, credFile, claudeOAuthPayload("sk-ant-oat01-ASECOND-gggg", now.Add(4*time.Hour-time.Second)))
	msgs := findChecks(buildDoctor(ctx, app, "", false), constants.CheckCredentialSuperseded)
	if len(msgs) != 1 || !strings.Contains(msgs[0], "bound to "+dir) {
		t.Fatalf("a copy one second behind the snapshot is overtaken: %v", msgs)
	}
}

// Two guards this check cannot do without, and neither is reachable with fewer than
// three bound directories — which is why both survived every mutation until now.
//
//   - the **winner-side** attribution. Ordering says which copy is later, never whose
//     login it is, so a store kae cannot tie to this account must not be reported as
//     having overtaken one that it can. Without it kae names one account's copy as the
//     thing that killed another's.
//   - the **grouping by account**. Without it every claude store is compared against
//     every other, so an unrelated account's fresher copy becomes the reason a
//     directory is reported.
func TestDoctorComparesOnlyCopiesOfTheSameAccountAndOnlyAttributedOnes(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	// Four distinct deadlines. side is deliberately the freshest thing on the machine:
	// if the grouping is dropped it becomes the "newer copy" for main's directories.
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	captureClaudeAt(t, app, "side", sideToken, now.Add(time.Hour))
	app.Config.Profiles["side"] = config.Profile{Accounts: map[string]string{constants.ToolClaude: "side"}}
	behind, ahead := twoBoundCopiesOfClaudeMain(t, app)
	sideDir := pinHere(t, app, modeShared)
	sideCred := dirCredFile(app, constants.ToolClaude, "side",
		app.Paths.SharedDir(paths.PinID(sideDir), constants.ToolClaude))
	// Re-bind that third directory to the other account, so one tool has two accounts
	// bound across three directories.
	code, out := captureStdout(t, func() int {
		return runRebind(ctx, app, commonOpts{Format: formatText}, constants.ToolClaude, "side")
	})
	mustExit(t, constants.ExitOK, code, out)

	writeFile(t, behind.CredFile, claudeOAuthPayload("sk-ant-oat01-BEHIND-aaaa", now.Add(4*time.Hour)))
	writeFile(t, ahead.CredFile, claudeOAuthPayload("sk-ant-oat01-AHEAD-bbbb", now.Add(8*time.Hour)))
	writeFile(t, sideCred, claudeOAuthPayload("sk-ant-oat01-SIDE-cccc", now.Add(24*time.Hour)))

	msgs := findChecks(buildDoctor(ctx, app, "", false), constants.CheckCredentialSuperseded)
	if len(msgs) != 1 {
		t.Fatalf("only main's overtaken directory is reported, got %d: %v", len(msgs), msgs)
	}
	if !strings.Contains(msgs[0], "bound to "+behind.Dir) {
		t.Errorf("the overtaken directory must be named: %q", msgs[0])
	}
	if !strings.Contains(msgs[0], "the store bound to "+ahead.Dir) {
		t.Errorf("the newer copy is main's other directory, not side's: %q", msgs[0])
	}
	if strings.Contains(msgs[0], sideDir) {
		t.Fatalf("a different account's copy must not be named as the newer one: %q", msgs[0])
	}

	// Now break the *winner's* attribution. It is still the later copy, but kae can no
	// longer say it is this account's — so it proves nothing about the other one.
	if err := os.Remove(filepath.Join(ahead.StoreDir, ".claude.json")); err != nil {
		t.Fatal(err)
	}
	if msgs := findChecks(buildDoctor(ctx, app, "", false), constants.CheckCredentialSuperseded); len(msgs) != 0 {
		t.Fatalf("an unattributable winner proves nothing about the loser: %v", msgs)
	}
}

// threeBoundCopiesOfClaudeMain is twoBoundCopiesOfClaudeMain with a second directory
// on the account's shared credential, which is what an ordinary machine looks like:
// two worktrees bound to one account are two *handles on one file*, not two copies.
//
// The two handles are what the fixtures above cannot express. With one, whichever
// question this check asks of "the" bound directory has a single answer, so a
// predicate that should have been asked of the credential and was asked of a handle
// is indistinguishable from a correct one.
func threeBoundCopiesOfClaudeMain(t *testing.T, app *App) (behind, ahead, alt boundCopy) {
	t.Helper()
	behind, ahead = twoBoundCopiesOfClaudeMain(t, app)
	alt.Dir, alt.StoreDir, alt.CredFile = boundStoreForClaudeMain(t, app)
	if alt.CredFile != ahead.CredFile {
		t.Fatalf("two directories bound to one account must share one credential, got %s and %s",
			ahead.CredFile, alt.CredFile)
	}
	return behind, ahead, alt
}

// hideIdentity removes a bound store's identity cache and returns the restore. The
// bytes are kept rather than rewritten from a fixture, so a restore cannot quietly
// put back something the bind never wrote.
func hideIdentity(t *testing.T, storeDir string) func() {
	t.Helper()
	path := filepath.Join(storeDir, ".claude.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	return func() {
		t.Helper()
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// Attribution is a property of the credential, and asking one handle about it made
// this check silent on the shape it exists for. Every directory bound to an account
// reads one file since the split, so the store that wins the ordering is whichever
// handle the walk reached first — and if that one had no identity cache beside it,
// `winnerUnattributable` suppressed **every** finding in the group while a sibling
// handle on the same file was confirming and was never asked.
//
// Reproduced with a reversible control before the fix (three bound directories: one
// finding, remove one directory's identity cache, zero, restore it, one again), which
// is the shape this test is. Both handles are hidden in turn rather than the winner
// only, because which one wins is walk order and nothing here should depend on it:
// one of the two arms is the winner's whichever way the walk goes.
func TestSupersededSurvivesOneSharedHandleLosingItsIdentityCache(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	behind, ahead, alt := threeBoundCopiesOfClaudeMain(t, app)
	writeFile(t, behind.CredFile, claudeOAuthPayload("sk-ant-oat01-BEHIND-mmmm", now.Add(4*time.Hour)))
	writeFile(t, ahead.CredFile, claudeOAuthPayload("sk-ant-oat01-AHEAD-nnnn", now.Add(8*time.Hour)))

	reported := func(t *testing.T, when string) {
		t.Helper()
		msgs := findChecks(buildDoctor(ctx, app, "", false), constants.CheckCredentialSuperseded)
		if len(msgs) != 1 {
			t.Fatalf("%s: the overtaken directory must still be reported, got %d: %v", when, len(msgs), msgs)
		}
		if !strings.Contains(msgs[0], "bound to "+behind.Dir) {
			t.Errorf("%s: the wrong directory was named: %q", when, msgs[0])
		}
	}
	reported(t, "with both handles labelled")
	for _, handle := range []struct {
		name  string
		store string
	}{{"ahead", ahead.StoreDir}, {"alt", alt.StoreDir}} {
		restore := hideIdentity(t, handle.store)
		reported(t, "with only "+handle.name+" unlabelled")
		restore()
	}
	reported(t, "with both handles labelled again")
}

// The guard the fix above must not have removed. `winnerUnattributable` is what keeps
// kae from telling a user their login is dead on the strength of a copy it cannot tie
// to the account, and widening attribution from one handle to the credential's readers
// is not the same as dropping it: with *no* handle able to speak, nothing attributes
// the copy and the group stays silent.
func TestSupersededStaysSilentWhenNoHandleCanAttributeTheCopy(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	behind, ahead, alt := threeBoundCopiesOfClaudeMain(t, app)
	writeFile(t, behind.CredFile, claudeOAuthPayload("sk-ant-oat01-BEHIND-pppp", now.Add(4*time.Hour)))
	writeFile(t, ahead.CredFile, claudeOAuthPayload("sk-ant-oat01-AHEAD-qqqq", now.Add(8*time.Hour)))
	hideIdentity(t, ahead.StoreDir)
	restoreAlt := hideIdentity(t, alt.StoreDir)

	if msgs := findChecks(buildDoctor(ctx, app, "", false), constants.CheckCredentialSuperseded); len(msgs) != 0 {
		t.Fatalf("a copy no reader can attribute proves nothing about the loser: %v", msgs)
	}
	// Positive control, so the silence above is the attribution guard and not a fixture
	// that never reaches the comparison: one reader speaking again is enough.
	restoreAlt()
	msgs := findChecks(buildDoctor(ctx, app, "", false), constants.CheckCredentialSuperseded)
	if len(msgs) != 1 || !strings.Contains(msgs[0], "bound to "+behind.Dir) {
		t.Fatalf("with one handle labelled again the same copy is reported: %v", msgs)
	}
}

// Two bound copies are still copies of each other when the account's own snapshot
// payload is gone (the `secret_missing` shape: `account.toml` and its identity
// artifact intact, the credential payload not in the backend). The comparison must
// survive that — and it only does because the fallback keeps the account **record**,
// which is what attribution compares each store's identity cache against. Zeroing
// the whole account there made every candidate refuse, so the branch reported
// nothing at all while its comment claimed the stores were still being compared.
func TestDoctorComparesBoundCopiesWhenTheSnapshotPayloadIsGone(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	behind, ahead := twoBoundCopiesOfClaudeMain(t, app)
	writeFile(t, behind.CredFile, claudeOAuthPayload("sk-ant-oat01-BEHIND-hhhh", now.Add(4*time.Hour)))
	writeFile(t, ahead.CredFile, claudeOAuthPayload("sk-ant-oat01-AHEAD-iiii", now.Add(8*time.Hour)))

	acc, found, err := account.Load(app.Paths.AccountDir(constants.ToolClaude, "main"))
	if err != nil || !found {
		t.Fatalf("captured snapshot missing: found=%v err=%v", found, err)
	}
	be := testBackend(t, app)
	if err := be.Delete(ctx, acc.Artifacts[credentialArtifactName(constants.ToolClaude)].SecretRef); err != nil {
		t.Fatal(err)
	}

	msgs := findChecks(buildDoctor(ctx, app, "", false), constants.CheckCredentialSuperseded)
	if len(msgs) != 1 {
		t.Fatalf("the two stores are still comparable, got %d: %v", len(msgs), msgs)
	}
	if !strings.Contains(msgs[0], "bound to "+behind.Dir) ||
		!strings.Contains(msgs[0], "the store bound to "+ahead.Dir) {
		t.Errorf("both directories must be named the right way round: %q", msgs[0])
	}
}

// credential_superseded is a third message built from a parsed credential, so it is
// a third path for a token to reach stdout, --json and stderr. AGENTS.md requires a
// redaction test for every new output path; the last one found a real leak. The
// sibling for the two snapshot-side builders is
// TestCredentialFreshnessMessagesNeverCarryTheToken — separate rather than a case in
// its table, because this one needs a bound directory and two copies to have
// anything to say.
func TestSupersededMessageNeverCarriesTheToken(t *testing.T) {
	const secretToken = "sk-ant-oat01-SUPERSEDED-CANARY-jjjj"
	const secretRefresh = "rt-" + secretToken
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	behind, ahead := twoBoundCopiesOfClaudeMain(t, app)
	// The canary goes in the copy that gets *reported*; claudeOAuthPayload derives the
	// refresh token from the access token, so both halves are in the payload the
	// message is built from.
	writeFile(t, behind.CredFile, claudeOAuthPayload(secretToken, now.Add(4*time.Hour)))
	writeFile(t, ahead.CredFile, claudeOAuthPayload("sk-ant-oat01-AHEAD-kkkk", now.Add(8*time.Hour)))

	// The fixture must actually reach the state this test is named for, or the
	// assertions below prove nothing about that path.
	if _, ok := findCheck(buildDoctor(ctx, app, "", false), constants.CheckCredentialSuperseded); !ok {
		t.Fatal("fixture no longer produces credential_superseded; this canary would pass vacuously")
	}
	for _, format := range []string{formatText, formatJSON} {
		_, stdout, stderr := captureBoth(t, func() int {
			return runDoctor(ctx, app, commonOpts{Format: format}, "")
		})
		for stream, out := range map[string]string{"stdout": stdout, "stderr": stderr} {
			if strings.Contains(out, secretToken) || strings.Contains(out, secretRefresh) {
				t.Fatalf("%s (%s) leaked a credential value: %q", stream, format, out)
			}
		}
	}
}
