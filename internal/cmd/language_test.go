package cmd

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/l10n"
	"github.com/webkaz-labs/kagikae/internal/lock"
	"github.com/webkaz-labs/kagikae/internal/testutil/l10ntest"
)

// The tests here select Japanese explicitly (l10ntest.UseJapanese), which is
// process-wide: none of them may call t.Parallel.

func TestJSONModeArgsFollowsTheCLIList(t *testing.T) {
	cases := []struct {
		args []string
		want bool
	}{
		{[]string{"ls", "--json"}, true},
		{[]string{"ls", "-json"}, true},
		{[]string{"ls", "--json=true"}, true},
		{[]string{"ls", "-json=1"}, true},
		{[]string{"ls", "--json=false"}, false},
		{[]string{"ls", "--format", "json"}, true},
		{[]string{"ls", "-format", "json"}, true},
		{[]string{"ls", "--format=json"}, true},
		{[]string{"ls", "--format", "text"}, false},
		{[]string{"--json"}, true},               // bare kae status
		{[]string{"main", "ls", "--json"}, true}, // flags may follow positionals
		{[]string{"ls"}, false},
		{nil, false},
		// The value of another flag does not count.
		{[]string{"rollback", "--to", "--json"}, false},
		{[]string{"ls", "--config", "--json"}, false},
		{[]string{"ls", "--config=--json"}, false},
		{[]string{"use", "-P", "--json"}, false},
		{[]string{"ls", "--format", "--json"}, false},
		// A child command after -- does not count.
		{[]string{"run", "claude", "main", "--", "claude", "--json"}, false},
		{[]string{"run", "claude", "main", "--json", "--", "claude"}, true},
		{[]string{"ls", "---json"}, false},
		{[]string{"ls", "json"}, false},
	}
	for _, tc := range cases {
		if got := jsonModeArgs(tc.args); got != tc.want {
			t.Errorf("jsonModeArgs(%q) = %v, want %v", tc.args, got, tc.want)
		}
	}
}

func TestCmdErrorCausesAndExitCodeDoNotDependOnLanguage(t *testing.T) {
	other := errors.New("disk on fire")
	single := errf(constants.ExitNotFound, "account %s/%s not found: %w", "claude", "main", lock.ErrBusy)
	double := errf(constants.ExitPermission, "cannot write %s: %w (and %w)", "x", other, lock.ErrBusy)
	plain := errf(constants.ExitUsage, "no cause %d", 3)

	check := func(t *testing.T) {
		t.Helper()
		// exitOf takes the outermost cmdError's code, never a wrapped cause's.
		if got := exitOf(single); got != constants.ExitNotFound {
			t.Errorf("exitOf(single) = %d", got)
		}
		if got := exitOf(double); got != constants.ExitPermission {
			t.Errorf("exitOf(double) = %d", got)
		}
		if !errors.Is(single, lock.ErrBusy) || !errors.Is(double, lock.ErrBusy) || !errors.Is(double, other) {
			t.Error("errors.Is must reach every %w cause")
		}
		var ce *cmdError
		if !errors.As(single, &ce) || ce != single {
			t.Error("errors.As must find the cmdError itself")
		}
		if errors.Unwrap(plain) != nil || len(plain.Unwrap()) != 0 {
			t.Error("a format without %w wraps nothing")
		}
		if got := single.Error(); got != "account claude/main not found: "+lock.ErrBusy.Error() {
			t.Errorf("Error() = %q", got)
		}
		if got := double.Error(); got != "cannot write x: disk on fire (and "+lock.ErrBusy.Error()+")" {
			t.Errorf("Error() = %q", got)
		}
	}
	t.Run("English", check)
	t.Run("Japanese", func(t *testing.T) {
		l10ntest.UseJapanese(t)
		restore := l10n.UseCatalogForTest(map[string]string{
			"account %s/%s not found: %w":  "アカウント %s/%s が見つかりません: %w",
			"cannot write %s: %w (and %w)": "%s に書き込めません: %w（ほかに %w）",
		})
		t.Cleanup(restore)
		check(t)
		// Only the human rendering changes.
		if got := l10n.Render(single); got != "アカウント claude/main が見つかりません: "+lock.ErrBusy.Error() {
			t.Errorf("Render(single) = %q", got)
		}
	})
}

