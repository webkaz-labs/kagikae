package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/config"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/paths"
)

// overlayTestApp (pin_test.go) defines profile "main" = {claude:main, agy:main}
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
		return runPin(context.Background(), app, commonOpts{Format: formatText}, "main", modeShared)
	})
	mustExit(t, constants.ExitOK, code, out)

	frag := readFile(t, fragmentRelPath)
	for _, want := range []string{
		"# kae:profile=main",
		"# kae:mode=shared",
		"# kae:account:claude=main",
		`KAE_PROFILE = "main"`,
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
	if code := runPin(context.Background(), app, commonOpts{Format: formatText}, "main", modeShared); code != constants.ExitOK {
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
		return runPin(context.Background(), app, commonOpts{Format: formatText}, "main", modeIsolated)
	})
	mustExit(t, constants.ExitOK, code, out)

	frag := readFile(t, fragmentRelPath)
	if !strings.Contains(frag, "# kae:mode=isolated") {
		t.Fatalf("fragment must record isolated mode:\n%s", frag)
	}
	isoDir := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "main")
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
		return runPin(context.Background(), app, commonOpts{Format: formatText}, "main", modeShared)
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

	if code := runPin(ctx, app, opts, "main", modeIsolated); code != constants.ExitOK {
		t.Fatalf("runPin isolated exit %d", code)
	}
	// Re-bind claude to a different account; only claude changes.
	code, out := captureStdout(t, func() int { return runRebind(ctx, app, opts, "claude", "beta") })
	mustExit(t, constants.ExitOK, code, out)

	frag := readFile(t, fragmentRelPath)
	if !strings.Contains(frag, "# kae:account:claude=beta") {
		t.Fatalf("account record not updated:\n%s", frag)
	}
	newDir := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "beta")
	if !strings.Contains(frag, `CLAUDE_CONFIG_DIR = "`+newDir+`"`) {
		t.Fatalf("env entry not repointed to beta:\n%s", frag)
	}
	// The new account set matches no named profile → KAE_PROFILE goes ad-hoc.
	if !strings.Contains(frag, `KAE_PROFILE = ""`) {
		t.Fatalf("KAE_PROFILE env entry must recompute to empty:\n%s", frag)
	}
	if !strings.Contains(frag, fragProfilePrefix+"\n") {
		t.Fatalf("# kae:profile= record must recompute to empty:\n%s", frag)
	}
}

// rebindCompanionApp binds claude (isolatable) across two profiles that carry
// companions: "main" with a git identity, "side" with a different git identity
// plus a gh token (so re-binding exercises both the git-config file repoint and
// the token redaction). A pre-existing ~/.gitconfig satisfies the [include].
func rebindCompanionApp(t *testing.T) *App {
	t.Helper()
	app := testApp(t, nil)
	app.Config.Profiles = map[string]config.Profile{
		"main": {Accounts: map[string]string{constants.ToolClaude: "main"}, Companions: map[string]config.CompanionData{
			constants.CompanionGit: {"email": "you@example.com", "name": "Main", "signingkey": ""},
		}},
		"side": {Accounts: map[string]string{constants.ToolClaude: "side"}, Companions: map[string]config.CompanionData{
			constants.CompanionGit: {"email": "side@example.com", "name": "Side", "signingkey": ""},
			constants.CompanionGH:  {"GH_TOKEN": ""},
		}},
	}
	writeFile(t, filepath.Join(app.Env.Home, ".gitconfig"), "[alias]\n\tlol = log --oneline\n")
	return app
}

func TestPinRebindRepointsCompanionsToNewProfile(t *testing.T) {
	app := rebindCompanionApp(t)
	chdirTemp(t)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	if code := runPin(ctx, app, opts, "main", modeIsolated); code != constants.ExitOK {
		t.Fatalf("runPin main exit %d", code)
	}
	mainGit := app.Paths.CompanionConfigFile("main", constants.CompanionGit)
	if frag := readFile(t, fragmentRelPath); !strings.Contains(frag, `GIT_CONFIG_GLOBAL = "`+mainGit+`"`) {
		t.Fatalf("pin must bind main's git config:\n%s", frag)
	}

	// Re-bind claude main→side: the account set now matches profile "side", so
	// its companion block must replace main's.
	code, out := captureStdout(t, func() int { return runRebind(ctx, app, opts, "claude", "side") })
	mustExit(t, constants.ExitOK, code, out)

	frag := readFile(t, fragmentRelPath)
	sideGit := app.Paths.CompanionConfigFile("side", constants.CompanionGit)
	if !strings.Contains(frag, `GIT_CONFIG_GLOBAL = "`+sideGit+`"`) {
		t.Fatalf("git companion not repointed to side:\n%s", frag)
	}
	if strings.Contains(frag, mainGit) {
		t.Fatalf("stale main git companion must be gone:\n%s", frag)
	}
	if !strings.Contains(frag, `KAE_PROFILE = "side"`) {
		t.Fatalf("KAE_PROFILE must recompute to side:\n%s", frag)
	}
	// side adds a gh token, so its exec() line and redaction must appear.
	if !strings.Contains(frag, `GH_TOKEN = "{{ exec(command=`) {
		t.Fatalf("side's gh token companion must be bound:\n%s", frag)
	}
	if !strings.Contains(frag, `redactions = ["GH_TOKEN"]`) {
		t.Fatalf("token redaction must be present and precede [env]:\n%s", frag)
	}
	// side's generated git file must [include] the home gitconfig and carry the
	// new identity (the prepare step ran).
	if got := readFile(t, sideGit); !strings.Contains(got, "email = side@example.com") {
		t.Fatalf("side git config not regenerated:\n%s", got)
	}
}

