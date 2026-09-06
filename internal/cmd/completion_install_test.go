package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/constants"
)

// seedAccountMeta writes a minimal account.toml under the temp-HOME accounts
// dir so the completion backend has live candidates to list.
func seedAccountMeta(t *testing.T, app *App, tool, name string) {
	t.Helper()
	dir := filepath.Join(app.Paths.AccountsDir(), tool, name)
	if err := account.Save(dir, account.Account{Version: 1, Tool: tool, Name: name}); err != nil {
		t.Fatal(err)
	}
}

func TestCompleteBackendKinds(t *testing.T) {
	app := testApp(t, nil)
	writeConfigFile(t, app, `
default_profile = "main"
[profiles.main]
accounts = { claude = "alice", codex = "alice" }
[profiles.side]
accounts = { claude = "bob" }
`)
	seedAccountMeta(t, app, constants.ToolClaude, "alice")
	seedAccountMeta(t, app, constants.ToolClaude, "bob")
	seedAccountMeta(t, app, constants.ToolCodex, "alice")

	// commands lists public commands but never the hidden __complete backend.
	_, out := captureStdout(t, func() int { return runComplete(app, []string{"commands"}) })
	if !strings.Contains(out, "use\n") || !strings.Contains(out, "completion\n") {
		t.Fatalf("commands missing entries:\n%s", out)
	}
	if strings.Contains(out, "__complete") {
		t.Fatalf("commands must not expose the hidden backend:\n%s", out)
	}

	// tools lists every canonical tool, one per line.
	_, out = captureStdout(t, func() int { return runComplete(app, []string{"tools"}) })
	for _, tool := range constants.Tools {
		if !strings.Contains(out, tool+"\n") {
			t.Fatalf("tools missing %q:\n%s", tool, out)
		}
	}

	// profiles come from the loaded config.
	_, out = captureStdout(t, func() int { return runComplete(app, []string{"profiles"}) })
	if !strings.Contains(out, "main\n") || !strings.Contains(out, "side\n") {
		t.Fatalf("profiles missing entries:\n%s", out)
	}

	// accounts (no tool) lists all captured names, deduped across tools.
	_, out = captureStdout(t, func() int { return runComplete(app, []string{"accounts"}) })
	if strings.Count(out, "alice\n") != 1 || !strings.Contains(out, "bob\n") {
		t.Fatalf("accounts (all) wrong dedup:\n%s", out)
	}

	// accounts <tool> scopes to that tool.
	_, out = captureStdout(t, func() int { return runComplete(app, []string{"accounts", constants.ToolClaude}) })
	if !strings.Contains(out, "alice\n") || !strings.Contains(out, "bob\n") {
		t.Fatalf("accounts claude missing entries:\n%s", out)
	}
	_, out = captureStdout(t, func() int { return runComplete(app, []string{"accounts", constants.ToolCodex}) })
	if !strings.Contains(out, "alice\n") || strings.Contains(out, "bob\n") {
		t.Fatalf("accounts codex must be scoped (no bob):\n%s", out)
	}

	// companions lists every canonical companion id, one per line.
	_, out = captureStdout(t, func() int { return runComplete(app, []string{"companions"}) })
	for _, id := range constants.Companions {
		if !strings.Contains(out, id+"\n") {
			t.Fatalf("companions missing %q:\n%s", id, out)
		}
	}

	// companion-knobs <id> lists that companion's knob names from its Spec.
	_, out = captureStdout(t, func() int { return runComplete(app, []string{"companion-knobs", constants.CompanionGit}) })
	for _, want := range []string{"email\n", "name\n", "signingkey\n"} {
		if !strings.Contains(out, want) {
			t.Fatalf("companion-knobs git missing %q:\n%s", want, out)
		}
	}
	_, out = captureStdout(t, func() int { return runComplete(app, []string{"companion-knobs", constants.CompanionGH}) })
	if !strings.Contains(out, "GH_TOKEN\n") {
		t.Fatalf("companion-knobs gh missing GH_TOKEN:\n%s", out)
	}
	// An unknown companion id yields nothing (matches nothing, no error).
	_, out = captureStdout(t, func() int { return runComplete(app, []string{"companion-knobs", "bogus"}) })
	if strings.TrimSpace(out) != "" {
		t.Fatalf("companion-knobs for an unknown id must be empty:\n%s", out)
	}

	// flags <command> lists the command's flags (common + extras), drawn from the
	// same registrars the parser uses (flagspec.go), so the list cannot drift.
	_, out = captureStdout(t, func() int { return runComplete(app, []string{"flags", "add"}) })
	for _, want := range []string{"--no-login\n", "--restore\n", "--config\n", "--json\n"} {
		if !strings.Contains(out, want) {
			t.Fatalf("flags add missing %q:\n%s", want, out)
		}
	}
	_, out = captureStdout(t, func() int { return runComplete(app, []string{"flags", "run"}) })
	for _, want := range []string{"-s\n", "-i\n", "--env\n", "-P\n"} {
		if !strings.Contains(out, want) {
			t.Fatalf("flags run missing %q:\n%s", want, out)
		}
	}
	// An unknown command yields the common flags only (no extras leak).
	_, out = captureStdout(t, func() int { return runComplete(app, []string{"flags", "status"}) })
	if !strings.Contains(out, "--json\n") || strings.Contains(out, "--no-login\n") {
		t.Fatalf("flags status should be common-only:\n%s", out)
	}
}

