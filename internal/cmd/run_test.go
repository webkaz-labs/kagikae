package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/config"
	"github.com/webkaz-labs/kagikae/internal/constants"
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
	kaeArgs, child := splitAtDashDash([]string{"claude", "work", "--", "claude", "-p", "hi"})
	if strings.Join(kaeArgs, " ") != "claude work" || strings.Join(child, " ") != "claude -p hi" {
		t.Fatalf("unexpected: %v %v", kaeArgs, child)
	}
	_, child = splitAtDashDash([]string{"claude", "work"})
	if child != nil {
		t.Fatalf("expected nil child: %v", child)
	}
}

func TestRunAuthTransaction(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	credsPath := filepath.Join(app.Env.Home, ".claude", ".credentials.json")

	seedClaude(t, app, workToken, "work-uuid")
	code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "work") })
	mustExit(t, constants.ExitOK, code, out)
	seedClaude(t, app, personalToken, "personal-uuid")
	code, out = captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "personal") })
	mustExit(t, constants.ExitOK, code, out)

	const refreshedToken = "sk-ant-oat01-REFRESHED-cccc"
	ranChild := false
	withInteractive(t, func(_ context.Context, extraEnv []string, name string, args ...string) (int, error) {
		ranChild = true
		if name != "claude" {
			t.Fatalf("unexpected child: %s %v", name, args)
		}
		// During the run the live state must be the work account.
		live := readFile(t, credsPath)
		if !strings.Contains(live, workToken) {
			t.Fatalf("work account not applied during run: %s", live)
		}
		// Simulate an OAuth refresh by the child.
		writeFile(t, credsPath, strings.Replace(live, workToken, refreshedToken, 1))
		return 7, nil
	})

	code, _ = captureStdout(t, func() int {
		return runRun(ctx, app, opts, modeAuth, "claude", "work", []string{"claude"})
	})
	if !ranChild {
		t.Fatal("child did not run")
	}
	if code != 7 {
		t.Fatalf("child exit code not propagated: %d", code)
	}
	// Previous (personal) live state restored.
	if live := readFile(t, credsPath); !strings.Contains(live, personalToken) {
		t.Fatalf("previous state not restored: %s", live)
	}
	// Refreshed credential recaptured into the work snapshot.
	code, out = captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "work") })
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
		return runRun(ctx, app, commonOpts{Format: formatText}, modeAuth, "claude", "nope", []string{"claude"})
	})
	mustExit(t, constants.ExitNotFound, code, out)
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
		return runRun(ctx, app, opts, modeEnv, "claude", "ci", []string{"claude", "-p", "x"})
	})
	mustExit(t, constants.ExitOK, code, out)
	if len(gotEnv) != 1 || gotEnv[0] != "ANTHROPIC_API_KEY=sk-test-123" {
		t.Fatalf("env not injected: %v", gotEnv)
	}

	// missing profile
	code, out = captureStdout(t, func() int {
		return runRun(ctx, app, opts, modeEnv, "codex", "ci", []string{"codex"})
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
		return runEnvSet(ctx, app, opts, []string{"gemini", "ci", "GEMINI_API_KEY=g-1", "GOOGLE_CLOUD_PROJECT=p-1"})
	})
	mustExit(t, constants.ExitOK, code, out)
	code, out = captureStdout(t, func() int { return runEnvList(ctx, app, commonOpts{Format: formatJSON}) })
	mustExit(t, constants.ExitOK, code, out)
	if !strings.Contains(out, "GEMINI_API_KEY") || strings.Contains(out, "g-1") {
		t.Fatalf("list must show names, never values: %s", out)
	}
	code, out = captureStdout(t, func() int {
		return runEnvUnset(ctx, app, opts, []string{"gemini", "ci", "GEMINI_API_KEY"})
	})
	mustExit(t, constants.ExitOK, code, out)
	code, out = captureStdout(t, func() int {
		return runEnvUnset(ctx, app, opts, []string{"gemini", "ci"})
	})
	mustExit(t, constants.ExitOK, code, out)
	code, out = captureStdout(t, func() int {
		return runEnvUnset(ctx, app, opts, []string{"gemini", "ci"})
	})
	mustExit(t, constants.ExitNotFound, code, out)
}

