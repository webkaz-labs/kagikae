package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/backup"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/lock"
	"github.com/webkaz-labs/kagikae/internal/runner"
)

// withInteractive replaces runner.RunInteractive for one test.
func withInteractive(t *testing.T, fn func(ctx context.Context, extraEnv []string, name string, args ...string) (int, error)) {
	t.Helper()
	saved := runner.RunInteractive
	runner.RunInteractive = fn
	t.Cleanup(func() { runner.RunInteractive = saved })
}

func TestSplitAtDashDash(t *testing.T) {
	kaeArgs, child := splitAtDashDash([]string{"claude", "main", "--", "claude", "-p", "hi"})
	if strings.Join(kaeArgs, " ") != "claude main" || strings.Join(child, " ") != "claude -p hi" {
		t.Fatalf("unexpected: %v %v", kaeArgs, child)
	}
	_, child = splitAtDashDash([]string{"claude", "main"})
	if child != nil {
		t.Fatalf("expected nil child: %v", child)
	}
}

func TestRunAuthTransaction(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	credsPath := filepath.Join(app.Env.Home, ".claude", ".credentials.json")

	seedClaude(t, app, mainToken, "main-uuid")
	code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "main") })
	mustExit(t, constants.ExitOK, code, out)
	seedClaude(t, app, sideToken, "side-uuid")
	code, out = captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "side") })
	mustExit(t, constants.ExitOK, code, out)

	ranChild := false
	withInteractive(t, func(_ context.Context, extraEnv []string, name string, args ...string) (int, error) {
		ranChild = true
		if name != "claude" {
			t.Fatalf("unexpected child: %s %v", name, args)
		}
		// During the run the live state must be the main account.
		live := readFile(t, credsPath)
		if !strings.Contains(live, mainToken) {
			t.Fatalf("main account not applied during run: %s", live)
		}
		// Simulate an OAuth refresh by the child.
		writeFile(t, credsPath, strings.Replace(live, mainToken, refreshedToken, 1))
		return 7, nil
	})

	code, _ = captureStdout(t, func() int {
		return runRun(ctx, app, opts, runModeShared, "claude", "main", []string{"claude"})
	})
	if !ranChild {
		t.Fatal("child did not run")
	}
	if code != 7 {
		t.Fatalf("child exit code not propagated: %d", code)
	}
	// Previous (side) live state restored.
	if live := readFile(t, credsPath); !strings.Contains(live, sideToken) {
		t.Fatalf("previous state not restored: %s", live)
	}
	// Refreshed credential recaptured into the main snapshot.
	code, out = captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "main") })
	mustExit(t, constants.ExitOK, code, out)
	if live := readFile(t, credsPath); !strings.Contains(live, refreshedToken) {
		t.Fatalf("refreshed token not recaptured: %s", live)
	}
}

func TestRunAuthNotCaptured(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	withInteractive(t, func(context.Context, []string, string, ...string) (int, error) {
		t.Fatal("child must not run")
		return 0, nil
	})
	code, out := captureStdout(t, func() int {
		return runRun(ctx, app, commonOpts{Format: formatText}, runModeShared, "claude", "nope", []string{"claude"})
	})
	mustExit(t, constants.ExitNotFound, code, out)
}

