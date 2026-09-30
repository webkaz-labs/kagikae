package cmd

// Completion install tests: the fpath and mise hook installation, positional
// routing and the syntax of the generated scripts.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/webkaz-labs/kagikae/internal/constants"
)

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

	path := app.Paths.MiseGlobalFragmentFile()
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
	want := before + after
	if got := readFile(t, app.Paths.MiseGlobalFragmentFile()); got != miseHookBlock("zsh") {
		t.Fatalf("missing migrated hook: %s", got)
	}
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
	if got := readFile(t, target); got != "" {
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

func TestCompletionInstallMiseHookCoexistsWithForeignHook(t *testing.T) {
	app := testApp(t, nil)
	path := globalMiseConfigPath(app.Env)
	original := "[hooks.enter]\nscript = \"echo hi\"\n"
	writeFile(t, path, original)
	if _, _, err := installMiseGlobalHook(app.Env, "bash"); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != original {
		t.Fatal("foreign hook changed")
	}
	if got := readFile(t, app.Paths.MiseGlobalFragmentFile()); got != miseHookBlock("bash") {
		t.Fatal("completion not registered separately")
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
	// Print-only prints what `kae completion fish` prints, for a shell to source:
	// the kae shell function included.
	if out != completionEvalScript("fish") {
		t.Fatalf("print-only must emit what kae completion fish prints:\n%s", out)
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
			"ls":      {`targets=ls-targets`, `"$cmd" != ls ]; then targets=place-targets`, `-i|--isolated|-isolated) scope=i`, `-s|--shared|-shared) scope=s`, `"$np" -eq 0 ] && [ -n "$scope" ]`, `__complete tools`, `"$np" -eq 0 ]`, `__complete "$targets"`, `"$np" -eq 1 ] && [ "$scope" = i ]`, `accounts "${pos[0]}"`},
			"account": {`"$np" -eq 1`, `__complete tools`, `"$np" -eq 2`, `accounts "${pos[1]}"`},
			"env":     {`"${pos[0]}" != "list"`, `"$np" -eq 1`, `__complete tools`, `"$np" -eq 2`, `accounts "${pos[1]}"`},
			"backup":  {`"$np" -eq 0`, `compgen -W "list"`},
		}},
		{"zsh", `__complete valued-flags`, map[string][]string{
			"use":     {`accounts ${pos[1]}`},
			"ls":      {`targets=ls-targets`, `"$cmd" != ls ]]; then targets=place-targets`, `-i|--isolated|-isolated) scope=i`, `-s|--shared|-shared) scope=s`, `(( np == 0 )) && [[ -n "$scope" ]]`, `__complete tools`, `(( np == 0 ))`, `__complete $targets`, `(( np == 1 )) && [[ "$scope" == i ]]`, `accounts ${pos[1]}`},
			"account": {`np == 1`, `__complete tools`, `np == 2`, `accounts ${pos[2]}`},
			"env":     {`"${pos[1]}" != list`, `np == 1`, `__complete tools`, `np == 2`, `accounts ${pos[2]}`},
			"backup":  {`np == 0`, `compadd -- list`},
		}},
		{"fish", `string match -q -- '-*'`, map[string][]string{
			"use":     {`accounts $pos[1]`},
			"ls":      {`set -l targets ls-targets`, `test $cmd != ls`, `set targets place-targets`, `contains -- -i $tokens`, `set isolated 1`, `contains -- -s $tokens`, `test $np -eq 0; and test $scoped -eq 1`, `__complete tools`, `test $np -eq 0`, `__complete $targets`, `test $np -eq 1; and test $isolated -eq 1`, `accounts $pos[1]`},
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
