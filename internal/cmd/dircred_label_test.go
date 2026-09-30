package cmd

// Pin and rebind tests over the directory identity label and the readers that
// decide whether a copy may be kept, retracted or spent.

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
)

// The re-bind between two accounts, which is the shape that made the old attribution
// destroy a live login. A shared bind's config dir belongs to the **pin-id**, so it still
// carries the *previous* binding's label; reading that as evidence about the *new*
// account's store answered `Conflicting` — the one refusal that still overwrites — about a
// store that label says nothing about. Measured 2026-08-08: the credential was then gone
// from every location, and the tool went on working for up to 8h before anyone found out.
//
// What answers it correctly is the sibling directory already reading that store. Its cache
// says the copy is main's, so this bind harvests it and writes it back instead of
// replacing it with an older snapshot. The fragments are read *before* this directory's is
// rewritten, which is what keeps the stale label out without threading the previous
// binding down to the attribution.
func TestRunPinRebindBetweenAccountsPreservesTheTargetsLiveCredential(t *testing.T) {
	app, now := twoClaudeAccounts(t)
	ctx := context.Background()

	// The sibling, already bound to claude/main and reading its store.
	_, siblingStore := bindClaudeHere(t, app, "main")
	// The directory about to be re-bound, currently on claude/side. chdir lands here, so
	// this is the directory the `kae pin main` below runs in.
	bindClaudeHere(t, app, "side")

	// The tool refreshed claude/main's copy in the store the sibling reads.
	const refreshed = "sk-ant-oat01-MAIN-REFRESHED-cccc"
	mainCopy := dirCredFile(app, constants.ToolClaude, "main", siblingStore)
	writeFile(t, mainCopy, claudeOAuthPayload(refreshed, now.Add(8*time.Hour)))

	if code, out := captureStdout(t, func() int {
		return runPin(ctx, app, commonOpts{Format: formatText}, "main", modeShared, false)
	}); code != constants.ExitOK {
		t.Fatalf("re-bind to main exit %d: %s", code, out)
	}

	if got := readFile(t, mainCopy); !strings.Contains(got, refreshed) {
		t.Fatalf("re-binding one directory destroyed the account's live credential: %s", got)
	}
	be := testBackend(t, app)
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); !strings.Contains(got, refreshed) {
		t.Fatalf("the sibling reader's evidence must let that copy be harvested: %s", got)
	}
}

// Two directories read one account's store and their identity caches **disagree**, which
// means somebody logged in as another account inside one of them. The copy is live and it
// is somebody's, and this bind is not the event that should decide whose: overwriting on a
// majority destroys a login with no backup, so kae keeps it and `kae doctor` reports the
// disagreeing directory as identity_drift.
//
// Measured 2026-08-08 as the second half of the shared-store defect: attribution used to
// ask only the directory being bound, so a cache that legitimately named this account
// confirmed a copy some *other* directory had poisoned, and an ordinary re-pin filed a
// foreign token into this account's snapshot.
func TestWriteDirCredentialKeepsACopyItsReadersDisagreeAbout(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))

	_, poisoned := bindClaudeHere(t, app, "main")
	// A login as side inside that directory: its cache names side while it goes on
	// reading claude/main's credential store.
	writeFile(t, filepath.Join(poisoned, ".claude.json"), claudeIdentityFile("side-uuid"))
	_, storeDir := bindClaudeHere(t, app, "main")

	const refreshed = "sk-ant-oat01-MAIN-REFRESHED-cccc"
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", storeDir),
		claudeOAuthPayload(refreshed, now.Add(8*time.Hour)))

	be := testBackend(t, app)
	_, stderr := captureStderr(t, func() int {
		if err := app.writeDirCredential(ctx, be, constants.ToolClaude, "main", storeDir, false); err != nil {
			t.Fatalf("writeDirCredential: %v", err)
		}
		return 0
	})

	if got := readFile(t, dirCredFile(app, constants.ToolClaude, "main", storeDir)); !strings.Contains(got, refreshed) {
		t.Fatalf("a copy kae cannot name the owner of must not be overwritten: %s", got)
	}
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); strings.Contains(got, refreshed) {
		t.Fatalf("a copy the readers disagree about must not be filed under this account: %s", got)
	}
	if !strings.Contains(stderr, "disagree about whose login it is") {
		t.Fatalf("the refusal must name the disagreement rather than a conflict: %q", stderr)
	}
	// A disagreement is missing evidence, not positive evidence, so it takes the keep
	// branch. Reported as `Conflicting` it would overwrite, which is the destruction.
	if !strings.Contains(stderr, "kept it rather than replacing it") {
		t.Fatalf("a disagreement must keep the copy: %q", stderr)
	}
}

