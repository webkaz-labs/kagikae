package envprofile

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/testutil/l10ntest"
	"github.com/webkaz-labs/kagikae/internal/testutil/secrettest"
)

// The env profile's errors are message values: Error() keeps the English text,
// and a human sink renders the catalog's Japanese before the external cause.
func TestEnvProfileErrorsRenderInJapanese(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	if err := os.WriteFile(metaFile(dir), []byte("vars = ["), 0o600); err != nil {
		t.Fatal(err)
	}
	profile := Profile{Tool: "claude", Account: "main", Vars: []string{"API_KEY"}}
	failing := secrettest.FailingBackend{MemBackend: secrettest.NewMem(), GetErr: secrettest.ErrBackendDown, DeleteErr: secrettest.ErrBackendDown}
	l10ntest.UseJapanese(t)

	_, _, err := Load(dir)
	l10ntest.ErrorText(t, "parse", err, "parse "+metaFile(dir)+": ", metaFile(dir)+" を解析できません: ", true)
	err = Save(filepath.Join(metaFile(dir), "sub"), profile)
	l10ntest.ErrorText(t, "create dir", err, "create env profile dir: ", "環境変数プロファイルのディレクトリを作成できません: ", true)
	_, err = EnvStrings(ctx, secrettest.NewMem(), profile)
	l10ntest.ErrorText(t, "missing value", err, "env value API_KEY is missing from the secret store; run: kae env set",
		"環境変数 API_KEY の値がシークレットストアにありません。kae env set を実行してください", false)
	_, err = EnvStrings(ctx, failing, profile)
	l10ntest.ErrorText(t, "read value", err, "read env value API_KEY: backend down",
		"環境変数 API_KEY の値を読み取れません: backend down", false)
	err = Delete(ctx, failing, dir, profile)
	l10ntest.ErrorText(t, "delete value", err, "delete env value API_KEY: backend down",
		"環境変数 API_KEY の値を削除できません: backend down", false)
}
