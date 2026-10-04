package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/l10n"
	"github.com/webkaz-labs/kagikae/internal/testutil/l10ntest"
)

// failureCase is one error the run, use, add, relogin and backup paths end on,
// built by the function that builds it, with its English and Japanese renderings and
// the exit code it carries. The English is the text before localization, byte for
// byte: it is also what JSON carries.
type failureCase struct {
	name   string
	err    func(t *testing.T) error
	en, ja string
	exit   int
}

func failureCases() []failureCase {
	cause := errors.New("open /work/side-project/.claude/.credentials.json: permission denied")
	return []failureCase{
		{
			name: "a login that cannot start quotes its cause verbatim",
			err:  func(t *testing.T) error { return errLaunchLogin("claude", cause) },
			en:   "launch claude login: open /work/side-project/.claude/.credentials.json: permission denied",
			ja:   "claude のログインを開始できません: open /work/side-project/.claude/.credentials.json: permission denied",
			exit: constants.ExitError,
		},
		{
			name: "the wrap keeps the cause's exit code",
			err:  func(t *testing.T) error { return errResolveCwd(fs.ErrPermission) },
			en:   "resolve the current directory: permission denied",
			ja:   "カレントディレクトリを解決できません: permission denied",
			exit: constants.ExitPermission,
		},
		{
			name: "a double failure names the operation as a message",
			err: func(t *testing.T) error {
				return doubleFailure(msgf("apply %s", "claude"), fmt.Errorf("write: %w", artifact.ErrUnsafe),
					l10n.Errorf("restore %s/%s: %w", "claude", "credentials", cause), "20261004T000000Z")
			},
			en: "apply claude failed (write: " + artifact.ErrUnsafe.Error() + ") and restore also failed " +
				"(restore claude/credentials: open /work/side-project/.claude/.credentials.json: permission denied); " +
				"run: kae rollback --to 20261004T000000Z",
			ja: "claude の適用に失敗し（write: " + artifact.ErrUnsafe.Error() + "）、元の状態への復元にも失敗しました" +
				"（claude/credentials を復元できません: open /work/side-project/.claude/.credentials.json: permission denied）。" +
				"kae rollback --to 20261004T000000Z を実行してください",
			exit: constants.ExitUnsafeRefused,
		},
		{
			name: "a double failure of a login step",
			err: func(t *testing.T) error {
				return doubleFailure(loginStepDetect.phrase(), cause, cause, "20261004T000000Z")
			},
			en: "detect the logged-in account failed (" + cause.Error() + ") and restore also failed (" + cause.Error() +
				"); run: kae rollback --to 20261004T000000Z",
			ja: "ログインしたアカウントの検出に失敗し（" + cause.Error() + "）、元の状態への復元にも失敗しました（" + cause.Error() +
				"）。kae rollback --to 20261004T000000Z を実行してください",
			exit: constants.ExitError,
		},
		{
			name: "a single explicit tool without home isolation",
			err: func(t *testing.T) error {
				_, err := isolatableTargets([]runTarget{{Tool: "cursor", Account: "main"}}, false,
					msgf("global isolated mode (kae use -i)"), "use -i")
				return err
			},
			en:   "cursor has no home-isolation env var; global isolated mode (kae use -i) supports claude and codex only",
			ja:   "cursor にはホームを独立させる環境変数がありません。グローバル独立モード（kae use -i）が対応するのは claude と codex だけです",
			exit: constants.ExitUnsupported,
		},
		{
			name: "relogin names what the directory binds in the enumeration of the language",
			err: func(t *testing.T) error {
				_, err := reloginTool(testApp(t, nil), "pin-id",
					fragmentInfo{Accounts: map[string]string{"codex": "main", "agy": "side"}}, "claude")
				return err
			},
			en:   "this directory does not bind claude; it binds codex, agy",
			ja:   "このディレクトリは claude を固定していません（固定しているツール: codex、agy）",
			exit: constants.ExitNotFound,
		},
		{
			name: "relogin in a directory that binds nothing kae knows",
			err: func(t *testing.T) error {
				_, err := reloginTool(testApp(t, nil), "pin-id", fragmentInfo{Accounts: map[string]string{}}, "")
				return err
			},
			en:   "this directory binds no tool kae can drive a login for (it binds no tools)",
			ja:   "このディレクトリには、kae がログインを実行できるツールが固定されていません（固定しているツール: なし）",
			exit: constants.ExitNotFound,
		},
		{
			name: "a wrapped step of the backup quotes the lower error",
			err: func(t *testing.T) error {
				return l10n.Errorf("backup %s: %w", "claude", l10n.Errorf("read payload %s: %w", "kae/backup/x", cause))
			},
			en: "backup claude: read payload kae/backup/x: " + cause.Error(),
			ja: "claude をバックアップできません: 保存データ kae/backup/x を読み取れません: " + cause.Error(),
		},
	}
}

