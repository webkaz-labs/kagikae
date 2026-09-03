//go:build !windows

// Package lock provides non-blocking advisory file locks. A busy lock fails
// fast instead of queueing so a waiting mutation cannot interleave with the
// operation holding it.
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
	return acquire(dir, name, syscall.LOCK_EX)
}

// AcquireShared takes a shared lock named name under dir. Shared holders may
// run concurrently, but conflict with Acquire on the same name.
func AcquireShared(dir, name string) (*Lock, error) {
	return acquire(dir, name, syscall.LOCK_SH)
}

func acquire(dir, name string, mode int) (*Lock, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create lock dir: %w", err)
	}
	path := filepath.Join(dir, name+".lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), mode|syscall.LOCK_NB); err != nil {
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
