package cmd

import (
	"testing"

	"github.com/webkaz-labs/kagikae/internal/l10n"
	"github.com/webkaz-labs/kagikae/internal/testutil/l10ntest"
)

// The adapter warnings carry their own closing stop in Japanese, so the run
// logged-out warning, which joins them and wraps them in parentheses, must not
// double it ("。。", "。）"). English bytes are fixed.
func TestLoggedOutDuringRunWarningAdapterWarnings(t *testing.T) {
	one := []message{msgf("%s is set and overrides the switched login", "CLAUDE_CODE_OAUTH_TOKEN")}
	two := append(one, msgf("%s is group/world readable; expected 0600", "~/.claude/.credentials.json"))
	cases := []struct {
		name     string
		warnings []message
		en, ja   string
	}{
		{
			"none", nil,
			"kae: warning: claude logged out during the run; snapshot claude/main left unchanged\n",
			"kae: warning: claude が実行中にログアウトしました。スナップショット claude/main は変更していません。\n",
		},
		{
			"one", one,
			"kae: warning: claude logged out during the run (CLAUDE_CODE_OAUTH_TOKEN is set and overrides the switched login); snapshot claude/main left unchanged\n",
			"kae: warning: claude が実行中にログアウトしました（CLAUDE_CODE_OAUTH_TOKEN が設定されているため、切り替えたログインより優先されます）。スナップショット claude/main は変更していません。\n",
		},
		{
			"two", two,
			"kae: warning: claude logged out during the run (CLAUDE_CODE_OAUTH_TOKEN is set and overrides the switched login; ~/.claude/.credentials.json is group/world readable; expected 0600); snapshot claude/main left unchanged\n",
			"kae: warning: claude が実行中にログアウトしました（CLAUDE_CODE_OAUTH_TOKEN が設定されているため、切り替えたログインより優先されます。~/.claude/.credentials.json がグループまたは全員に読み取れる権限になっています。0600 にしてください）。スナップショット claude/main は変更していません。\n",
		},
	}
	render := func(t *testing.T, w []message) string {
		_, out := captureStderr(t, func() int {
			warnLoggedOutDuringRunUnchanged("claude", "main", warningsDetail(w))
			return 0
		})
		return out
	}
	for _, tc := range cases {
		t.Run("english/"+tc.name, func(t *testing.T) {
			if got := render(t, tc.warnings); got != tc.en {
				t.Errorf("got %q\nwant %q", got, tc.en)
			}
		})
		t.Run("japanese/"+tc.name, func(t *testing.T) {
			l10ntest.UseJapanese(t)
			if got := render(t, tc.warnings); got != tc.ja {
				t.Errorf("got %q\nwant %q", got, tc.ja)
			}
		})
	}
	_ = l10n.Msg{}
}
