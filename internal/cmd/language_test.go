package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/companion"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/installation"
	"github.com/webkaz-labs/kagikae/internal/integration"
	"github.com/webkaz-labs/kagikae/internal/l10n"
	"github.com/webkaz-labs/kagikae/internal/lock"
	"github.com/webkaz-labs/kagikae/internal/secret"
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
		{[]string{"--json"}, true},                          // bare kae status
		{[]string{"use", "claude", "main", "--json"}, true}, // flags may follow positionals
		{[]string{"zzz", "--json"}, true},                   // an unknown command still has the common flags
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

// The sentinels exitOf maps are message values: the exit code and the JSON text
// do not depend on the language, and the human line renders the catalog's
// Japanese, alone or as the head of a message that wraps it.
func TestSentinelsKeepTheirExitCodeAndLocalizeTheHumanLine(t *testing.T) {
	sentinels := []struct {
		err  error
		exit int
	}{
		{artifact.ErrUnsafe, constants.ExitUnsafeRefused},
		{installation.ErrUnsafe, constants.ExitUnsafeRefused},
		{integration.ErrUnsafe, constants.ExitUnsafeRefused},
		{integration.ErrChanged, constants.ExitUnsafeRefused},
		{adapter.ErrUnsupported, constants.ExitUnsupported},
		{secret.ErrUnavailable, constants.ExitSecretStore},
		{lock.ErrBusy, constants.ExitLockBusy},
	}
	l10ntest.UseJapanese(t)
	for _, s := range sentinels {
		english := s.err.Error()
		for _, err := range []error{s.err, fmt.Errorf("op: %w", s.err), l10n.Errorf("%w: detail", s.err)} {
			if got := exitOf(err); got != s.exit {
				t.Errorf("exitOf(%q) = %d, want %d", err, got, s.exit)
			}
			code, stdout := captureStdout(t, func() int { return finish(commonOpts{Format: formatJSON}, err) })
			var report errorReport
			if jerr := json.Unmarshal([]byte(stdout), &report); jerr != nil || code != s.exit || report.Message != err.Error() {
				t.Errorf("JSON finish of %q: exit %d, %+v (%v)", err, code, report, jerr)
			}
		}
		code, stderr := captureStderr(t, func() int { return finish(commonOpts{Format: formatText}, s.err) })
		if code != s.exit || strings.Contains(stderr, english) || !strings.HasPrefix(stderr, "kae: ") {
			t.Errorf("human finish of %q must render the catalog's Japanese: exit %d, %q", english, code, stderr)
		}
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

// stageOneCase is one failure from localization stage 1 (docs/ROADMAP.md): the
// `kae:` line a failing command ends on, a usage error, a did-you-mean suffix or a
// common error, with its English and Japanese renderings.
type stageOneCase struct {
	name   string
	run    func(t *testing.T) (int, string)
	en, ja string
}

// stageOneCases builds the cases on fresh Apps, so the English and the Japanese
// pass each start from the same state.
func stageOneCases(t *testing.T) []stageOneCase {
	ctx := context.Background()
	text := commonOpts{Format: formatText}
	tools := strings.Join(constants.Tools, ", ")
	uncaptured, noConfig, busy := testApp(t, nil), testApp(t, nil), testApp(t, nil)
	broken := testApp(t, nil)
	broken.ConfigErr = errors.New("boom")
	return []stageOneCase{
		{
			name: "unknown command",
			run: func(t *testing.T) (int, string) {
				return captureStderr(t, func() int { return Root([]string{"zzzzzz"}) })
			},
			en: "unknown command: zzzzzz (see kae help)\n",
			ja: "不明なコマンドです: zzzzzz（kae help を参照してください）\n",
		},
		{
			name: "unknown command with a suggestion",
			run: func(t *testing.T) (int, string) {
				return captureStderr(t, func() int { return Root([]string{"statu"}) })
			},
			en: "unknown command: statu (see kae help) — did you mean \"status\"?\n",
			ja: "不明なコマンドです: statu（kae help を参照してください）。もしかして: \"status\"\n",
		},
		{
			name: "removed command",
			run: func(t *testing.T) (int, string) {
				return captureStderr(t, func() int { return Root([]string{"login"}) })
			},
			en: "kae login was removed in v0.5.0; run: kae add <tool> <account>\n",
			ja: "kae login は v0.5.0 で削除されました。kae add <tool> <account> を実行してください\n",
		},
		{
			// The replacement's explanation is part of the format, not an English argument.
			name: "removed command with an explained replacement",
			run: func(t *testing.T) (int, string) {
				return captureStderr(t, func() int { return Root([]string{"apply"}) })
			},
			en: "kae apply was removed in v0.8.0; run: kae use [--quiet] (bare use resolves the profile)\n",
			ja: "kae apply は v0.8.0 で削除されました。kae use [--quiet] を実行してください（引数なしの kae use がプロファイルを解決します）\n",
		},
		{
			name: "usage error",
			run: func(t *testing.T) (int, string) {
				return captureStderr(t, func() int {
					if _, ok := parseCommon("status", []string{"--format", "yaml"}, false, nil); ok {
						return constants.ExitOK
					}
					return constants.ExitUsage
				})
			},
			en: "unsupported format: yaml\n",
			ja: "対応していない出力形式です: yaml\n",
		},
		{
			name: "unknown tool with a suggestion",
			run: func(t *testing.T) (int, string) {
				return captureStderr(t, func() int { return runSwitch(ctx, testApp(t, nil), text, "cluade", "main") })
			},
			en: "kae: unknown tool \"cluade\" (tools: " + tools + ") — did you mean \"claude\"?\n",
			ja: "kae: 不明なツールです: \"cluade\"（ツール: " + strings.Join(constants.Tools, "、") + "）。もしかして: \"claude\"\n",
		},
		{
			name: "account not captured",
			run: func(t *testing.T) (int, string) {
				return captureStderr(t, func() int { return runSwitch(ctx, uncaptured, text, "claude", "side") })
			},
			en: "kae: account claude/side is not captured; first verify the live claude login belongs to account side " +
				"and uses the intended global store; only then, to re-capture, run: kae add --no-login claude side; " +
				"if logged out or uncertain, see docs/CLI.md Recovery guidance before capture\n",
			ja: "kae: アカウント claude/side は登録されていません。まず claude の現在のログインがアカウント side のもので、" +
				"意図したグローバルの認証ストアを使っていることを確認してください。確認できた場合に限り、登録し直すには " +
				"kae add --no-login claude side を実行してください。ログアウトしている場合や確信が持てない場合は、" +
				"登録する前に docs/CLI.md の Recovery guidance を参照してください\n",
		},
		{
			name: "config not found",
			run: func(t *testing.T) (int, string) {
				return captureStderr(t, func() int { return finish(text, noConfig.requireConfigFile()) })
			},
			en: "kae: config ~/.config/kagikae/config.toml not found; run: kae init\n",
			ja: "kae: 設定ファイル ~/.config/kagikae/config.toml が見つかりません。kae init を実行してください\n",
		},
		{
			// kae ls reports a group's error itself rather than through finish.
			name: "ls with a broken config",
			run: func(t *testing.T) (int, string) {
				chdirTo(t, t.TempDir())
				code, _, stderr := captureBoth(t, func() int { return runLs(ctx, broken, text) })
				return code, stderr
			},
			en: "kae: invalid config " + broken.ConfigPath + ": boom\n",
			ja: "kae: 設定ファイル " + broken.ConfigPath + " が不正です: boom\n",
		},
		{
			name: "companion knob",
			run: func(t *testing.T) (int, string) {
				return captureStderr(t, func() int {
					_, _, err := parseCompanionKnobs(companion.Spec{}, nil, strings.NewReader(""))
					return finish(text, err)
				})
			},
			en: "kae: no knobs given\n",
			ja: "kae: 設定項目が指定されていません\n",
		},
		{
			name: "lock busy",
			run: func(t *testing.T) (int, string) {
				held, err := lock.Acquire(busy.Paths.LocksDir(), lockNameConfig)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Release()
				return captureStderr(t, func() int {
					_, err := busy.acquireConfigLock()
					return finish(text, err)
				})
			},
			en: "kae: another kae process is editing the config; retry shortly\n",
			ja: "kae: 別の kae プロセスが設定ファイルを編集しています。しばらくしてから再試行してください\n",
		},
	}
}

func TestStageOneErrorsRenderInTheSelectedLanguage(t *testing.T) {
	english := map[string]int{}
	for _, c := range stageOneCases(t) {
		code, stderr := c.run(t)
		if stderr != c.en {
			t.Errorf("%s in English:\n got %q\nwant %q", c.name, stderr, c.en)
		}
		english[c.name] = code
	}
	t.Run("Japanese", func(t *testing.T) {
		l10ntest.UseJapanese(t)
		for _, c := range stageOneCases(t) {
			code, stderr := c.run(t)
			if stderr != c.ja {
				t.Errorf("%s in Japanese:\n got %q\nwant %q", c.name, stderr, c.ja)
			}
			if code != english[c.name] {
				t.Errorf("%s: exit %d in Japanese, %d in English", c.name, code, english[c.name])
			}
		}
	})
}

func TestStageOneErrorsStayEnglishUnderJSONAndKaeLangEn(t *testing.T) {
	const suggestion = "unknown command: statu (see kae help) — did you mean \"status\"?\n"
	l10ntest.UseJapanese(t)

	// --json: the usage error on stderr and the JSON message stay English.
	code, stderr := captureStderr(t, func() int { return Root([]string{"statu", "--json"}) })
	if code != constants.ExitUsage || stderr != suggestion {
		t.Errorf("JSON-mode usage error: exit %d, %q", code, stderr)
	}
	// Root selected English for its JSON-mode command line; select Japanese again
	// so the JSON report below is English because of JSON mode, not the language.
	l10n.Set(l10n.Japanese)
	app := testApp(t, nil)
	code, stdout := captureStdout(t, func() int {
		return runSwitch(context.Background(), app, commonOpts{Format: formatJSON}, "claude", "side")
	})
	var report errorReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("JSON error report: %v\n%s", err, stdout)
	}
	if code != constants.ExitNotFound || !strings.HasPrefix(report.Message, "account claude/side is not captured; first verify") {
		t.Errorf("the JSON error report must be English: exit %d, %q", code, report.Message)
	}

	// KAE_LANG=en outranks the Japanese the test selected.
	t.Setenv(l10n.EnvVar, "en")
	code, stderr = captureStderr(t, func() int { return Root([]string{"statu"}) })
	if code != constants.ExitUsage || stderr != suggestion {
		t.Errorf("KAE_LANG=en: exit %d, %q", code, stderr)
	}
}

func TestDidYouMeanWithoutACandidateRendersNothing(t *testing.T) {
	none := didYouMean("zzzzzz", []string{"status"})
	if none.Error() != "" || l10n.Render(none) != "" {
		t.Fatalf("English: %q, %q", none.Error(), l10n.Render(none))
	}
	l10ntest.UseJapanese(t)
	if got := l10n.Render(none); got != "" {
		t.Fatalf("Japanese: %q", got)
	}
}
