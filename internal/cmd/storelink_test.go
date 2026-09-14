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
	"github.com/webkaz-labs/kagikae/internal/runner"
)

// storeLinkTestApp is the two-linkable-tool fixture the link tests need: profile
// "main" binds claude and codex, both captured, plus a claude account "side" for
// the re-bind. Two tools rather than one on purpose — the re-bind's claim is
// about the link it must *not* touch, and a single-tool fixture cannot see that.
func storeLinkTestApp(t *testing.T) *App {
	t.Helper()
	app := testApp(t, nil)
	app.Config.Profiles = map[string]config.Profile{
		"main": {Accounts: map[string]string{
			constants.ToolClaude: "main",
			constants.ToolCodex:  "main",
		}},
	}
	// A shared bind links the real home's files in, so the real homes have to
	// hold something.
	writeFile(t, filepath.Join(app.Env.Home, ".claude", "settings.json"), `{"theme":"dark"}`)
	captureClaude(t, app, "main", mainToken)
	captureClaude(t, app, "side", sideToken)
	seedCodex(t, app, "codex-main-token")
	code, out := captureStdout(t, func() int {
		return runCapture(context.Background(), app, commonOpts{Format: formatText}, constants.ToolCodex, "main")
	})
	mustExit(t, constants.ExitOK, code, out)
	return app
}

// fragmentEnvDir returns the directory the fragment's [env] block points one
// variable at. The link tests compare against this rather than against a path
// they compose themselves: the claim is that the link and the tool's environment
// name the *same* store, and a test that derives both from the same formula
// cannot fail when they disagree.
func fragmentEnvDir(t *testing.T, envVar string) string {
	t.Helper()
	for _, line := range strings.Split(readFile(t, fragmentRelPath), "\n") {
		if value, ok := strings.CutPrefix(line, envVar+" = "); ok {
			return strings.Trim(value, `"`)
		}
	}
	t.Fatalf("fragment has no %s entry:\n%s", envVar, readFile(t, fragmentRelPath))
	return ""
}

func mustReadlink(t *testing.T, path string) string {
	t.Helper()
	target, err := os.Readlink(path)
	if err != nil {
		t.Fatalf("readlink %s: %v", path, err)
	}
	return target
}

func TestRunPinLinksEachStoreWhereTheFragmentPointsIt(t *testing.T) {
	for _, mode := range []string{modeShared, modeIsolated} {
		t.Run(mode, func(t *testing.T) {
			app := storeLinkTestApp(t)
			chdirTemp(t)
			code, out := captureStdout(t, func() int {
				return runPin(context.Background(), app, commonOpts{Format: formatText}, "main", mode, false)
			})
			mustExit(t, constants.ExitOK, code, out)

			for _, tool := range []string{constants.ToolClaude, constants.ToolCodex} {
				path := storeLinkRelPath(tool)
				want := fragmentEnvDir(t, isolationEnvVar(tool))
				if got := mustReadlink(t, path); got != want {
					t.Fatalf("%s points at %q; the fragment exports %q", path, got, want)
				}
				if !filepath.IsAbs(want) {
					t.Fatalf("premise: the link target must be absolute, got %q", want)
				}
				if info, err := os.Stat(path); err != nil || !info.IsDir() {
					t.Fatalf("%s must resolve to the store directory: %v", path, err)
				}
			}
			if !strings.Contains(out, "Linked .config/claude, .config/codex") {
				t.Fatalf("the report must name the links it made:\n%s", out)
			}
		})
	}
}

// --no-link is a statement about the directory, not about this run: a link kae
// left on an earlier pin would otherwise keep naming a store while the user has
// said not to point at one from here.
func TestRunPinNoLinkMakesNoneAndRetractsKaesOwn(t *testing.T) {
	app := storeLinkTestApp(t)
	chdirTemp(t)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	code, out := captureStdout(t, func() int { return runPin(ctx, app, opts, "main", modeShared, false) })
	mustExit(t, constants.ExitOK, code, out)
	if _, err := os.Lstat(storeLinkRelPath(constants.ToolClaude)); err != nil {
		t.Fatalf("premise: the first pin must leave a link: %v", err)
	}

	code, out = captureStdout(t, func() int { return runPin(ctx, app, opts, "main", modeShared, true) })
	mustExit(t, constants.ExitOK, code, out)
	for _, tool := range []string{constants.ToolClaude, constants.ToolCodex} {
		if _, err := os.Lstat(storeLinkRelPath(tool)); !os.IsNotExist(err) {
			t.Fatalf("--no-link must leave no %s link (lstat err: %v)", tool, err)
		}
	}
	if !strings.Contains(out, "Removed the store links .config/claude, .config/codex.") {
		t.Fatalf("the retraction must name every link it took back, on one line:\n%s", out)
	}
	// The binding itself is unaffected: --no-link withholds the pointer, not the
	// store the fragment exports.
	if dir := fragmentEnvDir(t, isolationEnvVar(constants.ToolClaude)); dir == "" {
		t.Fatal("--no-link must not disturb the fragment's env entry")
	}
}