func TestRunHomeMode(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	var gotEnv []string
	withInteractive(t, func(_ context.Context, extraEnv []string, _ string, _ ...string) (int, error) {
		gotEnv = extraEnv
		return 0, nil
	})
	code, out := captureStdout(t, func() int {
		return runRun(ctx, app, opts, modeHome, "claude", "work", []string{"claude"})
	})
	mustExit(t, constants.ExitOK, code, out)
	wantDir := app.Paths.HomeModeDir("claude", "work")
	if len(gotEnv) != 1 || gotEnv[0] != "CLAUDE_CONFIG_DIR="+wantDir {
		t.Fatalf("home env: %v", gotEnv)
	}
	if info, err := os.Stat(wantDir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("home dir: %v %v", info, err)
	}

	// gemini has no stable isolation env var
	code, out = captureStdout(t, func() int {
		return runRun(ctx, app, opts, modeHome, "gemini", "work", []string{"gemini"})
	})
	mustExit(t, constants.ExitUnsupported, code, out)

	// config can disable home mode
	disabled := false
	app.Config.Tools["claude"] = config.Tool{HomeModeEnabled: &disabled}
	code, out = captureStdout(t, func() int {
		return runRun(ctx, app, opts, modeHome, "claude", "work", []string{"claude"})
	})
	mustExit(t, constants.ExitUnsupported, code, out)
}

func TestRunOverlayMode(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	withInteractive(t, func(context.Context, []string, string, ...string) (int, error) { return 0, nil })

	// on by default since v0.5.0; the per-tool opt-out still refuses
	disabled := false
	app.Config.Tools["claude"] = config.Tool{OverlayModeEnabled: &disabled}
	code, out := captureStdout(t, func() int {
		return runRun(ctx, app, opts, modeOverlay, "claude", "work", []string{"claude"})
	})
	mustExit(t, constants.ExitUnsupported, code, out)

	app.Config.Tools["claude"] = config.Tool{}
	writeFile(t, filepath.Join(app.Env.Home, ".claude", "settings.json"), `{"theme":"dark"}`)
	if err := os.MkdirAll(filepath.Join(app.Env.Home, ".claude", "skills", "demo"), 0o700); err != nil {
		t.Fatal(err)
	}

	code, out = captureStdout(t, func() int {
		return runRun(ctx, app, opts, modeOverlay, "claude", "work", []string{"claude"})
	})
	mustExit(t, constants.ExitOK, code, out)
	overlayDir := app.Paths.OverlayDir("claude", "work")
	link, err := os.Readlink(filepath.Join(overlayDir, "settings.json"))
	if err != nil || link != filepath.Join(app.Env.Home, ".claude", "settings.json") {
		t.Fatalf("settings symlink: %q %v", link, err)
	}
	if _, err := os.Readlink(filepath.Join(overlayDir, "skills")); err != nil {
		t.Fatalf("skills symlink: %v", err)
	}
	// CLAUDE.md does not exist in the real home, so it must not be linked
	if _, err := os.Lstat(filepath.Join(overlayDir, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Fatal("non-existent shared item must not be linked")
	}

	// idempotent second run
	code, out = captureStdout(t, func() int {
		return runRun(ctx, app, opts, modeOverlay, "claude", "work", []string{"claude"})
	})
	mustExit(t, constants.ExitOK, code, out)

	// a real file where a link belongs is refused, not destroyed
	if err := os.Remove(filepath.Join(overlayDir, "settings.json")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(overlayDir, "settings.json"), `{"private":true}`)
	code, out = captureStdout(t, func() int {
		return runRun(ctx, app, opts, modeOverlay, "claude", "work", []string{"claude"})
	})
	mustExit(t, constants.ExitUnsafeRefused, code, out)
	if got := readFile(t, filepath.Join(overlayDir, "settings.json")); !strings.Contains(got, "private") {
		t.Fatal("refusal must not destroy overlay-local data")
	}
}

func TestLoginCapturesAndRestores(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	credsPath := filepath.Join(app.Env.Home, ".claude", ".credentials.json")
	seedClaude(t, app, personalToken, "personal-uuid")

	const newToken = "sk-ant-oat01-NEWLOGIN-dddd"
	withInteractive(t, func(_ context.Context, _ []string, name string, args ...string) (int, error) {
		if name != "claude" || len(args) != 1 || args[0] != "/login" {
			t.Fatalf("unexpected login command: %s %v", name, args)
		}
		seedClaude(t, app, newToken, "work-uuid")
		return 0, nil
	})

	// --restore: capture the new login but put the previous one back
	code, out := captureStdout(t, func() int { return runLogin(ctx, app, opts, "claude", "work", true) })
	mustExit(t, constants.ExitOK, code, out)
	if !strings.Contains(out, "restored the previous login") {
		t.Fatalf("unexpected output: %s", out)
	}
	if live := readFile(t, credsPath); !strings.Contains(live, personalToken) {
		t.Fatalf("previous login not restored: %s", live)
	}
	// captured snapshot is applyable
	code, out = captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "work") })
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
	seedClaude(t, app, personalToken, "personal-uuid")

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
	code, out := captureStdout(t, func() int { return runLogin(ctx, app, opts, "claude", "work", true) })
	mustExit(t, constants.ExitAuthMissing, code, out)
	if live := readFile(t, credsPath); !strings.Contains(live, personalToken) {
		t.Fatalf("--restore must put the previous login back even when capture fails: %s", live)
	}
}