func TestPinRebindToAdHocClearsCompanions(t *testing.T) {
	app := rebindCompanionApp(t)
	chdirTemp(t)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	if code := runPin(ctx, app, opts, "main", modeIsolated); code != constants.ExitOK {
		t.Fatalf("runPin main exit %d", code)
	}
	// Re-bind claude to an account in no profile → ad-hoc; companions clear.
	code, out := captureStdout(t, func() int { return runRebind(ctx, app, opts, "claude", "zeta") })
	mustExit(t, constants.ExitOK, code, out)

	frag := readFile(t, fragmentRelPath)
	if strings.Contains(frag, "GIT_CONFIG_GLOBAL") {
		t.Fatalf("ad-hoc re-bind must drop the git companion:\n%s", frag)
	}
	if strings.Contains(frag, "redactions = [") {
		t.Fatalf("ad-hoc re-bind must drop redactions:\n%s", frag)
	}
	if !strings.Contains(frag, `KAE_PROFILE = ""`) {
		t.Fatalf("KAE_PROFILE must go empty for ad-hoc:\n%s", frag)
	}
	// The fragment must remain valid TOML: claude's isolation entry survives.
	if !strings.Contains(frag, "CLAUDE_CONFIG_DIR = ") {
		t.Fatalf("isolation entry must be preserved:\n%s", frag)
	}
}

func TestApplyCompanionSectionRequiresEnvBlock(t *testing.T) {
	corrupt := []string{"# header", "# kae:profile=main", ""}
	// A companion section with no [env] anchor is a corrupt fragment: fail loud
	// rather than float a token line outside [env] where mise would drop it.
	if _, err := applyCompanionSection(corrupt, []string{`GH_TOKEN = "x"`}, []string{"GH_TOKEN"}); err == nil {
		t.Fatal("expected an error when [env] is absent but a companion section must be placed")
	}
	// With nothing to place (an ad-hoc clear), a missing [env] is tolerated.
	out, err := applyCompanionSection(corrupt, nil, nil)
	if err != nil {
		t.Fatalf("ad-hoc clear must not require [env]: %v", err)
	}
	if n := len(out); n == 0 || out[n-1] != "" {
		t.Fatalf("must restore the trailing newline: %v", out)
	}
}

func TestPinRebindRefusesUnboundTool(t *testing.T) {
	app := overlayTestApp(t)
	chdirTemp(t)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	if code := runPin(ctx, app, opts, "main", modeIsolated); code != constants.ExitOK {
		t.Fatalf("runPin exit %d", code)
	}
	// codex is not bound in this directory (the profile binds only claude).
	code, out := captureStdout(t, func() int { return runRebind(ctx, app, opts, "codex", "main") })
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
	if code := runPin(ctx, app, commonOpts{Format: formatText}, "main", modeShared); code != constants.ExitOK {
		t.Fatalf("runPin exit %d", code)
	}
	// Simulate a mise-active shell: the fragment's [env] is exported.
	sharedDir := app.Paths.SharedDir(pinID, constants.ToolClaude)
	app.Env.Getenv = func(key string) string {
		switch key {
		case constants.EnvKaeProfile:
			return "main"
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
	if claudeAccount == nil || *claudeAccount != "main" {
		t.Fatalf("status must report claude's bound account from the fragment, got %v", claudeAccount)
	}
}

func TestKaeManagedHomeKindClassifiesSegments(t *testing.T) {
	app := testApp(t, nil)
	pinID := "abcdef0123456789"
	if got := app.kaeManagedHomeKind(app.Paths.SharedDir(pinID, constants.ToolClaude)); got != modeShared {
		t.Fatalf("shared segment must classify as shared, got %q", got)
	}
	if got := app.kaeManagedHomeKind(app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "main")); got != modeIsolated {
		t.Fatalf("isolated segment must classify as isolated, got %q", got)
	}
	if got := app.kaeManagedHomeKind(app.Paths.GlobalIsolatedHomeDir(constants.ToolClaude, "main")); got != constants.ModeSync {
		t.Fatalf("global segment must classify as sync, got %q", got)
	}
}

func TestUnpinDeletesFragment(t *testing.T) {
	app := overlayTestApp(t)
	chdirTemp(t)
	if code := runPin(context.Background(), app, commonOpts{Format: formatText}, "main", modeShared); code != constants.ExitOK {
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
