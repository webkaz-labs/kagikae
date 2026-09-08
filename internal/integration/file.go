// Package integration provides content-checked mutations of owned integration files.
// Callers establish ownership and hold the relevant writer lock before Apply.
package integration

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/webkaz-labs/kagikae/internal/patch"
)

var ErrChanged = errors.New("integration changed; preview again")

var ErrUnsafe = errors.New("integration is not a supported regular file")

// File retains the observed identity and bytes; none of its contents is a report.
type File struct {
	Path    string
	Content []byte
	Exists  bool
	info    os.FileInfo
	parent  os.FileInfo
	realDir string
}

func Read(path string) (File, error) {
	f := File{Path: path}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return f, nil
	}
	if err != nil {
		return f, err
	}
	if !info.Mode().IsRegular() {
		return f, ErrUnsafe
	}
	f.info, f.Exists = info, true
	f.realDir, err = filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return f, err
	}
	f.parent, err = os.Stat(f.realDir)
	if err != nil {
		return f, err
	}
	h, err := os.Open(path)
	if err != nil {
		return f, err
	}
	defer h.Close()
	opened, err := h.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return f, ErrChanged
	}
	f.Content, err = io.ReadAll(io.LimitReader(h, 1024*1024+1))
	if err != nil {
		return f, err
	}
	if len(f.Content) > 1024*1024 {
		return f, ErrUnsafe
	}
	return f, nil
}

func (f File) Recheck() error {
	current, err := Read(f.Path)
	if err != nil {
		return err
	}
	if f.Exists != current.Exists || !bytes.Equal(f.Content, current.Content) {
		return ErrChanged
	}
	if f.Exists && (f.realDir != current.realDir || !os.SameFile(f.info, current.info) || !os.SameFile(f.parent, current.parent)) {
		return ErrChanged
	}
	return nil
}

// Apply preserves nonempty remainder bytes, or removes the exact observed file.
// It never removes parent directories or follows a file symlink.
func (f File) Apply(remainder []byte) error {
	if err := f.Recheck(); err != nil {
		return err
	}
	if !f.Exists || bytes.Equal(f.Content, remainder) {
		return nil
	}
	if len(remainder) == 0 {
		return os.Remove(f.Path)
	}
	return patch.WriteFileAtomic(f.Path, remainder, f.info.Mode().Perm())
}
