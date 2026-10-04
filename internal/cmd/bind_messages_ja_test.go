package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/l10n"
	"github.com/webkaz-labs/kagikae/internal/testutil/l10ntest"
)

// The bind, completion-install, mise init and uninstall messages as values: the
// English bytes are the ones the fmt calls wrote before, Japanese renders under the
// real catalog, and an error keeps its cause, its English Error() and its exit code.

// A wrapped bind error renders its head in Japanese and its external cause
// verbatim, keeps the English Error() that JSON carries, and keeps the exit code
// exitOf read through the old fmt.Errorf wrap.
func TestBindErrorsAreValuesThatKeepTheirCause(t *testing.T) {
	cause := errf(constants.ExitUnsafeRefused, "unsafe operation refused")
	external := errors.New("open x: permission denied")
	for _, tc := range []struct {
		name    string
		err     error
		english string
		ja      string
		exit    int
	}{
		{
			name:    "shared rebind",
			err:     bindModes()[0].rebindFailure("claude", "main", cause),
			english: "swap shared credential for claude: unsafe operation refused",
			ja:      "claude の共有の認証情報を差し替えられません: 安全でない操作を拒否しました",
			exit:    constants.ExitUnsafeRefused,
		},
		{
			name:    "isolated rebind",
			err:     bindModes()[1].rebindFailure("claude", "main", artifact.ErrUnsafe),
			english: "prepare isolated config for claude/main: unsafe operation refused",
			ja:      "claude/main の独立モードの設定を準備できません: 安全でない操作を拒否しました",
			exit:    constants.ExitUnsafeRefused,
		},
		{
			name:    "tree rebind",
			err:     bindModes()[2].rebindFailure("claude", "main", external),
			english: "prepare tree store for claude/main: open x: permission denied",
			ja:      "claude/main のツリーモードのストアを準備できません: open x: permission denied",
			exit:    constants.ExitError,
		},
		{
			name:    "unisolatable credential store",
			err:     l10n.Errorf("%w: kae cannot give this directory its own %s credential store (%s)", errGlobalCredentialStore, "codex", "CODEX_HOME"),
			english: "credential store is not per-directory: kae cannot give this directory its own codex credential store (CODEX_HOME)",
			ja:      "認証ストアがディレクトリごとに分かれていません: kae はこのディレクトリに専用の codex の認証ストアを用意できません（CODEX_HOME）",
			exit:    constants.ExitError,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.err.Error(); got != tc.english {
				t.Errorf("Error() = %q, want %q", got, tc.english)
			}
			if got := exitOf(tc.err); got != tc.exit {
				t.Errorf("exitOf = %d, want %d", got, tc.exit)
			}
			l10ntest.UseJapanese(t)
			if got := l10n.Render(tc.err); got != tc.ja {
				t.Errorf("Render = %q, want %q", got, tc.ja)
			}
			if got := tc.err.Error(); got != tc.english {
				t.Errorf("Error() under Japanese = %q, must stay English", got)
			}
		})
	}
	if !errors.Is(bindModes()[1].rebindFailure("claude", "main", artifact.ErrUnsafe), artifact.ErrUnsafe) {
		t.Error("the rebind wrap must keep its cause reachable")
	}
}

// identityTargetEscapes reports an unresolvable store with the OS error verbatim
// inside the Japanese head.
func TestIdentityTargetEscapesErrorInJapanese(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "gone")
	_, err := identityTargetEscapes(filepath.Join(missing, "id.json"), missing)
	if err == nil || !strings.HasPrefix(err.Error(), "resolve store dir "+missing+": ") {
		t.Fatalf("err = %v", err)
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the OS cause must stay reachable: %v", err)
	}
	l10ntest.UseJapanese(t)
	got := l10n.Render(err)
	if !strings.HasPrefix(got, "ストアのディレクトリ "+missing+" を解決できません: ") || !strings.Contains(got, "no such file or directory") {
		t.Errorf("Render = %q", got)
	}
}