func TestCompleteBackendErrors(t *testing.T) {
	app := testApp(t, nil)
	// An unknown kind exits non-zero.
	if code, _ := captureStdout(t, func() int { return runComplete(app, []string{"bogus"}) }); code != constants.ExitUsage {
		t.Fatalf("unknown kind must be usage error, got %d", code)
	}
	// No kind exits non-zero.
	if code, _ := captureStdout(t, func() int { return runComplete(app, nil) }); code != constants.ExitUsage {
		t.Fatalf("missing kind must be usage error, got %d", code)
	}
}

func TestCompleteBackendHiddenFromHelp(t *testing.T) {
	// __complete must not appear in `kae help` or in the completionCommands set.
	_, help := captureStdout(t, func() int { return Root([]string{"help"}) })
	if strings.Contains(help, "__complete") {
		t.Fatalf("__complete leaked into help:\n%s", help)
	}
	for _, c := range completionCommands {
		if c == "__complete" {
			t.Fatal("__complete must not be in completionCommands")
		}
	}
}

// TestCompletionScriptsCompleteFlags: each generated script offers flag-name
// completion (it calls `kae __complete flags`) when the current word is a flag.
func TestCompletionScriptsCompleteFlags(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		script, _ := completionScript(shell)
		if !strings.Contains(script, "kae __complete flags") {
			t.Fatalf("%s completion does not complete flag names:\n%s", shell, script)
		}
	}
}

// TestCompletionScriptsCompleteCompanion: each generated script wires the
// companion subcommand — its add/rm/list sub-verbs and the companion-id and
// knob argument positions — so `kae companion <TAB>` is not a dead end.
func TestCompletionScriptsCompleteCompanion(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		script, _ := completionScript(shell)
		for _, want := range []string{"add rm list", "__complete companions", "__complete companion-knobs"} {
			if !strings.Contains(script, want) {
				t.Fatalf("%s completion missing companion wiring %q:\n%s", shell, want, script)
			}
		}
	}
}

// positionalCommands says, for every command in completionCommands, whether it
// accepts a positional argument — true means `kae <cmd> <TAB>` must offer
// something, false means the command takes flags only. Each entry's comment is
// its positional shape — or, where the answer is not obvious from the shape, why
// (rollback). Not the usage line: the flags belong to `printHelp` and
// docs/CLI.md, and copying one here would make this a third hand-maintained copy
// of something those two already disagree on.
//
// Being keyed by completionCommands is the whole point, and the difference from
// subcommandVerbs: that table is opt-in, so a command missing from *both* it and
// the scripts is invisible to its guard — which is how the v0.10.0 companion gap
// and the `kae env` / `kae backup` gap (found 2026-08-06) each shipped. A command
// cannot be added to the router's completion set without being classified here,
// because TestEveryPositionalCommandCompletes checks the two sets match exactly.
//
// What it still cannot see, and neither can subcommandVerbs: a command dropped
// from completionCommands *and* from this map, since nothing machine-checks
// either against Root(). Adding that check by dispatching each command is not
// safe in a unit test — several commands reach newApp before a bad flag stops
// them, which would read the real environment. The remedy for the shape neither
// table sees is a test naming the verb literally, the way
// TestCompletionScriptsCompleteRelogin does for one with no sub-verbs to key on.
//
// What it deliberately does not claim: that a branch routes correctly. It proves
// the branch exists and emits candidates; whether the slots and array indices in
// it are right is TestCompletionPositionalRouting's question, per command.
var positionalCommands = map[string]bool{
	"init":       false,
	"edit":       false,
	"doctor":     true, // [<tool>]
	"add":        true, // <tool> [<account>]
	"use":        true, // [<profile> | <tool> <account>]
	"pin":        true, // [<profile> | <tool> <account>]
	"unpin":      false,
	"relogin":    true, // [<tool>]
	"run":        true, // [<profile> | <tool> <account>] -- <cmd>
	"env":        true, // <set|unset|list> ...
	"companion":  true, // <add|rm|list> ...
	"mise":       true, // init
	"accounts":   false,
	"ls":         false,
	"account":    true, // <rm|rename|set-identity> ...
	"profile":    true, // <save|set|unset|rm|default> ...
	"status":     false,
	"backup":     true,  // list
	"rollback":   false, // the backup id is --to's value, not a positional
	"completion": true,  // <bash|zsh|fish>
	"version":    false,
	"help":       false,
}

// TestEveryPositionalCommandCompletes: every command that takes a positional has
// a branch in all three generated scripts, and every command is classified.
//
// The converse is asserted too — a flags-only command must NOT have a branch —
// so a classification that goes stale is loud from either direction rather than
// silently weakening the first half.
func TestEveryPositionalCommandCompletes(t *testing.T) {
	for _, cmd := range completionCommands {
		if _, ok := positionalCommands[cmd]; !ok {
			t.Errorf("command %q is unclassified: say in positionalCommands whether it takes a positional, and give it a completion case if it does", cmd)
		}
	}
	for cmd := range positionalCommands {
		if !slices.Contains(completionCommands, cmd) {
			t.Errorf("positionalCommands classifies %q, which is not in completionCommands", cmd)
		}
	}
	for _, shell := range []string{"bash", "zsh", "fish"} {
		script, ok := completionScript(shell)
		if !ok {
			t.Fatalf("no completion script for %s", shell)
		}
		blocks := completionCaseBlocks(t, shell, script)
		for cmd, takesPositional := range positionalCommands {
			body, hasCase := blocks[cmd]
			switch {
			case takesPositional && !hasCase:
				t.Errorf("%s completion has no case for %q, so `kae %s <TAB>` is a dead end:\n%s", shell, cmd, cmd, script)
			case takesPositional && !emitsCandidates(shell, body):
				// "Has a branch" is not "offers something": a branch that emits
				// nothing is the same dead end with a case label in front of it.
				t.Errorf("%s completion's %q branch emits no candidates:\n%s", shell, cmd, body)
			case !takesPositional && hasCase:
				t.Errorf("%s completion has a case for %q, which positionalCommands says takes no positional — one of the two is wrong", shell, cmd)
			}
		}
	}
}

