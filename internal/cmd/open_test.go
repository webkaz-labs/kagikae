package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/paths"
	"github.com/webkaz-labs/kagikae/internal/runner"
	"github.com/webkaz-labs/kagikae/internal/testutil/runnertest"
	"github.com/webkaz-labs/kagikae/internal/textui"
)

// navRequest parses an open or cd command line as the command would.
func navRequest(t *testing.T, verb string, f lsFlags, pos ...string) lsRequest {
	t.Helper()
	req, code := parsePlaceArgs(verb, f, pos)
	if code != constants.ExitOK {
		t.Fatalf("parse %s %+v %v: exit %d", verb, f, pos, code)
	}
	return req
}

// cdPath runs the hidden `kae __cd` core and returns its exit code, stdout and
// stderr.
func cdPath(t *testing.T, app *App, req lsRequest) (int, string, string) {
	t.Helper()
	code, stdout, stderr := captureBoth(t, func() int {
		return runCdPath(context.Background(), app, commonOpts{Format: formatText}, req)
	})
	return code, strings.TrimSuffix(stdout, "\n"), stderr
}

// withOpener makes LookPath find exactly name.
func withOpener(app *App, name string) {
	app.Env.LookPath = func(file string) (string, error) {
		if file == name {
			return "/usr/bin/" + file, nil
		}
		return "", errors.New("not found")
	}
}

// The opener is the platform's, through the runner seam, with the path as its
// only argument; on success nothing is printed.
func TestOpenRunsThePlatformOpener(t *testing.T) {
	for goos, opener := range map[string]string{"darwin": "open", "linux": "xdg-open"} {
		t.Run(goos, func(t *testing.T) {
			app := testApp(t, nil)
			app.Env.GOOS = goos
			withOpener(app, opener)
			chdirTo(t, t.TempDir())
			home := filepath.Join(app.Env.Home, ".claude")
			mkdirs(t, home)
			fake := &runnertest.Fake{}
			var code int
			var stdout, stderr string
			runner.With(fake, func() {
				code, stdout, stderr = captureBoth(t, func() int {
					return runOpen(context.Background(), app, commonOpts{Format: formatText}, navRequest(t, "open", lsFlags{}, "claude"))
				})
			})
			mustExit(t, constants.ExitOK, code, stdout+stderr)
			if fake.Name != opener || !slices.Equal(fake.Args, []string{home}) {
				t.Fatalf("ran %s %v, want %s %s", fake.Name, fake.Args, opener, home)
			}
			if stdout != "" || stderr != "" {
				t.Fatalf("a successful open prints nothing: %q %q", stdout, stderr)
			}
		})
	}
}

// Without the opener — not found, or no opener for the platform — the path goes
// to stdout, a warning to stderr, and the exit code is 0. A failing opener is an
// error.
func TestOpenWithoutAnOpenerPrintsThePath(t *testing.T) {
	for _, goos := range []string{"linux", "windows"} {
		app := testApp(t, nil)
		app.Env.GOOS = goos
		chdirTo(t, t.TempDir())
		mkdirs(t, app.Paths.DataDir)
		fake := &runnertest.Fake{}
		var code int
		var stdout, stderr string
		runner.With(fake, func() {
			code, stdout, stderr = captureBoth(t, func() int {
				return runOpen(context.Background(), app, commonOpts{Format: formatText}, navRequest(t, "open", lsFlags{at: atFlag{n: 2, set: true}}, "kae"))
			})
		})
		mustExit(t, constants.ExitOK, code, stdout+stderr)
		if stdout != app.Paths.DataDir+"\n" {
			t.Fatalf("%s: stdout = %q, want the path", goos, stdout)
		}
		if !strings.Contains(stderr, "warning: no ") || fake.Name == "xdg-open" || fake.Name == "open" {
			t.Fatalf("%s: stderr = %q, ran %q", goos, stderr, fake.Name)
		}
	}

	app := testApp(t, nil)
	withOpener(app, "xdg-open")
	chdirTo(t, t.TempDir())
	mkdirs(t, app.Paths.ConfigDir)
	var code int
	var stderr string
	runner.With(&runnertest.Fake{Code: 4, Stderr: "boom"}, func() {
		code, _, stderr = captureBoth(t, func() int {
			return runOpen(context.Background(), app, commonOpts{Format: formatText}, navRequest(t, "open", lsFlags{at: atFlag{n: 1, set: true}}, "kae"))
		})
	})
	if code != constants.ExitError || !strings.Contains(stderr, "xdg-open") || !strings.Contains(stderr, "exited 4") {
		t.Fatalf("a failing opener = %d %q", code, stderr)
	}
}