// The links go with the binding, not with the store: `kae unpin` keeps the store
// on purpose, so a link left behind would name one nothing here binds.
func TestRunUnpinRemovesTheStoreLinks(t *testing.T) {
	app := storeLinkTestApp(t)
	chdirTemp(t)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	code, out := captureStdout(t, func() int { return runPin(ctx, app, opts, "main", modeIsolated, false) })
	mustExit(t, constants.ExitOK, code, out)
	store := mustReadlink(t, storeLinkRelPath(constants.ToolClaude))

	code, out = captureStdout(t, func() int { return runUnpin(ctx, app, opts, false) })
	mustExit(t, constants.ExitOK, code, out)
	for _, tool := range []string{constants.ToolClaude, constants.ToolCodex} {
		if _, err := os.Lstat(storeLinkRelPath(tool)); !os.IsNotExist(err) {
			t.Fatalf("unpin must remove the %s link (lstat err: %v)", tool, err)
		}
	}
	if info, err := os.Stat(store); err != nil || !info.IsDir() {
		t.Fatalf("unpin must remove the link and keep the store it pointed at: %v", err)
	}
}

// `kae pin <tool> <account>` re-binds one tool, so exactly one link moves. The
// untouched tool's link is the assertion that matters: a re-bind that re-ran the
// whole convergence would retract it, since a re-bind knows only its own store.
func TestRunRebindMovesOnlyTheRetargetedToolsLink(t *testing.T) {
	app := storeLinkTestApp(t)
	chdirTemp(t)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	code, out := captureStdout(t, func() int { return runPin(ctx, app, opts, "main", modeIsolated, false) })
	mustExit(t, constants.ExitOK, code, out)
	before := mustReadlink(t, storeLinkRelPath(constants.ToolClaude))
	codexBefore := mustReadlink(t, storeLinkRelPath(constants.ToolCodex))

	code, out = captureStdout(t, func() int {
		return runRebind(ctx, app, opts, constants.ToolClaude, "side", false)
	})
	mustExit(t, constants.ExitOK, code, out)

	after := mustReadlink(t, storeLinkRelPath(constants.ToolClaude))
	if after == before {
		t.Fatalf("the re-bound tool's link must follow the account, still %q", after)
	}
	if want := fragmentEnvDir(t, isolationEnvVar(constants.ToolClaude)); after != want {
		t.Fatalf("%s points at %q; the fragment exports %q", storeLinkRelPath(constants.ToolClaude), after, want)
	}
	if got := mustReadlink(t, storeLinkRelPath(constants.ToolCodex)); got != codexBefore {
		t.Fatalf("codex was not re-bound; its link must not move: %q → %q", codexBefore, got)
	}

	// --no-link on a re-bind is still about the directory: the tool it did not
	// re-bind loses its link too, because the flag says none belong here.
	code, out = captureStdout(t, func() int {
		return runRebind(ctx, app, opts, constants.ToolClaude, "main", true)
	})
	mustExit(t, constants.ExitOK, code, out)
	for _, tool := range []string{constants.ToolClaude, constants.ToolCodex} {
		if _, err := os.Lstat(storeLinkRelPath(tool)); !os.IsNotExist(err) {
			t.Fatalf("--no-link must leave no %s link behind (lstat err: %v)", tool, err)
		}
	}
}

