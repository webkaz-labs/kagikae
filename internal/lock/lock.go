//go:build !windows

// Package lock provides non-blocking per-tool advisory file locks. A busy
// lock fails fast instead of queueing so a waiting switch cannot interleave
// with another process's restore step.
package lock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// ErrBusy is returned when another process holds the lock.
var ErrBusy = errors.New("lock busy")

// Lock is a held advisory lock.
type Lock struct {
	file *os.File
}

// Acquire takes the lock named name under dir, creating dir if needed.
// It returns ErrBusy without blocking when the lock is already held.
func Acquire(dir, name string) (*Lock, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create lock dir: %w", err)
	}
	path := filepath.Join(dir, name+".lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrBusy
		}
		return nil, fmt.Errorf("flock: %w", err)
	}
	return &Lock{file: f}, nil
}

// Release drops the lock. Safe to call once.
func (l *Lock) Release() {
	if l == nil || l.file == nil {
		return
	}
	syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	l.file.Close()
	l.file = nil
}