// "kae could not look" and "nothing is there" are different findings, and only the second
// licenses a write. A per-directory store root without a breadcrumb is a store kae cannot
// name the directory of — bound by a kae older than the record — so the readers cannot be
// enumerated, and an unenumerable set must not read as an empty one.
//
// The bound directory here would otherwise confirm, which is what makes the incompleteness
// observable: without the gate the harvest runs and the snapshot changes.
func TestWriteDirCredentialKeepsWhatItCannotEnumerateTheReadersOf(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	_, storeDir := bindClaudeHere(t, app, "main")
	const refreshed = "sk-ant-oat01-MAIN-REFRESHED-cccc"
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", storeDir),
		claudeOAuthPayload(refreshed, now.Add(8*time.Hour)))
	mkdirs(t, filepath.Join(app.Paths.IsolationDir(), "0123456789abcdef"))

	be := testBackend(t, app)
	_, stderr := captureStderr(t, func() int {
		if err := app.writeDirCredential(ctx, be, constants.ToolClaude, "main", storeDir, false); err != nil {
			t.Fatalf("writeDirCredential: %v", err)
		}
		return 0
	})

	if got := readFile(t, dirCredFile(app, constants.ToolClaude, "main", storeDir)); !strings.Contains(got, refreshed) {
		t.Fatalf("a copy whose readers kae could not enumerate must be kept: %s", got)
	}
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); strings.Contains(got, refreshed) {
		t.Fatalf("an unenumerable reader set is not a confirmation: %s", got)
	}
	if !strings.Contains(stderr, "kae could not tell which directories read this credential") {
		t.Fatalf("the refusal must name the enumeration, not an absent cache: %q", stderr)
	}
	// A keep retracts only a label that **disagrees** with the account being bound. This
	// one agrees, so it is honest evidence and stays — retracting every label on every keep
	// survived the suite until this line existed.
	if got := readFile(t, filepath.Join(storeDir, ".claude.json")); !strings.Contains(got, "main-uuid") {
		t.Fatalf("a label that agrees is evidence and must survive a keep: %q", got)
	}
}

// A globally isolated home reads the account's credential too, and it has no fragment and
// no pin — so the fragment walk cannot see it and `state.synced` is the only thing that
// can. Without that half a machine whose only claude reader is `kae use -i` would have no
// reader at all, and every copy its tool refreshed would be kept and never harvested.
func TestUseIsolatedHarvestsWithTheGlobalHomeAsEvidence(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))

	if code, out := captureStdout(t, func() int {
		return runUseIsolated(ctx, app, opts, constants.ToolClaude, "main")
	}); code != constants.ExitOK {
		t.Fatalf("use -i exit %d: %s", code, out)
	}
	home := app.Paths.GlobalIsolatedHomeDir(constants.ToolClaude, "main")
	// Positive control: the home carries the identity that attributes the store, or the
	// harvest below would be refused for missing evidence and this test would prove
	// nothing about the state.synced half.
	if got := readFile(t, filepath.Join(home, ".claude.json")); !strings.Contains(got, "main-uuid") {
		t.Fatalf("the isolated home must carry the identity that attributes the store: %q", got)
	}

	// The tool refreshed the account's copy since.
	const refreshed = "sk-ant-oat01-MAIN-REFRESHED-cccc"
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", home),
		claudeOAuthPayload(refreshed, now.Add(8*time.Hour)))

	if code, out := captureStdout(t, func() int {
		return runUseIsolated(ctx, app, opts, constants.ToolClaude, "main")
	}); code != constants.ExitOK {
		t.Fatalf("second use -i exit %d: %s", code, out)
	}
	be := testBackend(t, app)
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); !strings.Contains(got, refreshed) {
		t.Fatalf("the only reader's evidence must let the copy be harvested: %s", got)
	}
}

// **The keep has to be idempotent, and it was not.** A bind that moves a directory to
// another account leaves the previous binding's label in its config dir, because a keep
// writes none — and on the next run the fragment names the new account, so that directory
// is one of the store's readers and its stale label is its only reading. It then reads as a
// conflicting reader, `Conflicting` overwrites, and the second of two identical `kae pin`
// calls destroys the live login the first one preserved, with a success line both times.
// Measured by review 2026-08-08. The keep now retracts a label that disagrees.
func TestRunPinTwiceKeepsTheSameCopyBothTimes(t *testing.T) {
	app, now := twoClaudeAccounts(t)
	ctx := context.Background()
	_, sideStore := bindClaudeHere(t, app, "side")
	if got := readFile(t, filepath.Join(sideStore, ".claude.json")); !strings.Contains(got, "side-uuid") {
		t.Fatalf("the fixture needs the previous binding's label in place: %q", got)
	}

	// A newer copy in claude/main's store that nothing reads — a `kae use -i`, another
	// machine, or a binding since unpinned.
	const live = "sk-ant-oat01-MAIN-LIVE-eeee"
	mainCopy := dirCredFile(app, constants.ToolClaude, "main", sideStore)
	mkdirs(t, filepath.Dir(mainCopy))
	writeFile(t, mainCopy, claudeOAuthPayload(live, now.Add(8*time.Hour)))

	opts := commonOpts{Format: formatText}
	for i, want := range []string{"first bind", "second bind"} {
		if code, out := captureStdout(t, func() int { return runPin(ctx, app, opts, "main", modeShared, false) }); code != constants.ExitOK {
			t.Fatalf("%s: pin exit %d: %s", want, code, out)
		}
		if got := readFile(t, mainCopy); !strings.Contains(got, live) {
			t.Fatalf("%s (run %d) destroyed the copy the keep preserved: %s", want, i+1, got)
		}
	}
	be := testBackend(t, app)
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); strings.Contains(got, live) {
		t.Fatalf("a copy no reader can attribute must not be filed under this account either: %s", got)
	}
}

