package cmd

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/freshness"
	"github.com/webkaz-labs/kagikae/internal/l10n"
	"github.com/webkaz-labs/kagikae/internal/testutil/l10ntest"
)

// useReportCase is one stdout or stderr rendering of the use family and its
// siblings, asserted in English (unchanged bytes)
// and in Japanese (the real catalog).
type useReportCase struct {
	name   string
	run    func(t *testing.T) string
	en, ja string
}

func useReportCases() []useReportCase {
	stdout := func(f func()) func(t *testing.T) string {
		return func(t *testing.T) string {
			_, out := captureStdout(t, func() int { f(); return 0 })
			return out
		}
	}
	stderr := func(f func()) func(t *testing.T) string {
		return func(t *testing.T) string {
			_, out := captureStderr(t, func() int { f(); return 0 })
			return out
		}
	}
	profile := "main"
	staleInfo := freshness.Info{Known: true, ExpiresAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)}
	dryReport := &switchReport{
		DryRun: true, Profile: &profile,
		Results: []switchResult{{
			Tool: "claude", Account: "main", Driver: "json",
			Actions: []action{
				{Kind: "json-pointer", Target: "~/.claude.json", Pointer: "/oauthAccount"},
				{Kind: "file", Target: "~/.claude/.credentials.json"},
			},
			Warnings: []l10n.Msg{msgf("snapshot credential %s", msgf("needs an interactive re-login in %s (%s)", msgf("%d day(s)", 3), "2026-10-07T00:00:00Z"))},
		}},
	}
	return []useReportCase{
		{
			name: "dry-run switch report",
			run:  stdout(func() { printSwitchReport(dryReport) }),
			en: "Would switch profile to main\n" +
				"\nclaude -> main (driver: json)\n" +
				"  patch ~/.claude.json /oauthAccount\n" +
				"  replace ~/.claude/.credentials.json\n" +
				"  preserve all other keys, settings, skills, hooks, history\n" +
				"  warning: snapshot credential needs an interactive re-login in 3 day(s) (2026-10-07T00:00:00Z)\n",
			ja: "プロファイル main に切り替える予定です\n" +
				"\nclaude -> main（ドライバー: json）\n" +
				"  ~/.claude.json の /oauthAccount を書き換えます\n" +
				"  ~/.claude/.credentials.json を置き換えます\n" +
				"  ほかのキー、設定、スキル、フック、履歴はそのまま残します\n" +
				"  警告: スナップショットの認証情報は、あと 3 日で対話的な再ログインが必要になります（2026-10-07T00:00:00Z）。\n",
		},
		{
			name: "applied switch report",
			run: stdout(func() {
				printSwitchReport(&switchReport{
					Profile: &profile, BackupID: "20261004T000000Z",
					Results: []switchResult{{Tool: "claude", Account: "main"}},
				})
			}),
			en: "Switched claude -> main\nActive profile: main\nBackup: 20261004T000000Z; to undo, run: kae rollback\n",
			ja: "切り替えました: claude -> main\n有効なプロファイル: main\nバックアップ: 20261004T000000Z。元に戻すには kae rollback を実行してください\n",
		},
		{
			name: "bare use no change",
			run:  stdout(func() { printBareUseReport(&bareUseReport{Profile: &profile}) }),
			en:   "Profile main already active (no changes)\n",
			ja:   "プロファイル main はすでに有効です（変更なし）\n",
		},
		{
			name: "capture report",
			run: stdout(func() {
				res := []captureResult{{
					Tool: "claude", Account: "main", Driver: "json",
					Actions: []action{{Kind: "file", Target: "~/.claude/.credentials.json"}},
				}}
				printCaptureReport(nil, &captureReport{Results: res})
				printCaptureReport(nil, &captureReport{DryRun: true, Results: res})
			}),
			en: "Captured claude/main (driver: json)\n  file ~/.claude/.credentials.json\n" +
				"Would capture claude/main (driver: json)\n  file ~/.claude/.credentials.json\n",
			ja: "登録しました: claude/main（ドライバー: json）\n  file ~/.claude/.credentials.json\n" +
				"登録する予定です: claude/main（ドライバー: json）\n  file ~/.claude/.credentials.json\n",
		},
		{
			name: "stale credential warning before apply",
			run: stderr(func() {
				warnBeforeApply([]switchResult{{
					Tool: "claude", Account: "main",
					Warnings: []l10n.Msg{msgf("snapshot credential is stale: %s", staleCredentialDetail(staleInfo, "claude", "main"))},
				}}, nil)
			}),
			en: "kae: warning: claude: snapshot credential is stale: it expired 2026-09-01T00:00:00Z and has no refresh token; " +
				"confirm account main and the intended global store outside a bound directory; stop other sessions using that credential, then, to log in as that account, run: kae add --restore claude main (captures the new login and restores the previous live state)\n",
			ja: "kae: warning: claude: スナップショットの認証情報が失効しています: 認証情報は 2026-09-01T00:00:00Z に期限切れになり、リフレッシュトークンもありません。" +
				"固定したディレクトリの外で、アカウント main と意図したグローバルの認証ストアを確認してください。その認証情報を使っている他のセッションを止めてから、そのアカウントでログインするには kae add --restore claude main を実行してください（新しいログインを登録し、直前の状態に戻します）。\n",
		},
	}
}

