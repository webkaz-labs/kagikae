package cmd

import (
	"context"
	"errors"
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