// The arm the first version of the retract could not reach: an **unenumerable** walk keeps
// the copy, correctly, and used to leave the stale label with it — so once the leftover
// store root went away the acting directory was a lone conflicting reader and the next
// identical bind destroyed what the first had kept. Found by review 2026-08-08, and the
// reason the "is this directory a reader" fact comes from the directory's own binding
// rather than from the walk: the walk can only fail about *other* directories.
func TestRunPinKeepsAndRetractsEvenWhenTheWalkIsIncomplete(t *testing.T) {
	app, now := twoClaudeAccounts(t)
	ctx := context.Background()
	_, sideStore := bindClaudeHere(t, app, "side")

	const live = "sk-ant-oat01-MAIN-LIVE-eeee"
	mainCopy := dirCredFile(app, constants.ToolClaude, "main", sideStore)
	mkdirs(t, filepath.Dir(mainCopy))
	writeFile(t, mainCopy, claudeOAuthPayload(live, now.Add(8*time.Hour)))
	// A per-directory store root with no breadcrumb: the reader walk is incomplete.
	leftover := filepath.Join(app.Paths.IsolationDir(), "0123456789abcdef")
	mkdirs(t, leftover)

	opts := commonOpts{Format: formatText}
	if code, out := captureStdout(t, func() int { return runPin(ctx, app, opts, "main", modeShared, false) }); code != constants.ExitOK {
		t.Fatalf("pin 1 exit %d: %s", code, out)
	}
	if got := readFile(t, mainCopy); !strings.Contains(got, live) {
		t.Fatalf("an unenumerable walk must keep the copy: %s", got)
	}

	// The leftover goes away, so the walk is complete again and this directory is now a
	// reader. Its label must not be the previous binding's by then.
	if err := os.RemoveAll(leftover); err != nil {
		t.Fatal(err)
	}
	if code, out := captureStdout(t, func() int { return runPin(ctx, app, opts, "main", modeShared, false) }); code != constants.ExitOK {
		t.Fatalf("pin 2 exit %d: %s", code, out)
	}
	if got := readFile(t, mainCopy); !strings.Contains(got, live) {
		t.Fatalf("the second bind destroyed the copy the first one kept: %s", got)
	}
}