// §B: with no -- <cmd>, a single-tool target defaults the child to that tool's
// upstream binary (claude→claude, cursor→cursor-agent).
func TestDefaultChildCmd(t *testing.T) {
	cases := []struct {
		target  runTarget
		profile string
		want    string // "" => expect a usage error
	}{
		{runTarget{Tool: constants.ToolClaude, Account: "main"}, "", "claude"},
		{runTarget{Tool: constants.ToolCursor, Account: "main"}, "", "cursor-agent"},
		{runTarget{Tool: constants.ToolAgy, Account: "main"}, "", "agy"},
	}
	for _, tc := range cases {
		got, err := defaultChildCmd([]runTarget{tc.target}, "")
		if err != nil || len(got) != 1 || got[0] != tc.want {
			t.Fatalf("%s: defaultChildCmd = %v, %v; want [%s]", tc.target.Tool, got, err, tc.want)
		}
	}
	// A profile target (or any multi-tool target) has no single binary to default.
	if _, err := defaultChildCmd([]runTarget{{Tool: "claude", Account: "main"}}, "myprofile"); exitOf(err) != constants.ExitUsage {
		t.Fatalf("profile target must require an explicit -- <cmd>: %v", err)
	}
	if _, err := defaultChildCmd([]runTarget{{Tool: "claude"}, {Tool: "codex"}}, ""); exitOf(err) != constants.ExitUsage {
		t.Fatalf("multi-tool target must require an explicit -- <cmd>: %v", err)
	}
}

// §B end-to-end: kae run claude main (no --) applies the account and launches
// the defaulted `claude` child through the runner seam.
func TestRunDefaultsChildBinary(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	seedClaude(t, app, mainToken, "main-uuid")
	code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "main") })
	mustExit(t, constants.ExitOK, code, out)

	var childName string
	withInteractive(t, func(_ context.Context, _ []string, name string, _ ...string) (int, error) {
		childName = name
		return 0, nil
	})
	// nil childCmd => runRun resolves the default child.
	code, out = captureStdout(t, func() int {
		return runRun(ctx, app, opts, runModeShared, "claude", "main", nil)
	})
	mustExit(t, constants.ExitOK, code, out)
	if childName != "claude" {
		t.Fatalf("default child = %q, want claude", childName)
	}
}

func TestRunEnvMode(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	code, out := captureStdout(t, func() int {
		return runEnvSet(ctx, app, opts, []string{"claude", "ci", "ANTHROPIC_API_KEY=sk-test-123"})
	})
	mustExit(t, constants.ExitOK, code, out)

	var gotEnv []string
	withInteractive(t, func(_ context.Context, extraEnv []string, _ string, _ ...string) (int, error) {
		gotEnv = extraEnv
		return 0, nil
	})
	code, out = captureStdout(t, func() int {
		return runRun(ctx, app, opts, runModeEnv, "claude", "ci", []string{"claude", "-p", "x"})
	})
	mustExit(t, constants.ExitOK, code, out)
	if len(gotEnv) != 1 || gotEnv[0] != "ANTHROPIC_API_KEY=sk-test-123" {
		t.Fatalf("env not injected: %v", gotEnv)
	}

	// missing profile
	code, out = captureStdout(t, func() int {
		return runRun(ctx, app, opts, runModeEnv, "codex", "ci", []string{"codex"})
	})
	mustExit(t, constants.ExitNotFound, code, out)
}

func TestEnvSetStdinAndUnset(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	values, err := parseEnvAssignments([]string{"GEMINI_API_KEY"}, strings.NewReader("from-stdin\n"))
	if err != nil || values["GEMINI_API_KEY"] != "from-stdin" {
		t.Fatalf("stdin value: %v %v", values, err)
	}
	if _, err := parseEnvAssignments([]string{"A=1", "B"}, strings.NewReader("")); err == nil {
		t.Fatal("mix of forms must fail")
	}
	if _, err := parseEnvAssignments([]string{"bad-name=1"}, nil); err == nil {
		t.Fatal("invalid var name must fail")
	}

	code, out := captureStdout(t, func() int {
		return runEnvSet(ctx, app, opts, []string{"agy", "ci", "GEMINI_API_KEY=g-1", "GOOGLE_CLOUD_PROJECT=p-1"})
	})
	mustExit(t, constants.ExitOK, code, out)
	code, out = captureStdout(t, func() int { return runEnvList(ctx, app, commonOpts{Format: formatJSON}) })
	mustExit(t, constants.ExitOK, code, out)
	if !strings.Contains(out, "GEMINI_API_KEY") || strings.Contains(out, "g-1") {
		t.Fatalf("list must show names, never values: %s", out)
	}
	code, out = captureStdout(t, func() int {
		return runEnvUnset(ctx, app, opts, []string{"agy", "ci", "GEMINI_API_KEY"})
	})
	mustExit(t, constants.ExitOK, code, out)
	code, out = captureStdout(t, func() int {
		return runEnvUnset(ctx, app, opts, []string{"agy", "ci"})
	})
	mustExit(t, constants.ExitOK, code, out)
	code, out = captureStdout(t, func() int {
		return runEnvUnset(ctx, app, opts, []string{"agy", "ci"})
	})
	mustExit(t, constants.ExitNotFound, code, out)
}

