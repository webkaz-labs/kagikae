package adapter_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/l10n"
	"github.com/webkaz-labs/kagikae/internal/runner"
	"github.com/webkaz-labs/kagikae/internal/testutil/l10ntest"
	"github.com/webkaz-labs/kagikae/internal/testutil/runnertest"
)

// errorCase is one error an adapter returns: how to raise it, the English text
// (Error(), what JSON and errors.Is see) and the Japanese a person reads. The
// expectations may name the placeholders "$HOME", replaced by the env's home, and
// "$CAUSE", replaced by the text of the one external cause the error wraps (a
// standard-library message whose wording kae does not own).
type errorCase struct {
	name        string
	raise       func(t *testing.T) (string, error)
	unsupported bool // wraps adapter.ErrUnsupported (exit code and doctor's unsupported check)
	en, ja      string
}

// identityIn raises adp's Identity error for an env whose home holds files.
func identityIn(adp adapter.Identifier, goos string, vars, files map[string]string) func(t *testing.T) (string, error) {
	return func(t *testing.T) (string, error) {
		env := testEnv(t, goos, vars)
		for rel, content := range files {
			write(t, filepath.Join(env.Home, rel), content)
		}
		_, err := adp.Identity(context.Background(), env)
		return env.Home, err
	}
}

// artifactsIn raises adp's Artifacts error (the driver or store refusal).
func artifactsIn(adp adapter.Adapter, goos string, vars, files map[string]string) func(t *testing.T) (string, error) {
	return func(t *testing.T) (string, error) {
		env := testEnv(t, goos, vars)
		for rel, content := range files {
			write(t, filepath.Join(env.Home, rel), content)
		}
		_, err := adp.Artifacts(context.Background(), env)
		return env.Home, err
	}
}

// cursorIdentity raises cursor's Identity error from a faked `cursor-agent status`.
func cursorIdentity(fake *runnertest.Fake) func(t *testing.T) (string, error) {
	return func(t *testing.T) (string, error) {
		env := testEnv(t, "darwin", nil)
		var err error
		runner.With(fake, func() { _, err = cursorAdapter.Identity(context.Background(), env) })
		if fake.Name != "cursor-agent" || strings.Join(fake.Args, " ") != "status" {
			t.Errorf("ran %s %q, want cursor-agent status", fake.Name, fake.Args)
		}
		return env.Home, err
	}
}

// wrappedCause returns the one cause err wraps with %w.
func wrappedCause(t *testing.T, err error) error {
	t.Helper()
	wrapper, ok := err.(interface{ Unwrap() []error })
	if !ok || len(wrapper.Unwrap()) != 1 {
		t.Fatalf("%q does not wrap exactly one cause", err)
	}
	return wrapper.Unwrap()[0]
}

