package patch

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// CredentialFileMode is the mode for newly created credential files.
const CredentialFileMode fs.FileMode = 0o600

// SyncFile flushes a written temp file before it is renamed into place. It is a
// variable only so a test binary can replace it; production code never
// reassigns it. A test cannot observe power-loss durability, so a no-op
// replacement does not weaken what a test proves.
var SyncFile = func(f *os.File) error { return f.Sync() }

// WriteFileAtomic writes data via a temp file + rename in the same directory.
// The given mode is always enforced, even when the destination existed with
// looser permissions (credential files must end up 0600). Parent directories
// are not created implicitly.
func WriteFileAtomic(path string, data []byte, mode fs.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := SyncFile(tmp); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename temp file: %w", err)
	}
	return nil
}
