package cmd

// kae relogin tests of what the pre-flight says and of the drift checks that
// decide whether a watched login may be captured.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/paths"
)

// The login flow is a write kae does not perform, so the copy already in the store is
// gone the moment the tool finishes — and since the credential split that copy belongs
// to the *account*, not to this directory. `kae pin` declines to overwrite one it
// cannot attribute in order to preserve it, and then names this command as the remedy;
// following that remedy destroyed the copy the refusal had kept, with no warning and no
// copy anywhere afterwards (measured 2026-08-08, end to end).
//
// So kae harvests before the flow, and says so when it could not. It may claim only
// what it observed: the harvest's own reason, never that the copy is another account's,
// which on this arm is exactly what kae could not establish.
func TestReloginSaysWhatTheLoginFlowIsAboutToReplace(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))

	// A sibling bound to the same account, which confirms — without it this directory
	// would be the only reader that disagrees, which is the `Conflicting` arm that
	// overwrites rather than the keep this test is about.
	_, _, siblingCred := boundStoreForClaudeMain(t, app)
	_, storeDir, credFile := boundStoreForClaudeMain(t, app)
	// Positive control on the fixture itself: the two directories must be reading one
	// copy, or the disagreement below is about a store nothing shares and the refusal
	// under test is never reached.
	if siblingCred != credFile {
		t.Fatalf("the two bindings must share one account credential store: %s vs %s", siblingCred, credFile)
	}

	// Somebody ran the tool's own /login here as another account: the identity lands in
	// this directory's config dir and the credential in the account's shared store.
	const foreign = "sk-ant-oat01-SIDE-cccc"
	writeFile(t, filepath.Join(storeDir, ".claude.json"), claudeIdentityFile("side-uuid"))
	writeFile(t, credFile, claudeOAuthPayload(foreign, now.Add(8*time.Hour)))

	withInteractive(t, loginInto(t, constants.ToolClaude, "sk-ant-oat01-MAIN-eeee", "main-uuid",
		now.Add(9*time.Hour), &[]string{}))
	code, stderr := captureStderr(t, func() int {
		return runRelogin(ctx, app, commonOpts{Format: formatText}, "")
	})
	mustExit(t, constants.ExitOK, code, stderr)

	warned := strings.Index(stderr, "completing the login flow replaces it")
	if warned < 0 {
		t.Fatalf("the flow must not replace a copy kae could not keep without saying so: %q", stderr)
	}
	// Before the write it warns about, which for this one means before the flow is
	// launched — a warning printed afterwards describes a loss that has already
	// happened (AGENTS.md).
	if launched := strings.Index(stderr, "complete the claude login flow"); launched < 0 || warned > launched {
		t.Errorf("the warning must precede the flow (warned=%d launched=%d): %q", warned, launched, stderr)
	}
	if !strings.Contains(stderr, "disagree about whose login it is") {
		t.Errorf("it must carry the harvest's own reason: %q", stderr)
	}
	// The ordered frame, which is the half the un-orderable test cannot pin. Here the
	// harvest refused *past* the supersedes gate, so kae did establish the copy is newer
	// and saying so is the most useful thing it knows. Without this assertion the flag
	// that picks the frame can be tied to Conflicting — which is false for every
	// unattributable refusal — and nothing fails (measured 2026-08-08).
	if !strings.Contains(stderr, "is newer than snapshot claude/main") {
		t.Errorf("kae established the ordering here, so the frame must carry it: %q", stderr)
	}
	// What kae did *not* establish, it may not say. On this arm the readers disagree,
	// so kae has no verdict about whose the copy is.
	if strings.Contains(stderr, "belongs to an account other than") {
		t.Errorf("kae did not attribute this copy, so it must not name an owner: %q", stderr)
	}
	// The refusal is real: the foreign copy was not filed under this account either.
	be := testBackend(t, app)
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); strings.Contains(got, foreign) {
		t.Fatalf("a copy kae could not attribute must not reach this snapshot: %s", got)
	}

	// Positive control, both directions: with the disagreement resolved the same
	// fixture harvests silently, so the warning above is the refusal and not a line
	// this path always prints.
	writeFile(t, filepath.Join(storeDir, ".claude.json"), claudeIdentityFile("main-uuid"))
	// Three distinct deadlines, in the order a real machine produces them: the copy in
	// the store is later than what the first run's login left in the snapshot (or the
	// harvest returns at its "nothing newer" arm and this control never reaches the
	// attribution it is controlling for), and the login is later again. Dating the store
	// copy *past* the login instead makes captureBackAfterRelogin short-circuit at
	// `!supersedes` — a state no real login reaches, since deadlines advance — and this
	// control would then pass while exercising it.
	const kept = "sk-ant-oat01-KEPT-hhhh"
	writeFile(t, credFile, claudeOAuthPayload(kept, now.Add(10*time.Hour)))
	const last = "sk-ant-oat01-LAST-iiii"
	withInteractive(t, loginInto(t, constants.ToolClaude, last, "main-uuid", now.Add(11*time.Hour), &[]string{}))
	code, stderr = captureStderr(t, func() int {
		return runRelogin(ctx, app, commonOpts{Format: formatText}, "")
	})
	mustExit(t, constants.ExitOK, code, stderr)
	if strings.Contains(stderr, "completing the login flow replaces it") {
		t.Errorf("an attributable copy is harvested, not warned about: %q", stderr)
	}
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); !strings.Contains(got, last) {
		t.Fatalf("the login is the newest copy here, so the capture back must have run: %s", got)
	}
	// Harvested rather than merely not-warned-about, and **before** the flow: the
	// post-flow capture back would report the same line about the login's own copy, so
	// only the ordering distinguishes the pass this test is about from the one that was
	// already there. The snapshot cannot carry this assertion — by the time the command
	// returns it holds what the login wrote, which supersedes both.
	harvested := strings.Index(stderr, "harvested the newer claude credential")
	launched := strings.Index(stderr, "complete the claude login flow")
	if harvested < 0 || launched < 0 || harvested > launched {
		t.Fatalf("the copy the flow replaces must be harvested before it (harvested=%d launched=%d): %q",
			harvested, launched, stderr)
	}
}

