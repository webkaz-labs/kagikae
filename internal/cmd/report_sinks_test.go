package cmd

import (
	"testing"

	"github.com/webkaz-labs/kagikae/internal/l10n"
	"github.com/webkaz-labs/kagikae/internal/testutil/l10ntest"
)

// The stdout and prompt sinks translate inside, like the stderr ones, and differ
// in stream and newline only.
func TestReportAndPromptSinksLocalize(t *testing.T) {
	const format = "Global active profile: %s"
	restore := l10n.UseCatalogForTest(map[string]string{
		format:      "有効なプロファイル: %s",
		"Proceed? ": "実行しますか? ",
	})
	defer restore()

	t.Run("english", func(t *testing.T) {
		_, out := captureStdout(t, func() int { reportf(format, "main"); return 0 })
		if want := "Global active profile: main\n"; out != want {
			t.Errorf("reportf = %q, want %q", out, want)
		}
		_, out = captureStdout(t, func() int { reportMessage(msgf(format, "main")); return 0 })
		if want := "Global active profile: main\n"; out != want {
			t.Errorf("reportMessage = %q, want %q", out, want)
		}
		_, errOut := captureStderr(t, func() int { promptf("Proceed? "); return 0 })
		if want := "Proceed? "; errOut != want {
			t.Errorf("promptf = %q, want %q (no newline)", errOut, want)
		}
	})
	t.Run("japanese", func(t *testing.T) {
		l10ntest.UseJapanese(t)
		_, out := captureStdout(t, func() int { reportf(format, "main"); return 0 })
		if want := "有効なプロファイル: main\n"; out != want {
			t.Errorf("reportf = %q, want %q", out, want)
		}
		_, out = captureStdout(t, func() int { reportMessage(msgf(format, "main")); return 0 })
		if want := "有効なプロファイル: main\n"; out != want {
			t.Errorf("reportMessage = %q, want %q", out, want)
		}
		_, errOut := captureStderr(t, func() int { promptf("Proceed? "); return 0 })
		if want := "実行しますか? "; errOut != want {
			t.Errorf("promptf = %q, want %q", errOut, want)
		}
	})
}
