package runner

import "syscall"

// sessionOf is the session id of the process pid.
func sessionOf(pid int) (int, error) { return syscall.Getsid(pid) }
