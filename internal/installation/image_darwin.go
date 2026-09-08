package installation

import (
	"encoding/binary"
	"fmt"
	"os"
	"reflect"
	"syscall"
	"unsafe"
)

// runningImage identifies the vnode backing this function's executable mapping,
// rather than reopening os.Executable after another installer replaced it.
// Layout: apple-oss-distributions/xnu f6217f891ac0bb64f3d375211650a4c1ff8ca1ea,
// bsd/sys/proc_info.h: proc_regionwithpathinfo (region 96, vnode 152, path 1024).
func runningImage() (device, inode uint64, err error) {
	const procPIDInfo = 2
	const procPIDRegionPathInfo = 8
	var data [1272]byte
	pc := reflect.ValueOf(runningImage).Pointer()
	n, _, errno := syscall.Syscall6(syscall.SYS_PROC_INFO, procPIDInfo,
		uintptr(os.Getpid()), procPIDRegionPathInfo, pc,
		uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)))
	if errno != 0 {
		return 0, 0, fmt.Errorf("inspect running image: %w", errno)
	}
	start := binary.LittleEndian.Uint64(data[80:88])
	size := binary.LittleEndian.Uint64(data[88:96])
	if n != uintptr(len(data)) || uint64(pc) < start || uint64(pc)-start >= size || binary.LittleEndian.Uint32(data[:4])&4 == 0 {
		return 0, 0, fmt.Errorf("running image mapping is unavailable")
	}
	return uint64(binary.LittleEndian.Uint32(data[96:100])), binary.LittleEndian.Uint64(data[104:112]), nil
}
