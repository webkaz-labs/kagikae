package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/constants"
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