// Whatever is at the link path and is not kae's is the user's. kae warns, skips
// that one link, and still binds the directory — the data is never the price of a
// convenience pointer (docs/CLI.md § kae pin and mise init Semantics).
func TestStoreLinksNeverReplaceWhatKaeDidNotWrite(t *testing.T) {
	cases := []struct {
		name  string
		place func(t *testing.T, app *App, path string)
		check func(t *testing.T, path string)
	}{{
		name: "real directory",
		place: func(t *testing.T, _ *App, path string) {
			writeFile(t, filepath.Join(path, "settings.json"), `{"theme":"dark"}`)
		},
		check: func(t *testing.T, path string) {
			if got := readFile(t, filepath.Join(path, "settings.json")); got != `{"theme":"dark"}` {
				t.Fatalf("the user's directory was disturbed: %q", got)
			}
		},
	}, {
		name:  "real file",
		place: func(t *testing.T, _ *App, path string) { writeFile(t, path, "mine\n") },
		check: func(t *testing.T, path string) {
			if got := readFile(t, path); got != "mine\n" {
				t.Fatalf("the user's file was disturbed: %q", got)
			}
		},
	}, {
		name: "symlink outside kae's isolation root",
		place: func(t *testing.T, app *App, path string) {
			elsewhere := filepath.Join(app.Env.Home, "elsewhere")
			writeFile(t, filepath.Join(elsewhere, "settings.json"), "{}")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(elsewhere, path); err != nil {
				t.Fatal(err)
			}
		},
		check: func(t *testing.T, path string) {
			if !strings.HasSuffix(mustReadlink(t, path), "elsewhere") {
				t.Fatalf("the user's own symlink was re-aimed to %q", mustReadlink(t, path))
			}
		},
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := storeLinkTestApp(t)
			chdirTemp(t)
			path := storeLinkRelPath(constants.ToolClaude)
			tc.place(t, app, path)

			var code int
			_, stderr := captureStderr(t, func() int {
				code = runPin(context.Background(), app, commonOpts{Format: formatText}, "main", modeShared, false)
				return code
			})
			if code != constants.ExitOK {
				t.Fatalf("a link conflict must not change the exit code, got %d", code)
			}
			if !strings.Contains(stderr, "is not a kae link") {
				t.Fatalf("the skipped link must be reported on stderr:\n%s", stderr)
			}
			tc.check(t, path)
			// The bind itself happened, and the tool kae could link still got its link.
			if _, err := os.Stat(fragmentRelPath); err != nil {
				t.Fatalf("the fragment must still be written: %v", err)
			}
			if _, err := os.Lstat(storeLinkRelPath(constants.ToolCodex)); err != nil {
				t.Fatalf("one conflict must not cost another tool its link: %v", err)
			}
		})
	}
}

// A broken kae link is still kae's: it is what a deleted store leaves behind, and
// reading it with Stat instead of Lstat would report the path as free and then
// fail to create the link over it.
func TestStoreLinkStateClassifiesABrokenKaeLink(t *testing.T) {
	app := testApp(t, nil)
	chdirTemp(t)
	path := storeLinkRelPath(constants.ToolClaude)
	gone := app.Paths.SharedDir("abcdef0123456789", constants.ToolClaude)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(gone, path); err != nil {
		t.Fatal(err)
	}
	if target, state := app.storeLinkState(path); state != storeLinkKae || target != gone {
		t.Fatalf("a broken link into the isolation root is kae's: state=%d target=%q", state, target)
	}
	if !app.removeStoreLink(path) {
		t.Fatal("kae must be able to retract its own broken link")
	}
}

// The link sits at .config/<tool>, and the fragment at .config/mise/conf.d/…, so
// a tool named `mise` would have kae aim a symlink at the directory holding its
// own fragment. Nothing in the tool list says it cannot be, so this says it.
func TestStoreLinkPathsCannotCollideWithTheFragment(t *testing.T) {
	for _, tool := range constants.Tools {
		link := filepath.Clean(storeLinkRelPath(tool))
		if link == fragmentRelPath || pathWithin(fragmentRelPath, link) {
			t.Fatalf("the %s store link at %s would contain the fragment %s", tool, link, fragmentRelPath)
		}
	}
}

// The flag reaches completion through the registrar, with no script change: this
// is what makes that true rather than assumed (docs/CLI.md § Keeping completion
// current — a new flag is not a structural change).
func TestPinCompletionOffersNoLink(t *testing.T) {
	if !strings.Contains(strings.Join(flagCompletions("pin"), " "), "--no-link") {
		t.Fatalf("`kae pin --<TAB>` must offer --no-link, got %v", flagCompletions("pin"))
	}
}

