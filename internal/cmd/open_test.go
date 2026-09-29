package cmd

import (
	"context"
	"errors"
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
)

// navRequest parses an open or cd command line as the command would.
func navRequest(t *testing.T, verb string, f lsFlags, pos ...string) lsRequest {
	t.Helper()
	req, code := parsePlaceArgs(verb, "usage", f, pos)
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
	if code != constants.ExitError || !strings.Contains(stderr, "xdg-open") || !strings.Contains(stderr, "boom") {
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
		{"--at", lsFlags{at: atFlag{n: 2, set: true}}, []string{"claude"}, filepath.Join(cwd, ".claude")},
		{"--at --root", lsFlags{at: atFlag{n: 2, set: true}, root: true}, []string{"claude"}, cwd},
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
		{"invalid account", lsFlags{isolated: true}, []string{"claude", "../x"}, "invalid account"},
		{"too many words", lsFlags{}, []string{"claude", "a", "b"}, "usage"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var code int
			_, stderr := captureStderr(t, func() int {
				_, code = parsePlaceArgs("open", "usage", tc.f, tc.pos)
				return code
			})
			if code != constants.ExitUsage || !strings.Contains(stderr, tc.want) {
				t.Fatalf("exit %d, stderr %q; want usage naming %q", code, stderr, tc.want)
			}
		})
	}
	// No target and a level selector without a tool are resolution's to decide.
	for _, f := range []lsFlags{{}, {project: true}, {below: true, root: true}} {
		if req, code := parsePlaceArgs("cd", "usage", f, nil); code != constants.ExitOK || !req.current || req.target != "" {
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
	// cwd's own .claude/ makes claude relevant to bare ls: user 1, project 2,
	// below 3 and 4.
	mkdirs(t, filepath.Join(cwd, ".claude"), filepath.Join(cwd, "a", ".claude"), filepath.Join(cwd, "b", ".claude"))
	for _, tc := range []struct {
		name string
		verb string
		f    lsFlags
		pos  []string
		want []string
	}{
		{"no target", "open", lsFlags{}, nil, []string{
			"kae open needs a target; choose one:", "kae open kae --at 2  " + app.Paths.DataDir,
			"kae open claude --at 3  " + filepath.Join(cwd, "a", ".claude"),
		}},
		{"kae has no current place", "cd", lsFlags{}, []string{"kae"}, []string{
			"kae cd kae matches 3 places", "kae cd kae --at 1  " + app.Paths.ConfigDir,
		}},
		{"several below", "cd", lsFlags{below: true}, []string{"claude"}, []string{
			"kae cd claude --below matches 2 places",
			"kae cd claude --at 3  " + filepath.Join(cwd, "a", ".claude"),
			"kae cd claude --at 4  " + filepath.Join(cwd, "b", ".claude"),
		}},
		{"several below, explicit, root", "open", lsFlags{below: true, root: true, shared: true}, []string{"claude"}, []string{
			"kae open -s claude --at 4 --root  " + filepath.Join(cwd, "b", ".claude"),
		}},
		{"level without a bound tool", "cd", lsFlags{project: true}, nil, []string{
			"kae cd --project needs a tool: no tool is bound here; choose one:",
			"kae cd claude --project", "kae cd codex --project",
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
	if code != constants.ExitUsage || !strings.Contains(stderr, "2 tools are bound here") || !strings.Contains(stderr, "kae cd codex --project") {
		t.Fatalf("two bound tools = %d %q", code, stderr)
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

// A level selector without a tool uses the one bound tool; an explicit target
// without a current place exits 7 as ls --current does; so does a place whose
// directory is missing, which neither open nor cd can reach.
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
		{"no repository", lsFlags{}, []string{"repo"}, "no current place for kae cd repo"},
		{"no level below", lsFlags{below: true}, []string{"claude"}, "no current place for kae cd claude --below"},
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
	} {
		code, stderr := captureStderr(t, func() int { return Root(append([]string{"cd"}, tc.args...)) })
		if code != constants.ExitUsage || !strings.Contains(stderr, tc.want) || !strings.Contains(stderr, "kae shell function") {
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
// the shell for a path with a space and a leading dash, passes its words intact;
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
			target := filepath.Join(root, "-dir with space")
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
