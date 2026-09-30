package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/preservation"
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
		{"preservation-verbs", []string{"preservation", ""}, []string{"list", "restore", "rm"}},
		{"preservation-restore", []string{"preservation", "restore", ""}, []string{"0123456789abcdef0123456789abcdef"}},
		{"preservation-rm", []string{"preservation", "rm", ""}, []string{"0123456789abcdef0123456789abcdef"}},
		{"preservation-list", []string{"preservation", "list", ""}, nil},
		{"preservation-finished", []string{"preservation", "restore", "0123456789abcdef0123456789abcdef", ""}, nil},
		{"flag-candidates", []string{"add", "--"}, []string{"--no-login"}},
		{"ls-targets", []string{"ls", ""}, []string{"account", "pin", "repo", "kae", "claude", "codex"}},
		{"ls-no-account-without-i", []string{"ls", "claude", ""}, nil},
		{"ls-isolated-tools", []string{"ls", "-i", ""}, []string{"claude", "codex"}},
		{"ls-isolated-account", []string{"ls", "-i", "claude", ""}, []string{"main", "side"}},
		{"ls-isolated-long-account", []string{"ls", "claude", "--isolated", ""}, []string{"main", "side"}},
		{"ls-shared-tools", []string{"ls", "-s", ""}, []string{"claude", "codex"}},
		{"ls-shared-no-account", []string{"ls", "-s", "claude", ""}, nil},
		{"open-targets", []string{"open", ""}, []string{"pin", "repo", "kae", "claude", "codex"}},
		{"cd-targets", []string{"cd", ""}, []string{"pin", "repo", "kae", "claude", "codex"}},
		{"cd-isolated-account", []string{"cd", "-i", "claude", ""}, []string{"main", "side"}},
		{"open-shared-tools", []string{"open", "-s", ""}, []string{"claude", "codex"}},
		{"open-no-account-without-i", []string{"open", "claude", ""}, nil},
		{"cd-at-value", []string{"cd", "claude", "--at", ""}, nil},
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
 ls-targets) printf '%s\n' account pin repo kae claude codex ;;
 place-targets) printf '%s\n' pin repo kae claude codex ;;
 preservations) printf '%s\n' 0123456789abcdef0123456789abcdef ;;
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
 case ls-targets
 printf '%s\n' account pin repo kae claude codex
 case place-targets
 printf '%s\n' pin repo kae claude codex
 case preservations
 printf '%s\n' 0123456789abcdef0123456789abcdef
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

// TestCompletionScriptsCompleteRelogin: `kae relogin <TAB>` offers the tools, and
// only at the first positional — the account is the binding's answer, never a word
// typed here, so a second slot would offer something the parser rejects.
//
// TestEveryPositionalCommandCompletes now makes the same assertion generically,
// and this test is kept for what that one structurally cannot see: it names
// "relogin" as a literal, so it still fires if the verb is dropped from
// completionCommands and positionalCommands together — the shape of gap that both
// table-driven guards are blind to.
func TestCompletionScriptsCompleteRelogin(t *testing.T) {
	if !slices.Contains(completionCommands, "relogin") {
		t.Fatal("relogin must be in completionCommands, or `kae <TAB>` never offers it")
	}
	for _, shell := range []string{"bash", "zsh", "fish"} {
		script, _ := completionScript(shell)
		caseExists := strings.Contains(script, "relogin)") || strings.Contains(script, "case relogin")
		if !caseExists {
			t.Errorf("%s completion has no case for relogin:\n%s", shell, script)
		}
	}
}

// And the router actually dispatches it: a command that completes but does not run
// is the same dead end from the other side.
//
// The unparseable flag is deliberate, and it is what keeps this test off the real
// environment: CmdRelogin calls parseCommon first and returns on its failure, so
// this path exits before newApp reads any config or state. A routed command answers
// with the flag error; an unrouted one falls to Root's default arm.
func TestRootDispatchesRelogin(t *testing.T) {
	_, out := captureStderr(t, func() int { return Root([]string{"relogin", "--not-a-flag-kae-defines"}) })
	if strings.Contains(out, "unknown command") {
		t.Fatalf("Root does not route relogin: %q", out)
	}
	if !strings.Contains(out, "not-a-flag-kae-defines") {
		t.Fatalf("expected the flag parser to answer, so nothing further ran: %q", out)
	}
}

// Completion reads metadata even when the secret backend is unavailable; it must
// neither reconcile interrupted records nor offer them as restoration targets.
func TestCompletePreservationIDs(t *testing.T) {
	app := testApp(t, nil)
	code, out := captureStdout(t, func() int { return runComplete(app, []string{"preservations", "restore"}) })
	if code != constants.ExitOK || out != "" {
		t.Fatalf("empty inventory: %d %q", code, out)
	}
	if _, err := os.Stat(app.Paths.PreservationsDir()); !os.IsNotExist(err) {
		t.Fatalf("completion created metadata: %v", err)
	}
	if err := os.MkdirAll(app.Paths.PreservationsDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	states := []string{constants.PreservationStateReady, constants.PreservationStatePending, constants.PreservationStateDeleting}
	var ids []string
	for i, state := range states {
		id := fmt.Sprintf("%032x", i+1)
		ids = append(ids, id)
		record := preservation.Record{
			SchemaVersion: constants.SchemaVersion, ID: id, CreatedAt: time.Unix(int64(i+1), 0), State: state, Size: 10, Digest: strings.Repeat("a", 64),
			Origin: preservation.Origin{
				Directory: app.Env.Home, Tool: constants.ToolClaude, BoundAccount: "main", Mode: constants.ModeShared, ConfigDir: app.Env.Home,
				Locator: preservation.Locator{Name: "credential", Kind: constants.KindFile, Target: filepath.Join(app.Env.Home, "credential")},
			},
		}
		data, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(app.Paths.PreservationsDir(), id+".json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct{ verb, want string }{{"restore", ids[0] + "\n"}, {"rm", ids[2] + "\n" + ids[1] + "\n" + ids[0] + "\n"}} {
		code, out := captureStdout(t, func() int { return runComplete(app, []string{"preservations", tc.verb}) })
		if code != constants.ExitOK || out != tc.want {
			t.Fatalf("%s: %d %q", tc.verb, code, out)
		}
	}
	if err := os.WriteFile(filepath.Join(app.Paths.PreservationsDir(), "invalid.json"), []byte("synthetic-secret-must-not-print"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out = captureStdout(t, func() int { return runComplete(app, []string{"preservations", "rm"}) })
	if code != constants.ExitError || out != "" {
		t.Fatalf("invalid metadata leaked: %d %q", code, out)
	}
}
