package cmd

// Tests of the harvests that account rename, rebind and the sweep run over a
// directory's credential copy.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/config"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/paths"
	"github.com/webkaz-labs/kagikae/internal/runner"
)

// renameFixtureApp is overlayTestApp with the config **on disk** that a rename needs, which
// is not the same thing as one in memory: `buildAccountRename` edits the profile reference
// through `editConfig`, and that reloads `app.Config` from the file — so a secret backend
// set only in memory is gone by the time the rest of the command reads it, and the harvest
// fails with "no OS credential store found" on a machine that has none. The `[security]`
// line is what makes these tests run the same way everywhere; it is not decoration.
func renameFixtureApp(t *testing.T) *App {
	t.Helper()
	app := overlayTestApp(t)
	writeConfigFile(t, app, "version = 1\n[security]\nsecret_backend = \"file\"\n"+
		"[profiles.main.accounts]\nclaude = \"main\"\n")
	return app
}

// `kae account rename` deletes the old account's credential refs, and every delete of a
// copy a bound directory is reading harvests first (docs/CREDENTIAL-RULES.md § Harvesting
// before a write or a delete). It did not, so renaming an account out from under a bound
// directory cost the newest login: the tool refreshes that copy in place, the rename
// carried only the older snapshot to the new name, and following kae's own remedy
// (`kae pin <tool> <new>`) built the new store from that older snapshot.
//
// The harvest goes into the **old** name, before the rename's first write, and stage 1
// carries the result forward — so this test's real subject is that ordering. Measured
// 2026-08-16; the whole path is in harvestRenamedAccountCredentials' doc.
//
// The fixture has to be pinWithIdentifiedClaude and not its sibling: an account with no
// recorded `/oauthAccount` cannot be attributed, so a harvest against it refuses whatever
// kae does, and a test written on it stays green against a broken kae and a fixed one
// alike. That is not hypothetical — this test was written that way first.
func TestAccountRenameHarvestsWhatTheBoundDirectoryIsReading(t *testing.T) {
	app := renameFixtureApp(t)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	_, _, live := pinWithIdentifiedClaude(t, app, modeIsolated)
	// What the tool refreshed in place while the directory was bound: newer than the
	// snapshot, and the only copy that can still refresh.
	const refreshed = "sk-ant-oat01-MAIN-REFRESHED-cccc"
	writeFile(t, live, claudeOAuthPayload(refreshed, app.Now().Add(8*time.Hour)))

	be := testBackend(t, app)
	if _, err := buildAccountRename(ctx, app, opts, "claude", "main", "side"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "side"); !strings.Contains(got, refreshed) {
		t.Fatalf("the renamed account must carry the copy the directory was reading, not the older snapshot: %s", got)
	}
	// Harvesting is not deleting: the copy stays where the tool put it. `kae unpin --purge`
	// is what removes it, and by then the snapshot already holds it.
	if got := readFile(t, live); !strings.Contains(got, refreshed) {
		t.Fatalf("the harvest copies and must not remove: %q", got)
	}

	// The payoff a user sees: kae's own remedy now lands on a snapshot that has the live
	// token, so the directory keeps working instead of asking for a login.
	if code := runRebind(ctx, app, opts, constants.ToolClaude, "side", false); code != constants.ExitOK {
		t.Fatalf("the re-bind kae recommends exit %d", code)
	}
	rebound := dirCredFile(app, constants.ToolClaude, "side", "")
	if got := readFile(t, rebound); !strings.Contains(got, refreshed) {
		t.Fatalf("re-binding must materialize the harvested copy, not the older one (%s): %q", rebound, got)
	}
}

