package cmd

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/l10n"
	"github.com/webkaz-labs/kagikae/internal/testutil/l10ntest"
)

// The columns an account table shows are chosen by column id, not by the words
// that head them, so a translated header does not change which columns --full adds.
// The test catalog holds stand-in words; the real terms are the catalog's.
func TestAccountTableColumnsAreChosenByIDUnderJapanese(t *testing.T) {
	chdirTemp(t)
	withTerminalColumns(t, 0)
	app := testApp(t, nil)
	seedIdentityAccount(t, app)
	restore := l10n.UseCatalogForTest(map[string]string{
		"Tool": "ツール", "Account": "アカウント", "Identity": "識別子", "Active": "使用中",
		"Driver": "ドライバー", "Auth": "認証", "Credential": "認証情報", "Limit": "利用枠",
		"Notes": "備考", "Captured": "登録日時",
	})
	defer restore()
	l10ntest.UseJapanese(t)

	want := map[string][2]int{ // default and --full column counts
		"status": {6, 8}, "accounts": {6, 8}, "ls": {5, 7}, "ls claude": {5, 7},
	}
	for name, run := range accountTableRunners(context.Background(), app) {
		var header string
		count := func(opts commonOpts) (int, string) {
			code, out := captureStdout(t, func() int { return run(opts) })
			mustExit(t, constants.ExitOK, code, out)
			for _, line := range strings.Split(out, "\n") {
				if strings.HasPrefix(line, "ツール ") {
					header = line
					return len(strings.Fields(line)), out
				}
			}
			t.Fatalf("%s: no Japanese header in:\n%s", name, out)
			return 0, ""
		}
		got, out := count(commonOpts{Format: formatText})
		if got != want[name][0] || strings.Contains(out, "識別子") || strings.Contains(out, "ドライバー") || strings.Contains(out, "main-uuid@example.com") {
			t.Errorf("%s default has %d columns, want %d without identity and driver:\n%s", name, got, want[name][0], out)
		}
		got, out = count(commonOpts{Format: formatText, Full: true})
		if got != want[name][1] || !strings.Contains(out, "識別子") || !strings.Contains(out, "main-uuid@example.com") {
			t.Errorf("%s --full has %d columns, want %d with identity and driver:\n%s", name, got, want[name][1], out)
		}
		if strings.Contains(header, "Credential") || strings.Contains(header, "Account") {
			t.Errorf("%s left an English header under Japanese: %s", name, header)
		}
	}
}

// The lead time words are a constant format per unit, and the English text is
// what the report carried before.
func TestLeadTimeWords(t *testing.T) {
	cases := []struct {
		d          time.Duration
		msg, ltEnd string
	}{
		{49*time.Hour + 30*time.Minute, "2 day(s)", "2 day(s) left"},
		{23*time.Hour + 59*time.Minute, "23 hour(s)", "23 hour(s) left"},
		{time.Hour, "1 hour(s)", "1 hour(s) left"},
		{59 * time.Minute, "under an hour", "under an hour left"},
		{-time.Minute, "under an hour", "under an hour left"},
	}
	for _, tc := range cases {
		if got := leadTimeMessage(tc.d).Error(); got != tc.msg {
			t.Errorf("leadTimeMessage(%v) = %q, want %q", tc.d, got, tc.msg)
		}
		if got := leadTimeLeft(tc.d); got != tc.ltEnd {
			t.Errorf("leadTimeLeft(%v) = %q, want %q", tc.d, got, tc.ltEnd)
		}
	}
}
