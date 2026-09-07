package patch

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// SyncDir acknowledges directory entry mutations through the OS filesystem API.
// It does not promise portable power-loss guarantees beyond that acknowledgement.
func SyncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
}

// MkdirAllDurable creates directories top-down, syncing each entry's parent before
// descending. It also syncs the nearest existing directory's parent: that entry
// may remain from an earlier call whose sync failed. It does not walk and sync
// unrelated ancestors of an existing tree.
func MkdirAllDurable(path string, mode fs.FileMode) error {
	return mkdirAllDurable(path, mode, SyncDir)
}

func mkdirAllDurable(path string, mode fs.FileMode, syncDir func(string) error) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	var missing []string
	base := abs
	for {
		info, err := os.Stat(base)
		if err == nil {
			if !info.IsDir() {
				return fmt.Errorf("not a directory: %s", base)
			}
			break
		}
		if !os.IsNotExist(err) {
			return err
		}
		missing = append(missing, filepath.Base(base))
		parent := filepath.Dir(base)
		if parent == base {
			return err
		}
		base = parent
	}
	base, err = filepath.EvalSymlinks(base)
	if err != nil {
		return err
	}
	if err := syncDir(filepath.Dir(base)); err != nil {
		return err
	}
	for i := len(missing) - 1; i >= 0; i-- {
		next := filepath.Join(base, missing[i])
		if err := os.Mkdir(next, mode); err != nil {
			if !os.IsExist(err) {
				return err
			}
			info, statErr := os.Stat(next)
			if statErr != nil {
				return statErr
			}
			if !info.IsDir() {
				return fmt.Errorf("not a directory: %s", next)
			}
		}
		if err := syncDir(base); err != nil {
			return err
		}
		base = next
	}
	return nil
}