func TestFinishLocalizesOnlyTheHumanLine(t *testing.T) {
	l10ntest.UseJapanese(t)
	t.Cleanup(l10n.UseCatalogForTest(map[string]string{
		"account %s/%s not found": "アカウント %s/%s が見つかりません",
	}))
	err := errf(constants.ExitNotFound, "account %s/%s not found", "claude", "main")

	code, stderr := captureStderr(t, func() int { return finish(commonOpts{Format: formatText}, err) })
	if code != constants.ExitNotFound || stderr != "kae: アカウント claude/main が見つかりません\n" {
		t.Fatalf("human finish: exit %d, %q", code, stderr)
	}

	code, stdout := captureStdout(t, func() int { return finish(commonOpts{Format: formatJSON}, err) })
	var report errorReport
	if jerr := json.Unmarshal([]byte(stdout), &report); jerr != nil {
		t.Fatalf("JSON finish: %v\n%s", jerr, stdout)
	}
	if code != constants.ExitNotFound || report.Message != "account claude/main not found" ||
		report.ErrorCode != constants.ErrorCode(constants.ExitNotFound) {
		t.Fatalf("JSON finish must be English with the same code: exit %d, %+v", code, report)
	}

	// A message the catalog lacks falls back to English, prefix unchanged.
	miss := errf(constants.ExitError, "not in the catalog %s", "x")
	_, stderr = captureStderr(t, func() int { return finish(commonOpts{Format: formatText}, miss) })
	if stderr != "kae: not in the catalog x\n" {
		t.Fatalf("catalog miss: %q", stderr)
	}
	// An error that is not a message value renders verbatim.
	_, stderr = captureStderr(t, func() int { return finish(commonOpts{Format: formatText}, os.ErrPermission) })
	if stderr != "kae: "+os.ErrPermission.Error()+"\n" {
		t.Fatalf("external error: %q", stderr)
	}
}

func TestUsageErrorsAreEnglishInJSONMode(t *testing.T) {
	const format = "unknown command: %s (see kae help)%s"
	l10ntest.UseJapanese(t)
	t.Cleanup(l10n.UseCatalogForTest(map[string]string{format: "不明なコマンドです: %s（kae help を参照）%s"}))

	code, stderr := captureStderr(t, func() int { return Root([]string{"zzzzzz"}) })
	if code != constants.ExitUsage || stderr != "不明なコマンドです: zzzzzz（kae help を参照）\n" {
		t.Fatalf("human usage error: exit %d, %q", code, stderr)
	}
	for _, args := range [][]string{
		{"zzzzzz", "--json"},
		{"zzzzzz", "--format=json"},
		{"zzzzzz", "-format", "json"},
	} {
		code, stderr := captureStderr(t, func() int { return Root(args) })
		if code != constants.ExitUsage || stderr != "unknown command: zzzzzz (see kae help)\n" {
			t.Errorf("Root(%q) must stay English: exit %d, %q", args, code, stderr)
		}
	}
}

func TestEnglishPinHoldsForTheProcess(t *testing.T) {
	for _, name := range l10n.SelectionVars() {
		if value, set := os.LookupEnv(name); set {
			t.Errorf("TestMain must clear %s, found %q", name, value)
		}
	}
	code, stderr := captureStderr(t, func() int { return Root([]string{"zzzzzz"}) })
	if code != constants.ExitUsage || !strings.HasPrefix(stderr, "unknown command: zzzzzz") {
		t.Fatalf("exit %d, %q", code, stderr)
	}
	if l10n.Current() != l10n.English {
		t.Fatal("Root under the pin must select English")
	}
}
