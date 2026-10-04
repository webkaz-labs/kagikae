package backup

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/testutil/l10ntest"
	"github.com/webkaz-labs/kagikae/internal/testutil/secrettest"
)

// failingDelete is a secret backend whose Delete fails with an external error.
type failingDelete struct{ *secrettest.MemBackend }

func (failingDelete) Delete(context.Context, string) error { return errors.New("backend down") }

// The backup store's errors are message values: Error() keeps the English text,
// and a human sink renders the catalog's Japanese before the external cause.
func TestBackupErrorsRenderInJapanese(t *testing.T) {
	dir := t.TempDir()
	const id = "20260101T000000Z"
	if err := os.WriteFile(metaPath(dir, id), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	l10ntest.UseJapanese(t)
	_, err := Get(dir, id)
	l10ntest.ErrorText(t, "parse", err, "parse backup "+id+": ", "バックアップ "+id+" を解析できません: ", true)
	err = Save(metaPath(dir, id), Meta{ID: "x"})
	l10ntest.ErrorText(t, "create dir", err, "create backups dir: ", "バックアップのディレクトリを作成できません: ", true)
	meta := Meta{ID: "x", Artifacts: []ArtifactRecord{{SecretRef: "backup/x/claude/oauth", Present: true}}}
	err = Delete(context.Background(), failingDelete{secrettest.NewMem()}, dir, meta)
	l10ntest.ErrorText(t, "delete payload", err, "delete backup payload backup/x/claude/oauth: backend down",
		"バックアップの保存データ backup/x/claude/oauth を削除できません: backend down", false)
}
