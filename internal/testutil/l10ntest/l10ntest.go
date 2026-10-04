// Package l10ntest pins the language of kae's human output in tests
// (docs/VALIDATION.md § Output language in tests). Assertions on human text assert
// the English rendering, so each test entrypoint that renders human text calls
// PinEnglish from its TestMain; a test that renders Japanese selects it explicitly
// with UseJapanese.
package l10ntest

import (
	"fmt"
	"os"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/l10n"
)

// PinEnglish clears every variable that selects the language, so neither
// KAE_LANG nor the developer's locale reaches an English assertion, and selects
// English. Call it from TestMain before m.Run. Child processes a test starts
// inherit the cleared environment.
func PinEnglish() error {
	for _, name := range l10n.SelectionVars() {
		if err := os.Unsetenv(name); err != nil {
			return fmt.Errorf("clear %s: %w", name, err)
		}
	}
	l10n.Set(l10n.English)
	return nil
}

// UseJapanese selects Japanese for the rest of t, both for code that renders
// directly and for a command line that selects the language again (KAE_LANG=ja,
// which a child process inherits too), and restores the English pin when t ends.
// It is process-wide: t.Setenv refuses a parallel test, and a test calling this
// must not call t.Parallel.
func UseJapanese(t *testing.T) {
	t.Helper()
	t.Setenv(l10n.EnvVar, "ja")
	l10n.Set(l10n.Japanese)
	t.Cleanup(func() { l10n.Set(l10n.English) })
}

// English returns the English text of each message, which is what JSON carries,
// so an assertion on a list of warnings reads strings whatever language is selected.
func English(messages []l10n.Msg) []string {
	texts := make([]string, len(messages))
	for i, m := range messages {
		texts[i] = m.Error()
	}
	return texts
}
