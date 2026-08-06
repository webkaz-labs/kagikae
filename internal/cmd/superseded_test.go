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
func boundStoreForClaudeMain(t *testing.T, app *App) (dir, credFile string) {
	t.Helper()
	dir = pinHere(t, app, modeShared)
	storeDir := app.Paths.SharedDir(paths.PinID(dir), constants.ToolClaude)
	credFile = filepath.Join(storeDir, ".credentials.json")
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
	return dir, credFile
}

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
	behindDir, behindCred := boundStoreForClaudeMain(t, app)
	aheadDir, aheadCred := boundStoreForClaudeMain(t, app)
	writeFile(t, behindCred, claudeOAuthPayload("sk-ant-oat01-BEHIND-aaaa", now.Add(4*time.Hour)))
	writeFile(t, aheadCred, claudeOAuthPayload("sk-ant-oat01-AHEAD-bbbb", now.Add(8*time.Hour)))

	msgs := findChecks(buildDoctor(ctx, app, "", false), constants.CheckCredentialSuperseded)
	if len(msgs) != 1 {
		t.Fatalf("exactly the overtaken directory is reported, got %d: %v", len(msgs), msgs)
	}
	// "bound to <dir>" with the separator, not a bare path: the two temp directories
	// are siblings, and a substring match on a path is how a prefix passes for a
	// match somewhere else.
	if !strings.Contains(msgs[0], "bound to "+behindDir) {
		t.Errorf("the overtaken directory must be named: %q", msgs[0])
	}
	// Where the newer copy is, because that is the cause the user cannot otherwise
	// see, and the remedy has to be run in the *other* directory.
	if !strings.Contains(msgs[0], "the store bound to "+aheadDir) {
		t.Errorf("the message must say where the newer copy is: %q", msgs[0])
	}
	if !strings.Contains(msgs[0], "cd "+behindDir+" && kae relogin claude") {
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
	dir, credFile := boundStoreForClaudeMain(t, app)
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
	dir, credFile := boundStoreForClaudeMain(t, app)
	writeFile(t, credFile, claudeOAuthPayload("sk-ant-oat01-BEHIND-eeee", now.Add(time.Hour)))
	identity := filepath.Join(filepath.Dir(credFile), ".claude.json")
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
	dir, credFile := boundStoreForClaudeMain(t, app)
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
	dir, credFile := boundStoreForClaudeMain(t, app)

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
	behindDir, behindCred := boundStoreForClaudeMain(t, app)
	aheadDir, aheadCred := boundStoreForClaudeMain(t, app)
	sideDir := pinHere(t, app, modeShared)
	sideCred := filepath.Join(app.Paths.SharedDir(paths.PinID(sideDir), constants.ToolClaude), ".credentials.json")
	// Re-bind that third directory to the other account, so one tool has two accounts
	// bound across three directories.
	code, out := captureStdout(t, func() int {
		return runRebind(ctx, app, commonOpts{Format: formatText}, constants.ToolClaude, "side")
	})
	mustExit(t, constants.ExitOK, code, out)

	writeFile(t, behindCred, claudeOAuthPayload("sk-ant-oat01-BEHIND-aaaa", now.Add(4*time.Hour)))
	writeFile(t, aheadCred, claudeOAuthPayload("sk-ant-oat01-AHEAD-bbbb", now.Add(8*time.Hour)))
	writeFile(t, sideCred, claudeOAuthPayload("sk-ant-oat01-SIDE-cccc", now.Add(24*time.Hour)))

	msgs := findChecks(buildDoctor(ctx, app, "", false), constants.CheckCredentialSuperseded)
	if len(msgs) != 1 {
		t.Fatalf("only main's overtaken directory is reported, got %d: %v", len(msgs), msgs)
	}
	if !strings.Contains(msgs[0], "bound to "+behindDir) {
		t.Errorf("the overtaken directory must be named: %q", msgs[0])
	}
	if !strings.Contains(msgs[0], "the store bound to "+aheadDir) {
		t.Errorf("the newer copy is main's other directory, not side's: %q", msgs[0])
	}
	if strings.Contains(msgs[0], sideDir) {
		t.Fatalf("a different account's copy must not be named as the newer one: %q", msgs[0])
	}

	// Now break the *winner's* attribution. It is still the later copy, but kae can no
	// longer say it is this account's — so it proves nothing about the other one.
	if err := os.Remove(filepath.Join(filepath.Dir(aheadCred), ".claude.json")); err != nil {
		t.Fatal(err)
	}
	if msgs := findChecks(buildDoctor(ctx, app, "", false), constants.CheckCredentialSuperseded); len(msgs) != 0 {
		t.Fatalf("an unattributable winner proves nothing about the loser: %v", msgs)
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
	behindDir, behindCred := boundStoreForClaudeMain(t, app)
	aheadDir, aheadCred := boundStoreForClaudeMain(t, app)
	writeFile(t, behindCred, claudeOAuthPayload("sk-ant-oat01-BEHIND-hhhh", now.Add(4*time.Hour)))
	writeFile(t, aheadCred, claudeOAuthPayload("sk-ant-oat01-AHEAD-iiii", now.Add(8*time.Hour)))

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
	if !strings.Contains(msgs[0], "bound to "+behindDir) ||
		!strings.Contains(msgs[0], "the store bound to "+aheadDir) {
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
	_, behindCred := boundStoreForClaudeMain(t, app)
	_, aheadCred := boundStoreForClaudeMain(t, app)
	// The canary goes in the copy that gets *reported*; claudeOAuthPayload derives the
	// refresh token from the access token, so both halves are in the payload the
	// message is built from.
	writeFile(t, behindCred, claudeOAuthPayload(secretToken, now.Add(4*time.Hour)))
	writeFile(t, aheadCred, claudeOAuthPayload("sk-ant-oat01-AHEAD-kkkk", now.Add(8*time.Hour)))

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
