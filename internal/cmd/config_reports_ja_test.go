package cmd

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/config"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/l10n"
	"github.com/webkaz-labs/kagikae/internal/testutil/l10ntest"
)

// The account, profile, backup, companion, init and install reports, asserted in
// English (unchanged bytes) and in Japanese (the real catalog). The Japanese
// cases select Japanese process-wide, so none of these tests may call t.Parallel.

func configReportCases() []useReportCase {
	stdout := func(f func()) func(t *testing.T) string {
		return func(t *testing.T) string {
			_, out := captureStdout(t, func() int { f(); return 0 })
			return out
		}
	}
	rm := accountRmReport{Tool: "claude", Account: "side", SecretsRemoved: 2, ProfilesUpdated: []string{"main", "side"}, ActiveCleared: true}
	dryRm := rm
	dryRm.DryRun = true
	rename := accountRenameReport{Tool: "claude", Old: "side", New: "alt", SecretsMoved: 1, ProfilesUpdated: []string{"main"}, ActiveUpdated: true}
	dryRename := rename
	dryRename.DryRun = true
	identity := accountSetIdentityReport{Tool: "agy", Account: "main", Identity: "you@example.com"}
	dryIdentity := identity
	dryIdentity.DryRun = true
	saved := profileReport{Profile: "main", Accounts: map[string]string{"claude": "main", "codex": "side"}}
	set := profileReport{Profile: "main", Accounts: map[string]string{"claude": "side"}}
	return []useReportCase{
		{
			name: "account rm",
			run:  stdout(func() { printAccountRm(&rm) }),
			en: "Removed claude/side (2 secret item(s))\n" +
				"  dropped the claude reference from profile(s): main, side\n" +
				"  cleared the active claude account in state\n",
			ja: "claude/side を削除しました（シークレットストアの項目 2 件）\n" +
				"  プロファイルから claude の参照を外しました: main、side\n" +
				"  状態に記録した claude の有効なアカウントを解除しました\n",
		},
		{
			name: "account rm --dry-run",
			run:  stdout(func() { printAccountRm(&dryRm) }),
			en:   "Would remove claude/side (2 secret item(s))\n  dropped the claude reference from profile(s): main, side\n  cleared the active claude account in state\n",
			ja:   "claude/side を削除する予定です（シークレットストアの項目 2 件）\n  プロファイルから claude の参照を外しました: main、side\n  状態に記録した claude の有効なアカウントを解除しました\n",
		},
		{
			name: "account rename",
			run:  stdout(func() { printAccountRename(&rename) }),
			en: "Renamed claude/side to claude/alt (1 secret item(s))\n" +
				"  rewrote the claude reference in profile(s): main\n" +
				"  updated the active claude account in state\n",
			ja: "claude/side の名前を claude/alt に変更しました（シークレットストアの項目 1 件）\n" +
				"  プロファイルの claude の参照を書き換えました: main\n" +
				"  状態に記録した claude の有効なアカウントを更新しました\n",
		},
		{
			name: "account rename --dry-run",
			run:  stdout(func() { printAccountRename(&dryRename) }),
			en:   "Would rename claude/side to claude/alt (1 secret item(s))\n  rewrote the claude reference in profile(s): main\n  updated the active claude account in state\n",
			ja:   "claude/side の名前を claude/alt に変更する予定です（シークレットストアの項目 1 件）\n  プロファイルの claude の参照を書き換えました: main\n  状態に記録した claude の有効なアカウントを更新しました\n",
		},
		{
			name: "account set-identity",
			run:  stdout(func() { printAccountSetIdentity(&identity) }),
			en:   "Set the agy/main identity to you@example.com\n",
			ja:   "agy/main のログイン識別子を you@example.com に設定しました\n",
		},
		{
			name: "account set-identity --dry-run",
			run:  stdout(func() { printAccountSetIdentity(&dryIdentity) }),
			en:   "Would set the agy/main identity to you@example.com\n",
			ja:   "agy/main のログイン識別子を you@example.com に設定する予定です\n",
		},
		{
			name: "profile save",
			run:  stdout(func() { printProfileSave(&saved) }),
			en:   "Saved profile main from the active accounts:\n  claude = main\n  codex = side\n",
			ja:   "有効なアカウントからプロファイル main を保存しました:\n  claude = main\n  codex = side\n",
		},
		{
			name: "profile save --dry-run",
			run:  stdout(func() { r := saved; r.DryRun = true; printProfileSave(&r) }),
			en:   "Would save profile main from the active accounts:\n  claude = main\n  codex = side\n",
			ja:   "有効なアカウントからプロファイル main を保存する予定です:\n  claude = main\n  codex = side\n",
		},
		{
			name: "profile set",
			run:  stdout(func() { printProfileSet(&set) }),
			en:   "Set claude = side in profile main\n",
			ja:   "プロファイル main に claude = side を設定しました\n",
		},
		{
			name: "profile set --dry-run",
			run:  stdout(func() { r := set; r.DryRun = true; printProfileSet(&r) }),
			en:   "Would set claude = side in profile main\n",
			ja:   "プロファイル main に claude = side を設定する予定です\n",
		},
		{
			name: "profile unset",
			run:  stdout(func() { printProfileUnset(&set) }),
			en:   "Unset claude from profile main\n",
			ja:   "プロファイル main から claude の割り当てを外しました\n",
		},
		{
			name: "profile unset --dry-run",
			run:  stdout(func() { r := set; r.DryRun = true; printProfileUnset(&r) }),
			en:   "Would unset claude from profile main\n",
			ja:   "プロファイル main から claude の割り当てを外す予定です\n",
		},
		{
			name: "profile rm",
			run:  stdout(func() { printProfileRm(&profileReport{Profile: "side"}) }),
			en:   "Removed profile side\n",
			ja:   "プロファイル side を削除しました\n",
		},
		{
			name: "profile rm --dry-run",
			run:  stdout(func() { printProfileRm(&profileReport{Profile: "side", DryRun: true}) }),
			en:   "Would remove profile side\n",
			ja:   "プロファイル side を削除する予定です\n",
		},
		{
			name: "rollback",
			run: stdout(func() {
				printRollback(&rollbackReport{BackupID: "20261004T000000Z", Restored: []restoredItem{{Tool: "claude", Artifacts: 2}}})
			}),
			en: "Rolled back to backup 20261004T000000Z\n  claude: 2 artifact(s)\n",
			ja: "バックアップ 20261004T000000Z に戻しました\n  claude: 認証要素 2 件\n",
		},
		{
			name: "rollback --dry-run",
			run:  stdout(func() { printRollback(&rollbackReport{DryRun: true, BackupID: "20261004T000000Z"}) }),
			en:   "Would roll back to backup 20261004T000000Z\n",
			ja:   "バックアップ 20261004T000000Z に戻す予定です\n",
		},
		{
			name: "backup list, empty",
			run: func(t *testing.T) string {
				app := testApp(t, nil)
				_, out := captureStdout(t, func() int { return runBackupList(context.Background(), app, commonOpts{Format: formatText}) })
				return out
			},
			en: "no backups yet (backups are created automatically before each switch)\n",
			ja: "バックアップはまだありません（バックアップは切替の前に毎回自動で作られます）\n",
		},
		{
			name: "profile default",
			run: stdout(func() {
				printProfileDefault(&profileReport{DefaultProfile: "main"})
				printProfileDefault(&profileReport{})
				printProfileDefault(&profileReport{DryRun: true, DefaultProfile: "side"})
				printProfileDefault(&profileReport{DryRun: true})
			}),
			en: "default_profile: main\ndefault_profile: (none)\nWould set default_profile to side\nWould clear default_profile\n",
			ja: "default_profile: main\ndefault_profile: なし\ndefault_profile を side に設定する予定です\ndefault_profile を解除する予定です\n",
		},
	}
}