// open and cd reach the path `kae ls <same> --current` or `--at N` prints, for
// every target form they share with ls.
func TestCdPathMatchesLs(t *testing.T) {
	app := testApp(t, nil)
	root := chdirTo(t, t.TempDir())
	scratchGit(t, root, "init", "-q")
	cwd := chdirTo(t, filepath.Join(root, "sub"))
	mkdirs(t, filepath.Join(cwd, ".claude"), filepath.Join(cwd, "a", ".codex"), filepath.Join(root, ".codex"),
		filepath.Join(app.Env.Home, ".claude"), app.Paths.GlobalIsolatedHomeDir(constants.ToolClaude, "side"),
		app.Paths.ConfigDir, app.Paths.DataDir, app.Paths.StateDir)
	writeFile(t, filepath.Join(cwd, "a", ".codex", "config.toml"), "\n")
	for _, tc := range []struct {
		name string
		f    lsFlags
		pos  []string
		want string
	}{
		{"user level", lsFlags{}, []string{"claude"}, filepath.Join(app.Env.Home, ".claude")},
		{"prefix", lsFlags{}, []string{"cl"}, filepath.Join(app.Env.Home, ".claude")},
		{"project", lsFlags{project: true}, []string{"claude"}, filepath.Join(cwd, ".claude")},
		{"project root", lsFlags{project: true, root: true}, []string{"claude"}, cwd},
		{"codex project", lsFlags{project: true}, []string{"codex"}, filepath.Join(root, ".codex")},
		{"below", lsFlags{below: true}, []string{"codex"}, filepath.Join(cwd, "a", ".codex")},
		{"below root", lsFlags{below: true, root: true}, []string{"codex"}, filepath.Join(cwd, "a")},
		{"--at", lsFlags{at: atFlag{n: 3, set: true}}, []string{"claude"}, filepath.Join(cwd, ".claude")},
		{"--at --root", lsFlags{at: atFlag{n: 3, set: true}, root: true}, []string{"claude"}, cwd},
		{"-s", lsFlags{shared: true}, []string{"claude"}, filepath.Join(app.Env.Home, ".claude")},
		{"-i", lsFlags{isolated: true}, []string{"claude", "side"}, app.Paths.GlobalIsolatedHomeDir(constants.ToolClaude, "side")},
		{"--home", lsFlags{home: true}, []string{"claude"}, filepath.Join(app.Env.Home, ".claude")},
		{"repo", lsFlags{}, []string{"repo"}, root},
		{"kae --at", lsFlags{at: atFlag{n: 3, set: true}}, []string{"kae"}, app.Paths.StateDir},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ls := tc.f
			ls.current = !ls.at.set
			lsReq, code := parseLsRequest(ls, tc.pos)
			if code != constants.ExitOK {
				t.Fatalf("ls parse: %d", code)
			}
			lsCode, lsPath := pickPath(t, app, lsReq)
			code, got, stderr := cdPath(t, app, navRequest(t, "cd", tc.f, tc.pos...))
			if code != constants.ExitOK || got != tc.want || lsCode != constants.ExitOK || lsPath != got {
				t.Fatalf("kae cd = %d %q (%s), kae ls = %d %q; want %q", code, got, stderr, lsCode, lsPath, tc.want)
			}
		})
	}
}

