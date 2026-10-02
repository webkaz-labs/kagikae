package cmd

// The per-directory tree mode (`kae pin -t`, docs/ADAPTERS.md § Per-directory tree
// bind): one account-agnostic store for the bound directory's tree, claude only.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/config"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/paths"
	"github.com/webkaz-labs/kagikae/internal/runner"
	"github.com/webkaz-labs/kagikae/internal/testutil/runnertest"
)

// treeTestApp is overlayTestApp whose main profile also maps codex, the tool tree mode
// leaves on the real home, with one opt-in shared item for claude.
func treeTestApp(t *testing.T) *App {
	t.Helper()
	app := overlayTestApp(t)
	app.Config.Profiles["main"] = config.Profile{Accounts: map[string]string{
		constants.ToolClaude: "main",
		constants.ToolCodex:  "main",
		constants.ToolAgy:    "main",
	}}
	app.Config.Tools[constants.ToolClaude] = config.Tool{IsolatedSharedItems: []string{"settings.json"}}
	return app
}

// pinMode binds the current directory in mode and returns its stdout and stderr.
func pinMode(t *testing.T, app *App, profile, mode string) (stdout, stderr string) {
	t.Helper()
	code, stdout, stderr := captureBoth(t, func() int {
		return runPin(context.Background(), app, commonOpts{Format: formatText}, profile, mode, false)
	})
	mustExit(t, constants.ExitOK, code, stdout+stderr)
	// Its warning and note lines are new output paths (docs/CLI.md § Output Rules).
	if strings.Contains(stdout+stderr, mainToken) || strings.Contains(stdout+stderr, sideToken) {
		t.Fatalf("kae pin (%s) printed a credential: %s%s", mode, stdout, stderr)
	}
	return stdout, stderr
}

