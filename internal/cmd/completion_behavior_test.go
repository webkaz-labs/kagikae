package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/constants"
)

func TestValuedFlagBackend(t *testing.T) {
	for _, command := range []string{"add", "run", "u", "mise", "env", "rollback"} {
		got := valuedFlagCompletions(command)
		if !slices.Contains(got, "--config") || !slices.Contains(got, "-config") {
			t.Fatalf("%s: missing config spellings: %v", command, got)
		}
		for _, boolean := range []string{"--json", "--no-login", "-i", "--force", "--write"} {
			if slices.Contains(got, boolean) {
				t.Fatalf("%s: boolean consumes a value: %s", command, boolean)
			}
		}
		code, out := captureStdout(t, func() int { return CmdComplete(t.Context(), []string{"valued-flags", command}) })
		if code != constants.ExitOK || strings.TrimSpace(out) != strings.Join(got, "\n") {
			t.Fatalf("backend %s: %d %q", command, code, out)
		}
	}
	for command, flag := range map[string]string{"add": "--identity", "run": "-P", "u": "--profile", "mise": "--mode", "rollback": "--to"} {
		if !slices.Contains(valuedFlagCompletions(command), flag) {
			t.Fatalf("%s missing %s", command, flag)
		}
	}
}

// Run the generated functions, not a Go reimplementation of their word parser.
// Only the shell's cursor interface and live candidate data are fixtures; flag
// arity comes from the same registrar the real completion backend uses.
func TestCompletionFlagValueRouting(t *testing.T) {
	cases := []struct {
		name  string
		words []string
		want  []string
	}{
		{"separate", []string{"env", "--config", "/p", "set", ""}, []string{"claude", "codex"}},
		{"single-dash", []string{"env", "-config", "/p", "set", ""}, []string{"claude", "codex"}},
		{"equals", []string{"env", "--config=/p", "set", ""}, []string{"claude", "codex"}},
		{"after-verb", []string{"env", "set", "--config", "/p", "claude", ""}, []string{"main", "side"}},
		{"short", []string{"use", "-P", "main", "claude", ""}, []string{"main", "side"}},
		{"alias", []string{"u", "--profile", "main", "claude", ""}, []string{"main", "side"}},
		{"boolean", []string{"add", "--no-login", ""}, []string{"claude", "codex"}},
		{"boolean-equals", []string{"add", "--no-login=false", ""}, []string{"claude", "codex"}},
		{"current-value", []string{"add", "--identity", ""}, nil},
		{"current-dash-value", []string{"add", "--identity", "--"}, nil},
		{"attached-current-value", []string{"add", "--identity=abc"}, nil},
		{"dash-value", []string{"add", "--identity", "-value", ""}, []string{"claude", "codex"}},
		{"account", []string{"account", "--config", "/p", "rm", "claude", ""}, []string{"main", "side"}},
		{"companion", []string{"companion", "add", "main", "--config", "/p", "git", ""}, []string{"email", "name"}},
		{"no-positionals", []string{"env", "--config", "/p", "list", ""}, nil},
		{"bare-dash-parser-policy", []string{"env", "--", "--config", "/p", "set", ""}, []string{"claude", "codex"}},
		{"flag-candidates", []string{"add", "--"}, []string{"--no-login"}},
	}
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			bin, err := exec.LookPath(shell)
			if err != nil {
				if shell == "bash" {
					t.Fatal(err)
				}
				t.Skipf("%s unavailable; behavioral coverage skipped", shell)
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) { runCompletionCase(t, bin, shell, tc.words, tc.want) })
			}
			if shell == "bash" {
				for _, tc := range []struct {
					line        string
					words, want []string
				}{
					{"kae env --config=/p set ", []string{"env", "--config", "=", "/p", "set", ""}, []string{"claude", "codex"}},
					{"kae env --config=", []string{"env", "--config", "=", ""}, nil},
					{"kae env --config= set ", []string{"env", "--config", "=", "set", ""}, []string{"claude", "codex"}},
					{"kae add --no-login=false ", []string{"add", "--no-login", "=", "false", ""}, []string{"claude", "codex"}},
					{"kae add --identity=main=side ", []string{"add", "--identity", "=", "main", "=", "side", ""}, []string{"claude", "codex"}},
					{"kae add --identity = ", []string{"add", "--identity", "=", ""}, []string{"claude", "codex"}},
					{"kae add --identity 'main side' ", []string{"add", "--identity", "main side", ""}, []string{"claude", "codex"}},
					{"kae add --identity main\\ side ", []string{"add", "--identity", "main side", ""}, []string{"claude", "codex"}},
				} {
					runCompletionCase(t, bin, shell, tc.words, tc.want, tc.line)
				}
				runCompletionCase(t, bin, shell, []string{"add", ""}, []string{"claude", "codex"}, "kae \\\nadd ")
				// COMP_POINT is a byte offset, and the suffix is after the cursor.
				prefix := "kae add --identity 日本 "
				runCompletionCase(t, bin, shell, []string{"add", "--identity", "日本", ""}, []string{"claude", "codex"}, prefix+"claude side", fmt.Sprint(len(prefix)))
			}
		})
	}
}