// An isolated re-bind **to the account the directory is already bound to** is what separates
// `modeStoreDir` from a hardcoded shared dir: there this pin's own isolated config dir is a
// reader and the shared dir is not, so acting under the wrong one flips the pass to
// "leaving it where it is" while the write replaces — a message that is the inverse of what
// happened, which is the class two earlier fixes exist for. `runRebind` has no no-op
// short-circuit, so this runs the whole path (execution-type review, 2026-08-08).
func TestRunRebindIsolatedToTheSameAccountActsUnderItsOwnDir(t *testing.T) {
	app := overlayTestApp(t)
	chdirTemp(t)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	if code := runPin(ctx, app, opts, "main", modeIsolated, false); code != constants.ExitOK {
		t.Fatalf("pin --isolated exit %d", code)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	config := app.Paths.IsolatedConfigDir(paths.PinID(cwd), constants.ToolClaude, "main")
	// A login as side inside the store this directory reads.
	writeFile(t, filepath.Join(config, ".claude.json"), claudeIdentityFile("side-uuid"))
	const sideLive = "sk-ant-oat01-SIDE-LIVE-eeee"
	credFile := dirCredFile(app, constants.ToolClaude, "main", config)
	writeFile(t, credFile, claudeOAuthPayload(sideLive, now.Add(8*time.Hour)))

	_, stderr := captureStderr(t, func() int { return runRebind(ctx, app, opts, constants.ToolClaude, "main", false) })

	// This directory *is* the conflicting reader and the write targets this very store, so
	// the copy is replaced — and the message has to say that rather than promise a keep.
	if !strings.Contains(stderr, "belongs to an account other than claude/main") {
		t.Fatalf("the acting directory is the conflicting reader here: %q", stderr)
	}
	if !strings.Contains(stderr, "and this bind replaces it") {
		t.Fatalf("the message must not promise a keep the write does not make: %q", stderr)
	}
	if got := readFile(t, credFile); strings.Contains(got, sideLive) {
		t.Fatalf("and it really is replaced: %s", got)
	}
}

// `kae pin <tool> <account>` is the other command that moves a directory between accounts,
// so its keep owes the same retract — and it reaches the materializers by a different route
// (runRebind, not isolationPlan). Handing them an empty binding there survived the suite
// (execution-type review, 2026-08-08), which would put the "keep, then destroy on the next
// run" defect back on this path alone.
func TestRunRebindKeepAlsoRetractsTheStaleLabel(t *testing.T) {
	app, now := twoClaudeAccounts(t)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	_, sideStore := bindClaudeHere(t, app, "side")
	label := filepath.Join(sideStore, ".claude.json")
	if got := readFile(t, label); !strings.Contains(got, "side-uuid") {
		t.Fatalf("the fixture needs the previous binding's label: %q", got)
	}
	// A newer copy in claude/main's store that nothing reads yet.
	const live = "sk-ant-oat01-MAIN-LIVE-eeee"
	mainCopy := dirCredFile(app, constants.ToolClaude, "main", sideStore)
	mkdirs(t, filepath.Dir(mainCopy))
	writeFile(t, mainCopy, claudeOAuthPayload(live, now.Add(8*time.Hour)))

	for run := 1; run <= 2; run++ {
		if code, out := captureStdout(t, func() int {
			return runRebind(ctx, app, opts, constants.ToolClaude, "main", false)
		}); code != constants.ExitOK {
			t.Fatalf("run %d: re-bind exit %d: %s", run, code, out)
		}
		if got := readFile(t, mainCopy); !strings.Contains(got, live) {
			t.Fatalf("run %d destroyed the copy the keep preserved: %s", run, got)
		}
	}
}

// The other direction of the same expression, and the destructive one: a re-bind to the
// account the directory is **already** bound to (`runRebind` has no short-circuit for it).
// The label there was written under that same account, so it is a live login and not kae's
// leftover. Forcing this call site to answer *stale* survived the whole suite while the same
// mutation of its twin in `isolationPlan` did not — two call sites computing one fact, covered asymmetrically
// (execution-type review, 2026-08-08).
func TestRunRebindToTheSameAccountKeepsTheLiveLabel(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	bindClaudeHere(t, app, "main") // a sibling that confirms, so the disagree arm keeps
	_, poisoned := bindClaudeHere(t, app, "main")
	// A login as side inside this directory, and its newer token in the account's store.
	label := filepath.Join(poisoned, ".claude.json")
	writeFile(t, label, claudeIdentityFile("side-uuid"))
	const sideLive = "sk-ant-oat01-SIDE-LIVE-eeee"
	credFile := dirCredFile(app, constants.ToolClaude, "main", poisoned)
	writeFile(t, credFile, claudeOAuthPayload(sideLive, now.Add(8*time.Hour)))

	if code, out := captureStdout(t, func() int {
		return runRebind(ctx, app, opts, constants.ToolClaude, "main", false)
	}); code != constants.ExitOK {
		t.Fatalf("same-account re-bind exit %d: %s", code, out)
	}
	if got := readFile(t, label); !strings.Contains(got, "side-uuid") {
		t.Fatalf("a label written under the account being re-bound is a live login, not a leftover: %q", got)
	}
	if got := readFile(t, credFile); !strings.Contains(got, sideLive) {
		t.Fatalf("and the copy the readers disagree about is kept: %s", got)
	}
}

// The two constant families that name a bind mechanism are compared across package
// boundaries without anything tying them: `isolationPlan` switches on `constants.Mode*`
// while `runRebind` switches the mode read from the fragment on `paths.*Segment`, so a
// drift would put the two entry points on different branches of the same question —
// the "two tables keyed differently" hazard AGENTS.md routes to for the completion guards.
// Measured interchangeable 2026-08-08; nothing enforced it.
func TestBindModeConstantsAgreeAcrossPackages(t *testing.T) {
	for _, pair := range []struct{ name, mode, segment string }{
		{"shared", constants.ModeShared, paths.SharedSegment},
		{"isolated", constants.ModeIsolated, paths.IsolatedSegment},
	} {
		if pair.mode != pair.segment {
			t.Errorf("%s: constants %q and paths %q must name one mechanism", pair.name, pair.mode, pair.segment)
		}
	}
}

// **An isolated config dir is keyed by the account, so a label in it is never stale.** Every
// label there was written while bound to that same account, so a disagreement can only be a
// login as somebody else — live evidence. Re-binding *back* to an account this directory had
// left is what proves it: that dir still exists (a re-bind keeps the stores it moves off)
// with its label intact, and retracting it deletes the only record of whose the credential
// is, after which an ordinary sibling confirms unopposed and files a foreign token.
// Measured by review 2026-08-08, and it reaches `kae pin -i` as well as `kae pin <tool>`,
// so both are exercised here.
func TestAnIsolatedRebindBackKeepsTheLiveLabelInThatAccountsDir(t *testing.T) {
	for _, tc := range []struct {
		name string
		back func(t *testing.T, app *App, ctx context.Context, opts commonOpts) int
	}{
		{"kae pin <tool> <account>", func(t *testing.T, app *App, ctx context.Context, opts commonOpts) int {
			return runRebind(ctx, app, opts, constants.ToolClaude, "main", false)
		}},
		{"kae pin -i <profile>", func(t *testing.T, app *App, ctx context.Context, opts commonOpts) int {
			return runPin(ctx, app, opts, "main", modeIsolated, false)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := overlayTestApp(t)
			app.Config.Profiles["side"] = config.Profile{
				Accounts: map[string]string{constants.ToolClaude: "side"},
			}
			chdirTemp(t)
			ctx := context.Background()
			opts := commonOpts{Format: formatText}
			now := app.Now()
			captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
			captureClaudeAt(t, app, "side", sideToken, now.Add(time.Hour))
			if code := runPin(ctx, app, opts, "main", modeIsolated, false); code != constants.ExitOK {
				t.Fatalf("pin -i main exit %d", code)
			}
			cwd, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			mainDir := app.Paths.IsolatedConfigDir(paths.PinID(cwd), constants.ToolClaude, "main")
			// A login as side inside claude/main's isolated store.
			label := filepath.Join(mainDir, ".claude.json")
			writeFile(t, label, claudeIdentityFile("side-uuid"))
			const sideLive = "sk-ant-oat01-SIDE-LIVE-eeee"
			writeFile(t, dirCredFile(app, constants.ToolClaude, "main", mainDir),
				claudeOAuthPayload(sideLive, now.Add(8*time.Hour)))

			// Away, then back.
			if code := runRebind(ctx, app, opts, constants.ToolClaude, "side", false); code != constants.ExitOK {
				t.Fatalf("re-bind to side exit %d", code)
			}
			if code, out := captureStdout(t, func() int { return tc.back(t, app, ctx, opts) }); code != constants.ExitOK {
				t.Fatalf("re-bind back exit %d: %s", code, out)
			}

			if got := readFile(t, label); !strings.Contains(got, "side-uuid") {
				t.Fatalf("an isolated dir's label is never a previous binding's, so it is evidence: %q", got)
			}
			be := testBackend(t, app)
			if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); strings.Contains(got, sideLive) {
				t.Fatalf("with that label gone the next reader confirms unopposed: %s", got)
			}
		})
	}
}