// Each failure renders English through Error() (and so in JSON) in both languages,
// localized only through a human sink, with the same exit code.
func TestRunLoginFailuresRenderInBothLanguages(t *testing.T) {
	for _, tc := range failureCases() {
		t.Run("English/"+tc.name, func(t *testing.T) {
			err := tc.err(t)
			if got := l10n.Render(err); got != tc.en {
				t.Errorf("English:\n got %q\nwant %q", got, tc.en)
			}
			if tc.exit != 0 && exitOf(err) != tc.exit {
				t.Errorf("exit = %d, want %d", exitOf(err), tc.exit)
			}
		})
	}
	for _, tc := range failureCases() {
		t.Run("Japanese/"+tc.name, func(t *testing.T) {
			l10ntest.UseJapanese(t)
			err := tc.err(t)
			if got := l10n.Render(err); got != tc.ja {
				t.Errorf("Japanese:\n got %q\nwant %q", got, tc.ja)
			}
			if got := err.Error(); got != tc.en {
				t.Errorf("Error() under Japanese = %q, want the English %q", got, tc.en)
			}
			if tc.exit != 0 && exitOf(err) != tc.exit {
				t.Errorf("exit = %d, want %d", exitOf(err), tc.exit)
			}
		})
	}
}

// The JSON error report of a wrapped failure is the English text even with Japanese
// selected, and the exit code is the cause's.
func TestWrappedFailureJSONStaysEnglish(t *testing.T) {
	l10ntest.UseJapanese(t)
	code, stdout := captureStdout(t, func() int {
		return finish(commonOpts{Format: formatJSON}, errResolveCwd(fs.ErrPermission))
	})
	if code != constants.ExitPermission {
		t.Errorf("exit = %d, want %d", code, constants.ExitPermission)
	}
	var report struct{ Message string }
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("%v: %q", err, stdout)
	}
	if report.Message != "resolve the current directory: permission denied" {
		t.Errorf("message = %q", report.Message)
	}
}

// joinNames lists names in the enumeration of the selected language and keeps a
// single name as itself.
func TestJoinNamesUsesTheLanguagesEnumeration(t *testing.T) {
	render := func(v any) string {
		if m, ok := v.(error); ok {
			return l10n.Render(m)
		}
		return fmt.Sprint(v)
	}
	for _, lang := range []struct {
		name      string
		set       func(t *testing.T)
		one, many string
	}{
		{"English", func(t *testing.T) {}, "claude", "claude, codex, agy"},
		{"Japanese", l10ntest.UseJapanese, "claude", "claude、codex、agy"},
	} {
		t.Run(lang.name, func(t *testing.T) {
			lang.set(t)
			if got := render(joinNames([]string{"claude"})); got != lang.one {
				t.Errorf("one name = %q, want %q", got, lang.one)
			}
			many := joinNames([]string{"claude", "codex", "agy"})
			if got := render(many); got != lang.many {
				t.Errorf("three names = %q, want %q", got, lang.many)
			}
			if got := many.(error).Error(); got != "claude, codex, agy" {
				t.Errorf("Error() = %q, want the English list", got)
			}
		})
	}
}