// A message may claim no more than kae observed, and this one's reason is sometimes
// kae saying it **cannot order** the two copies. So the frame must not call the copy
// newer: that contradicts the reason it interpolates, which is the fold
// docs/CLI.md § `kae rollback --json` is normative against and which
// captureBackAfterRelogin was corrected for once already.
func TestReloginPreFlightDoesNotClaimAnOrderingItCannotMake(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	_, _, credFile := boundStoreForClaudeMain(t, app)

	// Known (expiresAt present) but undated (a non-numeric value parses to the zero
	// time) with the tokens intact, so it is not the measured tombstone: readLiveCredential
	// classifies it liveUnreadable, which is the arm that cannot be ordered at all.
	writeFile(t, credFile,
		`{"claudeAiOauth":{"accessToken":"sk-ant-oat01-ODD-ffff","refreshToken":"rt-odd",`+
			`"expiresAt":"soon","refreshTokenExpiresAt":1830384000000}}`)

	withInteractive(t, loginInto(t, constants.ToolClaude, "sk-ant-oat01-NEW-gggg", "main-uuid",
		now.Add(9*time.Hour), &[]string{}))
	code, stderr := captureStderr(t, func() int {
		return runRelogin(ctx, app, commonOpts{Format: formatText}, "")
	})
	mustExit(t, constants.ExitOK, code, stderr)

	// Positive control first: without it, a run that never reached this arm would pass
	// the negative assertion below for free.
	if !strings.Contains(stderr, "cannot read or date the copy already there") {
		t.Fatalf("the fixture must reach the un-orderable arm: %q", stderr)
	}
	if !strings.Contains(stderr, "kae is not harvesting the claude credential already in") {
		t.Errorf("the pre-flight must still say it did not keep the copy: %q", stderr)
	}
	if strings.Contains(stderr, "is newer than snapshot") {
		t.Errorf("kae could not order these two copies, so it may not call one newer: %q", stderr)
	}
}

