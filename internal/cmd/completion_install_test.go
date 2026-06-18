package cmd

import (
	"context"
	"os"
	"path/filepath"
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
default_profile = "work"
[profiles.work]
accounts = { claude = "alice", codex = "alice" }
[profiles.personal]
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
	if !strings.Contains(out, "work\n") || !strings.Contains(out, "personal\n") {
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
		return runMiseInit(ctx, app, opts, "work", constants.ModeAuth, false, false)
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
