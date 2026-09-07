package patch

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestMkdirAllDurableRetriesFailedAncestor(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "new", "child")
	failed := errors.New("directory sync failed")
	fail := true
	syncDir := func(path string) error {
		if path == root && fail {
			return failed
		}
		return SyncDir(path)
	}
	if err := mkdirAllDurable(target, 0o700, syncDir); !errors.Is(err, failed) {
		t.Fatalf("save error = %v", err)
	}
	if _, err := os.Stat(filepath.Dir(target)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("descended before ancestor sync: %v", err)
	}
	if err := mkdirAllDurable(target, 0o700, syncDir); !errors.Is(err, failed) {
		t.Fatalf("retry bypassed failed sync: %v", err)
	}
	fail = false
	if err := mkdirAllDurable(target, 0o700, syncDir); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		t.Fatalf("target missing: %v", err)
	}
}

func TestMkdirAllDurableSymlinkRoot(t *testing.T) {
	root := t.TempDir()
	actual := filepath.Join(root, "actual")
	if err := os.Mkdir(actual, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(actual, link); err != nil {
		t.Fatal(err)
	}
	if err := MkdirAllDurable(filepath.Join(link, "new", "child"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(actual, "new", "child")); err != nil {
		t.Fatal(err)
	}
}