// emitsCandidates says whether a branch body actually offers something, in the
// terms each shell uses to do it.
func emitsCandidates(shell, body string) bool {
	for _, token := range map[string][]string{
		"bash": {"compgen -W"},
		"zsh":  {"compadd"},
		"fish": {"kae __complete", `printf '%s\n'`},
	}[shell] {
		if strings.Contains(body, token) {
			return true
		}
	}
	return false
}

// caseLabelPattern matches a bash/zsh case label alone on its line, including an
// alternation (`use|u|pin|p|run|r)`). The character class is wider than any
// command kae routes today so that a hyphenated or numbered one does not go
// quietly unmatched; `-*)` and `*)` stay out because neither starts with a letter.
//
// The anchors are defence rather than a property under test — measured: dropping
// them leaves every guard here green, because what they additionally exclude is a
// match *inside* a line (the `tools)` of `compadd -- ${(f)"$(kae __complete
// tools)"}`), and no caller looks up a block under that name.
var caseLabelPattern = regexp.MustCompile(`^[a-z][a-z0-9|_-]*\)$`)

// completionCaseBlocks splits a generated script's command dispatch into
// label -> branch body. bash and zsh label a branch on its own line and close it
// with a lone `;;`; fish writes `case a b` and the branch runs until a line
// indented no deeper than the label.
//
// The alternation is why the callers cannot simply grep for `cmd+")"`: `use`
// shares a branch with `pin` and `run` and appears nowhere as `use)`. Matching a
// branch *body* rather than the whole script matters for the same reason a
// per-verb substring check would not do: `backup`'s only sub-verb is `list`,
// which also occurs in companion's `add rm list`.
func completionCaseBlocks(t *testing.T, shell, script string) map[string]string {
	t.Helper()
	blocks := map[string]string{}
	lines := strings.Split(script, "\n")
	indentOf := func(s string) int { return len(s) - len(strings.TrimLeft(s, " ")) }
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		var labels, body []string
		if shell == "fish" {
			if !strings.HasPrefix(trimmed, "case ") {
				continue
			}
			labels = strings.Fields(strings.TrimPrefix(trimmed, "case "))
			for _, next := range lines[i+1:] {
				if strings.TrimSpace(next) != "" && indentOf(next) <= indentOf(line) {
					break
				}
				body = append(body, next)
			}
		} else {
			if !caseLabelPattern.MatchString(trimmed) {
				continue
			}
			labels = strings.Split(strings.TrimSuffix(trimmed, ")"), "|")
			for _, next := range lines[i+1:] {
				if strings.TrimSpace(next) == ";;" {
					break
				}
				body = append(body, next)
			}
		}
		joined := strings.Join(body, "\n")
		for _, label := range labels {
			blocks[label] = joined
		}
	}
	return blocks
}

// subcommandVerbs lists the sub-verbs each subcommand-group command dispatches
// (the literals inlined at the np==0 slot of the generated completion scripts).
// It is the parity guard's source of truth: when you add a subcommand group (or
// a verb), add it here and TestSubcommandCompletionParity forces the matching
// case into bash, zsh, and fish. Keep in lockstep with each command's dispatcher
// (e.g. CmdCompanion) and the script case blocks in completion.go.
//
// Being opt-in cuts the other way too, and no guard covers it: deleting an entry
// takes that group's sub-verb run out of the assertions with it, silently.
// positionalCommands still requires the branch to exist and to emit something,
// and TestCompletionPositionalRouting still checks the slots of the groups it
// names — but what the sub-verbs *are* is asserted here and nowhere else.
var subcommandVerbs = map[string][]string{
	"account":   {"rm", "rename", "set-identity"},
	"profile":   {"save", "set", "unset", "rm", "default"},
	"companion": {"add", "rm", "list"},
	"env":       {"set", "unset", "list"},
	"backup":    {"list"},
	"mise":      {"init"},
	// Not sub-verbs but the same thing structurally: a fixed run inlined at the
	// np==0 slot, which nothing else asserts. Left out, `kae completion <TAB>`
	// could offer anything at all with every test green.
	"completion": {"bash", "zsh", "fish"},
}

// TestCompletionRefreshRewritesRegisteredFile: `completion --refresh` rewrites an
// already-registered file from the current binary (so a structural change takes
// effect without a manual re-install) but never creates a registration for a
// shell that has none.
func TestCompletionRefreshRewritesRegisteredFile(t *testing.T) {
	app := testApp(t, nil)
	zshPath, _, err := completionTarget(app.Env, "zsh")
	if err != nil {
		t.Fatal(err)
	}
	if mkErr := os.MkdirAll(filepath.Dir(zshPath), 0o755); mkErr != nil {
		t.Fatal(mkErr)
	}
	if wErr := os.WriteFile(zshPath, []byte("# stale kae completion\n"), 0o644); wErr != nil {
		t.Fatal(wErr)
	}

	code, _ := captureStdout(t, func() int { return runCompletionRefresh(app, commonOpts{Format: formatText}) })
	mustExit(t, constants.ExitOK, code, "")

	got, err := os.ReadFile(zshPath)
	if err != nil {
		t.Fatal(err)
	}
	if want, _ := completionScript("zsh"); string(got) != want {
		t.Fatalf("refresh did not rewrite the registered zsh file to the current script")
	}
	// bash had no registered file, so refresh must not have created one.
	bashPath, _, _ := completionTarget(app.Env, "bash")
	if _, statErr := os.Stat(bashPath); statErr == nil {
		t.Fatalf("refresh must not create a registration for an unregistered shell: %s", bashPath)
	}
}