func TestRunModeRemoved(t *testing.T) {
	// --mode was removed in v0.8.0; it must give a targeted pointer (exit 64),
	// validated before any environment access.
	code, out := captureStderr(t, func() int {
		return CmdRun(context.Background(), []string{"--mode", "env", "claude", "main", "--", "true"})
	})
	if code != constants.ExitUsage || !strings.Contains(out, "--mode was removed") {
		t.Fatalf("run --mode must point at -s/-i/--env, got %d: %s", code, out)
	}
}

func TestRunIsolated(t *testing.T) {
	app := applyTestApp(t, nil) // claude main/side captured; main/side profiles
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	credsPath := filepath.Join(app.Env.Home, ".claude", ".credentials.json")
	// Record the live login so a leaked mutation would be detectable.
	beforeLive := readFile(t, credsPath)

	var gotEnv []string
	withInteractive(t, func(_ context.Context, extraEnv []string, _ string, _ ...string) (int, error) {
		gotEnv = extraEnv
		return 0, nil
	})

	// run -i materializes the per-account global isolated home and points the
	// child there; it never mutates the live credential and takes no lock.
	held, err := lock.Acquire(app.Paths.LocksDir(), "claude")
	if err != nil {
		t.Fatal(err)
	}
	code, out := captureStdout(t, func() int {
		return runRun(ctx, app, opts, runModeIsolated, "claude", "main", []string{"claude"})
	})
	held.Release()
	mustExit(t, constants.ExitOK, code, out)
	wantHome := app.Paths.GlobalIsolatedHomeDir("claude", "main")
	wantCred := app.Paths.CredStoreDir("claude", "main")
	// Both variables, or the child finds no credential where it looks: the home
	// carries the sessions and the credential is the account's one shared copy.
	want := []string{"CLAUDE_CONFIG_DIR=" + wantHome, "CLAUDE_SECURESTORAGE_CONFIG_DIR=" + wantCred}
	if len(gotEnv) != len(want) || gotEnv[0] != want[0] || gotEnv[1] != want[1] {
		t.Fatalf("isolated env: %v, want %v", gotEnv, want)
	}
	if got := readFile(t, filepath.Join(wantCred, ".credentials.json")); !strings.Contains(got, mainToken) {
		t.Fatalf("isolated credential not materialized: %s", got)
	}
	if live := readFile(t, credsPath); live != beforeLive {
		t.Fatalf("run -i must not touch the live credential: %s", live)
	}

	// A single explicit unsupported tool exits 5.
	code, out = captureStdout(t, func() int {
		return runRun(ctx, app, opts, runModeIsolated, "agy", "main", []string{"agy"})
	})
	mustExit(t, constants.ExitUnsupported, code, out)
}