func fragmentLine(t *testing.T, dir, prefix string) string {
	t.Helper()
	for _, line := range strings.Split(readFile(t, filepath.Join(dir, fragmentRelPath)), "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	return ""
}

// A leftover tree store never borrows a shared binding's account, and a leftover
// shared store never borrows a tree binding's: both are account-agnostic, so the only
// thing that names their credential's account is the binding being replaced, and only
// when that binding is of the store's own mode (T2). Borrowing across modes would file
// one account's token under another's name.
func TestTreeStoreAttributionNeverCrossesModes(t *testing.T) {
	app := testApp(t, nil)
	pinID := "abcdef0123456789"
	tree := dirStore{Mode: modeTree, Tool: constants.ToolClaude, Dir: app.Paths.TreeDir(pinID, constants.ToolClaude)}
	shared := dirStore{Mode: modeShared, Tool: constants.ToolClaude, Dir: app.Paths.SharedDir(pinID, constants.ToolClaude)}
	binding := func(mode string) fragmentInfo {
		return fragmentInfo{
			Mode:     mode,
			Accounts: map[string]string{constants.ToolClaude: "main"},
			CredDirs: map[string]string{constants.ToolClaude: app.credStoreDir(constants.ToolClaude, "main")},
		}
	}
	if got := storeAccount(tree, binding(modeTree)); got != "main" {
		t.Fatalf("a tree store under a tree binding is that binding's account, got %q", got)
	}
	if got := storeAccount(tree, binding(modeShared)); got != "" {
		t.Fatalf("a shared binding must not attribute a leftover tree store, got %q", got)
	}
	if got := storeAccount(shared, binding(modeTree)); got != "" {
		t.Fatalf("a tree binding must not attribute a leftover shared store, got %q", got)
	}
	// …and so neither is handed the binding's credential store to harvest from.
	if got := app.attributedCredDir(tree, binding(modeShared)); got != "" {
		t.Fatalf("a leftover tree store must keep its own credential location, got %q", got)
	}
	if got := app.attributedCredDir(shared, binding(modeTree)); got != "" {
		t.Fatalf("a leftover shared store must keep its own credential location, got %q", got)
	}
	if got, want := app.attributedCredDir(tree, binding(modeTree)), app.credStoreDir(constants.ToolClaude, "main"); got != want {
		t.Fatalf("the tree binding's own store reads its credential store: got %q, want %q", got, want)
	}
}

// `kae pin -t`: the fragment points claude at the tree store and at the account's
// credential store, the store links only the configured items, the directory's link
// names the tree store, and codex (and agy) keep the real home with a warning.
func TestPinTreeWritesTheFragmentAndTheStore(t *testing.T) {
	app := treeTestApp(t)
	captureClaude(t, app, "main", mainToken)
	writeFile(t, filepath.Join(app.Env.Home, ".claude", "CLAUDE.md"), "not opted in")
	dir := chdirTo(t, filepath.Join(t.TempDir(), "main-app"))
	_, stderr := pinMode(t, app, "main", modeTree)

	pinID := paths.PinID(dir)
	store := app.Paths.TreeDir(pinID, constants.ToolClaude)
	credDir := app.credStoreDir(constants.ToolClaude, "main")
	content := readFile(t, filepath.Join(dir, fragmentRelPath))
	for _, want := range []string{
		fragModePrefix + modeTree + "\n",
		fragAccountPrefix + "claude=main\n",
		"CLAUDE_CONFIG_DIR = " + strconv.Quote(store) + "\n",
		"CLAUDE_SECURESTORAGE_CONFIG_DIR = " + strconv.Quote(credDir) + "\n",
		"# warning: tree mode binds claude only, so codex keeps the real home",
		"# warning: agy has no stable home-isolation env var",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("fragment lacks %q:\n%s", want, content)
		}
	}
	for _, absent := range []string{"CODEX_HOME", fragAccountPrefix + "codex="} {
		if strings.Contains(content, absent) {
			t.Errorf("tree mode must not bind codex, but the fragment has %q:\n%s", absent, content)
		}
	}
	if !strings.Contains(stderr, "kae: warning: tree mode binds claude only, so codex keeps the real home") {
		t.Errorf("the bind must say on stderr that codex keeps the real home: %q", stderr)
	}
	if strings.Contains(stderr, "kae: warning: agy has no stable") {
		t.Errorf("agy's warning stays a fragment comment, as under -s and -i: %q", stderr)
	}
	info, err := os.Stat(store)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("the tree store must exist at 0700: %v %v", info, err)
	}
	if link, err := os.Readlink(filepath.Join(store, "settings.json")); err != nil ||
		link != filepath.Join(app.Env.Home, ".claude", "settings.json") {
		t.Fatalf("an opt-in item must be linked from the real home: %q %v", link, err)
	}
	if _, err := os.Lstat(filepath.Join(store, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Fatalf("an item not in isolated_shared_items must not be linked: %v", err)
	}
	if !strings.Contains(readFile(t, filepath.Join(credDir, ".credentials.json")), mainToken) {
		t.Fatal("the credential belongs in the account's credential store")
	}
	if _, err := os.Stat(filepath.Join(store, ".credentials.json")); !os.IsNotExist(err) {
		t.Fatalf("the tree store must hold no credential copy: %v", err)
	}
	if !strings.Contains(readFile(t, filepath.Join(store, ".claude.json")), "main-uuid") {
		t.Fatal("the identity cache in the tree store must name the bound account")
	}
	if link, err := os.Readlink(filepath.Join(dir, ".config", constants.ToolClaude)); err != nil || link != store {
		t.Fatalf("./.config/claude must name the tree store: %q %v", link, err)
	}
	if _, err := os.Lstat(filepath.Join(dir, ".config", constants.ToolCodex)); !os.IsNotExist(err) {
		t.Fatalf("an unbound codex gets no store link: %v", err)
	}
}

