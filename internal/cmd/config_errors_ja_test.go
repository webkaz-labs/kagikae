package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/companion"
	"github.com/webkaz-labs/kagikae/internal/config"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/l10n"
	"github.com/webkaz-labs/kagikae/internal/secret"
	"github.com/webkaz-labs/kagikae/internal/state"
	"github.com/webkaz-labs/kagikae/internal/testutil/l10ntest"
)

// The errors the config, account, env and companion commands wrap around a cause
// are message values: the English text, the cause reachable through errors.Is and
// the exit code exitOf derives from it are the same in both languages, and only
// the human line is Japanese. These tests select Japanese explicitly, so none of
// them may call t.Parallel.

type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

// wrapCase is one wrapping site reached through its own code path.
type wrapCase struct {
	name  string
	build func(t *testing.T) error
	cause error // what errors.Is must still reach
	exit  int
	en    string // Error(), which JSON carries
	ja    string // Render under Japanese
}

func wrapCases() []wrapCase {
	stdinErr := errors.New("stdin closed")
	fragmentErr := errors.New("disk full")
	return []wrapCase{
		{
			name: "env stdin read",
			build: func(*testing.T) error {
				_, err := readStdinSecret(failingReader{stdinErr}, "TOKEN")
				return err
			},
			cause: stdinErr,
			exit:  constants.ExitError,
			en:    "read value from stdin: stdin closed",
			ja:    "標準入力から値を読み取れません: stdin closed",
		},
		{
			// The exit code a sentinel cause gives survives the wrap.
			name: "env stdin read, secret store cause",
			build: func(*testing.T) error {
				_, err := readStdinSecret(failingReader{secret.ErrUnavailable}, "TOKEN")
				return err
			},
			cause: secret.ErrUnavailable,
			exit:  constants.ExitSecretStore,
			en:    "read value from stdin: secret store unavailable",
			// The sentinel is itself a message value, so it renders Japanese too.
			ja: "標準入力から値を読み取れません: シークレットストアを使えません",
		},
		{
			name: "config edit with no config file",
			build: func(t *testing.T) error {
				app := testApp(t, nil)
				return app.editConfig(func(*config.Editor) {})
			},
			cause: fs.ErrNotExist,
			exit:  constants.ExitError,
			en:    "read config for edit: open CONFIG: no such file or directory",
			ja:    "編集する設定ファイルを読み取れません: open CONFIG: no such file or directory",
		},
		{
			name: "global fragment regeneration with a failed restore",
			build: func(*testing.T) error {
				saves := 0
				return saveSyncedStateAndFragment(state.New(), state.New(), func(*state.State) error {
					saves++
					if saves == 2 {
						return errors.New("state locked")
					}
					return nil
				}, func(map[string]string) error { return fragmentErr })
			},
			cause: fragmentErr,
			exit:  constants.ExitError,
			en:    "regenerate global mise fragment: disk full; restoring previous state also failed: state locked",
			ja:    "グローバル mise のフラグメントを作り直せません: disk full。以前の状態の復元にも失敗しました: state locked",
		},
		{
			name: "global fragment regeneration, state restored",
			build: func(*testing.T) error {
				return saveSyncedStateAndFragment(state.New(), state.New(),
					func(*state.State) error { return nil },
					func(map[string]string) error { return fragmentErr })
			},
			cause: fragmentErr,
			exit:  constants.ExitError,
			en:    "regenerate global mise fragment (previous state restored): disk full",
			ja:    "グローバル mise のフラグメントを作り直せません（以前の状態は復元しました）: disk full",
		},
		{
			// auth_missing: the warnings detail is a value and renders Japanese too.
			name: "capture with no live login",
			build: func(t *testing.T) error {
				app := testApp(t, nil)
				return app.captureSnapshot(context.Background(), nil, toolPlan{
					Tool: "claude", Account: "main",
					Warnings: []message{msgf("metadata listing is incomplete; readable records are shown")},
				})
			},
			exit: constants.ExitAuthMissing,
			en:   "no live claude auth state found; log in with the official CLI first (metadata listing is incomplete; readable records are shown)",
			ja:   "claude の現在の認証状態が見つかりません。先に公式の CLI でログインしてください（メタデータの一覧が不完全です。読み取れた記録だけを表示しています）",
		},
		{
			name: "companion template parse",
			build: func(*testing.T) error {
				_, err := renderCompanionFile(companion.Spec{ID: "git", FileTmpl: "{{"}, nil, "~/.gitconfig")
				return err
			},
			exit: constants.ExitError,
			en:   "parse git config template: template: git:1: unclosed action",
			ja:   "git の設定ファイルのテンプレートを解析できません: template: git:1: unclosed action",
		},
	}
}