// The ls refusals that apply to open and cd, and the ones only they have.
func TestNavigateUsageErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		f    lsFlags
		pos  []string
		want string
	}{
		{"account is not a place", lsFlags{}, []string{"account"}, "not places"},
		{"account with --at", lsFlags{at: atFlag{n: 1, set: true}}, []string{"account"}, "not places"},
		{"-i with --home", lsFlags{isolated: true, home: true}, []string{"claude", "side"}, "-i resolves the user level"},
		{"account without -i", lsFlags{}, []string{"claude", "side"}, "needs -i: kae open -i claude side"},
		{"account with -s", lsFlags{shared: true}, []string{"claude", "side"}, "with -i"},
		{"-i without account", lsFlags{isolated: true}, []string{"claude"}, "kae open -i <tool> <account>"},
		{"-s and -i", lsFlags{shared: true, isolated: true}, []string{"claude"}, "mutually exclusive"},
		{"-s on a group", lsFlags{shared: true}, []string{"repo"}, "not a tool"},
		{"two selectors", lsFlags{project: true, below: true}, []string{"claude"}, "give one"},
		{"selector on a group", lsFlags{project: true}, []string{"repo"}, "needs a tool target: kae open <tool> --project"},
		{"--root without a level", lsFlags{root: true}, []string{"claude"}, "add --project or --below"},
		{"--root with --home", lsFlags{root: true, home: true}, []string{"claude"}, "add --project or --below"},
		{"selector with --at", lsFlags{project: true, at: atFlag{n: 1, set: true}}, []string{"claude"}, "--at a place number; give one"},
		{"--at without a target", lsFlags{at: atFlag{n: 1, set: true}}, nil, "needs a target"},
		{"ambiguous prefix", lsFlags{}, []string{"c"}, "ambiguous open target"},
		{"unknown target", lsFlags{}, []string{"repos"}, "unknown open target"},
		// account is refused, so a near miss of it suggests nothing.
		{"no account suggestion", lsFlags{}, []string{"acount"}, "(targets: pin, repo, kae, or a tool"},
		{"invalid account", lsFlags{isolated: true}, []string{"claude", "../x"}, "invalid account"},
		{"too many words", lsFlags{}, []string{"claude", "a", "b"}, "usage"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var code int
			_, stderr := captureStderr(t, func() int {
				_, code = parsePlaceArgs("open", tc.f, tc.pos)
				return code
			})
			if code != constants.ExitUsage || !strings.Contains(stderr, tc.want) {
				t.Fatalf("exit %d, stderr %q; want usage naming %q", code, stderr, tc.want)
			}
			if strings.Contains(stderr, `did you mean "account"`) {
				t.Fatalf("open suggested account, which it refuses: %q", stderr)
			}
		})
	}
	// ls still suggests it.
	_, stderr := captureStderr(t, func() int {
		_, code := parseLsRequest(lsFlags{}, []string{"acount"})
		return code
	})
	if !strings.Contains(stderr, `did you mean "account"`) {
		t.Fatalf("ls acount: %q", stderr)
	}
	// No target and a level selector without a tool are resolution's to decide.
	for _, f := range []lsFlags{{}, {project: true}, {below: true, root: true}} {
		if req, code := parsePlaceArgs("cd", f, nil); code != constants.ExitOK || !req.current || req.target != "" {
			t.Fatalf("kae cd %+v: %+v exit %d", f, req, code)
		}
	}
	// They print no report, so --json is refused before any environment is read,
	// and neither takes ls's --current or --pins.
	for _, args := range [][]string{
		{"open", "claude", "--json"},
		{cdPathCommand, "claude", "--format", "json"},
		{"open", "claude", "--current"},
		{cdPathCommand, "--pins"},
	} {
		code, stderr := captureStderr(t, func() int { return Root(args) })
		if code != constants.ExitUsage || stderr == "" {
			t.Fatalf("%v = %d %q, want a usage error", args, code, stderr)
		}
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

func cdPathOrOpen(t *testing.T, app *App, verb string, req lsRequest) (int, string, string) {
	t.Helper()
	if verb == "cd" {
		code, stdout, stderr := cdPath(t, app, req)
		return code, stdout, stderr
	}
	return captureBoth(t, func() int {
		return runOpen(context.Background(), app, commonOpts{Format: formatText}, req)
	})
}

// A level selector without a tool uses the one bound tool; a target with no
// place at all exits 7, and so does a place whose directory is missing, which
// neither open nor cd can reach.
func TestNavigateResolution(t *testing.T) {
	app := testApp(t, nil)
	bound := chdirTo(t, filepath.Join(t.TempDir(), "main-app"))
	writeFile(t, filepath.Join(bound, fragmentRelPath),
		fragModePrefix+modeShared+"\n"+fragAccountPrefix+"claude=main\n"+fragAccountPrefix+"agy=main\n")
	mkdirs(t, filepath.Join(bound, ".claude"), filepath.Join(app.Env.Home, ".claude"))
	sub := chdirTo(t, filepath.Join(bound, "sub"))
	mkdirs(t, filepath.Join(sub, ".claude"))

	// claude is the one place tool bound here (agy has no places).
	if code, got, stderr := cdPath(t, app, navRequest(t, "cd", lsFlags{project: true})); code != 0 || got != filepath.Join(sub, ".claude") {
		t.Fatalf("kae cd --project = %d %q %s", code, got, stderr)
	}
	if code, got, stderr := cdPath(t, app, navRequest(t, "cd", lsFlags{home: true})); code != 0 || got != filepath.Join(app.Env.Home, ".claude") {
		t.Fatalf("kae cd --home = %d %q %s", code, got, stderr)
	}
	if code, got, stderr := cdPath(t, app, navRequest(t, "cd", lsFlags{}, "pin")); code != 0 || got != bound {
		t.Fatalf("kae cd pin = %d %q %s", code, got, stderr)
	}

	for _, tc := range []struct {
		name string
		f    lsFlags
		pos  []string
		want string
	}{
		// No candidate place at all: not_found.
		{"no repository", lsFlags{}, []string{"repo"}, "no current place for kae cd repo here"},
		{"a tool without places", lsFlags{}, []string{"agy"}, "kae resolves no places for agy"},
		// The bound store was never created: the place is listed (missing).
		{"missing directory", lsFlags{}, []string{"claude"}, "does not exist"},
	} {
		code, got, stderr := cdPath(t, app, navRequest(t, "cd", tc.f, tc.pos...))
		if code != constants.ExitNotFound || got != "" || !strings.Contains(stderr, tc.want) {
			t.Fatalf("%s: %d %q %q; want not_found naming %q", tc.name, code, got, stderr, tc.want)
		}
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

// Without the shell function, `kae cd` reaches the binary: usage, and the
// command that does the same, with the words it was given.
func TestCdWithoutTheShellFunction(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, `cd "$(kae ls <target> --current)"`},
		{[]string{"claude"}, `cd "$(kae ls claude --current)"`},
		{[]string{"claude", "--project", "--root"}, `cd "$(kae ls claude --project --root --current)"`},
		{[]string{"kae", "--at", "2"}, `cd "$(kae ls kae --at 2)"`},
		{[]string{"--project"}, `cd "$(kae ls <target> --project --current)"`},
		{[]string{"-i", "claude", "it's"}, `cd "$(kae ls -i claude 'it'\''s' --current)"`},
		// A valued flag's value is not a target, and a format flag is not echoed.
		{[]string{"--at", "2"}, `cd "$(kae ls <target> --at 2)"`},
		{[]string{"kae", "-at=2"}, `cd "$(kae ls kae -at=2)"`},
		{[]string{"claude", "--json"}, `cd "$(kae ls claude --current)"`},
		// kae ls has no picker, so the suggestion prints the current place.
		{[]string{"claude", "--pick"}, `cd "$(kae ls claude --current)"`},
		{[]string{"--format", "json", "claude", "--config", "/p q"}, `cd "$(kae ls claude --config '/p q' --current)"`},
	} {
		code, stderr := captureStderr(t, func() int { return Root(append([]string{"cd"}, tc.args...)) })
		if code != constants.ExitUsage || !strings.Contains(stderr, tc.want) || !strings.Contains(stderr, "kae shell function") ||
			!strings.Contains(stderr, "kae completion fish | source") {
			t.Fatalf("kae cd %v = %d %q; want usage suggesting %s", tc.args, code, stderr, tc.want)
		}
	}
}

// The hidden entry stays out of help and of the completion command list.
func TestCdPathEntryIsHidden(t *testing.T) {
	_, help := captureStdout(t, func() int { return Root([]string{"help"}) })
	if strings.Contains(help, cdPathCommand) {
		t.Fatalf("%s leaked into help:\n%s", cdPathCommand, help)
	}
	if slices.Contains(completionCommands, cdPathCommand) {
		t.Fatalf("%s must not be in completionCommands", cdPathCommand)
	}
	for _, want := range []string{"kae open", "kae cd"} {
		if !strings.Contains(help, want) {
			t.Fatalf("help does not name %q:\n%s", want, help)
		}
	}
}

// Every output open and cd add — the printed path, the fallback, the candidate
// lists — carries paths, never a credential.
func TestNavigateNeverCarriesACredential(t *testing.T) {
	const canary = "sk-ant-oat01-NAVIGATE-CANARY-eeee"
	app := overlayTestApp(t)
	captureClaude(t, app, "main", canary)
	bound := chdirTo(t, filepath.Join(t.TempDir(), "main-app"))
	if code, out := captureStdout(t, func() int {
		return runPin(context.Background(), app, commonOpts{Format: formatText}, "main", modeIsolated, false)
	}); code != constants.ExitOK {
		t.Fatalf("runPin: %s", out)
	}
	store := app.Paths.IsolatedConfigDir(paths.PinID(bound), constants.ToolClaude, "main")
	stored := dirCredFile(app, constants.ToolClaude, "main", store)
	if !strings.Contains(readFile(t, stored), canary) {
		t.Fatalf("fixture does not place the canary in the bound store (%s); this test would pass vacuously", stored)
	}
	mkdirs(t, store, filepath.Join(bound, "a", ".claude"), filepath.Join(bound, "b", ".claude"))
	run := func(verb string, f lsFlags, pos ...string) (int, string, string) {
		return cdPathOrOpen(t, app, verb, navRequest(t, verb, f, pos...))
	}
	for _, tc := range []struct {
		verb string
		f    lsFlags
		pos  []string
		code int
		path string // a path the output must carry
	}{
		{"cd", lsFlags{}, []string{"claude"}, constants.ExitOK, store},
		{"open", lsFlags{}, []string{"claude"}, constants.ExitOK, store}, // no opener: the fallback
		{"cd", lsFlags{}, nil, constants.ExitUsage, store},
		{"open", lsFlags{below: true}, []string{"claude"}, constants.ExitUsage, filepath.Join(bound, "b", ".claude")},
	} {
		code, stdout, stderr := run(tc.verb, tc.f, tc.pos...)
		if code != tc.code || !strings.Contains(stdout+stderr, tc.path) {
			t.Fatalf("%s %v: %d; the place must be reported, or this canary proves nothing:\n%s\n%s", tc.verb, tc.pos, code, stdout, stderr)
		}
		if strings.Contains(stdout+stderr, canary) {
			t.Fatalf("%s %v leaked a credential value:\n%s\n%s", tc.verb, tc.pos, stdout, stderr)
		}
	}
	code, stderr := captureStderr(t, func() int { return CmdCd([]string{"claude"}) })
	if code != constants.ExitUsage || strings.Contains(stderr, canary) {
		t.Fatalf("kae cd without the function: %d %q", code, stderr)
	}
}

// shellFunctionFixture is a stand-in kae binary for the function tests: __cd
// answers from its first argument, and every other command echoes itself.
const shellFunctionFixture = `#!/bin/sh
if [ "$1" = ` + cdPathCommand + ` ]; then
  shift
  printf '%s\n' "$@" > "$KAE_TEST_ARGS.$1"
  case "$1" in
    ok) printf '%s\n' "$KAE_TEST_DIR" ;;
    empty) ;;
    *) echo "kae: no current place for kae cd $1 here" >&2; exit 7 ;;
  esac
  exit 0
fi
printf 'binary:%s\n' "$*"
`

// The kae shell function, sourced as rc eval and the mise hook do: kae cd moves
// the shell for a path with a space, passes its words intact;
// a failing resolve leaves PWD, returns its code and keeps its stderr; every
// other command reaches the binary. fish is best-effort and skipped without it.
func TestShellFunctionBehaviour(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			bin, err := exec.LookPath(shell)
			if err != nil {
				if shell == "bash" {
					t.Fatal(err)
				}
				t.Skipf("%s unavailable; behavioural coverage skipped", shell)
			}
			root := t.TempDir()
			binDir := filepath.Join(root, "bin")
			// A path with a space. A path starting with `-` is not reachable: __cd
			// prints absolute paths, so the function's `--` guard is defence only.
			target := filepath.Join(root, "dir with space")
			start := filepath.Join(root, "start")
			mkdirs(t, binDir, target, start)
			writeFile(t, filepath.Join(binDir, "kae"), shellFunctionFixture)
			if err := os.Chmod(filepath.Join(binDir, "kae"), 0o755); err != nil {
				t.Fatal(err)
			}
			argsFile := filepath.Join(root, "args")
			var body string
			args := []string{"--noprofile", "--norc"}
			switch shell {
			case "zsh":
				args = []string{"-f"}
				body = "compdef() { :; }\n"
			case "fish":
				args = []string{"--no-config"}
			}
			if shell == "fish" {
				body += completionEvalScript(shell) + `
cd '` + start + `'
kae cd ok -i claude 'a b' --at 2
echo "pwd=$PWD"
kae cd bogus
echo "code=$status"
echo "pwd=$PWD"
kae cd empty
echo "code=$status"
echo "pwd=$PWD"
kae ls pin
`
			} else {
				body += completionEvalScript(shell) + `
cd "` + start + `"
kae cd ok -i claude 'a b' --at 2
echo "pwd=$PWD"
kae cd bogus
echo "code=$?"
echo "pwd=$PWD"
kae cd empty
echo "code=$?"
echo "pwd=$PWD"
kae ls pin
`
			}
			script := filepath.Join(root, "run."+shell)
			writeFile(t, script, body)
			cmd := exec.Command(bin, append(args, script)...)
			cmd.Env = []string{
				"PATH=" + binDir + ":" + os.Getenv("PATH"), "HOME=" + root, "XDG_CONFIG_HOME=" + root,
				"XDG_DATA_HOME=" + root, "XDG_STATE_HOME=" + root,
				"KAE_TEST_DIR=" + target, "KAE_TEST_ARGS=" + argsFile,
			}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%s: %v\n%s", shell, err, out)
			}
			// The shell spells PWD through its own view of the temp dir.
			realTarget, _ := filepath.EvalSymlinks(target)
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			pwd := func(line string) string {
				p := strings.TrimPrefix(line, "pwd=")
				if r, err := filepath.EvalSymlinks(p); err == nil {
					return r
				}
				return p
			}
			want := []string{"pwd", "stderr", "code=7", "pwd", "code=0", "pwd", "binary:ls pin"}
			if len(lines) != len(want) {
				t.Fatalf("%s output:\n%s", shell, out)
			}
			if pwd(lines[0]) != realTarget {
				t.Fatalf("%s: kae cd ok left PWD at %q, want %q:\n%s", shell, lines[0], realTarget, out)
			}
			if !strings.Contains(lines[1], "no current place for kae cd bogus") {
				t.Fatalf("%s: the resolver's stderr must reach the terminal:\n%s", shell, out)
			}
			if lines[2] != "code=7" || pwd(lines[3]) != realTarget {
				t.Fatalf("%s: a failing resolve must return its code and leave PWD:\n%s", shell, out)
			}
			if lines[4] != "code=0" || pwd(lines[5]) != realTarget {
				t.Fatalf("%s: an empty path must not move the shell:\n%s", shell, out)
			}
			if lines[6] != "binary:ls pin" {
				t.Fatalf("%s: other commands must reach the binary:\n%s", shell, out)
			}
			if got := strings.Split(strings.TrimSpace(readFile(t, argsFile+".ok")), "\n"); !slices.Equal(got, []string{"ok", "-i", "claude", "a b", "--at", "2"}) {
				t.Fatalf("%s: the hidden entry got %q, want the words intact", shell, got)
			}
		})
	}
}

