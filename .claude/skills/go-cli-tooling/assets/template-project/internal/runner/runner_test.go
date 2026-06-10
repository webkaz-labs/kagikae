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
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) (string, string, int) {
	f.name = name
	f.args = append([]string(nil), args...)
	return f.stdout, f.stderr, f.code
}

func TestWithReplacesRunner(t *testing.T) {
	fake := &fakeRunner{stdout: "ok\n"}
	With(fake, func() {
		stdout, stderr, code := Run(context.Background(), "tool", "arg")
		if stdout != "ok\n" || stderr != "" || code != 0 {
			t.Fatalf("unexpected result: stdout=%q stderr=%q code=%d", stdout, stderr, code)
		}
	})
	if fake.name != "tool" || !reflect.DeepEqual(fake.args, []string{"arg"}) {
		t.Fatalf("unexpected command: name=%q args=%v", fake.name, fake.args)
	}
}