func TestWrappedCommandErrorsAreLocalized(t *testing.T) {
	for _, c := range wrapCases() {
		check := func(t *testing.T, wantHuman string) {
			t.Helper()
			err := c.build(t)
			if err == nil {
				t.Fatal("no error")
			}
			en := normalizeConfigPath(err.Error())
			if en != c.en {
				t.Errorf("Error() = %q, want %q", en, c.en)
			}
			if c.cause != nil && !errors.Is(err, c.cause) {
				t.Errorf("errors.Is lost the cause %v", c.cause)
			}
			if got := exitOf(err); got != c.exit {
				t.Errorf("exitOf = %d, want %d", got, c.exit)
			}
			if got := normalizeConfigPath(l10n.Render(err)); got != wantHuman {
				t.Errorf("Render = %q, want %q", got, wantHuman)
			}
		}
		t.Run(c.name+"/english", func(t *testing.T) { check(t, c.en) })
		t.Run(c.name+"/japanese", func(t *testing.T) {
			l10ntest.UseJapanese(t)
			check(t, c.ja)
		})
	}
}

// normalizeConfigPath replaces a temporary config path with CONFIG.
func normalizeConfigPath(s string) string {
	if i := strings.Index(s, "open /"); i >= 0 {
		if j := strings.Index(s[i:], ": no such"); j >= 0 {
			return s[:i] + "open CONFIG" + s[i+j:]
		}
	}
	return s
}

// A command that ends on a wrapped error prints the Japanese line in text mode and
// the English text in JSON, with the same exit code.
func TestInitCreateErrorIsLocalizedOnlyForPeople(t *testing.T) {
	run := func(t *testing.T, format string) (int, string, string) {
		app := testApp(t, nil)
		// A regular file where the data directory's parent should be makes
		// MkdirAll fail after the config lock is taken.
		blocker := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(blocker, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		app.Paths.DataDir = filepath.Join(blocker, "data")
		code, stdout, stderr := captureBoth(t, func() int {
			return runInit(context.Background(), app, commonOpts{Format: format})
		})
		stderr = strings.ReplaceAll(stderr, app.Paths.DataDir, "DATA")
		return code, stdout, strings.ReplaceAll(stderr, blocker, "BLOCKER")
	}
	t.Run("japanese text", func(t *testing.T) {
		l10ntest.UseJapanese(t)
		code, _, stderr := run(t, formatText)
		if code != constants.ExitError || stderr != "kae: DATA を作成できません: mkdir BLOCKER: not a directory\n" {
			t.Errorf("exit %d, stderr %q", code, stderr)
		}
	})
	t.Run("japanese json", func(t *testing.T) {
		l10ntest.UseJapanese(t)
		code, stdout, _ := run(t, formatJSON)
		var report errorReport
		if err := json.Unmarshal([]byte(stdout), &report); err != nil {
			t.Fatalf("%v: %q", err, stdout)
		}
		if code != constants.ExitError || !strings.HasPrefix(report.Message, "create ") ||
			!strings.HasSuffix(report.Message, ": not a directory") {
			t.Errorf("exit %d, message %q", code, report.Message)
		}
	})
}

// kae __companion-token's not-stored line is localized after the English prefix.
func TestCompanionTokenNotStoredIsLocalized(t *testing.T) {
	run := func(t *testing.T) (int, string) {
		app := testApp(t, nil)
		return captureStderr(t, func() int {
			return companionToken(context.Background(), app, []string{"main", "gh", "GH_TOKEN"})
		})
	}
	code, stderr := run(t)
	if code != constants.ExitNotFound ||
		stderr != "kae: companion token main/gh/GH_TOKEN is not stored; run: kae companion add main gh GH_TOKEN\n" {
		t.Errorf("english: exit %d, %q", code, stderr)
	}
	l10ntest.UseJapanese(t)
	code, stderr = run(t)
	if code != constants.ExitNotFound ||
		stderr != "kae: 周辺ツールのトークン main/gh/GH_TOKEN が保存されていません。kae companion add main gh GH_TOKEN を実行してください\n" {
		t.Errorf("japanese: exit %d, %q", code, stderr)
	}
}