func TestConfigReportsAreLocalized(t *testing.T) {
	for _, c := range configReportCases() {
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

// kae init's report, end to end: the blank line before the steps stays outside
// the catalog, and the commands keep their column.
func TestInitReportIsLocalized(t *testing.T) {
	run := func(t *testing.T) string {
		app := testApp(t, nil)
		code, out := captureStdout(t, func() int {
			return runInit(context.Background(), app, commonOpts{Format: formatText})
		})
		mustExit(t, constants.ExitOK, code, out)
		return strings.ReplaceAll(out, app.displayPath(app.ConfigPath), "CONFIG")
	}
	en := "Created CONFIG\n\nNext steps:\n" +
		"  kae doctor                             # check the environment\n" +
		"  kae add --no-login <tool> <account>    # snapshot the current login\n"
	if got := run(t); got != en {
		t.Errorf("english:\n got %q\nwant %q", got, en)
	}
	l10ntest.UseJapanese(t)
	ja := "CONFIG を作成しました\n\n次の手順:\n" +
		"  kae doctor                             # 環境の確認\n" +
		"  kae add --no-login <tool> <account>    # 現在のログインのスナップショット\n"
	if got := run(t); got != ja {
		t.Errorf("japanese:\n got %q\nwant %q", got, ja)
	}
}

// kae companion add / rm / list: the reports, the headers and the list separator
// follow the language; JSON does not.
func TestCompanionReportsAreLocalized(t *testing.T) {
	ctx := context.Background()
	steps := func(t *testing.T) []string {
		app := applyTestApp(t, nil)
		if err := os.MkdirAll(app.Paths.ConfigDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(app.ConfigPath, []byte(config.InitialContent("")+"\n[profiles.main.accounts]\nclaude = \"main\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if cfg, _, err := config.Load(app.ConfigPath); err != nil {
			t.Fatal(err)
		} else {
			app.Config = cfg
		}
		opts := commonOpts{Format: formatText}
		var outs []string
		for _, f := range []func() int{
			func() int { return runCompanionList(ctx, app, opts) },
			func() int {
				return runCompanionAdd(ctx, app, opts, []string{"main", "git", "email=you@example.com", "name=You"})
			},
			func() int { return runCompanionList(ctx, app, opts) },
			func() int { return runCompanionRm(ctx, app, opts, []string{"main", "git", "name"}) },
			func() int { return runCompanionRm(ctx, app, opts, []string{"main", "git"}) },
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
			"no companion bindings; run: kae companion add <profile> <id> KEY=VALUE\n",
			"Bound companion git for profile main: email, name\n",
			"Profile  Companion  Knobs",
			"Removed 1 knob(s) from companion git in profile main: name\n",
			"Removed companion git from profile main\n",
		})
	})
	t.Run("japanese", func(t *testing.T) {
		l10ntest.UseJapanese(t)
		check(t, steps(t), []string{
			"周辺ツールの設定がありません。kae companion add <profile> <id> KEY=VALUE を実行してください\n",
			"プロファイル main に周辺ツール git を設定しました: email、name\n",
			"プロファイル  周辺ツール  設定項目",
			"プロファイル main の周辺ツール git から設定項目 1 件を削除しました: name\n",
			"プロファイル main から周辺ツール git を削除しました\n",
		})
	})
}

// The unknown-companion refusal lists the companions with the line's separator in
// a person's line and keeps the English list in JSON.
func TestUnknownCompanionListFollowsTheLanguage(t *testing.T) {
	err := errf(constants.ExitUsage, "unknown companion %q (known: %s)", "zz", l10n.List([]string{"gh", "git"}))
	if got := err.Error(); got != `unknown companion "zz" (known: gh, git)` {
		t.Errorf("Error() = %q", got)
	}
	l10ntest.UseJapanese(t)
	if got := l10n.Render(err); got != `不明な周辺ツールです: "zz"（対応: gh、git）` {
		t.Errorf("Render = %q", got)
	}
	data, jerr := json.Marshal(errorReport{Message: err.Error()})
	if jerr != nil || !strings.Contains(string(data), `(known: gh, git)`) {
		t.Errorf("JSON = %s (%v)", data, jerr)
	}
}

func TestJoinList(t *testing.T) {
	cases := []struct {
		items  []string
		en, ja string
	}{
		{nil, "", ""},
		{[]string{"a"}, "a", "a"},
		{[]string{"a", "b"}, "a, b", "a、b"},
		{[]string{"a", "b", "c"}, "a, b, c", "a、b、c"},
	}
	for _, c := range cases {
		m := l10n.List(c.items)
		if m.Error() != c.en {
			t.Errorf("l10n.List(%q).Error() = %q, want %q", c.items, m.Error(), c.en)
		}
	}
	l10ntest.UseJapanese(t)
	for _, c := range cases {
		m := l10n.List(c.items)
		if got := l10n.Render(m); got != c.ja {
			t.Errorf("Render(l10n.List(%q)) = %q, want %q", c.items, got, c.ja)
		}
		if m.Error() != c.en {
			t.Errorf("Japanese changed Error(): %q", m.Error())
		}
	}
}
