package cmd

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/config"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/paths"
	"github.com/webkaz-labs/kagikae/internal/runner"
	"github.com/webkaz-labs/kagikae/internal/testutil/runnertest"
)

// overlayTestApp (pin_test.go) defines profile "main" = {claude:main, agy:main}
// with a real ~/.claude. agy has no isolation env var, so it exercises the
// warning path while claude exercises the env-entry path.

func TestRunPinSharedWritesFragment(t *testing.T) {
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

	// Binding a directory outside a repository must not invent an ignore file —
	// and must not claim one either. The tracked ./.gitignore kae used to write
	// is gone for good; ignoring now happens in the repository's exclude file
	// (ensureGitExcluded).
	if _, err := os.Stat(".gitignore"); !os.IsNotExist(err) {
		t.Fatalf("pin must not write a tracked .gitignore (stat err: %v)", err)
	}
	if strings.Contains(out, "ignored via") {
		t.Fatalf("no repository here, so the report must not claim an ignore rule:\n%s", out)
	}
}

// TestEnsureGitExcludedDerivesTargetAndEntry pins down both halves of the answer
// git gives, because neither is guessable and getting either wrong writes a rule
// that matches nothing: the common dir is cwd-relative in an ordinary checkout
// and absolute in a linked worktree, and the entry is anchored at the repository
// root rather than at the current directory.
func TestEnsureGitExcludedDerivesTargetAndEntry(t *testing.T) {
	frag := filepath.ToSlash(fragmentRelPath)
	// commonDir is what git prints for --git-common-dir, built from the temp cwd
	// so the absolute (linked-worktree) case has a directory it may create.
	// wantCommon is where the rule must land.
	for _, tc := range []struct {
		name       string
		commonDir  func(cwd string) string
		prefix     string
		wantCommon func(cwd string) string
		wantEntry  string
		code       int
	}{
		{
			name:       "repository root",
			commonDir:  func(string) string { return ".git" },
			wantCommon: func(cwd string) string { return filepath.Join(cwd, ".git") },
			wantEntry:  "/" + frag,
		},
		{
			name:       "subdirectory of an ordinary checkout",
			commonDir:  func(string) string { return "../.git" },
			prefix:     "sub/",
			wantCommon: func(cwd string) string { return filepath.Join(filepath.Dir(cwd), ".git") },
			wantEntry:  "/sub/" + frag,
		},
		{
			name:       "subdirectory two deep",
			commonDir:  func(string) string { return "../../.git" },
			prefix:     "a/b/",
			wantCommon: func(cwd string) string { return filepath.Join(filepath.Dir(filepath.Dir(cwd)), ".git") },
			wantEntry:  "/a/b/" + frag,
		},
		{
			// The worktree's own $GIT_DIR is never consulted, so the rule has to
			// land in the absolute common dir shared with the main checkout —
			// which is nowhere near the cwd.
			name:       "linked worktree reports an absolute common dir",
			commonDir:  func(cwd string) string { return filepath.Join(cwd, "elsewhere", "main", ".git") },
			wantCommon: func(cwd string) string { return filepath.Join(cwd, "elsewhere", "main", ".git") },
			wantEntry:  "/" + frag,
		},
		{name: "no repository, or no git to ask", commonDir: func(string) string { return "" }, code: 128},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chdirTemp(t)
			cwd, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			fake := &runnertest.Fake{
				Stdout: tc.commonDir(cwd) + "\n" + tc.prefix + "\n",
				Code:   tc.code,
			}
			var got string
			runner.With(fake, func() {
				got, err = ensureGitExcluded(context.Background(), fragmentRelPath)
			})
			if err != nil {
				t.Fatalf("ensureGitExcluded: %v", err)
			}
			if tc.code != 0 {
				if got != "" {
					t.Fatalf("no repository must yield no exclude file, got %q", got)
				}
				return
			}
			if want := filepath.Join(tc.wantCommon(cwd), "info", "exclude"); got != want {
				t.Fatalf("exclude file: got %q want %q", got, want)
			}
			if !strings.Contains(readFile(t, got), tc.wantEntry+"\n") {
				t.Fatalf("exclude file missing entry %q:\n%s", tc.wantEntry, readFile(t, got))
			}
		})
	}
}

