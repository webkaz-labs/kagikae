package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/adapter/claude"
	"github.com/webkaz-labs/kagikae/internal/config"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/paths"
	"github.com/webkaz-labs/kagikae/internal/runner"
	"github.com/webkaz-labs/kagikae/internal/testutil/runnertest"
)

// The property the whole split exists for: two directories bound to one account
// read **one** credential. Before it each held its own copy, and claude's refresh
// token rotates single-use, so whichever one the tool refreshed first logged the
// other out — with every offline check green in both.
//
// Asserted through the fragments rather than through the file, because the file is
// only where kae put it: what decides which credential the tool opens is the value
// the directory exports.
func TestTwoDirectoriesOfOneAccountShareOneCredential(t *testing.T) {
	app := overlayTestApp(t)
	captureClaudeAt(t, app, "main", mainToken, app.Now().Add(time.Hour))

	first, _, firstCred := boundStoreForClaudeMain(t, app)
	second, _, secondCred := boundStoreForClaudeMain(t, app)
	if first == second {
		t.Fatal("this test needs two distinct bound directories")
	}
	if firstCred != secondCred {
		t.Fatalf("the two bindings hold separate copies:\n%s\n%s", firstCred, secondCred)
	}
	want := app.credStoreDir(constants.ToolClaude, "main")
	for _, dir := range []string{first, second} {
		fragment, exists, err := readFragmentAt(dir)
		if err != nil || !exists {
			t.Fatalf("fragment for %s: exists=%v err=%v", dir, exists, err)
		}
		if got := fragment.CredDirs[constants.ToolClaude]; got != want {
			t.Fatalf("%s exports credential dir %q, want %q", dir, got, want)
		}
		// The config dirs must still differ, or this would be "one credential" bought
		// by sharing the sessions too — which is the design this one rejects.
		if fragment.Accounts[constants.ToolClaude] != "main" {
			t.Fatalf("%s does not bind claude/main: %+v", dir, fragment.Accounts)
		}
	}
	firstStore, _ := app.boundStoreDir(paths.PinID(first), constants.ToolClaude, mustFragmentAt(t, first))
	secondStore, _ := app.boundStoreDir(paths.PinID(second), constants.ToolClaude, mustFragmentAt(t, second))
	if firstStore == secondStore {
		t.Fatalf("the two directories must keep separate stores: %s", firstStore)
	}
}

// fragmentAt is mustFragment for a directory kae is not standing in — the
// sibling of readFragmentAt, since these tests compare two bound directories and
// only one of them can be the cwd.
func mustFragmentAt(t *testing.T, dir string) fragmentInfo {
	t.Helper()
	info, exists, err := readFragmentAt(dir)
	if err != nil || !exists {
		t.Fatalf("fragment for %s: exists=%v err=%v", dir, exists, err)
	}
	return info
}

// A shared credential is only safe to share if nothing deletes it out from under a
// sibling. `kae unpin --purge` in one directory used to remove the item that
// directory read; now that item is the account's, so the purge has to count what
// still points at it first.
func TestUnpinPurgeKeepsACredentialASiblingStillBinds(t *testing.T) {
	sim := &keychainSim{}
	runner.With(sim, func() {
		app := overlayTestApp(t)
		app.Env.GOOS = "darwin"
		ctx := context.Background()
		opts := commonOpts{Format: formatText}
		captureClaudeFromKeychain(t, app, sim, "main", mainToken, app.Now().Add(time.Hour))

		// pinHere, not boundStoreForClaudeMain: under the keychain driver the store
		// holds an item rather than a file, and that fixture asserts a file.
		keeper := pinHere(t, app, modeShared)
		purged := pinHere(t, app, modeShared)
		if keeper == purged {
			t.Fatal("this test needs two distinct bound directories")
		}
		sim.ops = nil

		// cwd is the second directory (pinHere chdirs), so this purges that one.
		_, stderr := captureStderr(t, func() int { return runUnpin(ctx, app, opts, true) })

		if strings.Contains(strings.Join(sim.ops, ","), "delete") {
			t.Fatalf("purging one directory must not delete a credential another still binds: %v", sim.ops)
		}
		if !strings.Contains(stderr, "still use the claude credential for main") {
			t.Fatalf("keeping it must say why: %q", stderr)
		}
		if _, err := os.Stat(filepath.Join(keeper, fragmentRelPath)); err != nil {
			t.Fatalf("the sibling binding must be untouched: %v", err)
		}
	})
}

// …and the other side of it: with nothing left pointing at the store, `--purge`
// still does what it is for. Without this, the guard above could be "never delete"
// and every test would pass.
func TestUnpinPurgeRemovesACredentialNothingElseBinds(t *testing.T) {
	sim := &keychainSim{}
	runner.With(sim, func() {
		app := overlayTestApp(t)
		app.Env.GOOS = "darwin"
		ctx := context.Background()
		opts := commonOpts{Format: formatText}
		captureClaudeFromKeychain(t, app, sim, "main", mainToken, app.Now().Add(time.Hour))
		pinHere(t, app, modeShared)
		sim.ops = nil

		code, out := captureStdout(t, func() int { return runUnpin(ctx, app, opts, true) })
		if code != constants.ExitOK {
			t.Fatalf("unpin --purge exit %d: %s", code, out)
		}
		if !strings.Contains(strings.Join(sim.ops, ","), "delete") {
			t.Fatalf("the last binding's purge must remove the credential: %v", sim.ops)
		}
		// What it removed is the account's own credential, not this directory's copy of
		// one, and it lived in the credential store rather than the config-dir store.
		// The two negative assertions elsewhere in this file cannot see a message that
		// is merely *wrong*, which is how "per-directory … (<config dir>)" survived.
		if !strings.Contains(out, "credential this account's bindings shared") {
			t.Errorf("the purge must say it removed the account's shared credential: %q", out)
		}
		if !strings.Contains(out, app.Paths.CredStoreDir(constants.ToolClaude, "main")) {
			t.Errorf("the purge must name where the credential lived: %q", out)
		}
		if strings.Contains(out, "per-directory") {
			t.Errorf("an account-wide credential must not be reported as per-directory: %q", out)
		}
	})
}

