package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/l10n"
	"github.com/webkaz-labs/kagikae/internal/testutil/l10ntest"
)

// warningCase is one `kae: warning:` / `kae: note:` / bare `kae:` continuation line
// (docs/CLI.md § Localization) with its English and Japanese renderings.
// The English is the line as it was before the stage, byte for byte: the sinks must
// not change it.
type warningCase struct {
	name   string
	run    func(t *testing.T)
	en, ja string
}

func warningCases() []warningCase {
	refusedClause := dirCredentialRefusalClause("claude", bindDirs{Config: "/work/side-project/.claude"}, "main",
		harvestRefusal{Why: msgf("its identity names a different account")})
	return []warningCase{
		{
			name: "constant format",
			run:  func(t *testing.T) { warnLoggedOutUnchanged("claude", "main") },
			en:   "kae: warning: claude is logged out; snapshot claude/main left unchanged\n",
			ja:   "kae: warning: claude はログアウトしています。スナップショット claude/main は変更していません。\n",
		},
		{
			name: "external cause is quoted whole in English",
			run: func(t *testing.T) {
				warnRecaptureFailed("claude", "main", errors.New("open x: permission denied"))
			},
			en: "kae: warning: recapture of claude/main failed: open x: permission denied\n",
			ja: "kae: warning: スナップショット claude/main の更新に失敗しました: open x: permission denied\n",
		},
		{
			name: "note",
			run: func(t *testing.T) {
				notef("the new tree store %s starts empty", "~/code/side-project/.kae")
			},
			en: "kae: note: the new tree store ~/code/side-project/.kae starts empty\n",
			ja: "kae: note: ツリーモードの新しいストア ~/code/side-project/.kae は空の状態から始まります。\n",
		},
		{
			name: "a reason carried as a message value",
			run: func(t *testing.T) {
				warnSnapshotUnchanged("claude", "main",
					msgf("the live %s credential needs a re-login while snapshot %s/%s still holds a usable one",
						"claude", "claude", "main"))
			},
			en: "kae: warning: the live claude credential needs a re-login while snapshot claude/main still holds a usable one; snapshot claude/main left unchanged\n",
			ja: "kae: warning: 現在の claude の認証情報は再ログインが必要ですが、スナップショット claude/main にはまだ使えるものが残っています。スナップショット claude/main は変更していません。\n",
		},
		{
			name: "a clause that wraps a refusal reason",
			run:  func(t *testing.T) { warnf("%s, so this write replaces it", refusedClause) },
			en: "kae: warning: kae is not harvesting the claude credential already in /work/side-project/.claude into snapshot claude/main " +
				"because its identity names a different account, so this write replaces it\n",
			ja: "kae: warning: ログイン中アカウントの記録が別のアカウントを示しているため、/work/side-project/.claude にすでにある claude の認証情報は、" +
				"スナップショット claude/main へ退避しません。そのため、この書き込みでそのコピーを置き換えます。\n",
		},
		{
			name: "a continuation line",
			run: func(t *testing.T) {
				warnRecaptureDeclined("claude", "main", msgf("its identity names a different account"), "", declinedByUse, declinedOneAccount)
			},
			en: "kae: warning: its identity names a different account; snapshot claude/main left unchanged\n" +
				"kae: kae could not preserve the live claude login it declined to adopt; it is lost once the previous state is restored\n",
			ja: "kae: warning: ログイン中アカウントの記録が別のアカウントを示している。スナップショット claude/main は変更していません。\n" +
				"kae: kae は、取り込まないと判断した現在の claude のログインを保全できませんでした。以前の状態を復元すると、そのログインは失われます。\n",
		},
		{
			name: "a message value that is not a failure",
			run: func(t *testing.T) {
				warnMessage(staleProfileBindingMessage("~/code/side-project", "side"))
			},
			en: "kae: warning: ~/code/side-project is still bound to profile side, which no longer exists; " +
				"to re-bind it, run: cd ~/code/side-project && kae pin <profile>\n",
			ja: "kae: warning: ~/code/side-project は、もう存在しないプロファイル side に固定されたままです。" +
				"固定し直すには cd ~/code/side-project && kae pin <profile> を実行してください。\n",
		},
		{
			name: "a warning that also reaches JSON is a value and renders English when the catalog lacks it",
			run:  func(t *testing.T) { warnMessage(msgf("claude: CLAUDE_CONFIG_DIR is relative")) },
			en:   "kae: warning: claude: CLAUDE_CONFIG_DIR is relative\n",
			ja:   "kae: warning: claude: CLAUDE_CONFIG_DIR is relative\n",
		},
		{
			name: "the unbound reason localizes on stderr only",
			run: func(t *testing.T) {
				m, _ := bindModeFor("tree")
				warnMessage(modeUnboundMessage(m, "codex"))
			},
			en: "kae: warning: tree mode binds claude only, so codex keeps the real home (docs/ROADMAP.md)\n",
			ja: "kae: warning: tree モードが固定するのは claude だけのため、codex は実ホームのままです（docs/ROADMAP.md）。\n",
		},
	}
}