// "kae could not look" and "there was nothing worth keeping" are the same output and
// opposite facts, and the only thing the user can still do about the second — not
// complete the flow — is available *before* it starts. So the pre-flight says so on
// the routes where it cannot even read the snapshot to compare against, rather than
// deferring to the post-flow report, which describes the loss after it happened.
func TestReloginSaysWhenItCannotCheckWhatTheFlowWillReplace(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	_, _, credFile := boundStoreForClaudeMain(t, app)
	// Something worth losing is in the store, so this is not the "nothing there" case.
	writeFile(t, credFile, claudeOAuthPayload("sk-ant-oat01-ATRISK-jjjj", now.Add(8*time.Hour)))

	// The binding still names claude/main; the snapshot it would compare against is gone
	// (an `kae account rm` between the bind and the login). The fragment is what relogin
	// reads, so the command still runs.
	if err := os.RemoveAll(app.Paths.AccountDir(constants.ToolClaude, "main")); err != nil {
		t.Fatalf("remove the snapshot: %v", err)
	}

	withInteractive(t, loginInto(t, constants.ToolClaude, "sk-ant-oat01-NEW-kkkk", "main-uuid",
		now.Add(9*time.Hour), &[]string{}))
	code, stderr := captureStderr(t, func() int {
		return runRelogin(ctx, app, commonOpts{Format: formatText}, "")
	})
	mustExit(t, constants.ExitOK, code, stderr)

	said := strings.Index(stderr, "cannot tell what the login flow is about to replace")
	if said < 0 {
		t.Fatalf("a read kae could not make must be said before the flow, not left silent: %q", stderr)
	}
	if launched := strings.Index(stderr, "complete the claude login flow"); launched < 0 || said > launched {
		t.Errorf("and before it (said=%d launched=%d): %q", said, launched, stderr)
	}
	// No remedy: kae genuinely has none here, and inventing one would send the user at a
	// command that cannot help.
	if strings.Contains(stderr, "kae pin claude") {
		t.Errorf("kae has no remedy for a snapshot it could not read: %q", stderr)
	}
}

// A new output path is a new place a secret can leak, and this one interpolates an
// error from the secret backend. AGENTS.md requires a redaction test for each.
func TestReloginPreFlightWarningsCarryNoSecret(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	_, storeDir, credFile := boundStoreForClaudeMain(t, app)

	// One reader disagreeing, so the pre-flight reaches the refusal that names the
	// store and a reason — the wordiest of the three lines.
	const atRisk = "sk-ant-oat01-ATRISK-llll"
	_, siblingStore, _ := boundStoreForClaudeMain(t, app)
	writeFile(t, filepath.Join(siblingStore, ".claude.json"), claudeIdentityFile("side-uuid"))
	writeFile(t, credFile, claudeOAuthPayload(atRisk, now.Add(8*time.Hour)))
	_ = storeDir

	const fresh = "sk-ant-oat01-FRESH-mmmm"
	withInteractive(t, loginInto(t, constants.ToolClaude, fresh, "main-uuid", now.Add(9*time.Hour), &[]string{}))
	code, out, stderr := captureBoth(t, func() int {
		return runRelogin(ctx, app, commonOpts{Format: formatText}, "")
	})
	mustExit(t, constants.ExitOK, code, stderr)

	// Positive control: the pre-flight really spoke, so the absences below are about
	// a line that exists rather than about a run that printed nothing.
	if !strings.Contains(stderr, "completing the login flow replaces it") {
		t.Fatalf("the fixture must reach the pre-flight refusal: %q", stderr)
	}
	for _, secret := range []string{atRisk, fresh, mainToken} {
		if strings.Contains(stderr, secret) || strings.Contains(out, secret) {
			t.Errorf("a credential reached the output: %q / %q", stderr, out)
		}
	}
}

