// Command installverify exercises the actual shell installer with non-forwarding
// release fixtures. Run through the Installer compatibility smoke, never directly.
package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/webkaz-labs/kagikae/tools/devtools/distribution"

	"github.com/webkaz-labs/kagikae/internal/installation"
	"github.com/webkaz-labs/kagikae/internal/lock"
	"github.com/webkaz-labs/kagikae/internal/paths"
	"github.com/webkaz-labs/kagikae/internal/runner"
)

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

func write(path, data string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(data), mode)
}

func archive(root, tag, platform string) error {
	name := fmt.Sprintf("kae_%s_%s.tar.gz", tag[1:], platform)
	path := filepath.Join(root, name)
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	z := gzip.NewWriter(f)
	w := tar.NewWriter(z)
	data := []byte("#!/bin/sh\ncase \"$1\" in version) echo 'kae " + tag + "';; completion) exit 0;; __install) exit 37;; *) exit 90;; esac\n")
	if err := w.WriteHeader(&tar.Header{Name: "kae", Mode: 0o755, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	if err := errors.Join(w.Close(), z.Close(), f.Close()); err != nil {
		return err
	}
	data, err = os.ReadFile(path)
	if err != nil {
		return err
	}
	return write(filepath.Join(root, "checksums.txt"), fmt.Sprintf("%x  %s\n", sha256.Sum256(data), name), 0o600)
}

func check() error {
	if len(os.Args) == 3 && os.Args[1] == "--probe-lock" {
		release, err := installation.Acquire(os.Args[2])
		if release != nil {
			_ = release()
		}
		if !errors.Is(err, lock.ErrBusy) {
			return fmt.Errorf("shell-held installation lock did not exclude Go: %v", err)
		}
		return nil
	}
	home := os.Getenv("HOME")
	if !strings.HasPrefix(filepath.Base(home), "kae-smoke-run.") {
		return errors.New("run via bash scripts/smoke-run.sh '## Installer compatibility smoke'")
	}
	repo, err := os.Getwd()
	if err != nil {
		return err
	}
	root, err := os.MkdirTemp(home, "installer-fixtures-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	shim := filepath.Join(root, "shim")
	assetDir := filepath.Join(root, "assets")
	if err := os.MkdirAll(assetDir, 0o700); err != nil {
		return err
	}
	destination := filepath.Join(root, "direct", "kae")
	// This shim runs only after the real installer has acquired its lock. It
	// asks the Go implementation to acquire that same destination before copying.
	install := "#!/bin/sh\nset -eu\n" + quote(self) + " --probe-lock " + quote(destination) + "\nexec /usr/bin/install \"$@\"\n"
	if err := write(filepath.Join(shim, "install"), install, 0o700); err != nil {
		return err
	}
	env := []string{}
	for _, item := range os.Environ() {
		if !strings.HasPrefix(item, "PATH=") && !strings.HasPrefix(item, "KAE_REPO=") {
			env = append(env, item)
		}
	}
	env = append(env, "PATH="+shim+":"+os.Getenv("PATH"), "KAE_REPO=webkaz-labs/kagikae")
	platform := runtime.GOOS + "_" + runtime.GOARCH
	var stderr string
	run := func(tag string, want int) error {
		files := map[string]string{}
		for _, name := range []string{"checksums.txt", fmt.Sprintf("kae_%s_%s.tar.gz", strings.TrimPrefix(tag, "v"), platform)} {
			asset := filepath.Join(assetDir, name)
			// Invalid-version controls are refused before the transport is used.
			if _, err := os.Stat(asset); err == nil {
				files["https://github.com/webkaz-labs/kagikae/releases/download/"+tag+"/"+name] = asset
			}
		}
		curl, err := distribution.CurlFixture(files)
		if err != nil {
			return err
		}
		if err := write(filepath.Join(shim, "curl"), curl, 0o700); err != nil {
			return err
		}

		var out string
		var code int
		out, stderr, code = runner.RunWithEnv(context.Background(), env, "sh", filepath.Join(repo, "scripts/install.sh"), "--version", tag, "--install-dir", filepath.Dir(destination))
		if code != want {
			return fmt.Errorf("installer %s returned %d, want %d: %s %s", tag, code, want, out, stderr)
		}
		return nil
	}
	for _, tag := range []string{"v0.20.3", "v0.20.2"} {
		if err := archive(assetDir, tag, platform); err != nil {
			return err
		}
		if err := run(tag, 0); err != nil {
			return err
		}
	}
	before, err := os.ReadFile(destination)
	if err != nil {
		return err
	}
	unmodified := func() error {
		got, err := os.ReadFile(destination)
		if err != nil || string(got) != string(before) {
			return errors.New("refused installer changed the destination")
		}
		return nil
	}
	receiptRoot := paths.Resolve(os.Getenv, home).InstallationsDir()
	receipt := installation.ReceiptPath(receiptRoot, destination)
	if err := write(receipt, "retained fixture receipt", 0o600); err != nil {
		return err
	}
	release, err := installation.Acquire(destination)
	if err != nil {
		return err
	}
	err = run("v0.20.2", 4)
	err = errors.Join(err, release(), unmodified())
	if err != nil {
		return err
	}
	if err := run("v0.20.2", 10); err != nil {
		return err
	}
	if got, err := os.ReadFile(receipt); err != nil || string(got) != "retained fixture receipt" {
		return errors.New("refused installer changed the receipt")
	}
	if err := os.Remove(receipt); err != nil {
		return err
	}
	history := filepath.Join(receiptRoot, "history", strings.TrimSuffix(filepath.Base(receipt), ".json"))
	if err := os.MkdirAll(history, 0o700); err != nil {
		return err
	}
	if err := run("v0.20.2", 10); err != nil {
		return err
	}
	if err := archive(assetDir, "v0.21.0", platform); err != nil {
		return err
	}
	if err := run("v0.21.0", 37); err != nil {
		return err
	}
	if err := run("v0.21.0-invalid", 2); err != nil {
		return err
	}
	// Under a faked uname, v0.24.0 still installs on linux/amd64 and darwin/arm64,
	// so the Intel macOS guard refuses neither by architecture nor by OS alone.
	// The archive is a shell script, so any host runs it.
	fakeUname := func(system, machine string) error {
		return write(filepath.Join(shim, "uname"), "#!/bin/sh\ncase \"$1\" in -s) echo "+system+";; -m) echo "+machine+";; *) exit 90;; esac\n", 0o700)
	}
	for _, host := range [][3]string{{"Linux", "x86_64", "linux_amd64"}, {"Darwin", "arm64", "darwin_arm64"}} {
		if err := fakeUname(host[0], host[1]); err != nil {
			return err
		}
		platform = host[2]
		if err := archive(assetDir, "v0.24.0", platform); err != nil {
			return err
		}
		if err := run("v0.24.0", 37); err != nil {
			return fmt.Errorf("%s: %w", platform, err)
		}
	}
	// Intel macOS: tags before v0.24.0 still install their darwin/amd64 archive;
	// later tags are refused by name before any download.
	if err := fakeUname("Darwin", "x86_64"); err != nil {
		return err
	}
	platform = "darwin_amd64"
	if err := archive(assetDir, "v0.23.0", platform); err != nil {
		return err
	}
	if err := run("v0.23.0", 37); err != nil {
		return err
	}
	if err := run("v0.24.0", 1); err != nil {
		return err
	}
	if !strings.Contains(stderr, "Intel macOS (darwin/amd64) is unsupported from v0.24.0; v0.24.0 has no darwin/amd64 archive (--version v0.23.0 installs the last one)") {
		return fmt.Errorf("darwin/amd64 v0.24.0 refused for another reason: %s", stderr)
	}
	if err := os.Remove(filepath.Join(shim, "uname")); err != nil {
		return err
	}
	return unmodified()
}

func main() {
	if err := check(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("installer compatibility: passed")
}
