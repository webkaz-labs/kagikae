package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/paths"
)

// overlayTestApp (pin_test.go) defines profile "work" = {claude:work, agy:work}
// with a real ~/.claude. agy has no isolation env var, so it exercises the
// warning path while claude exercises the env-entry path.

func TestRunPinSharedWritesFragmentAndGitignore(t *testing.T) {
	app := overlayTestApp(t)
	chdirTemp(t)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	pinID := paths.PinID(cwd)

	code, out := captureStdout(t, func() int {
		return runPin(context.Background(), app, commonOpts{Format: formatText}, "work", modeBond)
	})
	mustExit(t, constants.ExitOK, code, out)

	frag := readFile(t, fragmentRelPath)
	for _, want := range []string{
		"# kae:profile=work",
		"# kae:mode=shared",
		"# kae:account:claude=work",
		`KAE_PROFILE = "work"`,
		`CLAUDE_CONFIG_DIR = "` + app.Paths.SharedDir(pinID, constants.ToolClaude) + `"`,
		"agy has no stable home-isolation env var",
	} {
		if !strings.Contains(frag, want) {
			t.Fatalf("fragment missing %q:\n%s", want, frag)
		}
	}
	// A tool that keeps the real home (no env var) gets neither an account
	// record nor an env entry — only the warning comment.
	if strings.Contains(frag, "# kae:account:agy=") {
		t.Fatalf("agy must not get an account record:\n%s", frag)
	}

	gi := readFile(t, ".gitignore")
	if !strings.Contains(gi, "/"+filepath.ToSlash(fragmentRelPath)) {
		t.Fatalf(".gitignore missing fragment entry:\n%s", gi)
	}
	// Re-running must not duplicate the .gitignore entry.
	if code := runPin(context.Background(), app, commonOpts{Format: formatText}, "work", modeBond); code != constants.ExitOK {
		t.Fatalf("re-pin exit %d", code)
	}
	gi = readFile(t, ".gitignore")
	if strings.Count(gi, "/"+filepath.ToSlash(fragmentRelPath)) != 1 {
		t.Fatalf(".gitignore entry duplicated on re-pin:\n%s", gi)
	}
}

func TestRunPinIsolatedEncodesAccountInPath(t *testing.T) {
	app := overlayTestApp(t)
	chdirTemp(t)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	pinID := paths.PinID(cwd)

	code, out := captureStdout(t, func() int {
		return runPin(context.Background(), app, commonOpts{Format: formatText}, "work", modePin)
	})
	mustExit(t, constants.ExitOK, code, out)

	frag := readFile(t, fragmentRelPath)
	if !strings.Contains(frag, "# kae:mode=isolated") {
		t.Fatalf("fragment must record isolated mode:\n%s", frag)
	}
	isoDir := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "work")
	if !strings.Contains(frag, `CLAUDE_CONFIG_DIR = "`+isoDir+`"`) {
		t.Fatalf("fragment must point at the isolated config dir:\n%s", frag)
	}
}

func TestRunPinMiseActivatedMessage(t *testing.T) {
	app := overlayTestApp(t)
	app.Env.Getenv = func(key string) string {
		if key == "MISE_SHELL" {
			return "zsh"
		}
		return ""
	}
	chdirTemp(t)
	code, out := captureStdout(t, func() int {
		return runPin(context.Background(), app, commonOpts{Format: formatText}, "work", modeBond)
	})
	mustExit(t, constants.ExitOK, code, out)
	if !strings.Contains(out, "mise applies it on the next prompt") {
		t.Fatalf("expected the mise-activated next-step, got:\n%s", out)
	}
}

func TestPinRebindIsolatedRepointsFragment(t *testing.T) {
	app := overlayTestApp(t)
	chdirTemp(t)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	pinID := paths.PinID(cwd)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	if code := runPin(ctx, app, opts, "work", modePin); code != constants.ExitOK {
		t.Fatalf("runPin isolated exit %d", code)
	}
	// Re-bind claude to a different account; only claude changes.
	code, out := captureStdout(t, func() int { return runRebind(ctx, app, opts, "claude", "clientB") })
	mustExit(t, constants.ExitOK, code, out)

	frag := readFile(t, fragmentRelPath)
	if !strings.Contains(frag, "# kae:account:claude=clientB") {
		t.Fatalf("account record not updated:\n%s", frag)
	}
	newDir := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "clientB")
	if !strings.Contains(frag, `CLAUDE_CONFIG_DIR = "`+newDir+`"`) {
		t.Fatalf("env entry not repointed to clientB:\n%s", frag)
	}
	// The new account set matches no named profile → KAE_PROFILE goes ad-hoc.
	if !strings.Contains(frag, `KAE_PROFILE = ""`) {
		t.Fatalf("KAE_PROFILE env entry must recompute to empty:\n%s", frag)
	}
	if !strings.Contains(frag, fragProfilePrefix+"\n") {
		t.Fatalf("# kae:profile= record must recompute to empty:\n%s", frag)
	}
}