// kae runs the login itself, watches the store change, reads back an identity cache
// naming this account — and still declines to capture it, because a *sibling* worktree
// bound to the same account carries an unresolved `identity_drift`. Measured end to end
// 2026-08-16, and this is the behaviour rather than a defect being recorded: what it
// costs the user is a trip to the directory that disagrees, which the message now names,
// and the alternative was measured to cost a token filed under the wrong account.
//
// The reader set is the mechanism: sharedStoreAttribution asks every directory that
// reads `credstore/claude/main`, the sibling names somebody else, and a confirming
// reader beside a conflicting one is the `disagree` outcome — a refusal, not a conflict,
// so the copy is kept. The account snapshot then holds a copy this very login has
// already invalidated (claude's refresh token rotates single-use), which is what the
// remedy line exists to get the user out of.
//
// **An override was built here and reverted** (docs/ROADMAP.md § `kae relogin` declines
// to capture a login it watched happen). It let this directory's own confirmation
// outweigh the sibling, gated on kae having seen the tool write its label here — and
// that gate cannot be measured: the label's file is claude's mixed-state `~/.claude.json`,
// whose write time a startup moves, while the record's own bytes do not move for a
// relogin as the account already labelled here. attributionSource carries the
// measurement; TestReloginDoesNotCaptureASiblingsRefreshedCopy is what it protects.
//
// The drift is applied *after* the sibling's own bind so the fixture reaches this
// outcome and not an earlier one: a directory bound while a sibling disagrees takes
// the keep branch and writes no identity of its own, which would make every assertion
// below hold for missing evidence instead.
func TestReloginDeclinesALoginItWatchedWhenASiblingHasDrifted(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	dir, _, credFile := boundStoreForClaudeMain(t, app)
	siblingDir, siblingStore := bindClaudeHere(t, app, "main")
	writeFile(t, filepath.Join(siblingStore, ".claude.json"), claudeIdentityFile("side-uuid"))
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	const refreshed = "sk-ant-oat01-RELOGGED-aaaa"
	seen := []string{}
	withInteractive(t, loginInto(t, constants.ToolClaude, refreshed, "main-uuid", now.Add(8*time.Hour), &seen))

	code, out, stderr := captureBoth(t, func() int {
		return runRelogin(ctx, app, commonOpts{Format: formatText}, "")
	})
	mustExit(t, constants.ExitOK, code, stderr)

	// Positive controls for the two halves kae observed, so what follows is a refusal
	// about attribution rather than a flow that never happened: the login landed in the
	// store this directory reads, and it named this account there.
	if got := readFile(t, credFile); !strings.Contains(got, refreshed) {
		t.Fatalf("the login must land in the bound store for this to be about attribution: %s", got)
	}
	if got := readFile(t, filepath.Join(app.Paths.SharedDir(paths.PinID(dir), constants.ToolClaude),
		".claude.json")); !strings.Contains(got, "main-uuid") {
		t.Fatalf("the flow must leave this directory naming this account: %q", got)
	}
	if got := readFile(t, filepath.Join(siblingStore, ".claude.json")); !strings.Contains(got, "side-uuid") {
		t.Fatalf("the sibling must still name another account: %q", got)
	}

	be := testBackend(t, app)
	// What the refusal costs, and the reason the remedy below has to be there: the
	// snapshot still holds the copy this login invalidated, so `kae use claude main`
	// would apply a dead one.
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); !strings.Contains(got, mainToken) {
		t.Fatalf("the harvest declines here; the snapshot keeps its own copy: %s", got)
	}
	if !strings.Contains(stderr, "cannot confirm the claude login now in this directory is claude/main's") {
		t.Errorf("the refusal must be the attribution one: %q", stderr)
	}
	if !strings.Contains(stderr, "disagree about whose login it is") {
		t.Errorf("and its reason must be the reader disagreement: %q", stderr)
	}
	// The half a user can act on. Without it the message states a disagreement and
	// leaves them to find which of their directories caused it.
	if !strings.Contains(stderr, "another account's name in "+siblingDir) {
		t.Errorf("the directory a user has to go and fix must be named: %q", stderr)
	}
	if strings.Contains(out, "Captured the changed claude credential") {
		t.Errorf("the success line may not claim an account kae did not attribute: %q", out)
	}
	if strings.Contains(stderr, refreshed) || strings.Contains(out, refreshed) {
		t.Errorf("a credential must never reach a message: %q / %q", stderr, out)
	}
}

