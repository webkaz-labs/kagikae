package installation

import (
	"bytes"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/lock"
	"github.com/webkaz-labs/kagikae/internal/patch"
)

// Receipt is local bookkeeping, not cryptographic provenance or deletion consent.
type Receipt struct {
	SchemaVersion int    `json:"schema_version"`
	Source        string `json:"source"`
	Destination   string `json:"destination"`
	Version       string `json:"version"`
	SHA256        string `json:"sha256"`
	OS            string `json:"os"`
	Arch          string `json:"arch"`
	Status        string `json:"status"`
	ParentDevice  uint64 `json:"parent_device"`
	ParentInode   uint64 `json:"parent_inode"`
}

// ReceiptPath binds the record filename to the exact canonical destination.
func ReceiptPath(root, destination string) string {
	sum := sha256.Sum256([]byte(destination))
	return filepath.Join(root, fmt.Sprintf("%x.json", sum))
}

// Acquire uses atomic mkdir, also available in the legacy POSIX-shell installer.
// A killed writer leaves the directory for explicit recovery; never steal it
// merely because a saved PID is absent or has been reused.
func Acquire(destination string) (release func() error, err error) {
	path := filepath.Join(filepath.Dir(destination), "."+filepath.Base(destination)+".kae-install-lock")
	if err := os.Mkdir(path, 0o700); err != nil {
		if os.IsExist(err) {
			return nil, lock.ErrBusy
		}
		return nil, err
	}
	return func() error { return os.Remove(path) }, nil
}

func ownedFile(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || st.Nlink != 1 || st.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0o022 != 0 {
		return nil, ErrUnsafe
	}
	return info, nil
}

func parentIdentity(destination string) (uint64, uint64, error) {
	if !filepath.IsAbs(destination) || filepath.Clean(destination) != destination {
		return 0, 0, ErrUnsafe
	}
	name := filepath.Base(destination)
	if name != "kae" && name != "kagikae" {
		return 0, 0, ErrUnsafe
	}
	dir := filepath.Dir(destination)
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return 0, 0, err
	}
	if resolved != dir {
		return 0, 0, ErrUnsafe
	}
	for _, component := range strings.Split(dir, string(filepath.Separator)) {
		switch component {
		case "mise", "installs", "shims", "Cellar", "Caskroom", "Homebrew", "homebrew", ".asdf":
			return 0, 0, ErrUnsafe
		}
	}
	info, err := os.Stat(dir)
	if err != nil {
		return 0, 0, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || st.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0o022 != 0 {
		return 0, 0, ErrUnsafe
	}
	return uint64(st.Dev), st.Ino, nil
}

func binaryDigest(path string) (string, os.FileInfo, error) {
	info, err := ownedFile(path)
	if err != nil {
		return "", nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return "", nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return "", nil, ErrUnsafe
	}
	bi, err := buildinfo.Read(f)
	if err != nil || bi.Main.Path != "github.com/webkaz-labs/kagikae" || bi.Path != "github.com/webkaz-labs/kagikae" {
		return "", nil, ErrUnsafe
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", nil, err
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", nil, err
	}
	return hex.EncodeToString(h.Sum(nil)), opened, nil
}

// Load validates the record's schema, permissions, path binding and platform.
// It never creates directories, locks or missing records.
func Load(root, destination string) (Receipt, error) {
	var r Receipt
	p := ReceiptPath(root, destination)
	info, err := ownedFile(p)
	if err != nil {
		return r, err
	}
	if info.Mode().Perm() != 0o600 || info.Size() > 16384 {
		return r, ErrUnsafe
	}
	dir, err := os.Lstat(root)
	if err != nil || !dir.IsDir() || dir.Mode().Perm() != 0o700 || dir.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return r, ErrUnsafe
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return r, err
	}
	if _, _, err := patch.GetPointer(data, "/schema_version"); err != nil {
		return r, ErrUnsafe
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&r); err != nil {
		return r, ErrUnsafe
	}
	var extra any
	if d.Decode(&extra) != io.EOF || r.SchemaVersion != 1 || r.Destination != destination || r.OS != runtime.GOOS || r.Arch != runtime.GOARCH || (r.Source != constants.InstallSourceRelease && r.Source != constants.InstallSourceLocal) {
		return r, ErrUnsafe
	}
	if digest, err := hex.DecodeString(r.SHA256); err != nil || len(digest) != sha256.Size {
		return r, ErrUnsafe
	}
	switch r.Status {
	case constants.InstallPending, constants.InstallActive, constants.InstallRemoving, constants.InstallRemoved:
	default:
		return r, ErrUnsafe
	}
	return r, nil
}