func TestPinRebindRefusesUnboundTool(t *testing.T) {
	app := overlayTestApp(t)
	chdirTemp(t)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	if code := runPin(ctx, app, opts, "work", modePin); code != constants.ExitOK {
		t.Fatalf("runPin exit %d", code)
	}
	// codex is not bound in this directory (the profile binds only claude).
	code, out := captureStdout(t, func() int { return runRebind(ctx, app, opts, "codex", "work") })
	mustExit(t, constants.ExitNotFound, code, out)
}

func TestStatusReportsSharedModeAndBoundAccount(t *testing.T) {
	app := overlayTestApp(t)
	chdirTemp(t)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	pinID := paths.PinID(cwd)
	ctx := context.Background()
	if code := runPin(ctx, app, commonOpts{Format: formatText}, "work", modeBond); code != constants.ExitOK {
		t.Fatalf("runPin exit %d", code)
	}
	// Simulate a mise-active shell: the fragment's [env] is exported.
	sharedDir := app.Paths.SharedDir(pinID, constants.ToolClaude)
	app.Env.Getenv = func(key string) string {
		switch key {
		case constants.EnvKaeProfile:
			return "work"
		case "CLAUDE_CONFIG_DIR":
			return sharedDir
		}
		return ""
	}
	report, err := buildStatus(ctx, app)
	if err != nil {
		t.Fatal(err)
	}
	if report.Pinned == nil || report.Pinned.Mode != paths.SharedSegment {
		t.Fatalf("expected pinned mode shared, got %+v", report.Pinned)
	}
	var claudeAccount *string
	for _, ts := range report.Tools {
		if ts.Tool == constants.ToolClaude {
			claudeAccount = ts.Account
		}
	}
	if claudeAccount == nil || *claudeAccount != "work" {
		t.Fatalf("status must report claude's bound account from the fragment, got %v", claudeAccount)
	}
}

func TestKaeManagedHomeKindClassifiesSegments(t *testing.T) {
	app := testApp(t, nil)
	pinID := "abcdef0123456789"
	if got := app.kaeManagedHomeKind(app.Paths.SharedDir(pinID, constants.ToolClaude)); got != modeBond {
		t.Fatalf("shared segment must classify as bond, got %q", got)
	}
	if got := app.kaeManagedHomeKind(app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "work")); got != modePin {
		t.Fatalf("isolated segment must classify as pin, got %q", got)
	}
	// Pre-v0.7.2 isolated dirs used the "pin" segment; a not-yet-migrated
	// .mise.toml still points there and must classify as pin, not shared.
	legacy := filepath.Join(app.Paths.IsolationDir(), pinID, constants.ToolClaude, "pin", "work", "config")
	if got := app.kaeManagedHomeKind(legacy); got != modePin {
		t.Fatalf("legacy pin segment must classify as pin, got %q", got)
	}
}

func TestUnpinDeletesFragment(t *testing.T) {
	app := overlayTestApp(t)
	chdirTemp(t)
	if code := runPin(context.Background(), app, commonOpts{Format: formatText}, "work", modeBond); code != constants.ExitOK {
		t.Fatalf("runPin exit %d", code)
	}
	if _, err := os.Stat(fragmentRelPath); err != nil {
		t.Fatalf("fragment not written: %v", err)
	}

	code, out := captureStdout(t, func() int { return CmdUnpin(context.Background(), nil) })
	mustExit(t, constants.ExitOK, code, out)
	if _, err := os.Stat(fragmentRelPath); !os.IsNotExist(err) {
		t.Fatal("unpin must delete the fragment")
	}

	// A second unpin with neither a fragment nor a legacy block is not_found.
	code, out = captureStdout(t, func() int { return CmdUnpin(context.Background(), nil) })
	mustExit(t, constants.ExitNotFound, code, out)
}