func TestLoginCapturesAndRestores(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	credsPath := filepath.Join(app.Env.Home, ".claude", ".credentials.json")
	seedClaude(t, app, sideToken, "side-uuid")

	const newToken = "sk-ant-oat01-NEWLOGIN-dddd"
	withInteractive(t, func(_ context.Context, _ []string, name string, args ...string) (int, error) {
		if name != "claude" || len(args) != 1 || args[0] != "/login" {
			t.Fatalf("unexpected login command: %s %v", name, args)
		}
		seedClaude(t, app, newToken, "main-uuid")
		return 0, nil
	})

	// --restore: capture the new login but put the previous one back
	code, out := captureStdout(t, func() int { return runLogin(ctx, app, opts, "claude", "main", true) })
	mustExit(t, constants.ExitOK, code, out)
	if !strings.Contains(out, "restored the previous login") {
		t.Fatalf("unexpected output: %s", out)
	}
	if live := readFile(t, credsPath); !strings.Contains(live, sideToken) {
		t.Fatalf("previous login not restored: %s", live)
	}
	// captured snapshot is applyable
	code, out = captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "main") })
	mustExit(t, constants.ExitOK, code, out)
	if live := readFile(t, credsPath); !strings.Contains(live, newToken) {
		t.Fatalf("captured login not applied: %s", live)
	}
}

func TestLoginRestoreOnCaptureFailure(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	credsPath := filepath.Join(app.Env.Home, ".claude", ".credentials.json")
	seedClaude(t, app, sideToken, "side-uuid")

	// the "login flow" logs the user out entirely, so the capture fails
	withInteractive(t, func(context.Context, []string, string, ...string) (int, error) {
		if err := os.Remove(credsPath); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(app.Env.Home, ".claude.json")); err != nil {
			t.Fatal(err)
		}
		return 0, nil
	})
	code, out := captureStdout(t, func() int { return runLogin(ctx, app, opts, "claude", "main", true) })
	mustExit(t, constants.ExitAuthMissing, code, out)
	if live := readFile(t, credsPath); !strings.Contains(live, sideToken) {
		t.Fatalf("--restore must put the previous login back even when capture fails: %s", live)
	}
}

// End-to-end cover for the store a child moves under kae's feet, which is the one
// gap both review rounds named: everything else about it is unit-tested per
// function, and the defect that shipped was in the *wiring*.
//
// codex under `cli_auth_credentials_store = "auto"` reads its keychain item first
// and auth.json only when the item is absent, and its first save creates the item
// and deletes the file — so a login flow moves the store. On the specs resolved
// before the child, all three post-child steps read the store codex abandoned: the
// comparison sees no change, the capture sees no credential, and the restore writes
// a file nothing reads while reporting success.
func TestLoginRestoreFollowsAStoreTheChildMoved(t *testing.T) {
	app := testApp(t, nil)
	app.Env.GOOS = "darwin"
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	authPath := filepath.Join(app.Env.Home, ".codex", "auth.json")
	writeFile(t, filepath.Join(app.Env.Home, ".codex", "config.toml"),
		"cli_auth_credentials_store = \"auto\"\n")
	const previous = `{"tokens":{"access_token":"previous-access","account_id":"acct-main"}}`
	const loggedIn = `{"tokens":{"access_token":"new-access","account_id":"acct-side"}}`
	writeFile(t, authPath, previous)

	// No keychain item yet, so `auto` resolves to auth.json and the backup records it.
	sim := &keychainSim{}
	withInteractive(t, func(context.Context, []string, string, ...string) (int, error) {
		// codex's first save under `auto`: write the item, delete the file.
		sim.present, sim.payload = true, loggedIn
		if err := os.Remove(authPath); err != nil {
			t.Fatal(err)
		}
		return 0, nil
	})

	var code int
	var out string
	runner.With(sim, func() {
		code, out = captureStdout(t, func() int { return runLogin(ctx, app, opts, "codex", "side", true) })
	})
	mustExit(t, constants.ExitOK, code, out)

	// The login was captured from the item the child created, not reported as
	// "auth unchanged" against the file it deleted.
	meta := readFile(t, filepath.Join(app.Paths.AccountDir("codex", "side"), "account.toml"))
	if !strings.Contains(meta, constants.KindKeychain) {
		t.Fatalf("the snapshot must record the store the child moved to: %s", meta)
	}
	// --restore put the previous credential back **into the item codex now reads**,
	// not into the abandoned file.
	if sim.payload != previous {
		t.Fatalf("keychain item = %s, want the previous credential restored into it", sim.payload)
	}
	if _, err := os.Stat(authPath); err == nil {
		t.Fatal("the restore must not resurrect the store codex abandoned")
	}
}