// TestCompletionRefreshNoRegistration: refresh with nothing registered is a
// no-op success (it never creates files), so the build/install hook is safe to
// run unconditionally.
func TestCompletionRefreshNoRegistration(t *testing.T) {
	app := testApp(t, nil)
	code, out := captureStdout(t, func() int { return runCompletionRefresh(app, commonOpts{Format: formatText}) })
	mustExit(t, constants.ExitOK, code, out)
	if !strings.Contains(out, "No registered kae completion") {
		t.Fatalf("expected a nothing-to-refresh message, got: %s", out)
	}
}

// TestSubcommandCompletionParity is the recurrence guard for the v0.10.0
// companion gap (a subcommand group shipped with no completion case). For every
// group in subcommandVerbs it asserts the command is a known completion command
// and that each sub-verb appears in all three generated scripts. Adding a new
// subcommand group therefore forces a completion case in bash, zsh, and fish, or
// this test fails.
func TestSubcommandCompletionParity(t *testing.T) {
	scripts := map[string]string{}
	for _, shell := range []string{"bash", "zsh", "fish"} {
		s, ok := completionScript(shell)
		if !ok {
			t.Fatalf("no completion script for %s", shell)
		}
		scripts[shell] = s
	}
	for cmd, verbs := range subcommandVerbs {
		if !slices.Contains(completionCommands, cmd) {
			t.Errorf("subcommand group %q is not in completionCommands", cmd)
		}
		// The sub-verbs are inlined at the np==0 slot as one space-joined run
		// (compgen -W "...", compadd -- ..., printf '%s\n' ...), so assert that
		// exact run, and assert it inside the group's own branch. Both halves are
		// load-bearing: a per-verb substring check would false-pass on short verbs
		// that occur elsewhere ("add" is a substring of zsh's "compadd"), and a
		// whole-script check would false-pass on a run that is itself short and
		// shared (backup's only verb is "list", which companion's "add rm list"
		// also contains).
		verbRun := strings.Join(verbs, " ")
		for shell, script := range scripts {
			body, hasCase := completionCaseBlocks(t, shell, script)[cmd]
			if !hasCase {
				t.Errorf("%s completion has no case for subcommand group %q:\n%s", shell, cmd, script)
				continue
			}
			if !strings.Contains(body, verbRun) {
				t.Errorf("%s completion missing %q sub-verbs %q (inlined run) from its own case block:\n%s", shell, cmd, verbRun, body)
			}
		}
	}
}

// TestFlagSpecWiring guards that flagSetFor reaches each command's real
// registrar (not just the common flags), so flag completion matches the parser.
func TestFlagSpecWiring(t *testing.T) {
	cases := map[string][]string{
		// dry-run is included where withDryRun is true at the parseCommon call
		// site, so the spec's dryRun bool cannot silently drift from the parser.
		"add":        {"restore", "no-login", "dry-run"},
		"use":        {"shared", "isolated", "quiet", "profile", "dry-run"},
		"u":          {"isolated", "profile", "dry-run"},
		"run":        {"env", "shared", "profile"},
		"pin":        {"shared", "isolated"},
		"unpin":      {"purge"},
		"mise":       {"mode", "auto", "write", "profile"},
		"completion": {"install", "refresh"},
		"rollback":   {"to", "dry-run"},
		"account":    {"force", "dry-run"},
		"profile":    {"force", "clear", "dry-run"},
	}
	for cmd, want := range cases {
		fs := flagSetFor(cmd)
		for _, name := range want {
			if fs.Lookup(name) == nil {
				t.Errorf("flagSetFor(%q) missing flag %q (registry not wired to the command registrar)", cmd, name)
			}
		}
	}
	// run/pin/mise/completion are not dry-run commands; their spec must not add it.
	for _, cmd := range []string{"run", "pin", "mise", "completion"} {
		if flagSetFor(cmd).Lookup("dry-run") != nil {
			t.Errorf("flagSetFor(%q) must not offer --dry-run (parser does not accept it)", cmd)
		}
	}
}

func TestCompletionInstallFpath(t *testing.T) {
	for _, tc := range []struct {
		shell   string
		relPath string
	}{
		{"bash", ".local/share/bash-completion/completions/kae"},
		{"zsh", ".local/share/zsh/site-functions/_kae"},
		{"fish", ".config/fish/completions/kae.fish"},
	} {
		app := testApp(t, nil)
		script, _ := completionScript(tc.shell)
		opts := commonOpts{Format: formatText}

		code, out := captureStdout(t, func() int {
			return applyCompletionInstall(app, opts, tc.shell, script, installFpath)
		})
		mustExit(t, constants.ExitOK, code, out)

		path := filepath.Join(app.Env.Home, tc.relPath)
		if got := readFile(t, path); got != script {
			t.Fatalf("%s: installed script mismatch", tc.shell)
		}
		if !strings.Contains(out, path) {
			t.Fatalf("%s: install output must name the path:\n%s", tc.shell, out)
		}

		// Idempotent: a second install reports "up to date" and leaves the file.
		code, out = captureStdout(t, func() int {
			return applyCompletionInstall(app, opts, tc.shell, script, installFpath)
		})
		mustExit(t, constants.ExitOK, code, out)
		if !strings.Contains(out, "up to date") {
			t.Fatalf("%s: re-install must be idempotent:\n%s", tc.shell, out)
		}
	}
}

