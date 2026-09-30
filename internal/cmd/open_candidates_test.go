// Tests of how open and cd choose among candidate places.

package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/runner"
)

// recordingRunner runs the real programs and records every argv.
type recordingRunner struct{ calls [][]string }

func (r *recordingRunner) Run(ctx context.Context, name string, args ...string) (string, string, int) {
	r.calls = append(r.calls, append([]string{name}, args...))
	return runner.OSRunner{}.Run(ctx, name, args...)
}

func (r *recordingRunner) RunInput(ctx context.Context, stdin, name string, args ...string) (string, string, int) {
	r.calls = append(r.calls, append([]string{name}, args...))
	return runner.OSRunner{}.RunInput(ctx, stdin, name, args...)
}

func (r *recordingRunner) listed() bool {
	for _, call := range r.calls {
		if slices.Contains(call, "ls-files") {
			return true
		}
	}
	return false
}

// The user level and the ancestor project levels come before the below levels,
// so choosing one does not list the repository; --below, --at and a request with
// no single place do, and their numbers stay kae ls's.
func TestNavigateFindsBelowLevelsOnlyWhenNeeded(t *testing.T) {
	app := testApp(t, nil)
	root := chdirTo(t, t.TempDir())
	scratchGit(t, root, "init", "-q")
	mkdirs(t, filepath.Join(root, ".claude"), filepath.Join(app.Env.Home, ".claude"))
	writeFile(t, filepath.Join(root, "a", ".claude", "settings.json"), "{}\n")
	for _, tc := range []struct {
		name   string
		f      lsFlags
		want   string
		listed bool
	}{
		{"user level", lsFlags{}, filepath.Join(app.Env.Home, ".claude"), false},
		{"project", lsFlags{project: true}, filepath.Join(root, ".claude"), false},
		{"below", lsFlags{below: true}, filepath.Join(root, "a", ".claude"), true},
		{"--at", lsFlags{at: atFlag{n: 4, set: true}}, filepath.Join(root, "a", ".claude"), true},
	} {
		rec := &recordingRunner{}
		var code int
		var got, stderr string
		runner.With(rec, func() { code, got, stderr = cdPath(t, app, navRequest(t, "cd", tc.f, "claude")) })
		if code != constants.ExitOK || got != tc.want || rec.listed() != tc.listed {
			t.Fatalf("%s: %d %q %s; listed the repository = %v, want %v (%v)", tc.name, code, got, stderr, rec.listed(), tc.listed, rec.calls)
		}
	}
	// No project level: the candidates are the full list, with its numbers.
	chdirTo(t, filepath.Join(t.TempDir(), "bare"))
	scratchGit(t, filepath.Dir(mustCwdAbs(t)), "init", "-q")
	writeFile(t, filepath.Join(mustCwdAbs(t), "x", ".codex", "config.toml"), "\n")
	mkdirs(t, filepath.Join(app.Env.Home, ".codex"))
	code, _, stderr := cdPath(t, app, navRequest(t, "cd", lsFlags{project: true}, "codex"))
	if code != constants.ExitUsage || !strings.Contains(stderr, "kae cd codex --at 2  "+filepath.Join(mustCwdAbs(t), "x", ".codex")) {
		t.Fatalf("codex --project with none = %d:\n%s", code, stderr)
	}
}

// The count in "matches N places" is the candidates listed, after missing places
// are left out.
func TestSeveralMatchesCountsTheListedCandidates(t *testing.T) {
	app := testApp(t, nil)
	chdirTo(t, t.TempDir())
	mkdirs(t, app.Paths.ConfigDir, app.Paths.DataDir) // state is missing
	code, _, stderr := cdPath(t, app, navRequest(t, "cd", lsFlags{}, "kae"))
	if code != constants.ExitUsage || !strings.Contains(stderr, "kae cd kae matches 2 places") || strings.Contains(stderr, "--at 3") {
		t.Fatalf("kae cd kae = %d:\n%s", code, stderr)
	}
	if err := os.Remove(app.Paths.DataDir); err != nil {
		t.Fatal(err)
	}
	code, _, stderr = cdPath(t, app, navRequest(t, "cd", lsFlags{}, "kae"))
	if code != constants.ExitUsage || !strings.Contains(stderr, "kae cd kae matches 1 place;") {
		t.Fatalf("kae cd kae with one directory = %d:\n%s", code, stderr)
	}
}