// The git-exclude warning lists what it could not ignore with the language's
// separator and embeds kae's own cause in Japanese.
func TestGitExcludeWarningInJapanese(t *testing.T) {
	paths := []string{".config/mise/conf.d/kagikae.toml", ".claude"}
	cause := l10n.Errorf("git rev-parse returned %q", "x\ny")
	_, english := captureStderr(t, func() int { warnGitExclude(paths, cause); return 0 })
	want := "kae: warning: could not tell git to ignore .config/mise/conf.d/kagikae.toml, .claude: git rev-parse returned \"x\\ny\"\n" +
		"kae: the binding is in place; ignore .config/mise/conf.d/kagikae.toml, .claude yourself (machine-specific; must not be committed)\n"
	if english != want {
		t.Errorf("English stderr =\n%s\nwant\n%s", english, want)
	}
	l10ntest.UseJapanese(t)
	_, ja := captureStderr(t, func() int { warnGitExclude(paths, cause); return 0 })
	for _, want := range []string{
		"kae: warning: .config/mise/conf.d/kagikae.toml、.claude を無視するよう git に伝えられませんでした: git rev-parse の出力が想定外です: \"x\\ny\"\n",
		"kae: 固定は完了しています。.config/mise/conf.d/kagikae.toml、.claude は自分で無視してください",
	} {
		if !strings.Contains(ja, want) {
			t.Errorf("Japanese stderr missing %q:\n%s", want, ja)
		}
	}
}

// The ls usage errors enumerate with "、" in Japanese and ", " in English.
func TestLsTargetListsUseTheLanguageSeparator(t *testing.T) {
	_, english := captureStderr(t, func() int { _, code := resolveLsTarget("ls", "c"); return code })
	if !strings.Contains(english, "matches claude, codex") {
		t.Errorf("English usage error = %q", english)
	}
	l10ntest.UseJapanese(t)
	_, ja := captureStderr(t, func() int { _, code := resolveLsTarget("ls", "c"); return code })
	if !strings.Contains(ja, "claude、codex") || strings.Contains(ja, "claude, codex") {
		t.Errorf("Japanese usage error = %q", ja)
	}
	var at atFlag
	err := at.Set("0")
	if err == nil || err.Error() != `--at takes a place number from 1, got "0"` {
		t.Fatalf("Set(0) = %v", err)
	}
	if got := l10n.Render(err); got != `--at には 1 以上の場所の番号を指定してください（指定値: "0"）` {
		t.Errorf("Render = %q", got)
	}
}

// The completion menu, install report and activation note keep the English bytes
// the fmt calls wrote, and render in Japanese with the shell lines verbatim.
func TestCompletionInstallLinesInBothLanguages(t *testing.T) {
	app := testApp(t, nil)
	path, _, _ := completionTarget(app.Env, "zsh")
	var stderr string
	withStdin(t, "3\n", func() {
		_, stderr = captureStderr(t, func() int { return int(promptCompletionChoice(app.Env, "zsh")) })
	})
	want := "Register kae zsh completion:\n" +
		"  1) completion file in the shell's standard dir (" + path + ") [default]\n" +
		"  2) global mise [hooks.enter] (opt-in, experimental) — mise not detected on PATH\n" +
		"  3) print the script only\n" +
		"Choice [1]: "
	if stderr != want {
		t.Errorf("English menu =\n%q\nwant\n%q", stderr, want)
	}

	script, _ := completionScript("zsh")
	_, stdout, stderr := captureBoth(t, func() int {
		return applyCompletionInstall(app, commonOpts{Format: formatText}, "zsh", script, installFpath)
	})
	if stdout != "Installed kae zsh completion: "+path+"\n" {
		t.Errorf("English report = %q", stdout)
	}
	if wantNote := "Ensure this is on your fpath, e.g. add to ~/.zshrc:\n  fpath=(" + filepath.Dir(path) +
		" $fpath)\n  autoload -Uz compinit && compinit\nThen open a new shell.\n"; stderr != wantNote {
		t.Errorf("English note =\n%q\nwant\n%q", stderr, wantNote)
	}

	l10ntest.UseJapanese(t)
	withStdin(t, "\n", func() {
		_, stderr = captureStderr(t, func() int { return int(promptCompletionChoice(app.Env, "zsh")) })
	})
	for _, want := range []string{"kae の zsh 補完の登録先:\n", "（" + path + "）[既定]\n", "PATH に mise が見つかりません）\n", "選択 [1]: "} {
		if !strings.Contains(stderr, want) {
			t.Errorf("Japanese menu missing %q:\n%s", want, stderr)
		}
	}
	_, stdout, stderr = captureBoth(t, func() int {
		return applyCompletionInstall(app, commonOpts{Format: formatText}, "zsh", script, installFpath)
	})
	if stdout != "kae の zsh 補完は最新です: "+path+"\n" {
		t.Errorf("Japanese report = %q", stdout)
	}
	if !strings.Contains(stderr, "  fpath=("+filepath.Dir(path)+" $fpath)\n") || !strings.HasPrefix(stderr, "次の行が fpath に") {
		t.Errorf("Japanese note = %q", stderr)
	}
}