// The exclude file is the user's (and git's), not kae's: kae appends one entry to
// it and must leave everything else — the existing rules, the final-newline state,
// and the file's mode — exactly as found. The mode matters because this file is
// written by append for a reason (see ensureGitExcluded): a temp-file-and-rename
// would silently reset it, and it is shared by every worktree's binding.
func TestEnsureGitExcludedAppendsWithoutDisturbingTheFile(t *testing.T) {
	chdirTemp(t)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	excludeFile := filepath.Join(cwd, ".git", "info", "exclude")
	if err := os.MkdirAll(filepath.Dir(excludeFile), 0o755); err != nil {
		t.Fatal(err)
	}
	// No trailing newline, so a careless append would join onto the last rule.
	const existing = "# the user's own rules\n*.log\nbuild/"
	if err := os.WriteFile(excludeFile, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	fake := &runnertest.Fake{Stdout: ".git\n\n"}
	var file string
	runner.With(fake, func() {
		for range 3 {
			file, err = ensureGitExcluded(context.Background(), fragmentRelPath)
			if err != nil {
				t.Fatalf("ensureGitExcluded: %v", err)
			}
		}
	})
	if file != excludeFile {
		t.Fatalf("exclude file: got %q want %q", file, excludeFile)
	}
	got := readFile(t, file)
	entry := "/" + filepath.ToSlash(fragmentRelPath)
	if n := strings.Count(got, entry); n != 1 {
		t.Fatalf("entry written %d times, want 1:\n%s", n, got)
	}
	if !strings.HasPrefix(got, existing+"\n") {
		t.Fatalf("existing rules must survive verbatim, with the missing newline supplied:\n%q", got)
	}
	if !strings.Contains(got, "\nbuild/\n") {
		t.Fatalf("the last existing rule must not be joined onto kae's comment:\n%q", got)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode changed to %v; kae must not widen the user's exclude file", info.Mode().Perm())
	}
}

// TestEnsureGitExcludedLeavesEveryWorktreeClean is the claim the whole change
// exists for, checked against real git rather than a stub: one entry, recorded
// once from the main checkout, keeps the fragment out of `git status` in the main
// checkout *and* in a linked worktree. The stubbed tests above cannot prove this
// — they encode what git said when it was measured, and this is what re-measures
// it (docs/VALIDATION.md § Upstream Behaviour Assumptions).
func TestEnsureGitExcludedLeavesEveryWorktreeClean(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	main := filepath.Join(root, "main")
	linked := filepath.Join(root, "wt1")
	// -c keeps the identity out of the operator's own config; no hooks, no
	// signing, so a host gitconfig cannot make the commit fail.
	git := func(dir string, args ...string) string {
		t.Helper()
		full := append([]string{
			"-C", dir,
			"-c", "user.email=you@example.com", "-c", "user.name=kae test",
			"-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null",
		}, args...)
		out, err := exec.Command("git", full...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	git(main, "init", "-q")
	writeFile(t, filepath.Join(main, "tracked"), "content\n")
	git(main, "add", "-A")
	git(main, "commit", "-qm", "init")
	git(main, "worktree", "add", "-q", linked, "-b", "side")

	// A fragment in both checkouts, and the rule recorded from the main one only.
	for _, dir := range []string{main, linked} {
		writeFile(t, filepath.Join(dir, fragmentRelPath), "# fragment\n")
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(cwd) })
	if err := os.Chdir(main); err != nil {
		t.Fatal(err)
	}
	file, err := ensureGitExcluded(context.Background(), fragmentRelPath)
	if err != nil {
		t.Fatalf("ensureGitExcluded: %v", err)
	}
	if file == "" {
		t.Fatal("ensureGitExcluded reported no exclude file inside a real repository")
	}
	for _, dir := range []string{main, linked} {
		if status := git(dir, "status", "--porcelain"); status != "" {
			t.Fatalf("%s is not clean after one exclude entry:\n%s", dir, status)
		}
	}
	// The rule must live outside every working tree, or ignoring the fragment
	// would itself be a change to commit.
	for _, dir := range []string{main, linked} {
		if strings.HasPrefix(file, filepath.Join(dir)+string(filepath.Separator)+"info") {
			t.Fatalf("exclude file %s is inside working tree %s", file, dir)
		}
	}
	if status := git(main, "status", "--porcelain", "--ignored=matching",
		"--", fragmentRelPath); !strings.HasPrefix(status, "!!") {
		t.Fatalf("fragment is not reported as ignored, got %q", status)
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

	// Both accounts must be captured: a re-bind names one account explicitly, so
	// an uncaptured one is refused rather than bound without a credential.
	captureClaude(t, app, "main", mainToken)
	captureClaude(t, app, "beta", sideToken)

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
	// Every account a re-bind targets has to be captured; a re-bind names one
	// account, so an uncaptured one is refused instead of bound credential-less.
	captureClaude(t, app, "main", mainToken)
	captureClaude(t, app, "side", sideToken)
	captureClaude(t, app, "zeta", sideToken)
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

// A mode toggle moves every tool to the other mechanism's store, so the store the
// directory used before is unreachable the moment the fragment is rewritten. Its
// keychain item is invisible from the directory tree, so kae removes it rather than
// leaving a credential nothing points at.
func TestRunPinModeToggleRemovesTheOldModesItem(t *testing.T) {
	app := overlayTestApp(t)
	app.Env.GOOS = "darwin" // the keychain driver: the store is an item, not a file
	chdirTemp(t)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	pinID := paths.PinID(cwd)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	payload := `{"claudeAiOauth":{"accessToken":"` + mainToken + `"}}`
	runner.With(&runnertest.Fake{Stdout: payload, Code: 0}, func() {
		captureClaude(t, app, "main", mainToken)
	})

	isolatedDir := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "main")
	runner.With(&runnertest.Fake{Stdout: payload, Code: 0}, func() {
		if code := runPin(ctx, app, opts, "main", modeIsolated); code != constants.ExitOK {
			t.Fatalf("pin --isolated exit %d", code)
		}
	})

	fake := &runnertest.Fake{Stdout: payload, Code: 0}
	var out string
	runner.With(fake, func() {
		var code int
		code, out = captureStdout(t, func() int { return runPin(ctx, app, opts, "main", modeShared) })
		mustExit(t, constants.ExitOK, code, out)
	})
	if !strings.Contains(out, isolatedDir) {
		t.Fatalf("the superseded store must be reported as removed:\n%s", out)
	}
	// The store directory itself stays: it holds the sessions and settings a re-pin
	// restores, and only the credential was unreachable.
	if _, err := os.Stat(isolatedDir); err != nil {
		t.Fatalf("the isolated store dir must survive a mode toggle: %v", err)
	}
}

// unpin leaves everything by default — the promise that makes re-pinning restore a
// directory — and --purge is the opt-in for the one part that is invisible.
func TestUnpinPurgeRemovesTheItemAndPlainUnpinDoesNot(t *testing.T) {
	for _, purge := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "purge"}[purge], func(t *testing.T) {
			app := overlayTestApp(t)
			app.Env.GOOS = "darwin"
			chdirTemp(t)
			cwd, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			pinID := paths.PinID(cwd)
			ctx := context.Background()
			payload := `{"claudeAiOauth":{"accessToken":"` + mainToken + `"}}`
			runner.With(&runnertest.Fake{Stdout: payload, Code: 0}, func() {
				captureClaude(t, app, "main", mainToken)
				if code := runPin(ctx, app, commonOpts{Format: formatText}, "main", modeShared); code != constants.ExitOK {
					t.Fatal("pin failed")
				}
			})
			sharedDir := app.Paths.SharedDir(pinID, constants.ToolClaude)

			fake := &runnertest.Fake{Code: 0}
			runner.With(fake, func() {
				if code, out := captureStdout(t, func() int { return runUnpin(ctx, app, commonOpts{Format: formatText}, purge) }); code != constants.ExitOK {
					t.Fatalf("unpin exit %d: %s", code, out)
				}
			})
			deleted := strings.Contains(strings.Join(fake.Args, " "), sha8Of(sharedDir))
			if deleted != purge {
				t.Fatalf("purge=%v: item deleted=%v (ran %q %v)", purge, deleted, fake.Name, fake.Args)
			}
		})
	}
}

