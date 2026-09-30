// Tests of the shell function and the cd entry points around it.

package cmd

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/constants"
)

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