// The English and Japanese renderings of every line; the Japanese test selects the
// language process-wide, so neither may run in parallel.
func TestWarningLinesRenderInBothLanguages(t *testing.T) {
	for _, tc := range warningCases() {
		t.Run("English/"+tc.name, func(t *testing.T) {
			_, stderr := captureStderr(t, func() int { tc.run(t); return 0 })
			if stderr != tc.en {
				t.Errorf("English:\n got %q\nwant %q", stderr, tc.en)
			}
		})
	}
	for _, tc := range warningCases() {
		t.Run("Japanese/"+tc.name, func(t *testing.T) {
			l10ntest.UseJapanese(t)
			_, stderr := captureStderr(t, func() int { tc.run(t); return 0 })
			if stderr != tc.ja {
				t.Errorf("Japanese:\n got %q\nwant %q", stderr, tc.ja)
			}
		})
	}
}

// A refusal's reason and the remedy strings keep their English text for JSON and
// generated files: only a human sink renders them localized.
func TestWarningMessageValuesKeepEnglishError(t *testing.T) {
	l10ntest.UseJapanese(t)
	why := msgf("its identity names a different account")
	if got := why.Error(); got != "its identity names a different account" {
		t.Errorf("Error() under Japanese = %q", got)
	}
	m, _ := bindModeFor("tree")
	if got, want := modeUnboundReason(m, "codex"), "tree mode binds claude only, so codex keeps the real home (docs/ROADMAP.md)"; got != want {
		t.Errorf("modeUnboundReason must stay English for the generated comment and uninstall: %q", got)
	}
	if got := pinLoginRemedy("claude", "~/code/side-project").Error(); !strings.HasPrefix(got, "to verify the bound account") {
		t.Errorf("pinLoginRemedy().Error() must stay English for doctor: %q", got)
	}
	ordered := dirCredentialRefusalClause("claude", bindDirs{Config: "/work/side-project/.claude"}, "main",
		harvestRefusal{Why: why, Ordered: true})
	if got, want := l10n.Render(ordered), "/work/side-project/.claude にすでにある claude の認証情報はスナップショット claude/main より新しいものの、"+
		"ログイン中アカウントの記録が別のアカウントを示しているため、kae は退避しません"; got != want {
		t.Errorf("ordered clause:\n got %q\nwant %q", got, want)
	}
	if !(harvestRefusal{}).Why.Empty() || why.Empty() {
		t.Error("only the zero message is empty")
	}
}

// A note a real command writes reaches stderr localized, through the same sink.
func TestFilteredDoctorNoteIsLocalized(t *testing.T) {
	app := testApp(t, nil)
	run := func() string {
		_, stderr := captureStderr(t, func() int {
			return runDoctor(context.Background(), app, commonOpts{Format: formatText}, "claude")
		})
		return stderr
	}
	const en = "kae: note: companion and bound-directory checks are not per-tool and were skipped; to include them, run: kae doctor\n"
	if got := run(); !strings.Contains(got, en) {
		t.Fatalf("English: %q", got)
	}
	l10ntest.UseJapanese(t)
	const ja = "kae: note: 周辺ツールと固定したディレクトリの検査はツールごとではないため、省略しました。含めるには kae doctor を実行してください。\n"
	if got := run(); !strings.Contains(got, ja) {
		t.Fatalf("Japanese: %q", got)
	}
}
