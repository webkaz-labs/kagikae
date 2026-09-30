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