// A globally isolated home reads the same store without any fragment naming it, so
// it has to count as a reference too. Nothing else would notice: `kae use -i` leaves
// no per-directory record at all.
func TestUnpinPurgeKeepsACredentialAGloballyIsolatedHomeUses(t *testing.T) {
	sim := &keychainSim{}
	runner.With(sim, func() {
		app := overlayTestApp(t)
		app.Env.GOOS = "darwin"
		ctx := context.Background()
		opts := commonOpts{Format: formatText}
		captureClaudeFromKeychain(t, app, sim, "main", mainToken, app.Now().Add(time.Hour))
		pinHere(t, app, modeShared)
		if code, out := captureStdout(t, func() int {
			return runUseIsolated(ctx, app, opts, constants.ToolClaude, "main")
		}); code != constants.ExitOK {
			t.Fatalf("use -i exit %d: %s", code, out)
		}
		sim.ops = nil

		_, stderr := captureStderr(t, func() int { return runUnpin(ctx, app, opts, true) })

		if strings.Contains(strings.Join(sim.ops, ","), "delete") {
			t.Fatalf("a globally isolated home still reads that credential: %v", sim.ops)
		}
		if !strings.Contains(stderr, "still use the claude credential for main") {
			t.Fatalf("keeping it must say why: %q", stderr)
		}
	})
}

// The migration itself, on the sweep side: re-running `kae pin` moves the credential
// into the account's store, and the per-directory item the old binding left behind
// has to go with it. It is addressed by a service name nothing resolves any more, so
// nothing would ever reach it again — and an item at the config dir's name is exactly
// the signal the offline regression detector reads as "something wrote there since
// the last bind" (docs/ROADMAP.md), which a permanent leftover would poison.
//
// The store directory itself survives: it holds the sessions.
func TestRePinMigrationRemovesThePreSplitItem(t *testing.T) {
	app := overlayTestApp(t)
	app.Env.GOOS = "darwin"
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	payload := claudeOAuthPayload(mainToken, app.Now().Add(time.Hour))
	runner.With(&runnertest.Fake{Stdout: payload, Code: 0}, func() {
		captureClaude(t, app, "main", mainToken)
	})
	dir := pinHere(t, app, modeShared)
	storeDir := app.Paths.SharedDir(paths.PinID(dir), constants.ToolClaude)
	makePreSplit(t, app, constants.ToolClaude, "main", dir, storeDir)

	var out string
	runner.With(&runnertest.Fake{Stdout: payload, Code: 0}, func() {
		var code int
		code, out = captureStdout(t, func() int { return runPin(ctx, app, opts, "main", modeShared) })
		mustExit(t, constants.ExitOK, code, out)
	})

	if !strings.Contains(out, storeDir) {
		t.Fatalf("the pre-split credential must be reported as removed:\n%s", out)
	}
	// The store keeps the sessions a re-pin restores; only the credential moved.
	if !dirExists(storeDir) {
		t.Fatal("the store directory must survive the migration")
	}
}

// The same migration under the **file** driver, which is a separate case and was a
// real gap: the sweep's kind gate kept a file credential because it "lives inside the
// store directory", and that reasoning stops holding the moment the credential has
// moved out of it. Every reader now resolves through the new location, so a leftover
// there is invisible — and a shell exporting only the config variable would find it,
// refresh it, and invalidate the copy every directory bound to that account shares.
func TestRePinMigrationRemovesThePreSplitFile(t *testing.T) {
	app := overlayTestApp(t) // linux: the file driver
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	captureClaudeAt(t, app, "main", mainToken, app.Now().Add(time.Hour))
	dir := pinHere(t, app, modeShared)
	storeDir := app.Paths.SharedDir(paths.PinID(dir), constants.ToolClaude)
	makePreSplit(t, app, constants.ToolClaude, "main", dir, storeDir)
	preSplit := filepath.Join(storeDir, ".credentials.json")
	if !strings.Contains(readFile(t, preSplit), mainToken) {
		t.Fatal("the fixture must leave a pre-split copy for this test to mean anything")
	}

	if code, out := captureStdout(t, func() int { return runPin(ctx, app, opts, "main", modeShared) }); code != constants.ExitOK {
		t.Fatalf("re-pin exit %d: %s", code, out)
	}

	if got := readFile(t, preSplit); strings.Contains(got, mainToken) {
		t.Fatalf("the pre-split copy must go with the credential: %s", got)
	}
	credFile := filepath.Join(app.credStoreDir(constants.ToolClaude, "main"), ".credentials.json")
	if got := readFile(t, credFile); !strings.Contains(got, mainToken) {
		t.Fatalf("the account store must hold it now: %s", got)
	}
}

// …and the other side of that exception: an ordinary re-bind, with nothing migrating,
// leaves the account's credential alone. Without this the guard above could be "sweep
// every kept store" and the migration test would still pass.
//
// **Isolated** mode, and that is load-bearing rather than incidental. A shared re-bind
// keeps one store dir, so the sweep skips it at the `keep` check and never reaches the
// gate under test — measured: with shared mode this test survived a mutation that
// removed the `!purging` return entirely. Isolated re-keys the store by account, so
// the store being left is not kept, reaches removeDirCredential, and the gate is the
// only thing standing between a re-bind and deleting the previous account's shared
// credential.
func TestAPlainRebindDoesNotDeleteTheAccountCredential(t *testing.T) {
	app := overlayTestApp(t)
	app.Env.GOOS = "darwin"
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	payload := claudeOAuthPayload(mainToken, app.Now().Add(time.Hour))
	runner.With(&runnertest.Fake{Stdout: payload, Code: 0}, func() {
		captureClaude(t, app, "main", mainToken)
		captureClaude(t, app, "side", sideToken)
	})
	dir := pinHere(t, app, modeIsolated)

	var out string
	runner.With(&runnertest.Fake{Stdout: payload, Code: 0}, func() {
		var code int
		code, out = captureStdout(t, func() int {
			return runRebind(ctx, app, opts, constants.ToolClaude, "side")
		})
		mustExit(t, constants.ExitOK, code, out)
	})

	// The store being left must actually be reached by the sweep, or the assertion
	// below holds for the reason the shared-mode version did: it was skipped.
	left := app.Paths.IsolatedConfigDir(paths.PinID(dir), constants.ToolClaude, "main")
	if !dirExists(left) {
		t.Fatalf("the previous account's store must exist for this test to mean anything: %s", left)
	}

	// main's credential is that account's, and this directory binding something else
	// says nothing about it — another worktree, or `kae use -i main`, may still read it.
	if strings.Contains(out, "Removed the superseded per-directory") {
		t.Fatalf("a bind must not delete an account's credential:\n%s", out)
	}
}