// setSummary is a candidate set as one line per group, "heading: number path …",
// so a test states what an entry point offers once, whichever view shows it.
func setSummary(set *placeCandidates) []string {
	var out []string
	for _, g := range set.groups {
		line := g.Heading + ":"
		for _, row := range g.Rows {
			line += fmt.Sprintf(" %d %s", row.Number, row.Path)
		}
		out = append(out, line)
	}
	return out
}

// Each entry point that names no single place builds one candidate set: its
// groups, and their rows in `kae ls` order with `kae ls`'s numbers. Places whose
// directory is missing are not in it.
func TestNavigationCandidateSets(t *testing.T) {
	app := testApp(t, nil)
	cwd := chdirTo(t, t.TempDir())
	claudeHome := filepath.Join(app.Env.Home, ".claude")
	mkdirs(t, claudeHome, filepath.Join(cwd, ".claude"), filepath.Join(cwd, ".codex"),
		filepath.Join(cwd, "a", ".claude"), filepath.Join(cwd, "b", ".codex"),
		app.Paths.ConfigDir, app.Paths.DataDir)
	// codex's real home and kae's state directory are missing.
	set := func(f lsFlags, pos ...string) *placeCandidates {
		t.Helper()
		path, set, code := app.resolveNavigation(context.Background(), commonOpts{Format: formatText}, navRequest(t, "cd", f, pos...))
		if code != constants.ExitOK || path != "" || set == nil {
			t.Fatalf("kae cd %v = %q, %v, exit %d; want a candidate set", pos, path, set, code)
		}
		return set
	}
	for _, tc := range []struct {
		name string
		f    lsFlags
		pos  []string
		want []string
	}{
		{"no target", lsFlags{}, nil, []string{
			"claude: 1 " + claudeHome + " 3 " + filepath.Join(cwd, ".claude") + " 4 " + filepath.Join(cwd, "a", ".claude"),
			"codex: 2 " + filepath.Join(cwd, ".codex") + " 3 " + filepath.Join(cwd, "b", ".codex"),
			"kae: 1 " + app.Paths.ConfigDir + " 2 " + app.Paths.DataDir,
		}},
		{"several matches", lsFlags{}, []string{"kae"}, []string{
			"kae: 1 " + app.Paths.ConfigDir + " 2 " + app.Paths.DataDir,
		}},
		{"level without a bound tool, project", lsFlags{project: true}, nil, []string{
			"claude: 3 " + filepath.Join(cwd, ".claude"), "codex: 2 " + filepath.Join(cwd, ".codex"),
		}},
		{"level without a bound tool, below", lsFlags{below: true}, nil, []string{
			"claude: 4 " + filepath.Join(cwd, "a", ".claude"), "codex: 3 " + filepath.Join(cwd, "b", ".codex"),
		}},
		{"level without a bound tool, home", lsFlags{home: true}, nil, []string{"claude: 1 " + claudeHome}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := setSummary(set(tc.f, tc.pos...)); !slices.Equal(got, tc.want) {
				t.Fatalf("set = %q\nwant %q", got, tc.want)
			}
		})
	}
	// A target with no current place offers all its places: no level of claude
	// below a bare directory.
	bare := chdirTo(t, filepath.Join(t.TempDir(), "bare"))
	mkdirs(t, filepath.Join(bare, ".claude"))
	if got, want := setSummary(set(lsFlags{below: true}, "claude")), []string{
		"claude: 1 " + claudeHome + " 3 " + filepath.Join(bare, ".claude"),
	}; !slices.Equal(got, want) {
		t.Fatalf("no current place: set = %q, want %q", got, want)
	}
	chdirTo(t, cwd)
	// A level selector with one bound tool applies to it, and one place is no set.
	writeFile(t, filepath.Join(cwd, fragmentRelPath), fragModePrefix+modeShared+"\n"+fragAccountPrefix+"codex=main\n")
	path, got, code := app.resolveNavigation(context.Background(), commonOpts{Format: formatText}, navRequest(t, "cd", lsFlags{below: true}))
	if code != constants.ExitOK || got != nil || path != filepath.Join(cwd, "b", ".codex") {
		t.Fatalf("kae cd --below with codex bound = %q, %v, %d", path, got, code)
	}
	// No place of the level at all: the request is not_found.
	if err := os.RemoveAll(claudeHome); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(cwd, ".codex")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(cwd, ".claude")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(cwd, fragmentRelPath)); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := cdPath(t, app, navRequest(t, "cd", lsFlags{project: true}))
	if code != constants.ExitNotFound || !strings.Contains(stderr, "no existing place to choose") {
		t.Fatalf("kae cd --project with no project level = %d %q", code, stderr)
	}
}