// The same refusal with no sibling worktree at all, because a **globally isolated
// home** reads the account's credential store too — `prepareGlobalIsolatedHome`
// writes one per `kae use -i` / `kae run -i`, nothing removes it, and the reader walk
// reads those from disk with no liveness gate. So one stale `kae use -i` home is
// enough to reach this, and the entry's "two worktrees" is one shape of the mechanism
// rather than its extent — which is what makes the remedy line worth its own assertion
// here: the directory a user has to go fix is not one they pinned.
//
// Measured 2026-08-16, alongside the test above.
func TestReloginDeclinesALoginItWatchedWhenAnIsolatedHomeHasDrifted(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	_, _, credFile := boundStoreForClaudeMain(t, app)

	opts := commonOpts{Format: formatText}
	if code, out := captureStdout(t, func() int {
		return runUseIsolated(ctx, app, opts, constants.ToolClaude, "main")
	}); code != constants.ExitOK {
		t.Fatalf("use -i exit %d: %s", code, out)
	}
	home := app.Paths.GlobalIsolatedHomeDir(constants.ToolClaude, "main")
	if got := readFile(t, filepath.Join(home, ".claude.json")); !strings.Contains(got, "main-uuid") {
		t.Fatalf("the fixture needs the home to start out naming this account: %q", got)
	}
	// Somebody logged in as side inside that home. It has no fragment and no pin
	// record; it is a reader because the store path composes from the account name.
	writeFile(t, filepath.Join(home, ".claude.json"), claudeIdentityFile("side-uuid"))

	const refreshed = "sk-ant-oat01-RELOGGED-bbbb"
	seen := []string{}
	withInteractive(t, loginInto(t, constants.ToolClaude, refreshed, "main-uuid", now.Add(8*time.Hour), &seen))

	code, out, stderr := captureBoth(t, func() int {
		return runRelogin(ctx, app, opts, "")
	})
	mustExit(t, constants.ExitOK, code, stderr)

	if got := readFile(t, credFile); !strings.Contains(got, refreshed) {
		t.Fatalf("the login must land in the bound store for this to be about attribution: %s", got)
	}
	if got := readFile(t, filepath.Join(home, ".claude.json")); !strings.Contains(got, "side-uuid") {
		t.Fatalf("the home must still name another account: %q", got)
	}
	be := testBackend(t, app)
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); !strings.Contains(got, mainToken) {
		t.Fatalf("the harvest declines here; the snapshot keeps its own copy: %s", got)
	}
	if !strings.Contains(stderr, "disagree about whose login it is") {
		t.Errorf("the reason must be the reader disagreement: %q", stderr)
	}
	// The home itself, which is where the tool runs — and the case `kae doctor` does not
	// reach, so a message routed through doctor would have named nothing here.
	if !strings.Contains(stderr, "another account's name in "+app.displayPath(home)) {
		t.Errorf("the isolated home must be named: %q", stderr)
	}
	if strings.Contains(out, "Captured the changed claude credential") {
		t.Errorf("the success line may not claim an account kae did not attribute: %q", out)
	}
}

