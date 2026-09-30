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
// too), /dev/tty opens read-write, TERM is not "dumb", and the terminal reports a
// size (a terminal of 0 rows or columns cannot show anything). Redirecting stdin
// from /dev/null therefore always says no.
func Open(stdin *os.File, termName string) (*Terminal, bool) {
	return open(stdinIsTerminal(stdin), termName, func() (*os.File, error) {
		return os.OpenFile("/dev/tty", os.O_RDWR, 0)
	}, ttySize)
}

// stdinIsTerminal asks the terminal driver (an ioctl), which is false for
// /dev/null and for pipes.
func stdinIsTerminal(stdin *os.File) bool {
	return stdin != nil && term.IsTerminal(int(stdin.Fd()))
}

// ttySize is the terminal's columns and rows.
func ttySize(tty *os.File) (cols, rows int, err error) {
	return term.GetSize(int(tty.Fd()))
}

// open is Open with its observations injected.
func open(stdinIsTerminal bool, termName string, openTTY func() (*os.File, error), size func(*os.File) (cols, rows int, err error)) (*Terminal, bool) {
	if !stdinIsTerminal || termName == "dumb" {
		return nil, false
	}
	tty, err := openTTY()
	if err != nil {
		return nil, false
	}
	if cols, rows, err := size(tty); err != nil || cols <= 0 || rows <= 0 {
		tty.Close()
		return nil, false
	}
	return &Terminal{TTY: tty}, true
}