// TestCompletionInstallZshPrefersExistingFpathDir: when a common user zsh
// completions dir already exists (the user created it because it is on their
// fpath), --install writes there — so the file auto-loads in a new shell with no
// .zshrc change — instead of the XDG fallback that needs an fpath edit.
func TestCompletionInstallZshPrefersExistingFpathDir(t *testing.T) {
	app := testApp(t, nil)
	// ~/.config/zsh/completions on fpath (the common XDG-config convention).
	fpathDir := filepath.Join(app.Env.Home, ".config", "zsh", "completions")
	if err := os.MkdirAll(fpathDir, 0o755); err != nil {
		t.Fatal(err)
	}
	script, _ := completionScript("zsh")
	code, out := captureStdout(t, func() int {
		return applyCompletionInstall(app, commonOpts{Format: formatText}, "zsh", script, installFpath)
	})
	mustExit(t, constants.ExitOK, code, out)

	want := filepath.Join(fpathDir, "_kae")
	if got := readFile(t, want); got != script {
		t.Fatalf("zsh completion must install into the existing fpath dir %s", want)
	}
	// XDG fallback must NOT be written when an fpath dir exists.
	if _, err := os.Stat(filepath.Join(app.Env.Home, ".local", "share", "zsh", "site-functions", "_kae")); !os.IsNotExist(err) {
		t.Fatalf("must not fall back to the XDG dir when an fpath dir exists (err=%v)", err)
	}
	// The dir is on fpath, so the activation note must not ask for an fpath edit.
	dir, onFpath := zshCompletionDir(app.Env)
	if dir != fpathDir || !onFpath {
		t.Fatalf("zshCompletionDir = (%q, %v), want (%q, true)", dir, onFpath, fpathDir)
	}
	note := completionActivationNote("zsh", want, onFpath)
	if strings.Contains(note, "fpath=(") {
		t.Fatal("an existing-fpath-dir install must not print the fpath-add note")
	}
	if !strings.Contains(note, "compdump") {
		t.Fatalf("zsh auto-load note should mention the stale-compdump rebuild:\n%s", note)
	}
}