// A harvest kae cannot attribute must not be silent, because the rename goes ahead either
// way and the copy is then under a name no account has. This is the arm that used to say
// nothing at all — the defect measured on 2026-08-16 was silence, not wrong wording.
//
// pinWithCapturedClaude on purpose: its account records no `/oauthAccount`, which is a real
// shape (a tool that never wrote one, and agy, which cannot expose one at all) and the one
// that reaches the refusal without hand-editing anything. Its own doc carries the contrast.
func TestAccountRenameSaysSoWhenItCannotHarvest(t *testing.T) {
	app := renameFixtureApp(t)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	_, _, live := pinWithCapturedClaude(t, app, modeIsolated)
	const refreshed = "sk-ant-oat01-MAIN-REFRESHED-cccc"
	writeFile(t, live, claudeOAuthPayload(refreshed, app.Now().Add(8*time.Hour)))

	_, stderr := captureStderr(t, func() int {
		if _, err := buildAccountRename(ctx, app, opts, "claude", "main", "side"); err != nil {
			t.Fatalf("rename: %v", err)
		}
		return constants.ExitOK
	})
	if !strings.Contains(stderr, "is not harvesting it because") {
		t.Fatalf("a copy kae could not keep must be reported, not stranded in silence: %q", stderr)
	}
	// And it must say the thing kae used to get wrong: that re-binding does not recover it.
	if !strings.Contains(stderr, "re-binding will not reach it") {
		t.Fatalf("the warning must not repeat the remedy that does not work: %q", stderr)
	}
	// A refusal keeps. Nothing here may delete the copy it declined to attribute.
	if got := readFile(t, live); !strings.Contains(got, refreshed) {
		t.Fatalf("a refusal must leave the copy in place: %q", got)
	}
}

