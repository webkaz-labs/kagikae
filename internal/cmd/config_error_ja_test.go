package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/config"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/testutil/l10ntest"
)

// A config validation error is a message value, so the `invalid config` line
// that embeds it renders wholly in Japanese, while the JSON report and the exit
// code stay as in English.
func TestInvalidConfigLineEmbedsTheLocalizedCause(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("default_profile = \"side\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, loadErr := config.Load(path)
	if loadErr == nil {
		t.Fatal("expected a validation error")
	}
	l10ntest.UseJapanese(t)
	err := errInvalidConfig(path, loadErr)

	code, stderr := captureStderr(t, func() int { return finish(commonOpts{Format: formatText}, err) })
	want := "kae: 設定ファイル " + path + " が不正です: default_profile に指定した \"side\" は [profiles] に定義されていません\n"
	if code != constants.ExitInvalidConfig || stderr != want {
		t.Fatalf("human line: exit %d\n got %q\nwant %q", code, stderr, want)
	}

	code, stdout := captureStdout(t, func() int { return finish(commonOpts{Format: formatJSON}, err) })
	var report errorReport
	if jerr := json.Unmarshal([]byte(stdout), &report); jerr != nil {
		t.Fatalf("JSON report: %v\n%s", jerr, stdout)
	}
	if code != constants.ExitInvalidConfig ||
		report.Message != "invalid config "+path+": default_profile \"side\" is not defined under [profiles]" {
		t.Fatalf("the JSON report must be English with the same code: exit %d, %+v", code, report)
	}
}
