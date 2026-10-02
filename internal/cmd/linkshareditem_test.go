package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// linkSharedItem has four outcomes at the destination: nothing there (link it), a stale
// symlink (replace it), a correct symlink (leave it), and a real file or directory (a
// private override, left alone); a stat that fails for another reason is an error.
func TestLinkSharedItem(t *testing.T) {
	t.Run("absent destination is linked", func(t *testing.T) {
		dir := t.TempDir()
		src, dst := filepath.Join(dir, "src"), filepath.Join(dir, "dst")
		if err := linkSharedItem(src, dst); err != nil {
			t.Fatal(err)
		}
		if got, err := os.Readlink(dst); err != nil || got != src {
			t.Fatalf("want link to %s, got %q (%v)", src, got, err)
		}
	})
	t.Run("stale symlink is replaced", func(t *testing.T) {
		dir := t.TempDir()
		src, dst := filepath.Join(dir, "src"), filepath.Join(dir, "dst")
		if err := os.Symlink(filepath.Join(dir, "old"), dst); err != nil {
			t.Fatal(err)
		}
		if err := linkSharedItem(src, dst); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.Readlink(dst); got != src {
			t.Fatalf("stale link must point at %s, got %q", src, got)
		}
	})
	t.Run("correct symlink is left", func(t *testing.T) {
		dir := t.TempDir()
		src, dst := filepath.Join(dir, "src"), filepath.Join(dir, "dst")
		if err := os.Symlink(src, dst); err != nil {
			t.Fatal(err)
		}
		before, err := os.Lstat(dst)
		if err != nil {
			t.Fatal(err)
		}
		if err := linkSharedItem(src, dst); err != nil {
			t.Fatal(err)
		}
		after, err := os.Lstat(dst)
		if err != nil || !os.SameFile(before, after) {
			t.Fatalf("an already-correct link must not be recreated: %v", err)
		}
	})
	t.Run("real file is a private override", func(t *testing.T) {
		dir := t.TempDir()
		src, dst := filepath.Join(dir, "src"), filepath.Join(dir, "dst")
		if err := os.WriteFile(dst, []byte("mine"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := linkSharedItem(src, dst); err != nil {
			t.Fatal(err)
		}
		if got, err := os.ReadFile(dst); err != nil || string(got) != "mine" {
			t.Fatalf("a real file must be left alone: %q (%v)", got, err)
		}
	})
	t.Run("stat failure is reported", func(t *testing.T) {
		dir := t.TempDir()
		blocker := filepath.Join(dir, "file")
		if err := os.WriteFile(blocker, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		// A path below a regular file fails Lstat with ENOTDIR, not ENOENT.
		err := linkSharedItem(filepath.Join(dir, "src"), filepath.Join(blocker, "dst"))
		if err == nil || !strings.Contains(err.Error(), "stat link item") {
			t.Fatalf("want a stat link item error, got %v", err)
		}
	})
}
