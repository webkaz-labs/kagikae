//go:build !windows

package lock

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestAcquireRelease(t *testing.T) {
	dir := t.TempDir()
	l, err := Acquire(dir, "claude")
	if err != nil {
		t.Fatal(err)
	}
	l.Release()
	l2, err := Acquire(dir, "claude")
	if err != nil {
		t.Fatalf("re-acquire after release failed: %v", err)
	}
	l2.Release()
	l2.Release() // double release is safe
}

func TestDifferentNamesDoNotConflict(t *testing.T) {
	dir := t.TempDir()
	a, err := Acquire(dir, "claude")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Release()
	b, err := Acquire(dir, "codex")
	if err != nil {
		t.Fatalf("different lock name conflicted: %v", err)
	}
	b.Release()
}

// TestBusyAcrossProcesses verifies ErrBusy with a real second process,
// because flock is per-process and a same-process re-acquire would succeed.
func TestBusyAcrossProcesses(t *testing.T) {
	if os.Getenv("LOCK_TEST_CHILD") == "1" {
		dir := os.Getenv("LOCK_TEST_DIR")
		_, err := Acquire(dir, "claude")
		if errors.Is(err, ErrBusy) {
			os.Exit(42)
		}
		os.Exit(1)
	}
	dir := t.TempDir()
	l, err := Acquire(dir, "claude")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	cmd := exec.Command(os.Args[0], "-test.run", "TestBusyAcrossProcesses")
	cmd.Env = append(os.Environ(), "LOCK_TEST_CHILD=1", "LOCK_TEST_DIR="+dir)
	err = cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 42 {
		t.Fatalf("expected child exit 42 (busy), got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "claude.lock")); statErr != nil {
		t.Fatalf("lock file missing: %v", statErr)
	}
}

func TestSharedAndExclusiveCompatibilityAcrossProcesses(t *testing.T) {
	if mode := os.Getenv("LOCK_COMPAT_CHILD"); mode != "" {
		dir := os.Getenv("LOCK_TEST_DIR")
		var (
			l   *Lock
			err error
		)
		if mode == "shared" {
			l, err = AcquireShared(dir, "isolation-claude")
		} else {
			l, err = Acquire(dir, "isolation-claude")
		}
		if errors.Is(err, ErrBusy) {
			os.Exit(42)
		}
		if err != nil {
			os.Exit(1)
		}
		l.Release()
		os.Exit(0)
	}

	for _, tc := range []struct {
		name       string
		parentMode string
		childMode  string
		wantCode   int
	}{
		{name: "shared overlaps shared", parentMode: "shared", childMode: "shared", wantCode: 0},
		{name: "shared excludes exclusive", parentMode: "shared", childMode: "exclusive", wantCode: 42},
		{name: "exclusive excludes shared", parentMode: "exclusive", childMode: "shared", wantCode: 42},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			var (
				held *Lock
				err  error
			)
			if tc.parentMode == "shared" {
				held, err = AcquireShared(dir, "isolation-claude")
			} else {
				held, err = Acquire(dir, "isolation-claude")
			}
			if err != nil {
				t.Fatal(err)
			}
			defer held.Release()

			cmd := exec.Command(os.Args[0], "-test.run", "TestSharedAndExclusiveCompatibilityAcrossProcesses")
			cmd.Env = append(os.Environ(), "LOCK_COMPAT_CHILD="+tc.childMode, "LOCK_TEST_DIR="+dir)
			err = cmd.Run()
			if tc.wantCode == 0 {
				if err != nil {
					t.Fatalf("shared child should acquire: %v", err)
				}
				return
			}
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != tc.wantCode {
				t.Fatalf("expected child exit %d, got %v", tc.wantCode, err)
			}
		})
	}
}
