package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

type fakeRunner struct {
	stdout string
	stderr string
	code   int
	name   string
	args   []string
	stdin  string
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) (string, string, int) {
	f.name = name
	f.args = append([]string(nil), args...)
	return f.stdout, f.stderr, f.code
}

func (f *fakeRunner) RunInput(ctx context.Context, stdin, name string, args ...string) (string, string, int) {
	f.stdin = stdin
	return f.Run(ctx, name, args...)
}

func TestWithReplacesRunner(t *testing.T) {
	fake := &fakeRunner{stdout: "ok\n"}
	With(fake, func() {
		stdout, stderr, code := Run(context.Background(), "tool", "arg")
		if stdout != "ok\n" || stderr != "" || code != 0 {
			t.Fatalf("unexpected result: stdout=%q stderr=%q code=%d", stdout, stderr, code)
		}
		RunInput(context.Background(), "secret", "tool2", "a")
	})
	if fake.name != "tool2" || !reflect.DeepEqual(fake.args, []string{"a"}) {
		t.Fatalf("unexpected command: name=%q args=%v", fake.name, fake.args)
	}
	if fake.stdin != "secret" {
		t.Fatalf("stdin not passed: %q", fake.stdin)
	}
}

func TestOSRunnerRunInput(t *testing.T) {
	stdout, stderr, code := OSRunner{}.RunInput(context.Background(), "hello\n", "cat")
	if code != 0 || stdout != "hello\n" {
		t.Fatalf("unexpected: stdout=%q stderr=%q code=%d", stdout, stderr, code)
	}
}

func TestOSRunnerExitCode(t *testing.T) {
	_, _, code := OSRunner{}.Run(context.Background(), "false")
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
}

