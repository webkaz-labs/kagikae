package patch

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultSyncFileSyncs(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "x"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	// A real sync on a closed file fails; a no-op default would return nil.
	if err := SyncFile(f); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("default SyncFile on a closed file = %v, want os.ErrClosed", err)
	}
}

func TestWriteFileAtomicSyncFailureKeepsDestination(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "cred")
	if err := os.WriteFile(dest, []byte("old"), CredentialFileMode); err != nil {
		t.Fatal(err)
	}
	saved := SyncFile
	t.Cleanup(func() { SyncFile = saved })
	SyncFile = func(*os.File) error { return errors.New("injected sync failure") }

	err := WriteFileAtomic(dest, []byte("new"), CredentialFileMode)
	if err == nil || !strings.Contains(err.Error(), "sync temp file") {
		t.Fatalf("WriteFileAtomic error = %v, want one containing %q", err, "sync temp file")
	}
	got, readErr := os.ReadFile(dest)
	if readErr != nil || string(got) != "old" {
		t.Fatalf("destination = %q, %v; want previous content", got, readErr)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "cred" {
		t.Fatalf("directory has leftovers: %v", entries)
	}
}