func TestOverlayDirSymlinkRefused(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	enabled := true
	app.Config.Tools["claude"] = config.Tool{OverlayModeEnabled: &enabled}
	withInteractive(t, func(context.Context, []string, string, ...string) (int, error) { return 0, nil })

	elsewhere := t.TempDir()
	overlayDir := app.Paths.OverlayDir("claude", "evil")
	if err := os.MkdirAll(filepath.Dir(overlayDir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, overlayDir); err != nil {
		t.Fatal(err)
	}
	code, out := captureStdout(t, func() int {
		return runRun(ctx, app, opts, modeOverlay, "claude", "evil", []string{"claude"})
	})
	mustExit(t, constants.ExitUnsafeRefused, code, out)
}

func TestLoginUnsupportedTool(t *testing.T) {
	app := testApp(t, nil)
	code, out := captureStdout(t, func() int {
		return runLogin(context.Background(), app, commonOpts{Format: formatText}, "agy", "work", false)
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

	code, out = captureStdout(t, func() int { return runMiseInit(ctx, app, opts, "work", constants.ModeAuth, false, false) })
	mustExit(t, constants.ExitOK, code, out)
	for _, want := range []string{`KAE_PROFILE = "work"`, "[tasks.ai-use]", "kae run claude $KAE_PROFILE -- claude"} {
		if !strings.Contains(out, want) {
			t.Fatalf("print missing %q: %s", want, out)
		}
	}
	if strings.Contains(out, "[tasks.agy]") {
		t.Fatalf("agy task must be omitted: %s", out)
	}
	if _, err := os.Stat(".mise.toml"); !os.IsNotExist(err) {
		t.Fatal("print must not write")
	}

	code, out = captureStdout(t, func() int { return runMiseInit(ctx, app, opts, "work", constants.ModeAuth, false, true) })
	mustExit(t, constants.ExitOK, code, out)
	first := readFile(t, ".mise.toml")
	if !strings.Contains(first, miseBlockStart) || !strings.Contains(first, `KAE_PROFILE = "work"`) {
		t.Fatalf("write content: %s", first)
	}

	// rewrite with another profile replaces the block in place
	code, out = captureStdout(t, func() int { return runMiseInit(ctx, app, opts, "personal", constants.ModeAuth, false, true) })
	mustExit(t, constants.ExitOK, code, out)
	second := readFile(t, ".mise.toml")
	if strings.Contains(second, `"work"`) || !strings.Contains(second, `KAE_PROFILE = "personal"`) {
		t.Fatalf("block not replaced: %s", second)
	}
	if strings.Count(second, miseBlockStart) != 1 {
		t.Fatalf("duplicated block: %s", second)
	}

	// an existing file without markers is refused
	writeFile(t, ".mise.toml", "[tasks.custom]\nrun = \"echo hi\"\n")
	code, out = captureStdout(t, func() int { return runMiseInit(ctx, app, opts, "work", constants.ModeAuth, false, true) })
	mustExit(t, constants.ExitUnsafeRefused, code, out)
	if !strings.Contains(readFile(t, ".mise.toml"), "tasks.custom") {
		t.Fatal("unmarked file must not be modified")
	}
}