func TestLoginUnsupportedTool(t *testing.T) {
	app := testApp(t, nil)
	code, out := captureStdout(t, func() int {
		return runLogin(context.Background(), app, commonOpts{Format: formatText}, "agy", "main", false)
	})
	mustExit(t, constants.ExitUnsupported, code, out)
}

func TestMiseInitPrintAndWrite(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	workDir := t.TempDir()
	if err := os.Chdir(workDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(cwd) })

	// no profile anywhere -> usage error
	code, out := captureStdout(t, func() int { return runMiseInit(ctx, app, opts, "", constants.ModeAuth, false, false) })
	mustExit(t, constants.ExitUsage, code, out)

	code, out = captureStdout(t, func() int { return runMiseInit(ctx, app, opts, "main", constants.ModeAuth, false, false) })
	mustExit(t, constants.ExitOK, code, out)
	for _, want := range []string{`KAE_PROFILE = "main"`, "[tasks.ai-use]", "kae run claude $KAE_PROFILE -- claude"} {
		if !strings.Contains(out, want) {
			t.Fatalf("print missing %q: %s", want, out)
		}
	}
	if !strings.Contains(out, "[tasks.agy]") {
		t.Fatalf("agy task must be rendered since v0.6.0: %s", out)
	}
	// cursor's CLI binary is cursor-agent, not the tool id.
	if !strings.Contains(out, "kae run cursor $KAE_PROFILE -- cursor-agent") {
		t.Fatalf("cursor task must invoke the cursor-agent binary: %s", out)
	}
	if _, err := os.Stat(".mise.toml"); !os.IsNotExist(err) {
		t.Fatal("print must not write")
	}

	code, out = captureStdout(t, func() int { return runMiseInit(ctx, app, opts, "main", constants.ModeAuth, false, true) })
	mustExit(t, constants.ExitOK, code, out)
	first := readFile(t, ".mise.toml")
	if !strings.Contains(first, miseBlockStart) || !strings.Contains(first, `KAE_PROFILE = "main"`) {
		t.Fatalf("write content: %s", first)
	}

	// rewrite with another profile replaces the block in place
	code, out = captureStdout(t, func() int { return runMiseInit(ctx, app, opts, "side", constants.ModeAuth, false, true) })
	mustExit(t, constants.ExitOK, code, out)
	second := readFile(t, ".mise.toml")
	if strings.Contains(second, `"main"`) || !strings.Contains(second, `KAE_PROFILE = "side"`) {
		t.Fatalf("block not replaced: %s", second)
	}
	if strings.Count(second, miseBlockStart) != 1 {
		t.Fatalf("duplicated block: %s", second)
	}

	// an existing file without markers is refused
	writeFile(t, ".mise.toml", "[tasks.custom]\nrun = \"echo hi\"\n")
	code, out = captureStdout(t, func() int { return runMiseInit(ctx, app, opts, "main", constants.ModeAuth, false, true) })
	mustExit(t, constants.ExitUnsafeRefused, code, out)
	if !strings.Contains(readFile(t, ".mise.toml"), "tasks.custom") {
		t.Fatal("unmarked file must not be modified")
	}
}

