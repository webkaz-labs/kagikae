package adapter_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/l10n"
	"github.com/webkaz-labs/kagikae/internal/runner"
	"github.com/webkaz-labs/kagikae/internal/testutil/l10ntest"
	"github.com/webkaz-labs/kagikae/internal/testutil/runnertest"
)

// renderedChecks renders every Doctor message of adp for env in Japanese, and returns
// the English text of the same messages so a test can pin both.
func renderedChecks(t *testing.T, adp adapter.Adapter, env adapter.Env) (ja, en []string) {
	t.Helper()
	for _, c := range adp.Doctor(context.Background(), env) {
		ja = append(ja, l10n.Render(c.Message))
		en = append(en, c.Message.Error())
	}
	return ja, en
}

func renderedWarnings(t *testing.T, adp adapter.Adapter, env adapter.Env) (ja, en []string) {
	t.Helper()
	info, err := adp.Detect(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range info.Warnings {
		ja = append(ja, l10n.Render(w))
		en = append(en, w.Error())
	}
	return ja, en
}

// wantAll fails unless every want is a substring of one of lines.
func wantAll(t *testing.T, lines []string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		found := false
		for _, line := range lines {
			if strings.Contains(line, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("no line contains %q:\n%s", want, strings.Join(lines, "\n"))
		}
	}
}

// wantTranslated fails when any rendered Japanese line is unchanged English, which
// is what a missing catalog entry renders as.
func wantTranslated(t *testing.T, ja, en []string) {
	t.Helper()
	for i := range ja {
		if ja[i] == en[i] {
			t.Errorf("message %d is not translated: %q", i, en[i])
		}
	}
}

func TestAdapterChecksAndWarningsRenderInJapanese(t *testing.T) {
	l10ntest.UseJapanese(t)

	t.Run("claude", func(t *testing.T) {
		env := testEnv(t, "linux", nil)
		ja, en := renderedChecks(t, claudeAdapter, env)
		wantTranslated(t, ja, en)
		wantAll(t, ja, "claude が PATH に見つかりません。", "ドライバー: ",
			"現在のサブスクリプションの認証情報がありません（先に claude でログインしてください）。")

		creds := filepath.Join(env.Home, ".claude", ".credentials.json")
		write(t, creds, `{"claudeAiOauth":{"accessToken":"a"}}`)
		if err := os.Chmod(creds, 0o644); err != nil {
			t.Fatal(err)
		}
		ja, en = renderedChecks(t, claudeAdapter, env)
		wantTranslated(t, ja, en)
		wantAll(t, ja, "現在のサブスクリプションの認証情報があります。", "がグループまたは全員に読み取れる権限になっています。0600 にしてください。")

		rel := testEnv(t, "linux", map[string]string{
			"CLAUDE_CONFIG_DIR": "rel", "CLAUDE_SECURESTORAGE_CONFIG_DIR": "rel2",
		})
		ja, en = renderedWarnings(t, claudeAdapter, rel)
		wantTranslated(t, ja, en)
		wantAll(t, ja, "CLAUDE_CONFIG_DIR が相対パスです。", "CLAUDE_SECURESTORAGE_CONFIG_DIR が相対パスです。")
		ja, _ = renderedChecks(t, claudeAdapter, rel)
		wantAll(t, ja, "CLAUDE_CONFIG_DIR が相対パスです。")
	})

	t.Run("codex file store", func(t *testing.T) {
		env := testEnv(t, "linux", nil)
		write(t, filepath.Join(env.Home, ".codex", "config.toml"), "cli_auth_credentials_store = \"auto\"\n")
		ja, en := renderedChecks(t, codexAdapter, env)
		wantTranslated(t, ja, en)
		wantAll(t, ja, "認証ストア: ", "（auth.json）", "auth.json がありません。先に codex でログインしてください。")
		ja, en = renderedWarnings(t, codexAdapter, env)
		wantTranslated(t, ja, en)
		wantAll(t, ja, "auth.json が見つかりません。codex がログインしていないか")

		write(t, filepath.Join(env.Home, ".codex", "auth.json"), `{"tokens":{}}`)
		ja, en = renderedChecks(t, codexAdapter, env)
		wantTranslated(t, ja, en)
		wantAll(t, ja, "auth.json があります。")

		rel := testEnv(t, "linux", map[string]string{"CODEX_HOME": "rel"})
		ja, en = renderedWarnings(t, codexAdapter, rel)
		wantTranslated(t, ja, en)
		wantAll(t, ja, "CODEX_HOME が相対パスです。")
	})

	t.Run("codex keyring", func(t *testing.T) {
		env := testEnv(t, "darwin", nil)
		write(t, filepath.Join(env.Home, ".codex", "config.toml"), "cli_auth_credentials_store = \"keyring\"\n")
		runner.With(&runnertest.Fake{Stderr: "could not be found", Code: 44}, func() {
			ja, en := renderedChecks(t, codexAdapter, env)
			wantTranslated(t, ja, en)
			wantAll(t, ja, "認証ストア: keyring（この codex ホームに対応する Codex Auth のキーチェーン項目）",
				"この codex ホームに対応する Codex Auth のキーチェーン項目がありません。先に codex でログインしてください。")
			ja, en = renderedWarnings(t, codexAdapter, env)
			wantTranslated(t, ja, en)
			wantAll(t, ja, "この codex ホームに対応する Codex Auth のキーチェーン項目がありません。")
		})
		runner.With(&runnertest.Fake{Stdout: `{"tokens":{"access_token":"a","refresh_token":"r"}}`}, func() {
			ja, en := renderedChecks(t, codexAdapter, env)
			wantTranslated(t, ja, en)
			wantAll(t, ja, "この codex ホームに対応する Codex Auth のキーチェーン項目があります。")
		})
	})

	t.Run("codex contradicted store", func(t *testing.T) {
		env := testEnv(t, "darwin", nil)
		runner.With(&runnertest.Fake{Stdout: "{}"}, func() {
			ja, en := renderedChecks(t, codexAdapter, env)
			wantTranslated(t, ja, en)
			wantAll(t, ja, "kae は file ストア（auth.json）に解決しました。",
				"config.toml の cli_auth_credentials_store を確認し")
		})
	})

	t.Run("copilot", func(t *testing.T) {
		env := testEnv(t, "linux", nil)
		ja, en := renderedChecks(t, copilotAdapter, env)
		wantTranslated(t, ja, en)
		wantAll(t, ja, "config.json に有効なアカウントがありません。先に copilot login でログインしてください。")

		write(t, filepath.Join(env.Home, ".copilot", "config.json"),
			`{"lastLoggedInUser":{"host":"https://github.com","login":"main"}}`)
		ja, en = renderedChecks(t, copilotAdapter, env)
		wantTranslated(t, ja, en)
		wantAll(t, ja, "config.json に有効なアカウントが記録されています。")

		rel := testEnv(t, "linux", map[string]string{"COPILOT_HOME": "rel"})
		ja, en = renderedWarnings(t, copilotAdapter, rel)
		wantTranslated(t, ja, en)
		wantAll(t, ja, "COPILOT_HOME が相対パスです。")
	})

	t.Run("opencode", func(t *testing.T) {
		env := testEnv(t, "linux", nil)
		ja, en := renderedChecks(t, opencodeAdapter, env)
		wantTranslated(t, ja, en)
		wantAll(t, ja, "auth.json に openai（ChatGPT サブスクリプション）のログインがありません。先に opencode auth login でログインしてください。")

		auth := filepath.Join(env.Home, ".local", "share", "opencode", "auth.json")
		write(t, auth, `{"anthropic":{"type":"api","key":"x"}}`)
		ja, en = renderedWarnings(t, opencodeAdapter, env)
		wantTranslated(t, ja, en)
		wantAll(t, ja, "auth.json に openai の項目がありません。")

		write(t, auth, `{"openai":{"type":"oauth","access":"a","refresh":"r","expires":1}}`)
		ja, en = renderedChecks(t, opencodeAdapter, env)
		wantTranslated(t, ja, en)
		wantAll(t, ja, "auth.json に openai（ChatGPT サブスクリプション）のログインがあります。")

		rel := testEnv(t, "linux", map[string]string{"XDG_DATA_HOME": "rel"})
		ja, en = renderedWarnings(t, opencodeAdapter, rel)
		wantTranslated(t, ja, en)
		wantAll(t, ja, "XDG_DATA_HOME が相対パスです。")
	})

	t.Run("cursor", func(t *testing.T) {
		env := testEnv(t, "darwin", nil)
		runner.With(&runnertest.Fake{Stderr: "could not be found", Code: 44}, func() {
			ja, en := renderedChecks(t, cursorAdapter, env)
			wantTranslated(t, ja, en)
			wantAll(t, ja, "キーチェーンにアクセストークンがありません。先に cursor-agent login でログインしてください。",
				"ドライバー: ")
		})
		runner.With(&runnertest.Fake{Stdout: "opaque-token\n"}, func() {
			ja, en := renderedChecks(t, cursorAdapter, env)
			wantTranslated(t, ja, en)
			wantAll(t, ja, "キーチェーンにアクセストークンがあります。")
		})
	})

	t.Run("agy keychain", func(t *testing.T) {
		env := testEnv(t, "darwin", nil)
		runner.With(&runnertest.Fake{Stderr: "could not be found", Code: 44}, func() {
			ja, en := renderedChecks(t, agyAdapter, env)
			wantTranslated(t, ja, en)
			wantAll(t, ja, "gemini/antigravity のキーチェーン項目がありません。先に Antigravity アプリでログインしてください。")
			ja, en = renderedWarnings(t, agyAdapter, env)
			wantTranslated(t, ja, en)
			wantAll(t, ja, "gemini/antigravity のキーチェーン項目がありません。")
		})
		bypass := testEnv(t, "darwin", map[string]string{"SSH_TTY": "/dev/ttys001"})
		runner.With(&runnertest.Fake{Stdout: "opaque-token\n"}, func() {
			ja, en := renderedChecks(t, agyAdapter, bypass)
			wantTranslated(t, ja, en)
			wantAll(t, ja, "gemini/antigravity のキーチェーン項目があります。",
				"SSH_TTY が設定されています。ここでは agy がキーチェーンを使わず")
		})
	})

	t.Run("agy file", func(t *testing.T) {
		env := testEnv(t, "linux", nil)
		ja, en := renderedChecks(t, agyAdapter, env)
		wantTranslated(t, ja, en)
		wantAll(t, ja, "（このマシンでは agy を設定していません）",
			"ドライバー: ", "（ファイル形式。このプラットフォームではキーリングの切替に対応していません）")
		ja, en = renderedWarnings(t, agyAdapter, env)
		wantTranslated(t, ja, en)
		wantAll(t, ja, "このプラットフォームの agy アダプターは、ファイル形式の認証情報だけを扱います。")

		if err := os.MkdirAll(filepath.Join(env.Home, ".gemini", "antigravity-cli"), 0o700); err != nil {
			t.Fatal(err)
		}
		ja, en = renderedChecks(t, agyAdapter, env)
		wantTranslated(t, ja, en)
		wantAll(t, ja, "（agy は OS のキーリングを使っている可能性が高く、このプラットフォームでは kae は切り替えられません）")
		ja, en = renderedWarnings(t, agyAdapter, env)
		wantTranslated(t, ja, en)
		wantAll(t, ja, "認証情報のファイルが見つかりません。")

		write(t, filepath.Join(env.Home, ".gemini", "antigravity-cli", "credentials.json"), `{}`)
		ja, en = renderedChecks(t, agyAdapter, env)
		wantTranslated(t, ja, en)
		wantAll(t, ja, "ファイル形式の認証情報があります。")
	})

	t.Run("env conflict", func(t *testing.T) {
		got := l10n.Render(adapter.EnvConflictWarning("EXAMPLE_TOKEN"))
		if got != "EXAMPLE_TOKEN が設定されているため、切り替えたログインより優先されます。" {
			t.Fatalf("EnvConflictWarning = %q", got)
		}
	})
}

// The JSON of a check and a warning is English whatever the language, and an
// external cause (an err.Error() the check carries) is quoted verbatim.
func TestAdapterMessagesStayEnglishInJSON(t *testing.T) {
	l10ntest.UseJapanese(t)
	env := testEnv(t, "linux", nil)
	checks := claudeAdapter.Doctor(context.Background(), env)
	raw, err := json.Marshal(checks)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"message":"claude not found in PATH"`,
		`"message":"no live subscription credential (log in with claude first)"`,
	} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("json %s lacks %s", raw, want)
		}
	}
	if strings.ContainsFunc(string(raw), func(r rune) bool { return r > 0x7f }) {
		t.Errorf("json holds non-ASCII text under a Japanese run: %s", raw)
	}

	warnings, err := json.Marshal(adapter.EnvConflictWarnings(
		testEnv(t, "linux", map[string]string{"EXAMPLE_TOKEN": "x"}), []string{"EXAMPLE_TOKEN"},
	))
	if err != nil || string(warnings) != `["EXAMPLE_TOKEN is set and overrides the switched login"]` {
		t.Fatalf("warnings json = %s, %v", warnings, err)
	}

	// An unsupported platform's refusal is kae's own error: the check renders it in
	// Japanese and its JSON stays English.
	unsupported := claudeAdapter.Doctor(context.Background(), testEnv(t, "windows", nil))
	if len(unsupported) != 1 || unsupported[0].Code != constants.CheckUnsupported {
		t.Fatalf("checks = %+v, want one unsupported check", unsupported)
	}
	if got := l10n.Render(unsupported[0].Message); got != "対応していません: windows では claude の認証の切替に対応していません" {
		t.Errorf("unsupported check = %q", got)
	}
	raw, err = json.Marshal(unsupported)
	if err != nil || !strings.Contains(string(raw), `"message":"unsupported: claude auth switching is not supported on windows"`) {
		t.Errorf("unsupported json = %s, %v", raw, err)
	}

	// A malformed config.toml is reported by line with a fixed reason: the TOML
	// decoder's own text quotes the input, and this file holds MCP server env and
	// headers, so neither the value nor its key reaches output in either language.
	env = testEnv(t, "linux", nil)
	write(t, filepath.Join(env.Home, ".codex", "config.toml"),
		"[mcp_servers.x.env]\nSYNTH_API_KEY = SYNTHSECRET-unquoted\n")
	checks = codexAdapter.Doctor(context.Background(), env)
	if len(checks) == 0 || checks[0].Code != constants.CheckUnsupported {
		t.Fatalf("checks = %+v, want an unsupported check first", checks)
	}
	english, got := checks[0].Message.Error(), l10n.Render(checks[0].Message)
	if !strings.HasSuffix(english, ": the document has a syntax error at line 2") ||
		!strings.HasSuffix(got, " を解析できません: ドキュメントの 2 行目に構文エラーがあります") {
		t.Errorf("parse failure = %q / %q, want the line and a fixed reason", english, got)
	}
	for _, text := range []string{english, got} {
		if strings.Contains(text, "SYNTH") {
			t.Errorf("parse failure quotes the document: %q", text)
		}
	}
}
