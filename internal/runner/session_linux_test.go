package runner

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// sessionOf is the session id of the process pid, the fourth field after the
// command name in /proc/<pid>/stat (state, ppid, pgrp, session); Go's syscall
// has no getsid on Linux.
func sessionOf(pid int) (int, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, err
	}
	s := string(data)
	fields := strings.Fields(s[strings.LastIndexByte(s, ')')+1:])
	if len(fields) < 4 {
		return 0, fmt.Errorf("short /proc/%d/stat", pid)
	}
	return strconv.Atoi(fields[3])
}
