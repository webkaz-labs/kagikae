package cmd

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/account"
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
//
// The Japanese run is only a guard if Japanese text exists to leak, so the test
// puts a marked translation of one doctor check message and one `use` warning in
// the catalog and proves the marker renders for a person before it compares bytes.
func TestJSONBytesDoNotDependOnTheLanguage(t *testing.T) {
	chdirTemp(t)
	app := testApp(t, nil)
	seedIdentityAccount(t, app)
	// A snapshot with two days left makes `use` warn that it needs a re-login.
	seedClaudeOAuth(t, app, endOfLifeClaudeCred(app.Now(), 48*time.Hour, sideToken))
	if code, out := captureStdout(t, func() int {
		return runCapture(context.Background(), app, commonOpts{Format: formatText}, constants.ToolClaude, "side")
	}); code != constants.ExitOK {
		t.Fatalf("capture: %s", out)
	}
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
	const marker = "日本語の訳: "
	translate := map[string]string{}
	var doctorMessage l10n.Msg
	for _, check := range buildDoctor(ctx, app, "claude", doctorOptIns{}).Checks {
		if check.Code == constants.CheckActiveOrphan {
			doctorMessage = check.Message
		}
	}
	be, err := app.secretBackend()
	if err != nil {
		t.Fatal(err)
	}
	side, found, err := account.Load(app.Paths.AccountDir(constants.ToolClaude, "side"))
	if err != nil || !found {
		t.Fatalf("claude/side: found %v, err %v", found, err)
	}
	useWarning, _, err := app.snapshotFreshnessWarning(ctx, be, side)
	if err != nil {
		t.Fatal(err)
	}
	for name, m := range map[string]l10n.Msg{"doctor check message": doctorMessage, "use warning": useWarning} {
		if m.Empty() {
			t.Fatalf("the fixture produced no %s", name)
		}
		format, _ := m.MessageFormat()
		translate[format] = marker + format
	}
	restore := l10n.UseCatalogForTest(translate)
	defer restore()

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
		}, "needs an interactive re-login"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, english := captureStdout(t, tc.run)
			if !strings.Contains(english, tc.want) {
				t.Fatalf("the English output lacks %q, so this case does not guard it:\n%s", tc.want, english)
			}
			t.Run("japanese", func(t *testing.T) {
				l10ntest.UseJapanese(t)
				if got := l10n.Render(doctorMessage); !strings.HasPrefix(got, marker) {
					t.Fatalf("the injected translation does not render, so the byte comparison guards nothing: %q", got)
				}
				if got := l10n.Render(useWarning); !strings.HasPrefix(got, marker) {
					t.Fatalf("the injected translation does not render, so the byte comparison guards nothing: %q", got)
				}
				code, japanese := captureStdout(t, tc.run)
				if code != constants.ExitOK && code != constants.ExitError {
					t.Fatalf("exit %d: %s", code, japanese)
				}
				if japanese != english {
					t.Errorf("--json differs under Japanese:\n%s\nvs English:\n%s", japanese, english)
				}
				if strings.Contains(japanese, marker) {
					t.Errorf("--json carries the Japanese translation:\n%s", japanese)
				}
			})
		})
	}
}
