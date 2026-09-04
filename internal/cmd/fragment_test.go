package cmd

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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
//
// Every case is self-contained inside its own t.TempDir(): the repository root is
// that temp dir, `.git` is created under it, and the cwd sits depth directories
// below it. An earlier version fabricated relative common dirs like "../../.git"
// from the temp cwd, which resolved *outside* the case's own directory and left
// `$TMPDIR/.git/info/exclude` on the operator's machine — a test escaping its
// sandbox is the same defect class as a smoke check escaping HOME.
func TestEnsureGitExcludedDerivesTargetAndEntry(t *testing.T) {
	frag := filepath.ToSlash(fragmentRelPath)
	for _, tc := range []struct {
		name string
		// depth is how many directories below the repository root the cwd sits, and
		// prefix is what git reports for --show-prefix there.
		depth  int
		prefix string
		// elsewhere reports the common dir absolutely, in a directory nowhere near
		// the cwd, the way a linked worktree does.
		elsewhere bool
		// commonDir overrides what git prints entirely, for answers kae must refuse.
		commonDir func(root string) string
		wantEntry string
		code      int
		// refused marks an answer kae must decline rather than interpret: it records
		// nothing and warns, exactly as a non-zero exit does.
		refused bool
	}{
		{name: "repository root", wantEntry: "/" + frag},
		{
			name:      "subdirectory of an ordinary checkout",
			depth:     1,
			prefix:    "sub/",
			wantEntry: "/sub/" + frag,
		},
		{
			name:      "subdirectory two deep",
			depth:     2,
			prefix:    "a/b/",
			wantEntry: "/a/b/" + frag,
		},
		{
			// The worktree's own $GIT_DIR is never consulted, so the rule has to land
			// in the absolute common dir shared with the main checkout.
			name:      "linked worktree reports an absolute common dir",
			elsewhere: true,
			wantEntry: "/" + frag,
		},
		{
			// An entry is a *pattern*, so a directory name carrying wildmatch
			// metacharacters has to be escaped or the rule matches nothing while
			// `kae pin` reports the fragment as ignored.
			name:      "prefix with glob metacharacters",
			depth:     1,
			prefix:    "[wip]-a*b/",
			wantEntry: `/\[wip\]-a\*b/` + frag,
		},
		{
			// A leading space is a legal first character of a path component, so the
			// line ending is all that may be trimmed.
			name:      "prefix whose component starts with a space",
			depth:     1,
			prefix:    " spaced/",
			wantEntry: "/ spaced/" + frag,
		},
		{name: "no repository, or no git to ask", code: 128},
		{
			// A newline is legal in a path component and rev-parse quotes nothing, so
			// a third value means the two cannot be told apart. Following it would
			// create an exclude file somewhere unrelated and still claim success.
			name:      "a value containing a newline is refused, not truncated",
			commonDir: func(root string) string { return filepath.Join(root, "we\nird", ".git") },
			refused:   true,
		},
		{
			// A `git` that exits 0 naming somewhere that does not exist must be
			// refused, not acted on: kae would otherwise MkdirAll it and report the
			// fragment ignored, creating a tree nothing reads.
			name:      "a common dir that does not exist is refused",
			commonDir: func(root string) string { return filepath.Join(root, "junk") },
			refused:   true,
		},
		{
			name:      "a common dir that is a file is refused",
			commonDir: func(root string) string { return filepath.Join(root, "notadir") },
			refused:   true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			// os.Getwd (and so filepath.Abs) reports the symlink-resolved path on
			// darwin, where $TMPDIR sits under /var -> /private/var. Resolve once so
			// the expectations are the same shape as the answer.
			if resolved, err := filepath.EvalSymlinks(root); err == nil {
				root = resolved
			}
			gitDir := filepath.Join(root, ".git")
			if err := os.MkdirAll(gitDir, 0o755); err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(root, "notadir"), "not a directory\n")
			cwd := root
			for range tc.depth {
				cwd = filepath.Join(cwd, "d")
			}
			if err := os.MkdirAll(cwd, 0o755); err != nil {
				t.Fatal(err)
			}
			restore, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.Chdir(restore) })
			if err := os.Chdir(cwd); err != nil {
				t.Fatal(err)
			}

			// What git prints, and where the rule must land.
			reported, wantCommon := strings.Repeat("../", tc.depth)+".git", gitDir
			if tc.elsewhere {
				other := filepath.Join(t.TempDir(), "main", ".git")
				if err := os.MkdirAll(other, 0o755); err != nil {
					t.Fatal(err)
				}
				if resolved, err := filepath.EvalSymlinks(other); err == nil {
					other = resolved
				}
				reported, wantCommon = other, other
			}
			if tc.commonDir != nil {
				reported = tc.commonDir(root)
			}
			if tc.code != 0 {
				reported = ""
			}

			fake := &runnertest.Fake{Stdout: reported + "\n" + tc.prefix + "\n", Code: tc.code}
			var got string
			runner.With(fake, func() {
				got = ensureGitExcluded(context.Background(), fragmentRelPath)
			})
			// Assert the invocation, not only the reply. Every case here fabricates
			// git's output, so without this the whole table keeps passing if
			// --show-prefix is dropped from the command — and the anchoring it
			// supplies is the half that fails while reporting success.
			if want := []string{"rev-parse", "--git-common-dir", "--show-prefix"}; !slices.Equal(fake.Args, want) {
				t.Fatalf("git args = %v, want %v", fake.Args, want)
			}
			if tc.code != 0 || tc.refused {
				if got != "" {
					t.Fatalf("an unusable answer must yield no exclude file, got %q", got)
				}
				// And it must not have created anything for that answer.
				entries, rerr := os.ReadDir(root)
				if rerr != nil {
					t.Fatal(rerr)
				}
				for _, e := range entries {
					if e.Name() != ".git" && e.Name() != "notadir" && e.Name() != "d" {
						t.Fatalf("a refused answer must create nothing, found %q", e.Name())
					}
				}
				return
			}
			if want := filepath.Join(wantCommon, "info", "exclude"); got != want {
				t.Fatalf("exclude file: got %q want %q", got, want)
			}
			if !strings.Contains(readFile(t, got), tc.wantEntry+"\n") {
				t.Fatalf("exclude file missing entry %q:\n%s", tc.wantEntry, readFile(t, got))
			}
		})
	}
}

