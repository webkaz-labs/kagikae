package runner

import (
	"context"
	"os/exec"
	"reflect"
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
