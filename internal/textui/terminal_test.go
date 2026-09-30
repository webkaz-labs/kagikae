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
	for _, tc := range []struct {
		name  string
		stdin bool
		term  string
		open  func() (*os.File, error)
		want  bool
	}{
		{"everything present", true, "xterm-256color", fakeTTY, true},
		{"stdin is not a terminal", false, "xterm-256color", fakeTTY, false},
		{"TERM is dumb", true, "dumb", fakeTTY, false},
		{"/dev/tty does not open", true, "xterm-256color", noTTY, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := open(tc.stdin, tc.term, tc.open)
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
// ModeCharDevice test would let `kae cd </dev/null` open the picker.
func TestOpenRefusesDevNullAndNil(t *testing.T) {
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer devNull.Close()
	if _, ok := Open(devNull, "xterm"); ok {
		t.Fatal("/dev/null must not count as a terminal")
	}
	if _, ok := Open(nil, "xterm"); ok {
		t.Fatal("a nil stdin must not count as a terminal")
	}
	var zero *Terminal
	if err := zero.Close(); err != nil {
		t.Fatal(err)
	}
	if err := (&Terminal{}).Close(); err != nil {
		t.Fatal(err)
	}
}
