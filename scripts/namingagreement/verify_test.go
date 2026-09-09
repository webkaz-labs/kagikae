package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/scripts/internal/commandrun"
)

func TestMain(m *testing.M) {
	if filepath.Base(os.Args[0]) == "security" {
		os.Exit(securityMode(os.Args[1:]))
	}
	if len(os.Args) == 2 && os.Args[1] == "observe" {
		observeMain()
		return
	}
	os.Exit(m.Run())
}

func repository(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Dir(filepath.Dir(cwd))
}

func TestComparisonControls(t *testing.T) {
	write := []string{"add-generic-password", "-s", "service", "-a", "main"}
	read := []string{"find-generic-password", "-s", "service", "-a", "main"}
	if err := compare(write, [][]string{read}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		write []string
		reads [][]string
	}{{write, nil}, {nil, [][]string{read}}, {write, [][]string{{"find-generic-password", "-s", "service", "-a", "side"}}}, {write, [][]string{{"find-generic-password", "-s", "wrong", "-a", "main"}}}, {write, [][]string{{"delete-generic-password"}}}, {write, [][]string{{"find-generic-password"}}}, {write, [][]string{{"find-generic-password", "-s", "service", "-a", "main", "-a", "side"}}}} {
		if err := compare(c.write, c.reads); err == nil {
			t.Fatal("invalid observation accepted")
		}
	}
}

