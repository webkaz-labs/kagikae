package portable

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/scripts/internal/commandrun"
)

// The fake validates cwd, environment and argv without downloading analyzers.
// Actual formatter behavior remains owned by the pinned tools.
func TestForeignFormatter(t *testing.T) {
	source, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	target := filepath.Join(root, "foreign module")
	bin := filepath.Join(root, "bin")
	for _, dir := range []string{target, bin} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, body string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(target, "go.mod"), "module example.com/fixture\n", 0o600)
	write(filepath.Join(bin, "go"), `#!/bin/sh
set -eu
[ "$PWD" = "$FORMAT_TARGET" ] || exit 91
[ "$GOWORK" = off ] || exit 92
[ -z "$GOFLAGS" ] || exit 93
case "$*" in
  'list -m') printf 'example.com/fixture\n' ;;
  'run mvdan.cc/gofumpt@v0.10.0 -l .')
    if [ "$FORMAT_CASE" = tool-error ]; then exit 17; fi
    if [ "$FORMAT_CASE" = format ]; then printf 'main.go\n'; fi ;;
  'run golang.org/x/tools/cmd/goimports@v0.46.0 -local example.com/fixture -l .')
    if [ "$FORMAT_CASE" = imports ]; then printf 'main.go\n'; fi ;;
  *) printf 'unexpected argv: %s\n' "$*" >&2; exit 94 ;;
esac
`, 0o700)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FORMAT_TARGET", target)
	t.Setenv("GOWORK", filepath.Join(root, "missing.work"))
	t.Setenv("GOFLAGS", "-invalid")
	t.Setenv("GOCLI_LINT_CACHE_DIR", filepath.Join(root, "cache"))
	for _, tc := range []struct {
		name, want string
		success    bool
	}{
		{"clean", "go format check: ok", true},
		{"format", "run gofumpt", false},
		{"imports", "run goimports -local example.com/fixture", false},
		{"tool-error", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FORMAT_CASE", tc.name)
			result, err := (commandrun.Command{Name: "bash", Args: []string{filepath.Join(source, "scripts/check-go-format.sh"), "--portable", target}, Dir: root, Timeout: time.Minute}).Run(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if (result.ExitCode == 0) != tc.success || !strings.Contains(result.Stdout+result.Stderr, tc.want) {
				t.Fatalf("exit %d: %s%s", result.ExitCode, result.Stdout, result.Stderr)
			}
		})
	}
	if err := os.Remove(filepath.Join(target, "go.mod")); err != nil {
		t.Fatal(err)
	}
	result, err := (commandrun.Command{Name: "bash", Args: []string{filepath.Join(source, "scripts/check-go-format.sh"), "--portable", target}, Dir: root, Timeout: time.Minute}).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode == 0 || !strings.Contains(result.Stderr, "target must contain go.mod") {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Stderr)
	}
}