// Under the file driver the credential is a file in the account's store, and that
// store holds nothing else — no sessions, no settings. `--purge` therefore has to
// remove it, unlike a per-directory store's file, which `kae unpin` keeps on purpose
// along with everything beside it.
func TestUnpinPurgeRemovesAFileCredentialFromTheAccountStore(t *testing.T) {
	app := overlayTestApp(t) // linux: the file driver
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	captureClaudeAt(t, app, "main", mainToken, app.Now().Add(time.Hour))
	_, _, credFile := boundStoreForClaudeMain(t, app)
	if readFile(t, credFile) == "" {
		t.Fatal("the bind must materialize a credential for this test to mean anything")
	}

	if code, out := captureStdout(t, func() int { return runUnpin(ctx, app, opts, true) }); code != constants.ExitOK {
		t.Fatalf("unpin --purge exit %d: %s", code, out)
	}

	// The artifact, not the file: claude's file store is a JSON pointer inside
	// `.credentials.json`, so a delete removes `/claudeAiOauth` and leaves the
	// document. What must not survive is the token.
	if got := readFile(t, credFile); strings.Contains(got, mainToken) {
		t.Fatalf("the account store's credential must be removed by --purge: %s", got)
	}
}

// The purge that has something to lose, which the test above cannot reach: its store
// holds the copy the bind wrote from the snapshot, so the harvest returns at the
// timestamp comparison and attribution is never asked.
//
// The two gates are otherwise **mutually exclusive by construction**, and that is worth
// stating because it is not visible from either one. A per-account store may only be
// deleted once nothing points at it (`credStoreRefs == 0`), and the readers attribution
// asks about are enumerated from the same two sources — so at the moment the delete is
// allowed there is by definition no reader left to confirm the copy, and a refusal here
// is a deletion rather than a conservative choice. The directory being torn down is the
// honest reader: it read that credential until `runUnpin` removed its fragment moments
// earlier. Found by review, 2026-08-08.
func TestUnpinPurgeHarvestsANewerCopyBeforeRemovingIt(t *testing.T) {
	app := overlayTestApp(t) // linux: the file driver
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	_, storeDir := bindClaudeHere(t, app, "main")
	// The tool refreshed the account's copy in place since the bind.
	const refreshed = "sk-ant-oat01-MAIN-REFRESHED-cccc"
	credFile := dirCredFile(app, constants.ToolClaude, "main", storeDir)
	writeFile(t, credFile, claudeOAuthPayload(refreshed, now.Add(8*time.Hour)))

	if code, out := captureStdout(t, func() int { return runUnpin(ctx, app, opts, true) }); code != constants.ExitOK {
		t.Fatalf("unpin --purge exit %d: %s", code, out)
	}

	be := testBackend(t, app)
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); !strings.Contains(got, refreshed) {
		t.Fatalf("the last binding's purge must harvest before it deletes: %s", got)
	}
	if got := readFile(t, credFile); strings.Contains(got, refreshed) {
		t.Fatalf("and then remove it: %s", got)
	}
}

// A globally isolated home bound before the split still holds its own copy, and it
// has no pin, so neither the pin-level pass nor any sweep reaches it. Without a
// migration of its own, `kae use -i` writes the account snapshot into the credential
// store and leaves that copy — which under single-use rotation means the newer login
// is stranded and the home silently runs an older one, with no finding anywhere
// (`credential_unsplit` walks bound directories only).
func TestUseIsolatedMigratesAPreSplitHome(t *testing.T) {
	app := overlayTestApp(t) // linux: the file driver, so the copy is a file in the home
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))

	home := app.Paths.GlobalIsolatedHomeDir(constants.ToolClaude, "main")
	mkdirs(t, home)
	// The shape a pre-split `kae use -i` left: the credential in the home itself,
	// refreshed there since, with the identity that attributes it.
	const refreshed = "sk-ant-oat01-HOME-REFRESHED-cccc"
	writeFile(t, filepath.Join(home, ".credentials.json"), claudeOAuthPayload(refreshed, now.Add(8*time.Hour)))
	writeFile(t, filepath.Join(home, ".claude.json"), claudeIdentityFile("main-uuid"))

	if code, out := captureStdout(t, func() int {
		return runUseIsolated(ctx, app, opts, constants.ToolClaude, "main")
	}); code != constants.ExitOK {
		t.Fatalf("use -i exit %d: %s", code, out)
	}

	be := testBackend(t, app)
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); !strings.Contains(got, refreshed) {
		t.Fatalf("the home's newer copy must be harvested before it is superseded: %s", got)
	}
	credFile := filepath.Join(app.credStoreDir(constants.ToolClaude, "main"), ".credentials.json")
	if got := readFile(t, credFile); !strings.Contains(got, refreshed) {
		t.Fatalf("the account store must end up holding the newest copy: %s", got)
	}
	// And the copy nothing reads any more is gone: refreshing it elsewhere would
	// invalidate the one every binding of this account now shares.
	if got := readFile(t, filepath.Join(home, ".credentials.json")); strings.Contains(got, refreshed) {
		t.Fatalf("the pre-split copy must be removed once it is preserved: %s", got)
	}
}

// `kae relogin` exports what the **binding** records, not what a current bind would
// write. A directory bound before the split reads its own store, so deriving the
// credential dir from the account would drive the login into a store that directory
// does not read — and report a login that changed nothing while it stays stale.
func TestReloginExportsThePreSplitBindingsStore(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	captureClaudeAt(t, app, "main", mainToken, app.Now().Add(time.Hour))
	dir, storeDir, _ := boundStoreForClaudeMain(t, app)
	makePreSplit(t, app, constants.ToolClaude, "main", dir, storeDir)

	var seen []string
	withInteractive(t, loginInto(t, constants.ToolClaude,
		"sk-ant-oat01-RELOGGED-aaaa", "main-uuid", app.Now().Add(8*time.Hour), &seen))
	captureBoth(t, func() int { return runRelogin(ctx, app, commonOpts{Format: formatText}, "") })

	for _, entry := range seen {
		if strings.HasPrefix(entry, credentialEnvVar(constants.ToolClaude)+"=") {
			t.Fatalf("a pre-split binding records no credential store; kae must not invent one: %q", entry)
		}
	}
	// Positive control for the fixture: the flow really ran against this store.
	if !slices.Contains(seen, isolationEnvVar(constants.ToolClaude)+"="+storeDir) {
		t.Fatalf("the login must be driven into the bound store: %v", seen)
	}
}