// `run -s`'s own recapture goes through the two guards the switch-away recapture
// applies, and no third (docs/ROADMAP.md names them). Before v0.17.0 it called
// captureSnapshot directly, so all three tests below described the shipped
// behaviour rather than refusing it.
//
// The healthy case is first on purpose: it is the positive control that makes the
// two refusals mean something. Without it, a recapture that simply never ran would
// satisfy every "the snapshot was not poisoned" assertion below.
func TestRunSharedRecapturesAndKeepsTheRecordedIdentity(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	seedClaude(t, app, mainToken, "main")
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "main") })

	const rotated = "sk-ant-oat01-ROTATED-IN-CHILD"
	withInteractive(t, func(context.Context, []string, string, ...string) (int, error) {
		seedClaude(t, app, rotated, "main") // an ordinary in-tool refresh
		return 0, nil
	})
	code, out := captureStdout(t, func() int {
		return runRun(ctx, app, opts, runModeShared, "claude", "main", []string{"claude"})
	})
	mustExit(t, constants.ExitOK, code, out)

	be := testBackend(t, app)
	if got := snapshotPayload(t, app, be, "claude", "main"); !strings.Contains(got, rotated) {
		t.Fatalf("the child's refreshed credential was not recaptured: %s", got)
	}
	// persistSnapshot writes plan.Identity, which the run paths never set — so this is
	// the assertion that fails if the recapture stops carrying the snapshot's own.
	if id := recordedIdentity(t, app, "claude", "main"); id != "main@example.com" {
		t.Fatalf("the recapture blanked the recorded identity: %q", id)
	}
}

// The child ran the tool's own login flow and landed on somebody else's account.
// Filing that credential under the target's name is undetectable afterwards — the
// token is opaque, so live, snapshot and doctor would all agree on a wrong label.
func TestRunSharedRefusesToRecaptureAForeignLogin(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	seedClaude(t, app, mainToken, "main")
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "main") })

	const foreign = "sk-ant-oat01-FOREIGN-LOGIN"
	withInteractive(t, func(context.Context, []string, string, ...string) (int, error) {
		seedClaude(t, app, foreign, "stranger")
		return 0, nil
	})
	code, out, stderr := captureBoth(t, func() int {
		return runRun(ctx, app, opts, runModeShared, "claude", "main", []string{"claude"})
	})
	mustExit(t, constants.ExitOK, code, out)
	// Naming the pair is part of the assertion: with the two arguments swapped the
	// warning reads "for main/claude", which names no snapshot at all.
	if !strings.Contains(stderr, "the live claude identity is not the one kae applied for claude/main") {
		t.Errorf("expected a warning naming claude/main whose live login is not the one kae applied: %q", stderr)
	}
	if !strings.Contains(stderr, "kae add --no-login claude") {
		t.Errorf("the refusal must say how to keep that live login: %q", stderr)
	}
	for _, pii := range []string{foreign, "stranger", "@example.com"} {
		if strings.Contains(stderr, pii) {
			t.Errorf("identity/credential value %q must not reach stderr: %q", pii, stderr)
		}
	}

	be := testBackend(t, app)
	if got := snapshotPayload(t, app, be, "claude", "main"); strings.Contains(got, foreign) {
		t.Fatalf("the stranger's credential was filed under claude/main: %s", got)
	}
	if got := snapshotArtifact(t, app, be, "claude", "main", "oauth_account"); strings.Contains(got, "stranger") {
		t.Fatalf("the stranger's identity was filed under claude/main: %s", got)
	}
	if id := recordedIdentity(t, app, "claude", "main"); id != "main@example.com" {
		t.Fatalf("recorded identity changed: %q", id)
	}
}

