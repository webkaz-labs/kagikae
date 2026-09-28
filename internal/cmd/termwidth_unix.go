//go:build darwin || linux

package cmd

import (
	"os"
	"syscall"
	"unsafe"
)

// stdoutColumns asks the terminal behind stdout for its width. Zero means
// stdout is not a terminal, or the terminal did not answer.
func stdoutColumns() int {
	var ws struct{ Row, Col, Xpixel, Ypixel uint16 }
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, os.Stdout.Fd(),
		uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&ws)))
	if errno != 0 {
		return 0
	}
	return int(ws.Col)
}
