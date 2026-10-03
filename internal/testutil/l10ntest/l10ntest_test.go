package l10ntest

import (
	"os"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/l10n"
)

// The selection variables are set here, whatever the environment this test runs
// in, so a PinEnglish that left any of them or the selected language in place
// fails even where none was set to begin with.
func TestPinEnglishClearsEverySelectionVariable(t *testing.T) {
	values := map[string]string{
		"KAE_LANG": "ja", "LC_ALL": "ja_JP.UTF-8", "LC_MESSAGES": "ja_JP.UTF-8", "LANG": "ja_JP.UTF-8",
	}
	for _, name := range l10n.SelectionVars() {
		t.Setenv(name, values[name])
	}
	if len(values) != len(l10n.SelectionVars()) {
		t.Fatalf("this test sets %d variables, l10n selects on %v", len(values), l10n.SelectionVars())
	}
	l10n.Set(l10n.Japanese)
	t.Cleanup(func() { l10n.Set(l10n.English) })

	if err := PinEnglish(); err != nil {
		t.Fatal(err)
	}
	for name := range values {
		if value, set := os.LookupEnv(name); set {
			t.Errorf("%s is still set to %q", name, value)
		}
	}
	if l10n.Current() != l10n.English {
		t.Error("PinEnglish must select English")
	}
	if got := l10n.Detect(os.Getenv); got != l10n.English {
		t.Errorf("the environment PinEnglish leaves selects %v", got)
	}
}

func TestUseJapaneseRestoresEnglish(t *testing.T) {
	t.Run("japanese", func(t *testing.T) {
		UseJapanese(t)
		if l10n.Current() != l10n.Japanese || os.Getenv(l10n.EnvVar) != "ja" {
			t.Fatal("UseJapanese must select Japanese for the process and its children")
		}
	})
	if l10n.Current() != l10n.English {
		t.Fatal("UseJapanese must restore English when its test ends")
	}
}
