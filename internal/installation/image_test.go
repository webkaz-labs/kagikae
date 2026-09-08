package installation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/runner"
)

func TestRunningImageIdentity(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(executable)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyRunningImage(info); err != nil {
		t.Fatalf("running binary rejected: %v", err)
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(t.TempDir(), "image test")
	if err := os.WriteFile(copyPath, data, 0o700); err != nil {
		t.Fatal(err)
	}
	copyInfo, err := os.Stat(copyPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyRunningImage(copyInfo); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("same bytes in another inode accepted: %v", err)
	}
	out, stderr, code := runner.RunWithEnv(context.Background(),
		[]string{"KAE_TEST_IMAGE_REPLACEMENT=" + copyPath}, copyPath,
		"-test.run=^TestRunningImageReplacementChild$", "-test.count=1")
	if code != 0 {
		t.Fatalf("replacement child failed (%d): %s %s", code, out, stderr)
	}
}

func TestRunningImageReplacementChild(t *testing.T) {
	path := os.Getenv("KAE_TEST_IMAGE_REPLACEMENT")
	if path == "" {
		t.Skip("replacement runs in a copy of the test executable")
	}
	executable, err := os.Executable()
	if err != nil || executable != path {
		t.Fatalf("unexpected executable: %q %v", executable, err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyRunningImage(before); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	replacement := path + ".replacement"
	if err := os.WriteFile(replacement, data, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyRunningImage(after); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("replacement at own path accepted: %v", err)
	}
	if err := VerifyRunningImage(before); err != nil {
		t.Fatalf("kernel lost original image identity: %v", err)
	}
}