func TestUseReportsAreLocalized(t *testing.T) {
	for _, c := range useReportCases() {
		t.Run(c.name+"/english", func(t *testing.T) {
			if got := c.run(t); got != c.en {
				t.Errorf("english:\n got %q\nwant %q", got, c.en)
			}
		})
		t.Run(c.name+"/japanese", func(t *testing.T) {
			l10ntest.UseJapanese(t)
			if got := c.run(t); got != c.ja {
				t.Errorf("japanese:\n got %q\nwant %q", got, c.ja)
			}
		})
	}
}

// The global-isolation dry-run goes through an App.
func TestUseGlobalIsolationReportsAreLocalized(t *testing.T) {
	ctx := context.Background()
	run := func(t *testing.T) string {
		app := applyTestApp(t, nil)
		_, out := captureStdout(t, func() int {
			return runUseIsolated(ctx, app, commonOpts{DryRun: true, Format: formatText}, "claude", "main")
		})
		return strings.ReplaceAll(out, app.Paths.MiseGlobalFragmentFile(), "FRAGMENT")
	}
	t.Run("english", func(t *testing.T) {
		out := run(t)
		if !strings.Contains(out, "Would globally isolate claude -> main\n  home: ") || !strings.Contains(out, "Would write FRAGMENT\n") {
			t.Errorf("english dry-run: %q", out)
		}
	})
	t.Run("japanese", func(t *testing.T) {
		l10ntest.UseJapanese(t)
		out := run(t)
		if !strings.Contains(out, "グローバル独立環境にする予定です: claude -> main\n  ホーム: ") || !strings.Contains(out, "FRAGMENT を書き込む予定です\n") {
			t.Errorf("japanese dry-run: %q", out)
		}
	})
}

// kae env: the one-line reports and the table, in both languages.
func TestEnvReportsAreLocalized(t *testing.T) {
	ctx := context.Background()
	steps := func(t *testing.T) []string {
		app := applyTestApp(t, nil)
		opts := commonOpts{Format: formatText}
		var outs []string
		for _, f := range []func() int{
			func() int { return runEnvList(ctx, app, opts) },
			func() int {
				return runEnvSet(ctx, app, opts, []string{"claude", "main", "ANTHROPIC_BASE_URL=https://example.com", "TEAM=a"})
			},
			func() int { return runEnvList(ctx, app, opts) },
			func() int { return runEnvUnset(ctx, app, opts, []string{"claude", "main", "TEAM"}) },
			func() int { return runEnvUnset(ctx, app, opts, []string{"claude", "main"}) },
		} {
			code, out := captureStdout(t, f)
			mustExit(t, constants.ExitOK, code, out)
			outs = append(outs, out)
		}
		return outs
	}
	check := func(t *testing.T, outs, wants []string) {
		t.Helper()
		for i, want := range wants {
			if !strings.Contains(outs[i], want) {
				t.Errorf("step %d = %q, want it to contain %q", i, outs[i], want)
			}
		}
	}
	t.Run("english", func(t *testing.T) {
		check(t, steps(t), []string{
			"no env profiles; run: kae env set <tool> <account> KEY=VALUE\n",
			"Stored 2 variable(s) in env profile claude/main: ANTHROPIC_BASE_URL, TEAM\n",
			"Variables",
			"Removed 1 variable(s) from env profile claude/main\n",
			"Deleted env profile claude/main\n",
		})
	})
	t.Run("japanese", func(t *testing.T) {
		l10ntest.UseJapanese(t)
		check(t, steps(t), []string{
			"環境変数プロファイルがありません。kae env set <tool> <account> KEY=VALUE を実行してください\n",
			"環境変数プロファイル claude/main に環境変数 2 件を保存しました: ANTHROPIC_BASE_URL、TEAM\n",
			"変数",
			"環境変数プロファイル claude/main から環境変数 1 件を削除しました\n",
			"環境変数プロファイル claude/main を削除しました\n",
		})
	})
}