// Every error an adapter builds is kae's message: Japanese for a person, its English
// text unchanged for JSON and errors.Is. An external cause (the OS or a decoder)
// stays verbatim inside it.
func TestAdapterErrorsRenderInJapanese(t *testing.T) {
	l10ntest.UseJapanese(t)
	const codexConfig = ".codex/config.toml"
	cases := []errorCase{
		{
			name: "no adapter",
			raise: func(*testing.T) (string, error) {
				_, err := adapter.ForTool("nope")
				return "", err
			},
			en: `no adapter for tool "nope"`, ja: `ツール "nope" のアダプターがありません`,
		},
		{
			name:  "claude empty secure storage dir",
			raise: artifactsIn(claudeAdapter, "linux", map[string]string{"CLAUDE_SECURESTORAGE_CONFIG_DIR": ""}, nil),
			en: "unsupported: CLAUDE_SECURESTORAGE_CONFIG_DIR is set to an empty value, which collapses every config dir" +
				" onto claude's one global credential item (unset it to let kae manage claude)",
			ja: "対応していません: CLAUDE_SECURESTORAGE_CONFIG_DIR が空の値に設定されているため、すべての設定ディレクトリが" +
				" claude のグローバルな認証情報の項目 1 つを共有してしまいます（kae に claude を管理させるには、この環境変数の設定を解除してください）",
			unsupported: true,
		},
		{
			name:  "claude custom oauth url",
			raise: artifactsIn(claudeAdapter, "linux", map[string]string{"CLAUDE_CODE_CUSTOM_OAUTH_URL": "https://example.com"}, nil),
			en: "unsupported: CLAUDE_CODE_CUSTOM_OAUTH_URL is set, which renames claude's keychain item and identity file" +
				" (unset it to let kae manage claude)",
			ja: "対応していません: CLAUDE_CODE_CUSTOM_OAUTH_URL が設定されているため、claude のキーチェーン項目とアカウント記録のファイルの名前が変わります" +
				"（kae に claude を管理させるには、この環境変数の設定を解除してください）",
			unsupported: true,
		},
		{
			name:        "claude invalid driver",
			raise:       artifactsIn(claudeAdapter, "linux", map[string]string{"KAE_CLAUDE_DRIVER": "bogus"}, nil),
			en:          `unsupported: KAE_CLAUDE_DRIVER="bogus" is invalid (only "file" is supported)`,
			ja:          `対応していません: KAE_CLAUDE_DRIVER="bogus" は不正な値です（対応しているのは "file" だけです）`,
			unsupported: true,
		},
		{
			name:        "claude platform",
			raise:       artifactsIn(claudeAdapter, "windows", nil, nil),
			en:          "unsupported: claude auth switching is not supported on windows",
			ja:          "対応していません: windows では claude の認証の切替に対応していません",
			unsupported: true,
		},
		{
			name:  "claude identity unreadable",
			raise: identityIn(claudeAdapter, "linux", nil, nil),
			en:    "read $HOME/.claude.json: open $HOME/.claude.json: no such file or directory",
			ja:    "$HOME/.claude.json を読み取れません: open $HOME/.claude.json: no such file or directory",
		},
		{
			name:  "claude identity unparsable",
			raise: identityIn(claudeAdapter, "linux", nil, map[string]string{".claude.json": "{"}),
			en:    "parse $HOME/.claude.json: unexpected end of JSON input",
			ja:    "$HOME/.claude.json を解析できません: unexpected end of JSON input",
		},
		{
			name:  "claude identity missing",
			raise: identityIn(claudeAdapter, "linux", nil, map[string]string{".claude.json": "{}"}),
			en:    "no oauthAccount.emailAddress in $HOME/.claude.json",
			ja:    "$HOME/.claude.json に oauthAccount.emailAddress がありません",
		},
		{
			name:  "codex secret auth storage",
			raise: artifactsIn(codexAdapter, "darwin", nil, map[string]string{codexConfig: "cli_auth_credentials_store = \"auto\"\n[features]\nsecret_auth_storage = true\n"}),
			en: `unsupported: codex [features] secret_auth_storage keeps the credential in an encrypted secrets file,` +
				` not the "Codex Auth" keychain item`,
			ja:          `対応していません: codex の [features] secret_auth_storage は、認証情報をキーチェーン項目 "Codex Auth" ではなく暗号化されたシークレットのファイルに保存します`,
			unsupported: true,
		},
		{
			name:  "codex keyring off macOS",
			raise: artifactsIn(codexAdapter, "linux", nil, map[string]string{codexConfig: "cli_auth_credentials_store = \"keyring\"\n"}),
			en: `unsupported: codex cli_auth_credentials_store = "keyring" keeps the credential in the OS keyring,` +
				` which kae can only read on macOS (this is linux)`,
			ja:          `対応していません: codex の cli_auth_credentials_store = "keyring" は認証情報を OS のキーリングに保存しますが、kae がキーリングを読み取れるのは macOS だけです（この環境は linux です）`,
			unsupported: true,
		},
		{
			name:  "codex ephemeral",
			raise: artifactsIn(codexAdapter, "linux", nil, map[string]string{codexConfig: "cli_auth_credentials_store = \"ephemeral\"\n"}),
			en: `unsupported: codex cli_auth_credentials_store = "ephemeral" keeps the credential in memory for one process,` +
				` so there is nothing to capture or switch`,
			ja:          `対応していません: codex の cli_auth_credentials_store = "ephemeral" は認証情報を 1 つのプロセスのメモリーにだけ保持するため、登録や切替の対象がありません`,
			unsupported: true,
		},
		{
			name:        "codex unknown store",
			raise:       artifactsIn(codexAdapter, "linux", nil, map[string]string{codexConfig: "cli_auth_credentials_store = \"vault\"\n"}),
			en:          `unsupported: codex cli_auth_credentials_store = "vault" is not one of "file", "keyring", "auto", "ephemeral"`,
			ja:          `対応していません: codex の cli_auth_credentials_store = "vault" は "file"、"keyring"、"auto"、"ephemeral" のいずれでもありません`,
			unsupported: true,
		},
		{
			name:  "codex config unreadable",
			raise: artifactsIn(codexAdapter, "linux", nil, map[string]string{codexConfig + "/x": ""}),
			en:    "read $HOME/.codex/config.toml: read $HOME/.codex/config.toml: is a directory",
			ja:    "$HOME/.codex/config.toml を読み取れません: read $HOME/.codex/config.toml: is a directory",
		},
		{
			name:  "codex auth unreadable",
			raise: identityIn(codexAdapter, "linux", nil, nil),
			en:    "read $HOME/.codex/auth.json: open $HOME/.codex/auth.json: no such file or directory",
			ja:    "$HOME/.codex/auth.json を読み取れません: open $HOME/.codex/auth.json: no such file or directory",
		},
		{
			name:  "codex auth unparsable",
			raise: identityIn(codexAdapter, "linux", nil, map[string]string{".codex/auth.json": "{"}),
			en:    "parse $HOME/.codex/auth.json: unexpected end of JSON input",
			ja:    "$HOME/.codex/auth.json を解析できません: unexpected end of JSON input",
		},
		{
			name:  "codex auth without identity",
			raise: identityIn(codexAdapter, "linux", nil, map[string]string{".codex/auth.json": `{"tokens":{}}`}),
			en:    "no id_token email claim or account_id in $HOME/.codex/auth.json",
			ja:    "$HOME/.codex/auth.json に id_token の email クレームも account_id もありません",
		},
		{
			name: "codex keychain item missing",
			raise: func(t *testing.T) (string, error) {
				env := testEnv(t, "darwin", nil)
				write(t, filepath.Join(env.Home, codexConfig), "cli_auth_credentials_store = \"keyring\"\n")
				fake := &runnertest.Fake{Stderr: "could not be found", Code: 44}
				var err error
				runner.With(fake, func() { _, err = codexAdapter.Identity(context.Background(), env) })
				if fake.Name != "security" || len(fake.Args) == 0 || fake.Args[0] != "find-generic-password" {
					t.Errorf("ran %s %q, want security find-generic-password", fake.Name, fake.Args)
				}
				return env.Home, err
			},
			en: "no Codex Auth keychain item for this codex home",
			ja: "この codex ホームに対応する Codex Auth のキーチェーン項目がありません",
		},
		{
			name:  "copilot unreadable",
			raise: identityIn(copilotAdapter, "linux", nil, nil),
			en:    "read $HOME/.copilot/config.json: open $HOME/.copilot/config.json: no such file or directory",
			ja:    "$HOME/.copilot/config.json を読み取れません: open $HOME/.copilot/config.json: no such file or directory",
		},
		{
			name:  "copilot no last user",
			raise: identityIn(copilotAdapter, "linux", nil, map[string]string{".copilot/config.json": "{}"}),
			en:    "no /lastLoggedInUser in $HOME/.copilot/config.json",
			ja:    "$HOME/.copilot/config.json に /lastLoggedInUser がありません",
		},
		{
			name:  "copilot last user not an object",
			raise: identityIn(copilotAdapter, "linux", nil, map[string]string{".copilot/config.json": `{"lastLoggedInUser":1}`}),
			en:    "parse $HOME/.copilot/config.json/lastLoggedInUser: $CAUSE",
			ja:    "$HOME/.copilot/config.json/lastLoggedInUser を解析できません: $CAUSE",
		},
		{
			name:  "copilot no login",
			raise: identityIn(copilotAdapter, "linux", nil, map[string]string{".copilot/config.json": `{"lastLoggedInUser":{}}`}),
			en:    "no /lastLoggedInUser/login in $HOME/.copilot/config.json",
			ja:    "$HOME/.copilot/config.json に /lastLoggedInUser/login がありません",
		},
		{
			name:        "cursor platform",
			raise:       artifactsIn(cursorAdapter, "linux", nil, nil),
			en:          "unsupported: cursor auth switching is not supported on linux yet (credential switching is verified on macOS only)",
			ja:          "対応していません: linux ではまだ cursor の認証の切替に対応していません（認証情報の切替を確認しているのは macOS だけです）",
			unsupported: true,
		},
		{
			name:  "cursor status failed",
			raise: cursorIdentity(&runnertest.Fake{Stderr: "network down", Code: 2}),
			en:    "cursor-agent status failed (exit 2): network down",
			ja:    "cursor-agent status が失敗しました（終了コード 2）: network down",
		},
		{
			name:  "cursor status without login",
			raise: cursorIdentity(&runnertest.Fake{Stdout: "Not logged in\n"}),
			en:    "cursor-agent status did not report a logged-in account",
			ja:    "cursor-agent status がログイン中のアカウントを報告しませんでした",
		},
		{
			name:  "cursor status empty account",
			raise: cursorIdentity(&runnertest.Fake{Stdout: "Logged in as \n"}),
			en:    "cursor-agent status reported an empty account",
			ja:    "cursor-agent status が空のアカウントを報告しました",
		},
		{
			name:  "agy unreadable",
			raise: identityIn(agyAdapter, "linux", nil, nil),
			en:    "read $HOME/.gemini/google_accounts.json: open $HOME/.gemini/google_accounts.json: no such file or directory",
			ja:    "$HOME/.gemini/google_accounts.json を読み取れません: open $HOME/.gemini/google_accounts.json: no such file or directory",
		},
		{
			name:  "agy unparsable",
			raise: identityIn(agyAdapter, "linux", nil, map[string]string{".gemini/google_accounts.json": "{"}),
			en:    "parse $HOME/.gemini/google_accounts.json: unexpected end of JSON input",
			ja:    "$HOME/.gemini/google_accounts.json を解析できません: unexpected end of JSON input",
		},
		{
			name:  "agy no active account",
			raise: identityIn(agyAdapter, "linux", nil, map[string]string{".gemini/google_accounts.json": "{}"}),
			en:    "no active Google account in $HOME/.gemini/google_accounts.json",
			ja:    "$HOME/.gemini/google_accounts.json に有効な Google アカウントがありません",
		},
		{
			name:  "opencode unreadable",
			raise: identityIn(opencodeAdapter, "linux", nil, nil),
			en:    "read $HOME/.local/share/opencode/auth.json: open $HOME/.local/share/opencode/auth.json: no such file or directory",
			ja:    "$HOME/.local/share/opencode/auth.json を読み取れません: open $HOME/.local/share/opencode/auth.json: no such file or directory",
		},
		{
			name:  "opencode unparsable",
			raise: identityIn(opencodeAdapter, "linux", nil, map[string]string{".local/share/opencode/auth.json": "{"}),
			en:    "parse $HOME/.local/share/opencode/auth.json: unexpected end of JSON input",
			ja:    "$HOME/.local/share/opencode/auth.json を解析できません: unexpected end of JSON input",
		},
		{
			name:  "opencode without identity",
			raise: identityIn(opencodeAdapter, "linux", nil, map[string]string{".local/share/opencode/auth.json": `{"openai":{}}`}),
			en:    "no openai email claim or accountId in $HOME/.local/share/opencode/auth.json",
			ja:    "$HOME/.local/share/opencode/auth.json に openai の email クレームも accountId もありません",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home, err := tc.raise(t)
			if err == nil {
				t.Fatal("expected an error")
			}
			expand := func(s string) string {
				s = strings.ReplaceAll(s, "$HOME", home)
				if strings.Contains(s, "$CAUSE") {
					s = strings.ReplaceAll(s, "$CAUSE", wrappedCause(t, err).Error())
				}
				return s
			}
			if got := err.Error(); got != expand(tc.en) {
				t.Errorf("Error() = %q, want %q", got, expand(tc.en))
			}
			if got := l10n.Render(err); got != expand(tc.ja) {
				t.Errorf("Render = %q, want %q", got, expand(tc.ja))
			}
			if got := errors.Is(err, adapter.ErrUnsupported); got != tc.unsupported {
				t.Errorf("errors.Is(err, ErrUnsupported) = %v, want %v", got, tc.unsupported)
			}
			// The doctor check and a message that embeds the error carry it as a value,
			// so both render it in Japanese too.
			if got := l10n.Render(l10n.Of(err)); got != expand(tc.ja) {
				t.Errorf("Render(Of) = %q, want %q", got, expand(tc.ja))
			}
		})
	}
}

// An OS cause keeps its own error identity through kae's wrapper.
func TestAdapterReadErrorKeepsTheOSCause(t *testing.T) {
	env := testEnv(t, "linux", nil)
	_, err := claudeAdapter.Identity(context.Background(), env)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want os.ErrNotExist through the wrapper", err)
	}
}