// A tool that cannot separate its credential from its home has nothing to migrate,
// so naming it would be a warning the user can never clear.
func TestUnsplitCheckSaysNothingAboutAToolWithNoCredentialVariable(t *testing.T) {
	stores := []boundDirStore{{
		Dir: "/repo", Tool: constants.ToolCodex, Account: "main",
		StoreDir: "/store/codex", CredDir: "",
	}}
	if checks := pinUnsplitChecks(stores); len(checks) != 0 {
		t.Fatalf("codex keeps its credential in its home; nothing to report: %+v", checks)
	}
	// The control: the same shape for a tool that does split is reported.
	stores[0].Tool = constants.ToolClaude
	if checks := pinUnsplitChecks(stores); len(checks) != 1 {
		t.Fatalf("a claude binding with no credential entry must be reported: %+v", checks)
	}
}

// The export fallback is what a user pastes when mise is not active, so it has to
// carry both halves. With only the home variable the credential resolves at the
// config dir's name — a store kae has written nothing to.
func TestExportFallbackCarriesTheCredentialVariable(t *testing.T) {
	app := overlayTestApp(t)
	entry := app.isolationEntryFor(runTarget{Tool: constants.ToolClaude, Account: "main"}, "/store/shared")
	out := exportFallback("main", []isolationEntry{entry}, nil)

	for _, want := range []string{
		"export " + isolationEnvVar(constants.ToolClaude) + "='/store/shared'",
		"export " + credentialEnvVar(constants.ToolClaude) + "='" +
			app.credStoreDir(constants.ToolClaude, "main") + "'",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("the fallback must export %q:\n%s", want, out)
		}
	}
}

// The migration sweep runs **after** `writeDirIdentity` has stamped the store with
// this account's identity, so the evidence it would attribute a copy by is evidence
// kae wrote itself three steps earlier in the same command. Two consequences, and
// the second is the one this repo has bled on.
//
// Here: the pin-level pass refuses a copy it cannot attribute (no identity cache) —
// and then the sweep finds the identity the bind just wrote, agrees with itself, and
// harvests and deletes the copy the pass declined to touch.
func TestTheSweepDoesNotHarvestOnEvidenceTheBindJustWrote(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	dir := pinHere(t, app, modeShared)
	storeDir := app.Paths.SharedDir(paths.PinID(dir), constants.ToolClaude)
	makePreSplit(t, app, constants.ToolClaude, "main", dir, storeDir)

	const refreshed = "sk-ant-oat01-UNATTRIBUTABLE-cccc"
	writeFile(t, filepath.Join(storeDir, ".credentials.json"), claudeOAuthPayload(refreshed, now.Add(8*time.Hour)))
	// Nothing names the account this copy belongs to.
	if err := os.Remove(filepath.Join(storeDir, ".claude.json")); err != nil {
		t.Fatal(err)
	}

	_, stderr := captureStderr(t, func() int { return runPin(ctx, app, opts, "main", modeShared) })

	// The pass says it could not preserve it. The sweep must not then say it did.
	if !strings.Contains(stderr, "could not preserve") {
		t.Fatalf("the fixture must reach the pass's refusal: %q", stderr)
	}
	if strings.Contains(stderr, "harvested the newer claude credential") {
		t.Fatalf("a copy the pass refused must not be harvested by the sweep:\n%s", stderr)
	}
	if got := readFile(t, filepath.Join(storeDir, ".credentials.json")); !strings.Contains(got, refreshed) {
		t.Fatalf("a copy kae could not preserve must be kept, not deleted: %q", got)
	}
}

// …and the shape that files one account's token under another's name. A directory
// bound to main whose store holds **side's** copy and side's identity is reachable
// with no hostile input: it is what the pre-`kae relogin` remedy (`cd <dir> &&
// claude /login`) produced, and it is the state `identity_drift` exists to report.
//
// The pass refuses it on genuine evidence — the store's identity names another
// account. The sweep then runs after the bind has overwritten that identity with
// main's, compares main against main, and files side's token under main. The token
// is opaque, so live, snapshot and doctor would all agree on a label that is wrong.
func TestTheSweepDoesNotFileAnotherAccountsCopyUnderThisAccount(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	dir := pinHere(t, app, modeShared)
	storeDir := app.Paths.SharedDir(paths.PinID(dir), constants.ToolClaude)
	makePreSplit(t, app, constants.ToolClaude, "main", dir, storeDir)

	const sideRefreshed = "sk-ant-oat01-SIDE-REFRESHED-zzzz"
	writeFile(t, filepath.Join(storeDir, ".credentials.json"),
		claudeOAuthPayload(sideRefreshed, now.Add(8*time.Hour)))
	writeFile(t, filepath.Join(storeDir, ".claude.json"), claudeIdentityFile("side-uuid"))

	_, stderr := captureStderr(t, func() int { return runPin(ctx, app, opts, "main", modeShared) })

	if !strings.Contains(stderr, "belongs to an account other than") {
		t.Fatalf("the fixture must reach the pass's conflicting refusal: %q", stderr)
	}
	be := testBackend(t, app)
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); strings.Contains(got, sideRefreshed) {
		t.Fatalf("another account's token was filed under claude/main: %s", got)
	}
}

// The count has to fail closed, and it has two independent sources. "No reference
// found" and "kae could not look" differ by exactly one logged-out sibling, so each
// source gets a case rather than one standing in for the other — the fragment walk,
// and the pin index that names the directories to walk.
//
// Both are reached by turning the sibling's file into a **directory** after it was
// written, so the index still names it and the walk still reaches it: an unreadable
// source, not a missing one.
func TestUnpinPurgeKeepsACredentialItCannotCountReferencesFor(t *testing.T) {
	for _, tc := range []struct {
		name string
		path func(app *App, other string) string // the file to clobber
	}{
		{"unreadable fragment", func(_ *App, other string) string {
			return filepath.Join(other, fragmentRelPath)
		}},
		{"unreadable pin record", func(app *App, other string) string {
			return app.Paths.PinRecordFile(paths.PinID(other))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sim := &keychainSim{}
			runner.With(sim, func() {
				app := overlayTestApp(t)
				app.Env.GOOS = "darwin"
				ctx := context.Background()
				opts := commonOpts{Format: formatText}
				captureClaudeFromKeychain(t, app, sim, "main", mainToken, app.Now().Add(time.Hour))
				other := pinHere(t, app, modeShared)
				pinHere(t, app, modeShared)

				clobber := tc.path(app, other)
				if err := os.Remove(clobber); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(clobber, 0o700); err != nil {
					t.Fatal(err)
				}
				sim.ops = nil

				_, stderr := captureStderr(t, func() int { return runUnpin(ctx, app, opts, true) })

				if strings.Contains(strings.Join(sim.ops, ","), "delete") {
					t.Fatalf("a count kae could not complete must keep the credential: %v", sim.ops)
				}
				if !strings.Contains(stderr, "could not tell whether another binding still uses") {
					t.Fatalf("keeping it for that reason must say so: %q", stderr)
				}
			})
		})
	}
}