func save(root string, r Receipt) error {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return ErrUnsafe
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return patch.WriteFileAtomic(ReceiptPath(root, r.Destination), append(data, '\n'), 0o600)
}

// Install replaces a direct binary and records the matching receipt. A pending
// receipt survives interruption or finalization failure and disables removal.
// The caller creates and canonicalizes the destination directory before calling.
func Install(root, source, destination, version, kind string) (Receipt, error) {
	var r Receipt
	dev, ino, err := parentIdentity(destination)
	if err != nil {
		return r, err
	}
	if kind != constants.InstallSourceRelease && kind != constants.InstallSourceLocal {
		return r, ErrUnsafe
	}
	release, err := Acquire(destination)
	if err != nil {
		return r, err
	}
	defer release()
	previous, previousErr := Load(root, destination)
	if previousErr != nil && !os.IsNotExist(previousErr) {
		return r, previousErr
	}
	if previousErr == nil {
		if err := retainHistory(root, previous); err != nil {
			return r, err
		}
	}
	if _, err := ownedFile(destination); err != nil && !os.IsNotExist(err) {
		return r, err
	}
	digest, image, err := binaryDigest(source)
	if err != nil {
		return r, err
	}
	if err := VerifyRunningImage(image); err != nil {
		return r, err
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return r, err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != digest {
		return r, ErrUnsafe
	}
	r = Receipt{
		SchemaVersion: 1, Source: kind, Destination: destination, Version: version,
		SHA256: digest, OS: runtime.GOOS, Arch: runtime.GOARCH,
		Status: constants.InstallPending, ParentDevice: dev, ParentInode: ino,
	}
	return recordReplacement(root, r, data, save)
}

// recordReplacement keeps the transition testable with failed receipt storage.
// Install has validated source identity and holds the installation lock.
func recordReplacement(root string, r Receipt, data []byte, record func(string, Receipt) error) (Receipt, error) {
	if err := record(root, r); err != nil {
		return r, err
	}
	if gotDev, gotIno, err := parentIdentity(r.Destination); err != nil || r.ParentDevice != gotDev || r.ParentInode != gotIno {
		return r, ErrUnsafe
	}
	if err := patch.WriteFileAtomic(r.Destination, data, 0o755); err != nil {
		return r, err
	}
	r.Status = constants.InstallActive
	if err := record(root, r); err != nil {
		return r, fmt.Errorf("binary installed; receipt finalization failed; reinstall to repair: %w", err)
	}
	return r, nil
}

func retainHistory(root string, r Receipt) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	dir := historyDir(root, r.Destination)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return ErrUnsafe
	}
	digest := sha256.Sum256(data)
	return patch.WriteFileAtomic(filepath.Join(dir, fmt.Sprintf("%x.json", digest)), append(data, '\n'), 0o600)
}

func historyDir(root, destination string) string {
	key := strings.TrimSuffix(filepath.Base(ReceiptPath(root, destination)), ".json")
	return filepath.Join(root, "history", key)
}

// InspectRemoval is read-only. The selected destination must be the running image.
func InspectRemoval(root, destination string) (Receipt, error) {
	r, err := Load(root, destination)
	if err != nil {
		return r, err
	}
	if r.Status != constants.InstallActive && r.Status != constants.InstallRemoving {
		return r, ErrUnsafe
	}
	dev, ino, err := parentIdentity(destination)
	if err != nil || dev != r.ParentDevice || ino != r.ParentInode {
		return r, ErrUnsafe
	}
	digest, image, err := binaryDigest(destination)
	if err != nil {
		return r, err
	}
	if digest != r.SHA256 {
		return r, ErrUnsafe
	}
	return r, VerifyRunningImage(image)
}

// Remove rechecks the confirmed receipt under the shared installation lock and
// unlinks only the selected executable. Metadata failure is separate from unlink.
func Remove(root string, confirmed Receipt) (removed bool, err error) {
	release, err := Acquire(confirmed.Destination)
	if err != nil {
		return false, err
	}
	defer release()
	current, err := InspectRemoval(root, confirmed.Destination)
	if err != nil {
		return false, err
	}
	if current != confirmed {
		return false, ErrUnsafe
	}
	current.Status = constants.InstallRemoving
	if err := save(root, current); err != nil {
		return false, err
	}
	if _, err := InspectRemoval(root, current.Destination); err != nil {
		return false, err
	}
	if err := os.Remove(current.Destination); err != nil {
		return false, err
	}
	current.Status = constants.InstallRemoved
	if err := save(root, current); err != nil {
		return true, errors.Join(fmt.Errorf("binary removed; receipt history could not be finalized"), err)
	}
	return true, nil
}
