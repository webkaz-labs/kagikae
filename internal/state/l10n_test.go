package state

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/testutil/l10ntest"
)

// The state file's errors are message values: Error() keeps the English text,
// and a human sink renders the catalog's Japanese before the external cause.
func TestStateErrorsRenderInJapanese(t *testing.T) {
	dir := t.TempDir()
	malformed := filepath.Join(dir, "state.json")
	if err := os.WriteFile(malformed, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	l10ntest.UseJapanese(t)
	_, err := Load(dir)
	l10ntest.ErrorText(t, "read", err, "read state: ", "状態ファイルを読み取れません: ", true)
	_, err = Load(malformed)
	l10ntest.ErrorText(t, "parse", err, "parse state: ", "状態ファイルを解析できません: ", true)
	err = Save(filepath.Join(malformed, "sub", "state.json"), New())
	l10ntest.ErrorText(t, "create dir", err, "create state dir: ", "状態ファイルのディレクトリを作成できません: ", true)
}