// kae may not claim to have established what a directory reads when the read of its own
// binding **failed**. `readFragmentAt` answers an unreadable fragment with an error and an
// empty record, so dropping the error turns "kae could not look" into "this directory reads
// nothing" — and the keep then retracts a live label on the strength of an emptiness that is
// only the read failing. Found by review 2026-08-08, in the threading that fixed the
// previous defect.
func TestAnUnreadableOwnFragmentDoesNotRetractALiveLabel(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod 000 does not stop root from reading the fragment")
	}
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	bindClaudeHere(t, app, "main") // the sibling that confirms
	dir, poisoned := bindClaudeHere(t, app, "main")
	label := filepath.Join(poisoned, ".claude.json")
	writeFile(t, label, claudeIdentityFile("side-uuid"))
	const sideLive = "sk-ant-oat01-SIDE-LIVE-eeee"
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", poisoned),
		claudeOAuthPayload(sideLive, now.Add(8*time.Hour)))

	// The shell that activated this directory still exports the binding, so it goes on
	// reading that store — kae just cannot read the record of it.
	fragment := filepath.Join(dir, fragmentRelPath)
	if err := os.Chmod(fragment, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(fragment, 0o600) })
	if _, _, err := readFragmentAt(dir); err == nil {
		t.Skip("this filesystem does not make an unreadable fragment fail the read")
	}

	opts := commonOpts{Format: formatText}
	if code, out := captureStdout(t, func() int { return runPin(ctx, app, opts, "main", modeShared, false) }); code != constants.ExitOK {
		t.Fatalf("pin exit %d: %s", code, out)
	}
	if got := readFile(t, label); !strings.Contains(got, "side-uuid") {
		t.Fatalf("a failed read of this directory's own binding is not evidence that its label is stale: %q", got)
	}
}

// A globally isolated home passes no binding at all, and that zero value is the only thing
// between its live label and the retract. The claim it rests on — "the home is always one of
// its own account's readers, so the harvest confirms rather than keeps" — is one condition
// short: it confirms only while it is the only reader that can speak. Give it a bound
// sibling that confirms and the home becomes the **disagreeing** reader, which keeps.
// Measured by review 2026-08-08, after this was written down as unobservable.
func TestAGlobalIsolatedHomeKeepsItsOwnDisagreeingLabel(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	bindClaudeHere(t, app, "main") // a bound reader whose cache honestly says main

	if code, out := captureStdout(t, func() int {
		return runUseIsolated(ctx, app, opts, constants.ToolClaude, "main")
	}); code != constants.ExitOK {
		t.Fatalf("use -i exit %d: %s", code, out)
	}
	home := app.Paths.GlobalIsolatedHomeDir(constants.ToolClaude, "main")
	// A login as side inside the isolated home.
	homeLabel := filepath.Join(home, ".claude.json")
	writeFile(t, homeLabel, claudeIdentityFile("side-uuid"))
	const sideLive = "sk-ant-oat01-SIDE-LIVE-eeee"
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", home),
		claudeOAuthPayload(sideLive, now.Add(8*time.Hour)))

	if code, out := captureStdout(t, func() int {
		return runUseIsolated(ctx, app, opts, constants.ToolClaude, "main")
	}); code != constants.ExitOK {
		t.Fatalf("second use -i exit %d: %s", code, out)
	}
	if got := readFile(t, homeLabel); !strings.Contains(got, "side-uuid") {
		t.Fatalf("the home has no binding to prove its label stale, so it must survive: %q", got)
	}
	be := testBackend(t, app)
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); strings.Contains(got, sideLive) {
		t.Fatalf("retracting it lets the next run confirm and file a foreign token: %s", got)
	}
}

// The other direction of the same derivation, and the one that makes the walk unusable for
// it: `credStoreReaders` answers an incomplete enumeration with **no** readers, so
// membership in that list reads every directory as a stranger — including one that is
// reading the store and whose disagreeing label is a live login. Deriving the fact from the
// walk would therefore delete that evidence whenever a leftover store root exists somewhere
// else entirely, which is the round-3 defect conditioned on an unrelated directory.
func TestAnIncompleteWalkDoesNotMakeALiveLabelStale(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	bindClaudeHere(t, app, "main") // the sibling that confirms
	_, poisoned := bindClaudeHere(t, app, "main")
	label := filepath.Join(poisoned, ".claude.json")
	writeFile(t, label, claudeIdentityFile("side-uuid"))
	const sideLive = "sk-ant-oat01-SIDE-LIVE-eeee"
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", poisoned),
		claudeOAuthPayload(sideLive, now.Add(8*time.Hour)))
	// Somewhere else entirely: a store root with no breadcrumb.
	mkdirs(t, filepath.Join(app.Paths.IsolationDir(), "0123456789abcdef"))

	opts := commonOpts{Format: formatText}
	if code, out := captureStdout(t, func() int { return runPin(ctx, app, opts, "main", modeShared, false) }); code != constants.ExitOK {
		t.Fatalf("pin exit %d: %s", code, out)
	}
	if got := readFile(t, label); !strings.Contains(got, "side-uuid") {
		t.Fatalf("an unrelated leftover must not turn this directory's live label into a stale one: %q", got)
	}
}