func TestUnreviewedCopiesStayNonExecutable(t *testing.T) {
	root := t.TempDir()
	for i, body := range []string{"/usr/bin/security", "SecItemCopyMatching", "#!/bin/sh\nexit 0\n"} {
		source := filepath.Join(root, fmt.Sprint(i))
		if err := os.WriteFile(source, []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
		target := source + "-copy"
		if err := verifiedCopy(source, target); err == nil {
			t.Fatal("unreviewed bytes accepted")
		}
		info, err := os.Stat(target)
		if err != nil || info.Mode()&0o111 != 0 {
			t.Fatal("rejected copy executable", err)
		}
	}
	if err := verifiedCopy("", filepath.Join(root, "missing")); err == nil {
		t.Fatal("missing source")
	}
}

func TestCopiedBytesCheckedBeforeExecutable(t *testing.T) {
	root := t.TempDir()
	data := []byte("synthetic only")
	source := filepath.Join(root, "source")
	target := filepath.Join(root, "copy")
	if err := os.WriteFile(source, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := copyWithDigest(source, target, fmt.Sprintf("%x", sha256.Sum256(data))); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatal(info, err)
	}
}

func TestIsolationAndActualShimPreflight(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var home string
	err = withIsolation(context.Background(), repository(t), runCommand, func(env map[string]string) error {
		home = env["HOME"]
		if err := installShim(context.Background(), home, self, env, runCommand); err != nil {
			return err
		}
		program := filepath.Join(home, "shim/security")
		for _, key := range []string{"HOME", "XDG_DATA_HOME", "TMPDIR", "NAMING_LOG"} {
			bad := clone(env)
			delete(bad, key)
			if err := preflight(context.Background(), bad, program, runCommand); err == nil {
				t.Fatal("missing root", key)
			}
		}
		bad := clone(env)
		bad["XDG_DATA_HOME"] = "/outside"
		if preflight(context.Background(), bad, program, runCommand) == nil {
			t.Fatal("outside root accepted")
		}
		outside := t.TempDir()
		link := filepath.Join(home, "escaped")
		if err := os.Symlink(outside, link); err != nil {
			return err
		}
		bad["XDG_DATA_HOME"] = filepath.Join(link, "not-created")
		if preflight(context.Background(), bad, program, runCommand) == nil {
			t.Fatal("symlink root escaped")
		}
		bad = clone(env)
		bad["PATH"] = "/usr/bin:/bin"
		if preflight(context.Background(), bad, program, runCommand) == nil {
			t.Fatal("real security selected")
		}
		if err := os.Remove(program); err != nil {
			return err
		}
		if preflight(context.Background(), env, program, runCommand) == nil {
			t.Fatal("missing shim accepted")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(home); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("HOME not reclaimed", err)
	}
	if _, err := os.Stat(filepath.Dir(home)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("parent not reclaimed", err)
	}
}

func TestFailedPreambleOnlyCleansOwnedParent(t *testing.T) {
	outside := t.TempDir()
	sentinel := filepath.Join(outside, "keep")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"", "HOME=\x00", "HOME=/\x00", "HOME=relative\x00", "HOME=" + outside + "\x00", "command failure"} {
		owned := ""
		run := func(_ context.Context, c commandrun.Command) (commandrun.Result, error) {
			for _, e := range c.Env {
				if strings.HasPrefix(e, "TMPDIR=") {
					owned = strings.TrimPrefix(e, "TMPDIR=")
				}
			}
			if text == "command failure" {
				return commandrun.Result{}, errors.New("preamble failed")
			}
			return commandrun.Result{Stdout: text}, nil
		}
		err := withIsolation(context.Background(), repository(t), run, func(map[string]string) error { t.Fatal("unowned HOME yielded"); return nil })
		if err == nil {
			t.Fatal("invalid preamble accepted")
		}
		if _, err = os.Stat(owned); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("owned parent retained", err)
		}
		if data, err := os.ReadFile(sentinel); err != nil || string(data) != "keep" {
			t.Fatal("outside removed", err)
		}
	}
}

func TestSyntheticUpstreamControls(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	err = withIsolation(context.Background(), repository(t), runCommand, func(env map[string]string) error {
		root := env["HOME"]
		env["USER"] = "main"
		if err := installShim(context.Background(), root, self, env, runCommand); err != nil {
			return err
		}
		for _, c := range []struct {
			name, body string
			pass       bool
		}{{"match", "security find-generic-password -s Claude\\ Code-credentials -a main\nexit 1", true}, {"empty", "exit 1", false}, {"success", "exit 0", false}, {"delete", "security delete-generic-password\nexit 1", false}, {"fallback", "security find-generic-password -s Claude\\ Code-credentials -a main\ntouch \"$HOME/.credentials.json\"\nexit 1", false}} {
			upstream := filepath.Join(root, c.name)
			if err := os.WriteFile(upstream, []byte("#!/bin/sh\n"+c.body+"\n"), 0o700); err != nil {
				return err
			}
			err := verifyCase(context.Background(), root, root, self, upstream, env, runCommand)
			if (err == nil) != c.pass {
				t.Fatalf("%s: %v", c.name, err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestUpstreamLaunchAndTimeoutNotAuthFailure(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	err = withIsolation(context.Background(), repository(t), runCommand, func(env map[string]string) error {
		root := env["HOME"]
		env["USER"] = "main"
		if err := installShim(context.Background(), root, self, env, runCommand); err != nil {
			return err
		}
		for _, failure := range []error{context.DeadlineExceeded, context.Canceled, os.ErrNotExist} {
			calls := 0
			run := func(ctx context.Context, c commandrun.Command) (commandrun.Result, error) {
				calls++
				if c.Name == "upstream" {
					if c.Timeout != 45*time.Second {
						t.Fatal("upstream timeout changed")
					}
					return commandrun.Result{}, failure
				}
				return runCommand(ctx, c)
			}
			if err := verifyCase(context.Background(), root, root, self, "upstream", env, run); !errors.Is(err, failure) || calls != 3 {
				t.Fatal("execution failure masked", err, calls)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestVerifyRejectsInstalledUnknownBeforeAnyUpstreamRun(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "claude")
	sentinel := filepath.Join(root, "executed")
	if err := os.WriteFile(source, []byte("#!/bin/sh\ntouch "+sentinel+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	run := func(ctx context.Context, c commandrun.Command) (commandrun.Result, error) {
		calls++
		if c.Name != "/bin/sh" || len(c.Args) != 5 || !strings.Contains(c.Args[2], "env -0") {
			t.Fatal("upstream/shim launched before digest", c.Name)
		}
		return runCommand(ctx, c)
	}
	err = verify(context.Background(), repository(t), self, run, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "unreviewed Claude bytes") || calls != 1 {
		t.Fatal(err, calls)
	}
	if _, err := os.Stat(sentinel); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unknown Claude executed")
	}
}

func TestPreflightRequiresRecordedArgvAndExit44(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	err = withIsolation(context.Background(), repository(t), runCommand, func(env map[string]string) error {
		root := env["HOME"]
		if err := installShim(context.Background(), root, self, env, runCommand); err != nil {
			return err
		}
		for _, c := range []struct {
			log  string
			code int
		}{{"", 44}, {"[\"other\"]\n", 44}, {"[\"naming-preflight\"]\n", 0}} {
			run := func(_ context.Context, command commandrun.Command) (commandrun.Result, error) {
				if err := os.WriteFile(env["NAMING_LOG"], []byte(c.log), 0o600); err != nil {
					return commandrun.Result{}, err
				}
				return commandrun.Result{ExitCode: c.code}, nil
			}
			if err := preflight(context.Background(), env, filepath.Join(root, "shim/security"), run); err == nil {
				t.Fatal("bad preflight accepted")
			}
		}
		link := filepath.Join(root, "dangling")
		if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), link); err != nil {
			return err
		}
		bad := clone(env)
		bad["XDG_CONFIG_HOME"] = filepath.Join(link, "subdir")
		if err := preflight(context.Background(), bad, filepath.Join(root, "shim/security"), runCommand); err == nil {
			t.Fatal("dangling symlink escape accepted")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
