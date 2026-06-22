package cmd

import (
	"context"
	"os"
	"path/filepath"
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

// subcommandVerbs lists the sub-verbs each subcommand-group command dispatches
// (the literals inlined at the np==0 slot of the generated completion scripts).
// It is the parity guard's source of truth: when you add a subcommand group (or
// a verb), add it here and TestSubcommandCompletionParity forces the matching
// case into bash, zsh, and fish. Keep in lockstep with each command's dispatcher
// (e.g. CmdCompanion) and the script case blocks in completion.go.
var subcommandVerbs = map[string][]string{
	"account":   {"rm", "rename", "set-identity"},
	"profile":   {"save", "set", "unset", "rm", "default"},
	"companion": {"add", "rm", "list"},
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
		// exact run. A per-verb substring check would false-pass on short verbs
		// that occur elsewhere (e.g. "add" is a substring of zsh's "compadd").
		verbRun := strings.Join(verbs, " ")
		for shell, script := range scripts {
			// The group must have its own case block in each script (bash/zsh use
			// `cmd)`, fish uses `case cmd`).
			caseExists := strings.Contains(script, cmd+")") || strings.Contains(script, "case "+cmd)
			if !caseExists {
				t.Errorf("%s completion has no case for subcommand group %q:\n%s", shell, cmd, script)
			}
			if !strings.Contains(script, verbRun) {
				t.Errorf("%s completion missing %q sub-verbs %q (inlined run)", shell, cmd, verbRun)
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
		"mise":       {"mode", "auto", "write", "profile"},
		"completion": {"install"},
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

// TestCompletionAccountTokenIndex guards the per-shell positional routing in the
// static completion scripts: account completion must pass the tool word from the
// flag-filtered positional list at the right index for that shell's array
// convention. `kae use <tool> <TAB>` reads the first positional after `use`;
// `kae account rm <tool> <TAB>` reads the second (past the rm/rename subcommand).
// The positionals exclude flags, so `kae add --no-login <TAB>` still completes
// tools (the flag is skipped, not counted as the tool). An off-by-one or a
// missing flag-skip silently yields no/ wrong candidates (it once did for fish).
func TestCompletionAccountTokenIndex(t *testing.T) {
	for _, tc := range []struct {
		shell          string
		useToolRef     string // tool word in `kae use <tool> <TAB>`
		accountToolRef string // tool word in `kae account rm <tool> <TAB>`
		flagSkip       string // the construct that drops flag tokens from positionals
	}{
		{"bash", `accounts "${pos[0]}"`, `accounts "${pos[1]}"`, `-*) ;;`},
		{"zsh", `accounts ${pos[1]}`, `accounts ${pos[2]}`, `== -* ]] || pos`},
		{"fish", `accounts $pos[1]`, `accounts $pos[2]`, `string match -q -- '-*'`},
	} {
		script, _ := completionScript(tc.shell)
		if !strings.Contains(script, tc.useToolRef) {
			t.Fatalf("%s: missing `use` tool ref %q", tc.shell, tc.useToolRef)
		}
		if !strings.Contains(script, tc.accountToolRef) {
			t.Fatalf("%s: missing `account` tool ref %q", tc.shell, tc.accountToolRef)
		}
		if !strings.Contains(script, tc.flagSkip) {
			t.Fatalf("%s: missing the flag-skip construct %q (flags must not shift positionals)", tc.shell, tc.flagSkip)
		}
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
