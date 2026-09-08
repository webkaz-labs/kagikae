// Package installation owns direct executable replacement, installation receipts
// and the final removal of a receipt-backed running executable.
package installation

import (
	"errors"
	"os"
	"syscall"
)

// ErrUnsafe means ownership or image identity could not be established.
var ErrUnsafe = errors.New("installation ownership is unsafe")

// VerifyRunningImage compares a currently opened file with the kernel's running
// image identity. A digest of a pathname alone cannot detect self replacement.
func VerifyRunningImage(info os.FileInfo) error {
	dev, ino, err := runningImage()
	if err != nil {
		return err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || uint64(st.Dev) != dev || st.Ino != ino {
		return ErrUnsafe
	}
	return nil
}