// A refresh failed inside the child, so claude left a fully-formed blank payload.
// readLiveValues proves only that the artifact exists, so without the downgrade
// refusal the tombstone overwrites a snapshot that still works — irrecoverably,
// since the backup this run took reads the same dead store.
func TestRunSharedRefusesToRecaptureATombstone(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	const good = `{"accessToken":"main-live","refreshToken":"r","expiresAt":1800000000000}`
	seedClaudeOAuth(t, app, good)
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "main") })

	withInteractive(t, func(context.Context, []string, string, ...string) (int, error) {
		seedClaudeOAuth(t, app, `{"accessToken":"","refreshToken":"","expiresAt":0}`)
		return 0, nil
	})
	code, out, stderr := captureBoth(t, func() int {
		return runRun(ctx, app, opts, runModeShared, "claude", "main", []string{"claude"})
	})
	mustExit(t, constants.ExitOK, code, out)
	if !strings.Contains(stderr, "needs a re-login") {
		t.Errorf("expected a warning that the live credential is dead: %q", stderr)
	}
	be := testBackend(t, app)
	if got := snapshotPayload(t, app, be, "claude", "main"); !strings.Contains(got, "main-live") {
		t.Fatalf("the tombstone overwrote a usable snapshot: %s", got)
	}
}

// A refusal on this path is only safe because kae backs the declined copy up first.
// `meta` predates the child, so the child's credential lives in the live store alone
// until then — and the restore below overwrites it. Refusing without the backup was a
// logout reported as success (measured against bf77135, where the unguarded recapture
// happened to keep the copy in the snapshot instead).
func TestRunSharedPreservesALoginItDeclinesToAdopt(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	seedClaude(t, app, mainToken, "main")
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "main") })

	const foreign = "sk-ant-oat01-FOREIGN-LOGIN"
	withInteractive(t, func(context.Context, []string, string, ...string) (int, error) {
		seedClaude(t, app, foreign, "stranger")
		return 0, nil
	})
	code, out, stderr := captureBoth(t, func() int {
		return runRun(ctx, app, opts, runModeShared, "claude", "main", []string{"claude"})
	})
	mustExit(t, constants.ExitOK, code, out)

	id := backupIDFromWarning(t, stderr)
	// The message is worthless unless the id it names is a backup that really holds the
	// copy, so read the object rather than trusting the sentence. It is the newest one:
	// the pre-child backup was taken before this and the refusal's after it.
	meta, found, err := backup.Latest(app.Paths.BackupsDir())
	if err != nil || !found {
		t.Fatalf("no backup written: found=%v err=%v", found, err)
	}
	if meta.ID != id {
		t.Fatalf("the refusal named backup %s but the newest is %s", id, meta.ID)
	}
	be := testBackend(t, app)
	held := ""
	for _, rec := range meta.Artifacts {
		if rec.Name != "claude_ai_oauth" {
			continue
		}
		data, ok, err := be.Get(ctx, rec.SecretRef)
		if err != nil || !ok {
			t.Fatalf("backup payload %s unreadable: ok=%v err=%v", rec.SecretRef, ok, err)
		}
		held = string(data)
	}
	if !strings.Contains(held, foreign) {
		t.Fatalf("backup %s does not hold the login kae declined to adopt: %s", id, held)
	}
	if strings.Contains(stderr, "could not preserve") {
		t.Errorf("the backup succeeded, so kae must not say it could not preserve it: %q", stderr)
	}
}

// backupIDFromWarning extracts the id out of the refusal's second line, which is the
// only place it appears.
func backupIDFromWarning(t *testing.T, stderr string) string {
	t.Helper()
	const marker = "preserved only in backup "
	i := strings.Index(stderr, marker)
	if i < 0 {
		t.Fatalf("no backup named in the refusal: %q", stderr)
	}
	rest := stderr[i+len(marker):]
	if j := strings.IndexAny(rest, " \n"); j >= 0 {
		return rest[:j]
	}
	t.Fatalf("could not read the backup id out of %q", stderr)
	return ""
}

