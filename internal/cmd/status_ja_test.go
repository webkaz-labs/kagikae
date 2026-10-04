package cmd

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/l10n"
	"github.com/webkaz-labs/kagikae/internal/state"
	"github.com/webkaz-labs/kagikae/internal/testutil/l10ntest"
)

// These tests render the status, accounts and doctor reports in Japanese with
// the real catalog (docs/VALIDATION.md § Output language in tests). The English
// renderings are pinned by status_test.go and doctor_test.go.

// jaHeader is the account table's header row in Japanese text output.
func jaHeader(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, columnHeader(colTool)+" ") {
			return line
		}
	}
	t.Fatalf("no table header in:\n%s", out)
	return ""
}

// The headers are chosen by column, not by their words, so a Japanese table drops
// Identity and Driver without --full and keeps the same column count as English.
func TestJapaneseAccountTablesHideIdentityAndDriverUnlessFull(t *testing.T) {
	chdirTemp(t)
	withTerminalColumns(t, 0)
	app := testApp(t, nil)
	seedIdentityAccount(t, app)
	l10ntest.UseJapanese(t)
	if got := columnHeader(colIdentity); got == "Identity" {
		t.Fatalf("the Identity header is not translated: %q", got)
	}
	wantFull := map[string]string{
		"status":    "ツール アカウント 識別子 ドライバー 認証 認証情報 利用枠 備考",
		"accounts":  "ツール アカウント 識別子 有効 ドライバー 認証情報 利用枠 登録日時",
		"ls":        "ツール アカウント 識別子 有効 ドライバー 認証情報 利用枠",
		"ls claude": "ツール アカウント 識別子 有効 ドライバー 認証情報 利用枠",
	}
	wantDefault := map[string]string{
		"status":    "ツール アカウント 認証 認証情報 利用枠 備考",
		"accounts":  "ツール アカウント 有効 認証情報 利用枠 登録日時",
		"ls":        "ツール アカウント 有効 認証情報 利用枠",
		"ls claude": "ツール アカウント 有効 認証情報 利用枠",
	}
	for name, run := range accountTableRunners(context.Background(), app) {
		code, out := captureStdout(t, func() int { return run(commonOpts{Format: formatText}) })
		mustExit(t, constants.ExitOK, code, out)
		if got := strings.Join(strings.Fields(jaHeader(t, out)), " "); got != wantDefault[name] {
			t.Errorf("%s default header = %q, want %q", name, got, wantDefault[name])
		}
		if strings.Contains(out, "main-uuid@example.com") {
			t.Errorf("%s default table must not show the identity:\n%s", name, out)
		}
		code, out = captureStdout(t, func() int { return run(commonOpts{Format: formatText, Full: true}) })
		mustExit(t, constants.ExitOK, code, out)
		if got := strings.Join(strings.Fields(jaHeader(t, out)), " "); got != wantFull[name] {
			t.Errorf("%s --full header = %q, want %q", name, got, wantFull[name])
		}
		if !strings.Contains(out, "main-uuid@example.com") {
			t.Errorf("%s --full table must show the identity:\n%s", name, out)
		}
	}
}

// The stacked layout keeps the same choice: the labels are the Japanese headers,
// and the Identity and Driver labels appear only with --full.
func TestJapaneseStackedAccountTablesHideIdentityAndDriverUnlessFull(t *testing.T) {
	chdirTemp(t)
	withTerminalColumns(t, 20)
	app := testApp(t, nil)
	seedIdentityAccount(t, app)
	l10ntest.UseJapanese(t)
	for name, run := range accountTableRunners(context.Background(), app) {
		_, out := captureStdout(t, func() int { return run(commonOpts{Format: formatText}) })
		if strings.Contains(out, "ツール ") || !strings.Contains(out, "  認証情報") {
			t.Fatalf("%s should be stacked at 20 columns:\n%s", name, out)
		}
		if strings.Contains(out, "  識別子") || strings.Contains(out, "  ドライバー") || strings.Contains(out, "main-uuid@example.com") {
			t.Errorf("%s stacked default must not show Identity or Driver:\n%s", name, out)
		}
		_, out = captureStdout(t, func() int { return run(commonOpts{Format: formatText, Full: true}) })
		if !strings.Contains(out, "  識別子") || !strings.Contains(out, "  ドライバー") || !strings.Contains(out, "main-uuid@example.com") {
			t.Errorf("%s stacked --full must show Identity and Driver:\n%s", name, out)
		}
	}
}