// The negative control `migratePreSplitHome` owed: it deletes only what it could
// preserve. Without this, dropping its guard entirely — delete unconditionally —
// leaves every test green, and the doc comment's promise ("an unattributable,
// unreadable or unpreservable copy is left where it is") is unbacked.
func TestUseIsolatedKeepsAPreSplitHomeCopyItCannotAttribute(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))

	home := app.Paths.GlobalIsolatedHomeDir(constants.ToolClaude, "main")
	mkdirs(t, home)
	const refreshed = "sk-ant-oat01-HOME-UNATTRIBUTABLE-cccc"
	writeFile(t, filepath.Join(home, ".credentials.json"), claudeOAuthPayload(refreshed, now.Add(8*time.Hour)))
	// No identity cache beside it, so nothing names the account this copy belongs to.

	if code, out := captureStdout(t, func() int {
		return runUseIsolated(ctx, app, opts, constants.ToolClaude, "main")
	}); code != constants.ExitOK {
		t.Fatalf("use -i exit %d: %s", code, out)
	}

	if got := readFile(t, filepath.Join(home, ".credentials.json")); !strings.Contains(got, refreshed) {
		t.Fatalf("a copy kae could not attribute must be kept: %q", got)
	}
	be := testBackend(t, app)
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); strings.Contains(got, refreshed) {
		t.Fatalf("and must not be filed under this account: %s", got)
	}
}

// Two nets guard the kept-store exception, and a tool can be added that falls
// through both. The pass skips a tool whose rotation is not measured
// (`rotatesSingleUse`) before it judges anything, so such a store arrives at the
// sweep unmarked; `harvestBeforeDelete` then lets it through unconditionally for the
// same reason. Today no tool is in that position — claude is the only one with a
// credential variable and its rotation is measured — and this refuses the
// combination rather than waiting for the day it is created.
//
// The sibling guard for the other direction is
// TestHarvestIsDeclaredForMeasuredToolsOnly.
func TestNoSplittingToolSkipsBothNets(t *testing.T) {
	for _, tool := range constants.Tools {
		if credentialEnvVar(tool) != "" && !rotatesSingleUse(tool) {
			t.Errorf("%s can reach the kept-store exception, but the pin-level pass skips it "+
				"at the rotatesSingleUse gate and harvestBeforeDelete lets it through — so its "+
				"kept store would be swept with neither harvest nor attribution", tool)
		}
	}
}

// The shared-mode shape of "unmarked and unharvested reaches the exception": the
// account the previous binding named is gone, so the pass returns before judging and
// only harvestBeforeDelete stands between the sweep and a copy it cannot attribute.
// The isolated-mode sibling (TestRunPinSweepKeepsALostAccountsCredential) does not
// exercise the exception at all — there the store is outside `keep`, so it is swept
// on the ordinary path.
func TestAKeptStoreThePassSkippedIsStillKept(t *testing.T) {
	sim := &keychainSim{}
	runner.With(sim, func() {
		app := overlayTestApp(t)
		app.Env.GOOS = "darwin"
		ctx := context.Background()
		opts := commonOpts{Format: formatText}
		captureClaudeFromKeychain(t, app, sim, "main", mainToken, app.Now().Add(time.Hour))
		dir := pinHere(t, app, modeShared)
		storeDir := app.Paths.SharedDir(paths.PinID(dir), constants.ToolClaude)
		makePreSplit(t, app, constants.ToolClaude, "main", dir, storeDir)
		// The account goes away, so the pass returns at snapshotCredential — before it
		// judges the store, so it marks nothing.
		if err := os.RemoveAll(app.Paths.AccountDir(constants.ToolClaude, "main")); err != nil {
			t.Fatal(err)
		}
		captureClaudeFromKeychain(t, app, sim, "side", sideToken, app.Now().Add(time.Hour))
		app.Config.Profiles["main"] = config.Profile{Accounts: map[string]string{constants.ToolClaude: "side"}}
		sim.ops = nil

		_, stderr := captureStderr(t, func() int { return runPin(ctx, app, opts, "main", modeShared) })

		if strings.Contains(strings.Join(sim.ops, ","), "delete") {
			t.Fatalf("a store the pass never judged must not be deleted on the sweep's own reading: %v", sim.ops)
		}
		if !strings.Contains(stderr, "no account named claude/main exists any more") {
			t.Fatalf("the second net must say which arm kept it: %q", stderr)
		}
	})
}

// `--purge` must not announce having removed a credential from a store that never
// held one: the delete primitive treats absence as success, so the probe is what
// makes the report true. The positive control is
// TestUnpinPurgeRemovesAFileCredentialFromTheAccountStore.
func TestUnpinPurgeReportsNothingForAStoreWithNoCredential(t *testing.T) {
	app := overlayTestApp(t) // linux: the file driver
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	captureClaudeAt(t, app, "main", mainToken, app.Now().Add(time.Hour))
	_, _, credFile := boundStoreForClaudeMain(t, app)
	if err := os.Remove(credFile); err != nil {
		t.Fatal(err)
	}

	code, out := captureStdout(t, func() int { return runUnpin(ctx, app, opts, true) })
	mustExit(t, constants.ExitOK, code, out)

	if strings.Contains(out, "Removed the superseded per-directory") {
		t.Fatalf("nothing was there to remove; kae must not announce one:\n%s", out)
	}
}

// The migration's top guard is the **only** thing between a destructive call and a
// tool that has nowhere to migrate to, and that is a consequence of folding the body
// into removeDirCredential: `migrating: true` is what bypasses the file-store gate
// there, and harvestBeforeDelete returns true immediately for a tool whose rotation
// is not measured. Three conditions used to stand in the way; one does now, and
// `prepareGlobalIsolatedHome` calls this per tool with no other filter.
//
// So: codex's global isolated home must come through `kae use -i` untouched, and
// claude's pre-split copy must still be migrated — the pair, because a guard that
// refuses everything would satisfy either case alone.
func TestMigratePreSplitHomeOnlyTouchesAToolThatCanSplit(t *testing.T) {
	for _, tc := range []struct {
		tool, file, payload string
		wantKept            bool
	}{
		{constants.ToolCodex, "auth.json", `{"tokens":{"access_token":"codex-live-token"}}`, true},
		{constants.ToolClaude, ".credentials.json", "", false},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			app := overlayTestApp(t)
			ctx := context.Background()
			now := app.Now()
			payload := tc.payload
			if payload == "" {
				payload = claudeOAuthPayload("sk-ant-oat01-PRESPLIT-cccc", now.Add(8*time.Hour))
			}
			captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))

			home := app.Paths.GlobalIsolatedHomeDir(tc.tool, "main")
			mkdirs(t, home)
			writeFile(t, filepath.Join(home, tc.file), payload)
			writeFile(t, filepath.Join(home, ".claude.json"), claudeIdentityFile("main-uuid"))

			app.migratePreSplitHome(ctx, testBackend(t, app), tc.tool, "main", home)

			got := ""
			if data, err := os.ReadFile(filepath.Join(home, tc.file)); err == nil {
				got = string(data)
			}
			switch {
			case tc.wantKept && !strings.Contains(got, "codex-live-token"):
				t.Fatalf("%s has nowhere to migrate to; its home credential must be untouched: %q", tc.tool, got)
			case !tc.wantKept && strings.Contains(got, "PRESPLIT"):
				t.Fatalf("%s's pre-split copy must be migrated out of the home: %q", tc.tool, got)
			}
		})
	}
}