// A request that names no single place is the picker's; until it exists each is
// its no-terminal case: the candidates, then a usage error, on stderr.
func TestNavigatePickerCasesListCandidates(t *testing.T) {
	app := testApp(t, nil)
	cwd := chdirTo(t, t.TempDir())
	// cwd's own .claude/ makes claude relevant to bare ls: user 1, session 2,
	// project 3, below 4 and 5.
	mkdirs(t, filepath.Join(cwd, ".claude"), filepath.Join(cwd, ".codex"), filepath.Join(cwd, "a", ".claude"), filepath.Join(cwd, "b", ".claude"),
		app.Paths.ConfigDir, app.Paths.DataDir, app.Paths.StateDir)
	for _, tc := range []struct {
		name string
		verb string
		f    lsFlags
		pos  []string
		want []string
	}{
		{"no target", "open", lsFlags{}, nil, []string{
			"kae open needs a target; choose one:", "kae open kae --at 2  " + app.Paths.DataDir,
			"kae open claude --at 4  " + filepath.Join(cwd, "a", ".claude"),
		}},
		{"kae has no current place", "cd", lsFlags{}, []string{"kae"}, []string{
			"kae cd kae matches 3 places", "kae cd kae --at 1  " + app.Paths.ConfigDir,
		}},
		{"several below", "cd", lsFlags{below: true}, []string{"claude"}, []string{
			"kae cd claude --below matches 2 places",
			"kae cd claude --at 4  " + filepath.Join(cwd, "a", ".claude"),
			"kae cd claude --at 5  " + filepath.Join(cwd, "b", ".claude"),
		}},
		{"several below, explicit, root", "open", lsFlags{below: true, root: true, shared: true}, []string{"claude"}, []string{
			// The path is the one the command reaches: the directory holding .claude/.
			"kae open -s claude --at 5 --root  " + filepath.Join(cwd, "b") + "\n",
		}},
		// Each tool's places of that level, as place lines like any candidate list.
		{"level without a bound tool", "cd", lsFlags{project: true}, nil, []string{
			"kae cd --project needs a tool: no tool is bound here; choose one:",
			"kae cd claude --at 3  " + filepath.Join(cwd, ".claude"), "kae cd codex --at 2  " + filepath.Join(cwd, ".codex"),
		}},
		{"level without a bound tool, root", "cd", lsFlags{project: true, root: true}, nil, []string{
			"kae cd claude --at 3 --root  " + cwd + "\n", "kae cd codex --at 2 --root  " + cwd + "\n",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := cdPathOrOpen(t, app, tc.verb, navRequest(t, tc.verb, tc.f, tc.pos...))
			if code != constants.ExitUsage || stdout != "" {
				t.Fatalf("exit %d stdout %q; want usage with nothing on stdout:\n%s", code, stdout, stderr)
			}
			for _, want := range tc.want {
				if !strings.Contains(stderr, want) {
					t.Fatalf("stderr missing %q:\n%s", want, stderr)
				}
			}
		})
	}

	// Two bound tools: the level selector cannot choose one.
	writeFile(t, filepath.Join(cwd, fragmentRelPath),
		fragModePrefix+modeShared+"\n"+fragAccountPrefix+"claude=main\n"+fragAccountPrefix+"codex=main\n")
	code, _, stderr := cdPath(t, app, navRequest(t, "cd", lsFlags{project: true}))
	if code != constants.ExitUsage || !strings.Contains(stderr, "2 tools are bound here") || !strings.Contains(stderr, "kae cd codex --at 2  "+filepath.Join(cwd, ".codex")) {
		t.Fatalf("two bound tools = %d %q", code, stderr)
	}
}

