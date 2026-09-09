package commandrun

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestInputEnvironmentAndExit(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PROBE_SECRET", "must-not-inherit")
	result, err := (Command{Name: "/bin/sh", Args: []string{"-c", `printf '%s:' "${PROBE_SECRET-unset}"; cat; printf diagnostic >&2; exit 7`}, Env: []string{"HOME=" + root, "PATH=/usr/bin:/bin"}, Dir: root, Stdin: "fixture input", Timeout: time.Second}).Run(context.Background())
	if err != nil || result.ExitCode != 7 || result.Stdout != "unset:fixture input" || result.Stderr != "diagnostic" {
		t.Fatalf("result %#v, error %v", result, err)
	}
}

func TestLaunchAndCanceledContextAreNotOrdinaryExits(t *testing.T) {
	root := t.TempDir()
	spec := Command{Name: filepath.Join(root, "missing"), Env: []string{"HOME=" + root}, Dir: root, Timeout: time.Second}
	if _, err := spec.Run(context.Background()); err == nil {
		t.Fatal("missing executable accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	spec.Name = "/bin/echo"
	if _, err := spec.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled context: %v", err)
	}
	spec.Timeout = 0
	if _, err := spec.Run(context.Background()); err == nil {
		t.Fatal("unbounded command accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}