// What the refusal above is protecting, and the reason an override of it was built and
// reverted: the flow leaves nothing of its own — an abort, or a failure — while the
// **sibling** refreshes the shared store in place, which is a perfectly ordinary thing
// for its own claude to do during an interactive login. The store changed, so relogin
// does not take its unchanged branch and the harvest runs; the copy there is the
// sibling's, and this directory's label is the one `kae pin` planted. Capturing it files
// another account's token under this name, which is the one mistake nothing offline can
// detect afterwards.
//
// Nothing kae can read separates this run from the test above — same readers, same
// disagreement, same confirming label here. An override gated on "kae saw the tool write
// its label here" was measured not to separate them either (attributionSource), and a
// review reproduced exactly this leak through it before it was reverted.
func TestReloginDoesNotCaptureASiblingsRefreshedCopy(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	dir, _, credFile := boundStoreForClaudeMain(t, app)
	_, siblingStore := bindClaudeHere(t, app, "main")
	writeFile(t, filepath.Join(siblingStore, ".claude.json"), claudeIdentityFile("side-uuid"))
	// Somebody logged in as side inside the sibling, so the shared store holds side's copy.
	writeFile(t, credFile, claudeOAuthPayload("sk-ant-oat01-SIDE-OLD-dddd", now.Add(2*time.Hour)))
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	const sideRefreshed = "sk-ant-oat01-SIDE-REFRESHED-eeee"
	withInteractive(t, func(_ context.Context, extraEnv []string, _ string, _ ...string) (int, error) {
		credDir := ""
		for _, entry := range extraEnv {
			if rest, ok := strings.CutPrefix(entry, credentialEnvVar(constants.ToolClaude)+"="); ok {
				credDir = rest
			}
		}
		// The sibling's claude refreshes side's token while the user abandons the flow.
		// Nothing is written in *this* directory.
		writeFile(t, filepath.Join(credDir, ".credentials.json"),
			claudeOAuthPayload(sideRefreshed, now.Add(8*time.Hour)))
		return 1, nil
	})

	code, out, stderr := captureBoth(t, func() int {
		return runRelogin(ctx, app, commonOpts{Format: formatText}, "")
	})
	mustExit(t, constants.ExitOK, code, stderr)

	// Positive control: the store did change, so the harvest ran rather than being
	// short-circuited by relogin's unchanged branch, which returns before it.
	if got := readFile(t, credFile); !strings.Contains(got, sideRefreshed) {
		t.Fatalf("the fixture must leave the sibling's refreshed copy in the store: %s", got)
	}
	be := testBackend(t, app)
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); strings.Contains(got, sideRefreshed) {
		t.Fatalf("another account's token must not be filed under this one: %s", got)
	}
	if !strings.Contains(stderr, "disagree about whose login it is") {
		t.Errorf("the refusal must still be the reader disagreement: %q", stderr)
	}
	if strings.Contains(out, "Captured the changed claude credential") {
		t.Errorf("the success line may not claim an account kae did not attribute: %q", out)
	}
}

// The `disagree` outcome reached from the other side: the login kae just ran was as
// **another account**, so this directory's own label now names that account, while an
// honest sibling still reads the store as this one's. TestReloginDoesNotFileAnother
// AccountsLoginUnderThisAccount is the same user action with no sibling, where the
// answer is `Conflicting` and the message names a re-bind; here a confirming reader
// makes it the disagreement instead, and only the reason differs.
//
// It is the arm any override of that outcome has to leave alone, which is what makes it
// worth its own fixture rather than a variant comment.
func TestReloginDoesNotCaptureAForeignLoginItWatched(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	dir, _, credFile := boundStoreForClaudeMain(t, app)
	// An honest sibling: bound to the same account and still reading the store as its own.
	_, siblingStore := bindClaudeHere(t, app, "main")
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	const foreign = "sk-ant-oat01-SIDE-ffff"
	seen := []string{}
	withInteractive(t, loginInto(t, constants.ToolClaude, foreign, "side-uuid", now.Add(8*time.Hour), &seen))

	code, out, stderr := captureBoth(t, func() int {
		return runRelogin(ctx, app, commonOpts{Format: formatText}, "")
	})
	mustExit(t, constants.ExitOK, code, stderr)

	if got := readFile(t, filepath.Join(siblingStore, ".claude.json")); !strings.Contains(got, "main-uuid") {
		t.Fatalf("the sibling must be the confirming reader for this to be the disagree outcome: %q", got)
	}
	if got := readFile(t, credFile); !strings.Contains(got, foreign) {
		t.Fatalf("the login still belongs in the store it was made in: %s", got)
	}
	be := testBackend(t, app)
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); !strings.Contains(got, mainToken) {
		t.Fatalf("another account's login must not reach this snapshot: %s", got)
	}
	if strings.Contains(out, "Captured the changed claude credential") {
		t.Errorf("the success line may not claim an account kae did not attribute: %q", out)
	}
	// **The directory the user is standing in is not somewhere to send them.** It is one of
	// the readers that disagree here — that is what the login just made it — so a clause
	// naming the disagreeing directories names the cwd unless it is excluded, four clauses
	// after the same message called it "this directory". Measured doing exactly that
	// (2026-08-16, an independent review), and the fixture above was green through it
	// because it asserted nothing about this clause.
	if strings.Contains(stderr, "another account's name in "+dir) {
		t.Errorf("the remedy must not send the user to the directory they are in: %q", stderr)
	}
}
