package cmd

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

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

	// ls-targets lists the fixed group words, then every tool.
	_, out = captureStdout(t, func() int { return runComplete(app, []string{"ls-targets"}) })
	if want := strings.Join(lsTargetWords(), "\n") + "\n"; out != want {
		t.Fatalf("ls-targets = %q, want %q", out, want)
	}
	for _, word := range []string{"account\n", "pin\n", "repo\n", "kae\n", "claude\n"} {
		if !strings.Contains(out, word) {
			t.Fatalf("ls-targets missing %q:\n%s", word, out)
		}
	}

	// place-targets is ls-targets without account, whose rows are not places.
	_, out = captureStdout(t, func() int { return runComplete(app, []string{"place-targets"}) })
	if want := strings.Join(placeTargetWords(), "\n") + "\n"; out != want || strings.Contains(out, "account") || !strings.Contains(out, "pin\n") {
		t.Fatalf("place-targets = %q, want %q", out, want)
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
	// --pick is open and cd's, and the three scripts read this list, so every
	// shell offers it there and only there.
	for _, verb := range []string{"open", "cd", "ls"} {
		_, out = captureStdout(t, func() int { return runComplete(app, []string{"flags", verb}) })
		if got := strings.Contains(out, "--pick\n"); got != (verb != "ls") {
			t.Fatalf("flags %s lists --pick = %v:\n%s", verb, got, out)
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
	"init":         false,
	"edit":         false,
	"doctor":       true, // [<tool>]
	"add":          true, // <tool> [<account>]
	"use":          true, // [<profile> | <tool> <account>]
	"pin":          true, // [<profile> | <tool> <account>]
	"unpin":        false,
	"uninstall":    false,
	"relogin":      true, // [<tool>]
	"run":          true, // [<profile> | <tool> <account>] -- <cmd>
	"env":          true, // <set|unset|list> ...
	"companion":    true, // <add|rm|list> ...
	"mise":         true, // init
	"accounts":     false,
	"ls":           true, // [<target> | -s <tool> | -i <tool> <account>]
	"open":         true, // [<target> | -s <tool> | -i <tool> <account>]
	"cd":           true, // [<target> | -s <tool> | -i <tool> <account>] (the shell function's)
	"account":      true, // <rm|rename|set-identity> ...
	"profile":      true, // <save|set|unset|rm|default> ...
	"status":       false,
	"preservation": true,  // list/restore/rm
	"backup":       true,  // list
	"rollback":     false, // the backup id is --to's value, not a positional
	"completion":   true,  // <bash|zsh|fish>
	"version":      false,
	"help":         false,
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
	"account":      {"rm", "rename", "set-identity"},
	"profile":      {"save", "set", "unset", "rm", "default"},
	"companion":    {"add", "rm", "list"},
	"env":          {"set", "unset", "list"},
	"preservation": {"list", "restore", "rm"},
	"backup":       {"list"},
	"mise":         {"init"},
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
		"completion": {"install", "refresh", "no-function"},
		"open":       {"at", "pick", "project", "below", "home", "root", "shared", "isolated"},
		"cd":         {"at", "pick", "project", "below", "home", "root", "shared", "isolated"},
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