// `kae unpin --purge` still destroys the leftover copy under the old name — that is the
// documented choice (docs/CLI.md § kae pin), since keeping a copy kae cannot attribute
// strands a secret nothing kae offers can remove. What changed is that it no longer costs
// anything: the rename harvested it on the way past, so the account holds it.
//
// The pair is the point. This test and the harvest test above are what say the destructive
// path is safe *because* of the preserving one, rather than in spite of it; before the
// rename harvested, this same sequence was a logout with a warning that recommended a
// re-bind which could not have helped.
func TestUnpinPurgeAfterRenameLosesNothingTheRenameHarvested(t *testing.T) {
	app := renameFixtureApp(t)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	_, _, live := pinWithIdentifiedClaude(t, app, modeIsolated)
	const refreshed = "sk-ant-oat01-MAIN-REFRESHED-cccc"
	writeFile(t, live, claudeOAuthPayload(refreshed, app.Now().Add(8*time.Hour)))
	be := testBackend(t, app)
	if _, err := buildAccountRename(ctx, app, opts, "claude", "main", "side"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	// Control: the leftover is still there, so what the purge does below is observable.
	if !strings.Contains(readFile(t, live), refreshed) {
		t.Fatalf("the rename must leave the copy for the purge to take (%s)", live)
	}

	if code, stderr := captureStderr(t, func() int { return runUnpin(ctx, app, opts, true) }); code != constants.ExitOK {
		t.Fatalf("unpin --purge exit %d (%q)", code, stderr)
	}
	if got := readFile(t, live); strings.Contains(got, refreshed) {
		t.Fatalf("--purge takes a copy no named account holds; that is the documented choice: %q", got)
	}
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "side"); !strings.Contains(got, refreshed) {
		t.Fatalf("and the account must still hold what the purge destroyed: %s", got)
	}
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
	if code := runPin(ctx, app, opts, "main", modeIsolated, false); code != constants.ExitOK {
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
	code := runRebind(ctx, app, opts, constants.ToolClaude, "side", false)

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
		if code := runPin(ctx, app, opts, "main", modeIsolated, false); code != constants.ExitOK {
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

		_, stderr := captureStderr(t, func() int { return runPin(ctx, app, opts, "main", modeIsolated, false) })

		if strings.Contains(strings.Join(sim.ops, ","), "delete") {
			t.Fatalf("a bind must not delete the credential of an account it cannot harvest into: %v", sim.ops)
		}
		// Branch-specific: "left in place" is shared by the unreadable-store, the
		// unattributable and the account-gone messages, so matching it alone would pass on
		// a run that took a different arm entirely.
		if !strings.Contains(stderr, "account claude/main no longer exists") {
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

	if code := runPin(ctx, app, opts, "main", modeIsolated, false); code != constants.ExitOK {
		t.Fatalf("pin --isolated exit %d", code)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	old := app.Paths.IsolatedConfigDir(paths.PinID(cwd), constants.ToolClaude, "main")
	const refreshed = "sk-ant-oat01-MAIN-REFRESHED-cccc"
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", old), claudeOAuthPayload(refreshed, now.Add(8*time.Hour)))

	if code := runRebind(ctx, app, opts, constants.ToolClaude, "side", false); code != constants.ExitOK {
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
// What the pass may claim about a store the bind is moving **off**. A re-bind to another
// account writes a different credential store, so the copy this refusal is about is
// abandoned rather than replaced — and telling the user a live login is being spent when it
// is being stranded is the inverse of the fact they need to act on.
//
// It also has to name where the credential is. `store.Dir` is the config dir, which since
// the split holds no credential at all; every other speaker in this file was moved to
// credDirOrConfig after a smoke run found one naming "neither the thing removed nor where
// it lived". Both halves survived the whole suite until this test existed (measured
// 2026-08-08); the sibling arm's guard covered only its own direction.
func TestRunRebindConflictingCopyIsLeftBehindNotReplaced(t *testing.T) {
	app := overlayTestApp(t)
	chdirTemp(t)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	captureClaudeAt(t, app, "side", sideToken, now.Add(time.Hour))

	if code := runPin(ctx, app, opts, "main", modeShared, false); code != constants.ExitOK {
		t.Fatalf("pin --shared exit %d", code)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	shared := app.Paths.SharedDir(paths.PinID(cwd), constants.ToolClaude)
	mainStore := app.credStoreDir(constants.ToolClaude, "main")
	// A login as side inside the directory: this reader says side, and claude/main's own
	// store holds side's newer token. Re-binding to side is what identity_drift's "keep
	// what is there instead" tells the user to do.
	writeFile(t, filepath.Join(shared, ".claude.json"), claudeIdentityFile("side-uuid"))
	const sideLive = "sk-ant-oat01-SIDE-LIVE-eeee"
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", shared), claudeOAuthPayload(sideLive, now.Add(8*time.Hour)))

	_, stderr := captureStderr(t, func() int { return runRebind(ctx, app, opts, constants.ToolClaude, "side", false) })

	if !strings.Contains(stderr, "belongs to an account other than claude/main") {
		t.Fatalf("the refusal itself must still be reported: %q", stderr)
	}
	if strings.Contains(stderr, "this bind replaces it") {
		t.Fatalf("the store this bind moves off is not replaced by it: %q", stderr)
	}
	if !strings.Contains(stderr, "so this bind leaves it in place") {
		t.Fatalf("the consequence must be the one that happens: %q", stderr)
	}
	if !strings.Contains(stderr, mainStore) {
		t.Fatalf("the message must name where the credential is: %q", stderr)
	}
	// Positive control on the location halves: the config dir is a different directory,
	// and naming it would send the reader to one holding no credential.
	if strings.Contains(stderr, shared) {
		t.Fatalf("the config dir holds no credential and must not be named as its home: %q", stderr)
	}
	// And it really was left: the copy is still there, unharvested, for `unpin --purge`
	// or a re-bind back to reach.
	if got := readFile(t, filepath.Join(mainStore, ".credentials.json")); !strings.Contains(got, sideLive) {
		t.Fatalf("the abandoned copy must survive the re-bind: %s", got)
	}
}

func TestRunRebindSharedHarvestsThePreviousAccount(t *testing.T) {
	app := overlayTestApp(t)
	chdirTemp(t)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	captureClaudeAt(t, app, "side", sideToken, now.Add(time.Hour))

	if code := runPin(ctx, app, opts, "main", modeShared, false); code != constants.ExitOK {
		t.Fatalf("pin --shared exit %d", code)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	shared := app.Paths.SharedDir(paths.PinID(cwd), constants.ToolClaude)
	const refreshed = "sk-ant-oat01-MAIN-REFRESHED-cccc"
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", shared), claudeOAuthPayload(refreshed, now.Add(8*time.Hour)))

	if code := runRebind(ctx, app, opts, constants.ToolClaude, "side", false); code != constants.ExitOK {
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

// The half a walk of the bound directories does not reach: a globally isolated home
// (`kae use -i`) reads the account's own credential store with no fragment and no pin
// record anywhere, so the first version of the rename's harvest reported nothing to do and
// lost the copy exactly as before (measured 2026-08-16, with `pins=0`). credStoreReaders is
// what spans both, and this test is why the pass asks it instead of walking pins.
//
// The fail-closed contract requires the documented teardown first. That teardown leaves
// this home in place, so the retried rename must still discover it from disk and harvest
// the refreshed copy before changing the account. This is the credential-ordering half
// of the remedy in docs/ROADMAP.md § `kae account rename` leaves `state.synced` and the
// global fragment on the old name.
func TestAccountRenameHarvestsWhatAGloballyIsolatedHomeReads(t *testing.T) {
	app := renameFixtureApp(t)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	if code := runUseIsolated(ctx, app, opts, "claude", "main"); code != constants.ExitOK {
		t.Fatalf("use --isolated exit %d", code)
	}
	home := app.Paths.GlobalIsolatedHomeDir(constants.ToolClaude, "main")
	live := dirCredFile(app, constants.ToolClaude, "main", home)
	if !strings.Contains(readFile(t, live), mainToken) {
		t.Fatalf("the isolated home must read a materialized credential at %s", live)
	}
	// Nothing is bound: this is the state the pin walk cannot see.
	if index := app.boundDirectoryIndex(); index.err != nil || !index.complete || len(index.directories) != 0 {
		t.Fatalf("this test is only about the unpinned case: index=%+v", index)
	}
	const refreshed = "sk-ant-oat01-MAIN-REFRESHED-cccc"
	writeFile(t, live, claudeOAuthPayload(refreshed, now.Add(8*time.Hour)))
	if code := runSwitch(ctx, app, opts, "claude", "main"); code != constants.ExitOK {
		t.Fatalf("use --shared teardown exit %d", code)
	}

	be := testBackend(t, app)
	if _, err := buildAccountRename(ctx, app, opts, "claude", "main", "side"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "side"); !strings.Contains(got, refreshed) {
		t.Fatalf("the renamed account must carry what the isolated home was reading: %s", got)
	}
	if _, err := os.Stat(home); err != nil {
		t.Fatalf("rename must retain the old isolated home for recovery: %v", err)
	}
}

// The other half of the pass, and nothing reached it until this test: a binding made
// before the credential split keeps its copy *inside* the per-directory store, so
// credStoreReaders never names it and only a walk of the bound directories does. Disabling
// that walk passed the entire suite (measured 2026-08-16), which is the same shape as a
// test that covers one side of a two-sided predicate and reads as if it covered both.
//
// makePreSplit builds the state deliberately: kae cannot produce it any more, and a
// directory bound by an older release keeps its own credential until it is re-pinned.
func TestAccountRenameHarvestsAPreSplitDirectorysOwnCopy(t *testing.T) {
	app := renameFixtureApp(t)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	dir, storeDir, _ := pinWithIdentifiedClaude(t, app, modeIsolated)
	makePreSplit(t, app, constants.ToolClaude, "main", dir, storeDir)

	inStore := filepath.Join(storeDir, ".credentials.json")
	if !strings.Contains(readFile(t, inStore), mainToken) {
		t.Fatalf("the pre-split fixture must leave the credential inside the store (%s)", inStore)
	}
	const refreshed = "sk-ant-oat01-MAIN-REFRESHED-cccc"
	writeFile(t, inStore, claudeOAuthPayload(refreshed, app.Now().Add(8*time.Hour)))

	be := testBackend(t, app)
	if _, err := buildAccountRename(ctx, app, opts, "claude", "main", "side"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "side"); !strings.Contains(got, refreshed) {
		t.Fatalf("the renamed account must carry the pre-split directory's own copy: %s", got)
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
	if code := runPin(ctx, app, opts, "main", modeIsolated, false); code != constants.ExitOK {
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

	_, stderr := captureStderr(t, func() int { return runRebind(ctx, app, opts, constants.ToolClaude, "side", false) })

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

// `kae account rename` warns and tells the user to re-bind with `kae pin <tool> <new>`,
// which is **runRebind** — so that is the sweep the renamed account's newest copy meets,
// and its `purging=false` needs pinning separately from `runPin`'s (reading-type review,
// round 3: mutating this one to `true` passed the whole suite).
//
// That first sentence describes a rename that no longer reaches this sweep: the rename
// harvests before its first write now, so a renamed account's copy is already in the
// snapshot (TestAccountRenameHarvestsWhatTheBoundDirectoryIsReading). What still arrives
// here is an account gone by **removal**, and only for a store holding its own credential
// — the shape makePreSplit builds below. Measured 2026-08-16; a sweep for the unscoped
// version of that sentence reached the docs and both dircred.go comments and missed this
// one, because it grepped prose files.
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
		if code := runPin(ctx, app, opts, "main", modeIsolated, false); code != constants.ExitOK {
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
			return runRebind(ctx, app, opts, constants.ToolClaude, "side", false)
		})

		if strings.Contains(strings.Join(sim.ops, ","), "delete") {
			t.Fatalf("the re-bind kae itself recommends after a rename must not delete that copy: %v", sim.ops)
		}
		if !strings.Contains(stderr, "account claude/main no longer exists") {
			t.Fatalf("keeping it must name the branch that kept it: %q", stderr)
		}
	})
}