// TestZshCompletionDirPriority: the candidate dirs are tried in order
// (~/.config/zsh/completions > ~/.zsh/completions > ~/.zfunc), and with none
// present it falls back to the XDG data dir (onFpath=false).
func TestZshCompletionDirPriority(t *testing.T) {
	app := testApp(t, nil)
	home := app.Env.Home
	configDir := filepath.Join(home, ".config", "zsh", "completions")
	zshDir := filepath.Join(home, ".zsh", "completions")
	zfuncDir := filepath.Join(home, ".zfunc")

	// No candidate present → XDG fallback, not on fpath.
	if dir, onFpath := zshCompletionDir(app.Env); onFpath || dir != filepath.Join(home, ".local", "share", "zsh", "site-functions") {
		t.Fatalf("no-candidate: got (%q, %v), want the XDG dir, false", dir, onFpath)
	}
	// Only ~/.zfunc exists → it wins.
	if err := os.MkdirAll(zfuncDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if dir, onFpath := zshCompletionDir(app.Env); dir != zfuncDir || !onFpath {
		t.Fatalf("zfunc-only: got (%q, %v), want (%q, true)", dir, onFpath, zfuncDir)
	}
	// ~/.zsh/completions also exists → it outranks ~/.zfunc.
	if err := os.MkdirAll(zshDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if dir, _ := zshCompletionDir(app.Env); dir != zshDir {
		t.Fatalf("zsh-vs-zfunc: got %q, want %q", dir, zshDir)
	}
	// ~/.config/zsh/completions also exists → it outranks all.
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if dir, _ := zshCompletionDir(app.Env); dir != configDir {
		t.Fatalf("config-wins: got %q, want %q", dir, configDir)
	}
}

func TestCompletionInstallNeverTouchesMiseByDefault(t *testing.T) {
	app := testApp(t, nil)
	script, _ := completionScript("zsh")
	opts := commonOpts{Format: formatText}
	captureStdout(t, func() int { return applyCompletionInstall(app, opts, "zsh", script, installFpath) })

	// The default (fpath) path must not create the global mise config.
	if _, err := os.Stat(globalMiseConfigPath(app.Env)); !os.IsNotExist(err) {
		t.Fatalf("fpath install must not write the global mise config (err=%v)", err)
	}
}

func TestCompletionInstallMiseHook(t *testing.T) {
	app := testApp(t, nil)
	script, _ := completionScript("zsh")
	opts := commonOpts{Format: formatText}

	code, out := captureStdout(t, func() int {
		return applyCompletionInstall(app, opts, "zsh", script, installMiseHook)
	})
	mustExit(t, constants.ExitOK, code, out)

	path := globalMiseConfigPath(app.Env)
	content := readFile(t, path)
	if !strings.Contains(content, "[hooks.enter]") || !strings.Contains(content, "kae completion zsh") {
		t.Fatalf("mise hook not written:\n%s", content)
	}
	// The rendered config must parse as valid TOML.
	var parsed map[string]any
	if _, err := toml.Decode(content, &parsed); err != nil {
		t.Fatalf("mise config does not parse: %v\n%s", err, content)
	}

	// Idempotent: re-running replaces the marker block, not appends a duplicate.
	captureStdout(t, func() int {
		return applyCompletionInstall(app, opts, "zsh", script, installMiseHook)
	})
	again := readFile(t, path)
	if strings.Count(again, miseBlockStart) != 1 {
		t.Fatalf("mise hook re-install duplicated the block:\n%s", again)
	}
}

func TestMiseHookBlockUsesCurrentShellSemantics(t *testing.T) {
	for _, tc := range []struct {
		shell  string
		script string
	}{
		{shell: "bash", script: `eval "$(kae completion bash)"`},
		{shell: "zsh", script: `eval "$(kae completion zsh)"`},
		{shell: "fish", script: "kae completion fish | source"},
	} {
		t.Run(tc.shell, func(t *testing.T) {
			block := miseHookBlock(tc.shell)
			want := fmt.Sprintf("[hooks.enter]\nshell = %q\nscript = %q\n", tc.shell, tc.script)
			if !strings.Contains(block, want) {
				t.Fatalf("mise hook must target the active shell and run in it:\n%s", block)
			}
			if strings.Contains(block, "source <(") || strings.Contains(block, "\nrun = ") {
				t.Fatalf("completion hook must not use a spawned-shell command:\n%s", block)
			}
			var parsed map[string]any
			if _, err := toml.Decode(block, &parsed); err != nil {
				t.Fatalf("mise hook does not parse as TOML: %v\n%s", err, block)
			}
		})
	}
}

func TestCompletionRefreshMigratesExactLegacyMiseHook(t *testing.T) {
	app := testApp(t, nil)
	path := globalMiseConfigPath(app.Env)
	const before = "experimental = true\n"
	const after = "[tools]\nnode = \"24\"\n"
	const legacy = `# >>> kagikae >>>
# kae shell completion via mise (opt-in, experimental). Needs ` + "`mise activate`" + `,
# a trusted config, and ` + "`mise settings experimental=true`" + `. Fires on directory
# entry. Non-mise users register via the fpath file or
# eval "$(kae completion <shell>)" (docs/CLI.md).
[hooks.enter]
script = "source <(kae completion zsh)"
# <<< kagikae <<<
`
	target := filepath.Join(filepath.Dir(path), "actual-config.toml")
	writeFile(t, target, before+legacy+after)
	if err := os.Chmod(target, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Base(target), path); err != nil {
		t.Fatal(err)
	}

	code, out := captureStdout(t, func() int {
		return runCompletionRefresh(app, commonOpts{Format: formatText})
	})
	mustExit(t, constants.ExitOK, code, out)
	if !strings.Contains(out, "Refreshed kae zsh completion mise hook") {
		t.Fatalf("legacy hook migration was not reported: %s", out)
	}
	want := before + miseHookBlock("zsh") + after
	if got := readFile(t, target); got != want {
		t.Fatalf("legacy hook migration changed the wrong bytes:\ngot:\n%s\nwant:\n%s", got, want)
	}
	if linkInfo, err := os.Lstat(path); err != nil {
		t.Fatal(err)
	} else if linkInfo.Mode()&os.ModeSymlink == 0 {
		t.Fatal("legacy migration replaced the logical config symlink")
	}
	if info, err := os.Stat(target); err != nil {
		t.Fatal(err)
	} else if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("legacy hook migration changed config mode to %o, want 600", got)
	}
	var parsed map[string]any
	if _, err := toml.Decode(want, &parsed); err != nil {
		t.Fatalf("migrated config is invalid TOML: %v\n%s", err, want)
	}

	// The migrated current block is a registered no-op on the next refresh.
	code, out = captureStdout(t, func() int {
		return runCompletionRefresh(app, commonOpts{Format: formatText})
	})
	mustExit(t, constants.ExitOK, code, out)
	if strings.Contains(out, "Refreshed") || strings.Contains(out, "No registered") {
		t.Fatalf("current mise hook must be recognized as an unchanged registration: %s", out)
	}
	if got := readFile(t, target); got != want {
		t.Fatalf("idempotent refresh changed the current hook:\n%s", got)
	}
}

func TestCompletionRefreshRefusesLegacyBlockWhoseTableContinuesAfterMarker(t *testing.T) {
	for _, trailing := range []string{
		`shell = "zsh"`,
		`run = "echo manually extended"`,
	} {
		t.Run(trailing, func(t *testing.T) {
			app := testApp(t, nil)
			path := globalMiseConfigPath(app.Env)
			content := legacyMiseHookBlock("zsh") + "# retained comment\n\n" + trailing + "\n"
			writeFile(t, path, content)

			code, out := captureStdout(t, func() int {
				return runCompletionRefresh(app, commonOpts{Format: formatText})
			})
			mustExit(t, constants.ExitOK, code, out)
			if got := readFile(t, path); got != content {
				t.Fatalf("refresh changed a legacy-looking block whose table continues after its marker:\ngot:\n%s\nwant:\n%s", got, content)
			}
			if strings.Contains(out, "Refreshed") {
				t.Fatalf("unsafe migration was reported as completed: %s", out)
			}

			if _, _, err := installMiseGlobalHook(app.Env, "zsh"); err == nil {
				t.Fatal("explicit reinstall must also refuse a marker whose table continues outside it")
			}
			if got := readFile(t, path); got != content {
				t.Fatalf("explicit reinstall changed a continued hook table:\n%s", got)
			}
		})
	}
}

func TestCompletionInstallMiseHookUpdatePreservesMode(t *testing.T) {
	app := testApp(t, nil)
	path := globalMiseConfigPath(app.Env)
	target := filepath.Join(filepath.Dir(path), "actual-config.toml")
	writeFile(t, target, miseHookBlock("bash"))
	if err := os.Chmod(target, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Base(target), path); err != nil {
		t.Fatal(err)
	}

	_, changed, err := installMiseGlobalHook(app.Env, "zsh")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("changing the registered shell must update the owned block")
	}
	if linkInfo, err := os.Lstat(path); err != nil {
		t.Fatal(err)
	} else if linkInfo.Mode()&os.ModeSymlink == 0 {
		t.Fatal("owned block update replaced the logical config symlink")
	}
	if info, err := os.Stat(target); err != nil {
		t.Fatal(err)
	} else if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("owned block update changed config mode to %o, want 600", got)
	}
	if got := readFile(t, target); got != miseHookBlock("zsh") {
		t.Fatalf("owned block update did not update the symlink target:\n%s", got)
	}
}

