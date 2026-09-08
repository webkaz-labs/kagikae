package cmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/constants"
)

func TestInitHonorsConfigLock(t *testing.T) {
	app := testApp(t, nil)
	l, err := app.acquireConfigLock()
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	code, out := captureStdout(t, func() int {
		return runInit(context.Background(), app, commonOpts{Format: formatJSON})
	})
	mustExit(t, constants.ExitLockBusy, code, out)
	if _, err := os.Stat(app.ConfigPath); !os.IsNotExist(err) {
		t.Fatalf("busy init created config: %v", err)
	}
	l.Release()
	code, out = captureStdout(t, func() int {
		return runInit(context.Background(), app, commonOpts{Format: formatJSON})
	})
	mustExit(t, constants.ExitOK, code, out)
}

func TestInitRevalidatesExistingConfig(t *testing.T) {
	for _, content := range []string{"version = [", "version = 999\n"} {
		t.Run(content, func(t *testing.T) {
			app := testApp(t, nil)
			writeFile(t, app.ConfigPath, content)
			code, out := captureStdout(t, func() int {
				return runInit(context.Background(), app, commonOpts{Format: formatJSON})
			})
			mustExit(t, constants.ExitInvalidConfig, code, out)
			if got := readFile(t, app.ConfigPath); got != content {
				t.Fatal("init changed invalid configuration")
			}
		})
	}
}

func TestInitRefusesNonRegularConfig(t *testing.T) {
	for _, kind := range []string{"directory", "dangling_symlink"} {
		t.Run(kind, func(t *testing.T) {
			app := testApp(t, nil)
			if err := os.MkdirAll(filepath.Dir(app.ConfigPath), 0o700); err != nil {
				t.Fatal(err)
			}
			var err error
			if kind == "directory" {
				err = os.Mkdir(app.ConfigPath, 0o700)
			} else {
				err = os.Symlink(filepath.Join(t.TempDir(), "missing"), app.ConfigPath)
			}
			if err != nil {
				t.Fatal(err)
			}
			before, err := os.Lstat(app.ConfigPath)
			if err != nil {
				t.Fatal(err)
			}
			code, out := captureStdout(t, func() int {
				return runInit(context.Background(), app, commonOpts{Format: formatJSON})
			})
			if code == constants.ExitOK {
				t.Fatalf("accepted non-regular config: %s", out)
			}
			after, err := os.Lstat(app.ConfigPath)
			if err != nil || !os.SameFile(before, after) {
				t.Fatalf("init replaced existing config: %v", err)
			}
		})
	}
}
