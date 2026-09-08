package installation

import (
	"os"
	"syscall"
)

func runningImage() (device, inode uint64, err error) {
	info, err := os.Stat("/proc/self/exe")
	if err != nil {
		return 0, 0, err
	}
	st := info.Sys().(*syscall.Stat_t)
	return uint64(st.Dev), st.Ino, nil
}