// The retract's second condition, which the membership test alone does not cover: a
// directory that is **not** a reader of this store can still hold a label that agrees with
// the account being bound — the tool ran there and logged in as it. That label is honest
// evidence the moment this bind makes the directory a reader, so it is not retracted.
// Dropping the conjunct survived the suite (execution-type review, 2026-08-08).
func TestRunPinKeepsALabelThatAgreesEvenFromAStranger(t *testing.T) {
	app, now := twoClaudeAccounts(t)
	ctx := context.Background()
	_, sideStore := bindClaudeHere(t, app, "side")
	// Bound to side, but the tool ran here and logged in as main.
	label := filepath.Join(sideStore, ".claude.json")
	writeFile(t, label, claudeIdentityFile("main-uuid"))

	const live = "sk-ant-oat01-MAIN-LIVE-eeee"
	mainCopy := dirCredFile(app, constants.ToolClaude, "main", sideStore)
	mkdirs(t, filepath.Dir(mainCopy))
	writeFile(t, mainCopy, claudeOAuthPayload(live, now.Add(8*time.Hour)))

	opts := commonOpts{Format: formatText}
	if code, out := captureStdout(t, func() int { return runPin(ctx, app, opts, "main", modeShared, false) }); code != constants.ExitOK {
		t.Fatalf("pin exit %d: %s", code, out)
	}
	if got := readFile(t, label); !strings.Contains(got, "main-uuid") {
		t.Fatalf("a label that agrees is evidence and must survive a keep: %q", got)
	}
}

// The other half of the retract, and the one that inverts it: a label that disagrees has
// two causes wanting **opposite** actions. Left by a previous binding it is stale and must
// go; written by a login in this very directory it is the evidence the whole model rests
// on. Keyed on the label alone the retract deleted the second kind — after which the next
// identical run saw one silent reader and one confirming sibling, confirmed, and harvested
// the foreign token into this account's snapshot. That is the mis-filing the reader model
// exists to stop, reopened from the other side; measured by review 2026-08-08 and a
// regression of the retract that fixed the first half.
func TestRunPinTwiceKeepsALiveLabelThatDisagrees(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	bindClaudeHere(t, app, "main") // the sibling, whose label honestly says main
	_, poisoned := bindClaudeHere(t, app, "main")
	// A login as side in *this* directory, and the account's store now holds side's token —
	// the only copy of it anywhere, since claude/side has never been captured.
	label := filepath.Join(poisoned, ".claude.json")
	writeFile(t, label, claudeIdentityFile("side-uuid"))
	const sideLive = "sk-ant-oat01-SIDE-LIVE-eeee"
	credFile := dirCredFile(app, constants.ToolClaude, "main", poisoned)
	writeFile(t, credFile, claudeOAuthPayload(sideLive, now.Add(8*time.Hour)))

	be := testBackend(t, app)
	opts := commonOpts{Format: formatText}
	for run := 1; run <= 2; run++ {
		if code, out := captureStdout(t, func() int { return runPin(ctx, app, opts, "main", modeShared, false) }); code != constants.ExitOK {
			t.Fatalf("run %d: pin exit %d: %s", run, code, out)
		}
		if got := readFile(t, label); !strings.Contains(got, "side-uuid") {
			t.Fatalf("run %d retracted a live label, which is the evidence of the disagreement: %q", run, got)
		}
		if got := readFile(t, credFile); !strings.Contains(got, sideLive) {
			t.Fatalf("run %d destroyed the copy the readers disagree about: %s", run, got)
		}
		if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); strings.Contains(got, sideLive) {
			t.Fatalf("run %d filed another account's token under this one: %s", run, got)
		}
	}
}

// `runRebind` has to hand the pass the config dir of the mode the fragment names, or the
// pass acts under the *previous* account's isolated dir — which is a reader — and answers
// `Conflicting` where the write answers "this directory does not read it yet". Deriving it
// from `info.Accounts[tool]` survived the suite (execution-type review, 2026-08-08); the
// shared branch was already covered, this is the isolated one.
func TestRunRebindIsolatedActsUnderTheNewAccountsConfigDir(t *testing.T) {
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
	// A login as side inside the isolated store this directory currently reads.
	mainConfig := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "main")
	writeFile(t, filepath.Join(mainConfig, ".claude.json"), claudeIdentityFile("side-uuid"))
	const sideLive = "sk-ant-oat01-SIDE-LIVE-eeee"
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", mainConfig),
		claudeOAuthPayload(sideLive, now.Add(8*time.Hour)))

	_, stderr := captureStderr(t, func() int { return runRebind(ctx, app, opts, constants.ToolClaude, "side", false) })

	// The directory the re-bind acts for is claude/side's isolated config dir, which reads
	// nothing yet — so this is missing evidence with a login remedy, not a conflict.
	if !strings.Contains(stderr, "this directory does not read it yet") {
		t.Fatalf("the pass must act under the new account's config dir: %q", stderr)
	}
	if strings.Contains(stderr, "belongs to an account other than") {
		t.Fatalf("acting under the previous account's dir is what produces this wording: %q", stderr)
	}
}

// A `-s` ↔ `-i` toggle changes the config dir, so the pin-level pass and the write acted
// under two different identities: the pass under the old dir, which is a reader, and the
// write under the new one, which is not. The pass said `Conflicting` and predicted a
// replacement while the write kept the copy — the message was the exact inverse of what
// happened, on the one arm that had no consequence check. Measured by review 2026-08-08.
func TestRunPinModeToggleReportsWhatTheWriteActuallyDid(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	_, shared := bindClaudeHere(t, app, "main")
	// A login as side inside the directory, and the account's store holds side's copy.
	writeFile(t, filepath.Join(shared, ".claude.json"), claudeIdentityFile("side-uuid"))
	const sideLive = "sk-ant-oat01-SIDE-LIVE-eeee"
	credFile := dirCredFile(app, constants.ToolClaude, "main", shared)
	writeFile(t, credFile, claudeOAuthPayload(sideLive, now.Add(8*time.Hour)))

	opts := commonOpts{Format: formatText}
	_, stderr := captureStderr(t, func() int { return runPin(ctx, app, opts, "main", modeIsolated, false) })

	if got := readFile(t, credFile); !strings.Contains(got, sideLive) {
		t.Fatalf("the toggle kept the copy in earlier versions too; it must still: %s", got)
	}
	if strings.Contains(stderr, "this bind replaces it") {
		t.Fatalf("the message must not predict a replacement the write did not make: %q", stderr)
	}
	if !strings.Contains(stderr, "leaving it where it is") {
		t.Fatalf("the message must state what happened: %q", stderr)
	}
}

