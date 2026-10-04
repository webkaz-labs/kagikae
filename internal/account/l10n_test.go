package account

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/testutil/l10ntest"
)

// The snapshot metadata's errors are message values: Error() keeps the English
// text, and a human sink renders the catalog's Japanese before the external cause.
func TestAccountErrorsRenderInJapanese(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(metaFile(dir), []byte("version = ["), 0o600); err != nil {
		t.Fatal(err)
	}
	l10ntest.UseJapanese(t)
	_, _, err := Load(dir)
	l10ntest.ErrorText(t, "parse", err, "parse "+metaFile(dir)+": ", metaFile(dir)+" を解析できません: ", true)
	err = Save(filepath.Join(metaFile(dir), "main"), Account{Tool: "claude", Name: "main"})
	l10ntest.ErrorText(t, "create dir", err, "create account dir: ", "アカウントのディレクトリを作成できません: ", true)
}