func TestCompletionMiseHookRefusesDanglingSymlink(t *testing.T) {
	app := testApp(t, nil)
	path := globalMiseConfigPath(app.Env)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	const missing = "missing-config.toml"
	if err := os.Symlink(missing, path); err != nil {
		t.Fatal(err)
	}

	if _, _, err := installMiseGlobalHook(app.Env, "zsh"); err == nil {
		t.Fatal("explicit install must refuse a dangling global config symlink")
	}
	if _, _, _, _, err := refreshLegacyMiseGlobalHook(app.Env); err == nil {
		t.Fatal("automatic refresh must refuse a dangling global config symlink")
	}
	if linkInfo, err := os.Lstat(path); err != nil {
		t.Fatal(err)
	} else if linkInfo.Mode()&os.ModeSymlink == 0 {
		t.Fatal("dangling global config symlink was replaced")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), missing)); !os.IsNotExist(err) {
		t.Fatalf("dangling symlink target must not be created (err=%v)", err)
	}
}

func TestCompletionRefreshLeavesNonLegacyMiseBlockUntouched(t *testing.T) {
	app := testApp(t, nil)
	path := globalMiseConfigPath(app.Env)
	const custom = "# >>> kagikae >>>\n[hooks.enter]\nscript = \"echo manually edited\"\n# <<< kagikae <<<\n"
	writeFile(t, path, custom)

	code, out := captureStdout(t, func() int {
		return runCompletionRefresh(app, commonOpts{Format: formatText})
	})
	mustExit(t, constants.ExitOK, code, out)
	if got := readFile(t, path); got != custom {
		t.Fatalf("refresh must not infer a migration for a non-legacy block:\n%s", got)
	}
}

func TestCompletionInstallMiseHookRefusesForeignHook(t *testing.T) {
	app := testApp(t, nil)
	path := globalMiseConfigPath(app.Env)
	writeFile(t, path, "[hooks.enter]\nscript = \"echo hi\"\n")
	script, _ := completionScript("bash")
	opts := commonOpts{Format: formatText}

	code, _ := captureStdout(t, func() int {
		return applyCompletionInstall(app, opts, "bash", script, installMiseHook)
	})
	if code != constants.ExitUnsafeRefused {
		t.Fatalf("a foreign [hooks.enter] must be refused, got exit %d", code)
	}
	// The user's hook is left intact.
	if got := readFile(t, path); !strings.Contains(got, "echo hi") || strings.Contains(got, miseBlockStart) {
		t.Fatalf("foreign hook must be untouched:\n%s", got)
	}
	_, _, err := installMiseGlobalHook(app.Env, "bash")
	if err == nil || !strings.Contains(err.Error(), `shell = "bash"`) ||
		!strings.Contains(err.Error(), `script = "eval \"$(kae completion bash)\""`) {
		t.Fatalf("manual merge guidance must describe a current-shell hook, got: %v", err)
	}
}

func TestCompletionInstallPrintOnly(t *testing.T) {
	app := testApp(t, nil)
	script, _ := completionScript("fish")
	opts := commonOpts{Format: formatText}
	code, out := captureStdout(t, func() int {
		return applyCompletionInstall(app, opts, "fish", script, installPrintOnly)
	})
	mustExit(t, constants.ExitOK, code, out)
	if out != script {
		t.Fatalf("print-only must emit the script verbatim:\n%s", out)
	}
}

// TestCompletionPositionalRouting guards the per-shell positional routing in the
// generated scripts: each branch must read the argument it needs from the
// flag-filtered positional list, at the slot and array index that shell uses.
// `kae use <tool> <TAB>` reads the first positional after `use`; `kae account rm
// <tool> <TAB>` and `kae env set <tool> <TAB>` read the second, past the sub-verb.
// The positionals exclude flags, so `kae add --no-login <TAB>` still completes
// tools. An off-by-one or a missing flag-skip silently yields no candidates or the
// wrong ones (it once did for fish).
//
// Every literal is asserted inside the command's own branch, and in order,
// because these constructs repeat: `accounts "${pos[1]}"` appears in `account` as
// well as `env`, so a whole-script check passes on a branch that was never written
// — which is exactly how `env`'s tool and account slots could be deleted outright
// with all three shells still green — and both arms of one branch hold all the
// same literals, so an unordered check passes on arms that were swapped.
//
// Order is as far as matching text goes: it does not prove which arm a literal
// sits *in*, so flattening a branch into unconditional lines keeps every literal
// in sequence and passes here while completing wrongly. That is the real-machine
// smoke's question (docs/VALIDATION.md § Completion real-machine smoke).
func TestCompletionPositionalRouting(t *testing.T) {
	for _, tc := range []struct {
		shell    string
		flagSkip string              // the construct that drops flag tokens from positionals
		want     map[string][]string // case label -> literals its branch must contain
	}{
		{"bash", `__complete valued-flags`, map[string][]string{
			"use":     {`accounts "${pos[0]}"`},
			"account": {`"$np" -eq 1`, `__complete tools`, `"$np" -eq 2`, `accounts "${pos[1]}"`},
			"env":     {`"${pos[0]}" != "list"`, `"$np" -eq 1`, `__complete tools`, `"$np" -eq 2`, `accounts "${pos[1]}"`},
			"backup":  {`"$np" -eq 0`, `compgen -W "list"`},
		}},
		{"zsh", `__complete valued-flags`, map[string][]string{
			"use":     {`accounts ${pos[1]}`},
			"account": {`np == 1`, `__complete tools`, `np == 2`, `accounts ${pos[2]}`},
			"env":     {`"${pos[1]}" != list`, `np == 1`, `__complete tools`, `np == 2`, `accounts ${pos[2]}`},
			"backup":  {`np == 0`, `compadd -- list`},
		}},
		{"fish", `string match -q -- '-*'`, map[string][]string{
			"use":     {`accounts $pos[1]`},
			"account": {`$np -eq 1`, `__complete tools`, `$np -eq 2`, `accounts $pos[2]`},
			"env":     {`"$pos[1]" != list`, `$np -eq 1`, `__complete tools`, `$np -eq 2`, `accounts $pos[2]`},
			"backup":  {`$np -eq 0`, `printf '%s\n' list`},
		}},
	} {
		script, ok := completionScript(tc.shell)
		if !ok {
			t.Fatalf("no completion script for %s", tc.shell)
		}
		blocks := completionCaseBlocks(t, tc.shell, script)
		for cmd, wants := range tc.want {
			body, hasCase := blocks[cmd]
			if !hasCase {
				t.Errorf("%s: no case block for %q", tc.shell, cmd)
				continue
			}
			// In order, not merely present: swapping two arms' bodies leaves every
			// literal in the branch and completes the wrong thing at both slots —
			// `kae env set <TAB>` then offers nothing (and says `pos[1]: unbound
			// variable` under `set -u`), while `kae env set claude <TAB>` offers
			// tools. Measured: the unordered form passed that.
			rest := body
			for _, want := range wants {
				at := strings.Index(rest, want)
				if at < 0 {
					t.Errorf("%s: the %q branch is missing %q, or has it ahead of the literal that should precede it:\n%s", tc.shell, cmd, want, body)
					break
				}
				rest = rest[at+len(want):]
			}
		}
		if !strings.Contains(script, tc.flagSkip) {
			t.Errorf("%s: missing the flag-skip construct %q (flags must not shift positionals)", tc.shell, tc.flagSkip)
		}
	}
}