// Against real git, because the entry's shape is git's answer and not kae's: the
// links must leave `git status` as clean as the fragment does.
func TestRunPinRecordsTheStoreLinksInTheExcludeFile(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	app := storeLinkTestApp(t)
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		full := append([]string{"-C", repo}, args...)
		out, err := exec.Command("git", full...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	git("init", "-q")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(cwd) })
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}

	code, out := captureStdout(t, func() int {
		return runPin(context.Background(), app, commonOpts{Format: formatText}, "main", modeShared, false)
	})
	mustExit(t, constants.ExitOK, code, out)
	exclude := readFile(t, filepath.Join(repo, ".git", "info", "exclude"))
	for _, tool := range []string{constants.ToolClaude, constants.ToolCodex} {
		if entry := "/" + filepath.ToSlash(storeLinkRelPath(tool)); !strings.Contains(exclude, entry+"\n") {
			t.Fatalf("exclude file missing %q:\n%s", entry, exclude)
		}
	}
	if status := git("status", "--porcelain"); status != "" {
		t.Fatalf("a pin must leave the working tree clean:\n%s", status)
	}
	if !strings.Contains(out, "Linked .config/claude, .config/codex to this directory's stores (ignored via ") {
		t.Fatalf("the report must name the exclude file it used:\n%s", out)
	}
}

// revParseCounter answers `git rev-parse --git-common-dir --show-prefix` with the
// measured shape and counts how often it is asked. Counting is the point: the
// reply alone cannot tell one call from two, and two is what duplicated the
// banner below.
type revParseCounter struct{ calls int }

func (r *revParseCounter) Run(_ context.Context, name string, args ...string) (string, string, int) {
	if name == "git" && len(args) > 0 && args[0] == "rev-parse" {
		r.calls++
		return ".git\n\n", "", 0
	}
	return "", "", 1
}

func (r *revParseCounter) RunInput(ctx context.Context, _ string, name string, args ...string) (string, string, int) {
	return r.Run(ctx, name, args...)
}

// A bind records the fragment and the store links it just made as **one** group:
// one `git rev-parse`, one banner, one `ignored via` in the report. Recording
// them separately also worked — every path ended up excluded — which is why this
// asserts the count rather than the presence: the separate shape wrote the banner
// twice into the user's exclude file and asked git twice per pin.
func TestRunPinRecordsTheFragmentAndItsLinksInOneGroup(t *testing.T) {
	app := storeLinkTestApp(t)
	chdirTemp(t)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cwd, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	counter := &revParseCounter{}
	var code int
	var out string
	runner.With(counter, func() {
		code, out = captureStdout(t, func() int {
			return runPin(context.Background(), app, commonOpts{Format: formatText}, "main", modeShared, false)
		})
	})
	mustExit(t, constants.ExitOK, code, out)
	if counter.calls != 1 {
		t.Fatalf("a pin must ask git where the exclude file is once, asked %d times", counter.calls)
	}

	exclude := readFile(t, filepath.Join(cwd, ".git", "info", "exclude"))
	if n := strings.Count(exclude, excludeBannerLine); n != 1 {
		t.Fatalf("the banner must head one block, found %d:\n%s", n, exclude)
	}
	for _, path := range []string{fragmentRelPath, storeLinkRelPath(constants.ToolClaude), storeLinkRelPath(constants.ToolCodex)} {
		if entry := "/" + filepath.ToSlash(path); !strings.Contains(exclude, entry+"\n") {
			t.Fatalf("exclude file missing %q:\n%s", entry, exclude)
		}
	}

	// The one call is what lets both lines name the same file, and the order is
	// the fragment then its links — asserted because the recording moved above
	// the report to make the single call possible.
	wrote := strings.Index(out, "Wrote "+fragmentRelPath+" (ignored via ")
	linked := strings.Index(out, "Linked .config/claude, .config/codex to this directory's stores (ignored via ")
	if wrote < 0 || linked < 0 {
		t.Fatalf("both lines must name the exclude file:\n%s", out)
	}
	if linked < wrote {
		t.Fatalf("the report must stay `Wrote ...` then `Linked ...`:\n%s", out)
	}
}