// Wide headers set the width a column really occupies, so the width at which a
// table turns into blocks differs between the languages (docs/CLI.md
// § Localization). The cells here are one character wide, so the headers decide
// the width: 27 columns in English, 36 in Japanese.
func TestJapaneseTableToBlockWidthDiffersFromEnglish(t *testing.T) {
	cols := []column{colTool, colAccount, colDriver, colAuth}
	rows := [][]string{{"c", "m", "f", "-"}}
	render := func() string {
		_, out := captureStdout(t, func() int { printAccountTable(cols, rows, true, false); return 0 })
		return out
	}
	withTerminalColumns(t, 27)
	english := render()
	if !strings.HasPrefix(english, "Tool ") {
		t.Fatalf("English fits in 27 columns and stays a table:\n%s", english)
	}
	t.Run("japanese", func(t *testing.T) {
		l10ntest.UseJapanese(t)
		japanese := render()
		if strings.HasPrefix(japanese, "ツール ") || !strings.Contains(japanese, "  ドライバー") {
			t.Errorf("Japanese needs 36 columns and becomes blocks at 27:\n%s", japanese)
		}
		withTerminalColumns(t, 36)
		if got := render(); !strings.HasPrefix(got, "ツール ") {
			t.Errorf("Japanese fits in 36 columns and stays a table:\n%s", got)
		}
	})
}

// printTable aligns by display width, so a Japanese header line and its rows start
// every column at the same terminal column.
func TestJapaneseTableAlignsColumnsByDisplayWidth(t *testing.T) {
	l10ntest.UseJapanese(t)
	withTerminalColumns(t, 0)
	header := []string{columnHeader(colTool), columnHeader(colAccount), columnHeader(colAuth), columnHeader(colCredential)}
	rows := [][]string{
		{"claude", "main", l10n.Sprintf("present"), l10n.Sprintf("re-login now")},
		{"codex", "side", l10n.Sprintf("absent"), "-"},
	}
	_, out := captureStdout(t, func() int { printTable(header, rows, false); return 0 })
	token := regexp.MustCompile(`\S+`)
	var starts [][]int
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		var cols []int
		for _, loc := range token.FindAllStringIndex(line, -1) {
			cols = append(cols, displayWidth(line[:loc[0]]))
		}
		starts = append(starts, cols)
	}
	for i := 1; i < len(starts); i++ {
		if len(starts[i]) != len(starts[0]) {
			t.Fatalf("row %d has %d columns, the header %d:\n%s", i, len(starts[i]), len(starts[0]), out)
		}
		for c := range starts[0] {
			if starts[i][c] != starts[0][c] {
				t.Fatalf("column %d starts at %d in row %d, at %d in the header:\n%s", c, starts[i][c], i, starts[0][c], out)
			}
		}
	}
}

// --json is the English contract, with or without --full, whatever the language.
func TestJapaneseFullLeavesJSONEnglishAndUnchanged(t *testing.T) {
	chdirTemp(t)
	app := testApp(t, nil)
	seedIdentityAccount(t, app)
	english := map[string]string{}
	runners := accountTableRunners(context.Background(), app)
	for name, run := range runners {
		_, english[name] = captureStdout(t, func() int { return run(commonOpts{Format: formatJSON, Full: true}) })
	}
	l10ntest.UseJapanese(t)
	for name, run := range runners {
		for _, full := range []bool{false, true} {
			code, out := captureStdout(t, func() int { return run(commonOpts{Format: formatJSON, Full: full}) })
			mustExit(t, constants.ExitOK, code, out)
			if out != english[name] {
				t.Errorf("%s --json (full=%v) differs under Japanese:\n%s\nvs English:\n%s", name, full, out, english[name])
			}
			if strings.Contains(out, "ツール") || strings.Contains(out, "識別子") {
				t.Errorf("%s --json carries Japanese:\n%s", name, out)
			}
		}
	}
}

// The words of the status table's cells and the report lines around it render in
// Japanese; the tokens a script reads (the status labels, the credential state
// words ok and expiring, tool and account names) stay as they are.
func TestJapaneseStatusReportLines(t *testing.T) {
	chdirTemp(t)
	withTerminalColumns(t, 0)
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	seedClaudeOAuth(t, app, endOfLifeClaudeCred(app.Now(), 2*24*time.Hour, "a"))
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "soon") })
	captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "soon") })
	l10ntest.UseJapanese(t)

	_, out := captureStdout(t, func() int { return runStatus(ctx, app, commonOpts{Format: formatText, NoColor: true}) })
	for _, want := range []string{"グローバルで有効なプロファイル: なし", "残り 2 日", "あり", "なし"} {
		if !strings.Contains(out, want) {
			t.Errorf("the status text lacks %q:\n%s", want, out)
		}
	}
	for _, leaked := range []string{"Global active profile", "day(s) left", "present", "absent"} {
		if strings.Contains(out, leaked) {
			t.Errorf("the status text keeps the English %q:\n%s", leaked, out)
		}
	}
	if !strings.Contains(out, "claude") || !strings.Contains(out, "soon") {
		t.Errorf("tool and account names stay verbatim:\n%s", out)
	}
}