// A candidate whose directory does not exist is left out (choosing it would
// exit 7) and the rest keep kae ls's numbers; with none left, it is not_found.
func TestNavigateCandidatesLeaveOutMissingPlaces(t *testing.T) {
	app := testApp(t, nil)
	cwd := chdirTo(t, t.TempDir())
	// ~/.claude and ~/.codex do not exist; cwd's .claude/ does.
	mkdirs(t, filepath.Join(cwd, ".claude"))
	code, _, stderr := cdPath(t, app, navRequest(t, "cd", lsFlags{}))
	if code != constants.ExitUsage || !strings.Contains(stderr, "kae cd claude --at 3  "+filepath.Join(cwd, ".claude")) ||
		strings.Contains(stderr, "kae cd claude --at 1") || strings.Contains(stderr, "kae cd claude --at 2") || strings.Contains(stderr, "kae cd kae --at") {
		t.Fatalf("no target = %d:\n%s", code, stderr)
	}
	// codex has no project level here and its only place, the real home, is
	// missing: nothing to choose.
	code, _, stderr = cdPath(t, app, navRequest(t, "cd", lsFlags{project: true}, "codex"))
	if code != constants.ExitNotFound || !strings.Contains(stderr, "no existing place to choose") {
		t.Fatalf("kae cd codex --project with no ~/.codex = %d %q", code, stderr)
	}
	// Several matches, all missing: kae's directories were never created.
	code, _, stderr = cdPath(t, app, navRequest(t, "cd", lsFlags{}, "kae"))
	if code != constants.ExitNotFound {
		t.Fatalf("kae cd kae with no kae directory = %d %q", code, stderr)
	}
}

// A target with no current place is the picker's case over its places (the
// operator's reading of ROADMAP "Selection"); ls --current keeps not_found.
func TestNavigateNoCurrentPlaceListsTheTargetsPlaces(t *testing.T) {
	app := overlayTestApp(t)
	captureClaude(t, app, "main", mainToken)
	outside := chdirTo(t, filepath.Join(t.TempDir(), "outside"))

	// No bound directory yet: the pin group has no place to offer.
	code, _, stderr := cdPath(t, app, navRequest(t, "cd", lsFlags{}, "pin"))
	if code != constants.ExitNotFound || !strings.Contains(stderr, "no bound directory governs") {
		t.Fatalf("kae cd pin with nothing bound = %d %q", code, stderr)
	}

	bound := chdirTo(t, filepath.Join(filepath.Dir(outside), "main-app"))
	if code, out := captureStdout(t, func() int {
		return runPin(context.Background(), app, commonOpts{Format: formatText}, "main", modeShared, false)
	}); code != constants.ExitOK {
		t.Fatalf("runPin: %s", out)
	}
	chdirTo(t, outside)
	mkdirs(t, filepath.Join(outside, ".claude"))
	for _, tc := range []struct {
		name string
		f    lsFlags
		pos  []string
		want []string
	}{
		{"pin, not inside a bound directory", lsFlags{}, []string{"pin"}, []string{
			"kae cd pin has no current place here; choose one:", "kae cd pin --at 1  " + bound,
		}},
		{"no level below", lsFlags{below: true}, []string{"claude"}, []string{
			"kae cd claude --below has no current place here; choose one:",
			"kae cd claude --at 1  " + filepath.Join(app.Env.Home, ".claude"),
			"kae cd claude --at 3  " + filepath.Join(outside, ".claude"),
		}},
		// With --root only the places that have a root are offered, as the path
		// the command reaches.
		{"no level below, root", lsFlags{below: true, root: true}, []string{"claude"}, []string{
			"kae cd claude --at 3 --root  " + outside + "\n",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, got, stderr := cdPath(t, app, navRequest(t, "cd", tc.f, tc.pos...))
			if code != constants.ExitUsage || got != "" {
				t.Fatalf("exit %d stdout %q; want usage:\n%s", code, got, stderr)
			}
			for _, want := range tc.want {
				if !strings.Contains(stderr, want) {
					t.Fatalf("stderr missing %q:\n%s", want, stderr)
				}
			}
			if tc.f.root && strings.Contains(stderr, "--at 1") {
				t.Fatalf("the user level has no root and must not be offered with --root:\n%s", stderr)
			}
		})
	}
	// ls --current keeps its not_found.
	if code, _ := pickPath(t, app, lsRequest{target: constants.PlaceGroupPin, current: true}); code != constants.ExitNotFound {
		t.Fatalf("kae ls pin --current = %d, want not_found", code)
	}

	// --root with no place that has a root: nothing to offer.
	chdirTo(t, filepath.Join(t.TempDir(), "bare"))
	code, _, stderr = cdPath(t, app, navRequest(t, "cd", lsFlags{below: true, root: true}, "claude"))
	if code != constants.ExitNotFound || !strings.Contains(stderr, "no existing place to choose") {
		t.Fatalf("--below --root with no project level = %d %q", code, stderr)
	}
}