// dirSpecs overrides the credential variable **always**, with the config dir itself
// when the pair is not split — never leaving whatever the surrounding shell exports
// to answer. kae runs inside the bound shell that exported one, so resolving a
// pre-split store there would otherwise read the account-wide item and call it that
// store's, which is then what a harvest is free to overwrite.
func TestDirSpecsOverridesTheCredentialVariableForAnUnsplitStore(t *testing.T) {
	app := overlayTestApp(t)
	app.Env.GOOS = "darwin"
	ambient := app.credStoreDir(constants.ToolClaude, "main")
	app.Env.Getenv = func(key string) string {
		if key == credentialEnvVar(constants.ToolClaude) {
			return ambient
		}
		return ""
	}
	store := app.Paths.SharedDir("deadbeefdeadbeef", constants.ToolClaude)

	specs, err := app.dirSpecs(context.Background(), constants.ToolClaude, bindDirs{Config: store})
	if err != nil {
		t.Fatal(err)
	}
	sp, ok := specByName(specs, credentialArtifactName(constants.ToolClaude))
	if !ok {
		t.Fatal("no credential spec resolved")
	}
	if want := claude.KeychainService + "-" + sha8Of(store); sp.Target != want {
		t.Fatalf("an unsplit store must resolve its own item: got %q, want %q", sp.Target, want)
	}
	if sp.Target == claude.KeychainService+"-"+sha8Of(ambient) {
		t.Fatal("the ambient credential variable answered for a store it has nothing to do with")
	}
}

// One credential, one finding. Two directories bound to one account read the same
// store, so reporting it once per binding said "the credential bound to <dir>" N
// times about a single copy — which reads as N separate problems, and the remedy in
// any one of them fixes all of them. A directory still holding its own pre-split
// copy is a different credential and keeps its own finding.
func TestAStaleSharedCredentialIsReportedOncePerCredential(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	captureClaudeAt(t, app, "main", mainToken, app.Now().Add(time.Hour))
	first, _, credFile := boundStoreForClaudeMain(t, app)
	second, _, _ := boundStoreForClaudeMain(t, app)
	writeFile(t, credFile, deadClaudeCred)

	msgs := findChecks(buildDoctor(ctx, app, "", false), constants.CheckCredentialStale)
	bound := []string{}
	for _, m := range msgs {
		if strings.Contains(m, "bound to ") {
			bound = append(bound, m)
		}
	}
	if len(bound) != 1 {
		t.Fatalf("two bindings of one credential must report once, got %d: %v", len(bound), bound)
	}
	// **Either** of them: which one carries the remedy is the walk's order, which is
	// the pin index's, not the order they were bound in. Asserting one of the two
	// specifically is how this test failed for a reason that had nothing to do with
	// the rule under test.
	if !strings.Contains(bound[0], first) && !strings.Contains(bound[0], second) {
		t.Errorf("the remedy must name a directory that reads it: %q", bound[0])
	}

	// The negative control: give one of them its own copy and the count goes back to
	// two, so the dedup is keyed on the credential and not simply capping the report.
	behind, ahead := twoBoundCopiesOfClaudeMain(t, app)
	writeFile(t, behind.CredFile, deadClaudeCred)
	writeFile(t, ahead.CredFile, deadClaudeCred)
	msgs = findChecks(buildDoctor(ctx, app, "", false), constants.CheckCredentialStale)
	bound = bound[:0]
	for _, m := range msgs {
		if strings.Contains(m, "bound to ") {
			bound = append(bound, m)
		}
	}
	if len(bound) < 2 {
		t.Fatalf("two distinct copies must each be reported, got %d: %v", len(bound), bound)
	}
}

// The migration prompt. A directory bound before the split keeps its own copy, which
// stays healthy right up to the moment another binding of that account refreshes —
// so no freshness check can see it and only its *shape* gives it away.
func TestDoctorNamesADirectoryBoundBeforeTheCredentialSplit(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	captureClaudeAt(t, app, "main", mainToken, app.Now().Add(time.Hour))
	dir, storeDir, _ := boundStoreForClaudeMain(t, app)

	if msgs := findChecks(buildDoctor(ctx, app, "", false), constants.CheckCredentialUnsplit); len(msgs) != 0 {
		t.Fatalf("a directory bound by this kae has nothing to migrate: %v", msgs)
	}

	makePreSplit(t, app, constants.ToolClaude, "main", dir, storeDir)

	msgs := findChecks(buildDoctor(ctx, app, "", false), constants.CheckCredentialUnsplit)
	if len(msgs) != 1 {
		t.Fatalf("the unsplit directory must be named exactly once, got %d: %v", len(msgs), msgs)
	}
	if !strings.Contains(msgs[0], dir) {
		t.Errorf("the finding must name the directory to re-bind: %q", msgs[0])
	}
	if !strings.Contains(msgs[0], "cd "+dir+" && kae pin") {
		t.Errorf("the remedy is a re-bind in that directory: %q", msgs[0])
	}
}