// An ignore rule is cosmetic and runs after the directory is already bound, so a
// failure to record it must not fail `kae pin` — that would skip the credential
// sweep and the export fallback, on every re-run, for a problem that does not go
// away. Reproduced the way it actually happens: the exclude file unreachable,
// which pinning a linked worktree makes possible because the file lives in the
// *main checkout's* .git.
//
// Which branch this hits is worth naming, because the two are easy to confuse:
// with .git unwritable and info/ absent, ReadFile gets ENOENT (skipped) and it is
// os.MkdirAll that fails. The OpenFile-on-an-unwritable-existing-file branch needs
// the file to exist already, and is covered by case E of the § per-worktree smoke
// block in docs/VALIDATION.md — where the first draft chmod'd the *directory* and
// so proved nothing, since that does not stop a write to a file already in it.
func TestEnsureGitExcludedNeverFailsThePin(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the mode bits this test relies on")
	}
	chdirTemp(t)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	gitDir := filepath.Join(cwd, ".git")
	if err := os.MkdirAll(gitDir, 0o500); err != nil { // no write bit
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(gitDir, 0o700) })

	fake := &runnertest.Fake{Stdout: ".git\n\n"}
	var got string
	_, stderr := captureStderr(t, func() int {
		runner.With(fake, func() { got = ensureGitExcluded(context.Background(), fragmentRelPath) })
		return constants.ExitOK
	})
	if got != "" {
		t.Fatalf("a failure must report no exclude file, got %q", got)
	}
	if !strings.Contains(stderr, "could not tell git to ignore") {
		t.Fatalf("the failure must be a stderr warning, not silence:\n%s", stderr)
	}
	if !strings.Contains(stderr, "the binding is in place") {
		t.Fatalf("the warning must say the bind succeeded and the fragment needs ignoring:\n%s", stderr)
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
			file = ensureGitExcluded(context.Background(), fragmentRelPath)
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
	// Plus one in a subdirectory, recorded from *there*, so the anchoring half is
	// measured against real git and not only against a prefix the test invented.
	// At the repository root the prefix is empty, so a root-only check passes just
	// as happily with --show-prefix dropped.
	nested := filepath.Join(main, "nested")
	writeFile(t, filepath.Join(nested, fragmentRelPath), "# fragment\n")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(cwd) })
	if err := os.Chdir(main); err != nil {
		t.Fatal(err)
	}
	file := ensureGitExcluded(context.Background(), fragmentRelPath)
	if file == "" {
		t.Fatal("ensureGitExcluded reported no exclude file inside a real repository")
	}
	// That one entry has to have covered the linked worktree's fragment as well as
	// the main checkout's — the whole point — but it cannot cover the nested one,
	// whose rule is anchored a directory deeper.
	if status := git(linked, "status", "--porcelain"); status != "" {
		t.Fatalf("one entry from the main checkout must cover the worktree too:\n%s", status)
	}
	if status := git(main, "status", "--porcelain"); !strings.Contains(status, "nested") {
		t.Fatalf("the nested fragment should still be untracked at this point, got %q", status)
	}
	if err := os.Chdir(nested); err != nil {
		t.Fatal(err)
	}
	if got := ensureGitExcluded(context.Background(), fragmentRelPath); got != file {
		t.Fatalf("a subdirectory must record into the same common exclude file: got %q want %q", got, file)
	}
	// Against real git: the entry is anchored at the repository root, so it must
	// carry the prefix. Dropping --show-prefix writes "/.config/…" here, which is
	// already present from the root pin — so only this assertion, and the status
	// check below it, would notice.
	if want := "/nested/" + filepath.ToSlash(fragmentRelPath); !strings.Contains(readFile(t, file), want+"\n") {
		t.Fatalf("exclude file missing the root-anchored nested entry %q:\n%s", want, readFile(t, file))
	}
	for _, dir := range []string{main, linked} {
		if status := git(dir, "status", "--porcelain"); status != "" {
			t.Fatalf("%s is not clean after the exclude entries:\n%s", dir, status)
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
	// CmdUnpin constructs its own App, so isolate the process environment as well
	// as the App used to create the fixture. A temp App alone left unpin taking a
	// lock below the operator's real XDG_STATE_HOME.
	t.Setenv("HOME", app.Env.Home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Dir(app.Paths.ConfigDir))
	t.Setenv("XDG_DATA_HOME", filepath.Dir(app.Paths.DataDir))
	t.Setenv("XDG_STATE_HOME", filepath.Dir(app.Paths.StateDir))
	t.Setenv("XDG_RUNTIME_DIR", filepath.Dir(app.Paths.RuntimeDir))
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

// A mode toggle moves every tool to the other mechanism's *config* store, so the store
// the directory used before is unreachable the moment the fragment is rewritten. Its
// keychain item is invisible from the directory tree, so kae removes it rather than
// leaving a credential nothing points at. It holds a credential only when the binding
// predates the per-account credential store, which is why this test ages the layout —
// the body says so at the call, and this comment said "the other mechanism's store"
// until 2026-08-08, which reads as the credential moving too. It does not.
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
	// A payload with a real expiresAt: without one kae cannot judge what the item
	// holds, and a store it cannot read is deliberately kept rather than deleted.
	payload := claudeOAuthPayload(mainToken, time.Date(2026, 6, 11, 9, 0, 0, 0, time.UTC))
	runner.With(&runnertest.Fake{Stdout: payload, Code: 0}, func() {
		captureClaude(t, app, "main", mainToken)
	})

	isolatedDir := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "main")
	runner.With(&runnertest.Fake{Stdout: payload, Code: 0}, func() {
		if code := runPin(ctx, app, opts, "main", modeIsolated); code != constants.ExitOK {
			t.Fatalf("pin --isolated exit %d", code)
		}
	})
	// Aged into the pre-split layout, because that is the only arrangement where a
	// mode toggle strands a credential at all: since the split both modes read the
	// account's own store, so the toggle moves the sessions and leaves the credential
	// exactly where it was. What still has to be swept is the per-directory item a
	// binding from before the split left in the store it is moving off.
	makePreSplit(t, app, constants.ToolClaude, "main", cwd, isolatedDir)

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

// `kae unpin --purge` is the one caller that may delete a usable copy whose account no
// longer exists — it was asked to remove these credentials, and keeping it would strand a
// live token no kae command can address. That is the *promise* direction of the same
// argument the bind sweeps make in reverse, and nothing pinned this call site: flipping it
// to the bind sweeps' answer made `--purge` a silent no-op here and survived the whole
// suite (execution-type review, round 3).
func TestUnpinPurgeDeletesALostAccountsCredential(t *testing.T) {
	sim := &keychainSim{}
	runner.With(sim, func() {
		app := overlayTestApp(t)
		app.Env.GOOS = "darwin"
		chdirTemp(t)
		ctx := context.Background()
		opts := commonOpts{Format: formatText}
		captureClaudeFromKeychain(t, app, sim, "main", mainToken, app.Now().Add(time.Hour))
		if code := runPin(ctx, app, opts, "main", modeIsolated); code != constants.ExitOK {
			t.Fatalf("pin --isolated exit %d", code)
		}
		if err := os.RemoveAll(app.Paths.AccountDir(constants.ToolClaude, "main")); err != nil {
			t.Fatal(err)
		}
		sim.payload = claudeOAuthPayload("sk-ant-oat01-MAIN-REFRESHED-cccc", app.Now().Add(8*time.Hour))
		sim.ops = nil

		_, stderr := captureStderr(t, func() int { return runUnpin(ctx, app, opts, true) })

		if !strings.Contains(strings.Join(sim.ops, ","), "delete") {
			t.Fatalf("--purge must remove a copy nothing can harvest: %v", sim.ops)
		}
		if !strings.Contains(stderr, "deleted without being kept anywhere") {
			t.Fatalf("deleting it must say so: %q", stderr)
		}
	})
}

// unpin leaves everything by default — the promise that makes re-pinning restore a
// directory — and --purge is the opt-in for the one part that is invisible.
func TestUnpinPurgeRemovesTheItemAndPlainUnpinDoesNot(t *testing.T) {
	for _, purge := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "purge"}[purge], func(t *testing.T) {
			app := overlayTestApp(t)
			app.Env.GOOS = "darwin"
			chdirTemp(t)
			ctx := context.Background()
			// A payload with a real expiresAt: without one kae cannot judge what the item
			// holds, and a store it cannot read is deliberately kept rather than deleted.
			payload := claudeOAuthPayload(mainToken, time.Date(2026, 6, 11, 9, 0, 0, 0, time.UTC))
			runner.With(&runnertest.Fake{Stdout: payload, Code: 0}, func() {
				captureClaude(t, app, "main", mainToken)
				if code := runPin(ctx, app, commonOpts{Format: formatText}, "main", modeShared); code != constants.ExitOK {
					t.Fatal("pin failed")
				}
			})
			// The item a purge takes is the account's credential store, which is where the
			// credential lives now — the shared store holds the sessions.
			sweptDir := app.credStoreDir(constants.ToolClaude, "main")

			fake := &runnertest.Fake{Stdout: payload, Code: 0}
			runner.With(fake, func() {
				if code, out := captureStdout(t, func() int { return runUnpin(ctx, app, commonOpts{Format: formatText}, purge) }); code != constants.ExitOK {
					t.Fatalf("unpin exit %d: %s", code, out)
				}
			})
			// The delete, not any call naming the item: the sweep *reads* the store before
			// deciding, and that read carries the same service name — so matching on the
			// name alone reported a deletion that had not happened.
			args := strings.Join(fake.Args, " ")
			deleted := strings.Contains(args, "delete-generic-password") && strings.Contains(args, sha8Of(sweptDir))
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
	// A payload with a real expiresAt: without one kae cannot judge what the item
	// holds, and a store it cannot read is deliberately kept rather than deleted.
	payload := claudeOAuthPayload(mainToken, time.Date(2026, 6, 11, 9, 0, 0, 0, time.UTC))

	runner.With(&runnertest.Fake{Stdout: payload, Code: 0}, func() {
		captureClaude(t, app, "main", mainToken)
		captureClaude(t, app, "beta", sideToken)
		if code := runPin(ctx, app, opts, "main", modeIsolated); code != constants.ExitOK {
			t.Fatal("pin --isolated failed")
		}
	})
	oldDir := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "main")
	newDir := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "beta")
	// Pre-split, for the same reason the mode toggle above is: since the split the
	// previous account's credential is that account's own store, which a re-bind must
	// **not** delete — another worktree may still be bound to it. The item that is
	// still this directory's alone, and still has to go, is the one an older kae left
	// in the store keyed by config dir.
	makePreSplit(t, app, constants.ToolClaude, "main", cwd, oldDir)

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