// The kae shell function is emitted only where the output is sourced in the
// current shell (rc eval, the mise hook, print-only); a completion file — the
// --install file, a --refresh rewrite, a packaged resource (--no-function) —
// holds the completion alone, and sourcing one defines no kae function.
func TestCompletionFileHoldsNoShellFunction(t *testing.T) {
	marker := "command kae " + cdPathCommand
	for _, shell := range []string{"bash", "zsh", "fish"} {
		script, _ := completionScript(shell)
		if strings.Contains(script, marker) || !strings.Contains(completionEvalScript(shell), marker) {
			t.Fatalf("%s: only the eval script may define the function", shell)
		}
		code, out := captureStdout(t, func() int { return CmdCompletion(context.Background(), []string{shell}) })
		if code != constants.ExitOK || out != completionEvalScript(shell) {
			t.Fatalf("kae completion %s must print the completion and the function", shell)
		}
		code, out = captureStdout(t, func() int { return CmdCompletion(context.Background(), []string{shell, "--no-function"}) })
		if code != constants.ExitOK || out != script {
			t.Fatalf("kae completion %s --no-function must print the completion alone", shell)
		}

		app := testApp(t, nil)
		code, out = captureStdout(t, func() int {
			return applyCompletionInstall(app, commonOpts{Format: formatText}, shell, script, installFpath)
		})
		mustExit(t, constants.ExitOK, code, out)
		path, _, _ := completionTarget(app.Env, shell)
		if got := readFile(t, path); got != script {
			t.Fatalf("%s: the registered file must be the completion alone", shell)
		}
		// Sourcing the file, as the shell does when it loads it, defines no kae.
		var probe []string
		switch shell {
		case "bash":
			probe = []string{"--noprofile", "--norc", "-c", `source "$1"; if declare -F kae >/dev/null; then echo defined; else echo none; fi`, "probe", path}
		case "zsh":
			probe = []string{"-f", "-c", `compdef() { :; }; source "$1"; if (( $+functions[kae] )); then echo defined; else echo none; fi`, "probe", path}
		default:
			continue // fish: best-effort, and not installed on every machine
		}
		bin, err := exec.LookPath(shell)
		if err != nil {
			if shell == "bash" {
				t.Fatal(err)
			}
			continue
		}
		cmd := exec.Command(bin, probe...)
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + app.Env.Home}
		got, err := cmd.CombinedOutput()
		if err != nil || strings.TrimSpace(string(got)) != "none" {
			t.Fatalf("%s: sourcing the registered file: %v %q", shell, err, got)
		}
	}
	for _, args := range [][]string{{"bash", "--install", "--no-function"}, {"--refresh", "--no-function"}} {
		code, _ := captureStderr(t, func() int { return CmdCompletion(context.Background(), args) })
		if code != constants.ExitUsage {
			t.Fatalf("kae completion %v = %d, want usage", args, code)
		}
	}
}