// Launch does not wait for what the program leaves running: a child holding a
// captured pipe would keep Run waiting for it, and here it holds only files.
func TestOSRunnerLaunchWaitsForTheProgramOnly(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	start := time.Now()
	// The child keeps the stdout Launch gave it; its stderr is redirected only so
	// the test binary's own stderr pipe is not held after the test ends.
	if code, err := (OSRunner{}).Launch(context.Background(), sh, "-c", "sleep 5 2>/dev/null & exit 0"); code != 0 || err != nil {
		t.Fatalf("code = %d, err = %v", code, err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("Launch waited %v for the program's background child", elapsed)
	}
	// Control: Run captures the output, so the same program keeps it waiting.
	start = time.Now()
	(OSRunner{}).Run(context.Background(), sh, "-c", "sleep 1 & exit 0")
	if time.Since(start) < 900*time.Millisecond {
		t.Fatalf("control: Run returned before the child exited, so the Launch check proves nothing")
	}
	if code, err := (OSRunner{}).Launch(context.Background(), sh, "-c", "exit 3"); code != 3 || err != nil {
		t.Fatalf("exit code = %d (%v), want 3", code, err)
	}
	// A program that cannot start is an error, not an exit code, and prints
	// nothing itself: the caller reports it once.
	if code, err := (OSRunner{}).Launch(context.Background(), "/nonexistent/kae-opener"); code == 0 || err == nil {
		t.Fatalf("an unstartable program = %d %v", code, err)
	}
}

// A runner without Launch still answers through Run.
func TestLaunchFallsBackToRun(t *testing.T) {
	fake := &fakeRunner{code: 4}
	var code int
	With(fake, func() { code, _ = Launch(context.Background(), "opener", "/p") })
	if code != 4 || fake.name != "opener" || !reflect.DeepEqual(fake.args, []string{"/p"}) {
		t.Fatalf("code %d, ran %s %v", code, fake.name, fake.args)
	}
}

// LaunchWithEnv sets its entries over the inherited environment, waits for the
// program only, and returns at ctx's deadline even when the program hangs.
func TestLaunchWithEnv(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	t.Setenv("KAE_LAUNCH_TEST", "inherited")
	ctx := context.Background()
	if code, err := LaunchWithEnv(ctx, []string{"KAE_LAUNCH_TEST=set"}, sh, "-c", `test "$KAE_LAUNCH_TEST" = set`); code != 0 || err != nil {
		t.Fatalf("the extra entry did not win over the inherited one: %d %v", code, err)
	}
	if code, err := LaunchWithEnv(ctx, nil, sh, "-c", "exit 3"); code != 3 || err != nil {
		t.Fatalf("exit code = %d (%v), want 3", code, err)
	}
	start := time.Now()
	if code, err := LaunchWithEnv(ctx, nil, sh, "-c", "sleep 5 & exit 0"); code != 0 || err != nil {
		t.Fatalf("code = %d, err = %v", code, err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("LaunchWithEnv waited %v for the program's background child", elapsed)
	}
	deadline, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	start = time.Now()
	if code, _ := LaunchWithEnv(deadline, nil, sh, "-c", "sleep 5 & exec sleep 5"); code == 0 {
		t.Fatal("a program killed at the deadline reported success")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("LaunchWithEnv returned %v after a 200ms deadline", elapsed)
	}
	if code, err := LaunchWithEnv(ctx, nil, "/nonexistent/kae-restart"); code == 0 || err == nil {
		t.Fatalf("an unstartable program = %d %v", code, err)
	}
}

// QueryWithEnv reads the program's stdout, sets its entries over the inherited
// environment, and waits for the program only — not for a child it leaves
// holding that stdout — under sh and dash alike, and returns at ctx's deadline.
func TestQueryWithEnv(t *testing.T) {
	for _, shell := range []string{"/bin/sh", "/bin/dash"} {
		t.Run(filepath.Base(shell), func(t *testing.T) {
			if _, err := os.Stat(shell); err != nil {
				t.Skipf("no %s", shell)
			}
			t.Setenv("KAE_QUERY_TEST", "inherited")
			ctx := context.Background()
			if out, code := QueryWithEnv(ctx, []string{"KAE_QUERY_TEST=set"}, shell, "-c", `printf '%s' "$KAE_QUERY_TEST"`); out != "set" || code != 0 {
				t.Fatalf("out = %q, code = %d; want the extra entry over the inherited one", out, code)
			}
			if out, code := QueryWithEnv(ctx, nil, shell, "-c", "echo partial; exit 3"); out != "partial\n" || code != 3 {
				t.Fatalf("out = %q, code = %d, want partial and 3", out, code)
			}

			// The background child keeps the stdout it inherited; the pid file lets
			// the test stop it instead of leaving it running.
			pidFile := filepath.Join(t.TempDir(), "pid")
			t.Cleanup(func() { killPidFile(pidFile) })
			start := time.Now()
			out, code := QueryWithEnv(ctx, nil, shell, "-c", `sleep 10 & echo $! > "$1"; echo '{"status":"running"}'`, "query", pidFile)
			if elapsed := time.Since(start); elapsed > 3*time.Second {
				t.Fatalf("QueryWithEnv waited %v for the program's background child", elapsed)
			}
			if out != "{\"status\":\"running\"}\n" || code != 0 {
				t.Fatalf("out = %q, code = %d", out, code)
			}
			if _, err := os.Stat(pidFile); err != nil {
				t.Fatalf("the background child was never started, so the check proves nothing: %v", err)
			}

			deadline, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
			defer cancel()
			start = time.Now()
			if _, code := QueryWithEnv(deadline, nil, shell, "-c", "exec sleep 5"); code == 0 {
				t.Fatal("a program killed at the deadline reported success")
			}
			if elapsed := time.Since(start); elapsed > 3*time.Second {
				t.Fatalf("QueryWithEnv returned %v after a 200ms deadline", elapsed)
			}
		})
	}
	if out, code := QueryWithEnv(context.Background(), nil, "/nonexistent/kae-status"); code == 0 || out != "" {
		t.Fatalf("an unstartable program = %q %d", out, code)
	}
}

// QueryWithEnv reads at most QueryMaxOutput bytes.
func TestQueryWithEnvCapsTheOutput(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	out, code := QueryWithEnv(context.Background(), nil, sh, "-c", "head -c 1100000 /dev/zero")
	if code != 0 || len(out) != QueryMaxOutput {
		t.Fatalf("read %d bytes (code %d), want %d", len(out), code, QueryMaxOutput)
	}
}

func killPidFile(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return
	}
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}