// An isolated re-bind re-keys the store by account, so the account the directory
// left behind owns a store nothing points at. Its item goes; a sibling tool's does
// not, because the same fragment still binds that one.
func TestPinRebindIsolatedRemovesThePreviousAccountsItem(t *testing.T) {
	app := overlayTestApp(t)
	app.Env.GOOS = "darwin"
	chdirTemp(t)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	pinID := paths.PinID(cwd)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	payload := `{"claudeAiOauth":{"accessToken":"` + mainToken + `"}}`

	runner.With(&runnertest.Fake{Stdout: payload, Code: 0}, func() {
		captureClaude(t, app, "main", mainToken)
		captureClaude(t, app, "beta", sideToken)
		if code := runPin(ctx, app, opts, "main", modeIsolated); code != constants.ExitOK {
			t.Fatal("pin --isolated failed")
		}
	})
	oldDir := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "main")
	newDir := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "beta")

	fake := &runnertest.Fake{Stdout: payload, Code: 0}
	var out string
	runner.With(fake, func() {
		var code int
		code, out = captureStdout(t, func() int { return runRebind(ctx, app, opts, "claude", "beta") })
		mustExit(t, constants.ExitOK, code, out)
	})
	if !strings.Contains(out, oldDir) {
		t.Fatalf("the previous account's store must be reported as removed:\n%s", out)
	}
	if strings.Contains(out, newDir) {
		t.Fatalf("the newly bound store must not be swept:\n%s", out)
	}
}