func TestCompletionRefreshWithNothingRegisteredInJapanese(t *testing.T) {
	app := testApp(t, nil)
	_, english := captureStdout(t, func() int { return runCompletionRefresh(app, commonOpts{Format: formatText}) })
	if english != "No registered kae completion to refresh; run: kae completion <bash|zsh|fish> --install\n" {
		t.Errorf("English = %q", english)
	}
	l10ntest.UseJapanese(t)
	_, ja := captureStdout(t, func() int { return runCompletionRefresh(app, commonOpts{Format: formatText}) })
	if ja != "更新する kae の補完が登録されていません。kae completion <bash|zsh|fish> --install を実行してください\n" {
		t.Errorf("Japanese = %q", ja)
	}
}

// The mise init preview keeps its blank line and English bytes on stderr.
func TestMiseInitPreviewHintInBothLanguages(t *testing.T) {
	app := testApp(t, nil)
	chdirTemp(t)
	run := func() int {
		return runMiseInit(context.Background(), app, commonOpts{Format: formatText}, "main", constants.ModeAuth, true, false)
	}
	_, _, english := captureBoth(t, run)
	if english != "\nkae: preview only; to apply, run: kae mise init --profile main --auto --write\n" {
		t.Errorf("English hint = %q", english)
	}
	l10ntest.UseJapanese(t)
	_, _, ja := captureBoth(t, run)
	if ja != "\nkae: プレビューのみです。適用するには kae mise init --profile main --auto --write を実行してください。\n" {
		t.Errorf("Japanese hint = %q", ja)
	}
}

// The uninstall report renders its manual actions in Japanese while JSON
// manual_actions keeps the English sentences.
func TestUninstallManualActionsInJapaneseAndEnglishJSON(t *testing.T) {
	app := testApp(t, nil)
	executable := filepath.Join(app.Env.Home, "kae")
	run := func(format string) (string, string) {
		_, stdout, stderr := captureBoth(t, func() int {
			return runUninstall(context.Background(), app, commonOpts{Format: format, DryRun: true}, nil, executable)
		})
		return stdout, stderr
	}
	l10ntest.UseJapanese(t)
	text, _ := run(formatText)
	for _, want := range []string{
		"アンインストール: 連携 " + constants.UninstallPending + "、実行ファイル " + constants.UninstallPending,
		"残すデータ:\n",
		"検出の対象は、記録済みの固定したディレクトリ",
		"記録のない実行ファイルです: " + executable + "。",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("Japanese report missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Discovery covers") || strings.Contains(text, "Retained data") {
		t.Errorf("English prose under Japanese:\n%s", text)
	}

	l10n.Set(l10n.English) // JSON mode selects English, as Select does
	out, _ := run(formatJSON)
	var report uninstallWire
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Manual) < 4 || !strings.HasPrefix(report.Manual[0], "Discovery covers known bound directories") ||
		report.Manual[len(report.Manual)-1] != "Unrecorded executable: "+executable+". A manual copy or plain go install has no removal receipt; reinstall this path through the supported direct installer before automatic removal." {
		t.Errorf("JSON manual_actions = %q", report.Manual)
	}
}