// The stderr and stdout lines of the run, add, relogin and preservation paths, through
// their sinks: the English is byte for byte what the tests and docs/VALIDATION.md
// grep (relogin's capture line is compared whole), and the Japanese is the catalog's.
func TestRunLoginLinesRenderInBothLanguages(t *testing.T) {
	stderrLines := []struct {
		name   string
		run    func()
		en, ja string
	}{
		{
			name: "run -i names the shared home on two lines",
			run: func() {
				infof("run -i: %s runs in %s\n  (shared with `kae use -i %s`; concurrent `kae use` in other shells is not blocked)",
					"claude", "~/.local/share/kagikae/isolation/global/claude/main", "main")
			},
			en: "kae: run -i: claude runs in ~/.local/share/kagikae/isolation/global/claude/main\n" +
				"  (shared with `kae use -i main`; concurrent `kae use` in other shells is not blocked)\n",
			ja: "kae: run -i: claude は ~/.local/share/kagikae/isolation/global/claude/main で実行します。\n" +
				"  （kae use -i main と共有します。ほかのシェルで同時に実行する kae use はブロックされません）\n",
		},
		{
			name: "a list issue keeps its code and entry as tokens",
			run: func() {
				d := listDiagnostics{Issues: []listIssue{{Code: constants.ListIssueRead, Entry: "sha256:ab"}}}
				d.print()
			},
			en: "kae: metadata listing is incomplete; readable records are shown\n" +
				"kae: " + constants.ListIssueRead + " sha256:ab; check metadata file and parent-directory permissions; keep the entry while investigating\n",
			ja: "kae: メタデータの一覧が不完全です。読み取れた記録だけを表示しています。\n" +
				"kae: " + constants.ListIssueRead + " sha256:ab。メタデータのファイルと親ディレクトリの権限を確認してください。調べている間はそのエントリを残してください。\n",
		},
		{
			name: "previous auth state restored",
			run:  func() { infof("previous auth state restored (backup %s)", "20261004T000000Z") },
			en:   "kae: previous auth state restored (backup 20261004T000000Z)\n",
			ja:   "kae: 以前の認証状態を復元しました（バックアップ 20261004T000000Z）。\n",
		},
		{
			name: "add names the account it captures as",
			run: func() {
				infof("complete the %s login flow; the result is captured as %s when it exits (previous state backed up as %s)",
					"claude", "claude/main", "20261004T000000Z")
			},
			en: "kae: complete the claude login flow; the result is captured as claude/main when it exits (previous state backed up as 20261004T000000Z)\n",
			ja: "kae: claude のログイン手順を完了してください。終了すると、結果を claude/main として登録します（以前の状態はバックアップ 20261004T000000Z に保存しました）。\n",
		},
		{
			name: "relogin runs against the bound store",
			run: func() {
				infof("complete the %s login flow; kae is running it against this directory's own store (%s), "+
					"so it refreshes %s/%s and not the real home", "claude", "CLAUDE_CONFIG_DIR=~/code/side-project/.kae", "claude", "side")
			},
			en: "kae: complete the claude login flow; kae is running it against this directory's own store " +
				"(CLAUDE_CONFIG_DIR=~/code/side-project/.kae), so it refreshes claude/side and not the real home\n",
			ja: "kae: claude のログイン手順を完了してください。kae はこのディレクトリ専用のストア（CLAUDE_CONFIG_DIR=~/code/side-project/.kae）に対して実行するため、" +
				"更新されるのは本物のホームではなく claude/side です。\n",
		},
	}
	for _, tc := range stderrLines {
		t.Run("English/"+tc.name, func(t *testing.T) {
			_, stderr := captureStderr(t, func() int { tc.run(); return 0 })
			if stderr != tc.en {
				t.Errorf("English:\n got %q\nwant %q", stderr, tc.en)
			}
		})
		t.Run("Japanese/"+tc.name, func(t *testing.T) {
			l10ntest.UseJapanese(t)
			_, stderr := captureStderr(t, func() int { tc.run(); return 0 })
			if stderr != tc.ja {
				t.Errorf("Japanese:\n got %q\nwant %q", stderr, tc.ja)
			}
		})
	}
	reloginCaptured := func() int {
		reportf("Captured the changed %s credential for %s/%s from this directory's store", "claude", "claude", "side")
		return 0
	}
	if _, got := captureStdout(t, reloginCaptured); got != "Captured the changed claude credential for claude/side from this directory's store\n" {
		t.Errorf("English relogin capture line = %q", got)
	}
	t.Run("Japanese relogin capture line", func(t *testing.T) {
		l10ntest.UseJapanese(t)
		if _, got := captureStdout(t, reloginCaptured); got != "このディレクトリのストアから、変更された claude の認証情報を claude/side へ取り込み直しました\n" {
			t.Errorf("Japanese relogin capture line = %q", got)
		}
	})
}

// The preservation list header is localized; the ID column is a token.
func TestPreservationListHeaderIsLocalized(t *testing.T) {
	app := testApp(t, nil)
	list := func() string {
		_, stdout := captureStdout(t, func() int {
			return runPreservation(context.Background(), app, commonOpts{Format: formatText}, "list", "")
		})
		return stdout
	}
	if got := list(); !strings.Contains(got, "Binding account (owner unknown)") {
		t.Fatalf("English: %q", got)
	}
	l10ntest.UseJapanese(t)
	got := list()
	for _, want := range []string{"ID", "ツール", "ディレクトリ", "固定したアカウント（所有者は不明）", "状態", "バイト数"} {
		if !strings.Contains(got, want) {
			t.Errorf("Japanese header lacks %q: %q", want, got)
		}
	}
}