// The account switch in a tree directory: the config line and the store's sessions
// and history stay byte-identical, while the credential entry, the account record and
// /oauthAccount move. Switching back shows both accounts' sessions in the one store.
func TestRebindInATreeDirectoryKeepsTheStore(t *testing.T) {
	app := treeTestApp(t)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	runner.With(&runnertest.Fake{Code: 0}, func() {
		captureClaude(t, app, "main", mainToken)
		captureClaude(t, app, "side", sideToken)
	})
	dir := chdirTo(t, filepath.Join(t.TempDir(), "main-app"))
	pinMode(t, app, "main", modeTree)
	store := app.Paths.TreeDir(paths.PinID(dir), constants.ToolClaude)
	configLine := fragmentLine(t, dir, "CLAUDE_CONFIG_DIR = ")

	mainSession := filepath.Join(store, "projects", "-main-app", "main.jsonl")
	history := filepath.Join(store, "history.jsonl")
	writeFile(t, mainSession, `{"session":"main"}`+"\n")
	writeFile(t, history, `{"display":"main prompt"}`+"\n")
	// What a running claude wrote into the tree's identity file after the bind: the
	// patch must re-read the file, not reuse what kae read before (ARCHITECTURE.md
	// § Known Traps).
	identity := filepath.Join(store, ".claude.json")
	var doc map[string]any
	if err := json.Unmarshal([]byte(readFile(t, identity)), &doc); err != nil {
		t.Fatal(err)
	}
	doc["numStartups"] = 7
	data, _ := json.Marshal(doc)
	writeFile(t, identity, string(data))

	rebind := func(account string) {
		t.Helper()
		code, out, stderr := captureBoth(t, func() int { return runRebind(ctx, app, opts, constants.ToolClaude, account, false) })
		mustExit(t, constants.ExitOK, code, out+stderr)
		if strings.Contains(out+stderr, mainToken) || strings.Contains(out+stderr, sideToken) {
			t.Fatalf("a tree switch printed a credential: %s%s", out, stderr)
		}
		if !strings.Contains(out, "Re-bound claude to account "+account+" (tree;") {
			t.Fatalf("the re-bind must report the tree mode: %q", out)
		}
	}
	rebind("side")

	if got := fragmentLine(t, dir, "CLAUDE_CONFIG_DIR = "); got != configLine {
		t.Fatalf("the config line must not move on a tree switch: %q -> %q", configLine, got)
	}
	if got, want := fragmentLine(t, dir, "CLAUDE_SECURESTORAGE_CONFIG_DIR = "),
		"CLAUDE_SECURESTORAGE_CONFIG_DIR = "+strconv.Quote(app.credStoreDir(constants.ToolClaude, "side")); got != want {
		t.Fatalf("the credential line must follow the account: %q, want %q", got, want)
	}
	if got := mustFragmentAt(t, dir).Accounts[constants.ToolClaude]; got != "side" {
		t.Fatalf("the account record must name side, got %q", got)
	}
	if got := readFile(t, mainSession); got != `{"session":"main"}`+"\n" {
		t.Fatalf("projects/ must be untouched by a switch: %q", got)
	}
	if got := readFile(t, history); got != `{"display":"main prompt"}`+"\n" {
		t.Fatalf("history.jsonl must be untouched by a switch: %q", got)
	}
	after := readFile(t, identity)
	if !strings.Contains(after, "side-uuid") || strings.Contains(after, "main-uuid") {
		t.Fatalf("/oauthAccount must name side after the switch: %s", after)
	}
	if !strings.Contains(after, `"numStartups":7`) {
		t.Fatalf("the patch must keep what claude wrote after the bind (re-read before patching): %s", after)
	}
	if !strings.Contains(readFile(t, filepath.Join(app.credStoreDir(constants.ToolClaude, "side"), ".credentials.json")), sideToken) {
		t.Fatal("side's credential belongs in side's credential store")
	}

	sideSession := filepath.Join(store, "projects", "-main-app", "side.jsonl")
	writeFile(t, sideSession, `{"session":"side"}`+"\n")
	rebind("main")
	if got := fragmentLine(t, dir, "CLAUDE_CONFIG_DIR = "); got != configLine {
		t.Fatalf("switching back must not move the config line either: %q", got)
	}
	entries, err := os.ReadDir(filepath.Join(store, "projects", "-main-app"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if !slices.Equal(names, []string{"main.jsonl", "side.jsonl"}) {
		t.Fatalf("back on main the tree holds both accounts' sessions, got %v", names)
	}
	if !strings.Contains(readFile(t, identity), "main-uuid") {
		t.Fatal("/oauthAccount must name main again")
	}
}

// Two directories bound with -t to one account read one credential: the tree stores
// differ, the credential entry is the same, and neither store holds a copy.
func TestTwoTreeDirectoriesOfOneAccountShareOneCredential(t *testing.T) {
	app := treeTestApp(t)
	captureClaude(t, app, "main", mainToken)
	first := chdirTo(t, filepath.Join(t.TempDir(), "main-app"))
	pinMode(t, app, "main", modeTree)
	second := chdirTo(t, filepath.Join(t.TempDir(), "side-project"))
	pinMode(t, app, "main", modeTree)

	want := app.credStoreDir(constants.ToolClaude, "main")
	for _, dir := range []string{first, second} {
		if got := mustFragmentAt(t, dir).CredDirs[constants.ToolClaude]; got != want {
			t.Fatalf("%s exports credential dir %q, want %q", dir, got, want)
		}
		store := app.Paths.TreeDir(paths.PinID(dir), constants.ToolClaude)
		if _, err := os.Stat(filepath.Join(store, ".credentials.json")); !os.IsNotExist(err) {
			t.Fatalf("%s holds a second credential copy: %v", store, err)
		}
	}
	if app.Paths.TreeDir(paths.PinID(first), constants.ToolClaude) == app.Paths.TreeDir(paths.PinID(second), constants.ToolClaude) {
		t.Fatal("each directory keeps its own tree store")
	}
}

// Every reader of the binding names it tree: kae ls --pins (text and JSON), the user
// level and session row of `kae ls claude` from a subdirectory, `kae status`, and the
// global-scope warning.
func TestTreeModeShowsWhereverTheModeIsReported(t *testing.T) {
	app := treeTestApp(t)
	captureClaude(t, app, "main", mainToken)
	bound := chdirTo(t, filepath.Join(t.TempDir(), "main-app"))
	pinMode(t, app, "main", modeTree)
	store := app.Paths.TreeDir(paths.PinID(bound), constants.ToolClaude)

	code, out := captureStdout(t, func() int { return runLsPins(app, commonOpts{Format: formatJSON}) })
	mustExit(t, constants.ExitOK, code, out)
	var pins pinsReport
	if err := json.Unmarshal([]byte(out), &pins); err != nil {
		t.Fatal(err)
	}
	if len(pins.BoundDirectories) != 1 || pins.BoundDirectories[0].Mode != constants.ModeTree ||
		pins.BoundDirectories[0].Stores[constants.ToolClaude] != store {
		t.Fatalf("ls --pins --json must report mode tree and the tree store: %s", out)
	}
	code, out = captureStdout(t, func() int { return runLsPins(app, commonOpts{Format: formatText}) })
	mustExit(t, constants.ExitOK, code, out)
	if !strings.Contains(out, " tree") {
		t.Fatalf("ls --pins must show tree: %s", out)
	}

	chdirTo(t, filepath.Join(bound, "sub", "deeper"))
	claude := placesOf(t, app, lsRequest{target: constants.ToolClaude})
	if claude[0].Path != store || claude[0].Mode != constants.ModeTree || claude[0].Account != "main" {
		t.Fatalf("the user level below a tree directory is the tree store: %+v", claude[0])
	}
	if got, want := kindsAndPaths(claude)[1], wantSession(t, store); got != want {
		t.Fatalf("the session row follows the governing binding: %q, want %q", got, want)
	}

	// A mise-active shell in the subdirectory: the fragment's [env] is exported.
	env := map[string]string{
		constants.EnvKaeProfile:           "main",
		"CLAUDE_CONFIG_DIR":               store,
		"CLAUDE_SECURESTORAGE_CONFIG_DIR": app.credStoreDir(constants.ToolClaude, "main"),
	}
	app.Env.Getenv = func(key string) string { return env[key] }
	app.Env.LookupEnv = func(key string) (string, bool) { v, ok := env[key]; return v, ok }
	report, err := buildStatus(context.Background(), app)
	if err != nil {
		t.Fatal(err)
	}
	if report.Pinned == nil || report.Pinned.Mode != constants.ModeTree {
		t.Fatalf("status must report pinned mode tree from a subdirectory, got %+v", report.Pinned)
	}
	_, stderr := captureStderr(t, func() int { app.pinnedGlobalScope(); return 0 })
	if !strings.Contains(stderr, "this directory is bound (tree)") {
		t.Fatalf("the global-scope warning must name tree: %q", stderr)
	}
}

// unpin keeps the tree store and removes the link; a re-pin finds the store again; and
// `--purge` counts a tree fragment as a reader of the account's credential store.
func TestUnpinKeepsTheTreeStoreAndPurgeCountsTreeFragments(t *testing.T) {
	sim := &keychainSim{}
	runner.With(sim, func() {
		app := treeTestApp(t)
		app.Env.GOOS = "darwin"
		ctx := context.Background()
		opts := commonOpts{Format: formatText}
		captureClaudeFromKeychain(t, app, sim, "main", mainToken, app.Now().Add(time.Hour))

		keeper := pinHere(t, app, modeTree)
		purged := pinHere(t, app, modeTree)
		store := app.Paths.TreeDir(paths.PinID(purged), constants.ToolClaude)
		session := filepath.Join(store, "projects", "x", "s.jsonl")
		writeFile(t, session, "kept")
		sim.ops = nil

		// cwd is purged (pinHere chdirs).
		_, stderr := captureStderr(t, func() int { return runUnpin(ctx, app, opts, true) })
		if strings.Contains(strings.Join(sim.ops, ","), "delete") {
			t.Fatalf("a tree fragment elsewhere still reads the credential; purge must keep it: %v", sim.ops)
		}
		if !strings.Contains(stderr, "still use the claude credential for main") {
			t.Fatalf("keeping it must say why: %q", stderr)
		}
		if readFile(t, session) != "kept" {
			t.Fatal("unpin must keep the tree store")
		}
		if _, err := os.Lstat(filepath.Join(purged, ".config", constants.ToolClaude)); !os.IsNotExist(err) {
			t.Fatalf("unpin removes the store link: %v", err)
		}

		// A re-pin finds the store again.
		code, out := captureStdout(t, func() int {
			return runPin(ctx, app, opts, "main", modeTree, false)
		})
		mustExit(t, constants.ExitOK, code, out)
		if got := fragmentLine(t, purged, "CLAUDE_CONFIG_DIR = "); got != "CLAUDE_CONFIG_DIR = "+strconv.Quote(store) {
			t.Fatalf("the re-pin must point at the kept store: %q", got)
		}
		if readFile(t, session) != "kept" {
			t.Fatal("the re-pin must find the kept sessions")
		}

		// With the keeper unpinned too, nothing reads the credential store: purge takes it.
		if err := os.Chdir(keeper); err != nil {
			t.Fatal(err)
		}
		if code, out := captureStdout(t, func() int { return runUnpin(ctx, app, opts, false) }); code != constants.ExitOK {
			t.Fatalf("unpin keeper: %s", out)
		}
		if err := os.Chdir(purged); err != nil {
			t.Fatal(err)
		}
		sim.ops = nil
		if code, out := captureStdout(t, func() int { return runUnpin(ctx, app, opts, true) }); code != constants.ExitOK {
			t.Fatalf("unpin --purge: %s", out)
		}
		if !strings.Contains(strings.Join(sim.ops, ","), "delete") {
			t.Fatalf("with no tree fragment left, purge removes the credential: %v", sim.ops)
		}
	})
}

// A mode change moves no credential and no session: each change prints a note naming
// the store it left, the note for -t says the new tree store starts empty, and the
// exit code stays 0. The note is a new output path, so it is checked for the token.
func TestModeChangesNoteTheStoreTheyLeave(t *testing.T) {
	app := treeTestApp(t)
	captureClaude(t, app, "main", mainToken)
	dir := chdirTo(t, filepath.Join(t.TempDir(), "main-app"))
	pinID := paths.PinID(dir)
	cred := filepath.Join(app.credStoreDir(constants.ToolClaude, "main"), ".credentials.json")
	pinMode(t, app, "main", modeShared)
	shared := app.Paths.SharedDir(pinID, constants.ToolClaude)
	writeFile(t, filepath.Join(shared, "projects", "x", "s.jsonl"), "shared session")
	before := readFile(t, cred)

	tree := app.Paths.TreeDir(pinID, constants.ToolClaude)
	_, stderr := pinMode(t, app, "main", modeTree)
	for _, want := range []string{
		"kae: note: changing this directory from shared to tree moves no sessions; claude's stay in " +
			app.displayPath(shared) + ", which `kae pin -s` finds again",
		"kae: note: the new tree store " + app.displayPath(tree) + " starts empty",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
	if !strings.Contains(stderr, "~/") {
		t.Errorf("paths under HOME are printed through displayPath: %q", stderr)
	}
	if readFile(t, cred) != before {
		t.Fatal("a mode change must not move or rewrite the credential")
	}
	if _, err := os.Stat(filepath.Join(tree, "projects")); !os.IsNotExist(err) {
		t.Fatalf("a new tree store starts empty: %v", err)
	}
	if readFile(t, filepath.Join(shared, "projects", "x", "s.jsonl")) != "shared session" {
		t.Fatal("the store left behind keeps its sessions")
	}

	_, stderr = pinMode(t, app, "main", modeIsolated)
	if !strings.Contains(stderr, "from tree to isolated moves no sessions; claude's stay in "+
		app.displayPath(tree)+", which `kae pin -t` finds again") {
		t.Errorf("-t -> -i must name the tree store it left: %q", stderr)
	}
	if strings.Contains(stderr, "starts empty") {
		t.Errorf("only a new tree store is said to start empty: %q", stderr)
	}
	_, stderr = pinMode(t, app, "main", modeShared)
	if !strings.Contains(stderr, "from isolated to shared moves no sessions; claude's stay in "+
		app.displayPath(app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "main"))) {
		t.Errorf("-i -> -s must note the isolated store it left: %q", stderr)
	}
	_, stderr = pinMode(t, app, "main", modeTree)
	if !strings.Contains(stderr, "from shared to tree") || strings.Contains(stderr, "starts empty") {
		t.Errorf("returning to a kept tree store is not a new one: %q", stderr)
	}
	if _, stderr = pinMode(t, app, "main", modeTree); strings.Contains(stderr, "kae: note:") {
		t.Errorf("a re-pin in the same mode is not a mode change: %q", stderr)
	}
	if readFile(t, cred) != before {
		t.Fatal("no mode change moved the credential")
	}
}

// uninstall recognises the tree fragment it rendered — including the warning comments
// of the tools tree mode leaves on the real home — and keeps the tree store.
func TestUninstallRecognisesATreeFragmentAndKeepsTheStore(t *testing.T) {
	app := treeTestApp(t)
	dir := t.TempDir()
	targets := []runTarget{
		{Tool: constants.ToolClaude, Account: "side"},
		{Tool: constants.ToolCodex, Account: "side"},
		{Tool: constants.ToolAgy, Account: "side"},
	}
	entries := app.modeIsolationEntries(mustBindMode(t, modeTree), targets, paths.PinID(dir))
	content := renderDirFragment("side", modeTree, entries, nil, nil)
	if !strings.Contains(content, "# warning: tree mode binds claude only") {
		t.Fatalf("fixture must carry codex's warning or it tests nothing:\n%s", content)
	}
	if !app.ownedUninstallFragment(dir, content) {
		t.Fatalf("a kae-rendered tree fragment must be recognised:\n%s", content)
	}
	edited := strings.Replace(content, "codex keeps the real home", "codex keeps my home", 1)
	if app.ownedUninstallFragment(dir, edited) {
		t.Fatal("an edited warning comment is not kae's rendering")
	}

	fragment := filepath.Join(dir, fragmentRelPath)
	writeFile(t, fragment, content)
	store := filepath.Join(app.Paths.TreeDir(paths.PinID(dir), constants.ToolClaude), "history.jsonl")
	writeFile(t, store, "kept")
	if err := app.recordPinnedDir(paths.PinID(dir), dir); err != nil {
		t.Fatal(err)
	}
	code, output := captureStdout(t, func() int {
		return runUninstall(context.Background(), app, commonOpts{Format: formatJSON, Yes: true}, []string{dir}, filepath.Join(app.Env.Home, "kae"))
	})
	mustExit(t, constants.ExitUnsafeRefused, code, output) // no direct receipt
	if _, err := os.Stat(fragment); !os.IsNotExist(err) {
		t.Fatalf("the tree fragment must be removed: %v\n%s", err, output)
	}
	if readFile(t, store) != "kept" {
		t.Fatal("uninstall must keep the tree store")
	}
}

// codex in a tree directory: the re-bind refuses with unsupported (5) and names why,
// rather than the not_found (7) an unbound tool gets. A mode flag with <tool> <account>
// stays a usage error, and so does more than one mode flag.
func TestTreeRefusesCodexAndConflictingFlags(t *testing.T) {
	app := treeTestApp(t)
	captureClaude(t, app, "main", mainToken)
	chdirTo(t, filepath.Join(t.TempDir(), "main-app"))
	pinMode(t, app, "main", modeTree)
	code, stdout, stderr := captureBoth(t, func() int {
		return runRebind(context.Background(), app, commonOpts{Format: formatText}, constants.ToolCodex, "side", false)
	})
	mustExit(t, constants.ExitUnsupported, code, stdout+stderr)
	if !strings.Contains(stderr, "tree mode binds claude only") {
		t.Fatalf("the refusal must name the reason: %q", stderr)
	}
	if strings.Contains(stdout+stderr, mainToken) {
		t.Fatal("the refusal printed a credential")
	}
	for _, args := range [][]string{
		{"-t", "codex", "side"},
		{"--tree", "claude", "side"},
		{"-t", "-s"},
		{"-i", "-t"},
		{"-s", "-i", "-t"},
	} {
		if code := CmdPin(context.Background(), args); code != constants.ExitUsage {
			t.Errorf("kae pin %v must be a usage error, got %d", args, code)
		}
	}
}

// -t is kae pin's alone: completion offers it for pin only, and use and run reject it.
func TestTreeFlagIsPinsAlone(t *testing.T) {
	pin := flagCompletions("pin")
	if !slices.Contains(pin, "--tree") || !slices.Contains(pin, "-t") {
		t.Fatalf("`kae pin -<TAB>` must offer -t/--tree, got %v", pin)
	}
	for _, cmd := range []string{"use", "run", "ls", "open", "cd", "unpin"} {
		if got := flagCompletions(cmd); slices.Contains(got, "--tree") || slices.Contains(got, "-t") {
			t.Errorf("`kae %s` must not offer -t, got %v", cmd, got)
		}
	}
	if code := CmdUse(context.Background(), []string{"-t", "main"}); code != constants.ExitUsage {
		t.Errorf("kae use -t must exit 64, got %d", code)
	}
	if code := CmdRun(context.Background(), []string{"-t", "claude", "main", "--", "true"}); code != constants.ExitUsage {
		t.Errorf("kae run -t must exit 64, got %d", code)
	}
}
