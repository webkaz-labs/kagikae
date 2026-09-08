package integration

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestApplyRefusesChangedFileOrParent(t *testing.T) {
	for _, change := range []string{"content", "inode", "parent", "symlink"} {
		t.Run(change, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "config")
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "integration.toml")
			if err := os.WriteFile(path, []byte("owned"), 0o600); err != nil {
				t.Fatal(err)
			}
			f, err := Read(path)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "content":
				err = os.WriteFile(path, []byte("custom"), 0o600)
			case "inode":
				if err := os.Rename(path, path+".saved"); err != nil {
					t.Fatal(err)
				}
				err = os.WriteFile(path, []byte("owned"), 0o600)
			case "parent":
				if err := os.Rename(dir, dir+".saved"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				err = os.Rename(filepath.Join(dir+".saved", "integration.toml"), path)
			case "symlink":
				if err := os.Rename(path, path+".saved"); err != nil {
					t.Fatal(err)
				}
				err = os.Symlink(path+".saved", path)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := f.Apply(nil); !errors.Is(err, ErrChanged) && !errors.Is(err, ErrUnsafe) {
				t.Fatalf("changed target accepted: %v", err)
			}
			if _, err := os.Lstat(path); err != nil {
				t.Fatalf("changed target was removed: %v", err)
			}
		})
	}
}

func TestApplyRetainsUnrelatedBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "integration.toml")
	if err := os.WriteFile(path, []byte("owned\n# user comment\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	f, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Apply([]byte("# user comment\n")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "# user comment\n" {
		t.Fatalf("remainder changed: %q %v", data, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("permissions changed: %v", err)
	}
}
