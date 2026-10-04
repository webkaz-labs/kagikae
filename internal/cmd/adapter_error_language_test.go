package cmd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/testutil/l10ntest"
)

// An adapter's refusal reaches `kae doctor` as a check message: the human report
// renders it in Japanese, the JSON report carries the English text, and the exit
// code is the same in both languages.
func TestDoctorRendersAnAdapterRefusalInTheSelectedLanguage(t *testing.T) {
	const (
		en = `unsupported: KAE_CLAUDE_DRIVER="bogus" is invalid (only "file" is supported)`
		ja = `対応していません: KAE_CLAUDE_DRIVER="bogus" は不正な値です（対応しているのは "file" だけです）`
	)
	ctx := context.Background()
	doctor := func(t *testing.T, format string) (int, string) {
		app := testApp(t, map[string]string{constants.EnvKaeClaudeDriver: "bogus"})
		code, stdout, _ := captureBoth(t, func() int { return runDoctor(ctx, app, commonOpts{Format: format}, "claude") })
		return code, stdout
	}

	englishCode, stdout := doctor(t, formatText)
	if !strings.Contains(stdout, en) {
		t.Errorf("English report lacks %q:\n%s", en, stdout)
	}

	l10ntest.UseJapanese(t)
	code, stdout := doctor(t, formatText)
	if !strings.Contains(stdout, ja) || strings.Contains(stdout, en) {
		t.Errorf("Japanese report must show %q and not the English:\n%s", ja, stdout)
	}
	if code != englishCode {
		t.Errorf("exit %d in Japanese, %d in English", code, englishCode)
	}

	code, stdout = doctor(t, formatJSON)
	var report struct {
		Checks []struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"checks"`
	}
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("doctor JSON: %v\n%s", err, stdout)
	}
	found := false
	for _, c := range report.Checks {
		if c.Code == constants.CheckUnsupported && c.Message == en {
			found = true
		}
	}
	if !found {
		t.Errorf("JSON report lacks the English unsupported check %q:\n%s", en, stdout)
	}
	if code != englishCode {
		t.Errorf("JSON exit %d, text exit %d", code, englishCode)
	}
}