func TestJapaneseStatusPrintsBoundDirectoryHomesAndWarningCount(t *testing.T) {
	l10ntest.UseJapanese(t)
	withTerminalColumns(t, 0)
	app := testApp(t, nil)
	profile := "main"
	report := &statusReport{
		Pinned:         &pinnedStatus{Profile: profile, Mode: constants.ModeShared},
		GlobalIsolated: []globalIsolatedStatus{{Tool: "claude", Account: "main", Home: "/tmp/home"}},
		ActiveProfile:  &profile,
		Tools: []toolStatus{{
			Tool: "claude", Enabled: true, Warnings: []l10n.Msg{l10n.Msgf("unknown config key %q ignored", "x")},
			Accounts: []string{},
		}},
	}
	_, out := captureStdout(t, func() int {
		printStatusReport(app, report, commonOpts{NoColor: true})
		return 0
	})
	for _, want := range []string{
		"このディレクトリ: プロファイル main（固定、shared）",
		"グローバル独立環境のホーム（kae use -i と kae run -i が共有します）:",
		"グローバルで有効なプロファイル: main",
		"警告 1 件",
		"設定ファイルの不明なキー \"x\" は無視しました。",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the status text lacks %q:\n%s", want, out)
		}
	}
	if got := credentialCell(constants.CredentialStale, "", time.Now()); got != "今すぐ再ログイン" {
		t.Errorf("a stale credential cell = %q", got)
	}
	if got := credentialCell(constants.CredentialOK, "", time.Now()); got != constants.CredentialOK {
		t.Errorf("the ok state word is a token and stays as it is: %q", got)
	}
}

func TestJapaneseAccountsEmptyHint(t *testing.T) {
	l10ntest.UseJapanese(t)
	app := testApp(t, nil)
	code, out := captureStdout(t, func() int {
		return runAccounts(context.Background(), app, commonOpts{Format: formatText})
	})
	mustExit(t, constants.ExitOK, code, out)
	if want := "登録済みのアカウントがありません。kae add <tool> <account> を実行してください\n"; out != want {
		t.Errorf("accounts = %q, want %q", out, want)
	}
}

// doctor renders each check message in Japanese and keeps the status label and
// the tool name as tokens; the summary lines are Japanese too.
func TestJapaneseDoctorReport(t *testing.T) {
	app := testApp(t, nil)
	st, err := app.loadState()
	if err != nil {
		t.Fatal(err)
	}
	st.Active["claude"] = "ghost"
	if err := state.Save(app.Paths.StateFile(), st); err != nil {
		t.Fatal(err)
	}
	l10ntest.UseJapanese(t)
	ctx := context.Background()
	_, out := captureStdout(t, func() int { return runDoctor(ctx, app, commonOpts{Format: formatText, NoColor: true}, "claude") })
	for _, want := range []string{
		"プラットフォーム: ",
		"シークレットストア: ",
		"[warn] claude: 状態ファイルは claude/ghost を有効なアカウントとして記録していますが、そのスナップショットはもう存在しません。",
		"kae use claude <account> を実行してください",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the doctor text lacks %q:\n%s", want, out)
		}
	}
	for _, leaked := range []string{"platform:", "secret backend:", "state records"} {
		if strings.Contains(out, leaked) {
			t.Errorf("the doctor text keeps the English %q:\n%s", leaked, out)
		}
	}
	if !strings.Contains(out, "先に進めない問題は見つかりませんでした") && !strings.Contains(out, "エラーが見つかりました。切り替える前に修正してください") {
		t.Errorf("the doctor summary is not Japanese:\n%s", out)
	}
}