// A user's `alias kae=…` must not break the eval: `kae() {` is alias-expanded
// (zsh then rejects the whole script, completion included), `function kae {` is
// not. bash reads its script as an interactive shell does its rc.
func TestShellFunctionSurvivesAnAlias(t *testing.T) {
	for _, shell := range []string{"bash", "zsh"} {
		t.Run(shell, func(t *testing.T) {
			bin, err := exec.LookPath(shell)
			if err != nil {
				if shell == "bash" {
					t.Fatal(err)
				}
				t.Skipf("%s unavailable", shell)
			}
			root := t.TempDir()
			binDir := filepath.Join(root, "bin")
			target := filepath.Join(root, "dir with space")
			mkdirs(t, binDir, target)
			writeFile(t, filepath.Join(binDir, "kae"), shellFunctionFixture)
			if err := os.Chmod(filepath.Join(binDir, "kae"), 0o755); err != nil {
				t.Fatal(err)
			}
			evalFile := filepath.Join(root, "eval."+shell)
			writeFile(t, evalFile, completionEvalScript(shell))
			args := []string{"--noprofile", "--norc", "-i"}
			registered := `complete -p kae >/dev/null && echo registered`
			// -i alone does not make bash expand aliases in a script it reads
			// (measured with bash 3.2: the kae() form passed with -i alone), so ask for it too.
			body := "shopt -s expand_aliases\n"
			if shell == "zsh" {
				args = []string{"-f"}
				registered = `(( $+functions[_kae] )) && echo registered`
				body = "compdef() { :; }\n"
			}
			body += `alias kae='kae --no-color'
eval "$(cat '` + evalFile + `')"
echo "eval=$?"
` + registered + `
\kae cd ok
echo "pwd=$PWD"
`
			script := filepath.Join(root, "alias."+shell)
			writeFile(t, script, body)
			cmd := exec.Command(bin, append(args, script)...)
			cmd.Env = []string{
				"PATH=" + binDir + ":" + os.Getenv("PATH"), "HOME=" + root, "TERM=dumb",
				"KAE_TEST_DIR=" + target, "KAE_TEST_ARGS=" + filepath.Join(root, "args"),
			}
			var stderr strings.Builder
			cmd.Stderr = &stderr
			out, _ := cmd.Output()
			realTarget, _ := filepath.EvalSymlinks(target)
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			if len(lines) != 3 || lines[0] != "eval=0" || lines[1] != "registered" {
				t.Fatalf("%s with an alias: eval or registration failed:\n%s\nstderr:\n%s", shell, out, stderr.String())
			}
			got := strings.TrimPrefix(lines[2], "pwd=")
			if r, err := filepath.EvalSymlinks(got); err == nil {
				got = r
			}
			if got != realTarget {
				t.Fatalf("%s: \\kae cd left PWD at %q:\n%s\nstderr:\n%s", shell, got, out, stderr.String())
			}
		})
	}
}

