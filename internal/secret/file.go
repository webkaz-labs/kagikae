package secret

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/webkaz-labs/kagikae/internal/patch"
)

// fileBackend is the explicit opt-in plaintext store. Files are 0600 under
// 0700 directories; doctor warns permanently while this backend is active.
type fileBackend struct {
	dir string
	// syncDir is an optional test seam for errors after a filesystem mutation.
	syncDir func(string) error
}

func (fileBackend) Name() string { return BackendFile }

func (b fileBackend) path(key string) string {
	return filepath.Join(b.dir, filepath.FromSlash(key)+".secret")
}

func (b fileBackend) Get(_ context.Context, key string) ([]byte, bool, error) {
	if err := validateKey(key); err != nil {
		return nil, false, err
	}
	data, err := os.ReadFile(b.path(key))
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	value, err := decodePayload(BackendFile, key, string(data))
	if err != nil {
		return nil, false, err
	}
	return value, true, nil
}

func (b fileBackend) Set(_ context.Context, key string, value []byte) error {
	if err := validateKey(key); err != nil {
		return err
	}
	path := b.path(key)
	if err := patch.MkdirAllDurable(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create secret dir: %w", err)
	}
	if err := patch.WriteFileAtomic(path, []byte(encodePayload(value)), 0o600); err != nil {
		return err
	}
	return b.syncParents(path)
}

func (b fileBackend) Delete(_ context.Context, key string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	path := b.path(key)
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	// An absent file can be a retry after an unlink whose directory sync failed.
	return b.syncParents(path)
}

// syncParents acknowledges the containing directory mutation, including an
// idempotent retry after an unlink whose sync failed.
func (b fileBackend) syncParents(path string) error {
	dir := filepath.Dir(path)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	sync := b.syncDir
	if sync == nil {
		sync = patch.SyncDir
	}
	return sync(dir)
}

// Keys lists every stored key by walking the secrets dir for *.secret files and
// mapping each path back to its slash-separated key. A missing dir is no keys.
func (b fileBackend) Keys(_ context.Context) ([]string, error) {
	var keys []string
	err := filepath.WalkDir(b.dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".secret") {
			return nil
		}
		rel, err := filepath.Rel(b.dir, path)
		if err != nil {
			return err
		}
		keys = append(keys, filepath.ToSlash(strings.TrimSuffix(rel, ".secret")))
		return nil
	})
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return keys, nil
}
