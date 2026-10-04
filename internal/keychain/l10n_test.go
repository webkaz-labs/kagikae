package keychain

import (
	"context"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/runner"
	"github.com/webkaz-labs/kagikae/internal/testutil/l10ntest"
	"github.com/webkaz-labs/kagikae/internal/testutil/runnertest"
)

// A failed `security` call is a message value: Error() keeps the English text,
// and a human sink renders the catalog's Japanese with the command, the item
// names, the exit code and the stderr snippet verbatim.
func TestSecurityFailuresRenderInJapanese(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name    string
		call    func() error
		english string
		ja      string
	}{
		{
			"read", func() error { _, _, err := ReadItem(ctx, "svc"); return err },
			`security find-generic-password "svc" failed (exit 1)`,
			`security find-generic-password "svc" が失敗しました（終了コード 1）`,
		},
		{
			"exists for account", func() error { _, err := ItemExistsForAccount(ctx, "svc", "main"); return err },
			`security find-generic-password "svc" (account "main") failed (exit 1)`,
			`security find-generic-password "svc"（アカウント "main"）が失敗しました（終了コード 1）`,
		},
		{
			"item account", func() error { _, _, err := ItemAccount(ctx, "svc"); return err },
			`security find-generic-password "svc" failed (exit 1)`,
			`security find-generic-password "svc" が失敗しました（終了コード 1）`,
		},
		{
			"write", func() error { return WriteItem(ctx, "svc", "main", []byte("x")) },
			`security add-generic-password "svc" failed (exit 1): boom`,
			`security add-generic-password "svc" が失敗しました（終了コード 1）: boom`,
		},
		{
			"delete", func() error { return DeleteItem(ctx, "svc") },
			`security delete-generic-password "svc" failed (exit 1): boom`,
			`security delete-generic-password "svc" が失敗しました（終了コード 1）: boom`,
		},
	}
	l10ntest.UseJapanese(t)
	for _, tc := range cases {
		var err error
		runner.With(&runnertest.Fake{Stderr: "boom\n", Code: 1}, func() { err = tc.call() })
		l10ntest.ErrorText(t, tc.name, err, tc.english, tc.ja, false)
	}
}
