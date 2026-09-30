// Package textui is the one place kae decides whether it may talk to a person
// at a terminal and opens the terminal it talks on.
//
// The controlling terminal (/dev/tty) is used rather than stdout: `kae cd`'s
// stdout is read through `$(…)`, so an interactive chooser must draw elsewhere.
package textui

import (
	"os"

	"golang.org/x/term"
)

// Terminal is the controlling terminal, opened for reading and writing. The
// zero value is a stand-in for tests: it has no file and Close does nothing.
type Terminal struct {
	TTY *os.File
}

// Close releases the terminal.
func (t *Terminal) Close() error {
	if t == nil || t.TTY == nil {
		return nil
	}
	return t.TTY.Close()
}

// Open returns the controlling terminal when kae may prompt: stdin is a terminal
// (an ioctl, not a character-device check, since /dev/null is a character device
// too), /dev/tty opens read-write, and TERM is not "dumb". Redirecting stdin
// from /dev/null therefore always says no.
func Open(stdin *os.File, termName string) (*Terminal, bool) {
	return open(stdinIsTerminal(stdin), termName, func() (*os.File, error) {
		return os.OpenFile("/dev/tty", os.O_RDWR, 0)
	})
}

// stdinIsTerminal asks the terminal driver (an ioctl), which is false for
// /dev/null and for pipes.
func stdinIsTerminal(stdin *os.File) bool {
	return stdin != nil && term.IsTerminal(int(stdin.Fd()))
}

// open is Open with its three observations injected.
func open(stdinIsTerminal bool, termName string, openTTY func() (*os.File, error)) (*Terminal, bool) {
	if !stdinIsTerminal || termName == "dumb" {
		return nil, false
	}
	tty, err := openTTY()
	if err != nil {
		return nil, false
	}
	return &Terminal{TTY: tty}, true
}
