package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/adapter/claude"
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

		if code, out := captureStdout(t, func() int { return runUnpin(ctx, app, opts, true) }); code != constants.ExitOK {
			t.Fatalf("unpin --purge exit %d: %s", code, out)
		}
		if !strings.Contains(strings.Join(sim.ops, ","), "delete") {
			t.Fatalf("the last binding's purge must remove the credential: %v", sim.ops)
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

// …and the other side of that exception: an ordinary re-bind, with nothing migrating,
// leaves the account's credential alone. Without this the guard above could be "sweep
// every kept store" and the migration test would still pass.
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
	pinHere(t, app, modeShared)

	var out string
	runner.With(&runnertest.Fake{Stdout: payload, Code: 0}, func() {
		var code int
		code, out = captureStdout(t, func() int {
			return runRebind(ctx, app, opts, constants.ToolClaude, "side")
		})
		mustExit(t, constants.ExitOK, code, out)
	})

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

// The count has to fail closed. An unreadable fragment is not "no reference": kae
// cannot tell, and the difference between those two answers is one logged-out
// sibling. Reached with a fragment path that is a directory, which errors on read
// rather than reading as absent.
func TestUnpinPurgeKeepsACredentialItCannotCountReferencesFor(t *testing.T) {
	sim := &keychainSim{}
	runner.With(sim, func() {
		app := overlayTestApp(t)
		app.Env.GOOS = "darwin"
		ctx := context.Background()
		opts := commonOpts{Format: formatText}
		captureClaudeFromKeychain(t, app, sim, "main", mainToken, app.Now().Add(time.Hour))
		other := pinHere(t, app, modeShared)
		pinHere(t, app, modeShared)

		// The sibling's fragment becomes unreadable *after* it was written, so the pin
		// index still names it and the walk still reaches it.
		fragment := filepath.Join(other, fragmentRelPath)
		if err := os.Remove(fragment); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(fragment, 0o700); err != nil {
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

	specs, err := app.dirSpecs(context.Background(), constants.ToolClaude, oneDir(store))
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
	if !slicesContains(seen, want) {
		t.Fatalf("the login flow must be handed %q, got %v", want, seen)
	}
}

func slicesContains(haystack []string, want string) bool {
	for _, s := range haystack {
		if s == want {
			return true
		}
	}
	return false
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
