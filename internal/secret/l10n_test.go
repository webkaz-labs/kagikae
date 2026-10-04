package secret

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/runner"
	"github.com/webkaz-labs/kagikae/internal/testutil/l10ntest"
	"github.com/webkaz-labs/kagikae/internal/testutil/runnertest"
)

// The secret store's errors are message values: Error() keeps the English text
// and a human sink renders the catalog's Japanese, with the sentinel head and
// the upstream stderr snippet in place.
func TestSecretErrorsRenderInJapanese(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	corrupt := fileBackend{dir: dir}
	if err := os.WriteFile(corrupt.path("claude"), []byte("not base64!"), 0o600); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	failing := &runnertest.Fake{Stderr: "boom\n", Code: 1}

	cases := []struct {
		name    string
		err     func() error
		english string
		ja      string
	}{
		{
			"keychain off macOS", func() error {
				_, err := Resolve(BackendKeychain, "linux", dir, lookPathMissing)
				return err
			}, "secret store unavailable: keychain backend requires macOS",
			"シークレットストアを使えません: キーチェーンは macOS でしか使えません",
		},
		{
			"no secret-tool", func() error {
				_, err := Resolve(BackendLibsecret, "linux", dir, lookPathMissing)
				return err
			}, "secret store unavailable: secret-tool not found in PATH (install libsecret tools)",
			"シークレットストアを使えません: PATH に secret-tool が見つかりません（libsecret のツールをインストールしてください）",
		},
		{
			"no OS store", func() error {
				_, err := Resolve(BackendAuto, "linux", dir, lookPathMissing)
				return err
			}, `secret store unavailable: no OS credential store found; install libsecret tools or opt in with security.secret_backend = "file"`,
			`シークレットストアを使えません: OS の認証ストアが見つかりません。libsecret のツールをインストールするか、security.secret_backend = "file" を設定してファイルへの保存を選んでください`,
		},
		{"unknown backend", func() error {
			_, err := Resolve("bogus", "linux", dir, lookPathFound)
			return err
		}, `unknown secret_backend "bogus"`, `secret_backend の値 "bogus" は不明です`},
		{"invalid key", func() error {
			_, _, err := corrupt.Get(ctx, "../x")
			return err
		}, `invalid secret key "../x"`, `シークレットのキー "../x" が不正です`},
		{
			"not kagikae-encoded", func() error {
				_, _, err := corrupt.Get(ctx, "claude")
				return err
			}, "file entry claude is not kagikae-encoded: illegal base64 data at input byte 3",
			"file の項目 claude は kagikae の形式でエンコードされていません: illegal base64 data at input byte 3",
		},
		{
			"create secret dir", func() error {
				return fileBackend{dir: blocker}.Set(ctx, "claude", []byte("x"))
			}, "create secret dir: not a directory: " + blocker,
			"シークレットのディレクトリを作成できません: ディレクトリではありません: " + blocker,
		},
		{
			"keychain get", func() (err error) {
				runner.With(failing, func() { _, _, err = keychainBackend{}.Get(ctx, "claude") })
				return err
			}, "security find-generic-password failed (exit 1)",
			"security find-generic-password が失敗しました（終了コード 1）",
		},
		{
			"keychain set", func() (err error) {
				runner.With(failing, func() { err = keychainBackend{}.Set(ctx, "claude", []byte("x")) })
				return err
			}, "security add-generic-password failed (exit 1): boom",
			"security add-generic-password が失敗しました（終了コード 1）: boom",
		},
		{
			"keychain delete", func() (err error) {
				runner.With(failing, func() { err = keychainBackend{}.Delete(ctx, "claude") })
				return err
			}, "security delete-generic-password failed (exit 1)",
			"security delete-generic-password が失敗しました（終了コード 1）",
		},
		{"libsecret lookup", func() (err error) {
			runner.With(failing, func() { _, _, err = libsecretBackend{}.Get(ctx, "claude") })
			return err
		}, "secret-tool lookup failed (exit 1): boom", "secret-tool lookup が失敗しました（終了コード 1）: boom"},
		{"libsecret store", func() (err error) {
			runner.With(failing, func() { err = libsecretBackend{}.Set(ctx, "claude", []byte("x")) })
			return err
		}, "secret-tool store failed (exit 1): boom", "secret-tool store が失敗しました（終了コード 1）: boom"},
		{"libsecret clear", func() (err error) {
			runner.With(failing, func() { err = libsecretBackend{}.Delete(ctx, "claude") })
			return err
		}, "secret-tool clear failed (exit 1): boom", "secret-tool clear が失敗しました（終了コード 1）: boom"},
		{"libsecret search", func() (err error) {
			runner.With(failing, func() { _, err = libsecretBackend{}.Keys(ctx) })
			return err
		}, "secret-tool search failed (exit 1): boom", "secret-tool search が失敗しました（終了コード 1）: boom"},
	}
	l10ntest.UseJapanese(t)
	for _, tc := range cases {
		l10ntest.ErrorText(t, tc.name, tc.err(), tc.english, tc.ja, false)
	}
	_, err := Resolve(BackendAuto, "linux", dir, lookPathMissing)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("errors.Is must still find ErrUnavailable under Japanese: %v", err)
	}
}