// A child that logged out must not erase the snapshot. The arm that stops it was
// rewritten in this diff (from captureSnapshot's ExitAuthMissing to `!anyPresent`) and
// had no test: for claude the downgrade guard converges on the same answer, so a
// mutation removing it survives — the tool that shows it is one whose rotation is not
// measured, where nothing else declines and persistSnapshot deletes the credential ref.
// codex is that tool.
func TestRunSharedLoggedOutChildKeepsTheSnapshot(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	seedCodex(t, app, "codex-main-token")
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "codex", "main") })
	seedCodex(t, app, "codex-side-token")
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "codex", "side") })
	captureStdout(t, func() int { return runSwitch(ctx, app, opts, "codex", "side") })

	withInteractive(t, func(context.Context, []string, string, ...string) (int, error) {
		if err := os.Remove(filepath.Join(app.Env.Home, ".codex", "auth.json")); err != nil {
			t.Fatal(err)
		}
		return 0, nil
	})
	code, out, stderr := captureBoth(t, func() int {
		return runRun(ctx, app, opts, runModeShared, "codex", "main", []string{"codex"})
	})
	mustExit(t, constants.ExitOK, code, out)
	if !strings.Contains(stderr, "logged out during the run") {
		t.Errorf("expected the logged-out warning: %q", stderr)
	}
	if got := snapshotPayload(t, app, testBackend(t, app), "codex", "main"); !strings.Contains(got, "codex-main-token") {
		t.Errorf("a logged-out child erased the snapshot: %s", got)
	}
}

// The logged-out warning carries the adapter's own warnings. captureSnapshot used to
// append them to its auth_missing error and `run -s` prints them nowhere else, so
// without this an env_conflict — a plausible explanation for the logout — is invisible
// in exactly the case it explains.
func TestRunSharedLoggedOutWarningCarriesAdapterWarnings(t *testing.T) {
	// An env_conflict variable: it warns without moving any of kae's stores, so the
	// fixture still resolves. A relative CLAUDE_CONFIG_DIR would warn too, but it also
	// moves the store, so the account could not be captured at all.
	app := testApp(t, map[string]string{"ANTHROPIC_API_KEY": "sk-test-123"})
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	seedClaude(t, app, mainToken, "main")
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "main") })

	withInteractive(t, func(context.Context, []string, string, ...string) (int, error) {
		if err := os.Remove(filepath.Join(app.Env.Home, ".claude", ".credentials.json")); err != nil {
			t.Fatal(err)
		}
		return 0, nil
	})
	code, out, stderr := captureBoth(t, func() int {
		return runRun(ctx, app, opts, runModeShared, "claude", "main", []string{"claude"})
	})
	mustExit(t, constants.ExitOK, code, out)
	if !strings.Contains(stderr, "logged out during the run") {
		t.Fatalf("expected the logged-out warning: %q", stderr)
	}
	if !strings.Contains(stderr, "ANTHROPIC_API_KEY") {
		t.Errorf("the logged-out warning must carry the adapter's warnings: %q", stderr)
	}
	if strings.Contains(stderr, "sk-test-123") {
		t.Errorf("a warning must name the variable, never its value: %q", stderr)
	}
}

// A refusal that cannot name a backup must say the copy is lost rather than imply it
// survives. Unreachable through a command (createBackup would have to fail), so it is
// asserted on the helper directly — the alternative is an untested claim about a
// credential being destroyed.
func TestRecaptureRefusalWithNoBackupSaysTheCopyIsLost(t *testing.T) {
	_, stderr := captureStderr(t, func() int {
		warnRecaptureIdentityUnconfirmed("claude", "kae cannot tell", "")
		return 0
	})
	if !strings.Contains(stderr, "could not preserve") || !strings.Contains(stderr, "lost once") {
		t.Errorf("want a warning that the declined login is lost: %q", stderr)
	}
	if strings.Contains(stderr, "preserved only in backup") {
		t.Errorf("with no backup, kae must not claim the copy survives: %q", stderr)
	}
	_, withID := captureStderr(t, func() int {
		warnRecaptureIdentityUnconfirmed("claude", "kae cannot tell", "20260101T000000Z")
		return 0
	})
	if strings.Contains(withID, "could not preserve") {
		t.Errorf("with a backup, kae must not say it could not preserve it: %q", withID)
	}
}