// The credential variable is exported into the login flow, not just into the
// fragment. Without it `kae relogin` drives the tool's login against the store's own
// name, so the new token lands where kae does not read it and the command reports a
// login that changed nothing — with the directory still stale.
func TestReloginExportsTheCredentialVariable(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	captureClaudeAt(t, app, "main", mainToken, app.Now().Add(time.Hour))
	boundStoreForClaudeMain(t, app)

	var seen []string
	withInteractive(t, loginInto(t, constants.ToolClaude,
		"sk-ant-oat01-RELOGGED-aaaa", "main-uuid", app.Now().Add(8*time.Hour), &seen))
	if code, out := captureStdout(t, func() int {
		return runRelogin(ctx, app, commonOpts{Format: formatText}, "")
	}); code != constants.ExitOK {
		t.Fatalf("relogin exit %d: %s", code, out)
	}

	want := credentialEnvVar(constants.ToolClaude) + "=" + app.credStoreDir(constants.ToolClaude, "main")
	if !slices.Contains(seen, want) {
		t.Fatalf("the login flow must be handed %q, got %v", want, seen)
	}
}

// A credential written for one account must not be reachable under another's name.
// The store path is composed from the account, so this is really a guard on the
// composition: one account's directory can never be inside another's.
func TestCredStoreDirsOfTwoAccountsAreDistinct(t *testing.T) {
	app := overlayTestApp(t)
	main := app.credStoreDir(constants.ToolClaude, "main")
	side := app.credStoreDir(constants.ToolClaude, "side")
	if main == "" || side == "" || main == side {
		t.Fatalf("two accounts must get two stores: %q vs %q", main, side)
	}
	if pathWithin(main, side) || pathWithin(side, main) {
		t.Fatalf("neither store may contain the other: %q vs %q", main, side)
	}
	// A tool with no way to move its credential alone gets none, and callers read
	// that empty answer as "the credential is in the config dir".
	if got := app.credStoreDir(constants.ToolCodex, "main"); got != "" {
		t.Fatalf("codex has no credential variable, so it must have no store: %q", got)
	}
}

// The masking that keeps a global command out of a bound directory's credential.
// applyGlobalScope hid the config dir already; hiding one of the pair and not the
// other would have `kae use` write claude's credential into the directory's store
// while reading and reporting the real home.
func TestGlobalScopeHidesBothHalvesOfABinding(t *testing.T) {
	app := overlayTestApp(t)
	credDir := app.credStoreDir(constants.ToolClaude, "main")
	env := map[string]string{
		"CLAUDE_CONFIG_DIR":                    app.Paths.SharedDir("deadbeefdeadbeef", constants.ToolClaude),
		credentialEnvVar(constants.ToolClaude): credDir,
		"UNRELATED":                            "kept",
	}
	app.Env.Getenv = func(key string) string { return env[key] }
	app.applyGlobalScope()

	for _, key := range []string{"CLAUDE_CONFIG_DIR", credentialEnvVar(constants.ToolClaude)} {
		if got := app.Env.Getenv(key); got != "" {
			t.Errorf("a kae-managed %s must be hidden from global scope, got %q", key, got)
		}
	}
	if got := app.Env.Getenv("UNRELATED"); got != "kept" {
		t.Errorf("only kae's own values are hidden, got %q", got)
	}
	// A value the user set themselves stays honored, the same way the config dir's
	// does: kae masks what it wrote, not the variable.
	app2 := overlayTestApp(t)
	app2.Env.Getenv = func(key string) string {
		if key == credentialEnvVar(constants.ToolClaude) {
			return "/somewhere/of/their/own"
		}
		return ""
	}
	app2.applyGlobalScope()
	if got := app2.Env.Getenv(credentialEnvVar(constants.ToolClaude)); got != "/somewhere/of/their/own" {
		t.Errorf("a user-set credential dir must survive global scope, got %q", got)
	}
}

// The masking has to cover **both** env seams, because an adapter asks
// `Env.IsSet` through LookupEnv and not through Getenv — and claude refuses the one
// value kae never writes, an *empty* credential variable. Mask only Getenv and every
// bound directory reads as "set to empty": every global command run there reports
// claude unsupported, including the mise enter hook that fires on `cd`.
//
// This injects LookupEnv the way production does (internal/cmd/app.go). Without it
// Env.IsSet degrades to a non-empty test on Getenv, which is the masked value — so
// the defect is structurally unreachable from a fixture that leaves it nil, which is
// how it shipped past the first round of tests here.
func TestGlobalScopeDoesNotMakeABoundDirectoryLookUnsupported(t *testing.T) {
	app := overlayTestApp(t)
	env := map[string]string{
		"CLAUDE_CONFIG_DIR":                    app.Paths.SharedDir("deadbeefdeadbeef", constants.ToolClaude),
		credentialEnvVar(constants.ToolClaude): app.credStoreDir(constants.ToolClaude, "main"),
	}
	app.Env.Getenv = func(key string) string { return env[key] }
	app.Env.LookupEnv = func(key string) (string, bool) { value, ok := env[key]; return value, ok }
	app.applyGlobalScope()

	if app.Env.IsSet(credentialEnvVar(constants.ToolClaude)) {
		t.Error("a kae-set credential variable must read as absent, not as empty")
	}
	adp, err := adapter.ForTool(constants.ToolClaude)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adp.Artifacts(context.Background(), app.Env); err != nil {
		t.Fatalf("a global command inside a bound directory must still resolve claude: %v", err)
	}
	// The positive control for the refusal itself: a user-set empty value is still
	// refused, so the masking above is not simply disabling the check.
	app2 := overlayTestApp(t)
	app2.Env.Getenv = func(string) string { return "" }
	app2.Env.LookupEnv = func(key string) (string, bool) {
		return "", key == credentialEnvVar(constants.ToolClaude)
	}
	app2.applyGlobalScope()
	if _, err := adp.Artifacts(context.Background(), app2.Env); !errors.Is(err, adapter.ErrUnsupported) {
		t.Fatalf("an empty value the user set must still be refused, got %v", err)
	}
}

// A leftover store bound to one account must never be handed another account's
// credential store to read. The walk returns stores of older bindings forever, so
// taking the replaced fragment's entry verbatim would have the harvest compare one
// account's copy against another's identity — and file it under the wrong name on a
// match.
func TestALeftoverStoreIsNotGivenAnotherAccountsCredentialDir(t *testing.T) {
	app := overlayTestApp(t)
	prev := fragmentInfo{
		Mode:     modeShared,
		Accounts: map[string]string{constants.ToolClaude: "main"},
		CredDirs: map[string]string{constants.ToolClaude: app.credStoreDir(constants.ToolClaude, "main")},
	}
	bound := dirStore{Tool: constants.ToolClaude, Dir: "/store/shared"}
	if got := app.attributedCredDir(bound, prev); got != prev.CredDirs[constants.ToolClaude] {
		t.Fatalf("the store this binding names must keep its credential dir, got %q", got)
	}
	leftover := dirStore{Tool: constants.ToolClaude, Dir: "/store/isolated/side", Account: "side"}
	if got := app.attributedCredDir(leftover, prev); got != "" {
		t.Fatalf("a leftover store of another account must fall back to itself, got %q", got)
	}
}

