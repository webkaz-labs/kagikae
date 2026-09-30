package cmd

// Tests of how a binding's fragment names its credential store: pre-split
// migration, spec overrides, global scope and rebinding the credential entry.

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
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/paths"
	"github.com/webkaz-labs/kagikae/internal/runner"
	"github.com/webkaz-labs/kagikae/internal/testutil/runnertest"
)

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
		return runRebind(ctx, app, opts, constants.ToolClaude, "side", false)
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
		return runRebind(ctx, app, opts, constants.ToolClaude, "side", false)
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
		return runRebind(ctx, app, opts, constants.ToolClaude, "side", false)
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