// unstartable is a runner whose Launch cannot start the program.
type unstartable struct{ runnertest.Fake }

func (u *unstartable) Launch(context.Context, string, ...string) (int, error) {
	return 1, errors.New("exec: no such file")
}

// An opener that cannot be started is reported once, in one line.
func TestOpenReportsAnUnstartableOpenerOnce(t *testing.T) {
	app := testApp(t, nil)
	withOpener(app, "xdg-open")
	chdirTo(t, t.TempDir())
	mkdirs(t, app.Paths.ConfigDir)
	var code int
	var stderr string
	runner.With(&unstartable{}, func() {
		code, _, stderr = captureBoth(t, func() int {
			return runOpen(context.Background(), app, commonOpts{Format: formatText}, navRequest(t, "open", lsFlags{at: atFlag{n: 1, set: true}}, "kae"))
		})
	})
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	if code != constants.ExitError || len(lines) != 1 || !strings.Contains(lines[0], "no such file") || strings.Contains(stderr, "exited") {
		t.Fatalf("unstartable opener = %d %q", code, stderr)
	}
}

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

// The terminal seam says no unless a test injects one, and production's default
// asks textui.
func TestAppTerminalSeam(t *testing.T) {
	app := testApp(t, nil)
	if tty, ok := app.terminal(); ok || tty != nil {
		t.Fatalf("a test App has a terminal: %v %v", tty, ok)
	}
	app.openTerminal = func() (*textui.Terminal, bool) { return &textui.Terminal{}, true }
	if _, ok := app.terminal(); !ok {
		t.Fatal("an injected terminal was ignored")
	}
	if newApp("").openTerminal == nil {
		t.Fatal("production App has no terminal opener")
	}
}
