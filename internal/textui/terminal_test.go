package textui

import (
	"errors"
	"os"
	"testing"
)

func TestOpenNeedsStdinTerminalTTYAndATerm(t *testing.T) {
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer devNull.Close()
	fakeTTY := func() (*os.File, error) { return os.Open(os.DevNull) }
	noTTY := func() (*os.File, error) { return nil, errors.New("no controlling terminal") }
	size := func(cols, rows int, err error) func(*os.File) (int, int, error) {
		return func(*os.File) (int, int, error) { return cols, rows, err }
	}
	for _, tc := range []struct {
		name  string
		stdin bool
		term  string
		open  func() (*os.File, error)
		size  func(*os.File) (int, int, error)
		want  bool
	}{
		{"everything present", true, "xterm-256color", fakeTTY, size(80, 24, nil), true},
		{"stdin is not a terminal", false, "xterm-256color", fakeTTY, size(80, 24, nil), false},
		{"TERM is dumb", true, "dumb", fakeTTY, size(80, 24, nil), false},
		{"/dev/tty does not open", true, "xterm-256color", noTTY, size(80, 24, nil), false},
		{"zero rows", true, "xterm-256color", fakeTTY, size(80, 0, nil), false},
		{"zero columns", true, "xterm-256color", fakeTTY, size(0, 24, nil), false},
		{"size unreadable", true, "xterm-256color", fakeTTY, size(0, 0, errors.New("no size")), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := open(tc.stdin, tc.term, tc.open, tc.size)
			if ok != tc.want || (got != nil) != tc.want {
				t.Fatalf("open = %v, %v; want ok %v", got, ok, tc.want)
			}
			if got != nil {
				if err := got.Close(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

// /dev/null is a character device, which is why the check is an ioctl: a
// ModeCharDevice test would let `kae cd </dev/null` open the picker. The stdin
// predicate is tested on its own, so a regression fails here even where no
// controlling terminal exists for Open to find.
func TestStdinPredicateRefusesDevNullAndNil(t *testing.T) {
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer devNull.Close()
	info, err := devNull.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		t.Fatalf("fixture: %s must be a character device for this test to mean anything (%v, %v)", os.DevNull, info, err)
	}
	if stdinIsTerminal(devNull) {
		t.Fatal("/dev/null must not count as a terminal")
	}
	if stdinIsTerminal(nil) {
		t.Fatal("a nil stdin must not count as a terminal")
	}
	pipe, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pipe.Close()
	defer w.Close()
	if stdinIsTerminal(pipe) {
		t.Fatal("a pipe must not count as a terminal")
	}
	if _, ok := Open(devNull, "xterm"); ok {
		t.Fatal("Open accepted /dev/null")
	}
	var zero *Terminal
	if err := zero.Close(); err != nil {
		t.Fatal(err)
	}
	if err := (&Terminal{}).Close(); err != nil {
		t.Fatal(err)
	}
}