// The global-home walk matches by **path**, and that filter is load-bearing: without it a
// home of any other account counts as a reader of this one's store, disagrees with every
// real reader, and the harvest stops running for every account as soon as a second account
// has an isolated home. Replacing the condition with `true` survived the whole suite
// (execution-type review, 2026-08-08).
func TestAnotherAccountsGlobalHomeIsNotAReader(t *testing.T) {
	app, now := twoClaudeAccounts(t)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	// claude/side gets a global isolated home, labelled side by the materializer.
	if code, out := captureStdout(t, func() int {
		return runUseIsolated(ctx, app, opts, constants.ToolClaude, "side")
	}); code != constants.ExitOK {
		t.Fatalf("use -i side exit %d: %s", code, out)
	}
	sideHome := app.Paths.GlobalIsolatedHomeDir(constants.ToolClaude, "side")
	if got := readFile(t, filepath.Join(sideHome, ".claude.json")); !strings.Contains(got, "side-uuid") {
		t.Fatalf("the fixture needs side's home to name side: %q", got)
	}

	// A directory bound to claude/main, whose own reading is honest.
	_, storeDir := bindClaudeHere(t, app, "main")
	const refreshed = "sk-ant-oat01-MAIN-REFRESHED-cccc"
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", storeDir),
		claudeOAuthPayload(refreshed, now.Add(8*time.Hour)))

	be := testBackend(t, app)
	if err := app.writeDirCredential(ctx, be, constants.ToolClaude, "main", storeDir, false); err != nil {
		t.Fatalf("writeDirCredential: %v", err)
	}
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); !strings.Contains(got, refreshed) {
		t.Fatalf("another account's isolated home must not be read as a reader of this store: %s", got)
	}
}

// A sibling's disagreement is not a licence for an unrelated bind to spend the copy. The
// readers all name another account, so the copy is a live login of *somebody's* — but the
// directory being bound is not one of them and has no reading of its own, so it takes the
// keep branch with everything else that cannot establish an owner.
//
// Introduced by the first version of the reader model and caught by review, 2026-08-08:
// `Conflicting` was returned whenever every reader that could speak conflicted, with no
// requirement that this directory be one of them. A majority of one is not different from a
// majority — and here it destroyed the only copy of the sibling's login, which the bind
// *before* the reader model had kept.
func TestRunPinDoesNotSpendACopyOnlyASiblingDisagreesAbout(t *testing.T) {
	app := overlayTestApp(t)
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	_, siblingStore := bindClaudeHere(t, app, "main")
	// A login as side inside the sibling. Its cache names side and the account's store now
	// holds side's refreshed token — the only copy of it anywhere, since claude/side has
	// never been captured.
	writeFile(t, filepath.Join(siblingStore, ".claude.json"), claudeIdentityFile("side-uuid"))
	const sideLive = "sk-ant-oat01-SIDE-LIVE-eeee"
	credFile := dirCredFile(app, constants.ToolClaude, "main", siblingStore)
	writeFile(t, credFile, claudeOAuthPayload(sideLive, now.Add(8*time.Hour)))

	// A brand-new directory, which has never read that store. pinHere rather than
	// bindClaudeHere: this bind is expected to keep the copy, so it writes no identity
	// label and that fixture's control would fail for the right reason.
	pinHere(t, app, modeShared)

	if got := readFile(t, credFile); !strings.Contains(got, sideLive) {
		t.Fatalf("a bind that reads nothing must not spend a login its readers say is somebody's: %s", got)
	}
	be := testBackend(t, app)
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); strings.Contains(got, sideLive) {
		t.Fatalf("and must not file it under this account either: %s", got)
	}
}

// A **pre-split** binding keeps its credential inside its own store, so it does not read
// the account's — and the reader filter has to say that from the fragment's recorded
// entry rather than by composing the account's store path from the account it binds.
// Deriving it counts such a directory as a reader and lets its label attribute a copy it
// never reads; that mutation survived the whole suite (execution-type review, 2026-08-08).
func TestAPreSplitBindingDoesNotReadTheAccountStore(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	_, storeA := bindClaudeHere(t, app, "main")
	dirB, storeB := bindClaudeHere(t, app, "main")
	// B is left in the shape a kae from before the split produced, and someone logged in
	// there as side. Its own store holds its own credential; it reads nothing of claude/main's.
	makePreSplit(t, app, constants.ToolClaude, "main", dirB, storeB)
	writeFile(t, filepath.Join(storeB, ".claude.json"), claudeIdentityFile("side-uuid"))

	const refreshed = "sk-ant-oat01-MAIN-REFRESHED-cccc"
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", storeA), claudeOAuthPayload(refreshed, now.Add(8*time.Hour)))

	be := testBackend(t, app)
	if err := app.writeDirCredential(ctx, be, constants.ToolClaude, "main", storeA, false); err != nil {
		t.Fatalf("writeDirCredential: %v", err)
	}
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); !strings.Contains(got, refreshed) {
		t.Fatalf("a pre-split binding reads its own store, so its label must not veto this harvest: %s", got)
	}
}

