package runner

import (
	"context"
	"reflect"
	"testing"
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
