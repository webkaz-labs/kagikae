//go:build !windows

package lock

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/l10n"
	"github.com/webkaz-labs/kagikae/internal/testutil/l10ntest"
)

// TestAcquireErrorsRenderInJapanese pins the lock errors twice: the English
// Error() with the OS cause, and the Japanese rendering that quotes the same
// cause verbatim. errors.Is and errors.As still reach the cause.
func TestAcquireErrorsRenderInJapanese(t *testing.T) {
	l10ntest.UseJapanese(t)
	t.Run("lock dir", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(dir, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := Acquire(dir, "claude")
		wantRendered(t, err, "create lock dir: ", "ロックのディレクトリを作成できません: ")
	})
	t.Run("lock file", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, "claude.lock"), 0o700); err != nil {
			t.Fatal(err)
		}
		_, err := Acquire(dir, "claude")
		wantRendered(t, err, "open lock file: ", "ロックファイルを開けません: ")
	})
	t.Run("busy", func(t *testing.T) {
		dir := t.TempDir()
		held, err := Acquire(dir, "claude")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Release()
		_, err = Acquire(dir, "claude")
		if !errors.Is(err, ErrBusy) {
			t.Fatalf("want ErrBusy, got %v", err)
		}
		if got, want := l10n.Render(err), "ロックが競合しています"; got != want {
			t.Errorf("Japanese: got %q, want %q", got, want)
		}
	})
}

func wantRendered(t *testing.T, err error, en, ja string) {
	t.Helper()
	var cause *fs.PathError
	if !errors.As(err, &cause) {
		t.Fatalf("the OS cause is not reachable: %v", err)
	}
	if got, want := err.Error(), en+cause.Error(); got != want {
		t.Errorf("English: got %q, want %q", got, want)
	}
	if got, want := l10n.Render(err), ja+cause.Error(); got != want {
		t.Errorf("Japanese: got %q, want %q", got, want)
	}
}