// TestCompletionScriptsAreSyntacticallyValid parses each generated script with the
// shell it targets. Nothing else does: the scripts are Go string constants, so the
// shellcheck task (which walks scripts/*.sh) never sees them, and one that fails to
// parse breaks completion for every user of that shell.
//
// A shell that is not installed is skipped rather than faked, so the assertion is
// only ever as strong as the machine — but bash is required to have run, or an
// image without any of the three would let this pass while checking nothing.
//
// Two ways this could check nothing while passing, both closed here because both
// were reachable: a shell name the script table does not know yields the empty
// string, and every shell accepts an empty file; and a parse flag that does not
// parse (zsh `--version`) accepts a broken one. So the name is taken with its ok,
// and each shell is first shown a deliberately unterminated `if` and must reject
// it before its verdict on the real script is worth anything.
func TestCompletionScriptsAreSyntacticallyValid(t *testing.T) {
	// Unterminated in all three: bash and zsh want `fi`, fish wants `end`.
	const mustNotParse = "if true\n"
	var checked []string
	for _, tc := range []struct {
		shell, bin string
		parseOnly  []string // rc files are skipped so a failure can only be the script
	}{
		{"bash", "bash", []string{"--noprofile", "--norc", "-n"}},
		{"zsh", "zsh", []string{"-f", "-n"}},
		{"fish", "fish", []string{"--no-execute"}},
	} {
		bin, err := exec.LookPath(tc.bin)
		if err != nil {
			t.Logf("%s is not installed here; its syntax went unchecked", tc.shell)
			continue
		}
		script, ok := completionScript(tc.shell)
		if !ok {
			t.Fatalf("no completion script for %s", tc.shell)
		}
		parse := func(name, content string) error {
			path := filepath.Join(t.TempDir(), name+"."+tc.shell)
			if wErr := os.WriteFile(path, []byte(content), 0o600); wErr != nil {
				t.Fatal(wErr)
			}
			out, rErr := exec.Command(bin, append(append([]string{}, tc.parseOnly...), path)...).CombinedOutput()
			if rErr != nil {
				return fmt.Errorf("%w\n%s", rErr, out)
			}
			return nil
		}
		if parse("control", mustNotParse) == nil {
			t.Errorf("%s %v accepted an unterminated `if`, so it is not parsing and its verdict below means nothing", tc.shell, tc.parseOnly)
			continue
		}
		if pErr := parse("completion", script); pErr != nil {
			t.Errorf("%s rejects the generated script: %v", tc.shell, pErr)
		}
		checked = append(checked, tc.shell)
	}
	if !slices.Contains(checked, "bash") {
		t.Errorf("no bash to parse the generated script with, so this checked %v and proves nothing", checked)
	}
}

func TestMiseInitRendersCompletionTasks(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	chdirTemp(t)

	code, out := captureStdout(t, func() int {
		return runMiseInit(ctx, app, opts, "main", constants.ModeAuth, false, false)
	})
	mustExit(t, constants.ExitOK, code, out)
	for _, want := range []string{
		"[tasks.ai-switch]",
		"[tasks.ai-switch-tool]",
		`complete "profile" run="kae __complete profiles"`,
		`complete "tool" run="kae __complete tools"`,
		`complete "account" run="kae __complete accounts"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("mise block missing %q:\n%s", want, out)
		}
	}

	// The rendered block (with its triple-quoted usage specs) parses as TOML.
	block := out[strings.Index(out, miseBlockStart):]
	block = block[:strings.Index(block, miseBlockEnd)+len(miseBlockEnd)]
	var parsed map[string]any
	if _, err := toml.Decode(block, &parsed); err != nil {
		t.Fatalf("rendered mise block does not parse: %v\n%s", err, block)
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