// A reader that cannot speak does not veto one that can: with one directory confirming and
// another that has no cache yet, the copy is still this account's and is still harvested.
// Requiring silence to be empty survived the suite (execution-type review, 2026-08-08).
func TestAReaderWithNothingToSayDoesNotVetoOneThatConfirms(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	_, confirming := bindClaudeHere(t, app, "main")
	_, mute := bindClaudeHere(t, app, "main")
	if err := os.Remove(filepath.Join(mute, ".claude.json")); err != nil {
		t.Fatal(err) // the ordinary state until the tool has run in that directory
	}

	const refreshed = "sk-ant-oat01-MAIN-REFRESHED-cccc"
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", confirming), claudeOAuthPayload(refreshed, now.Add(8*time.Hour)))

	be := testBackend(t, app)
	if err := app.writeDirCredential(ctx, be, constants.ToolClaude, "main", confirming, false); err != nil {
		t.Fatalf("writeDirCredential: %v", err)
	}
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); !strings.Contains(got, refreshed) {
		t.Fatalf("a reader with nothing to say must not veto one that confirms: %s", got)
	}
}

// With **two** readers that cannot speak, neither one's reason may be reported: it would
// describe one directory as if it described the store. The single-reader carry above it is
// what makes that distinction worth pinning — collapsing both into one arm survived the
// suite either way round (execution-type review, 2026-08-08).
func TestTwoReadersThatCannotSpeakGetTheirOwnReason(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	_, mute := bindClaudeHere(t, app, "main")
	if err := os.Remove(filepath.Join(mute, ".claude.json")); err != nil {
		t.Fatal(err)
	}
	_, unreadable := bindClaudeHere(t, app, "main")
	// Well-formed JSON that names no account: a different reason from the one above, so a
	// carry of either would be visible.
	writeFile(t, filepath.Join(unreadable, ".claude.json"), `{"oauthAccount":null,"projects":{}}`)

	const refreshed = "sk-ant-oat01-MAIN-REFRESHED-cccc"
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", unreadable), claudeOAuthPayload(refreshed, now.Add(8*time.Hour)))

	be := testBackend(t, app)
	_, stderr := captureStderr(t, func() int {
		if err := app.writeDirCredential(ctx, be, constants.ToolClaude, "main", unreadable, false); err != nil {
			t.Fatalf("writeDirCredential: %v", err)
		}
		return 0
	})
	if !strings.Contains(stderr, "no directory that reads this credential could attribute it") {
		t.Fatalf("two silent readers must get the reason that describes the store: %q", stderr)
	}
	if strings.Contains(stderr, "the directory holds no identity cache to compare") ||
		strings.Contains(stderr, "kae cannot read the identity records it would compare") {
		t.Fatalf("neither reader's own reason may stand in for the store's: %q", stderr)
	}
	// A keep retracts a label that **disagrees**, never one kae could not read — the same
	// rule an unreadable credential gets, for the same reason: kae has not established that
	// it is wrong. Retracting on any refusal rather than on the conflicting one survived the
	// suite until this line existed.
	if got := readFile(t, filepath.Join(unreadable, ".claude.json")); !strings.Contains(got, `"oauthAccount":null`) {
		t.Fatalf("a label kae cannot read must survive a keep: %q", got)
	}
}

// `kae run -i` prepares the same per-account home `kae use -i` does and **never writes
// state.synced** — so a reader set sourced from that map left it with no reader at all,
// and every run after the first kept the copy instead of harvesting it, leaving the account
// snapshot holding a credential single-use rotation had already invalidated. Found by
// review, 2026-08-08; the readers are read from disk for exactly this.
func TestRunIsolatedHomeIsAReaderWithoutStateSynced(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	be := testBackend(t, app)

	// The one call `kae run -i` makes per target (run.go); nothing else about that path
	// touches the credential.
	if _, err := app.prepareGlobalIsolatedHome(ctx, be, constants.ToolClaude, "main", false); err != nil {
		t.Fatalf("prepare isolated home: %v", err)
	}
	home := app.Paths.GlobalIsolatedHomeDir(constants.ToolClaude, "main")
	if got := readFile(t, filepath.Join(home, ".claude.json")); !strings.Contains(got, "main-uuid") {
		t.Fatalf("the home must carry the identity that attributes the store: %q", got)
	}
	// The whole point of the fixture: this path records nothing in state.synced, so a
	// reader set read from there would be empty. Asserted rather than assumed — if
	// `run -i` ever starts recording it, this test stops covering the disk source and
	// says so instead of passing for the other reason.
	st, err := app.loadState()
	if err != nil {
		t.Fatal(err)
	}
	if _, recorded := st.Synced[constants.ToolClaude]; recorded {
		t.Fatal("this fixture only covers the disk source while run -i leaves state.synced alone")
	}

	const refreshed = "sk-ant-oat01-MAIN-REFRESHED-cccc"
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", home),
		claudeOAuthPayload(refreshed, now.Add(8*time.Hour)))
	if _, err := app.prepareGlobalIsolatedHome(ctx, be, constants.ToolClaude, "main", false); err != nil {
		t.Fatalf("prepare isolated home again: %v", err)
	}
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); !strings.Contains(got, refreshed) {
		t.Fatalf("the home is the only reader, and its evidence must let the copy be harvested: %s", got)
	}
}
