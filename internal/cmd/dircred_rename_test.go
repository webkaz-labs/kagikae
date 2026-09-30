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

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/config"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/paths"
	"github.com/webkaz-labs/kagikae/internal/runner"
	"github.com/webkaz-labs/kagikae/internal/testutil/runnertest"
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
	if !strings.Contains(stderr, "so kae is leaving it where it is") {
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
			app := overlayTestApp(t)
			ctx := context.Background()
			captureClaudeAt(t, app, "main", mainToken, app.Now().Add(time.Hour))
			_, credDir := bindClaudeHere(t, app, "main")
			writeFile(t, dirCredFile(app, constants.ToolClaude, "main", credDir),
				`{"claudeAiOauth":`+tc.oauth+`}`)

			be := testBackend(t, app)
			_, stderr := captureStderr(t, func() int {
				if err := app.writeDirCredential(ctx, be, constants.ToolClaude, "main", credDir, false); err != nil {
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
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	_, storeDir := bindClaudeHere(t, app, "main")
	const refreshed = "sk-ant-oat01-MAIN-REFRESHED-cccc"
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", storeDir), claudeOAuthPayload(refreshed, now.Add(8*time.Hour)))
	// The reader's own cache replaced by a link back out to the real home — the shape a
	// pre-v0.16.0 shared bind left — still naming the account being bound.
	identityFile := filepath.Join(storeDir, ".claude.json")
	if err := os.Remove(identityFile); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(app.Env.Home, ".claude.json"), identityFile); err != nil {
		t.Fatal(err)
	}
	// Positive first: the link resolves to a payload that names the *same* account, so the
	// refusal below is the escape guard rather than a disagreement or an unreadable file.
	if got := readFile(t, identityFile); !strings.Contains(got, "main-uuid") {
		t.Fatalf("the link must resolve to the real home's payload naming main, got %q", got)
	}

	be := testBackend(t, app)
	_, stderr := captureStderr(t, func() int {
		if err := app.writeDirCredential(ctx, be, constants.ToolClaude, "main", storeDir, false); err != nil {
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
			return app.prepareBond(ctx, be, tool, account, pinID, false)
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