// Each check message a command-layer producer builds renders in Japanese through
// the real catalog: the English wording is gone, the arguments are kept, and no
// verb is left unfilled.
func TestJapaneseCheckMessages(t *testing.T) {
	l10ntest.UseJapanese(t)
	cases := []struct {
		name string
		msg  l10n.Msg
		want []string // fragments of the Japanese text
	}{
		{
			"companion git unset", companionDriftMessage("main", "user.email", "you@example.com", "", 1),
			[]string{"プロファイル main: git の user.email がここでは未設定です", "\"you@example.com\"", "mise env を実行してください"},
		},
		{
			"companion git differs", companionDriftMessage("main", "user.email", "you@example.com", "other@example.com", 0),
			[]string{"git の user.email はここでは \"other@example.com\" です", "git config --show-origin user.email"},
		},
		{
			"token inactive", tokenDriftInactiveMessage("main", "gh", "GH_TOKEN", "side"),
			[]string{"gh はログイン \"side\" に固定されています", "GH_TOKEN が未設定です"},
		},
		{
			"token unverified", tokenDriftMismatchMessage("main", "gh", "side", "", 1, "boom"),
			[]string{"gh のトークンのログインを、固定したログイン \"side\" と照合して確認できませんでした（boom）"},
		},
		{
			"token mismatch", tokenDriftMismatchMessage("main", "gh", "side", "alt", 0, ""),
			[]string{"gh のトークンはログイン \"alt\" に解決されます", "期待するのは \"side\" です"},
		},
		{
			"identity untracked", identityUntrackedMessage("claude", "main", "oauth_account"),
			[]string{"アカウント main: oauth_account のログイン中アカウントの記録がまだ保存されていません", "claude を起動するのは"},
		},
		{
			"identity missing", identityDriftMessage("claude", "main", "oauth_account", false),
			[]string{"アカウント main: このアカウントが有効なのに、現在の oauth_account のログイン中アカウントの記録がありません", "kae use claude main を実行してください"},
		},
		{
			"identity differs", identityDriftMessage("claude", "main", "oauth_account", true),
			[]string{"現在の oauth_account のログイン中アカウントの記録が、kae が適用したものと異なります", "Upstream Behaviour Assumptions を参照"},
		},
		{
			"bound identity drift", pinIdentityDriftMessage(boundDirStore{Dir: "/work/main-app", Tool: "claude", Account: "main"}),
			[]string{"/work/main-app の claude のログイン中アカウントの記録が", "claude/main とは別のアカウントを指しています"},
		},
		{
			"superseded remedy", supersededRemedy("claude", "main", "/work/main-app", true),
			[]string{"新しいスナップショットからそのディレクトリを固定し直してください", "cd /work/main-app && kae pin claude main を実行してください"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text := l10n.Render(tc.msg)
			if text == tc.msg.Error() {
				t.Fatalf("the message renders in English: %q", text)
			}
			for _, want := range tc.want {
				if !strings.Contains(text, want) {
					t.Errorf("the Japanese text lacks %q:\n%s", want, text)
				}
			}
			if strings.Contains(text, "%!") {
				t.Errorf("an argument is left unfilled: %s", text)
			}
		})
	}

	check, ok := upstreamVersionCheck("claude", "2.9.0 (Claude Code)", "2.1.0")
	if !ok {
		t.Fatal("expected a version finding")
	}
	for _, want := range []string{"インストール済みの claude 2.9.0", "バージョン 2.1.0 を超えています"} {
		if text := l10n.Render(check.Message); !strings.Contains(text, want) {
			t.Errorf("the version check lacks %q:\n%s", want, text)
		}
	}
}

// The configuration and bound-directory index checks, rendered through the report
// a person reads.
func TestJapanesePinChecks(t *testing.T) {
	app := overlayTestApp(t)
	cwd := pinHere(t, app, modeShared)
	gone := filepath.Join(t.TempDir(), "moved-away")
	if err := app.recordPinnedDir("00112233445566778899aabbccddeeff", gone); err != nil {
		t.Fatal(err)
	}
	l10ntest.UseJapanese(t)
	var absent, dangling string
	for _, c := range app.pinChecks("") {
		text := l10n.Render(c.Message)
		if strings.Contains(c.Message.Error(), gone) {
			absent = text
		}
		if strings.Contains(c.Message.Error(), cwd) {
			dangling = text
		}
	}
	if !strings.Contains(absent, gone+" は kae pin で固定されましたが、記録されたパスがもう存在しません") {
		t.Errorf("the absent-path check is not Japanese: %q", absent)
	}
	if !strings.Contains(dangling, "claude/main に固定されていますが、そのアカウントは登録されていません") ||
		!strings.Contains(dangling, "kae pin claude <account> を実行してください") {
		t.Errorf("the dangling-account check is not Japanese: %q", dangling)
	}
}

func TestJapaneseConfigWarningsRenderAndStayEnglishInJSON(t *testing.T) {
	l10ntest.UseJapanese(t)
	msg := l10n.Msgf("[tools.%s] ignored: %s was removed; use %s instead", "gemini", "gemini", "antigravity")
	if got, want := l10n.Render(msg), "[tools.gemini] は無視しました。gemini は削除されました。代わりに antigravity を使ってください。"; got != want {
		t.Errorf("Render = %q, want %q", got, want)
	}
	check := adapter.Check{Code: constants.CheckConfigValid, Status: constants.StatusWarn, Message: msg}
	if got, want := check.Message.Error(), "[tools.gemini] ignored: gemini was removed; use antigravity instead"; got != want {
		t.Errorf("the English text = %q, want %q", got, want)
	}
}
