package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/l10n"
	"github.com/webkaz-labs/kagikae/internal/state"
	"github.com/webkaz-labs/kagikae/internal/testutil/l10ntest"
)

// Nothing a JSON-mode process writes is localized (docs/CLI.md § Localization),
// and the process language already pins English there. These runs go further and
// select Japanese in the process while rendering --json, so a message value that
// reached JSON through its localized text instead of its English one, or through
// an encoder that escapes `<` and `&`, changes the bytes here. The doctor case
// carries a `<account>` placeholder in a check message.
func TestJSONBytesDoNotDependOnTheLanguage(t *testing.T) {
	chdirTemp(t)
	app := testApp(t, nil)
	seedIdentityAccount(t, app)
	captureClaude(t, app, "side", sideToken)
	// An active account whose snapshot is gone makes doctor print its remedy,
	// "run: kae use claude <account>".
	st, err := app.loadState()
	if err != nil {
		t.Fatal(err)
	}
	st.Active["claude"] = "ghost"
	if err := state.Save(app.Paths.StateFile(), st); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	json := commonOpts{Format: formatJSON}
	cases := []struct {
		name string
		run  func() int
		want string // a fragment that proves the case exercises the escaped characters
	}{
		{"doctor", func() int { return runDoctor(ctx, app, json, "claude") }, "kae use claude <account>"},
		{"status", func() int { return runStatus(ctx, app, json) }, `"warnings"`},
		{"use", func() int {
			return runSwitch(ctx, app, commonOpts{Format: formatJSON, DryRun: true}, "claude", "side")
		}, `"warnings"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, english := captureStdout(t, tc.run)
			if !strings.Contains(english, tc.want) {
				t.Fatalf("the English output lacks %q, so this case does not guard it:\n%s", tc.want, english)
			}
			t.Run("japanese", func(t *testing.T) {
				l10ntest.UseJapanese(t)
				code, japanese := captureStdout(t, tc.run)
				if code != constants.ExitOK && code != constants.ExitError {
					t.Fatalf("exit %d: %s", code, japanese)
				}
				if japanese != english {
					t.Errorf("--json differs under Japanese:\n%s\nvs English:\n%s", japanese, english)
				}
			})
		})
	}
}

// A check message is a value: doctor's text renders it in the selected language
// and its --json keeps the English text of the same value.
func TestDoctorCheckMessageLocalizesInTextOnly(t *testing.T) {
	const english = "state records %s/%s as active but that snapshot no longer exists, so kae cannot say which %s account is live; " +
		"to pick one, run: kae use %s <account> (kae ls shows the captured ones)"
	restore := l10n.UseCatalogForTest(map[string]string{english: "状態は %[1]s/%[2]s を有効としていますが、そのスナップショットがありません。%[3]s %[4]s"})
	defer restore()
	app := testApp(t, nil)
	st, err := app.loadState()
	if err != nil {
		t.Fatal(err)
	}
	st.Active["claude"] = "ghost"
	if err := state.Save(app.Paths.StateFile(), st); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	l10ntest.UseJapanese(t)

	_, text := captureStdout(t, func() int { return runDoctor(ctx, app, commonOpts{Format: formatText}, "claude") })
	if !strings.Contains(text, "状態は claude/ghost を有効としていますが") {
		t.Errorf("the text report must render the check message in Japanese:\n%s", text)
	}
	_, out := captureStdout(t, func() int { return runDoctor(ctx, app, commonOpts{Format: formatJSON}, "claude") })
	if !strings.Contains(out, "state records claude/ghost as active but that snapshot no longer exists") ||
		strings.Contains(out, "状態は") {
		t.Errorf("--json must carry the English text:\n%s", out)
	}
}
