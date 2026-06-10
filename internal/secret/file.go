package secret

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/webkaz-labs/kagikae/internal/patch"
)

// fileBackend is the explicit opt-in plaintext store. Files are 0600 under
// 0700 directories; doctor warns permanently while this backend is active.
type fileBackend struct {
	dir string
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
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create secret dir: %w", err)
	}
	return patch.WriteFileAtomic(path, []byte(encodePayload(value)), 0o600)
}

func (b fileBackend) Delete(_ context.Context, key string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	err := os.Remove(b.path(key))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