func runCompletionCase(t *testing.T, bin, shell string, words, want []string, sourceLine ...string) {
	t.Helper()
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	tokens := []string{"'kae'"}
	for _, word := range words {
		tokens = append(tokens, quote(word))
	}
	valued := quote(strings.Join(valuedFlagCompletions(words[0]), "\n"))
	script, ok := completionScript(shell)
	if !ok {
		t.Fatal(shell)
	}
	fixture := `kae() {
 case "$2" in
 valued-flags) printf '%s\n' ` + valued + ` ;;
 tools) printf '%s\n' claude codex ;;
 profiles) printf '%s\n' main ;;
 accounts) if [ "$3" = claude ]; then printf '%s\n' main side; fi ;;
 companion-knobs) if [ "$3" = git ]; then printf '%s\n' email name; fi ;;
 flags) printf '%s\n' --no-login ;;
 esac
}
`
	var invocation string
	args := []string{"--noprofile", "--norc"}
	switch shell {
	case "bash":
		line := strings.Join(tokens, " ")
		if len(sourceLine) > 0 {
			line = sourceLine[0]
		}
		point := fmt.Sprint(len(line))
		if len(sourceLine) > 1 {
			point = sourceLine[1]
		}
		fixture += "COMP_LINE=" + quote(line) + "\nCOMP_POINT=" + point + "\n"
		invocation = fmt.Sprintf("COMP_WORDS=(%s)\nCOMP_CWORD=%d\n_kae\nif [ ${#COMPREPLY[@]} -gt 0 ]; then printf '%%s\\n' \"${COMPREPLY[@]}\"; fi\n", strings.Join(tokens, " "), len(tokens)-1)
	case "zsh":
		args = []string{"-f"}
		fixture = "compdef() { :; }\ncompadd() { shift; printf '%s\\n' \"$@\"; }\n" + fixture
		invocation = fmt.Sprintf("words=(%s)\nCURRENT=%d\n_kae\n", strings.Join(tokens, " "), len(tokens))
	case "fish":
		args = []string{"--no-config"}
		fixture = `function kae
 switch $argv[2]
 case valued-flags
 printf '%s\n' ` + valued + `
 case tools
 printf '%s\n' claude codex
 case profiles
 printf '%s\n' main
 case accounts
 if test "$argv[3]" = claude; printf '%s\n' main side; end
 case companion-knobs
 if test "$argv[3]" = git; printf '%s\n' email name; end
 case flags
 printf '%s\n' --no-login
 end
end
function commandline
 if test "$argv[1]" = -opc; printf '%s\n' $test_tokens; else; printf '%s\n' $test_current; end
end
`
		invocation = "set -g test_tokens " + strings.Join(tokens[:len(tokens)-1], " ") + "\nset -g test_current " + tokens[len(tokens)-1] + "\n__kae_complete\n"
	}
	root := t.TempDir()
	path := filepath.Join(root, "completion."+shell)
	if err := os.WriteFile(path, []byte(fixture+script+"\n"+invocation), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, append(args, path)...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root, "XDG_CONFIG_HOME=" + root, "XDG_DATA_HOME=" + root, "XDG_STATE_HOME=" + root, "XDG_RUNTIME_DIR=" + root, "LC_ALL=en_US.UTF-8"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v: %s", shell, err, out)
	}
	got := strings.Fields(string(out))
	if !slices.Equal(got, want) {
		t.Fatalf("%s %q: got %q, want %q", shell, words, got, want)
	}
}