// A pre-split store keeps reading its own credential through every sweep, which is
// what makes migration lossless: the harvest has to find the copy that is actually
// there, not the one the account would have if the directory had been re-bound.
func TestAPreSplitStoreKeepsItsOwnCredentialDir(t *testing.T) {
	app := overlayTestApp(t)
	prev := fragmentInfo{
		Mode:     modeShared,
		Accounts: map[string]string{constants.ToolClaude: "main"},
		CredDirs: map[string]string{}, // bound before the split
	}
	store := dirStore{Tool: constants.ToolClaude, Dir: "/store/shared"}
	if got := app.attributedCredDir(store, prev); got != "" {
		t.Fatalf("a pre-split store's credential is inside it, got %q", got)
	}
	if got := (dirStore{Dir: "/store/shared"}).dirs().credDirOrConfig(); got != "/store/shared" {
		t.Fatalf("an empty credential dir must resolve to the store, got %q", got)
	}
}

// The fragment is parsed back through the [env] line, so a value that does not
// unquote must be dropped rather than stored raw: half a path would send a sweep at
// a directory nobody exported.
func TestFragmentCredentialEntryIsParsedFromTheEnvLine(t *testing.T) {
	info := parseDirFragment(strings.Join([]string{
		"# kae:profile=main",
		"# kae:mode=shared",
		"# kae:account:claude=main",
		"[env]",
		`CLAUDE_CONFIG_DIR = "/store/shared"`,
		`CLAUDE_SECURESTORAGE_CONFIG_DIR = "/cred/claude/main"`,
	}, "\n"))
	if got := info.CredDirs[constants.ToolClaude]; got != "/cred/claude/main" {
		t.Fatalf("credential entry not parsed: %q", got)
	}
	broken := parseDirFragment(`CLAUDE_SECURESTORAGE_CONFIG_DIR = "/unterminated`)
	if got, ok := broken.CredDirs[constants.ToolClaude]; ok {
		t.Fatalf("an unquotable value must be dropped, got %q", got)
	}
}

// A re-bind moves the credential entry in **both** modes, because the account
// selects the store. Shared mode is the one that used to leave every env line alone,
// which would have left the fragment naming the previous account's credential while
// kae wrote the new account's — a directory running an account nothing claims.
func TestRebindMovesTheCredentialEntryInSharedMode(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	runner.With(&runnertest.Fake{Code: 0}, func() {
		captureClaude(t, app, "main", mainToken)
		captureClaude(t, app, "side", sideToken)
	})
	dir := pinHere(t, app, modeShared)

	if code, out := captureStdout(t, func() int {
		return runRebind(ctx, app, opts, constants.ToolClaude, "side")
	}); code != constants.ExitOK {
		t.Fatalf("re-bind exit %d: %s", code, out)
	}

	fragment := mustFragmentAt(t, dir)
	if got, want := fragment.CredDirs[constants.ToolClaude],
		app.credStoreDir(constants.ToolClaude, "side"); got != want {
		t.Fatalf("the credential entry must follow the account: got %q, want %q", got, want)
	}
	if strings.Count(readFile(t, filepath.Join(dir, fragmentRelPath)),
		credentialEnvVar(constants.ToolClaude)+" = ") != 1 {
		t.Fatal("the re-bind must rewrite the entry, not add a second one")
	}
}

// The isolated arm of the same rule, which is a separate case and not a variant:
// there the home line **is** rewritten, so an insert placed in that arm fires even
// when the fragment already has a credential line — leaving two of them. mise rejects
// a duplicate key in one table, so the whole fragment stops loading and the directory
// falls back to the real home; and kae's own parser takes the last line, which is the
// *previous* account's store. Asserted by count, because both wrong answers are
// "the right line is present".
func TestRebindMovesTheCredentialEntryInIsolatedMode(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	runner.With(&runnertest.Fake{Code: 0}, func() {
		captureClaude(t, app, "main", mainToken)
		captureClaude(t, app, "side", sideToken)
	})
	dir := pinHere(t, app, modeIsolated)

	if code, out := captureStdout(t, func() int {
		return runRebind(ctx, app, opts, constants.ToolClaude, "side")
	}); code != constants.ExitOK {
		t.Fatalf("re-bind exit %d: %s", code, out)
	}

	content := readFile(t, filepath.Join(dir, fragmentRelPath))
	if n := strings.Count(content, credentialEnvVar(constants.ToolClaude)+" = "); n != 1 {
		t.Fatalf("the re-bind must rewrite the entry, not add a second one (%d present):\n%s", n, content)
	}
	if got, want := mustFragmentAt(t, dir).CredDirs[constants.ToolClaude],
		app.credStoreDir(constants.ToolClaude, "side"); got != want {
		t.Fatalf("the credential entry must follow the account: got %q, want %q", got, want)
	}
}

// …and adds one to a fragment that has none, which is the re-bind half of the
// migration. Without it `kae pin <tool> <account>` writes the new account's
// credential to the account's store and leaves the directory reading the old
// per-directory item: a logout reported as a successful re-bind.
func TestRebindAddsTheCredentialEntryToAPreSplitFragment(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	runner.With(&runnertest.Fake{Code: 0}, func() {
		captureClaude(t, app, "main", mainToken)
		captureClaude(t, app, "side", sideToken)
	})
	dir := pinHere(t, app, modeShared)
	storeDir := app.Paths.SharedDir(paths.PinID(dir), constants.ToolClaude)
	makePreSplit(t, app, constants.ToolClaude, "main", dir, storeDir)

	if code, out := captureStdout(t, func() int {
		return runRebind(ctx, app, opts, constants.ToolClaude, "side")
	}); code != constants.ExitOK {
		t.Fatalf("re-bind exit %d: %s", code, out)
	}

	fragment := mustFragmentAt(t, dir)
	if got, want := fragment.CredDirs[constants.ToolClaude],
		app.credStoreDir(constants.ToolClaude, "side"); got != want {
		t.Fatalf("the re-bind must add the credential entry: got %q, want %q", got, want)
	}
	// Inside the [env] table, or mise never exports it and the whole entry is
	// decorative.
	content := readFile(t, filepath.Join(dir, fragmentRelPath))
	envAt := strings.Index(content, "[env]")
	if envAt < 0 || strings.Index(content, credentialEnvVar(constants.ToolClaude)+" = ") < envAt {
		t.Fatalf("the added entry must be inside [env]:\n%s", content)
	}
}
